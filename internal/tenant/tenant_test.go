package tenant

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoTenants makes an install with two tenants and returns an open handle
// for each. Every name here is invented.
func twoTenants(t *testing.T) (in *Install, a, b *Store) {
	t.Helper()
	in, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ta, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := in.Create("Test Person Two")
	if err != nil {
		t.Fatal(err)
	}
	if a, err = in.Store(ta.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	if b, err = in.Store(tb.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return in, a, b
}

func TestTwoTenantsCannotSeeEachOther(t *testing.T) {
	_, a, b := twoTenants(t)
	if a.TenantID() == b.TenantID() {
		t.Fatal("two tenants were given the same id")
	}

	// Rows.
	if _, err := a.DB().Exec("INSERT INTO tenant_meta(key, value) VALUES('note', 'only one')"); err != nil {
		t.Fatal(err)
	}
	var got string
	err := b.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'note'").Scan(&got)
	if err == nil {
		t.Errorf("tenant two read tenant one's row: %q", got)
	}

	// Files.
	f, err := a.Files().Create("note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(f, "only one"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Files().Open("note.txt"); err == nil {
		t.Error("tenant two opened tenant one's file by name")
	}
	for _, p := range []string{
		"../../" + a.TenantID() + "/files/note.txt",
		filepath.Join("..", "..", a.TenantID(), "files", "note.txt"),
	} {
		if _, err := b.Files().Open(p); err == nil {
			t.Errorf("tenant two reached tenant one's file through %s", p)
		}
		if _, err := b.Artifacts().Open(p); err == nil {
			t.Errorf("tenant two reached tenant one's file from artifacts through %s", p)
		}
	}
	if _, err := a.Files().Open("note.txt"); err != nil {
		t.Errorf("tenant one cannot open its own file: %v", err)
	}
}

func TestMigrationsRunFromAnEmptyFileAndOnce(t *testing.T) {
	dir := t.TempDir()
	in, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ta, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(ta.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("no migration was applied to an empty file")
	}
	var mode string
	if err := s.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Errorf("journal mode is %q, want wal", mode)
	}
	var fk int
	if err := s.DB().QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Error("foreign keys are off")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Open the same folder again: nothing runs twice, and the registry is kept.
	in2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := in2.Tenants(); len(got) != 1 || got[0].ID != ta.ID || got[0].Name != ta.Name || !got[0].Created.Equal(ta.Created) {
		t.Fatalf("the registry changed across a reopen: %+v, want %+v", got, ta)
	}
	s2, err := in2.Store(ta.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })
	second, err := s2.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Errorf("migrations changed on reopen: %v then %v", first, second)
	}
}

func TestStoreRefusesUnknownOrMalformedID(t *testing.T) {
	in, _, _ := twoTenants(t)
	for _, id := range []string{"", "../x", "..", "0000000000000000", "ABCDEF0123456789", "tenants", "a/b"} {
		if s, err := in.Store(id); err == nil {
			s.Close()
			t.Errorf("Store(%q) opened a handle", id)
		}
	}
}

func TestStoreRefusesADatabaseFromAnotherTenant(t *testing.T) {
	dir := t.TempDir()
	in, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ta, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := in.Create("Test Person Two")
	if err != nil {
		t.Fatal(err)
	}
	// Copy one's database over two's, as a mistaken restore would.
	raw, err := os.ReadFile(filepath.Join(dir, "tenants", ta.ID, "keystone.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, leftover := range []string{"keystone.db-wal", "keystone.db-shm"} {
		os.Remove(filepath.Join(dir, "tenants", tb.ID, leftover))
	}
	if err := os.WriteFile(filepath.Join(dir, "tenants", tb.ID, "keystone.db"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := in.Store(tb.ID); err == nil {
		s.Close()
		t.Error("a database copied from another tenant was opened")
	}
}

func TestRegistrySurvivesReopenAndKeepsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	in, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ta, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}

	// Another part of Keystone adds a field of its own to keystone.json.
	path := filepath.Join(dir, "keystone.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["port_note"] = json.RawMessage(`"kept"`)
	raw, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	in2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tb, err := in2.Create("Test Person Two")
	if err != nil {
		t.Fatal(err)
	}
	got := in2.Tenants()
	if len(got) != 2 || got[0].ID != ta.ID || got[1].ID != tb.ID || got[0].Name != "Test Person One" {
		t.Fatalf("the registry lost a tenant across a reopen: %+v", got)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"port_note"`) {
		t.Errorf("an unknown field was dropped from keystone.json:\n%s", raw)
	}
}

func TestNamesAreChecked(t *testing.T) {
	in, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "   ", strings.Repeat("x", maxNameLength+1)} {
		if _, err := in.Create(name); err == nil {
			t.Errorf("Create(%q) made a tenant", name)
		}
	}
}
