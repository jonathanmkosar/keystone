package adminaccount

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/settings"
)

// Invented passwords, used in throwaway folders only.
const (
	passwordOne = "admin one's long phrase"
	passwordTwo = "admin two's long phrase"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	o, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	o.SignIn.PasswordIterations = 1000 // fast in tests; the shipped value is the registry's
	o.SignIn.Passkeys = true
	o.SignIn.PasskeyRPID = "keystone.test"
	o.SignIn.PasskeyOrigin = "https://keystone.test"
	return o
}

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

// fixClock stops the clock at a whole second and returns a way to move it.
func fixClock(t *testing.T) func(time.Duration) {
	t.Helper()
	at := time.Date(2026, 11, 27, 17, 0, 0, 0, time.UTC)
	was := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = was })
	return func(d time.Duration) { at = at.Add(d) }
}

func addWithPassword(t *testing.T, s *Store, name, password string, o Options) Account {
	t.Helper()
	a, err := s.AddAccount(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword(a.ID, password, o.SignIn); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestDefaultsComeFromTheRegistry(t *testing.T) {
	o, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	if o.SessionFor != 8*time.Hour || o.IdleFor != 30*time.Minute {
		t.Errorf("the shipped limits are %s and %s, want 8h and 30m", o.SessionFor, o.IdleFor)
	}
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"admin.session_for", "admin.idle_for"} {
		for _, bad := range []string{"0s", "-1m", "soon", ""} {
			_, err := ReadOptions(func(k string) (string, bool) {
				if k == key {
					return bad, bad != ""
				}
				s, ok := settings.Lookup(reg, k)
				return s.Default, ok
			})
			if err == nil {
				t.Errorf("%s = %q was taken", key, bad)
			}
		}
	}
}

func TestTheDatabaseIsInItsOwnFolder(t *testing.T) {
	_, dir := openTest(t)
	if _, err := os.Stat(filepath.Join(dir, "admin", "admin.db")); err != nil {
		t.Errorf("the admin database is not at admin/admin.db: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tenants")); err == nil {
		t.Error("opening the admin database made a tenants folder")
	}
	// A second open finds the migrations done.
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}

func TestAccounts(t *testing.T) {
	s, _ := openTest(t)
	a, err := s.AddAccount("  Test Admin One ")
	if err != nil {
		t.Fatal(err)
	}
	if !IsID(a.ID) || a.Name != "Test Admin One" {
		t.Errorf("the new account is %+v", a)
	}
	if _, err := s.AddAccount("test admin one"); !errors.Is(err, ErrNameTaken) {
		t.Errorf("a second account with the same name in another case: %v", err)
	}
	if _, err := s.AddAccount(""); err == nil {
		t.Error("an account with no name was made")
	}
	if _, err := s.AddAccount(strings.Repeat("x", 101)); err == nil {
		t.Error("an account with a 101-character name was made")
	}
	b, err := s.AddAccount("Test Admin Two")
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Accounts()
	if err != nil || len(list) != 2 {
		t.Fatalf("the list is %+v, %v", list, err)
	}
	for _, by := range []string{a.ID, "Test Admin One", "TEST ADMIN ONE"} {
		if got, err := s.Find(by); err != nil || got.ID != a.ID {
			t.Errorf("finding %q: %+v %v", by, got, err)
		}
	}
	if _, err := s.Find("Nobody"); !errors.Is(err, ErrNoAccount) {
		t.Errorf("finding nobody: %v", err)
	}
	if err := s.RemoveAccount(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveAccount(b.ID); !errors.Is(err, ErrNoAccount) {
		t.Errorf("removing it twice: %v", err)
	}
	if _, err := s.Account("not an id"); !errors.Is(err, ErrNoAccount) {
		t.Errorf("an id of the wrong form: %v", err)
	}
}

func TestPasswordSignInAndTheToken(t *testing.T) {
	s, dir := openTest(t)
	o := testOptions(t)
	fixClock(t)
	a := addWithPassword(t, s, "Test Admin One", passwordOne, o)
	if err := s.SetPassword(a.ID, "short", o.SignIn); !errors.Is(err, ErrBadPassword) {
		t.Errorf("a short password: %v", err)
	}

	for _, by := range []string{a.ID, "test admin one"} {
		token, sess, acc, err := s.SignIn(by, passwordOne, o)
		if err != nil {
			t.Fatalf("signing in by %q: %v", by, err)
		}
		if !strings.HasPrefix(token, "ksa_") || len(token) != 4+64 || !IsToken(token) {
			t.Errorf("the token %q is not ksa_ and 64 hex", token)
		}
		if acc.ID != a.ID || sess.AccountID != a.ID || sess.Method != signin.MethodPassword || !sess.Expires.Equal(now().Add(o.SessionFor)) {
			t.Errorf("the session is %+v for %+v", sess, acc)
		}
		got, err := s.Check(token, o)
		if err != nil || got.ID != sess.ID {
			t.Errorf("checking the token: %+v %v", got, err)
		}
	}
	if got, _ := s.Account(a.ID); !got.LastSignIn.Equal(now()) {
		t.Errorf("the last sign-in is %s", got.LastSignIn)
	}
	for _, bad := range []struct{ account, password string }{
		{a.ID, passwordTwo}, {"Nobody", passwordOne}, {"", ""},
	} {
		if _, _, _, err := s.SignIn(bad.account, bad.password, o); !errors.Is(err, ErrRefused) {
			t.Errorf("signing in as %q with a wrong password: %v", bad.account, err)
		}
	}
	// A person's token, or anything else, is refused before the database
	// is read.
	for _, bad := range []string{"", "kss_0123456789abcdef_" + strings.Repeat("a", 64), "ksa_" + strings.Repeat("a", 64), "ksa_nothing"} {
		if _, err := s.Check(bad, o); !errors.Is(err, ErrNoSession) {
			t.Errorf("the token %q: %v", bad, err)
		}
	}
	// Neither the password nor a token is in the file.
	s.Close()
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), passwordOne) || strings.Contains(string(raw), "ksa_") {
		t.Error("the admin database holds the password or a token")
	}
}

func TestWrongPasswordsWait(t *testing.T) {
	s, _ := openTest(t)
	o := testOptions(t)
	advance := fixClock(t)
	a := addWithPassword(t, s, "Test Admin One", passwordOne, o)
	for i := 0; i < o.SignIn.FailuresBeforeWait; i++ {
		if _, _, _, err := s.SignIn(a.ID, passwordTwo, o); !errors.Is(err, ErrRefused) {
			t.Fatalf("wrong try %d: %v", i+1, err)
		}
	}
	if _, _, _, err := s.SignIn(a.ID, passwordOne, o); !errors.Is(err, ErrWait) {
		t.Errorf("the right password during the wait: %v", err)
	}
	advance(o.SignIn.FailureWait + time.Second)
	if _, _, _, err := s.SignIn(a.ID, passwordOne, o); err != nil {
		t.Errorf("after the wait: %v", err)
	}
}

func TestSessionLimits(t *testing.T) {
	s, _ := openTest(t)
	o := testOptions(t)
	o.SessionFor, o.IdleFor = 2*time.Hour, 30*time.Minute
	advance := fixClock(t)
	a := addWithPassword(t, s, "Test Admin One", passwordOne, o)

	// Used every 20 minutes, it lives to its end and no further.
	token, _, _, err := s.SignIn(a.ID, passwordOne, o)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		advance(20 * time.Minute)
		if _, err := s.Check(token, o); err != nil {
			t.Fatalf("used after %d minutes: %v", 20*(i+1), err)
		}
	}
	advance(20 * time.Minute) // two hours
	if _, err := s.Check(token, o); !errors.Is(err, ErrNoSession) {
		t.Errorf("a session past admin.session_for: %v", err)
	}

	// Left alone for the idle limit, it ends.
	token, _, _, err = s.SignIn(a.ID, passwordOne, o)
	if err != nil {
		t.Fatal(err)
	}
	advance(29 * time.Minute)
	if _, err := s.Check(token, o); err != nil {
		t.Fatalf("used after 29 minutes: %v", err)
	}
	advance(30 * time.Minute)
	if _, err := s.Check(token, o); !errors.Is(err, ErrNoSession) {
		t.Errorf("a session idle for admin.idle_for: %v", err)
	}
	// And it is gone, not only refused.
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&n)
	if n != 0 {
		t.Errorf("%d ended sessions are still kept", n)
	}
}

func TestSessionsEnd(t *testing.T) {
	s, dir := openTest(t)
	o := testOptions(t)
	a := addWithPassword(t, s, "Test Admin One", passwordOne, o)
	b := addWithPassword(t, s, "Test Admin Two", passwordTwo, o)
	signIn := func(id, pw string) string {
		token, _, _, err := s.SignIn(id, pw, o)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}

	// Signing out ends one session.
	one, two := signIn(a.ID, passwordOne), signIn(a.ID, passwordOne)
	if err := s.SignOut(one); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(one, o); !errors.Is(err, ErrNoSession) {
		t.Errorf("a signed-out session: %v", err)
	}
	if _, err := s.Check(two, o); err != nil {
		t.Errorf("the other session ended too: %v", err)
	}
	// A new password ends every session of that account, and no other's.
	other := signIn(b.ID, passwordTwo)
	if err := s.SetPassword(a.ID, passwordTwo+" again", o.SignIn); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(two, o); !errors.Is(err, ErrNoSession) {
		t.Errorf("a session after a new password: %v", err)
	}
	if _, err := s.Check(other, o); err != nil {
		t.Errorf("another account's session ended: %v", err)
	}
	// Removing an account, from a second process as the command line does,
	// ends its sessions at once.
	cmd, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Close()
	if err := cmd.RemoveAccount(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(other, o); !errors.Is(err, ErrNoSession) {
		t.Errorf("a removed account's session: %v", err)
	}
}

func TestTheAdminAuditLogIsAppendOnly(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	e, err := s.Append(ctx, audit.Entry{Kind: audit.KindAPI, RequestID: "r1", Actor: ActorPrefix + "0123456789abcdef", Action: "GET /api/v1/admin/people", Outcome: audit.OutcomeStarted, Sent: "the phrase sk-ant-api03-" + strings.Repeat("x", 40)}, "the phrase")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.Sent, "the phrase") || strings.Contains(e.Sent, "sk-ant") {
		t.Errorf("an entry kept a secret: %q", e.Sent)
	}
	if _, err := s.Append(ctx, audit.Entry{Kind: audit.KindAPI, RequestID: "r1", Actor: "", Action: "x", Outcome: audit.OutcomeOK}); err == nil {
		t.Error("an entry with no actor was written")
	}
	for _, q := range []string{"UPDATE admin_audit_log SET actor = 'x'", "DELETE FROM admin_audit_log", "INSERT OR REPLACE INTO admin_audit_log(id, at, kind, request_id, actor, action, outcome) VALUES(1, 'a', 'api', 'r', 'a', 'a', 'ok')"} {
		if _, err := s.db.Exec(q); err == nil {
			t.Errorf("%s was allowed", q)
		}
	}
	got, err := s.Log(ctx, audit.Filter{RequestID: "r1"})
	if err != nil || len(got) != 1 || got[0].ID != e.ID {
		t.Errorf("the log reads back %+v, %v", got, err)
	}
}

// device stands in for a phone that holds a passkey, as internal/signin's
// tests do: it builds the bytes a browser hands over, with an ES256 key.
type device struct {
	id, spki []byte
	key      *ecdsa.PrivateKey
	count    uint32
	o        signin.Options
}

func newDevice(t *testing.T, o signin.Options) *device {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 20)
	rand.Read(id)
	return &device{id: id, spki: spki, key: key, o: o}
}

func (d *device) clientData(typ string, challenge []byte) []byte {
	raw, _ := json.Marshal(map[string]any{"type": typ, "challenge": base64.RawURLEncoding.EncodeToString(challenge), "origin": d.o.PasskeyOrigin, "crossOrigin": false})
	return raw
}

func (d *device) authData(attested bool) []byte {
	site := sha256.Sum256([]byte(d.o.PasskeyRPID))
	out := append([]byte{}, site[:]...)
	flags := byte(0x01 | 0x04)
	if attested {
		flags |= 0x40
	}
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, d.count)
	if attested {
		out = append(out, make([]byte, 16)...)
		out = binary.BigEndian.AppendUint16(out, uint16(len(d.id)))
		out = append(out, d.id...)
		out = append(out, 0xa0)
	}
	return out
}

func (d *device) create(p signin.PasskeyPrompt) signin.PasskeyCreation {
	return signin.PasskeyCreation{CredentialID: d.id, ClientDataJSON: d.clientData("webauthn.create", p.Challenge), AuthenticatorData: d.authData(true), PublicKey: d.spki}
}

func (d *device) get(t *testing.T, p signin.PasskeyPrompt, user string) signin.PasskeyAssertion {
	t.Helper()
	d.count++
	cd, ad := d.clientData("webauthn.get", p.Challenge), d.authData(false)
	hash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), hash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, d.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signin.PasskeyAssertion{CredentialID: d.id, ClientDataJSON: cd, AuthenticatorData: ad, Signature: sig, UserHandle: []byte(user)}
}

func TestPasskeys(t *testing.T) {
	s, _ := openTest(t)
	o := testOptions(t)
	a, err := s.AddAccount("Test Admin One")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddAccount("Test Admin Two")
	if err != nil {
		t.Fatal(err)
	}
	d := newDevice(t, o.SignIn)

	// A prompt to add is the account's own: another account cannot use it.
	p, err := s.BeginAddPasskey(a.ID, o.SignIn)
	if err != nil {
		t.Fatal(err)
	}
	if string(p.UserID) != a.ID {
		t.Errorf("the prompt's user is %q", p.UserID)
	}
	if _, err := s.AddPasskey(b.ID, "Phone", d.create(p), o.SignIn); !errors.Is(err, ErrRefused) {
		t.Errorf("another account answered the prompt: %v", err)
	}
	p, _ = s.BeginAddPasskey(a.ID, o.SignIn)
	if _, err := s.AddPasskey(a.ID, "", d.create(p), o.SignIn); !errors.Is(err, ErrPasskeyName) {
		t.Errorf("a passkey with no name: %v", err)
	}
	p, _ = s.BeginAddPasskey(a.ID, o.SignIn)
	pk, err := s.AddPasskey(a.ID, "Phone", d.create(p), o.SignIn)
	if err != nil {
		t.Fatal(err)
	}

	// It signs in, once per prompt, and only as its own account.
	sp, err := s.BeginPasskeySignIn(o.SignIn)
	if err != nil {
		t.Fatal(err)
	}
	answer := d.get(t, sp, a.ID)
	token, sess, acc, err := s.PasskeySignIn(answer, o)
	if err != nil || acc.ID != a.ID || sess.Method != signin.MethodPasskey || !IsToken(token) {
		t.Fatalf("passkey sign-in: %+v %+v %v", acc, sess, err)
	}
	if _, _, _, err := s.PasskeySignIn(answer, o); !errors.Is(err, ErrRefused) {
		t.Errorf("the same answer twice: %v", err)
	}
	sp, _ = s.BeginPasskeySignIn(o.SignIn)
	if _, _, _, err := s.PasskeySignIn(d.get(t, sp, b.ID), o); !errors.Is(err, ErrRefused) {
		t.Errorf("a passkey that says it is another account's: %v", err)
	}

	// Listed and removed by its own account alone.
	if list, err := s.Passkeys(a.ID); err != nil || len(list) != 1 || list[0].ID != pk.ID || list[0].LastUsed.IsZero() {
		t.Errorf("the passkeys are %+v, %v", list, err)
	}
	if err := s.RemovePasskey(b.ID, pk.ID); !errors.Is(err, ErrNoPasskey) {
		t.Errorf("another account removed it: %v", err)
	}
	if err := s.RemovePasskey(a.ID, pk.ID); err != nil {
		t.Fatal(err)
	}
	sp, _ = s.BeginPasskeySignIn(o.SignIn)
	if _, _, _, err := s.PasskeySignIn(d.get(t, sp, a.ID), o); !errors.Is(err, ErrRefused) {
		t.Errorf("a removed passkey signed in: %v", err)
	}

	// Off as shipped.
	off := o
	off.SignIn.Passkeys = false
	if _, err := s.BeginPasskeySignIn(off.SignIn); !errors.Is(err, ErrPasskeysOff) {
		t.Errorf("a prompt with passkeys off: %v", err)
	}
	if _, err := s.BeginAddPasskey(a.ID, off.SignIn); !errors.Is(err, ErrPasskeysOff) {
		t.Errorf("an add prompt with passkeys off: %v", err)
	}
}
