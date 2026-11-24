package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/settings"
)

func TestSettingsPrintsTheRegistry(t *testing.T) {
	var out, errw bytes.Buffer
	if code := run([]string{"settings"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	for _, want := range []string{"server.address", "data.dir", "appearance.theme", "each person", "admin, for the install"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the table does not mention %q:\n%s", want, out.String())
		}
	}
}

func TestSettingsAsJSONAndByLane(t *testing.T) {
	var out, errw bytes.Buffer
	if code := run([]string{"settings", "--json", "--lane", "interface"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	var got []settings.Setting
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if len(got) == 0 {
		t.Fatal("the interface lane printed no settings")
	}
	for _, s := range got {
		if s.Lane != "interface" {
			t.Errorf("%s is from the %s lane", s.Key, s.Lane)
		}
	}
}

func TestUnknownCommandIsRefused(t *testing.T) {
	var out, errw bytes.Buffer
	if code := run([]string{"launch"}, &out, &errw); code != 2 {
		t.Errorf("want exit 2, got %d", code)
	}
	if !strings.Contains(errw.String(), "is not a command") {
		t.Errorf("no explanation given: %s", errw.String())
	}
}

func TestVersionAndHelp(t *testing.T) {
	var out, errw bytes.Buffer
	if code := run([]string{"version"}, &out, &errw); code != 0 || !strings.HasPrefix(out.String(), "keystone ") {
		t.Errorf("version: exit %d, %q", code, out.String())
	}
	out.Reset()
	if code := run(nil, &out, &errw); code != 0 || !strings.Contains(out.String(), "Usage:") {
		t.Errorf("no arguments should print help: exit %d, %q", code, out.String())
	}
}
