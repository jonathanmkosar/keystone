package main

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
)

// An invented password, used in a throwaway folder only.
const password = "test admin's own phrase"

func TestTheCommand(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run(dir, []string{"add", "Test", "Admin"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	id := regexp.MustCompile(`[a-f0-9]{16}`).FindString(out.String())
	if id == "" {
		t.Fatalf("add printed no id: %s", out.String())
	}
	if err := run(dir, []string{"add", "test admin"}, nil, &out); !errors.Is(err, adminaccount.ErrNameTaken) {
		t.Errorf("a second account with the same name: %v", err)
	}

	// The password comes from standard input, never an argument.
	if err := run(dir, []string{"password", id}, strings.NewReader(""), &out); err == nil {
		t.Error("an empty password was set")
	}
	if err := run(dir, []string{"password", id}, strings.NewReader(password+"\r\n"), &out); err != nil {
		t.Fatal(err)
	}
	db, err := adminaccount.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	o, err := adminaccount.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := db.SignIn(id, password, o); err != nil {
		t.Errorf("the password set by the command does not sign in: %v", err)
	}
	db.Close()

	out.Reset()
	if err := run(dir, []string{"list"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), id) || !strings.Contains(out.String(), "Test Admin") || strings.Contains(out.String(), password) {
		t.Errorf("list printed:\n%s", out.String())
	}

	// The last account is kept unless --yes is given.
	if err := run(dir, []string{"remove", id}, nil, &out); !errors.Is(err, errLastAccount) {
		t.Errorf("removing the last account without --yes: %v", err)
	}
	if err := run(dir, []string{"add", "Test Admin Two"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run(dir, []string{"remove", id}, nil, &out); err != nil {
		t.Errorf("removing one of two accounts: %v", err)
	}
	out.Reset()
	run(dir, []string{"list"}, nil, &out)
	second := regexp.MustCompile(`([a-f0-9]{16})\s+Test Admin Two`).FindStringSubmatch(out.String())
	if second == nil {
		t.Fatalf("the second account is not listed:\n%s", out.String())
	}
	if err := run(dir, []string{"remove", second[1], "--yes"}, nil, &out); err != nil {
		t.Errorf("removing the last account with --yes: %v", err)
	}

	for _, bad := range [][]string{{}, {"add"}, {"password"}, {"remove"}, {"list", "extra"}, {"remove", "a", "b"}} {
		if err := run(dir, bad, nil, &out); !errors.Is(err, errUsage) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if err := run(dir, []string{"unknown"}, nil, &out); err == nil {
		t.Error("an unknown command ran")
	}

}
