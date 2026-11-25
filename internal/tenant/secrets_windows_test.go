//go:build windows

package tenant

import (
	"encoding/json"
	"strings"
	"testing"
)

// Open, with no keeper given and a new install told to use the system's,
// uses the operating system's. This test runs on Windows only, where that
// is DPAPI and a wrap leaves nothing behind. The reopen names no keeper at
// all: keystone.json has recorded it.
func TestOpenUsesDPAPIOnWindows(t *testing.T) {
	dir := t.TempDir()
	in, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	in.NewInstallKeeper("system")
	made, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Secrets().Set(secretName, secretValue); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(registryText(t, dir)), &doc); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(strings.Fields(string(doc[masterKeyField])), ""); got != `{"keeper":"dpapi"}` {
		t.Errorf("keystone.json records the master key as %s, want the dpapi keeper", got)
	}

	// The same folder, opened again as after a restart.
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := again.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })
	if got, ok, err := s2.Secrets().Get(secretName); err != nil || !ok || got != secretValue {
		t.Errorf("the secret did not come back after a reopen: %v, %v", ok, err)
	}
}
