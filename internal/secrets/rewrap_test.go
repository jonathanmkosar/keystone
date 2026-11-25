package secrets

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewrapMovesTheDataKeyToAnotherKeeper(t *testing.T) {
	here, there := NewMemoryKeeper(), NewMemoryKeeper()
	path, v := oneSecret(t, here)
	if err := v.Set("connector.mail.token", "another invented value"); err != nil {
		t.Fatal(err)
	}
	before := mustRead(t, path)

	raw, count, ok, err := v.Rewrapped(there)
	if err != nil || !ok {
		t.Fatalf("Rewrapped: %v, %v", ok, err)
	}
	if count != 2 {
		t.Errorf("Rewrapped counted %d secrets, want 2", count)
	}
	if !bytes.Equal(mustRead(t, path), before) {
		t.Error("Rewrapped changed the vault's own file")
	}
	if holds(raw, testSecret) {
		t.Error("the rewrapped file holds the plain value")
	}

	// The new file opens with the other keeper, and the same data key.
	moved := filepath.Join(t.TempDir(), "secrets.bin")
	if err := os.WriteFile(moved, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	w := Open(moved, tenantOne, there)
	if got, ok, err := w.Get(testName); err != nil || !ok || got != testSecret {
		t.Fatalf("the rewrapped file did not give the value back: %v, %v", ok, err)
	}
	keyHere, err := v.read()
	if err != nil {
		t.Fatal(err)
	}
	keyThere, err := w.read()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(keyHere.key, keyThere.key) {
		t.Error("the data key changed on the way")
	}
	// And not with the keeper it came from.
	if _, _, err := Open(moved, tenantOne, here).Get(testName); err == nil {
		t.Error("the rewrapped file still opens with the first keeper")
	}
	// A write in the new place keeps the moved data key.
	if err := w.Set("provider.other", "a third invented value"); err != nil {
		t.Fatal(err)
	}
	if names, err := w.Names(); err != nil || len(names) != 3 {
		t.Errorf("after a write in the new place: %v, %v", names, err)
	}
}

func TestRewrapRefusesWhatItCannotOpen(t *testing.T) {
	here, there := NewMemoryKeeper(), NewMemoryKeeper()
	path, _ := oneSecret(t, here)
	raw := mustRead(t, path)

	var asked []string
	spy := spyKeeper{there, &asked}
	damaged := append([]byte(nil), raw...)
	damaged[len(damaged)-1] ^= 1
	cases := map[string]struct {
		raw    []byte
		tenant string
		from   Keeper
	}{
		"the wrong keeper":   {raw, tenantOne, NewMemoryKeeper()},
		"another tenant":     {raw, tenantTwo, here},
		"a changed byte":     {damaged, tenantOne, here},
		"not a secrets file": {[]byte("an invented file"), tenantOne, here},
		"a cut file":         {raw[:len(raw)/2], tenantOne, here},
	}
	for what, c := range cases {
		_, _, err := Rewrap(c.raw, c.tenant, c.from, spy)
		if err == nil {
			t.Errorf("%s: rewrapped", what)
			continue
		}
		if strings.Contains(err.Error(), testSecret) {
			t.Errorf("%s: the error carries the value", what)
		}
	}
	if len(asked) != 0 {
		t.Errorf("the new keeper was asked to wrap a key from a file that does not open: %v", asked)
	}

	// The same keeper on both sides checks a file and changes nothing it
	// holds.
	again, count, err := Rewrap(raw, tenantOne, here, here)
	if err != nil || count != 1 {
		t.Fatalf("a check of a good file: %d, %v", count, err)
	}
	moved := filepath.Join(t.TempDir(), "secrets.bin")
	if err := os.WriteFile(moved, again, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := Open(moved, tenantOne, here).Get(testName); err != nil || !ok || got != testSecret {
		t.Errorf("a file checked with its own keeper does not open: %v, %v", ok, err)
	}
}

func TestRewrappedWithNoFile(t *testing.T) {
	v := Open(filepath.Join(t.TempDir(), "secrets.bin"), tenantOne, NewMemoryKeeper())
	raw, count, ok, err := v.Rewrapped(NewMemoryKeeper())
	if err != nil || ok || raw != nil || count != 0 {
		t.Errorf("a vault with no file gave %d bytes, %d, %v, %v", len(raw), count, ok, err)
	}
}

func TestKeyKeeper(t *testing.T) {
	key := bytes.Repeat([]byte{7}, keySize)
	k, err := KeyKeeper("backup", key)
	if err != nil {
		t.Fatal(err)
	}
	if k.Name() != "backup" {
		t.Errorf("the name is %q", k.Name())
	}
	key[0] = 8 // the keeper holds its own copy
	same, err := KeyKeeper("backup", bytes.Repeat([]byte{7}, keySize))
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := k.Wrap(bytes.Repeat([]byte{9}, keySize), "a context")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := same.Unwrap(wrapped, "a context"); err != nil {
		t.Errorf("a keeper with the same key cannot unwrap: %v", err)
	}
	if _, err := same.Unwrap(wrapped, "another context"); err == nil {
		t.Error("a key wrapped for one context unwrapped for another")
	}
	printed := fmt.Sprintf("%v %+v %#v", k, k, k)
	if strings.Contains(printed, "\x07\x07") || strings.Contains(printed, "0707") || strings.Contains(printed, "7 7 7") {
		t.Errorf("printing the keeper shows its key: %s", printed)
	}
	for what, bad := range map[string][]byte{"short": key[:16], "long": append(key, 1), "none": nil} {
		if _, err := KeyKeeper("backup", bad); err == nil {
			t.Errorf("a %s key was taken", what)
		}
	}
	if _, err := KeyKeeper("", bytes.Repeat([]byte{7}, keySize)); err == nil {
		t.Error("a keeper with no name was made")
	}
}
