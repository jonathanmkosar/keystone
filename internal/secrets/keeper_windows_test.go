//go:build windows

package secrets

import (
	"bytes"
	"path/filepath"
	"testing"
)

// The real keeper. It runs on Windows only, uses the account's own DPAPI
// key, and leaves nothing behind: DPAPI stores nothing for a wrap.
func TestDPAPIKeeper(t *testing.T) {
	k, err := System()
	if err != nil {
		t.Fatal(err)
	}
	if k.Name() != "dpapi" {
		t.Fatalf("the system keeper on Windows is %q, want dpapi", k.Name())
	}
	checkKeeper(t, k)
	if _, err := k.Wrap(nil, "x"); err == nil {
		t.Error("DPAPI wrapped an empty key")
	}
}

func TestAVaultUnderDPAPI(t *testing.T) {
	k, err := System()
	if err != nil {
		t.Fatal(err)
	}
	path, v := oneSecret(t, k)
	raw := mustRead(t, path)
	if holds(raw, testSecret) {
		t.Error("secrets.bin holds the value")
	}

	// A second vault, as after a restart: DPAPI unwraps the same key.
	got, ok, err := Open(path, tenantOne, k).Get(testName)
	if err != nil || !ok || got != testSecret {
		t.Fatalf("the value did not come back under DPAPI: %v, %v", ok, err)
	}
	if err := v.Set("provider.other", "another invented value"); err != nil {
		t.Fatal(err)
	}
	// The data key is made once. Later writes keep the same wrapped key.
	before, _ := splitFile(t, raw)
	after, _ := splitFile(t, mustRead(t, path))
	if !bytes.Equal(before, after) {
		t.Error("a second write wrapped a new data key")
	}

	// Tenant one's file is refused for tenant two, and by a keeper that
	// is not DPAPI.
	if _, ok, err := Open(path, tenantTwo, k).Get(testName); err == nil {
		t.Errorf("tenant two read tenant one's file under DPAPI: found %v", ok)
	}
	if _, ok, err := Open(path, tenantOne, NewMemoryKeeper()).Get(testName); err == nil {
		t.Errorf("a keeper without the master key read the file: found %v", ok)
	}
	if _, ok, err := Open(filepath.Join(t.TempDir(), "secrets.bin"), tenantOne, k).Get(testName); err != nil || ok {
		t.Errorf("an empty vault under DPAPI gave %v, %v", ok, err)
	}
}
