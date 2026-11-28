package tenant

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/secrets"
)

// The install vault (P7-06) is sealed like a person's and bound to the
// install: a person's secrets.bin in data/admin/, or the install's in a
// person's folder, is refused.
func TestTheInstallVaultIsBoundToTheInstall(t *testing.T) {
	dir := t.TempDir()
	in, err := OpenWithKeeper(dir, secrets.NewMemoryKeeper())
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
	if in.AdminFolder() != filepath.Join(dir, "admin") {
		t.Errorf("the install's folder is %s", in.AdminFolder())
	}
	if _, err := os.Stat(in.AdminFolder()); !os.IsNotExist(err) {
		t.Error("asking for the install's folder made it")
	}
	if err := os.MkdirAll(in.AdminFolder(), 0o700); err != nil {
		t.Fatal(err)
	}
	vault := in.InstallVault()
	if in.InstallVault() != vault {
		t.Error("two calls gave two install vaults")
	}
	if err := vault.Set("search.house", "an invented install key"); err != nil {
		t.Fatal(err)
	}
	if err := s.Secrets().Set("search.mine", "an invented person's key"); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := vault.Get("search.house"); err != nil || !ok || got != "an invented install key" {
		t.Fatalf("the install vault gave %v, %v", ok, err)
	}
	installFile := filepath.Join(in.AdminFolder(), "secrets.bin")
	personFile := filepath.Join(dir, "tenants", made.ID, "secrets.bin")
	installRaw, _ := os.ReadFile(installFile)
	personRaw, _ := os.ReadFile(personFile)

	// The person's file in data/admin/: refused.
	if err := os.WriteFile(installFile, personRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Names(); err == nil {
		t.Error("a person's secrets file was read as the install vault")
	}
	// The install's file in the person's folder: refused.
	if err := os.WriteFile(installFile, installRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(personFile, installRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Secrets().Names(); err == nil {
		t.Error("the install vault was read as a person's")
	}
	if names, err := vault.Names(); err != nil || len(names) != 1 {
		t.Errorf("the install vault, put back, lists %v, %v", names, err)
	}
}
