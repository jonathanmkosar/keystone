package tenant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/secrets"
)

// Every name and value here is invented.
const (
	secretName  = "provider.test"
	secretValue = "an invented value, not a real key"
)

// twoTenantsWithKeeper makes an install whose master key is held in memory,
// with two tenants, and returns the data folder and an open handle for each.
func twoTenantsWithKeeper(t *testing.T) (dir string, in *Install, a, b *Store) {
	t.Helper()
	dir = t.TempDir()
	in, err := OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"Test Person One", "Test Person Two"} {
		made, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		if i == 0 {
			a = s
		} else {
			b = s
		}
	}
	return dir, in, a, b
}

func registryText(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, registryFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestOneTenantCannotReadAnothersSecrets(t *testing.T) {
	dir, _, a, b := twoTenantsWithKeeper(t)
	if err := a.Secrets().Set(secretName, secretValue); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := a.Secrets().Get(secretName); err != nil || !ok || got != secretValue {
		t.Fatalf("tenant one cannot read its own secret: %v, %v", ok, err)
	}

	// Tenant two's handle sees nothing of it.
	if _, ok, err := b.Secrets().Get(secretName); err != nil || ok {
		t.Errorf("tenant two found tenant one's secret: %v, %v", ok, err)
	}
	if names, err := b.Secrets().Names(); err != nil || len(names) != 0 {
		t.Errorf("tenant two lists %v, %v", names, err)
	}

	// Tenant one's file, copied into tenant two's folder as a mistaken
	// restore would, is refused, although the master key is the same.
	raw, err := os.ReadFile(filepath.Join(dir, "tenants", a.TenantID(), secretsFile))
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(dir, "tenants", b.TenantID(), secretsFile)
	if err := os.WriteFile(copied, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := b.Secrets().Get(secretName); err == nil {
		t.Errorf("tenant two read tenant one's file from its own folder: found %v", ok)
	}
	if err := b.Secrets().Set("provider.other", "an invented value"); err == nil {
		t.Error("tenant two wrote over the file it could not read")
	}
	after, err := os.ReadFile(copied)
	if err != nil || !bytes.Equal(raw, after) {
		t.Errorf("the refused file was changed: %v", err)
	}
}

func TestTheRegistryRecordsTheKeeperAndNoKey(t *testing.T) {
	dir, _, a, _ := twoTenantsWithKeeper(t)
	if strings.Contains(registryText(t, dir), masterKeyField) {
		t.Fatal("keystone.json names a keeper before any secret was stored")
	}
	// Reading an empty vault asks the keeper for nothing.
	if names, err := a.Secrets().Names(); err != nil || len(names) != 0 {
		t.Fatalf("an empty vault lists %v, %v", names, err)
	}
	if strings.Contains(registryText(t, dir), masterKeyField) {
		t.Fatal("keystone.json names a keeper after a read of an empty vault")
	}

	if err := a.Secrets().Set(secretName, secretValue); err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(registryText(t, dir)), &doc); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(strings.Fields(string(doc[masterKeyField])), ""); got != `{"keeper":"memory"}` {
		t.Errorf("keystone.json records the master key as %s, want only the keeper's name", got)
	}
	var listed []Tenant
	if err := json.Unmarshal(doc["tenants"], &listed); err != nil || len(listed) != 2 {
		t.Errorf("recording the keeper lost the tenant list: %v, %v", listed, err)
	}

	// The value is nowhere in the data folder: not in the registry, not in
	// a database, and not in secrets.bin.
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte(secretValue)) {
			t.Errorf("%s holds the value", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAnotherMasterKeyCannotReadAnInstallsSecrets(t *testing.T) {
	dir, _, a, _ := twoTenantsWithKeeper(t)
	if err := a.Secrets().Set(secretName, secretValue); err != nil {
		t.Fatal(err)
	}

	// The same data folder, opened where the master key is another one,
	// as a copy on another machine would be.
	other, err := OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatalf("an install whose secrets cannot be read must still open: %v", err)
	}
	s, err := other.Store(a.TenantID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if got, ok, err := s.Secrets().Get(secretName); err == nil {
		t.Errorf("another master key read the secret: found %v, %d bytes", ok, len(got))
	}
	if err := s.Secrets().Set(secretName, "an invented value"); err == nil {
		t.Error("another master key replaced the secret")
	}
	// The rest of the tenant's data still opens.
	var id string
	if err := s.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'tenant_id'").Scan(&id); err != nil || id != a.TenantID() {
		t.Errorf("the database did not open beside unreadable secrets: %v", err)
	}
}

func TestAKeeperOtherThanTheRecordedOneIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, registryFile), []byte(`{"version": 1, "tenants": [], "master_key": {"keeper": "some-other-keeper"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for what, keeper := range map[string]secrets.Keeper{
		"a keeper held in memory": secrets.NewMemoryKeeper(),
		"the keeper the registry names, which this system does not have": nil,
	} {
		in, err := OpenWithKeeper(dir, keeper)
		if err != nil {
			t.Fatalf("%s: the install must still open: %v", what, err)
		}
		made, err := in.Create("Test Person One")
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		err = s.Secrets().Set(secretName, secretValue)
		if err == nil {
			t.Errorf("%s: a secret was stored", what)
		} else if !strings.Contains(err.Error(), "some-other-keeper") {
			t.Errorf("%s: the error does not name the recorded keeper: %v", what, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "tenants", made.ID, secretsFile)); err == nil {
			t.Errorf("%s: a secrets file was written", what)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(registryText(t, dir), "some-other-keeper") {
		t.Error("the recorded keeper was changed")
	}
}

func TestHandlesForOneTenantShareOneVault(t *testing.T) {
	_, in, a, _ := twoTenantsWithKeeper(t)
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := in.Store(a.TenantID())
			if err != nil {
				t.Error(err)
				return
			}
			defer s.Close()
			if err := s.Secrets().Set(fmt.Sprintf("provider.test%d", i), "an invented value"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	names, err := a.Secrets().Names()
	if err != nil || len(names) != 10 {
		t.Errorf("10 handles left %d secrets, %v", len(names), err)
	}
}
