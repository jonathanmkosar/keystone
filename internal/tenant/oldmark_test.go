package tenant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The first pass of P15-01 kept an admin mark on each tenant in
// keystone.json and added a step-up to a person's sessions. Both are gone
// (decision 0653): an old file still loads, the next save drops the field,
// and the forward migration takes back what the step-up added.

func TestAKeystoneFileWithTheOldMarkStillLoads(t *testing.T) {
	dir := t.TempDir()
	in, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	// Write the mark back in, as the first pass's binary wrote it.
	path := filepath.Join(dir, registryFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(string(raw), `"name": "Test Person One",`, `"name": "Test Person One", "admin": true,`, 1)
	if old == string(raw) {
		t.Fatalf("the test could not place the old mark in %s", raw)
	}
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	again, err := Open(dir)
	if err != nil {
		t.Fatalf("a keystone.json with the old admin mark did not load: %v", err)
	}
	if got := again.Tenants(); len(got) != 1 || got[0].ID != made.ID {
		t.Fatalf("the tenants read back are %+v", got)
	}
	if _, err := again.Create("Test Person Two"); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"admin"`) {
		t.Errorf("a save kept the old admin mark:\n%s", raw)
	}
}

func TestTheStepUpIsTakenBackByAMigration(t *testing.T) {
	_, a, _ := twoTenants(t)
	rows, err := a.DB().Query("SELECT name FROM pragma_table_info('signin_sessions')")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name == "admin_until" {
			t.Error("signin_sessions still has admin_until")
		}
	}
	if _, err := a.DB().Exec("INSERT INTO signin_challenges(challenge, purpose, expires_at) VALUES(x'01', 'step-up', '2026-11-27T17:00:00Z')"); err == nil {
		t.Error("a passkey prompt can still have the purpose step-up")
	}
	if _, err := a.DB().Exec("INSERT INTO signin_challenges(challenge, purpose, expires_at) VALUES(x'02', 'sign-in', '2026-11-27T17:00:00Z')"); err != nil {
		t.Errorf("a sign-in prompt is refused: %v", err)
	}
}
