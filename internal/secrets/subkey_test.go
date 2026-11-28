package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Every id and value here is invented.
func TestSubkey(t *testing.T) {
	dir := t.TempDir()
	k := NewMemoryKeeper()
	v := Open(filepath.Join(dir, "secrets.bin"), "aaaaaaaaaaaaaaaa", k)
	a, err := v.Subkey("keystone at rest v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "secrets.bin")); err != nil {
		t.Fatal("the first subkey did not make the data key and its file")
	}
	if names, err := v.Names(); err != nil || len(names) != 0 {
		t.Fatalf("the vault holds %v (%v)", names, err)
	}
	again, _ := Open(filepath.Join(dir, "secrets.bin"), "aaaaaaaaaaaaaaaa", k).Subkey("keystone at rest v1")
	other, _ := v.Subkey("another purpose")
	if len(a) != 32 || !bytes.Equal(a, again) || bytes.Equal(a, other) {
		t.Error("a subkey is not the same for one purpose and different for two")
	}
	// The same data key file under another tenant id does not open, so it
	// gives no subkey.
	if _, err := Open(filepath.Join(dir, "secrets.bin"), "bbbbbbbbbbbbbbbb", k).Subkey("keystone at rest v1"); err == nil {
		t.Error("another tenant's file gave a subkey")
	}
	if _, err := v.Subkey("Not A Purpose!"); err == nil {
		t.Error("a purpose of any shape was taken")
	}
	if err := v.Set("provider.example", "invented value"); err != nil {
		t.Fatal(err)
	}
	if after, _ := v.Subkey("keystone at rest v1"); !bytes.Equal(after, a) {
		t.Error("storing a secret changed the data key")
	}
}
