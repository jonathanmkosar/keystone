package tenant

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/secrets"
)

// diskMark reads one tenant's mark straight from keystone.json.
func diskMark(t *testing.T, dir, id string) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, registryFile))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Tenants []Tenant `json:"tenants"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for _, tn := range file.Tenants {
		if tn.ID == id {
			return tn.Admin
		}
	}
	t.Fatalf("tenant %s is not in keystone.json", id)
	return false
}

func TestTheAdminMark(t *testing.T) {
	dir := t.TempDir()
	in, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	one, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	two, err := in.Create("Test Person Two")
	if err != nil {
		t.Fatal(err)
	}
	if one.Admin || diskMark(t, dir, one.ID) {
		t.Fatal("a new tenant carries the admin mark")
	}
	if ok, err := in.IsAdmin(one.ID); err != nil || ok {
		t.Fatalf("a new tenant is an admin: %v %v", ok, err)
	}

	got, err := in.SetAdmin(one.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Admin || !diskMark(t, dir, one.ID) || diskMark(t, dir, two.ID) {
		t.Fatalf("SetAdmin did not mark the one tenant alone: %+v", got)
	}
	if ok, _ := in.IsAdmin(one.ID); !ok {
		t.Error("IsAdmin does not see the mark")
	}
	if ok, _ := in.IsAdmin(two.ID); ok {
		t.Error("IsAdmin marks the other tenant")
	}
	if ok, _ := in.IsAdmin("0000000000000000"); ok {
		t.Error("an id that is not in the registry is an admin")
	}
	marks, err := in.Marks()
	if err != nil || len(marks) != 1 || !marks[one.ID] {
		t.Errorf("Marks: %v %v", marks, err)
	}
	if _, err := in.SetAdmin("0000000000000000", true); err == nil {
		t.Error("a tenant that does not exist was marked")
	}

	// Reopened, the mark is still there.
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := again.IsAdmin(one.ID); !ok {
		t.Error("the mark did not survive reopening the install")
	}
}

// A server opened the install before the command line changed a mark. It
// sees the change at its next IsAdmin, and a save it makes for another
// reason never writes the old mark back (decision 0641).
func TestAMarkChangedByAnotherProcessCountsAtOnce(t *testing.T) {
	dir := t.TempDir()
	server, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	one, err := server.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}

	command, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := command.SetAdmin(one.ID, true); err != nil {
		t.Fatal(err)
	}
	if ok, err := server.IsAdmin(one.ID); err != nil || !ok {
		t.Fatalf("the running server does not see a mark set from the command line: %v %v", ok, err)
	}
	// The server saves the registry, with its old list in memory.
	if _, err := server.Create("Test Person Two"); err != nil {
		t.Fatal(err)
	}
	if !diskMark(t, dir, one.ID) {
		t.Fatal("a save by the server took away a mark the command line set")
	}

	// And the other way: the mark is taken away while the server holds it.
	command, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := command.SetAdmin(one.ID, false); err != nil {
		t.Fatal(err)
	}
	if ok, _ := server.IsAdmin(one.ID); ok {
		t.Fatal("the running server still sees a mark the command line took away")
	}
	if _, err := server.Create("Test Person Three"); err != nil {
		t.Fatal(err)
	}
	if diskMark(t, dir, one.ID) {
		t.Fatal("a save by the server gave back a mark the command line took away")
	}
}

// A backup carries no mark, a restore onto another install grants none, and
// a restore over a marked tenant keeps the install's mark.
func TestABackupCarriesNoMark(t *testing.T) {
	src, id := fullTenant(t)
	if _, err := src.SetAdmin(id, true); err != nil {
		t.Fatal(err)
	}
	file, made := backupOf(t, src, id)
	if made.Tenant.Admin {
		t.Error("the backup says the tenant is an admin")
	}
	if checked, err := CheckBackup(bytes.NewReader(file), backupPassphrase); err != nil || checked.Tenant.Admin {
		t.Errorf("the backup's manifest says the tenant is an admin: %+v %v", checked.Tenant, err)
	}

	dir := t.TempDir()
	dst, err := OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.Restore(bytes.NewReader(file), backupPassphrase, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := dst.IsAdmin(id); ok || diskMark(t, dir, id) {
		t.Error("a restore onto another install granted the admin mark")
	}

	if _, err := src.Restore(bytes.NewReader(file), backupPassphrase, RestoreOptions{Replace: true}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := src.IsAdmin(id); !ok {
		t.Error("a restore over a marked tenant took the install's mark away")
	}
}
