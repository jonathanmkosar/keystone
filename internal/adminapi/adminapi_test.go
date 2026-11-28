package adminapi

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/adminview"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name and password here is invented.
const (
	passwordAdmin = "test admin's own phrase"
	passwordOne   = "test person one's phrase"
	passwordTwo   = "test person two's phrase"
)

type rig struct {
	t        *testing.T
	in       *tenant.Install
	h        http.Handler
	o        adminaccount.Options
	routes   []api.Route
	admin    adminaccount.Account
	one, two string
}

func newRig(t *testing.T, passkeys bool) *rig {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := &rig{t: t, in: in, o: adminaccount.Options{SessionFor: time.Hour, IdleFor: 10 * time.Minute, SignIn: signin.Options{
		PasswordIterations: 50, PasswordMinLength: 12, SessionLifetime: time.Hour, FailuresBeforeWait: 3, FailureWait: time.Minute,
		Passkeys: passkeys, PasskeyRPID: "localhost", PasskeyOrigin: "http://localhost:7900", PasskeyChallengeLifetime: time.Minute,
	}}}
	for _, p := range []struct {
		id       *string
		name, pw string
	}{{&g.one, "Test Person One", passwordOne}, {&g.two, "Test Person Two", passwordTwo}} {
		made, err := in.Create(p.name)
		if err != nil {
			t.Fatal(err)
		}
		*p.id = made.ID
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := signin.SetPassword(s, p.pw, g.o.SignIn); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	db := g.db()
	if g.admin, err = db.AddAccount("Test Admin"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPassword(g.admin.ID, passwordAdmin, g.o.SignIn); err != nil {
		t.Fatal(err)
	}
	db.Close()
	open := func() (*adminaccount.Store, error) { return adminaccount.Open(in.Dir()) }
	g.routes = append(api.ShellRoutes(api.Shell{Install: in, SignIn: g.o.SignIn, PageLimit: 50, Version: "test", Routes: func() []api.Route { return g.routes }}),
		Routes(Options{View: adminview.New(in), Open: open, Accounts: g.o})...)
	g.routes = append(g.routes, api.AdminEntriesRoute(50, nil))
	if g.h, err = api.Handler(api.Options{Install: in, MaxBodyBytes: 1 << 16, Heartbeat: time.Second, Admin: g.o}, g.routes); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *rig) db() *adminaccount.Store {
	g.t.Helper()
	db, err := adminaccount.Open(g.in.Dir())
	if err != nil {
		g.t.Fatal(err)
	}
	return db
}

// do makes one call. person and admin are the two cookies, either empty.
func (g *rig) do(method, path, person, admin string, body any) *httptest.ResponseRecorder {
	g.t.Helper()
	var r *http.Request
	if body != nil {
		raw, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if person != "" {
		r.AddCookie(&http.Cookie{Name: api.CookieName, Value: person})
	}
	if admin != "" {
		r.AddCookie(&http.Cookie{Name: api.AdminCookieName, Value: admin})
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r)
	return rec
}

// adminSignIn signs in at the admin's door and returns the token from the
// admin cookie, or "".
func (g *rig) adminSignIn(body SignInBody) (string, *httptest.ResponseRecorder) {
	g.t.Helper()
	rec := g.do("POST", api.AdminPrefix+"session", "", "", body)
	for _, c := range rec.Result().Cookies() {
		if c.Name == api.AdminCookieName && rec.Code == http.StatusOK {
			return c.Value, rec
		}
	}
	return "", rec
}

func (g *rig) personSignIn(id, password string) string {
	g.t.Helper()
	rec := g.do("POST", api.Prefix+"session", "", "", api.SignInBody{Tenant: id, Password: password})
	for _, c := range rec.Result().Cookies() {
		if c.Name == api.CookieName && rec.Code == http.StatusOK {
			return c.Value
		}
	}
	g.t.Fatalf("a person's sign-in answered %d", rec.Code)
	return ""
}

func (g *rig) adminLog(requestID string) []audit.Entry {
	g.t.Helper()
	db := g.db()
	defer db.Close()
	got, err := db.Log(context.Background(), audit.Filter{RequestID: requestID})
	if err != nil {
		g.t.Fatal(err)
	}
	return got
}

func (g *rig) personLog(id, requestID string) []audit.Entry {
	g.t.Helper()
	s, err := g.in.Store(id)
	if err != nil {
		g.t.Fatal(err)
	}
	defer s.Close()
	got, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID})
	if err != nil {
		g.t.Fatal(err)
	}
	return got
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e api.ErrorBody
	json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Code
}

func TestAdminSignIn(t *testing.T) {
	g := newRig(t, false)
	for _, by := range []string{g.admin.ID, "test admin"} {
		token, rec := g.adminSignIn(SignInBody{Account: by, Password: passwordAdmin})
		if !adminaccount.IsToken(token) {
			t.Fatalf("signing in by %q answered %d %s", by, rec.Code, rec.Body)
		}
		var reply SessionReply
		if json.Unmarshal(rec.Body.Bytes(), &reply) != nil || reply.Account.ID != g.admin.ID || reply.Account.Name != "Test Admin" || reply.Session.Expires.IsZero() || reply.Session.IdleUntil.IsZero() {
			t.Errorf("the sign-in answer is %s", rec.Body)
		}
		if strings.Contains(rec.Body.String(), token) {
			t.Error("the body holds the token")
		}
		c := rec.Result().Cookies()[0]
		if c.Path != api.AdminPrefix || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
			t.Errorf("the admin cookie is %+v", c)
		}
		if log := g.adminLog(rec.Header().Get("X-Request-Id")); len(log) != 1 || log[0].Actor != adminaccount.ActorPrefix+g.admin.ID || log[0].Outcome != audit.OutcomeOK {
			t.Errorf("the sign-in is in the admin log as %+v", log)
		}
	}

	// Refused: a wrong password, a person's password, an unknown account,
	// both a password and a passkey, and passkeys while they are off. Each
	// is in the admin log, with no password in it.
	for what, c := range map[string]struct {
		body        SignInBody
		status      int
		code, actor string
	}{
		"a wrong password":     {SignInBody{Account: g.admin.ID, Password: "not the phrase at all"}, http.StatusUnauthorized, "refused", adminaccount.ActorPrefix + g.admin.ID},
		"a person's password":  {SignInBody{Account: g.admin.ID, Password: passwordOne}, http.StatusUnauthorized, "refused", adminaccount.ActorPrefix + g.admin.ID},
		"an unknown account":   {SignInBody{Account: "Nobody", Password: passwordAdmin}, http.StatusUnauthorized, "refused", adminaccount.ActorUnknown},
		"a person's id":        {SignInBody{Account: g.one, Password: passwordOne}, http.StatusUnauthorized, "refused", adminaccount.ActorUnknown},
		"a password and a key": {SignInBody{Account: g.admin.ID, Password: passwordAdmin, Passkey: &PasskeyCredential{}}, http.StatusBadRequest, "bad_request", adminaccount.ActorUnknown},
	} {
		token, rec := g.adminSignIn(c.body)
		if token != "" || rec.Code != c.status || code(t, rec) != c.code {
			t.Errorf("%s answered %d %s", what, rec.Code, rec.Body)
		}
		log := g.adminLog(rec.Header().Get("X-Request-Id"))
		if len(log) != 1 || log[0].Actor != c.actor || log[0].Outcome != audit.OutcomeRefused {
			t.Errorf("%s is in the admin log as %+v", what, log)
		}
		if len(log) == 1 && c.body.Password != "" && strings.Contains(log[0].Sent+log[0].Received+log[0].Action, c.body.Password) {
			t.Errorf("%s left the password in the admin log", what)
		}
	}
	// An admin account cannot sign in at a person's door.
	if rec := g.do("POST", api.Prefix+"session", "", "", api.SignInBody{Tenant: g.admin.ID, Password: passwordAdmin}); rec.Code != http.StatusUnauthorized {
		t.Errorf("an admin account at a person's door answered %d", rec.Code)
	}
	// Wrong passwords wait, as a person's do.
	g.adminSignIn(SignInBody{Account: g.admin.ID, Password: "not the phrase at all"})
	g.adminSignIn(SignInBody{Account: g.admin.ID, Password: "not the phrase at all"})
	if _, rec := g.adminSignIn(SignInBody{Account: g.admin.ID, Password: passwordAdmin}); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("the right password during the wait answered %d", rec.Code)
	}
	if rec := g.do("POST", api.AdminPrefix+"session/passkey", "", "", nil); rec.Code != http.StatusBadRequest || code(t, rec) != "passkeys_off" {
		t.Errorf("a passkey prompt with passkeys off: %d %s", rec.Code, rec.Body)
	}
}

func TestTheSessionAndSignOut(t *testing.T) {
	g := newRig(t, false)
	token, _ := g.adminSignIn(SignInBody{Account: g.admin.ID, Password: passwordAdmin})
	rec := g.do("GET", api.AdminPrefix+"session", "", token, nil)
	var reply SessionReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reply) != nil || reply.Account.ID != g.admin.ID {
		t.Fatalf("who am I: %d %s", rec.Code, rec.Body)
	}
	rec = g.do("DELETE", api.AdminPrefix+"session", "", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-out: %d %s", rec.Code, rec.Body)
	}
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].Name != api.AdminCookieName || c[0].MaxAge >= 0 {
		t.Errorf("sign-out did not clear the admin cookie: %+v", c)
	}
	if rec := g.do("GET", api.AdminPrefix+"session", "", token, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a signed-out session answered %d", rec.Code)
	}
}

func TestPeopleIsInBothLogsAndThePersonSeesIt(t *testing.T) {
	g := newRig(t, false)
	token, _ := g.adminSignIn(SignInBody{Account: g.admin.ID, Password: passwordAdmin})
	person := g.personSignIn(g.two, passwordTwo)

	rec := g.do("GET", api.AdminPrefix+"people", "", token, nil)
	var reply PeopleReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reply) != nil || len(reply.People) != 2 {
		t.Fatalf("people: %d %s", rec.Code, rec.Body)
	}
	if p := reply.People[1]; p.ID != g.two || p.Name != "Test Person Two" || p.LastSignIn.IsZero() {
		t.Errorf("the second person is %+v", p)
	}
	id := rec.Header().Get("X-Request-Id")
	if log := g.adminLog(id); len(log) != 2 || log[0].Actor != adminaccount.ActorPrefix+g.admin.ID || log[1].Outcome != audit.OutcomeOK {
		t.Errorf("people is in the admin log as %+v", log)
	}
	for _, p := range []string{g.one, g.two} {
		if log := g.personLog(p, id); len(log) != 2 || log[0].Actor != adminview.ActorPrefix+g.admin.ID || log[1].Outcome != audit.OutcomeOK {
			t.Errorf("people is in a person's log as %+v", log)
		}
	}

	// The person sees it in their own Settings.
	rec = g.do("GET", api.Prefix+"audit/admin", person, "", nil)
	var seen api.AuditReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &seen) != nil {
		t.Fatalf("the person's admin entries: %d %s", rec.Code, rec.Body)
	}
	found := 0
	for _, e := range seen.Entries {
		if e.RequestID == id && strings.Contains(e.Sent, `"by":"Test Admin"`) {
			found++
		}
	}
	if found != 2 {
		t.Errorf("the person sees %d entries of the admin's read, want 2: %s", found, rec.Body)
	}
}

func TestEveryAdminRouteIsRefusedToAPerson(t *testing.T) {
	g := newRig(t, false)
	person := g.personSignIn(g.one, passwordOne)
	for _, rt := range g.routes {
		if !rt.Admin || rt.Door {
			continue
		}
		path := strings.ReplaceAll(rt.Path, "{id}", "x")
		if rec := g.do(rt.Method, path, person, "", map[string]string{}); rec.Code != http.StatusForbidden || code(t, rec) != "not_admin" {
			t.Errorf("%s %s with a person's session answered %d %s", rt.Method, rt.Path, rec.Code, rec.Body)
		}
	}
}

// device stands in for a phone that holds a passkey, with an ES256 key, as
// internal/signin's tests do.
type device struct {
	id, spki []byte
	key      *ecdsa.PrivateKey
	count    uint32
}

func newDevice(t *testing.T) *device {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spki, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	id := make([]byte, 20)
	rand.Read(id)
	return &device{id: id, spki: spki, key: key}
}

func (d *device) clientData(typ, challenge string) []byte {
	raw, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": "http://localhost:7900", "crossOrigin": false})
	return raw
}

func (d *device) authData(attested bool) []byte {
	site := sha256.Sum256([]byte("localhost"))
	out := append([]byte{}, site[:]...)
	flags := byte(0x05)
	if attested {
		flags |= 0x40
	}
	out = binary.BigEndian.AppendUint32(append(out, flags), d.count)
	if attested {
		out = binary.BigEndian.AppendUint16(append(out, make([]byte, 16)...), uint16(len(d.id)))
		out = append(append(out, d.id...), 0xa0)
	}
	return out
}

func TestAdminPasskeys(t *testing.T) {
	g := newRig(t, true)
	token, _ := g.adminSignIn(SignInBody{Account: g.admin.ID, Password: passwordAdmin})
	d := newDevice(t)

	// Add one.
	rec := g.do("POST", api.AdminPrefix+"passkeys/prompt", "", token, nil)
	var p PasskeyPromptReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &p) != nil || p.User == nil || p.UserVerification != "required" {
		t.Fatalf("the add prompt: %d %s", rec.Code, rec.Body)
	}
	if user, _ := unb64(p.User.ID); string(user) != g.admin.ID {
		t.Errorf("the prompt's user is %q", user)
	}
	rec = g.do("POST", api.AdminPrefix+"passkeys", "", token, AddPasskeyBody{
		Name: "Phone", RawID: b64(d.id), ClientDataJSON: b64(d.clientData("webauthn.create", p.Challenge)),
		AuthenticatorData: b64(d.authData(true)), PublicKey: b64(d.spki),
	})
	var pk Passkey
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &pk) != nil || pk.Name != "Phone" {
		t.Fatalf("adding the passkey: %d %s", rec.Code, rec.Body)
	}

	// Sign in with it.
	rec = g.do("POST", api.AdminPrefix+"session/passkey", "", "", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &p) != nil || len(p.AllowCredentials) != 0 {
		t.Fatalf("the sign-in prompt: %d %s", rec.Code, rec.Body)
	}
	d.count++
	cd, ad := d.clientData("webauthn.get", p.Challenge), d.authData(false)
	hash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), hash[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, d.key, digest[:])
	answer := &PasskeyCredential{ID: b64(d.id), RawID: b64(d.id), Type: "public-key", Response: PasskeyAssertion{
		ClientDataJSON: b64(cd), AuthenticatorData: b64(ad), Signature: b64(sig), UserHandle: b64([]byte(g.admin.ID)),
	}}
	byKey, rec := g.adminSignIn(SignInBody{Passkey: answer})
	if !adminaccount.IsToken(byKey) {
		t.Fatalf("passkey sign-in: %d %s", rec.Code, rec.Body)
	}
	if again, rec := g.adminSignIn(SignInBody{Passkey: answer}); again != "" || rec.Code != http.StatusUnauthorized {
		t.Errorf("the same answer twice: %d", rec.Code)
	}

	// List and remove.
	rec = g.do("GET", api.AdminPrefix+"passkeys", "", byKey, nil)
	var list PasskeysReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Passkeys) != 1 || list.Passkeys[0].LastUsed.IsZero() {
		t.Fatalf("the passkeys: %d %s", rec.Code, rec.Body)
	}
	if rec := g.do("DELETE", api.AdminPrefix+"passkeys/"+pk.ID, "", byKey, nil); rec.Code != http.StatusOK {
		t.Errorf("removing it: %d %s", rec.Code, rec.Body)
	}
	if rec := g.do("DELETE", api.AdminPrefix+"passkeys/"+pk.ID, "", byKey, nil); rec.Code != http.StatusNotFound {
		t.Errorf("removing it twice: %d", rec.Code)
	}
}
