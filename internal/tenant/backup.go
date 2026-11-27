package tenant

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/backup"
	"github.com/jonathanmkosar/keystone/internal/secrets"
)

// Backup and restore of one tenant (P13-04). The file's format is
// internal/backup's; this file decides what goes in and how a tenant comes
// back out. See docs/architecture/backup.md.
//
// A backup holds a manifest and the tenant's folder under "tenant/": the
// database, copied whole by SQLite, the secrets file with its data key
// wrapped by the backup's own key, and every other file and folder. Like
// the rest of this package, it reads no setting: the command passes the
// values in.

const (
	manifestName   = "manifest.json"
	tenantPrefix   = "tenant/"
	backupFormat   = "keystone tenant backup"
	backupVersion  = 1
	maxManifest    = 1 << 20
	maxSecretsFile = 16 << 20
)

// takenApart are the files at the top of a tenant's folder that a backup
// does not copy as they are: the database and its journals, which SQLite
// copies whole, and the secrets file, whose data key is wrapped again. A
// backup that holds one of the others is refused.
var takenApart = map[string]bool{
	"keystone.db": true, "keystone.db-wal": true, "keystone.db-shm": true, "keystone.db-journal": true,
	secretsFile: true, secretsFile + ".tmp": true,
}

// BrowserProfileFolder is the folder at the top of a tenant's folder that
// holds the agent's browser profile while its browser runs (P10-03, the
// guard's ProfileFolder). It is made new at each start and removed at
// close, and its files are held open while the browser runs, so a backup
// leaves it out.
const BrowserProfileFolder = "browser-profile"

// ErrTenantExists is returned by Restore when the tenant in the backup is
// already on the install and the restore was not told to replace it.
var ErrTenantExists = errors.New("a restore does not replace a tenant unless it is told to")

// BackupOptions are the values someone might change. The command reads
// them from the settings registry (backup.*) and passes them in.
type BackupOptions struct {
	// Rounds is how many rounds of PBKDF2-SHA-256 the passphrase goes
	// through, from backup.MinRounds to backup.MaxRounds.
	Rounds int
	// MinPassphrase is the fewest characters a passphrase may have.
	MinPassphrase int
}

// BackupInfo says what a backup holds. It names no secret.
type BackupInfo struct {
	Tenant Tenant
	MadeAt time.Time
	// Files and Bytes count the files from the tenant's folder, the
	// database and the secrets file among them, and their size before
	// compression.
	Files int
	Bytes int64
	// Secrets is how many secrets the tenant had stored.
	Secrets int
}

// manifest is the first entry of every backup.
type manifest struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	MadeAt     time.Time `json:"made_at"`
	Tenant     Tenant    `json:"tenant"`
	Migrations []string  `json:"migrations"`
	Secrets    bool      `json:"secrets"`
}

// Backup writes one tenant to w, sealed under passphrase. It can run while
// the server runs: the database is copied by SQLite as one consistent
// snapshot. If it fails, what was written to w is not a backup, and the
// caller removes it.
func (in *Install) Backup(id string, w io.Writer, passphrase string, o BackupOptions) (BackupInfo, error) {
	if o.MinPassphrase < 1 {
		return BackupInfo{}, errors.New("the shortest passphrase a backup takes must be 1 character or more")
	}
	if !utf8.ValidString(passphrase) || utf8.RuneCountInString(passphrase) < o.MinPassphrase {
		return BackupInfo{}, fmt.Errorf("a backup's passphrase is at least %d characters", o.MinPassphrase)
	}
	s, err := in.Store(id)
	if err != nil {
		return BackupInfo{}, err
	}
	defer s.Close()
	info := BackupInfo{MadeAt: time.Now().UTC().Truncate(time.Second)}
	for _, t := range in.Tenants() {
		if t.ID == id {
			info.Tenant = t
		}
	}
	// The admin mark is the install's, not the person's: a backup carries
	// none, and a restore keeps the mark the install has (decision 0641).
	info.Tenant.Admin = false

	// The database, as one snapshot, in a folder of its own inside the
	// data folder, where the database already is.
	work, err := os.MkdirTemp(in.dir, ".backup-")
	if err != nil {
		return BackupInfo{}, err
	}
	defer os.RemoveAll(work)
	snapshot := filepath.Join(work, "keystone.db")
	if _, err := s.DB().Exec("VACUUM INTO ?", snapshot); err != nil {
		return BackupInfo{}, fmt.Errorf("tenant %s: the database could not be copied: %w", id, err)
	}
	applied, err := s.Migrations()
	if err != nil {
		return BackupInfo{}, err
	}

	bw, err := backup.Create(w, passphrase, o.Rounds)
	if err != nil {
		return BackupInfo{}, err
	}
	sealed, count, hasSecrets, err := s.Secrets().Rewrapped(bw.Keeper())
	if err != nil {
		return BackupInfo{}, err
	}
	info.Secrets = count
	m := manifest{Format: backupFormat, Version: backupVersion, MadeAt: info.MadeAt,
		Tenant: info.Tenant, Migrations: applied, Secrets: hasSecrets}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return BackupInfo{}, err
	}
	if err := bw.File(manifestName, int64(len(raw)), info.MadeAt, bytes.NewReader(raw)); err != nil {
		return BackupInfo{}, err
	}
	add := func(name string, size int64, mod time.Time, r io.Reader) error {
		info.Files++
		info.Bytes += size
		return bw.File(tenantPrefix+name, size, mod, r)
	}
	if err := addFile(snapshot, func(size int64, mod time.Time, r io.Reader) error {
		return add("keystone.db", size, mod, r)
	}); err != nil {
		return BackupInfo{}, err
	}
	if hasSecrets {
		if err := add(secretsFile, int64(len(sealed)), info.MadeAt, bytes.NewReader(sealed)); err != nil {
			return BackupInfo{}, err
		}
	}

	// Everything else in the tenant's folder, as it is.
	root, err := os.OpenRoot(filepath.Join(in.dir, "tenants", id))
	if err != nil {
		return BackupInfo{}, err
	}
	defer root.Close()
	fsys := root.FS()
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." || (!strings.Contains(p, "/") && takenApart[p]) {
			return nil
		}
		if p == BrowserProfileFolder && d.IsDir() { // P10-03
			return fs.SkipDir
		}
		switch {
		case d.IsDir():
			fi, err := d.Info()
			if err != nil {
				return err
			}
			return bw.Dir(tenantPrefix+p, fi.ModTime())
		case d.Type().IsRegular():
			f, err := fsys.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			fi, err := f.Stat()
			if err != nil {
				return err
			}
			return add(p, fi.Size(), fi.ModTime(), f)
		}
		return fmt.Errorf("tenant %s: %s is neither a file nor a folder, and a backup takes only those", id, p)
	})
	if err != nil {
		return BackupInfo{}, err
	}
	return info, bw.Close()
}

// addFile opens the file at path and hands it to add with its size.
func addFile(path string, add func(size int64, mod time.Time, r io.Reader) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	return add(fi.Size(), fi.ModTime(), f)
}

// CheckBackup reads a whole backup and checks it, and writes nothing: every
// chunk, the manifest, every name, and that the secrets open with the
// passphrase. It says what the backup holds.
func CheckBackup(r io.Reader, passphrase string) (BackupInfo, error) {
	c, err := checkBackup(r, passphrase, nil)
	return c.info, err
}

// checked is a backup that was read whole and passed every check.
type checked struct {
	info    BackupInfo
	entries []backup.Entry
	// secrets is the secrets file as the backup holds it, with its data
	// key wrapped by the backup's own key.
	secrets []byte
	// reader is the first read, kept for its key and its keeper.
	reader *backup.Reader
	// sum is the SHA-256 of the whole file, so a second read can tell
	// that the file did not change in between.
	sum []byte
}

func damaged(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{backup.ErrDamaged}, args...)...)
}

// checkBackup reads the whole backup in r. early, when set, is called with
// the tenant as soon as the manifest is read, so a restore that would be
// refused is refused before the rest is read.
func checkBackup(r io.Reader, passphrase string, early func(Tenant) error) (checked, error) {
	h := sha256.New()
	br, err := backup.Open(io.TeeReader(r, h), passphrase)
	if err != nil {
		return checked{}, err
	}
	c := checked{reader: br}
	var m manifest
	haveManifest, haveDB := false, false
	for {
		e, err := br.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return checked{}, err
		}
		c.entries = append(c.entries, e)
		if !haveManifest {
			if e.Name != manifestName || e.Dir || e.Size > maxManifest {
				return checked{}, damaged("it does not start with its manifest")
			}
			raw, err := io.ReadAll(br)
			if err != nil {
				return checked{}, err
			}
			if m, err = readManifest(raw); err != nil {
				return checked{}, err
			}
			haveManifest = true
			if early != nil {
				if err := early(m.Tenant); err != nil {
					return checked{}, err
				}
			}
			continue
		}
		rel, ok := strings.CutPrefix(e.Name, tenantPrefix)
		if !ok {
			return checked{}, damaged("%q is outside the tenant's folder", e.Name)
		}
		top := !strings.Contains(rel, "/")
		switch {
		case top && rel == "keystone.db":
			if e.Dir {
				return checked{}, damaged("its database is a folder")
			}
			haveDB = true
		case top && rel == secretsFile:
			if e.Dir || e.Size > maxSecretsFile {
				return checked{}, damaged("its secrets file is not a file of a size Keystone writes")
			}
			if c.secrets, err = io.ReadAll(br); err != nil {
				return checked{}, err
			}
		case top && takenApart[rel]:
			return checked{}, damaged("it holds %s, which a backup never takes", rel)
		}
		if !e.Dir {
			c.info.Files++
			c.info.Bytes += e.Size
		}
	}
	switch {
	case !haveManifest:
		return checked{}, damaged("it holds nothing")
	case !haveDB:
		return checked{}, damaged("it holds no database")
	case m.Secrets != (c.secrets != nil):
		return checked{}, damaged("its manifest and its contents disagree about the secrets")
	}
	if c.secrets != nil {
		// The backup's own keeper on both sides: the file is opened and
		// nothing changes.
		_, n, err := secrets.Rewrap(c.secrets, m.Tenant.ID, br.Keeper(), br.Keeper())
		if err != nil {
			return checked{}, damaged("its secrets do not open: %v", err)
		}
		c.info.Secrets = n
	}
	c.info.Tenant, c.info.MadeAt = m.Tenant, m.MadeAt
	c.sum = h.Sum(nil)
	return c, nil
}

// readManifest checks a backup's manifest, and that this binary knows
// every migration its database has had.
func readManifest(raw []byte) (manifest, error) {
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return manifest{}, damaged("its manifest is not JSON")
	}
	if m.Format != backupFormat {
		return manifest{}, damaged("its manifest is not a Keystone tenant backup's")
	}
	if m.Version > backupVersion {
		return manifest{}, errors.New("this backup was made by a newer Keystone: use a newer binary to restore it")
	}
	name := strings.TrimSpace(m.Tenant.Name)
	switch {
	case m.Version != backupVersion:
		return manifest{}, damaged("its manifest has no version this binary knows")
	case !idShape.MatchString(m.Tenant.ID):
		return manifest{}, damaged("its manifest names no tenant id")
	case name == "" || name != m.Tenant.Name || len(name) > maxNameLength:
		return manifest{}, damaged("its manifest's tenant has no name Keystone gives")
	case m.Tenant.Created.IsZero() || m.MadeAt.IsZero():
		return manifest{}, damaged("its manifest has no dates")
	}
	all, err := migrations()
	if err != nil {
		return manifest{}, err
	}
	known := map[string]bool{}
	for _, mg := range all {
		known[mg.name] = true
	}
	for _, name := range m.Migrations {
		if !known[name] {
			return manifest{}, fmt.Errorf("this backup's database has migration %s, which this binary does not know: use a newer binary to restore it", name)
		}
	}
	return m, nil
}

// RestoreOptions say how a restore may proceed.
type RestoreOptions struct {
	// Replace lets the backup take the place of a tenant with the same id.
	Replace bool
	// Ready, when set, is called with the restored tenant's store before
	// the tenant is put in place. An error stops the restore, and nothing
	// is left behind. The command signs every old session out here.
	Ready func(*Store) error
}

// Restored says what a restore did.
type Restored struct {
	BackupInfo
	// Replaced says the backup took the place of a tenant with its id.
	Replaced bool
	// Leftover names the replaced tenant's old folder when it could not
	// be removed afterwards. The restore itself is complete.
	Leftover string
}

// Restore brings the tenant in a backup into this install, under this
// install's own keeper. It is all or nothing:
//
//  1. The whole file is read and checked, and nothing is written.
//  2. The tenant's data key is wrapped by this install's keeper.
//  3. The file is read again into a new folder beside the tenants', and
//     must be the same file, byte for byte.
//  4. The database there is opened, brought up to date and checked, the
//     secrets must open, and Ready runs.
//  5. Only then is the folder renamed into place and the tenant added to
//     the registry.
//
// A tenant that is already on the install is refused unless Replace is
// set. The server keeps the registry in memory, so it should be stopped
// while a restore runs.
func (in *Install) Restore(r io.ReadSeeker, passphrase string, o RestoreOptions) (Restored, error) {
	c, err := checkBackup(r, passphrase, func(t Tenant) error { return in.mayRestore(t, o.Replace, true) })
	if err != nil {
		return Restored{}, err
	}
	t := c.info.Tenant
	var sealed []byte
	if c.secrets != nil {
		if sealed, _, err = secrets.Rewrap(c.secrets, t.ID, c.reader.Keeper(), installKeeper{in}); err != nil {
			return Restored{}, fmt.Errorf("tenant %s: its data key could not be wrapped by this install's keeper: %w", t.ID, err)
		}
	}

	stage, err := os.MkdirTemp(filepath.Join(in.dir, "tenants"), "."+t.ID+".restore-")
	if err != nil {
		return Restored{}, err
	}
	placed := false
	defer func() {
		if !placed {
			os.RemoveAll(stage) // ours alone: MkdirTemp above made it
		}
	}()
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return Restored{}, err
	}
	if err := extract(r, c, stage, sealed); err != nil {
		return Restored{}, err
	}
	if err := in.prepare(stage, c, o.Ready); err != nil {
		return Restored{}, err
	}
	res, err := in.place(stage, t, o.Replace)
	if err != nil {
		return Restored{}, err
	}
	placed = true
	res.BackupInfo = c.info
	return res, nil
}

// mayRestore refuses a tenant that is already on the install, by its entry
// in the registry or by its folder, unless replace is set.
func (in *Install) mayRestore(t Tenant, replace, lock bool) error {
	if lock {
		in.mu.Lock()
		defer in.mu.Unlock()
	}
	if replace {
		return nil
	}
	for _, have := range in.tenants {
		if have.ID == t.ID {
			return fmt.Errorf("tenant %s is already on this install, as %q, and the backup is of %q: %w", t.ID, have.Name, t.Name, ErrTenantExists)
		}
	}
	if _, err := os.Lstat(filepath.Join(in.dir, "tenants", t.ID)); err == nil {
		return fmt.Errorf("a folder for tenant %s is already in the data folder, though the registry does not list it: %w", t.ID, ErrTenantExists)
	}
	return nil
}

// extract reads the backup a second time into stage. Every entry must be
// the one the first read saw, and the file must hash the same.
func extract(r io.Reader, c checked, stage string, sealed []byte) error {
	h := sha256.New()
	br, err := c.reader.Reopen(io.TeeReader(r, h))
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer root.Close()
	changed := damaged("the file changed while it was being restored")
	i := 0
	for {
		e, err := br.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if i >= len(c.entries) || e.Name != c.entries[i].Name || e.Dir != c.entries[i].Dir || e.Size != c.entries[i].Size {
			return changed
		}
		i++
		rel, ok := strings.CutPrefix(e.Name, tenantPrefix)
		if !ok {
			continue // the manifest
		}
		name := filepath.FromSlash(rel)
		if e.Dir {
			if err := root.MkdirAll(name, 0o700); err != nil {
				return err
			}
			continue
		}
		if dir := filepath.Dir(name); dir != "." {
			if err := root.MkdirAll(dir, 0o700); err != nil {
				return err
			}
		}
		var src io.Reader = br
		if rel == secretsFile {
			src = bytes.NewReader(sealed)
		}
		if err := writeNew(root, name, src); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
	}
	if i != len(c.entries) || !bytes.Equal(h.Sum(nil), c.sum) {
		return changed
	}
	for _, sub := range []string{"files", "artifacts"} {
		if err := root.MkdirAll(sub, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// writeNew writes a file that must not be there yet, and syncs it.
func writeNew(root *os.Root, name string, src io.Reader) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, src)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// prepare opens the tenant in stage as a store: its database brought up to
// date, checked by SQLite and checked to be this tenant's; the restore
// noted in tenant_meta; its secrets opened under this install's keeper;
// and Ready run. The store is closed before the folder is moved.
func (in *Install) prepare(stage string, c checked, ready func(*Store) error) error {
	t := c.info.Tenant
	s, err := openStore(stage, t.ID, secrets.Open(filepath.Join(stage, secretsFile), t.ID, installKeeper{in}))
	if err != nil {
		return fmt.Errorf("tenant %s: the restored database does not open: %w", t.ID, err)
	}
	err = func() error {
		var result string
		if err := s.DB().QueryRow("PRAGMA quick_check").Scan(&result); err != nil {
			return err
		}
		if result != "ok" {
			return damaged("its database fails SQLite's check: %s", result)
		}
		if _, err := s.DB().Exec(`INSERT INTO tenant_meta(key, value) VALUES('restored_at', ?), ('restored_from_backup_made_at', ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			time.Now().UTC().Format(time.RFC3339), c.info.MadeAt.UTC().Format(time.RFC3339)); err != nil {
			return err
		}
		if c.secrets != nil {
			names, err := s.Secrets().Names()
			if err != nil {
				return fmt.Errorf("its secrets do not open under this install's keeper: %w", err)
			}
			if len(names) != c.info.Secrets {
				return fmt.Errorf("tenant %s: %d secrets were restored, and the backup holds %d", t.ID, len(names), c.info.Secrets)
			}
		}
		if ready != nil {
			return ready(s)
		}
		return nil
	}()
	if cerr := s.Close(); err == nil {
		err = cerr
	}
	return err
}

// place renames the restored folder into place and adds the tenant to the
// registry. A replaced tenant's folder is moved aside first, and put back
// if anything after that fails.
func (in *Install) place(stage string, t Tenant, replace bool) (Restored, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if err := in.mayRestore(t, replace, false); err != nil {
		return Restored{}, err
	}
	folder := filepath.Join(in.dir, "tenants", t.ID)
	var res Restored
	aside := ""
	if _, err := os.Lstat(folder); err == nil {
		aside = stage + "-replaced"
		if err := os.Rename(folder, aside); err != nil {
			return Restored{}, fmt.Errorf("tenant %s: its folder could not be moved aside, which happens when the server has it open. Stop the server and restore again: %w", t.ID, err)
		}
		res.Replaced = true
	}
	putBack := func() error {
		if aside == "" {
			return nil
		}
		return os.Rename(aside, folder)
	}
	if err := os.Rename(stage, folder); err != nil {
		if perr := putBack(); perr != nil {
			return Restored{}, fmt.Errorf("tenant %s: the restored folder could not be put in place (%v), and the old one is still at %s: %w", t.ID, err, aside, perr)
		}
		return Restored{}, fmt.Errorf("tenant %s: the restored folder could not be put in place: %w", t.ID, err)
	}
	before := slices.Clone(in.tenants)
	// save takes every admin mark from the file on disk, so the mark a
	// backup says it had is never the one written (decision 0641).
	t.Admin = false
	if i := slices.IndexFunc(in.tenants, func(have Tenant) bool { return have.ID == t.ID }); i >= 0 {
		in.tenants[i] = t
		res.Replaced = true
	} else {
		in.tenants = append(in.tenants, t)
	}
	if err := in.save(); err != nil {
		in.tenants = before
		if rerr := os.Rename(folder, stage); rerr != nil {
			return Restored{}, fmt.Errorf("tenant %s: the registry could not be saved (%v), and the restored folder at %s could not be taken out again: %w", t.ID, err, folder, rerr)
		}
		if perr := putBack(); perr != nil {
			return Restored{}, fmt.Errorf("tenant %s: the registry could not be saved (%v), and the old folder is still at %s: %w", t.ID, err, aside, perr)
		}
		return Restored{}, fmt.Errorf("tenant %s: the registry could not be saved, so nothing was restored: %w", t.ID, err)
	}
	if aside != "" {
		if err := os.RemoveAll(aside); err != nil {
			res.Leftover = aside
		}
	}
	return res, nil
}
