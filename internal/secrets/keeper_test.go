package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkKeeper runs what every keeper must do: unwrap what it wrapped, and
// refuse another context, a changed copy and an empty one.
func checkKeeper(t *testing.T, k Keeper) {
	t.Helper()
	key := bytes.Repeat([]byte{0x5a}, keySize)
	wrapped, err := k.Wrap(key, "for tenant one")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wrapped, key) {
		t.Error("the wrapped key holds the key")
	}
	got, err := k.Unwrap(wrapped, "for tenant one")
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("the keeper did not unwrap what it wrapped: %v", err)
	}
	if _, err := k.Unwrap(wrapped, "for tenant two"); err == nil {
		t.Error("a key wrapped for one context unwrapped for another")
	}
	if _, err := k.Unwrap(wrapped, ""); err == nil {
		t.Error("a key wrapped for one context unwrapped with none")
	}
	if _, err := k.Unwrap(flip(wrapped, len(wrapped)/2), "for tenant one"); err == nil {
		t.Error("a changed wrapped key was unwrapped")
	}
	if _, err := k.Unwrap(nil, "for tenant one"); err == nil {
		t.Error("an empty wrapped key was unwrapped")
	}
	again, err := k.Wrap(key, "for tenant one")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(again, wrapped) {
		t.Error("wrapping the same key twice gave the same bytes")
	}
}

func TestMemoryKeeper(t *testing.T) {
	k := NewMemoryKeeper()
	if k.Name() != "memory" {
		t.Errorf("the memory keeper is named %q", k.Name())
	}
	checkKeeper(t, k)

	wrapped, err := k.Wrap(bytes.Repeat([]byte{1}, keySize), "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMemoryKeeper().Unwrap(wrapped, "x"); err == nil {
		t.Error("a second memory keeper unwrapped the first one's key")
	}
}

func TestFileKeeper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile", "keystone", "master.key")
	k := fileKeeper(path)
	if k.Name() != "file" {
		t.Errorf("the file keeper is named %q", k.Name())
	}

	// An unwrap never makes the master key.
	if _, err := k.Unwrap([]byte("anything at all, long enough to hold a nonce"), "x"); err == nil {
		t.Error("the file keeper unwrapped with no master key")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("an unwrap made the key file")
	}

	checkKeeper(t, k)
	raw := mustRead(t, path)
	if len(strings.TrimSpace(string(raw))) != 2*keySize {
		t.Errorf("the key file holds %d characters, want %d of hex", len(strings.TrimSpace(string(raw))), 2*keySize)
	}

	// A second keeper on the same file, as after a restart, has the same
	// master key. One on another file does not.
	wrapped, err := k.Wrap(bytes.Repeat([]byte{1}, keySize), "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileKeeper(path).Unwrap(wrapped, "x"); err != nil {
		t.Errorf("a second keeper on the same key file cannot unwrap: %v", err)
	}
	if !bytes.Equal(raw, mustRead(t, path)) {
		t.Error("the key file changed after it was made")
	}
	elsewhere := fileKeeper(filepath.Join(t.TempDir(), "master.key"))
	if _, err := elsewhere.Wrap(bytes.Repeat([]byte{1}, keySize), "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := elsewhere.Unwrap(wrapped, "x"); err == nil {
		t.Error("a keeper with another key file unwrapped the key")
	}

	// A damaged key file is refused, and the error does not carry it.
	bad := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(bad, []byte("not a master key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = fileKeeper(bad).Wrap(bytes.Repeat([]byte{1}, keySize), "x")
	if err == nil {
		t.Error("a damaged key file was used")
	} else if strings.Contains(err.Error(), "not a master key") {
		t.Errorf("the error carries the key file's text: %v", err)
	}
	if string(mustRead(t, bad)) != "not a master key\n" {
		t.Error("a damaged key file was replaced")
	}
}

// When two processes make the master key at the same moment, both use the
// one that was stored first.
func TestTheFirstStoredMasterKeyWins(t *testing.T) {
	first := strings.Repeat("ab", keySize)
	stored := ""
	k := &keyKeeper{
		name: "test",
		find: func() (string, bool, error) { return stored, stored != "", nil },
		add: func(string) error {
			stored = first // the other process got there first
			return nil
		},
	}
	master, err := k.master(true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(master, bytes.Repeat([]byte{0xab}, keySize)) {
		t.Error("the keeper used a master key that was not the one stored")
	}

	// A store that takes the key and does not keep it is an error.
	lost := &keyKeeper{
		name: "test",
		find: func() (string, bool, error) { return "", false, nil },
		add:  func(string) error { return nil },
	}
	if _, err := lost.Wrap(bytes.Repeat([]byte{1}, keySize), "x"); err == nil {
		t.Error("a master key that was not kept was used")
	}
	failing := &keyKeeper{
		name: "test",
		find: func() (string, bool, error) { return "", false, errors.New("the store is locked") },
	}
	if _, err := failing.Wrap(bytes.Repeat([]byte{1}, keySize), "x"); err == nil || !strings.Contains(err.Error(), "the store is locked") {
		t.Errorf("a store that cannot be read gave %v", err)
	}
}

func TestNamed(t *testing.T) {
	// The file keeper's key file goes in the user's profile, so point the
	// profile at a temporary folder on every system.
	profile := t.TempDir()
	t.Setenv("AppData", profile)
	t.Setenv("XDG_CONFIG_HOME", profile)
	t.Setenv("HOME", profile)

	k, err := Named("file")
	if err != nil {
		t.Fatal(err)
	}
	if k.Name() != "file" {
		t.Errorf("Named(file) gave the %s keeper", k.Name())
	}
	checkKeeper(t, k)
	var found []string
	filepath.WalkDir(profile, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			found = append(found, filepath.Base(p))
		}
		return nil
	})
	if len(found) != 1 || found[0] != "master.key" {
		t.Errorf("the file keeper left %v in the profile, want only master.key", found)
	}

	for _, name := range []string{"memory", "", "no-such-keeper"} {
		if k, err := Named(name); err == nil {
			t.Errorf("Named(%q) gave the %s keeper", name, k.Name())
		}
	}

	// "system" and the system keeper's own name give the same keeper, or
	// the same refusal where the system has none.
	sys, err := System()
	if err != nil {
		if !errors.Is(err, ErrNoKeeper) {
			t.Errorf("a system with no keeper gave %v, want ErrNoKeeper", err)
		}
		if _, err := Named("system"); !errors.Is(err, ErrNoKeeper) {
			t.Errorf("Named(system) gave %v, want ErrNoKeeper", err)
		}
		return
	}
	for _, name := range []string{"system", sys.Name()} {
		k, err := Named(name)
		if err != nil || k.Name() != sys.Name() {
			t.Errorf("Named(%q) gave %v, %v, want the %s keeper", name, k, err, sys.Name())
		}
	}
}
