package secrets

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Every name and value here is invented.
const (
	tenantOne  = "00000000000000a1"
	tenantTwo  = "00000000000000b2"
	testName   = "provider.test"
	testSecret = "an invented value, not a real key"
)

// holds reports whether data holds value as it is, or as hex or base64.
func holds(data []byte, value string) bool {
	for _, form := range []string{
		value,
		hex.EncodeToString([]byte(value)),
		base64.StdEncoding.EncodeToString([]byte(value)),
	} {
		if bytes.Contains(data, []byte(form)) {
			return true
		}
	}
	return false
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// oneSecret makes a vault for tenant one that holds the test secret.
func oneSecret(t *testing.T, k Keeper) (path string, v *Vault) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "secrets.bin")
	v = Open(path, tenantOne, k)
	if err := v.Set(testName, testSecret); err != nil {
		t.Fatal(err)
	}
	return path, v
}

func TestSetGetNamesDelete(t *testing.T) {
	k := NewMemoryKeeper()
	path := filepath.Join(t.TempDir(), "secrets.bin")
	v := Open(path, tenantOne, k)

	// An empty vault has no file and needs no keeper.
	if names, err := v.Names(); err != nil || len(names) != 0 {
		t.Fatalf("an empty vault lists %v, %v", names, err)
	}
	if _, ok, err := v.Get(testName); err != nil || ok {
		t.Fatalf("an empty vault has a value: %v, %v", ok, err)
	}
	if err := v.Delete(testName); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("reading an empty vault made a file")
	}

	if err := v.Set(testName, testSecret); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("connector.mail.token", "another invented value"); err != nil {
		t.Fatal(err)
	}
	if err := v.Set(testName, testSecret+" changed"); err != nil {
		t.Fatal(err)
	}

	// A second vault on the same file, as after a restart.
	again := Open(path, tenantOne, k)
	got, ok, err := again.Get(testName)
	if err != nil || !ok || got != testSecret+" changed" {
		t.Fatalf("Get gave %v, %v, and not the value that was set", ok, err)
	}
	names, err := again.Names()
	if err != nil || strings.Join(names, ",") != "connector.mail.token,"+testName {
		t.Fatalf("Names gave %v, %v", names, err)
	}
	if err := again.Delete(testName); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := v.Get(testName); err != nil || ok {
		t.Fatalf("a deleted secret is still there: %v, %v", ok, err)
	}
	if got, ok, err := v.Get("connector.mail.token"); err != nil || !ok || got != "another invented value" {
		t.Fatalf("deleting one secret lost another: %v, %v", ok, err)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("a temporary file was left beside secrets.bin")
	}
}

func TestTheFileDoesNotHoldThePlainValue(t *testing.T) {
	path, _ := oneSecret(t, NewMemoryKeeper())
	raw := mustRead(t, path)
	if holds(raw, testSecret) {
		t.Error("secrets.bin holds the value")
	}
	if holds(raw, testName) {
		t.Error("secrets.bin holds the secret's name in the clear")
	}
	if !bytes.HasPrefix(raw, magic) {
		t.Error("secrets.bin does not start with the magic")
	}
}

func TestAnotherMasterKeyCannotReadTheFile(t *testing.T) {
	path, _ := oneSecret(t, NewMemoryKeeper())
	before := mustRead(t, path)

	other := Open(path, tenantOne, NewMemoryKeeper())
	if got, ok, err := other.Get(testName); err == nil {
		t.Errorf("another master key read the file: found %v, %d bytes", ok, len(got))
	}
	if names, err := other.Names(); err == nil {
		t.Errorf("another master key listed the file: %v", names)
	}
	// It must not write either: that would replace secrets it cannot read.
	if err := other.Set("provider.other", "an invented value"); err == nil {
		t.Error("another master key wrote to the file")
	}
	if err := other.Delete(testName); err == nil {
		t.Error("another master key deleted from the file")
	}
	if !bytes.Equal(before, mustRead(t, path)) {
		t.Error("the file changed under a master key that cannot read it")
	}
}

func TestAnotherTenantCannotReadTheFile(t *testing.T) {
	k := NewMemoryKeeper()
	path, _ := oneSecret(t, k)
	raw := mustRead(t, path)

	// The whole file, copied into tenant two's folder.
	copied := filepath.Join(t.TempDir(), "secrets.bin")
	if err := os.WriteFile(copied, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	two := Open(copied, tenantTwo, k)
	if _, ok, err := two.Get(testName); err == nil {
		t.Errorf("tenant two read tenant one's file: found %v", ok)
	}
	if err := two.Set("provider.other", "an invented value"); err == nil {
		t.Error("tenant two wrote over tenant one's file")
	}
	if !bytes.Equal(raw, mustRead(t, copied)) {
		t.Error("the copied file was changed")
	}

	// Only the wrapped key, put at the head of tenant two's own file.
	twoPath := filepath.Join(t.TempDir(), "secrets.bin")
	own := Open(twoPath, tenantTwo, k)
	if err := own.Set(testName, "tenant two's invented value"); err != nil {
		t.Fatal(err)
	}
	oneWrapped, oneRest := splitFile(t, raw)
	twoWrapped, twoRest := splitFile(t, mustRead(t, twoPath))
	// Tenant one's wrapped key over tenant two's secrets, and tenant one's
	// secrets under tenant two's wrapped key: neither opens for tenant two.
	for _, spliced := range [][]byte{
		joinFile(oneWrapped, twoRest),
		joinFile(twoWrapped, oneRest),
	} {
		if err := os.WriteFile(twoPath, spliced, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := own.Get(testName); err == nil {
			t.Errorf("a spliced file was read: found %v", ok)
		}
	}
}

// looseKeeper wraps without binding to the context, as a careless keeper
// might. The file must still be bound to its tenant.
type looseKeeper struct{ Keeper }

func (k looseKeeper) Wrap(key []byte, _ string) ([]byte, error) { return k.Keeper.Wrap(key, "") }
func (k looseKeeper) Unwrap(wrapped []byte, _ string) ([]byte, error) {
	return k.Keeper.Unwrap(wrapped, "")
}

// spyKeeper notes the contexts it was asked to wrap and unwrap for.
type spyKeeper struct {
	Keeper
	contexts *[]string
}

func (k spyKeeper) Wrap(key []byte, context string) ([]byte, error) {
	*k.contexts = append(*k.contexts, context)
	return k.Keeper.Wrap(key, context)
}

func (k spyKeeper) Unwrap(wrapped []byte, context string) ([]byte, error) {
	*k.contexts = append(*k.contexts, context)
	return k.Keeper.Unwrap(wrapped, context)
}

func TestTheDataKeyIsWrappedForItsTenant(t *testing.T) {
	var contexts []string
	k := spyKeeper{NewMemoryKeeper(), &contexts}
	for _, tenant := range []string{tenantOne, tenantTwo} {
		v := Open(filepath.Join(t.TempDir(), "secrets.bin"), tenant, k)
		if err := v.Set(testName, testSecret); err != nil {
			t.Fatal(err)
		}
		if _, _, err := v.Get(testName); err != nil {
			t.Fatal(err)
		}
	}
	// One wrap and one unwrap for each tenant: the Set that makes the
	// key, and the Get that reads the file back.
	if len(contexts) != 4 {
		t.Fatalf("the keeper was asked %d times, want 4: %q", len(contexts), contexts)
	}
	for i, context := range contexts {
		want := tenantOne
		if i >= 2 {
			want = tenantTwo
		}
		if !strings.Contains(context, want) {
			t.Errorf("the keeper was asked with the context %q, which does not name tenant %s", context, want)
		}
	}
	if contexts[0] == contexts[2] {
		t.Error("two tenants' data keys were wrapped with the same context")
	}
}

func TestTheFileIsBoundToItsTenantWhateverTheKeeperDoes(t *testing.T) {
	k := looseKeeper{NewMemoryKeeper()}
	path, _ := oneSecret(t, k)
	if _, ok, err := Open(path, tenantTwo, k).Get(testName); err == nil {
		t.Errorf("tenant two read tenant one's file with the right data key: found %v", ok)
	}
	if got, ok, err := Open(path, tenantOne, k).Get(testName); err != nil || !ok || got != testSecret {
		t.Errorf("tenant one cannot read its own file: %v, %v", ok, err)
	}
}

// splitFile cuts a secrets file into its wrapped key and what follows.
func splitFile(t *testing.T, raw []byte) (wrapped, rest []byte) {
	t.Helper()
	head := len(magic) + 2
	if len(raw) < head {
		t.Fatal("the file is too short to split")
	}
	end := head + int(binary.BigEndian.Uint16(raw[len(magic):]))
	if len(raw) < end {
		t.Fatal("the file is too short to split")
	}
	return raw[head:end], raw[end:]
}

func joinFile(wrapped, rest []byte) []byte {
	out := append([]byte{}, magic...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(wrapped)))
	out = append(out, wrapped...)
	return append(out, rest...)
}

func TestADamagedFileIsRefusedAndNotReplaced(t *testing.T) {
	k := NewMemoryKeeper()
	path, v := oneSecret(t, k)
	good := mustRead(t, path)
	wrapped, _ := splitFile(t, good)
	afterKey := len(magic) + 2 + len(wrapped)

	damaged := map[string][]byte{
		"empty":                        {},
		"not a secrets file":           []byte("this is some other file"),
		"cut inside the key":           good[:len(magic)+10],
		"cut inside the secrets":       good[:len(good)-5],
		"only the head":                good[:afterKey],
		"a bit changed in the key":     flip(good, len(magic)+2+len(wrapped)/2),
		"a bit changed in the nonce":   flip(good, afterKey+1),
		"a bit changed in the secrets": flip(good, len(good)-20),
		"a longer key length":          flip(good, len(magic)+1),
		"more added at the end":        append(append([]byte{}, good...), 0),
	}
	for what, raw := range damaged {
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := v.Get(testName); err == nil {
			t.Errorf("%s: Get read the file, found %v", what, ok)
		}
		if names, err := v.Names(); err == nil {
			t.Errorf("%s: Names read the file: %v", what, names)
		}
		err := v.Set("provider.other", "an invented value")
		if err == nil {
			t.Errorf("%s: Set wrote a new file over the damaged one", what)
		} else if !strings.Contains(err.Error(), "left as it is") {
			t.Errorf("%s: the error does not say the file was kept: %v", what, err)
		}
		if err := v.Delete(testName); err == nil {
			t.Errorf("%s: Delete wrote a new file over the damaged one", what)
		}
		if !bytes.Equal(raw, mustRead(t, path)) {
			t.Errorf("%s: the damaged file was changed", what)
		}
	}

	// The good file, put back, still reads.
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := v.Get(testName); err != nil || !ok || got != testSecret {
		t.Errorf("the good file no longer reads: %v, %v", ok, err)
	}
}

func flip(raw []byte, at int) []byte {
	out := append([]byte{}, raw...)
	out[at] ^= 0x01
	return out
}

func TestNamesAndValuesAreChecked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.bin")
	v := Open(path, tenantOne, NewMemoryKeeper())
	for _, name := range []string{"", "Provider", "has space", "../x", ".hidden", "-dash", strings.Repeat("a", 101), "näme"} {
		err := v.Set(name, testSecret)
		if err == nil {
			t.Errorf("Set took the name %q", name)
		} else if strings.Contains(err.Error(), testSecret) {
			t.Errorf("the error for the name %q carries the value", name)
		}
		if _, _, err := v.Get(name); err == nil {
			t.Errorf("Get took the name %q", name)
		}
		if err := v.Delete(name); err == nil {
			t.Errorf("Delete took the name %q", name)
		}
	}
	bad := map[string]string{
		"empty":    "",
		"too long": strings.Repeat("x", maxValueSize+1),
		"not text": testSecret + "\xff\xfe",
	}
	for what, value := range bad {
		err := v.Set(testName, value)
		if err == nil {
			t.Errorf("Set took a value that is %s", what)
		} else if value != "" && strings.Contains(err.Error(), value) {
			t.Errorf("the error for a value that is %s carries the value", what)
		} else if strings.Contains(err.Error(), testSecret) {
			t.Errorf("the error for a value that is %s carries part of the value", what)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a refused Set made a file")
	}
	if err := v.Set(testName, strings.Repeat("x", maxValueSize)); err != nil {
		t.Errorf("the longest value was refused: %v", err)
	}
	if err := v.Set(strings.Repeat("a", 100), "an invented value with \"quotes\", <angles>,\nlines and ünïcode"); err != nil {
		t.Errorf("the longest name was refused: %v", err)
	}
	got, ok, err := v.Get(strings.Repeat("a", 100))
	if err != nil || !ok || got != "an invented value with \"quotes\", <angles>,\nlines and ünïcode" {
		t.Errorf("a value with quotes, lines and accents came back changed: %v, %v", ok, err)
	}
}

func TestWritersDoNotLoseEachOthersSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.bin")
	v := Open(path, tenantOne, NewMemoryKeeper())
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := v.Set(fmt.Sprintf("provider.test%d", i), "an invented value"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	names, err := v.Names()
	if err != nil || len(names) != 20 {
		t.Errorf("20 writers left %d secrets, %v", len(names), err)
	}
}

func TestNothingPrintsAValueOrAKey(t *testing.T) {
	k := NewMemoryKeeper()
	_, v := oneSecret(t, k)
	master := k.(*keyKeeper).key
	for _, text := range []string{
		fmt.Sprintf("%v %+v %#v %s", k, k, k, k),
		fmt.Sprintf("%v %+v %#v", v, v, v),
		fmt.Sprint(v.Names()),
	} {
		if holds([]byte(text), testSecret) {
			t.Errorf("printing gave the value away: %s", text)
		}
		// A byte slice prints as numbers, as hex or as text.
		if strings.Contains(text, strings.Trim(fmt.Sprint(master), "[]")) || holds([]byte(text), string(master)) {
			t.Errorf("printing gave the master key away: %s", text)
		}
	}
}
