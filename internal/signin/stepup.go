package signin

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The admin step-up (P15-01, decision 0643). The admin area asks for the
// password or a passkey again, on the session the person already has, and
// stays open until the session's admin_until. A wrong password counts
// towards the same wait as a wrong sign-in, so the step-up is not a second
// door to guess a password through.

// purposeStepUp is the purpose of a passkey prompt for a step-up. A sign-in
// answer cannot use it, and a step-up answer cannot sign in.
const purposeStepUp = "step-up"

// StepUp checks the tenant's password, as SignIn does and with the same
// count of wrong tries, and opens the admin area on the session sessionID
// until lasts from now, or until the session ends if that is sooner. It
// returns the new admin_until.
//
// A wrong password is ErrRefused, a try during the wait is ErrWait, and a
// session that is not a live one of this tenant is ErrNoSession.
func StepUp(s *tenant.Store, sessionID, password string, o Options, lasts time.Duration) (time.Time, error) {
	if lasts <= 0 {
		return time.Time{}, errors.New("a step-up must last longer than nothing")
	}
	t, err := checkPassword(s, password, o)
	if err != nil {
		return time.Time{}, err
	}
	return stepUp(s, sessionID, t, lasts)
}

// BeginPasskeyStepUp starts a step-up with a passkey: a prompt with a fresh
// challenge for this purpose alone.
func BeginPasskeyStepUp(s *tenant.Store, o Options) (PasskeyPrompt, error) {
	return begin(s, purposeStepUp, o)
}

// PasskeyStepUp checks a browser's answer to BeginPasskeyStepUp, as
// PasskeySignIn checks a sign-in, and opens the admin area on the session
// as StepUp does.
func PasskeyStepUp(s *tenant.Store, sessionID string, a PasskeyAssertion, o Options, lasts time.Duration) (time.Time, error) {
	if lasts <= 0 {
		return time.Time{}, errors.New("a step-up must last longer than nothing")
	}
	t, err := checkAssertion(s, a, purposeStepUp, o)
	if err != nil {
		return time.Time{}, err
	}
	return stepUp(s, sessionID, t, lasts)
}

// stepUp writes admin_until on one live session: t plus lasts, never past
// the session's end.
func stepUp(s *tenant.Store, sessionID string, t time.Time, lasts time.Duration) (time.Time, error) {
	var expires string
	err := s.DB().QueryRow("SELECT expires_at FROM signin_sessions WHERE id = ? AND expires_at > ?", sessionID, stamp(t)).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNoSession
	}
	if err != nil {
		return time.Time{}, err
	}
	end, err := time.Parse(stampLayout, expires)
	if err != nil {
		return time.Time{}, err
	}
	until := t.Add(lasts).Truncate(time.Second)
	if until.After(end) {
		until = end
	}
	res, err := s.DB().Exec("UPDATE signin_sessions SET admin_until = ? WHERE id = ?", stamp(until), sessionID)
	if err != nil {
		return time.Time{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return time.Time{}, err
	} else if n == 0 {
		return time.Time{}, ErrNoSession
	}
	return until, nil
}

// EndStepUp closes the admin area on one session before its time. A session
// that is not one of this tenant's is ErrNoSession.
func EndStepUp(s *tenant.Store, sessionID string) error {
	res, err := s.DB().Exec("UPDATE signin_sessions SET admin_until = '' WHERE id = ?", sessionID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNoSession
	}
	return nil
}

// LastSignIn returns when the tenant last opened a session, by a password
// or a passkey, or the zero time when it never has. Before P15-01 it was
// not kept; for a tenant that has not signed in since, it is the newest
// session still open (decision 0646).
func LastSignIn(s *tenant.Store) (time.Time, error) {
	var at string
	err := s.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'last_sign_in'").Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.DB().QueryRow("SELECT COALESCE(MAX(created_at), '') FROM signin_sessions").Scan(&at)
	}
	if err != nil {
		return time.Time{}, err
	}
	if at == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(stampLayout, at)
	if err != nil {
		return time.Time{}, fmt.Errorf("tenant %s: the last sign-in %q: %w", s.TenantID(), at, err)
	}
	return t, nil
}
