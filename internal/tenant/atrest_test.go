package tenant

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/secrets"
)

// Every name, word and value here is invented.

const (
	atRestMarker = "PERIWINKLEMARKERWORD"
	fileID       = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// fill writes the marker into a tenant's database, files, artifacts and
// secrets, through the store as the rest of Keystone does.
func fill(t *testing.T, in *Install, id string) {
	t.Helper()
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DB().Exec("INSERT INTO tenant_meta(key, value) VALUES('note', ?)", atRestMarker+" in the database"); err != nil {
		t.Fatal(err)
	}
	if err := s.FilesFolder().WriteFile(fileID, []byte(atRestMarker+" in a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.ArtifactsFolder().MkdirAll("chatone/artone", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.ArtifactsFolder().WriteFile("chatone/artone/v1", []byte(atRestMarker+" in an artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Secrets().Set("provider.example", atRestMarker+"-secret"); err != nil {
		t.Fatal(err)
	}
}

// readsBack checks that what fill wrote reads back through the store.
func readsBack(t *testing.T, in *Install, id string) {
	t.Helper()
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var note string
	if err := s.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'note'").Scan(&note); err != nil || !strings.Contains(note, atRestMarker) {
		t.Fatalf("the database gave %q, %v", note, err)
	}
	for folder, name := range map[*Folder]string{s.FilesFolder(): fileID, s.ArtifactsFolder(): "chatone/artone/v1"} {
		raw, err := folder.ReadFile(name)
		if err != nil || !strings.Contains(string(raw), atRestMarker) {
			t.Fatalf("%s gave %q, %v", name, raw, err)
		}
	}
	if v, ok, err := s.Secrets().Get("provider.example"); err != nil || !ok || !strings.Contains(v, atRestMarker) {
		t.Fatalf("the secret: %v %v", ok, err)
	}
}

// noPlainOnDisk reads every file in a folder from disk and fails on the
// marker, a plain SQLite header, or a plain name.
func noPlainOnDisk(t *testing.T, folder string) {
	t.Helper()
	files := 0
	err := filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, plain := range []string{"chatone", "artone", fileID} {
			if strings.Contains(d.Name(), plain) {
				t.Errorf("%s is a plain name on disk", p)
			}
		}
		if d.IsDir() {
			return nil
		}
		files++
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte(atRestMarker)) {
			t.Errorf("%s holds the marker word", p)
		}
		if bytes.Contains(raw, []byte("SQLite format 3")) {
			t.Errorf("%s holds a plain database header", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 5 {
		t.Fatalf("only %d files were read", files)
	}
}

func sealedInstall(t *testing.T, dir string, k secrets.Keeper) *Install {
	t.Helper()
	in, err := OpenWithKeeper(dir, k)
	if err != nil {
		t.Fatal(err)
	}
	in.EncryptAtRest(AtRest{On: true})
	return in
}

func TestANewTenantIsSealed(t *testing.T) {
	dir := t.TempDir()
	keeper := secrets.NewMemoryKeeper()
	in := sealedInstall(t, dir, keeper)
	one, err := in.Create("Person One")
	if err != nil {
		t.Fatal(err)
	}
	if sealed, err := in.Sealed(one.ID); err != nil || !sealed {
		t.Fatalf("sealed %v, %v", sealed, err)
	}
	fill(t, in, one.ID)
	readsBack(t, in, one.ID)
	noPlainOnDisk(t, in.folder(one.ID))

	// Open again with the same master key: it opens.
	again, err := OpenWithKeeper(dir, keeper)
	if err != nil {
		t.Fatal(err)
	}
	readsBack(t, again, one.ID)

	// Without the master key, the install does not open.
	if _, err := OpenWithKeeper(dir, secrets.NewMemoryKeeper()); err == nil {
		t.Fatal("a sealed install opened under another master key")
	}
}

// copyDir copies a folder as it is on disk.
func copyDir(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, raw, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAFolderCopiedToAnotherTenantDoesNotOpen(t *testing.T) {
	in := sealedInstall(t, t.TempDir(), secrets.NewMemoryKeeper())
	one, _ := in.Create("Person One")
	fill(t, in, one.ID)
	for _, whole := range []bool{false, true} {
		two, err := in.Create("Person Two")
		if err != nil {
			t.Fatal(err)
		}
		folder := in.folder(two.ID)
		if whole {
			// Everything, the secrets file and seal.json too.
			os.RemoveAll(folder)
			copyDir(t, in.folder(one.ID), folder)
		} else {
			// The database and the files, under the second tenant's own
			// secrets file and seal.json.
			for _, sub := range []string{"files", "artifacts"} {
				os.RemoveAll(filepath.Join(folder, sub))
				copyDir(t, filepath.Join(in.folder(one.ID), sub), filepath.Join(folder, sub))
			}
			raw, _ := os.ReadFile(filepath.Join(in.folder(one.ID), "keystone.db"))
			os.WriteFile(filepath.Join(folder, "keystone.db"), raw, 0o600)
		}
		in.forgetKeys(two.ID)
		if s, err := in.Store(two.ID); err == nil {
			s.Close()
			t.Fatalf("whole %v: a copied folder opened as another tenant", whole)
		}
	}
	// The files alone, put in the other tenant's folder, do not open.
	three, _ := in.Create("Person Three")
	s1, _ := in.Store(one.ID)
	name := s1.FilesFolder().disk(fileID)
	raw, err := os.ReadFile(filepath.Join(in.folder(one.ID), "files", name))
	s1.Close()
	if err != nil {
		t.Fatal(err)
	}
	s3, err := in.Store(three.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	os.WriteFile(filepath.Join(in.folder(three.ID), "files", s3.FilesFolder().disk(fileID)), raw, 0o600)
	if _, err := s3.FilesFolder().ReadFile(fileID); err == nil {
		t.Fatal("another tenant's file opened")
	}
}

func TestAHalfDoneFolderIsRefused(t *testing.T) {
	in := sealedInstall(t, t.TempDir(), secrets.NewMemoryKeeper())
	one, _ := in.Create("Person One")
	os.Remove(filepath.Join(in.folder(one.ID), sealFile))
	if s, err := in.Store(one.ID); err == nil {
		s.Close()
		t.Fatal("a sealed database with no seal.json opened")
	} else if !strings.Contains(err.Error(), "half sealed") {
		t.Fatalf("refused with %v", err)
	}
}

// plainInstall makes an install as an older Keystone left it: plain
// tenants, with the marker everywhere.
func plainInstall(t *testing.T, dir string, keeper secrets.Keeper, people int) (*Install, []string) {
	t.Helper()
	in, err := OpenWithKeeper(dir, keeper)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < people; i++ {
		p, err := in.Create("Plain Person")
		if err != nil {
			t.Fatal(err)
		}
		fill(t, in, p.ID)
		ids = append(ids, p.ID)
	}
	raw, _ := os.ReadFile(filepath.Join(in.folder(ids[0]), "keystone.db"))
	if !bytes.Contains(raw, []byte("SQLite format 3")) {
		t.Fatal("the plain install is not plain")
	}
	return in, ids
}

func TestAPlainInstallIsSealedInPlace(t *testing.T) {
	dir := t.TempDir()
	keeper := secrets.NewMemoryKeeper()
	plain, ids := plainInstall(t, dir, keeper, 2)
	_ = plain

	in := sealedInstall(t, dir, keeper)
	var lines []string
	r, err := in.SealAll(SealOptions{Backup: testBackupOptions, Progress: func(s string) { lines = append(lines, s) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sealed) != 2 || len(r.Backups) != 2 || r.Already != 0 {
		t.Fatalf("report %+v", r)
	}
	for _, line := range lines {
		if strings.Contains(line, atRestMarker) || strings.Contains(line, "Plain Person") {
			t.Errorf("a progress line holds a person's words: %s", line)
		}
	}
	for i, id := range ids {
		readsBack(t, in, id)
		noPlainOnDisk(t, in.folder(id))
		// The backup made before sealing opens with its passphrase.
		pass, err := in.BackupPassphrase(r.Backups[i])
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(r.Backups[i])
		if err != nil {
			t.Fatal(err)
		}
		info, err := CheckBackup(f, pass)
		f.Close()
		if err != nil || info.Tenant.ID != id {
			t.Fatalf("the backup: %+v %v", info, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "tenants"))
	if len(entries) != 2 {
		t.Errorf("the tenants folder holds %d entries after sealing", len(entries))
	}
	// A second run finds nothing to do.
	if again, err := in.SealAll(SealOptions{Backup: testBackupOptions}); err != nil || again.Already != 2 || len(again.Sealed) != 0 {
		t.Fatalf("again: %+v %v", again, err)
	}
	// The backup's passphrase does not open under another master key.
	other, err := OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.BackupPassphrase(r.Backups[0]); err == nil {
		t.Fatal("the backup's passphrase opened under another master key")
	}
}

// TestAnInterruptedSealResumes takes the data folder at every step of the
// sealing, as a stop at that moment would leave it, and opens each copy:
// each opens whole, plain or sealed, with every word in it, and a second
// sealing finishes it.
func TestAnInterruptedSealResumes(t *testing.T) {
	dir := t.TempDir()
	keeper := secrets.NewMemoryKeeper()
	_, ids := plainInstall(t, dir, keeper, 1)
	id := ids[0]
	stops := map[string]string{}
	sealHook = func(step string) {
		at := t.TempDir()
		copyDir(t, dir, at)
		stops[step] = at
	}
	defer func() { sealHook = nil }()
	in := sealedInstall(t, dir, keeper)
	if _, err := in.SealAll(SealOptions{Backup: testBackupOptions}); err != nil {
		t.Fatal(err)
	}
	sealHook = nil
	want := map[string]bool{"copied": false, "marked": false, "aside": true, "placed": true}
	for step, sealedAfter := range want {
		at, ok := stops[step]
		if !ok {
			t.Fatalf("no stop at %s", step)
		}
		again, err := OpenWithKeeper(at, keeper)
		if err != nil {
			t.Fatalf("%s: the install does not open: %v", step, err)
		}
		sealed, err := again.Sealed(id)
		if err != nil {
			t.Fatal(err)
		}
		// "aside" with a whole sealed copy goes forward; before seal.json
		// was written, it goes back.
		if step == "aside" {
			sealedAfter = true
		}
		if sealed != sealedAfter {
			t.Errorf("%s: sealed %v after the stop, want %v", step, sealed, sealedAfter)
		}
		readsBack(t, again, id)
		for _, left := range []string{again.stageOf(id), again.asideOf(id)} {
			if _, err := os.Lstat(left); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s: %s was left behind", step, filepath.Base(left))
			}
		}
		again.EncryptAtRest(AtRest{On: true})
		if _, err := again.SealAll(SealOptions{Backup: testBackupOptions}); err != nil {
			t.Fatalf("%s: sealing again: %v", step, err)
		}
		readsBack(t, again, id)
		noPlainOnDisk(t, again.folder(id))
	}
}

// TestAStopWithTheCopyHalfMadeGoesBack: the plain folder moved aside and a
// sealed copy without seal.json, as a hand or a disk could leave it, puts
// the plain folder back.
func TestAStopWithTheCopyHalfMadeGoesBack(t *testing.T) {
	dir := t.TempDir()
	keeper := secrets.NewMemoryKeeper()
	in, ids := plainInstall(t, dir, keeper, 1)
	id := ids[0]
	os.Rename(in.folder(id), in.asideOf(id))
	os.MkdirAll(filepath.Join(in.stageOf(id), "files"), 0o700)
	os.WriteFile(filepath.Join(in.stageOf(id), "keystone.db"), []byte("half"), 0o600)
	again, err := OpenWithKeeper(dir, keeper)
	if err != nil {
		t.Fatal(err)
	}
	if sealed, _ := again.Sealed(id); sealed {
		t.Fatal("a half-made copy went in place")
	}
	readsBack(t, again, id)
}

func TestASealedBackupRestoresElsewhere(t *testing.T) {
	in := sealedInstall(t, t.TempDir(), secrets.NewMemoryKeeper())
	one, _ := in.Create("Person One")
	fill(t, in, one.ID)
	var buf bytes.Buffer
	if _, err := in.Backup(one.ID, &buf, "invented passphrase", testBackupOptions); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte(atRestMarker)) {
		t.Fatal("the backup holds the marker")
	}
	there := sealedInstall(t, t.TempDir(), secrets.NewMemoryKeeper())
	if _, err := there.Restore(bytes.NewReader(buf.Bytes()), "invented passphrase", RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	if sealed, _ := there.Sealed(one.ID); !sealed {
		t.Fatal("the restored tenant is not sealed")
	}
	readsBack(t, there, one.ID)
	noPlainOnDisk(t, there.folder(one.ID))
}
