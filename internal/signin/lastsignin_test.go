package signin

import (
	"testing"
	"time"
)

func TestLastSignInIsKept(t *testing.T) {
	_, a, _, _ := twoTenants(t)
	o := testOptions()
	advance := fixClock(t)
	if at, err := LastSignIn(a); err != nil || !at.IsZero() {
		t.Fatalf("a tenant who never signed in: %s %v", at, err)
	}
	token := mustSignIn(t, a, passwordOne, o)
	first := now()
	if at, _ := LastSignIn(a); !at.Equal(first) {
		t.Errorf("the last sign-in is %s, want %s", at, first)
	}
	// A sign-out does not forget it.
	if err := SignOut(a, token); err != nil {
		t.Fatal(err)
	}
	if at, _ := LastSignIn(a); !at.Equal(first) {
		t.Errorf("after signing out the last sign-in is %s", at)
	}
	advance(time.Hour)
	if _, _, err := SignIn(a, passwordOne, o); err != nil {
		t.Fatal(err)
	}
	if at, _ := LastSignIn(a); !at.Equal(now()) {
		t.Errorf("the last sign-in did not move on: %s", at)
	}

	// Before P15-01 it was not kept: the newest open session stands in.
	if _, err := a.DB().Exec("DELETE FROM tenant_meta WHERE key = 'last_sign_in'"); err != nil {
		t.Fatal(err)
	}
	if at, _ := LastSignIn(a); !at.Equal(now()) {
		t.Errorf("with no row the last sign-in is %s, want the newest session", at)
	}
}
