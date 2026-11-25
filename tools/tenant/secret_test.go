package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name and value here is invented.
const testValue = "an invented value, not a real key"

func testInstall(t *testing.T) (in *tenant.Install, id string) {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	return in, made.ID
}

// stored reads a secret back through the package, the way the server will.
func stored(t *testing.T, in *tenant.Install, id, name string) (string, bool) {
	t.Helper()
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	value, ok, err := s.Secrets().Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return value, ok
}

func TestSecretSetListDelete(t *testing.T) {
	in, id := testInstall(t)
	var out bytes.Buffer
	run := func(stdin string, args ...string) error {
		return runSecret(in, args, strings.NewReader(stdin), false, &out)
	}

	// The line ending a shell adds, and the mark PowerShell can add in
	// front, are not part of the value.
	if err := run("\ufeff"+testValue+"\r\n", "set", id, "provider.test"); err != nil {
		t.Fatal(err)
	}
	if got, ok := stored(t, in, id, "provider.test"); !ok || got != testValue {
		t.Errorf("the stored value is not the one piped in: found %v, %d bytes", ok, len(got))
	}
	if err := run("another invented value\n", "set", id, "connector.mail.token"); err != nil {
		t.Fatal(err)
	}
	if err := run("", "list", id); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "connector.mail.token\nprovider.test\n\n2 secrets") {
		t.Errorf("list printed:\n%s", out.String())
	}
	if err := run("", "delete", id, "provider.test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := stored(t, in, id, "provider.test"); ok {
		t.Error("a deleted secret is still stored")
	}
	if err := run("", "delete", id, "provider.test"); err == nil {
		t.Error("deleting a secret that is not there gave no error")
	}
	for _, value := range []string{testValue, "another invented value"} {
		if strings.Contains(out.String(), value) {
			t.Errorf("a command printed a value:\n%s", out.String())
		}
	}
}

func TestSecretRefusals(t *testing.T) {
	in, id := testInstall(t)
	var out bytes.Buffer

	// A value is never taken from the keyboard, where it would show, and
	// never from an argument.
	err := runSecret(in, []string{"set", id, "provider.test"}, strings.NewReader(testValue), true, &out)
	if err == nil {
		t.Error("a value was taken from the keyboard")
	}
	if err := runSecret(in, []string{"set", id, "provider.test", testValue}, strings.NewReader(""), false, &out); !errors.Is(err, errSecretUsage) {
		t.Errorf("a value given as an argument gave %v, want the usage", err)
	}
	if _, ok := stored(t, in, id, "provider.test"); ok {
		t.Error("a refused set stored something")
	}

	refused := map[string][]string{
		"no command":        {},
		"no tenant":         {"list"},
		"an unknown word":   {"show", id, "provider.test"},
		"set with no name":  {"set", id},
		"list with a name":  {"list", id, "provider.test"},
		"an unknown tenant": {"set", "0000000000000000", "provider.test"},
		"a path as tenant":  {"list", "../" + id},
		"a bad name":        {"set", id, "Not A Name"},
	}
	for what, args := range refused {
		err := runSecret(in, args, strings.NewReader(testValue+"\n"), false, &out)
		if err == nil {
			t.Errorf("%s: no error", what)
		} else if strings.Contains(err.Error(), testValue) {
			t.Errorf("%s: the error carries the value: %v", what, err)
		}
	}
	for what, stdin := range map[string]string{"nothing": "", "only a line ending": "\r\n"} {
		if err := runSecret(in, []string{"set", id, "provider.test"}, strings.NewReader(stdin), false, &out); err == nil {
			t.Errorf("a set with %s on standard input stored something", what)
		}
	}
	if strings.Contains(out.String(), testValue) {
		t.Errorf("a refused command printed the value:\n%s", out.String())
	}
	if names, _ := func() ([]string, error) {
		s, err := in.Store(id)
		if err != nil {
			return nil, err
		}
		defer s.Close()
		return s.Secrets().Names()
	}(); len(names) != 0 {
		t.Errorf("refused commands left secrets behind: %v", names)
	}
}

func TestIsTerminal(t *testing.T) {
	// A file is not the keyboard. The null device is a device, and so is
	// refused as the keyboard would be, which is the safe way round.
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !isTerminal(f) {
		t.Error("the null device was taken for a pipe or a file")
	}
	plain, err := os.CreateTemp(t.TempDir(), "value")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if isTerminal(plain) {
		t.Error("a file was taken for the keyboard")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminal(r) {
		t.Error("a pipe was taken for the keyboard")
	}
}
