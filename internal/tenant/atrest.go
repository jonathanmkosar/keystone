package tenant

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/atrest"
	"github.com/jonathanmkosar/keystone/internal/secrets"
)

// Encryption at rest (P15-06). A sealed tenant's folder holds seal.json,
// its database sealed page by page through internal/atrest's VFS, its
// secrets file as it always was, and its files sealed and named by keyed
// hashes. A folder without seal.json is plain and opens as it always did.
// See docs/architecture/encryption.md and decisions 0660 to 0664.
//
// Like the rest of this package, nothing here reads a setting: the server
// passes encryption.at_rest and encryption.lock_wait in through
// EncryptAtRest, and the backup.* settings through SealOptions.

const (
	sealFile    = "seal.json"
	sealFormat  = "keystone sealed folder"
	sealVersion = 1
	// sealedDSN follows openDB, with a rollback journal in place of the
	// write-ahead log, which the VFS cannot hold (decision 0660), and
	// every sync SQLite asks for, which a rollback journal needs to
	// survive a loss of power.
	sealedDSN = "&_txlock=immediate&_pragma=journal_mode(truncate)&_pragma=synchronous(full)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
)

// AtRest says how the install keeps people's data on disk.
type AtRest struct {
	// On makes every new tenant sealed, and lets SealAll seal the plain
	// ones. A sealed tenant opens whatever it says.
	On bool
	// LockWait is how long opening a sealed database waits for another
	// process that has it open. Zero keeps the package's default.
	LockWait time.Duration
}

// EncryptAtRest tells the install how to keep people's data on disk. The
// caller passes the encryption.* settings.
func (in *Install) EncryptAtRest(o AtRest) {
	in.mu.Lock()
	in.atRest = o
	in.mu.Unlock()
	if o.LockWait > 0 {
		atrest.SetLockWait(o.LockWait)
	}
}

func (in *Install) sealing() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.atRest.On
}

// sealMark is seal.json. It holds no key: the check is a keyed hash that
// tells whether the keys at hand are this folder's.
type sealMark struct {
	Format  string    `json:"format"`
	Version int       `json:"version"`
	Tenant  string    `json:"tenant"`
	Check   string    `json:"check"`
	Sealed  time.Time `json:"sealed"`
}

// readSeal reads a folder's seal.json. ok is false when there is none.
func readSeal(folder string) (m sealMark, ok bool, err error) {
	raw, err := os.ReadFile(filepath.Join(folder, sealFile))
	if errors.Is(err, fs.ErrNotExist) {
		return sealMark{}, false, nil
	}
	if err != nil {
		return sealMark{}, false, err
	}
	if err := json.Unmarshal(raw, &m); err != nil || m.Format != sealFormat {
		return sealMark{}, false, fmt.Errorf("%s in %s is not one Keystone wrote", sealFile, folder)
	}
	if m.Version != sealVersion {
		return sealMark{}, false, fmt.Errorf("%s in %s was written by another version of Keystone", sealFile, folder)
	}
	return m, true, nil
}

func (m sealMark) matches(id string, k *atrest.Keys) error {
	if m.Tenant != id || k.Tenant() != id {
		return fmt.Errorf("tenant %s: its folder was sealed for tenant %s, and is not opened", id, m.Tenant)
	}
	if m.Check != k.Check() {
		return fmt.Errorf("tenant %s: its folder was sealed under another key: %w", id, atrest.ErrSealed)
	}
	return nil
}

// writeSeal writes seal.json last of all, synced, under another name first.
func writeSeal(folder string, m sealMark) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(folder, sealFile+".tmp")
	if err := writeSynced(tmp, append(raw, '\n')); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(folder, sealFile)); err != nil {
		return err
	}
	syncDir(folder)
	return nil
}

// Sealed reports whether a tenant's folder is sealed.
func (in *Install) Sealed(id string) (bool, error) {
	if !idShape.MatchString(id) {
		return false, fmt.Errorf("%q is not a tenant id", id)
	}
	_, ok, err := readSeal(in.folder(id))
	return ok, err
}

func (in *Install) folder(id string) string { return filepath.Join(in.dir, "tenants", id) }

// keysFor returns a tenant's at-rest keys, from the subkey its vault gives,
// and keeps them. A tenant with no data key gets one. The caller does not
// hold in.mu: the vault may ask the keeper, which takes it.
func (in *Install) keysFor(id string) (*atrest.Keys, error) {
	in.keyMu.Lock()
	k := in.keys[id]
	in.keyMu.Unlock()
	if k != nil {
		return k, nil
	}
	in.mu.Lock()
	v := in.vault(id)
	in.mu.Unlock()
	root, err := v.Subkey(atrest.Purpose)
	if err != nil {
		return nil, fmt.Errorf("tenant %s: its keys for data at rest: %w", id, err)
	}
	if k, err = atrest.Derive(root, id); err != nil {
		return nil, err
	}
	in.keyMu.Lock()
	if in.keys == nil {
		in.keys = map[string]*atrest.Keys{}
	}
	in.keys[id] = k
	in.keyMu.Unlock()
	return k, nil
}

// forgetKeys drops the kept keys of a tenant whose data key changed, as a
// restore that replaces it does.
func (in *Install) forgetKeys(id string) {
	in.keyMu.Lock()
	delete(in.keys, id)
	in.keyMu.Unlock()
}

// unlock gives the VFS the keys of a sealed tenant's folder, after checking
// them against its seal.json. A plain tenant needs nothing. The caller does
// not hold in.mu.
func (in *Install) unlock(id string) error {
	if !idShape.MatchString(id) {
		return nil // open refuses it
	}
	folder := in.folder(id)
	m, ok, err := readSeal(folder)
	if err != nil || !ok {
		return err
	}
	if _, err := os.Stat(filepath.Join(folder, secretsFile)); err != nil {
		return fmt.Errorf("tenant %s: its folder is sealed and its secrets file, which holds its data key, cannot be read: %w", id, err)
	}
	k, err := in.keysFor(id)
	if err != nil {
		return err
	}
	if err := m.matches(id, k); err != nil {
		return err
	}
	return atrest.Use(folder, k)
}

// unlockStage gives the VFS the keys of a folder that is not in place yet,
// a restore's, from its own secrets file. It returns what takes them back.
func (in *Install) unlockStage(stage, id string) (func(), error) {
	m, ok, err := readSeal(stage)
	if err != nil || !ok {
		return func() {}, err
	}
	v := secrets.Open(filepath.Join(stage, secretsFile), id, installKeeper{in})
	root, err := v.Subkey(atrest.Purpose)
	if err != nil {
		return nil, fmt.Errorf("tenant %s: its keys for data at rest: %w", id, err)
	}
	k, err := atrest.Derive(root, id)
	if err != nil {
		return nil, err
	}
	if err := m.matches(id, k); err != nil {
		return nil, err
	}
	if err := atrest.Use(stage, k); err != nil {
		return nil, err
	}
	return func() { atrest.Forget(stage) }, nil
}

// openFolderDB opens the database of the tenant whose folder is folder:
// through the VFS when the folder is sealed, as openDB does when it is
// plain. A folder that is neither whole, a sealed database with no
// seal.json or a plain one with it, is refused and left as it is.
func openFolderDB(folder, id string) (*sql.DB, *atrest.Keys, error) {
	path := filepath.Join(folder, atrest.DatabaseFile)
	m, sealed, err := readSeal(folder)
	if err != nil {
		return nil, nil, fmt.Errorf("tenant %s: %w", id, err)
	}
	head := make([]byte, 16)
	if f, err := os.Open(path); err == nil {
		n, _ := io.ReadFull(f, head)
		head = head[:n]
		f.Close()
	} else {
		head = nil
	}
	halfDone := fmt.Errorf("tenant %s: its folder is half sealed (%s and the database disagree), and is not opened. Start the server again to finish or undo the sealing, or restore the person from a backup", id, sealFile)
	if !sealed {
		if atrest.IsPageFile(head) {
			return nil, nil, halfDone
		}
		db, err := openDB(path)
		return db, nil, err
	}
	if len(head) > 0 && !atrest.IsPageFile(head) {
		return nil, nil, halfDone
	}
	k := atrest.KeysFor(folder)
	if k == nil {
		return nil, nil, fmt.Errorf("tenant %s: its folder is sealed, and its keys were not given", id)
	}
	if err := m.matches(id, k); err != nil {
		return nil, nil, err
	}
	db, err := openSealedDB(path)
	if err != nil {
		return nil, nil, fmt.Errorf("tenant %s: %w", id, err)
	}
	return db, k, nil
}

// openSealedDB opens a sealed database whose folder's keys the VFS has.
func openSealedDB(path string) (*sql.DB, error) {
	dsn, err := atrest.DSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn+sealedDSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("the sealed database did not open. If another Keystone process, the server or a command, has this person open, stop it or wait for it: %w", err)
	}
	return db, nil
}

// Create adds a tenant (see createPlain). With encryption at rest on, the
// new tenant's folder is sealed before Create returns. If that fails the
// tenant is there, plain, and the next SealAll seals it.
func (in *Install) Create(name string) (Tenant, error) {
	t, err := in.createPlain(name)
	if err != nil || !in.sealing() {
		return t, err
	}
	if err := in.seal(t.ID); err != nil {
		return t, fmt.Errorf("tenant %s was added, and its folder could not be sealed, so it stays plain until the server's next start seals it: %w", t.ID, err)
	}
	return t, nil
}

// SealOptions are the values someone might change. The server reads them
// from the settings registry and passes them in.
type SealOptions struct {
	// Backup is how the backup made before each tenant is sealed is
	// written: the backup.* settings.
	Backup BackupOptions
	// BackupFolder is where those backups go. Empty is backups/
	// before-encryption in the data folder.
	BackupFolder string
	// Progress, when set, is told each step in a line for the server's log.
	// No line holds anything of a person's but their id.
	Progress func(string)
}

// SealReport says what SealAll did.
type SealReport struct {
	// Sealed are the tenants sealed by this call, in order.
	Sealed []string
	// Already counts the tenants that were sealed before it.
	Already int
	// Backups are the backups written before sealing, one per tenant in
	// Sealed. Each has its passphrase beside it, wrapped by the install
	// master key: BackupPassphrase reads it.
	Backups []string
}

// SealAll seals every plain tenant, one at a time, each after a backup of
// it (decision 0663). It stops at the first tenant that fails: that tenant
// and those after it stay plain and as they were, and the error says which.
// Every tenant it sealed is sealed for good.
func (in *Install) SealAll(o SealOptions) (SealReport, error) {
	say := func(format string, args ...any) {
		if o.Progress != nil {
			o.Progress(fmt.Sprintf(format, args...))
		}
	}
	var r SealReport
	for _, t := range in.Tenants() {
		if err := in.recover(t.ID); err != nil {
			return r, err
		}
		sealed, err := in.Sealed(t.ID)
		if err != nil {
			return r, err
		}
		if sealed {
			r.Already++
			continue
		}
		say("encryption at rest: tenant %s: writing a backup before sealing", t.ID)
		file, err := in.backupBeforeSealing(t.ID, o)
		if err != nil {
			return r, fmt.Errorf("tenant %s: the backup before sealing failed, so nothing was sealed: %w", t.ID, err)
		}
		say("encryption at rest: tenant %s: backup written to %s; sealing", t.ID, file)
		if err := in.seal(t.ID); err != nil {
			return r, fmt.Errorf("tenant %s: not sealed, and its folder was left plain: %w", t.ID, err)
		}
		r.Sealed = append(r.Sealed, t.ID)
		r.Backups = append(r.Backups, file)
		say("encryption at rest: tenant %s: sealed", t.ID)
	}
	return r, nil
}

// The two folders beside a tenant's own while it is sealed.
func (in *Install) stageOf(id string) string {
	return filepath.Join(in.dir, "tenants", "."+id+".sealing")
}

func (in *Install) asideOf(id string) string {
	return filepath.Join(in.dir, "tenants", "."+id+".plain")
}

// recover finishes or undoes a sealing that a stop cut short, from what it
// finds (decision 0663). The folder in place is only ever the whole plain
// one or the whole sealed one; the sealed copy is made whole and read back
// in its own folder, with seal.json written last, before the two trade
// places. The plain copy is removed only once the sealed folder in place
// has opened. The caller does not hold in.mu.
func (in *Install) recover(id string) error {
	folder, stage, aside := in.folder(id), in.stageOf(id), in.asideOf(id)
	there := func(p string) bool { _, err := os.Lstat(p); return err == nil }
	_, stageDone, err := readSeal(stage)
	if err != nil {
		stageDone = false
	}
	if !there(folder) {
		switch {
		case stageDone:
			// Cut short between the two renames: the sealed copy was
			// whole and read back, so it goes in place.
			if err := os.Rename(stage, folder); err != nil {
				return fmt.Errorf("tenant %s: its sealed folder could not be put in place: %w", id, err)
			}
		case there(aside):
			// The plain folder was moved aside and the sealed copy is not
			// whole: the plain one goes back.
			if err := os.RemoveAll(stage); err != nil {
				return err
			}
			if err := os.Rename(aside, folder); err != nil {
				return fmt.Errorf("tenant %s: its plain folder could not be put back from %s: %w", id, aside, err)
			}
			return nil
		default:
			return fmt.Errorf("tenant %s has no folder", id)
		}
	}
	_, sealed, err := readSeal(folder)
	if err != nil {
		return err
	}
	if !sealed {
		if there(aside) {
			return fmt.Errorf("tenant %s: a plain copy of its folder is at %s beside its plain folder, which Keystone never leaves; move one of them out of the tenants folder by hand", id, aside)
		}
		return os.RemoveAll(stage) // a sealing cut short; it starts again
	}
	if there(stage) {
		if err := os.RemoveAll(stage); err != nil {
			return err
		}
	}
	if there(aside) {
		// Sealed and in place: once it opens, the plain copy goes.
		if err := in.unlock(id); err != nil {
			return err
		}
		if err := in.readBack(id); err != nil {
			return fmt.Errorf("tenant %s: its sealed folder does not open, and its plain copy is kept at %s: %w", id, aside, err)
		}
		if err := os.RemoveAll(aside); err != nil {
			return fmt.Errorf("tenant %s: sealed, and its plain copy at %s could not be removed: %w", id, aside, err)
		}
	}
	return nil
}

// readBack opens the tenant in place as a store and has SQLite check it.
func (in *Install) readBack(id string) error {
	in.mu.Lock()
	s, err := in.open(id)
	in.mu.Unlock()
	if err != nil {
		return err
	}
	defer s.Close()
	var result string
	if err := s.DB().QueryRow("PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("SQLite's check says: %s", result)
	}
	return nil
}

// seal seals one plain tenant in place (decision 0663):
//
//  1. Its keys, which make its data key if it has none.
//  2. A sealed copy of the database, made by SQLite from the plain one,
//     and of every file, in a folder of its own beside the tenant's.
//  3. Read back: the copy opened through the VFS and checked by SQLite,
//     every table's rows counted and hashed on both sides, every file
//     opened and its hash compared, everything synced; seal.json last.
//  4. The plain folder moved aside and the sealed one into place, and
//     opened there.
//  5. Only then the plain copy removed.
//
// A failure before step 4 leaves the plain folder as it was and removes the
// copy. A failure in step 4 puts the plain folder back.
func (in *Install) seal(id string) error {
	if err := in.recover(id); err != nil {
		return err
	}
	if sealed, err := in.Sealed(id); err != nil || sealed {
		return err
	}
	folder, stage, aside := in.folder(id), in.stageOf(id), in.asideOf(id)
	k, err := in.keysFor(id)
	if err != nil {
		return err
	}
	if err := os.Mkdir(stage, 0o700); err != nil {
		return err
	}
	placed := false
	defer func() {
		atrest.Forget(stage)
		if !placed {
			os.RemoveAll(stage) // ours alone: Mkdir above made it
		}
	}()
	if err := atrest.Use(stage, k); err != nil {
		return err
	}
	if err := in.sealCopy(id, folder, stage, k); err != nil {
		return err
	}
	sealStep("copied")
	if err := writeSeal(stage, sealMark{Format: sealFormat, Version: sealVersion, Tenant: id, Check: k.Check(),
		Sealed: time.Now().UTC().Truncate(time.Second)}); err != nil {
		return err
	}
	atrest.Forget(stage)
	sealStep("marked")

	// Trade places.
	if err := os.Rename(folder, aside); err != nil {
		return fmt.Errorf("its plain folder could not be moved aside, which happens when another program has a file in it open: %w", err)
	}
	sealStep("aside")
	if err := os.Rename(stage, folder); err != nil {
		if perr := os.Rename(aside, folder); perr != nil {
			return fmt.Errorf("the sealed folder could not be put in place (%v), and the plain one is still at %s: %w", err, aside, perr)
		}
		return fmt.Errorf("the sealed folder could not be put in place: %w", err)
	}
	placed = true
	sealStep("placed")
	err = in.unlock(id)
	if err == nil {
		err = in.readBack(id)
	}
	if err != nil {
		// Undo: the sealed folder back to the stage, the plain one back.
		if rerr := os.Rename(folder, stage); rerr != nil {
			return fmt.Errorf("the sealed folder does not open in place (%v), and could not be taken out again, so the plain one is still at %s: %w", err, aside, rerr)
		}
		placed = false
		if perr := os.Rename(aside, folder); perr != nil {
			return fmt.Errorf("the sealed folder does not open in place (%v), and the plain one could not be put back from %s: %w", err, aside, perr)
		}
		return fmt.Errorf("the sealed folder does not open in place, so the plain one was put back: %w", err)
	}
	if err := os.RemoveAll(aside); err != nil {
		return fmt.Errorf("sealed, and the plain copy at %s could not be removed; the next start removes it: %w", aside, err)
	}
	return nil
}

// sealHook, in tests, is told each step of seal as it is reached, so a test
// can take the data folder as a stop at that moment would leave it.
var sealHook func(step string)

func sealStep(step string) {
	if sealHook != nil {
		sealHook(step)
	}
}

// sealCopy makes the sealed copy of a plain tenant's folder in stage and
// reads it back.
func (in *Install) sealCopy(id, folder, stage string, k *atrest.Keys) error {
	in.mu.Lock()
	plain, err := in.open(id)
	in.mu.Unlock()
	if err != nil {
		return err
	}
	defer plain.Close()
	if plain.Sealed() {
		return fmt.Errorf("tenant %s is sealed already", id)
	}

	// The database, copied by SQLite through the VFS, then opened and
	// checked against the plain one.
	stageDB := filepath.Join(stage, atrest.DatabaseFile)
	target, err := atrest.DSN(stageDB)
	if err != nil {
		return err
	}
	if _, err := plain.DB().Exec("VACUUM INTO ?", target); err != nil {
		return fmt.Errorf("the database could not be copied: %w", err)
	}
	want, err := tableSums(plain.DB())
	if err != nil {
		return err
	}
	copied, err := openSealedDB(stageDB)
	if err != nil {
		return err
	}
	err = func() error {
		var result string
		if err := copied.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
			return err
		}
		if result != "ok" {
			return fmt.Errorf("the sealed copy of the database fails SQLite's check: %s", result)
		}
		got, err := tableSums(copied)
		if err != nil {
			return err
		}
		return sameSums(want, got)
	}()
	if cerr := copied.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if f, err := os.OpenFile(stageDB, os.O_RDWR, 0); err == nil {
		err = f.Sync()
		f.Close()
		if err != nil {
			return err
		}
	}

	// Every other file.
	return sealTree(folder, stage, k)
}

// tableSums counts and hashes the rows of every table in a database, its
// rowid with each row where it has one, in the order the table keeps them.
func tableSums(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query("SELECT name FROM sqlite_schema WHERE type = 'table' AND sql NOT LIKE 'CREATE VIRTUAL%' ORDER BY name")
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, name)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	sums := map[string]string{}
	for _, table := range tables {
		quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		r, err := db.Query("SELECT rowid, * FROM " + quoted)
		if err != nil {
			r, err = db.Query("SELECT * FROM " + quoted) // WITHOUT ROWID
		}
		if err != nil {
			return nil, err
		}
		cols, err := r.Columns()
		if err != nil {
			r.Close()
			return nil, err
		}
		h := sha256.New()
		n := 0
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for r.Next() {
			if err := r.Scan(ptrs...); err != nil {
				r.Close()
				return nil, err
			}
			for _, v := range vals {
				fmt.Fprintf(h, "%T:%v\x00", v, v)
			}
			h.Write([]byte{'\n'})
			n++
		}
		if err := r.Close(); err != nil {
			return nil, err
		}
		sums[table] = fmt.Sprintf("%d rows, %x", n, h.Sum(nil))
	}
	return sums, nil
}

func sameSums(want, got map[string]string) error {
	var names []string
	for name := range want {
		names = append(names, name)
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if want[name] != got[name] {
			return fmt.Errorf("the sealed copy of the database differs from the plain one in table %s", name)
		}
	}
	return nil
}

// sealTree seals every file of a plain tenant's folder into stage, each
// name a keyed hash and each file read back and compared by hash. The
// database and its journals are copied by SQLite, the secrets file is
// copied as it is (it is sealed with the data key already), and the
// agent's browser profile, made new at each start, is left out. Anything
// else at the top of the folder is refused, so nothing is left behind
// unsealed or dropped without a word.
func sealTree(folder, stage string, k *atrest.Keys) error {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return err
	}
	for _, sub := range []string{"files", "artifacts"} {
		if err := os.MkdirAll(filepath.Join(stage, sub), 0o700); err != nil {
			return err
		}
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case name == atrest.DatabaseFile, strings.HasPrefix(name, atrest.DatabaseFile+"-"),
			name == secretsFile+".tmp", name == sealFile, name == sealFile+".tmp":
		case name == BrowserProfileFolder && e.IsDir():
		case name == secretsFile && e.Type().IsRegular():
			raw, err := os.ReadFile(filepath.Join(folder, name))
			if err != nil {
				return err
			}
			if err := writeSynced(filepath.Join(stage, name), raw); err != nil {
				return err
			}
			back, err := os.ReadFile(filepath.Join(stage, name))
			if err != nil || !bytes.Equal(back, raw) {
				return fmt.Errorf("the copy of %s does not read back the same", name)
			}
		case (name == "files" || name == "artifacts") && e.IsDir():
			if err := sealFolder(filepath.Join(folder, name), filepath.Join(stage, name), k); err != nil {
				return err
			}
		default:
			return fmt.Errorf("the folder holds %s, which Keystone does not keep there and does not know how to seal: move it out of the folder and start again", name)
		}
	}
	syncDir(stage)
	return nil
}

// sealFolder seals the files of one folder, and the folders in it, into
// dst under keyed names.
func sealFolder(src, dst string, k *atrest.Keys) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		from, to := filepath.Join(src, e.Name()), filepath.Join(dst, k.Name(e.Name()))
		switch {
		case e.IsDir():
			if err := os.Mkdir(to, 0o700); err != nil {
				return err
			}
			if err := sealFolder(from, to, k); err != nil {
				return err
			}
		case e.Type().IsRegular():
			plain, err := os.ReadFile(from)
			if err != nil {
				return err
			}
			sealed, err := k.SealFile(plain)
			if err != nil {
				return err
			}
			if err := writeSynced(to, sealed); err != nil {
				return err
			}
			back, err := os.ReadFile(to)
			if err != nil {
				return err
			}
			opened, err := k.OpenFile(back)
			if err != nil {
				return fmt.Errorf("a sealed file does not read back: %w", err)
			}
			if sha256.Sum256(opened) != sha256.Sum256(plain) {
				return errors.New("a sealed file reads back different from the plain one")
			}
		default:
			return fmt.Errorf("%s is neither a file nor a folder, and only those are sealed", from)
		}
	}
	syncDir(dst)
	return nil
}

// writeSynced writes a new file and syncs it.
func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// syncDir makes the names in a folder durable where the system can. Windows
// cannot open a folder to sync it; NTFS keeps its own journal of names.
func syncDir(dir string) {
	if f, err := os.Open(dir); err == nil {
		f.Sync()
		f.Close()
	}
}

// The backup before sealing (decision 0663) is a backup in the P13-04
// format, sealed under a random passphrase. The passphrase is kept beside
// it, wrapped by the install master key and bound to the tenant id, in a
// file of the same name and ".key": only this install, on this machine,
// can read it, with BackupPassphrase.

const (
	backupKeyMagic = "KSBK1"
	keyFileSuffix  = ".key"
	backupSuffix   = ".keystone-backup"
)

func passphraseContext(id string) string {
	return "keystone backup passphrase v1 for tenant " + id
}

func (in *Install) backupBeforeSealing(id string, o SealOptions) (string, error) {
	dir := o.BackupFolder
	if dir == "" {
		dir = filepath.Join(in.dir, "backups", "before-encryption")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	pass := hex.EncodeToString(raw)
	wrapped, err := installKeeper{in}.Wrap([]byte(pass), passphraseContext(id))
	if err != nil {
		return "", fmt.Errorf("the backup's passphrase could not be wrapped by the install master key: %w", err)
	}
	var tag [3]byte // two backups of one tenant in one second still differ
	if _, err := rand.Read(tag[:]); err != nil {
		return "", err
	}
	file := filepath.Join(dir, id+"-"+time.Now().UTC().Format("20060102T150405Z")+"-"+hex.EncodeToString(tag[:])+backupSuffix)
	part := file + ".part"
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	done := false
	defer func() {
		if !done {
			os.Remove(part)
			os.Remove(file + keyFileSuffix)
		}
	}()
	_, err = in.Backup(id, f, pass, o.Backup)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	// Read back whole with the passphrase before anything is sealed.
	r, err := os.Open(part)
	if err != nil {
		return "", err
	}
	_, err = CheckBackup(r, pass)
	r.Close()
	if err != nil {
		return "", fmt.Errorf("the backup does not read back: %w", err)
	}
	if err := writeSynced(file+keyFileSuffix, append([]byte(backupKeyMagic), wrapped...)); err != nil {
		return "", err
	}
	if err := os.Rename(part, file); err != nil {
		return "", err
	}
	syncDir(dir)
	done = true
	return file, nil
}

// BackupPassphrase returns the passphrase of a backup written before
// sealing, from the file beside it, with the install master key. The
// backup's name starts with the tenant id it was made for.
func (in *Install) BackupPassphrase(backupFile string) (string, error) {
	base := filepath.Base(backupFile)
	if !strings.HasSuffix(base, backupSuffix) || len(base) < 16 || !idShape.MatchString(base[:16]) {
		return "", fmt.Errorf("%s is not the name of a backup written before sealing", base)
	}
	raw, err := os.ReadFile(backupFile + keyFileSuffix)
	if err != nil {
		return "", err
	}
	if !bytes.HasPrefix(raw, []byte(backupKeyMagic)) {
		return "", fmt.Errorf("%s%s is not a passphrase file Keystone wrote", base, keyFileSuffix)
	}
	pass, err := installKeeper{in}.Unwrap(raw[len(backupKeyMagic):], passphraseContext(base[:16]))
	if err != nil {
		return "", fmt.Errorf("the passphrase does not open with this install's master key: %w", err)
	}
	return string(pass), nil
}
