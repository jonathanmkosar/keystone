package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name here is invented.
func TestTheAdminCommand(t *testing.T) {
	dir := t.TempDir()
	in, err := tenant.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	one, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	two, err := in.Create("Test Person Two")
	if err != nil {
		t.Fatal(err)
	}
	marked := func(id string) bool {
		t.Helper()
		ok, err := in.IsAdmin(id)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runAdmin(in, args, &out)
		return out.String(), err
	}

	for _, args := range [][]string{nil, {one.ID}, {one.ID, "yes"}, {one.ID, "on", "extra"}} {
		if _, err := run(args...); !errors.Is(err, errAdminUsage) {
			t.Errorf("tenant admin %v: %v, want the usage", args, err)
		}
	}
	if _, err := run("0000000000000000", "on"); err == nil {
		t.Error("a tenant that does not exist was marked")
	}

	out, err := run(one.ID, "on")
	if err != nil || !marked(one.ID) || marked(two.ID) || !strings.Contains(out, "is an admin") {
		t.Fatalf("tenant admin on: %q %v", out, err)
	}
	if _, err := run(two.ID, "on"); err != nil || !marked(two.ID) {
		t.Fatalf("a second admin: %v", err)
	}
	// With two admins, either can lose the mark.
	if _, err := run(two.ID, "off"); err != nil || marked(two.ID) {
		t.Fatalf("tenant admin off with another admin left: %v", err)
	}
	// The last admin is kept, with a warning, unless --yes is given.
	if _, err := run(one.ID, "off"); !errors.Is(err, errLastAdmin) || !marked(one.ID) {
		t.Fatalf("the last admin's mark was taken without --yes: %v", err)
	}
	out, err = run("--yes", one.ID, "off")
	if err != nil || marked(one.ID) || !strings.Contains(out, "no admin now") {
		t.Fatalf("tenant admin off --yes for the last admin: %q %v", out, err)
	}
	// Taking the mark from someone who has none is not the last admin.
	if _, err := run(two.ID, "off"); err != nil {
		t.Errorf("taking a mark that is not there: %v", err)
	}
}
