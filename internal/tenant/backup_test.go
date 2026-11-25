package tenant

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/backup"
	"github.com/jonathanmkosar/keystone/internal/secrets"
)

// Every name, value and passphrase here is invented.
const backupPassphrase = "an invented passphrase for a test"

var testBackupOptions = BackupOptions{Rounds: backup.MinRounds, MinPassphrase: 12}

// countingKeeper counts the keys it is asked to wrap.
type countingKeeper struct {
	secrets.Keeper
	wraps *atomic.Int32
}

func (k countingKeeper) Wrap(key []byte, context string) ([]byte, error) {
	k.wraps.Add(1)
	return k.Keeper.Wrap(key, context)
}

func newCountingKeeper() (countingKeeper, *atomic.Int32) {
	n := &atomic.Int32{}
	return countingKeeper{secrets.NewMemoryKeeper(), n}, n
}

// fullTenant makes an install with one tenant that has a row, files in
// both its folders and a stored secret.
func fullTenant(t *testing.T) (in *Install, id string) {
	t.Helper()
	in, err := OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DB().Exec("INSERT INTO tenant_meta(key, value) VALUES('note', 'an invented note')"); err != nil {
		t.Fatal(err)
	}
	if err := s.Files().MkdirAll("deep/er", 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"upload.txt": "an invented upload", "deep/er/inside.txt": "an invented file deeper in"} {
		if err := s.Files().WriteFile(name, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Artifacts().WriteFile("v1.html", []byte("<p>an invented artifact</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Secrets().Set(secretName, secretValue); err != nil {
		t.Fatal(err)
	}
	return in, made.ID
}

func backupOf(t *testing.T, in *Install, id string) ([]byte, BackupInfo) {
	t.Helper()
	var buf bytes.Buffer
	info, err := in.Backup(id, &buf, backupPassphrase, testBackupOptions)
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), info
}

// listing is every path under dir with its size, and keystone.json whole,
// so a test can tell that nothing was written.
func listing(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		size := info.Size()
		if d.IsDir() {
			size = 0
		}
		lines = append(lines, fmt.Sprintf("%s %d", filepath.ToSlash(rel), size))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	raw, _ := os.ReadFile(filepath.Join(dir, registryFile))
	return strings.Join(lines, "\n") + "\n" + string(raw)
}

func TestBackupAndRestoreOnAnotherInstall(t *testing.T) {
	src, id := fullTenant(t)
	file, made := backupOf(t, src, id)
	if bytes.Contains(file, []byte(secretValue)) || bytes.Contains(file, []byte("an invented note")) {
		t.Error("the backup holds the tenant's data in the clear")
	}
	if made.Secrets != 1 || made.Files != 5 || made.Tenant.ID != id {
		t.Errorf("the backup says %+v", made)
	}
	checkedInfo, err := CheckBackup(bytes.NewReader(file), backupPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if checkedInfo != made {
		t.Errorf("the check says %+v, and the backup said %+v", checkedInfo, made)
	}

	// Another install, with a master key of its own.
	dir := t.TempDir()
	keeper, wraps := newCountingKeeper()
	dst, err := OpenWithKeeper(dir, keeper)
	if err != nil {
		t.Fatal(err)
	}
	var readied int
	got, err := dst.Restore(bytes.NewReader(file), backupPassphrase, RestoreOptions{Ready: func(s *Store) error {
		readied++
		var note string
		return s.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'note'").Scan(&note)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if readied != 1 || got.Replaced || got.Leftover != "" || got.BackupInfo != made {
		t.Errorf("the restore says %+v, ready ran %d times", got, readied)
	}
	if n := wraps.Load(); n != 1 {
		t.Errorf("the new keeper wrapped %d keys, want the one data key", n)
	}
	if listed := dst.Tenants(); len(listed) != 1 || listed[0] != made.Tenant {
		t.Errorf("the registry lists %+v, want %+v", listed, made.Tenant)
	}

	// The same install opened again, as the server would be.
	again, err := OpenWithKeeper(dir, keeper)
	if err != nil {
		t.Fatal(err)
	}
	s, err := again.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var note, restoredAt string
	if err := s.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'note'").Scan(&note); err != nil || note != "an invented note" {
		t.Errorf("the row came back as %q, %v", note, err)
	}
	if err := s.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'restored_at'").Scan(&restoredAt); err != nil || restoredAt == "" {
		t.Errorf("the restore is not noted in tenant_meta: %v", err)
	}
	for name, want := range map[string]string{"upload.txt": "an invented upload", "deep/er/inside.txt": "an invented file deeper in"} {
		if raw, err := s.Files().ReadFile(name); err != nil || string(raw) != want {
			t.Errorf("files/%s came back as %q, %v", name, raw, err)
		}
	}
	if raw, err := s.Artifacts().ReadFile("v1.html"); err != nil || !strings.Contains(string(raw), "artifact") {
		t.Errorf("the artifact came back as %q, %v", raw, err)
	}
	if value, ok, err := s.Secrets().Get(secretName); err != nil || !ok || value != secretValue {
		t.Errorf("the secret did not come back under the new keeper: %v, %v", ok, err)
	}
	if !strings.Contains(registryText(t, dir), `"keeper": "memory"`) {
		t.Error("the new install did not record its keeper")
	}
	// No staging folder is left beside the tenant.
	entries, err := os.ReadDir(filepath.Join(dir, "tenants"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != id {
		t.Errorf("tenants/ holds %v", entries)
	}
	// And the first install's keeper cannot open the restored secrets: the
	// data key is wrapped by the new one only.
	raw, err := os.ReadFile(filepath.Join(dir, "tenants", id, secretsFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := secrets.Rewrap(raw, id, secrets.NewMemoryKeeper(), secrets.NewMemoryKeeper()); err == nil {
		t.Error("another keeper opened the restored secrets")
	}
}

func TestARestoreLeavesOtherTenantsAlone(t *testing.T) {
	src, id := fullTenant(t)
	file, _ := backupOf(t, src, id)

	dir, dst, other, _ := twoTenantsWithKeeper(t)
	if _, err := other.DB().Exec("INSERT INTO tenant_meta(key, value) VALUES('note', 'the other person''s note')"); err != nil {
		t.Fatal(err)
	}
	if err := other.Files().WriteFile("upload.txt", []byte("the other person's upload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Restore(bytes.NewReader(file), backupPassphrase, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := len(dst.Tenants()); n != 3 {
		t.Errorf("the registry lists %d tenants, want 3", n)
	}
	var note string
	if err := other.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'note'").Scan(&note); err != nil || note != "the other person's note" {
		t.Errorf("the other tenant's row is %q, %v", note, err)
	}
	if raw, err := other.Files().ReadFile("upload.txt"); err != nil || string(raw) != "the other person's upload" {
		t.Errorf("the other tenant's file is %q, %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tenants", id)); err != nil {
		t.Error(err)
	}
}

func TestARestoreRefusesATenantThatIsThere(t *testing.T) {
	in, id := fullTenant(t)
	file, _ := backupOf(t, in, id)
	before := listing(t, in.dir)
	_, err := in.Restore(bytes.NewReader(file), backupPassphrase, RestoreOptions{})
	if !errors.Is(err, ErrTenantExists) {
		t.Fatalf("a restore over the tenant gave %v, want ErrTenantExists", err)
	}
	if after := listing(t, in.dir); after != before {
		t.Errorf("a refused restore changed the data folder:\n%s\nwas\n%s", after, before)
	}

	// A folder with the id and no entry in the registry is refused too.
	dir := t.TempDir()
	dst, err := OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "tenants", id), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Restore(bytes.NewReader(file), backupPassphrase, RestoreOptions{}); !errors.Is(err, ErrTenantExists) {
		t.Errorf("a restore over a stray folder gave %v, want ErrTenantExists", err)
	}
}

func TestARestoreReplacesWhenToldTo(t *testing.T) {
	in, id := fullTenant(t)
	file, _ := backupOf(t, in, id)

	// Changes after the backup, which the restore takes back.
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec("UPDATE tenant_meta SET value = 'changed after the backup' WHERE key = 'note'"); err != nil {
		t.Fatal(err)
	}
	if err := s.Files().WriteFile("later.txt", []byte("written after the backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Secrets().Set("provider.later", "another invented value"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	got, err := in.Restore(bytes.NewReader(file), backupPassphrase, RestoreOptions{Replace: true})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Replaced || got.Leftover != "" {
		t.Errorf("the restore says %+v", got)
	}
	if n := len(in.Tenants()); n != 1 {
		t.Errorf("the registry lists %d tenants after a replace", n)
	}
	s, err = in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var note string
	if err := s.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'note'").Scan(&note); err != nil || note != "an invented note" {
		t.Errorf("after the replace the row is %q, %v", note, err)
	}
	if _, err := s.Files().Stat("later.txt"); err == nil {
		t.Error("a file written after the backup is still there")
	}
	if names, err := s.Secrets().Names(); err != nil || len(names) != 1 || names[0] != secretName {
		t.Errorf("after the replace the secrets are %v, %v", names, err)
	}
	entries, err := os.ReadDir(filepath.Join(in.dir, "tenants"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("tenants/ holds %d entries after a replace, want only the tenant", len(entries))
	}
}

func TestARefusedRestoreWritesNothing(t *testing.T) {
	src, id := fullTenant(t)
	file, _ := backupOf(t, src, id)
	cut := file[:len(file)-10]
	changed := append([]byte(nil), file...)
	changed[len(changed)-5] ^= 1

	cases := map[string]struct {
		file       []byte
		passphrase string
		want       error
	}{
		"a wrong passphrase":        {file, "another invented passphrase", backup.ErrPassphrase},
		"a byte changed at the end": {changed, backupPassphrase, backup.ErrDamaged},
		"a file cut short":          {cut, backupPassphrase, backup.ErrDamaged},
	}
	for what, c := range cases {
		dir := t.TempDir()
		keeper, wraps := newCountingKeeper()
		dst, err := OpenWithKeeper(dir, keeper)
		if err != nil {
			t.Fatal(err)
		}
		before := listing(t, dir)
		_, err = dst.Restore(bytes.NewReader(c.file), c.passphrase, RestoreOptions{})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: gave %v, want %v", what, err, c.want)
		}
		if after := listing(t, dir); after != before {
			t.Errorf("%s: the data folder changed:\n%s", what, after)
		}
		// The check comes before anything else: the keeper was never
		// asked to wrap the data key.
		if n := wraps.Load(); n != 0 {
			t.Errorf("%s: the keeper wrapped %d keys before the file was checked", what, n)
		}
		if len(dst.Tenants()) != 0 {
			t.Errorf("%s: a tenant was added", what)
		}
	}
}

func TestAFailureAfterTheCheckLeavesNothing(t *testing.T) {
	src, id := fullTenant(t)
	file, _ := backupOf(t, src, id)
	dir := t.TempDir()
	dst, err := OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	_, err = dst.Restore(bytes.NewReader(file), backupPassphrase, RestoreOptions{Ready: func(*Store) error {
		return errors.New("an invented refusal")
	}})
	if err == nil || !strings.Contains(err.Error(), "an invented refusal") {
		t.Fatalf("a failing Ready gave %v", err)
	}
	if len(dst.Tenants()) != 0 {
		t.Error("a tenant was added")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "tenants"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a failed restore left %v in tenants/", entries)
	}
	// Opened again, the install has no tenant and opens.
	if again, err := OpenWithKeeper(dir, secrets.NewMemoryKeeper()); err != nil || len(again.Tenants()) != 0 {
		t.Errorf("the install after a failed restore: %v", err)
	}

	// A failed replace puts the old tenant back as it was.
	before := listing(t, src.dir)
	_, err = src.Restore(bytes.NewReader(file), backupPassphrase, RestoreOptions{Replace: true, Ready: func(*Store) error {
		return errors.New("an invented refusal")
	}})
	if err == nil {
		t.Fatal("a failing Ready on a replace gave no error")
	}
	if after := listing(t, src.dir); after != before {
		t.Errorf("a failed replace changed the data folder:\n%s\nwas\n%s", after, before)
	}
}

// crafted writes a backup by hand, as someone who knew the passphrase
// could, with the manifest m and the files given, in order.
func crafted(t *testing.T, m manifest, files map[string][]byte, order []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	bw, err := backup.Create(&buf, backupPassphrase, backup.MinRounds)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := bw.File(manifestName, int64(len(raw)), now, bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	for _, name := range order {
		if err := bw.File(name, int64(len(files[name])), now, bytes.NewReader(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := bw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestARestoreRefusesABackupThatIsNotRight(t *testing.T) {
	src, id := fullTenant(t)
	db, err := os.ReadFile(filepath.Join(src.dir, "tenants", id, "keystone.db"))
	if err != nil {
		t.Fatal(err)
	}
	good := func() manifest {
		var applied []string
		all, err := migrations()
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range all {
			applied = append(applied, m.name)
		}
		return manifest{Format: backupFormat, Version: backupVersion, MadeAt: time.Now().UTC(),
			Tenant: src.Tenants()[0], Migrations: applied}
	}
	withDB := map[string][]byte{"tenant/keystone.db": db}
	dbOnly := []string{"tenant/keystone.db"}

	future := good()
	future.Migrations = append(future.Migrations, "20991231T000000_from_a_newer_keystone.sql")
	newer := good()
	newer.Version = backupVersion + 1
	otherID := good()
	otherID.Tenant.ID = "0123456789abcdef"
	noName := good()
	noName.Tenant.Name = " "
	saysSecrets := good()
	saysSecrets.Secrets = true

	cases := map[string]struct {
		file []byte
		want string
	}{
		"a migration this binary does not know": {crafted(t, future, withDB, dbOnly), "does not know"},
		"a newer version":                       {crafted(t, newer, withDB, dbOnly), "newer Keystone"},
		"another tenant's database":             {crafted(t, otherID, withDB, dbOnly), "another tenant"},
		"a tenant with no name":                 {crafted(t, noName, withDB, dbOnly), "no name"},
		"no database":                           {crafted(t, good(), nil, nil), "no database"},
		"secrets it does not hold":              {crafted(t, saysSecrets, withDB, dbOnly), "disagree"},
		"a file outside the tenant's folder": {crafted(t, good(), map[string][]byte{"tenant/keystone.db": db, "elsewhere.txt": []byte("x")},
			[]string{"tenant/keystone.db", "elsewhere.txt"}), "outside the tenant's folder"},
		"a journal of the database": {crafted(t, good(), map[string][]byte{"tenant/keystone.db": db, "tenant/keystone.db-wal": []byte("x")},
			[]string{"tenant/keystone.db", "tenant/keystone.db-wal"}), "never takes"},
		"secrets that do not open": {crafted(t, saysSecrets, map[string][]byte{"tenant/keystone.db": db, "tenant/secrets.bin": []byte("KSS1 invented")},
			[]string{"tenant/keystone.db", "tenant/secrets.bin"}), "secrets do not open"},
	}
	for what, c := range cases {
		dir := t.TempDir()
		dst, err := OpenWithKeeper(dir, secrets.NewMemoryKeeper())
		if err != nil {
			t.Fatal(err)
		}
		_, err = dst.Restore(bytes.NewReader(c.file), backupPassphrase, RestoreOptions{})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: gave %v, want an error saying %q", what, err, c.want)
		}
		entries, rerr := os.ReadDir(filepath.Join(dir, "tenants"))
		if rerr != nil || len(entries) != 0 || len(dst.Tenants()) != 0 {
			t.Errorf("%s: the refused restore left %v", what, entries)
		}
	}

	// A manifest that is not first.
	var buf bytes.Buffer
	bw, err := backup.Create(&buf, backupPassphrase, backup.MinRounds)
	if err != nil {
		t.Fatal(err)
	}
	if err := bw.File("tenant/keystone.db", int64(len(db)), time.Now(), bytes.NewReader(db)); err != nil {
		t.Fatal(err)
	}
	if err := bw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckBackup(bytes.NewReader(buf.Bytes()), backupPassphrase); !errors.Is(err, backup.ErrDamaged) {
		t.Errorf("a backup that does not start with its manifest gave %v", err)
	}
}

func TestBackupRefusals(t *testing.T) {
	in, id := fullTenant(t)
	cases := map[string]struct {
		id         string
		passphrase string
		o          BackupOptions
	}{
		"a short passphrase":   {id, "too short", testBackupOptions},
		"no passphrase":        {id, "", testBackupOptions},
		"no shortest length":   {id, backupPassphrase, BackupOptions{Rounds: backup.MinRounds}},
		"too few rounds":       {id, backupPassphrase, BackupOptions{Rounds: 10, MinPassphrase: 12}},
		"a tenant not there":   {"0000000000000000", backupPassphrase, testBackupOptions},
		"a path as the tenant": {"../" + id, backupPassphrase, testBackupOptions},
	}
	for what, c := range cases {
		var buf bytes.Buffer
		if _, err := in.Backup(c.id, &buf, c.passphrase, c.o); err == nil {
			t.Errorf("%s: a backup was made", what)
		}
	}
	// No snapshot folder is left in the data folder.
	entries, err := os.ReadDir(in.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".backup-") {
			t.Errorf("a backup left %s in the data folder", e.Name())
		}
	}
}

func TestABackupRefusesALink(t *testing.T) {
	in, id := fullTenant(t)
	link := filepath.Join(in.dir, "tenants", id, "files", "link")
	if err := os.Symlink(filepath.Join(in.dir, registryFile), link); err != nil {
		t.Skipf("this system will not make a link here: %v", err)
	}
	var buf bytes.Buffer
	if _, err := in.Backup(id, &buf, backupPassphrase, testBackupOptions); err == nil || !strings.Contains(err.Error(), "neither a file nor a folder") {
		t.Errorf("a backup of a folder with a link gave %v", err)
	}
}

func TestATenantWithNoSecrets(t *testing.T) {
	src, err := OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, err := src.Create("Test Person Two")
	if err != nil {
		t.Fatal(err)
	}
	file, info := backupOf(t, src, made.ID)
	if info.Secrets != 0 || info.Files != 1 {
		t.Errorf("the backup says %+v", info)
	}
	dir := t.TempDir()
	keeper, wraps := newCountingKeeper()
	dst, err := OpenWithKeeper(dir, keeper)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Restore(bytes.NewReader(file), backupPassphrase, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	// With no secrets the keeper is never used, and the install records none.
	if wraps.Load() != 0 || strings.Contains(registryText(t, dir), masterKeyField) {
		t.Error("a restore with no secrets used the keeper")
	}
	if _, err := os.Stat(filepath.Join(dir, "tenants", made.ID, secretsFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a secrets file was made: %v", err)
	}
	s, err := dst.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, root := range []*os.Root{s.Files(), s.Artifacts()} {
		if _, err := root.Stat("."); err != nil {
			t.Error(err)
		}
	}
}

func TestABackupWhileTheTenantIsWritten(t *testing.T) {
	in, id := fullTenant(t)
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// A write transaction left open on another handle: the snapshot takes
	// what was committed, and the backup does not wait for it.
	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE tenant_meta SET value = 'not committed' WHERE key = 'note'"); err != nil {
		t.Fatal(err)
	}
	file, _ := backupOf(t, in, id)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	dst, err := OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Restore(bytes.NewReader(file), backupPassphrase, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	r, err := dst.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var note string
	if err := r.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'note'").Scan(&note); err != nil || note != "an invented note" {
		t.Errorf("the snapshot holds %q, %v", note, err)
	}
}

var _ io.ReadSeeker = (*bytes.Reader)(nil)
