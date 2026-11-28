package setupapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name and password here is invented.
const chosen = "a phrase the person chose"

var rules = signin.Options{PasswordIterations: 50, PasswordMinLength: 12, SessionLifetime: time.Hour, FailuresBeforeWait: 3, FailureWait: time.Minute}

type rig struct {
	t   *testing.T
	in  *tenant.Install
	h   http.Handler
	now time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	g := &rig{t: t, in: in, now: time.Now()}
	routes := append(api.ShellRoutes(api.Shell{Install: in, SignIn: rules, PageLimit: 50, Version: "test"}),
		Routes(Options{Install: in, SignIn: rules, Log: api.NewLog(nil), Now: func() time.Time { return g.now }})...)
	if g.h, err = api.Handler(api.Options{Install: in, MaxBodyBytes: 1 << 16, Heartbeat: time.Second}, routes); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *rig) person(name string) tenant.Tenant {
	g.t.Helper()
	p, err := g.in.Create(name)
	if err != nil {
		g.t.Fatal(err)
	}
	return p
}

func (g *rig) code(id string, ttl time.Duration) string {
	g.t.Helper()
	db, err := adminaccount.Open(g.in.Dir())
	if err != nil {
		g.t.Fatal(err)
	}
	defer db.Close()
	code, _, err := db.IssueSetupCode(id, "0123456789abcdef", ttl, g.now)
	if err != nil {
		g.t.Fatal(err)
	}
	return code
}

func (g *rig) setUp(code, password string) *httptest.ResponseRecorder {
	g.t.Helper()
	raw, _ := json.Marshal(Body{Code: code, Password: password})
	r := httptest.NewRequest("POST", Path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r)
	return rec
}

func errCode(rec *httptest.ResponseRecorder) string {
	var e struct {
		Code string `json:"code"`
	}
	json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Code
}

// A setup code works once, for its person, and not after it expires; the
// person's password is theirs, and the call is in their own log without the
// code or the password.
func TestASetupCodeSetsThePasswordOnce(t *testing.T) {
	g := newRig(t)
	p := g.person("Setup Person One")
	other := g.person("Setup Person Two")
	code := g.code(p.ID, time.Hour)

	// A password the rules refuse leaves the code unused.
	if rec := g.setUp(code, "short"); rec.Code != http.StatusBadRequest || errCode(rec) != "bad_password" {
		t.Errorf("a short password: %d %s", rec.Code, rec.Body)
	}
	rec := g.setUp(strings.ToLower(strings.ReplaceAll(code, "-", " ")), chosen)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
	var reply api.SessionReply
	if json.Unmarshal(rec.Body.Bytes(), &reply) != nil || reply.Person.ID != p.ID || reply.Person.Name != "Setup Person One" {
		t.Errorf("setup answered %s", rec.Body)
	}
	cookie := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == api.CookieName {
			cookie = c.Value
		}
	}
	s, err := g.in.Store(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := signin.Check(s, cookie); err != nil {
		t.Errorf("the session setup opened: %v", err)
	}
	if _, _, err := signin.SignIn(s, chosen, rules); err != nil {
		t.Errorf("the chosen password: %v", err)
	}
	// Once only.
	if rec := g.setUp(code, chosen+" again"); rec.Code != http.StatusUnauthorized || errCode(rec) != "refused" {
		t.Errorf("the code a second time: %d %s", rec.Code, rec.Body)
	}
	// In the person's own log, with neither the code nor the password.
	entries, err := audit.List(context.Background(), s, audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	setups := 0
	for _, e := range entries {
		if e.Action == "POST "+Path {
			setups++
		}
		all := e.Sent + e.Received
		if strings.Contains(all, chosen) || strings.Contains(all, adminaccount.NormalizeCode(code)) || strings.Contains(all, code) {
			t.Errorf("the log holds the code or the password: %+v", e)
		}
	}
	if setups != 2 { // the refused password and the setup
		t.Errorf("%d setup entries in the person's log, want 2", setups)
	}

	// Expired.
	late := g.code(other.ID, time.Minute)
	g.now = g.now.Add(2 * time.Minute)
	if rec := g.setUp(late, chosen); rec.Code != http.StatusUnauthorized {
		t.Errorf("an expired code: %d %s", rec.Code, rec.Body)
	}
	// Typed wrong, and empty.
	if rec := g.setUp("ABCD-EFGH-JKLM-NPQR-STUV", chosen); rec.Code != http.StatusUnauthorized {
		t.Errorf("a code Keystone never made: %d", rec.Code)
	}
	if rec := g.setUp("", chosen); rec.Code != http.StatusUnauthorized {
		t.Errorf("no code: %d", rec.Code)
	}
}

// A disabled person's code is refused, and stays unused until they are
// enabled.
func TestADisabledPersonCannotSetUp(t *testing.T) {
	g := newRig(t)
	p := g.person("Setup Person Three")
	code := g.code(p.ID, time.Hour)
	s, err := g.in.Store(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := signin.Disable(s, time.Now()); err != nil {
		t.Fatal(err)
	}
	if rec := g.setUp(code, chosen); rec.Code != http.StatusForbidden || errCode(rec) != "disabled" {
		t.Errorf("a disabled person's setup: %d %s", rec.Code, rec.Body)
	}
	if err := signin.Enable(s); err != nil {
		t.Fatal(err)
	}
	if rec := g.setUp(code, chosen); rec.Code != http.StatusOK {
		t.Errorf("after enabling, the same code: %d %s", rec.Code, rec.Body)
	}
}
