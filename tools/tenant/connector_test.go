package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// A connector that runs a command is added from the command line, listed
// with its token named and never shown, checked, and removed.
func TestConnectorCommand(t *testing.T) {
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	entry := `{"name":"files","label":"Files","transport":"stdio","command":"keystone-no-such-command-for-a-test","args":["--folder","C:\\Notes"],"secret":"connector.files","token_env":"FILES_TOKEN"}`
	var out bytes.Buffer
	if err := runConnector(in, []string{"add", made.ID, "-"}, strings.NewReader("\ufeff"+entry), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Added connector files") || !strings.Contains(out.String(), "tenant secret set") {
		t.Errorf("add said %q", out.String())
	}
	out.Reset()
	if err := runConnector(in, []string{"add", made.ID, "-"}, strings.NewReader(entry), &out); err == nil || !strings.Contains(err.Error(), "remove it first") {
		t.Errorf("a second add gave %v", err)
	}
	s, err := in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	const token = "an invented token for the command's test, not a real one"
	s.Secrets().Set("connector.files", token)
	s.Close()
	out.Reset()
	if err := runConnector(in, []string{"check", made.ID, "files"}, nil, &out); err != nil || !strings.Contains(out.String(), "could not be reached") || !strings.Contains(out.String(), "could not be started") {
		t.Errorf("a check of a command that is not there said %q, %v", out.String(), err)
	}
	out.Reset()
	if err := runConnector(in, []string{"list", made.ID}, nil, &out); err != nil || !strings.Contains(out.String(), "keystone-no-such-command-for-a-test --folder") || !strings.Contains(out.String(), "connector.files") || strings.Contains(out.String(), token) {
		t.Errorf("list said %q, %v", out.String(), err)
	}
	for _, bad := range []string{`{"name":"x","transport":"stdio"}`, `{"name":"x","transport":"stdio","command":"c","surprise":1}`, `not json`} {
		if err := runConnector(in, []string{"add", made.ID, "-"}, strings.NewReader(bad), &out); err == nil {
			t.Errorf("the entry %s was added", bad)
		}
	}
	out.Reset()
	if err := runConnector(in, []string{"remove", made.ID, "files"}, nil, &out); err != nil || !strings.Contains(out.String(), "still in the vault") {
		t.Errorf("remove said %q, %v", out.String(), err)
	}
	if err := runConnector(in, []string{"remove", made.ID, "files"}, nil, &out); err == nil {
		t.Error("removing a connector that is not there did not fail")
	}
	if err := runConnector(in, []string{"nothing", made.ID}, nil, &out); !errors.Is(err, errConnectorUsage) {
		t.Errorf("an unknown command gave %v", err)
	}
}
