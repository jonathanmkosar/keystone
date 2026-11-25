package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name, address and value here is invented.
const hostedEntry = `{
  "name": "example-large",
  "label": "Example Large",
  "adapter": "anthropic",
  "endpoint": "https://api.provider-one.example",
  "provider_model": "example-large-1",
  "key": "provider.one",
  "context_window": 200000,
  "output_limit": 16000,
  "capabilities": ["tools", "thinking"],
  "price": {"input": 3, "output": 15},
  "class": "hosted"
}`

const localEntry = `{
  "name": "house-small",
  "label": "House small",
  "adapter": "chat_completions",
  "endpoint": "http://127.0.0.1:11434/v1",
  "provider_model": "example-small:8b",
  "context_window": 8192,
  "output_limit": 2048,
  "price": {"input": 0, "output": 0},
  "class": "local"
}`

func registryOf(t *testing.T, in *tenant.Install, id string) []models.Entry {
	t.Helper()
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entries, err := models.List(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestModelAddListRemove(t *testing.T) {
	in, id := testInstall(t)
	var out bytes.Buffer
	run := func(stdin string, args ...string) error {
		return runModel(in, args, strings.NewReader(stdin), &out)
	}

	// From standard input, with the mark PowerShell can add in front.
	if err := run(bom+hostedEntry+"\r\n", "add", id, "-"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Added model example-large") || !strings.Contains(out.String(), "tenant secret set "+id+" provider.one") {
		t.Errorf("add printed:\n%s", out.String())
	}
	// From a file.
	file := filepath.Join(t.TempDir(), "entry.json")
	if err := os.WriteFile(file, []byte(localEntry), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run("", "add", id, file); err != nil {
		t.Fatal(err)
	}
	if got := registryOf(t, in, id); len(got) != 2 || got[0].Model.Name != "example-large" || got[1].Model.Class != models.ClassLocal {
		t.Fatalf("the registry holds %+v", got)
	}

	// The list names the key and says whether the vault has it.
	out.Reset()
	if err := run("", "list", id); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "provider.one (not in the vault)") || !strings.Contains(out.String(), "2 models") {
		t.Errorf("list printed:\n%s", out.String())
	}
	if err := runSecret(in, []string{"set", id, "provider.one"}, strings.NewReader(testValue+"\n"), false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run("", "list", id); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "not in the vault") || !strings.Contains(out.String(), "provider.one") {
		t.Errorf("list printed, with the key stored:\n%s", out.String())
	}
	if strings.Contains(out.String(), testValue) {
		t.Errorf("list printed a key:\n%s", out.String())
	}

	// A name that is there is refused, and the registry is not changed.
	if err := run(hostedEntry, "add", id, "-"); !errors.Is(err, models.ErrExists) {
		t.Errorf("adding a model twice gave %v", err)
	}

	if err := run("", "remove", id, "example-large"); err != nil {
		t.Fatal(err)
	}
	if got := registryOf(t, in, id); len(got) != 1 || got[0].Model.Name != "house-small" {
		t.Errorf("after remove the registry holds %+v", got)
	}
	if err := run("", "remove", id, "example-large"); err == nil {
		t.Error("removing a model that is not there gave no error")
	}
	if _, ok := stored(t, in, id, "provider.one"); !ok {
		t.Error("removing a model removed its key from the vault")
	}
}

func TestModelRefusals(t *testing.T) {
	in, id := testInstall(t)
	var out bytes.Buffer
	run := func(stdin string, args ...string) error {
		return runModel(in, args, strings.NewReader(stdin), &out)
	}
	for _, args := range [][]string{{}, {"add"}, {"add", id}, {"list", id, "more"}, {"remove", id}, {"show", id}} {
		if err := run("", args...); !errors.Is(err, errModelUsage) {
			t.Errorf("%v gave %v, want the usage", args, err)
		}
	}
	// A key pasted where its name belongs is refused and not repeated.
	pasted := strings.Replace(hostedEntry, `"provider.one"`, `"AN-INVENTED-KEY-0123456789"`, 1)
	err := run(pasted, "add", id, "-")
	if !errors.Is(err, models.ErrInvalid) {
		t.Fatalf("an entry with a key in it gave %v", err)
	}
	if strings.Contains(err.Error(), "AN-INVENTED-KEY") || strings.Contains(out.String(), "AN-INVENTED-KEY") {
		t.Errorf("the refusal repeats the key: %v", err)
	}
	for _, bad := range []string{"", "  \n", "not json", `{"name":"x"}`, hostedEntry + hostedEntry} {
		if err := run(bad, "add", id, "-"); err == nil {
			t.Errorf("the entry %q was added", bad)
		}
	}
	if err := run("", "add", id, filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("a file that is not there gave no error")
	}
	if err := run(hostedEntry, "add", "0000000000000000", "-"); err == nil {
		t.Error("a model was added for a tenant that does not exist")
	}
	if got := registryOf(t, in, id); len(got) != 0 {
		t.Errorf("refused entries left %d models", len(got))
	}
}

func TestModelAddSaysTheTestTenantIgnoresIt(t *testing.T) {
	in, _ := testInstall(t)
	res, err := loadTestTenant(in)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runModel(in, []string{"add", res.Tenant.ID, "-"}, strings.NewReader(localEntry), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "This is the test tenant") {
		t.Errorf("add printed:\n%s", out.String())
	}
}
