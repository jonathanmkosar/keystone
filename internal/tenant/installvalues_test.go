package tenant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/secrets"
)

// The install's setting values live in keystone.json beside the tenant list,
// and every field and key this package does not know is kept (P15-03).
func TestInstallValuesKeepWhatTheyDoNotKnow(t *testing.T) {
	dir := t.TempDir()
	in, err := OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.createPlain("Test Person One"); err != nil {
		t.Fatal(err)
	}
	// Another program writes fields of its own, and a setting this build
	// does not know, after the install was opened.
	path := filepath.Join(dir, registryFile)
	var file map[string]json.RawMessage
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	file["someone_elses"] = json.RawMessage(`{"kept":true}`)
	file[SettingsField] = json.RawMessage(`{"later.setting":"7"}`)
	raw, _ = json.Marshal(file)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := in.InstallValues(SettingsField)
	if err != nil || got["later.setting"] != "7" {
		t.Fatalf("the values read back as %v, %v", got, err)
	}
	if err := in.ChangeInstallValues(SettingsField, func(m map[string]string) error { m["server.page_limit"] = "40"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := in.ChangeInstallValues(PersonDefaultsField, func(m map[string]string) error { m["appearance.theme"] = "dark"; return nil }); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	file = nil
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	var theirs struct{ Kept bool }
	if json.Unmarshal(file["someone_elses"], &theirs) != nil || !theirs.Kept {
		t.Errorf("a field this package does not own was not kept: %s", raw)
	}
	var values, defaults map[string]string
	json.Unmarshal(file[SettingsField], &values)
	json.Unmarshal(file[PersonDefaultsField], &defaults)
	if values["later.setting"] != "7" || values["server.page_limit"] != "40" || defaults["appearance.theme"] != "dark" {
		t.Errorf("keystone.json holds %v and %v", values, defaults)
	}
	if len(in.Tenants()) != 1 {
		t.Errorf("the tenant list was lost")
	}
	// A reopened install reads the same, and its next save keeps them.
	again, err := OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.createPlain("Test Person Two"); err != nil {
		t.Fatal(err)
	}
	if got, _ := again.InstallValues(PersonDefaultsField); got["appearance.theme"] != "dark" {
		t.Errorf("after another save the defaults are %v", got)
	}
	// A refused change writes nothing; an unknown field is refused.
	if err := again.ChangeInstallValues(SettingsField, func(m map[string]string) error { m["x.y"] = "1"; return os.ErrInvalid }); err == nil {
		t.Error("a change that failed was kept")
	}
	if got, _ := again.InstallValues(SettingsField); got["x.y"] != "" {
		t.Error("a failed change reached keystone.json")
	}
	if _, err := again.InstallValues("tenants"); err == nil {
		t.Error("the tenant list was offered as setting values")
	}
}
