package signin

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name and password here is invented.
const (
	passwordOne = "test person one's phrase"
	passwordTwo = "test person two's phrase"
)

// testOptions are quick to hash with. The shipped values are read in
// TestDefaultsComeFromTheRegistry.
func testOptions() Options {
	return Options{
		PasswordIterations:       50,
		PasswordMinLength:        12,
		SessionLifetime:          24 * time.Hour,
		FailuresBeforeWait:       3,
		FailureWait:              time.Minute,
		Passkeys:                 true,
		PasskeyRPID:              "localhost",
		PasskeyOrigin:            "http://localhost:7900",
		PasskeyChallengeLifetime: 5 * time.Minute,
	}
}

// twoTenants makes an install with two tenants and returns an open store
// for each, and the data folder.
func twoTenants(t *testing.T) (in *tenant.Install, a, b *tenant.Store, dir string) {
	t.Helper()
	dir = t.TempDir()
	in, err := tenant.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	open := func(name string) *tenant.Store {
		tn, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(tn.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	return in, open("Test Person One"), open("Test Person Two"), dir
}

// fixClock stops the clock and returns a function that moves it on.
func fixClock(t *testing.T) (advance func(time.Duration)) {
	t.Helper()
	at := time.Date(2026, 11, 24, 21, 0, 0, 0, time.UTC)
	old := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = old })
	return func(d time.Duration) { at = at.Add(d) }
}

// closeIf closes a store that should not have been opened.
func closeIf(s *tenant.Store) {
	if s != nil {
		s.Close()
	}
}

func mustSignIn(t *testing.T, s *tenant.Store, password string, o Options) string {
	t.Helper()
	if err := SetPassword(s, password, o); err != nil {
		t.Fatal(err)
	}
	token, _, err := SignIn(s, password, o)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestDefaultsComeFromTheRegistry(t *testing.T) {
	o, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	if o.PasswordIterations < 100000 {
		t.Errorf("the shipped hash is %d rounds, too few to be slow", o.PasswordIterations)
	}
	if o.PasswordMinLength < 8 || o.SessionLifetime <= 0 || o.FailuresBeforeWait < 1 || o.FailureWait <= 0 {
		t.Errorf("the shipped options are not usable: %+v", o)
	}
	if o.Passkeys {
		t.Error("passkeys ship switched on, before a browser has proven them")
	}
	if o.PasskeyRPID == "" || o.PasskeyOrigin == "" || o.PasskeyChallengeLifetime <= 0 {
		t.Errorf("the shipped passkey options are not usable: %+v", o)
	}

	values := map[string]string{
		"signin.password_iterations":        "1000",
		"signin.password_min_length":        "12",
		"signin.session_lifetime":           "1h",
		"signin.failures_before_wait":       "5",
		"signin.failure_wait":               "1m",
		"signin.passkeys":                   "true",
		"signin.passkey_rp_id":              "localhost",
		"signin.passkey_origin":             "http://localhost:7900",
		"signin.passkey_challenge_lifetime": "5m",
	}
	read := func() (Options, error) {
		return ReadOptions(func(k string) (string, bool) { v, ok := values[k]; return v, ok })
	}
	if o, err := read(); err != nil || o.PasswordIterations != 1000 || !o.Passkeys || o.SessionLifetime != time.Hour {
		t.Errorf("good values were misread: %+v %v", o, err)
	}
	for key, bad := range map[string]string{
		"signin.password_iterations":  "0",
		"signin.password_min_length":  "none",
		"signin.session_lifetime":     "0s",
		"signin.failures_before_wait": "0",
		"signin.failure_wait":         "-1m",
	} {
		good := values[key]
		values[key] = bad
		if _, err := read(); err == nil {
			t.Errorf("%s = %q was accepted", key, bad)
		}
		values[key] = good
	}
	delete(values, "signin.session_lifetime")
	if _, err := read(); err == nil {
		t.Error("a missing setting was accepted")
	}
}

func TestPasswordSignIn(t *testing.T) {
	_, a, _, _ := twoTenants(t)
	o := testOptions()

	if _, _, err := SignIn(a, passwordOne, o); !errors.Is(err, ErrRefused) {
		t.Errorf("a tenant with no password signed in: %v", err)
	}
	if has, err := HasPassword(a); err != nil || has {
		t.Errorf("HasPassword before one is set: %v %v", has, err)
	}
	for _, bad := range []string{"", "too short", strings.Repeat("x", maxPasswordBytes+1), "not text \xff\xfe at all"} {
		if err := SetPassword(a, bad, o); !errors.Is(err, ErrBadPassword) {
			t.Errorf("a password of %d bytes was accepted: %v", len(bad), err)
		}
	}
	if err := SetPassword(a, passwordOne, o); err != nil {
		t.Fatal(err)
	}
	if has, err := HasPassword(a); err != nil || !has {
		t.Errorf("HasPassword after one is set: %v %v", has, err)
	}
	for _, wrong := range []string{"", passwordTwo, passwordOne + " ", strings.ToUpper(passwordOne), strings.Repeat("x", maxPasswordBytes+1)} {
		if _, _, err := SignIn(a, wrong, o); !errors.Is(err, ErrRefused) {
			t.Errorf("a wrong password signed in: %v", err)
		}
		// Clear the count, so this loop tests the password and not the wait.
		if _, err := a.DB().Exec("UPDATE signin_password SET failures = 0"); err != nil {
			t.Fatal(err)
		}
	}
	token, sess, err := SignIn(a, passwordOne, o)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := TenantOf(token); !ok || id != a.TenantID() {
		t.Errorf("the token does not name its tenant: %q %v", id, ok)
	}
	got, err := Check(a, token)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != sess.ID || got.TenantID != sess.TenantID || got.Method != sess.Method ||
		!got.Created.Equal(sess.Created) || !got.Expires.Equal(sess.Expires) {
		t.Errorf("Check returned %+v, and SignIn %+v", got, sess)
	}
	if got.TenantID != a.TenantID() || got.Method != MethodPassword || got.ID == "" || strings.Contains(token, got.ID) {
		t.Errorf("the session is wrong: %+v", got)
	}
	if want := got.Created.Add(o.SessionLifetime); !got.Expires.Equal(want) {
		t.Errorf("the session ends at %s, want %s", got.Expires, want)
	}

	// Two sign-ins are two sessions with two tokens.
	token2, sess2, err := SignIn(a, passwordOne, o)
	if err != nil {
		t.Fatal(err)
	}
	if token2 == token || sess2.ID == sess.ID {
		t.Error("two sign-ins shared a token or a session id")
	}

	// A new password replaces the old one.
	if err := SetPassword(a, passwordTwo, o); err != nil {
		t.Fatal(err)
	}
	if _, _, err := SignIn(a, passwordOne, o); !errors.Is(err, ErrRefused) {
		t.Errorf("the old password still signs in: %v", err)
	}
	if _, _, err := SignIn(a, passwordTwo, o); err != nil {
		t.Errorf("the new password does not sign in: %v", err)
	}
}

func TestNeitherThePasswordNorTheTokenIsStored(t *testing.T) {
	_, a, _, dir := twoTenants(t)
	o := testOptions()
	token := mustSignIn(t, a, passwordOne, o)
	secret := token[strings.LastIndex(token, "_")+1:]

	var hash []byte
	if err := a.DB().QueryRow("SELECT token_hash FROM signin_sessions").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hash, hashToken(token)) || len(hash) != 32 {
		t.Error("the session row does not hold the SHA-256 of the token")
	}
	var salt, stored []byte
	if err := a.DB().QueryRow("SELECT salt, hash FROM signin_password").Scan(&salt, &stored); err != nil {
		t.Fatal(err)
	}
	if len(salt) != saltBytes || len(stored) != hashBytes {
		t.Errorf("the salt is %d bytes and the hash %d", len(salt), len(stored))
	}

	// Look through the database as it sits on disk.
	folder := filepath.Join(dir, "tenants", a.TenantID())
	read := 0
	for _, name := range []string{"keystone.db", "keystone.db-wal"} {
		raw, err := os.ReadFile(filepath.Join(folder, name))
		if err != nil {
			continue
		}
		read += len(raw)
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("%s holds the session token", name)
		}
		if bytes.Contains(raw, []byte(passwordOne)) {
			t.Errorf("%s holds the password", name)
		}
	}
	if read == 0 {
		t.Fatal("the database files could not be read, so nothing was checked")
	}

	// Two tenants with the same password do not share a hash.
	_, c, d, _ := twoTenants(t)
	mustSignIn(t, c, passwordOne, o)
	mustSignIn(t, d, passwordOne, o)
	var hc, hd []byte
	if err := c.DB().QueryRow("SELECT hash FROM signin_password").Scan(&hc); err != nil {
		t.Fatal(err)
	}
	if err := d.DB().QueryRow("SELECT hash FROM signin_password").Scan(&hd); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(hc, hd) {
		t.Error("the same password gave the same hash for two tenants: the salt is not working")
	}
}

// The item's proof: a session for tenant A is refused for tenant B.
func TestASessionForOneTenantIsRefusedForAnother(t *testing.T) {
	in, a, b, _ := twoTenants(t)
	o := testOptions()
	tokenA := mustSignIn(t, a, passwordOne, o)
	tokenB := mustSignIn(t, b, passwordTwo, o)

	if _, err := Check(a, tokenA); err != nil {
		t.Fatalf("tenant one's session is refused on its own store: %v", err)
	}
	if _, err := Check(b, tokenA); !errors.Is(err, ErrNoSession) {
		t.Errorf("tenant one's session was accepted on tenant two's store: %v", err)
	}
	if _, err := Check(a, tokenB); !errors.Is(err, ErrNoSession) {
		t.Errorf("tenant two's session was accepted on tenant one's store: %v", err)
	}

	// The API's path: the token alone picks the store, and it picks its own.
	s, sess, err := Resolve(in, tokenA)
	if err != nil {
		t.Fatal(err)
	}
	if s.TenantID() != a.TenantID() || sess.TenantID != a.TenantID() {
		t.Errorf("tenant one's token resolved to tenant %s", s.TenantID())
	}
	s.Close()

	// The same token with the other tenant's id written into it.
	secret := tokenA[strings.LastIndex(tokenA, "_")+1:]
	forged := tokenPrefix + b.TenantID() + "_" + secret
	for name, st := range map[string]*tenant.Store{"one": a, "two": b} {
		if _, err := Check(st, forged); !errors.Is(err, ErrNoSession) {
			t.Errorf("a token re-addressed to tenant two was accepted on tenant %s's store: %v", name, err)
		}
	}
	if s, _, err := Resolve(in, forged); !errors.Is(err, ErrNoSession) {
		closeIf(s)
		t.Errorf("a token re-addressed to tenant two resolved: %v", err)
	}

	// Tenant one's session row, copied into tenant two's database, as a
	// mistaken restore or a bug elsewhere might do.
	var id, method, created, expires string
	var hash []byte
	if err := a.DB().QueryRow("SELECT id, token_hash, method, created_at, expires_at FROM signin_sessions").
		Scan(&id, &hash, &method, &created, &expires); err != nil {
		t.Fatal(err)
	}
	if _, err := b.DB().Exec("INSERT INTO signin_sessions(id, token_hash, method, created_at, expires_at) VALUES(?, ?, ?, ?, ?)",
		id, hash, method, created, expires); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(b, tokenA); !errors.Is(err, ErrNoSession) {
		t.Errorf("a copied session row let tenant one's token onto tenant two's store: %v", err)
	}
	if _, err := Check(b, forged); !errors.Is(err, ErrNoSession) {
		t.Errorf("a copied session row let a re-addressed token onto tenant two's store: %v", err)
	}
	s, _, err = Resolve(in, tokenA)
	if err != nil || s.TenantID() != a.TenantID() {
		t.Errorf("after the copy, tenant one's token no longer resolves to tenant one: %v", err)
	}
	closeIf(s)

	// Signing out through the wrong store ends nothing.
	if err := SignOut(b, tokenA); !errors.Is(err, ErrNoSession) {
		t.Errorf("tenant one's session was signed out through tenant two's store: %v", err)
	}
	if _, err := Check(a, tokenA); err != nil {
		t.Errorf("tenant one's session did not survive: %v", err)
	}

	// One tenant's password is nothing to the other.
	if _, _, err := SignIn(b, passwordOne, o); !errors.Is(err, ErrRefused) {
		t.Errorf("tenant one's password signed in to tenant two: %v", err)
	}
	// And ending all of one tenant's sessions leaves the other's.
	if err := SignOutAll(a); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(b, tokenB); err != nil {
		t.Errorf("tenant two's session ended with tenant one's: %v", err)
	}
}

func TestTokensThatAreNotSessions(t *testing.T) {
	in, a, _, _ := twoTenants(t)
	o := testOptions()
	token := mustSignIn(t, a, passwordOne, o)
	secret := token[strings.LastIndex(token, "_")+1:]
	last := "0"
	if strings.HasSuffix(token, "0") {
		last = "1"
	}
	for _, bad := range []string{
		"",
		"kss_",
		a.TenantID(),
		secret,
		a.TenantID() + "_" + secret,
		tokenPrefix + a.TenantID() + "_" + secret[:63],
		tokenPrefix + a.TenantID() + "_" + strings.ToUpper(secret),
		tokenPrefix + "0000000000000000_" + secret,
		tokenPrefix + "../" + a.TenantID() + "_" + secret,
		tokenPrefix + a.TenantID() + "_" + strings.Repeat("0", 64),
		token[:len(token)-1] + last,
		token + " ",
		" " + token,
		token + "\n",
	} {
		if _, err := Check(a, bad); !errors.Is(err, ErrNoSession) {
			t.Errorf("Check accepted %q: %v", bad, err)
		}
		if s, _, err := Resolve(in, bad); !errors.Is(err, ErrNoSession) {
			closeIf(s)
			t.Errorf("Resolve accepted %q: %v", bad, err)
		}
		if err := SignOut(a, bad); !errors.Is(err, ErrNoSession) {
			t.Errorf("SignOut accepted %q: %v", bad, err)
		}
	}
	if _, err := Check(a, token); err != nil {
		t.Errorf("the real session did not survive: %v", err)
	}
}

func TestSignOutAndExpiry(t *testing.T) {
	in, a, _, _ := twoTenants(t)
	o := testOptions()
	advance := fixClock(t)

	token := mustSignIn(t, a, passwordOne, o)
	other, _, err := SignIn(a, passwordOne, o)
	if err != nil {
		t.Fatal(err)
	}

	// Sign-out ends that one session and leaves the other.
	if err := SignOut(a, token); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(a, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("a signed-out session is still accepted: %v", err)
	}
	if s, _, err := Resolve(in, token); !errors.Is(err, ErrNoSession) {
		closeIf(s)
		t.Errorf("a signed-out session still resolves: %v", err)
	}
	if err := SignOut(a, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("signing out twice: %v", err)
	}
	if _, err := Check(a, other); err != nil {
		t.Errorf("signing out one session ended another: %v", err)
	}

	// A session lasts its lifetime and no longer.
	advance(o.SessionLifetime - time.Second)
	if _, err := Check(a, other); err != nil {
		t.Errorf("a session ended a second early: %v", err)
	}
	advance(time.Second)
	if _, err := Check(a, other); !errors.Is(err, ErrNoSession) {
		t.Errorf("a session outlived its lifetime: %v", err)
	}
	var n int
	if err := a.DB().QueryRow("SELECT count(*) FROM signin_sessions").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d session rows are left after both ended", n)
	}

	// SignOutAll, and a new password, each end every session.
	t1, _, err := SignIn(a, passwordOne, o)
	if err != nil {
		t.Fatal(err)
	}
	t2, _, err := SignIn(a, passwordOne, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := SignOutAll(a); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{t1, t2} {
		if _, err := Check(a, tok); !errors.Is(err, ErrNoSession) {
			t.Errorf("a session survived SignOutAll: %v", err)
		}
	}
	t3, _, err := SignIn(a, passwordOne, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetPassword(a, passwordTwo, o); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(a, t3); !errors.Is(err, ErrNoSession) {
		t.Errorf("a session survived a change of password: %v", err)
	}

	// Signing in clears out sessions that have run out.
	if _, _, err := SignIn(a, passwordTwo, o); err != nil {
		t.Fatal(err)
	}
	advance(o.SessionLifetime + time.Hour)
	if _, _, err := SignIn(a, passwordTwo, o); err != nil {
		t.Fatal(err)
	}
	if err := a.DB().QueryRow("SELECT count(*) FROM signin_sessions").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d session rows after a sign-in long after the last, want 1", n)
	}
}

func TestWrongPasswordsMakeItWait(t *testing.T) {
	_, a, b, _ := twoTenants(t)
	o := testOptions()
	advance := fixClock(t)
	if err := SetPassword(a, passwordOne, o); err != nil {
		t.Fatal(err)
	}
	tokenB := mustSignIn(t, b, passwordTwo, o)

	for i := 0; i < o.FailuresBeforeWait; i++ {
		if _, _, err := SignIn(a, "not the password "+string(rune('a'+i)), o); !errors.Is(err, ErrRefused) {
			t.Fatalf("wrong password %d: %v", i+1, err)
		}
	}
	// Now even the right password waits.
	if _, _, err := SignIn(a, passwordOne, o); !errors.Is(err, ErrWait) {
		t.Fatalf("after %d wrong passwords the next try was not made to wait: %v", o.FailuresBeforeWait, err)
	}
	advance(o.FailureWait - time.Second)
	if _, _, err := SignIn(a, passwordOne, o); !errors.Is(err, ErrWait) {
		t.Errorf("the wait ended early: %v", err)
	}
	// Tries made during the wait do not make it longer.
	advance(time.Second)
	if _, _, err := SignIn(a, "still not the password", o); !errors.Is(err, ErrRefused) {
		t.Errorf("the wait did not end: %v", err)
	}
	// One more wrong one after the wait, and it waits again at once.
	if _, _, err := SignIn(a, passwordOne, o); !errors.Is(err, ErrWait) {
		t.Errorf("a wrong password after the wait did not start another: %v", err)
	}
	advance(o.FailureWait)
	if _, _, err := SignIn(a, passwordOne, o); err != nil {
		t.Fatalf("the right password after the wait: %v", err)
	}
	// A good sign-in clears the count.
	for i := 0; i < o.FailuresBeforeWait-1; i++ {
		if _, _, err := SignIn(a, "not the password", o); !errors.Is(err, ErrRefused) {
			t.Fatal(err)
		}
	}
	if _, _, err := SignIn(a, passwordOne, o); err != nil {
		t.Errorf("the count was not cleared by a good sign-in: %v", err)
	}

	// One tenant's wrong passwords never make another wait.
	if _, _, err := SignIn(b, passwordTwo, o); err != nil {
		t.Errorf("tenant two was made to wait for tenant one's wrong passwords: %v", err)
	}
	if _, err := Check(b, tokenB); err != nil {
		t.Errorf("tenant two's session was touched: %v", err)
	}
}

func TestStoredHashFollowsTheIterationSetting(t *testing.T) {
	_, a, _, _ := twoTenants(t)
	o := testOptions()
	if err := SetPassword(a, passwordOne, o); err != nil {
		t.Fatal(err)
	}
	stored := func() (n int, hash []byte) {
		t.Helper()
		if err := a.DB().QueryRow("SELECT iterations, hash FROM signin_password").Scan(&n, &hash); err != nil {
			t.Fatal(err)
		}
		return n, hash
	}
	n, before := stored()
	if n != o.PasswordIterations {
		t.Fatalf("stored %d rounds, want %d", n, o.PasswordIterations)
	}

	// The setting goes up. A wrong password changes nothing.
	o.PasswordIterations = 80
	if _, _, err := SignIn(a, passwordTwo, o); !errors.Is(err, ErrRefused) {
		t.Fatal(err)
	}
	if n, _ := stored(); n != 50 {
		t.Errorf("a wrong password moved the stored hash to %d rounds", n)
	}
	// The right one signs in and moves the hash.
	token, _, err := SignIn(a, passwordOne, o)
	if err != nil {
		t.Fatal(err)
	}
	n, after := stored()
	if n != 80 || bytes.Equal(before, after) {
		t.Errorf("the stored hash did not move to the new setting: %d rounds", n)
	}
	if _, _, err := SignIn(a, passwordOne, o); err != nil {
		t.Errorf("the password stopped working after the hash moved: %v", err)
	}
	// Moving the hash is not a change of password: sessions stay.
	if _, err := Check(a, token); err != nil {
		t.Errorf("moving the hash ended a session: %v", err)
	}
}
