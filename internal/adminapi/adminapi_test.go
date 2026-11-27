package adminapi

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminview"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name and password here is invented.
const (
	passwordAdmin = "test admin's own phrase"
	passwordTwo   = "test person two's phrase"
)

type rig struct {
	t            *testing.T
	in           *tenant.Install
	h            http.Handler
	o            signin.Options
	admin, other string
}

func newRig(t *testing.T, passkeys bool) *rig {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := &rig{t: t, in: in, o: signin.Options{
		PasswordIterations: 50, PasswordMinLength: 12, SessionLifetime: time.Hour, FailuresBeforeWait: 3, FailureWait: time.Minute,
		Passkeys: passkeys, PasskeyRPID: "localhost", PasskeyOrigin: "http://localhost:7900", PasskeyChallengeLifetime: time.Minute,
	}}
	for _, p := range []struct {
		id       *string
		name, pw string
	}{{&g.admin, "Test Admin", passwordAdmin}, {&g.other, "Test Person Two", passwordTwo}} {
		made, err := in.Create(p.name)
		if err != nil {
			t.Fatal(err)
		}
		*p.id = made.ID
		g.with(made.ID, func(s *tenant.Store) {
			if err := signin.SetPassword(s, p.pw, g.o); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := in.SetAdmin(g.admin, true); err != nil {
		t.Fatal(err)
	}
	var routes []api.Route
	routes = append(api.ShellRoutes(api.Shell{Install: in, SignIn: g.o, PageLimit: 50, Version: "test", Routes: func() []api.Route { return routes }}),
		Routes(Options{View: adminview.New(in), SignIn: g.o, StepUpFor: 15 * time.Minute})...)
	if g.h, err = api.Handler(api.Options{Install: in, MaxBodyBytes: 1 << 16, Heartbeat: time.Second}, routes); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *rig) with(id string, f func(*tenant.Store)) {
	g.t.Helper()
	s, err := g.in.Store(id)
	if err != nil {
		g.t.Fatal(err)
	}
	defer s.Close()
	f(s)
}

func (g *rig) do(method, path, token string, body any) *httptest.ResponseRecorder {
	g.t.Helper()
	var r *http.Request
	if body != nil {
		raw, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.AddCookie(&http.Cookie{Name: api.CookieName, Value: token})
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r)
	return rec
}

func (g *rig) signIn(id, password string) string {
	g.t.Helper()
	rec := g.do("POST", api.Prefix+"session", "", api.SignInBody{Tenant: id, Password: password})
	for _, c := range rec.Result().Cookies() {
		if c.Name == api.CookieName && rec.Code == http.StatusOK {
			return c.Value
		}
	}
	g.t.Fatalf("sign-in answered %d", rec.Code)
	return ""
}

func (g *rig) log(id string) []audit.Entry {
	g.t.Helper()
	var out []audit.Entry
	g.with(id, func(s *tenant.Store) {
		var err error
		if out, err = audit.List(context.Background(), s, audit.Filter{}); err != nil {
			g.t.Fatal(err)
		}
	})
	return out
}

func code(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e api.ErrorBody
	json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Code
}

func TestStepUpThenPeople(t *testing.T) {
	g := newRig(t, false)
	token := g.signIn(g.admin, passwordAdmin)
	g.signIn(g.other, passwordTwo)

	if rec := g.do("GET", api.AdminPrefix+"people", token, nil); rec.Code != http.StatusUnauthorized || code(t, rec) != "step_up" {
		t.Fatalf("people before a step-up: %d %s", rec.Code, rec.Body.String())
	}
	for what, body := range map[string]any{
		"nothing":  StepUpBody{},
		"both":     StepUpBody{Password: passwordAdmin, Passkey: &PasskeyCredential{}},
		"not JSON": "a string",
	} {
		if rec := g.do("POST", api.AdminPrefix+"step-up", token, body); rec.Code != http.StatusBadRequest {
			t.Errorf("a step-up with %s: %d %s", what, rec.Code, rec.Body.String())
		}
	}
	rec := g.do("POST", api.AdminPrefix+"step-up", token, StepUpBody{Password: passwordTwo})
	if rec.Code != http.StatusForbidden || code(t, rec) != "refused" {
		t.Errorf("a step-up with another person's password: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), passwordTwo) {
		t.Error("the answer holds the password")
	}
	if rec := g.do("POST", api.AdminPrefix+"step-up/passkey", token, nil); rec.Code != http.StatusBadRequest || code(t, rec) != "passkeys_off" {
		t.Errorf("a passkey prompt with passkeys off: %d %s", rec.Code, rec.Body.String())
	}

	rec = g.do("POST", api.AdminPrefix+"step-up", token, StepUpBody{Password: passwordAdmin})
	var up StepUpReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &up) != nil {
		t.Fatalf("the step-up: %d %s", rec.Code, rec.Body.String())
	}
	if d := time.Until(up.AdminUntil); d <= 14*time.Minute || d > 15*time.Minute {
		t.Errorf("the step-up lasts %s, want 15m", d)
	}

	before := len(g.log(g.other))
	rec = g.do("GET", api.AdminPrefix+"people", token, nil)
	var reply PeopleReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reply) != nil || len(reply.People) != 2 {
		t.Fatalf("people: %d %s", rec.Code, rec.Body.String())
	}
	if p := reply.People[1]; p.ID != g.other || p.Admin || p.LastSignIn.IsZero() || p.Name != "Test Person Two" {
		t.Errorf("the other person is %+v", p)
	}
	id := rec.Header().Get("X-Request-Id")
	// Both logs: the admin's, as admin, and the other person's, as admin:<id>.
	var mine int
	for _, e := range g.log(g.admin) {
		if e.RequestID == id {
			mine++
			if e.Actor != api.ActorAdmin || e.Action != "GET "+api.AdminPrefix+"people" {
				t.Errorf("the admin's entry is %+v", e)
			}
		}
	}
	theirs := g.log(g.other)[before:]
	if mine != 2 || len(theirs) != 2 {
		t.Fatalf("the call left %d entries in the admin's log and %d in the other person's", mine, len(theirs))
	}
	for _, e := range theirs {
		if e.RequestID != id || e.Actor != adminview.ActorPrefix+g.admin {
			t.Errorf("the other person's entry is %+v", e)
		}
	}

	// Closed by hand, the admin area asks again.
	if rec := g.do("DELETE", api.AdminPrefix+"step-up", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("closing the admin area: %d %s", rec.Code, rec.Body.String())
	}
	if rec := g.do("GET", api.AdminPrefix+"people", token, nil); rec.Code != http.StatusUnauthorized || code(t, rec) != "step_up" {
		t.Errorf("people after closing the admin area: %d %s", rec.Code, rec.Body.String())
	}

	// The other person is refused every admin route.
	other := g.signIn(g.other, passwordTwo)
	for _, rt := range Routes(Options{}) {
		var body any
		if rt.Method == "POST" {
			body = StepUpBody{Password: passwordTwo}
		}
		if rec := g.do(rt.Method, rt.Path, other, body); rec.Code != http.StatusForbidden || code(t, rec) != "not_admin" {
			t.Errorf("%s %s for a person without the mark: %d %s", rt.Method, rt.Path, rec.Code, rec.Body.String())
		}
	}
}

func TestWrongStepUpsWait(t *testing.T) {
	g := newRig(t, false)
	token := g.signIn(g.admin, passwordAdmin)
	for i := 0; i < g.o.FailuresBeforeWait; i++ {
		g.do("POST", api.AdminPrefix+"step-up", token, StepUpBody{Password: "not the phrase at all"})
	}
	rec := g.do("POST", api.AdminPrefix+"step-up", token, StepUpBody{Password: passwordAdmin})
	if rec.Code != http.StatusTooManyRequests || code(t, rec) != "wait" || rec.Header().Get("Retry-After") == "" {
		t.Errorf("a step-up during the wait: %d %s", rec.Code, rec.Body.String())
	}
}

// device is a passkey made for the test, ES256, which builds the bytes a
// browser sends from the WebAuthn rules, as signin's own tests do.
type device struct {
	id  []byte
	key *ecdsa.PrivateKey
}

func newDevice(t *testing.T) *device {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	d := &device{id: make([]byte, 20), key: key}
	rand.Read(d.id)
	return d
}

func (d *device) authData(attested bool) []byte {
	site := sha256.Sum256([]byte("localhost"))
	out := append([]byte{}, site[:]...)
	flags := byte(0x01 | 0x04)
	if attested {
		flags |= 0x40
	}
	out = append(out, flags)
	out = binary.BigEndian.AppendUint32(out, 0)
	if attested {
		out = append(out, make([]byte, 16)...)
		out = binary.BigEndian.AppendUint16(out, uint16(len(d.id)))
		out = append(out, d.id...)
		out = append(out, 0xa0)
	}
	return out
}

func clientData(typ string, challenge []byte) []byte {
	raw, _ := json.Marshal(map[string]any{"type": typ, "challenge": base64.RawURLEncoding.EncodeToString(challenge), "origin": "http://localhost:7900", "crossOrigin": false})
	return raw
}

func TestPasskeyStepUp(t *testing.T) {
	g := newRig(t, true)
	d := newDevice(t)
	g.with(g.admin, func(s *tenant.Store) {
		p, err := signin.BeginAddPasskey(s, g.o)
		if err != nil {
			t.Fatal(err)
		}
		spki, _ := x509.MarshalPKIXPublicKey(&d.key.PublicKey)
		if _, err := signin.AddPasskey(s, "Test laptop", signin.PasskeyCreation{
			CredentialID: d.id, ClientDataJSON: clientData("webauthn.create", p.Challenge), AuthenticatorData: d.authData(true), PublicKey: spki,
		}, g.o); err != nil {
			t.Fatal(err)
		}
	})
	token := g.signIn(g.admin, passwordAdmin)

	rec := g.do("POST", api.AdminPrefix+"step-up/passkey", token, nil)
	var prompt PasskeyPromptReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &prompt) != nil {
		t.Fatalf("the passkey prompt: %d %s", rec.Code, rec.Body.String())
	}
	if prompt.RPID != "localhost" || prompt.UserVerification != "required" || len(prompt.AllowCredentials) != 1 ||
		prompt.AllowCredentials[0].ID != base64.RawURLEncoding.EncodeToString(d.id) || prompt.Timeout <= 0 {
		t.Errorf("the prompt is %+v", prompt)
	}
	challenge, err := base64.RawURLEncoding.DecodeString(prompt.Challenge)
	if err != nil {
		t.Fatal(err)
	}
	cd, ad := clientData("webauthn.get", challenge), d.authData(false)
	hash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), hash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, d.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	b := base64.RawURLEncoding.EncodeToString
	answer := &PasskeyCredential{ID: b(d.id), RawID: b(d.id), Type: "public-key", Response: PasskeyAssertion{
		ClientDataJSON: b(cd), AuthenticatorData: b(ad), Signature: b(sig), UserHandle: b([]byte(g.admin)),
	}}
	if rec := g.do("POST", api.AdminPrefix+"step-up", token, StepUpBody{Passkey: answer}); rec.Code != http.StatusOK {
		t.Fatalf("the passkey step-up: %d %s", rec.Code, rec.Body.String())
	}
	if rec := g.do("GET", api.AdminPrefix+"people", token, nil); rec.Code != http.StatusOK {
		t.Errorf("people after a passkey step-up: %d", rec.Code)
	}
	// The same answer again is refused.
	if rec := g.do("POST", api.AdminPrefix+"step-up", token, StepUpBody{Passkey: answer}); rec.Code != http.StatusForbidden {
		t.Errorf("a passkey answer used twice: %d", rec.Code)
	}
	bad := *answer
	bad.Response.Signature = "not base64url!"
	if rec := g.do("POST", api.AdminPrefix+"step-up", token, StepUpBody{Passkey: &bad}); rec.Code != http.StatusBadRequest {
		t.Errorf("a passkey answer that is not base64url: %d", rec.Code)
	}
}
