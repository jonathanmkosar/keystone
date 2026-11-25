package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name and password here is invented.
func TestSetPasswordReadsOneLineFromInput(t *testing.T) {
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tn, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	o, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	signIn := func(password string) error {
		t.Helper()
		s, err := in.Store(tn.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		_, _, err = signin.SignIn(s, password, o)
		return err
	}

	// Nothing given, or too little, sets nothing.
	for _, input := range []string{"", "\n", "\r\n", "short\n"} {
		if err := setPassword(in, tn.ID, strings.NewReader(input)); err == nil {
			t.Errorf("input %q set a password", input)
		}
	}
	if err := signIn("short"); !errors.Is(err, signin.ErrRefused) {
		t.Errorf("a refused password signs in: %v", err)
	}

	// One line, as typed on Windows: the line ending is not part of it,
	// the space inside it is, and a second line is ignored.
	const password = "test person one's phrase "
	if err := setPassword(in, tn.ID, strings.NewReader(password+"\r\na second line\r\n")); err != nil {
		t.Fatal(err)
	}
	if err := signIn(password); err != nil {
		t.Errorf("the password that was set does not sign in: %v", err)
	}
	if err := signIn(strings.TrimSpace(password)); !errors.Is(err, signin.ErrRefused) {
		t.Errorf("the password signs in without its last space: %v", err)
	}

	// A tenant that is not there.
	if err := setPassword(in, "0000000000000000", strings.NewReader(password+"\n")); err == nil {
		t.Error("a password was set for a tenant that does not exist")
	}
}
