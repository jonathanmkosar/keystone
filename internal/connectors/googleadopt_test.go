package connectors

import (
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/google"
)

// A sign-in made elsewhere with the Calendar scope becomes the person's
// Google sign-in here: the token in their vault, the account recorded, the
// Calendar connector on, and no Gmail connector, which its scopes do not
// cover. The other person has none of it.
func TestAGoogleSignInIsTakenOver(t *testing.T) {
	one, two := stores(t)
	scopes := []string{"openid", "email", google.ScopeCalendar}
	covered, err := AdoptGoogle(bg(), one, "person.one@example.com", scopes, "invented-refresh-token", 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(covered) != 1 || covered[0] != google.Calendar {
		t.Fatalf("covered %v, want Calendar alone", covered)
	}
	if got, ok, err := one.Secrets().Get(GoogleSecret); err != nil || !ok || got != "invented-refresh-token" {
		t.Errorf("the vault holds %q, %v, %v", got, ok, err)
	}
	var email, kept string
	if err := one.DB().QueryRow("SELECT email, scopes FROM google_accounts WHERE id = 1").Scan(&email, &kept); err != nil {
		t.Fatal(err)
	}
	if email != "person.one@example.com" || kept != strings.Join(scopes, " ") {
		t.Errorf("the account reads %q with %q", email, kept)
	}
	k, err := Get(bg(), one, string(google.Calendar))
	if err != nil || k.Transport != TransportGoogle || !k.Enabled || k.Secret != GoogleSecret || len(k.Tools) == 0 {
		t.Errorf("the Calendar connector reads %+v: %v", k.Connector, err)
	}
	if _, err := Get(bg(), one, string(google.Gmail)); !errors.Is(err, ErrNotFound) {
		t.Errorf("a Gmail connector was made from Calendar scopes: %v", err)
	}
	// Again, as a second run of the command would: nothing doubles.
	if _, err := AdoptGoogle(bg(), one, "person.one@example.com", scopes, "invented-refresh-token-2", 40); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := one.Secrets().Get(GoogleSecret); got != "invented-refresh-token-2" {
		t.Errorf("a second run kept the first token")
	}
	// The other person has nothing of it.
	if _, ok, _ := two.Secrets().Get(GoogleSecret); ok {
		t.Error("the other person's vault holds a Google token")
	}
	if _, err := Get(bg(), two, string(google.Calendar)); !errors.Is(err, ErrNotFound) {
		t.Errorf("the other person has a Calendar connector: %v", err)
	}
}

// What cannot be a sign-in is refused, and nothing is kept.
func TestAGoogleSignInThatDoesNotReadIsRefused(t *testing.T) {
	one, _ := stores(t)
	for name, c := range map[string]struct {
		email, refresh string
		scopes         []string
	}{
		"no token":         {"person.one@example.com", "  ", []string{google.ScopeCalendar}},
		"a token of words": {"person.one@example.com", "two words", []string{google.ScopeCalendar}},
		"no address":       {"", "invented", []string{google.ScopeCalendar}},
		"no service":       {"person.one@example.com", "invented", []string{"openid", "email"}},
		"half of Gmail":    {"person.one@example.com", "invented", []string{google.ScopeGmailRead}},
	} {
		if _, err := AdoptGoogle(bg(), one, c.email, c.scopes, c.refresh, 40); !errors.Is(err, ErrAdopt) {
			t.Errorf("%s: %v, want ErrAdopt", name, err)
		}
	}
	if _, ok, _ := one.Secrets().Get(GoogleSecret); ok {
		t.Error("a refused sign-in left a token in the vault")
	}
}
