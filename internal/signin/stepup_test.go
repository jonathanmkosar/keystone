package signin

import (
	"errors"
	"testing"
	"time"
)

func TestStepUpOpensTheAdminAreaForAWhile(t *testing.T) {
	_, a, _, _ := twoTenants(t)
	o := testOptions()
	advance := fixClock(t)
	token := mustSignIn(t, a, passwordOne, o)
	sess, err := Check(a, token)
	if err != nil {
		t.Fatal(err)
	}
	if !sess.AdminUntil.IsZero() || sess.SteppedUp(now()) {
		t.Fatalf("a new session has a step-up: %+v", sess)
	}

	until, err := StepUp(a, sess.ID, passwordOne, o, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if want := now().Add(15 * time.Minute); !until.Equal(want) {
		t.Errorf("the step-up lasts until %s, want %s", until, want)
	}
	if sess, err = Check(a, token); err != nil || !sess.AdminUntil.Equal(until) || !sess.SteppedUp(now()) {
		t.Fatalf("the session does not carry its step-up: %+v %v", sess, err)
	}
	advance(15*time.Minute - time.Second)
	if sess, _ = Check(a, token); !sess.SteppedUp(now()) {
		t.Error("the step-up ended early")
	}
	advance(time.Second)
	if sess, _ = Check(a, token); sess.SteppedUp(now()) {
		t.Error("the step-up did not end on time")
	}

	// It never lasts past the session.
	until, err = StepUp(a, sess.ID, passwordOne, o, 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !until.Equal(sess.Expires) {
		t.Errorf("a step-up longer than the session lasts until %s, and the session ends %s", until, sess.Expires)
	}

	// Ended by hand.
	if err := EndStepUp(a, sess.ID); err != nil {
		t.Fatal(err)
	}
	if sess, _ = Check(a, token); !sess.AdminUntil.IsZero() {
		t.Error("EndStepUp left the step-up")
	}
	if err := EndStepUp(a, "not-a-session"); !errors.Is(err, ErrNoSession) {
		t.Errorf("ending the step-up of no session: %v", err)
	}
	if _, err := StepUp(a, sess.ID, passwordOne, o, 0); err == nil {
		t.Error("a step-up that lasts nothing was granted")
	}
}

func TestStepUpRefusals(t *testing.T) {
	_, a, b, _ := twoTenants(t)
	o := testOptions()
	fixClock(t)
	token := mustSignIn(t, a, passwordOne, o)
	mustSignIn(t, b, passwordTwo, o)
	sess, err := Check(a, token)
	if err != nil {
		t.Fatal(err)
	}

	// The other tenant's password, and a session the store does not have.
	if _, err := StepUp(a, sess.ID, passwordTwo, o, time.Minute); !errors.Is(err, ErrRefused) {
		t.Errorf("another tenant's password stepped up: %v", err)
	}
	if _, err := StepUp(a, "0000000000000000", passwordOne, o, time.Minute); !errors.Is(err, ErrNoSession) {
		t.Errorf("a session that is not there stepped up: %v", err)
	}
	// Tenant one's session id on tenant two's store opens nothing there.
	if _, err := StepUp(b, sess.ID, passwordTwo, o, time.Minute); !errors.Is(err, ErrNoSession) {
		t.Errorf("tenant one's session stepped up on tenant two's store: %v", err)
	}
	if got, _ := Check(a, token); !got.AdminUntil.IsZero() {
		t.Fatal("a refused step-up left a step-up")
	}

	// Wrong passwords at the step-up count towards the sign-in's wait.
	if err := SetPassword(a, passwordOne, o); err != nil { // clears the count, and ends the session
		t.Fatal(err)
	}
	token = mustSignIn(t, a, passwordOne, o)
	sess, _ = Check(a, token)
	for i := 0; i < o.FailuresBeforeWait-1; i++ {
		if _, err := StepUp(a, sess.ID, "not the phrase at all", o, time.Minute); !errors.Is(err, ErrRefused) {
			t.Fatalf("wrong password %d: %v", i+1, err)
		}
	}
	if _, _, err := SignIn(a, "still not the phrase", o); !errors.Is(err, ErrRefused) {
		t.Fatalf("the last wrong password before the wait: %v", err)
	}
	if _, err := StepUp(a, sess.ID, passwordOne, o, time.Minute); !errors.Is(err, ErrWait) {
		t.Errorf("during the sign-in's wait the step-up took the right password: %v", err)
	}
}

func TestPasskeyStepUp(t *testing.T) {
	_, a, _, _ := twoTenants(t)
	o := testOptions()
	advance := fixClock(t)
	token := mustSignIn(t, a, passwordOne, o)
	sess, _ := Check(a, token)
	d := added(t, a, "es256", o)

	// A sign-in prompt's answer does not step up, and a step-up prompt's
	// answer does not sign in.
	if _, err := PasskeyStepUp(a, sess.ID, d.get(t, prompt(t, a, o)), o, time.Minute); !errors.Is(err, ErrRefused) {
		t.Errorf("a sign-in prompt's answer stepped up: %v", err)
	}
	p, err := BeginPasskeyStepUp(a, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PasskeySignIn(a, d.get(t, p), o); !errors.Is(err, ErrRefused) {
		t.Errorf("a step-up prompt's answer signed in: %v", err)
	}

	advance(time.Second)
	p, err = BeginPasskeyStepUp(a, o)
	if err != nil {
		t.Fatal(err)
	}
	answer := d.get(t, p)
	until, err := PasskeyStepUp(a, sess.ID, answer, o, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := Check(a, token); !got.AdminUntil.Equal(until) || !got.SteppedUp(now()) {
		t.Errorf("a passkey step-up did not open the admin area: %+v", got)
	}
	// An answer works once.
	if _, err := PasskeyStepUp(a, sess.ID, answer, o, 10*time.Minute); !errors.Is(err, ErrRefused) {
		t.Errorf("a step-up answer was used twice: %v", err)
	}

	off := o
	off.Passkeys = false
	if _, err := BeginPasskeyStepUp(a, off); !errors.Is(err, ErrPasskeysOff) {
		t.Errorf("a step-up prompt with passkeys off: %v", err)
	}
}

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
