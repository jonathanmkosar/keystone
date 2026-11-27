package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/signin"
)

// adminFixture is the shell's fixture with two admin routes: one that says
// what its handler was given, and a step-up route that opens the admin area
// with the password.
func adminFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	o := testOptions()
	f.routes = append(f.routes,
		Route{Method: "GET", Path: AdminPrefix + "probe", Admin: true, Summary: "Says what the handler was given.",
			Handle: func(w http.ResponseWriter, r *http.Request, c Call) {
				WriteJSON(w, http.StatusOK, map[string]any{"tenant": c.Store.TenantID(), "admin": c.Admin})
			}},
		Route{Method: "POST", Path: AdminPrefix + "grant", Admin: true, StepUp: true, Summary: "Opens the admin area with the password.",
			Handle: func(w http.ResponseWriter, r *http.Request, c Call) {
				var body struct {
					Password string `json:"password"`
				}
				if !ReadJSON(w, r, &body) {
					return
				}
				until, err := signin.StepUp(c.Store, c.Session.ID, body.Password, o, time.Minute)
				if err != nil {
					Error(w, r, http.StatusForbidden, "refused", "refused")
					return
				}
				WriteJSON(w, http.StatusOK, map[string]any{"admin_until": until, "admin": c.Admin})
			}},
	)
	var err error
	if f.h, err = Handler(Options{Install: f.in, Log: NewLog(f.log), MaxBodyBytes: 1024, Heartbeat: 10 * time.Millisecond}, f.routes); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTheAdminDoor(t *testing.T) {
	f := adminFixture(t)
	if _, err := f.in.SetAdmin(f.one, true); err != nil {
		t.Fatal(err)
	}
	admin, _ := f.signIn(f.one, passwordOne)
	person, _ := f.signIn(f.two, passwordTwo)
	if admin == "" || person == "" {
		t.Fatal("the fixture's people cannot sign in")
	}
	grant := `{"password":"` + passwordOne + `"}`
	adminPaths := []struct{ method, path, body string }{
		{"GET", AdminPrefix + "probe", ""},
		{"POST", AdminPrefix + "grant", `{"password":"` + passwordTwo + `"}`},
		{"GET", AdminPrefix + "no/such/route", ""},
		{"GET", strings.TrimSuffix(AdminPrefix, "/"), ""},
	}

	// A person without the mark is refused everywhere under the prefix,
	// the step-up route and paths that are no route included, and each
	// refusal is in their own log with the actor person.
	before := len(f.entries(f.two))
	for _, c := range adminPaths {
		rec := f.do(c.method, c.path, person, c.body)
		if rec.Code != http.StatusForbidden || errorOf(t, rec).Code != "not_admin" {
			t.Errorf("%s %s for a person without the mark: %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	got := f.entries(f.two)[before:]
	if len(got) != 2*len(adminPaths) {
		t.Fatalf("the refusals left %d entries in the person's log, want %d", len(got), 2*len(adminPaths))
	}
	for _, e := range got {
		if e.Actor != ActorPerson || (e.Outcome != audit.OutcomeStarted && e.Status != http.StatusForbidden) {
			t.Errorf("a refusal is recorded as %+v", e)
		}
	}

	// An admin without a step-up is refused every admin route but the
	// step-up route, with the actor admin in their log.
	for _, c := range []struct{ method, path string }{{"GET", AdminPrefix + "probe"}, {"GET", AdminPrefix + "no/such/route"}} {
		rec := f.do(c.method, c.path, admin, "")
		if rec.Code != http.StatusUnauthorized || errorOf(t, rec).Code != "step_up" {
			t.Errorf("%s %s with no step-up: %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	entries := f.entries(f.one)
	if last := entries[len(entries)-1]; last.Actor != ActorAdmin || last.Status != http.StatusUnauthorized {
		t.Errorf("a refused admin call is recorded as %+v", last)
	}
	// A wrong password at the step-up route reaches the route, which refuses it.
	if rec := f.do("POST", AdminPrefix+"grant", admin, `{"password":"not the phrase at all"}`); rec.Code != http.StatusForbidden || errorOf(t, rec).Code != "refused" {
		t.Errorf("a wrong step-up: %d %s", rec.Code, rec.Body.String())
	}
	rec := f.do("POST", AdminPrefix+"grant", admin, grant)
	if rec.Code != http.StatusOK {
		t.Fatalf("the step-up: %d %s", rec.Code, rec.Body.String())
	}

	// With the step-up the admin route runs, told it came through the door.
	rec = f.do("GET", AdminPrefix+"probe", admin, "")
	var probe struct {
		Tenant string `json:"tenant"`
		Admin  bool   `json:"admin"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &probe) != nil || probe.Tenant != f.one || !probe.Admin {
		t.Fatalf("the admin route after a step-up: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do("GET", AdminPrefix+"no/such/route", admin, ""); rec.Code != http.StatusNotFound {
		t.Errorf("an admin path that is no route, after a step-up: %d", rec.Code)
	}
	// A route outside the prefix is the admin's own, as a person: Admin is
	// false and the actor is person.
	rec = f.do("GET", Prefix+"probe/x", admin, "")
	entries = f.entries(f.one)
	if rec.Code != http.StatusOK || entries[len(entries)-1].Actor != ActorPerson {
		t.Errorf("an ordinary route called by an admin is recorded as %+v", entries[len(entries)-1])
	}
	// The session says so.
	var me SessionReply
	if rec := f.do("GET", Prefix+"session", admin, ""); json.Unmarshal(rec.Body.Bytes(), &me) != nil || !me.Admin || me.Session.AdminUntil.IsZero() {
		t.Errorf("the admin's session reply: %s", rec.Body.String())
	}
	var them SessionReply
	if rec := f.do("GET", Prefix+"session", person, ""); json.Unmarshal(rec.Body.Bytes(), &them) != nil || them.Admin || !them.Session.AdminUntil.IsZero() {
		t.Errorf("the person's session reply: %s", rec.Body.String())
	}

	// The mark taken away shuts the door at the next call, step-up or not.
	// (internal/tenant's tests take it away from another process.)
	if _, err := f.in.SetAdmin(f.one, false); err != nil {
		t.Fatal(err)
	}
	if rec := f.do("GET", AdminPrefix+"probe", admin, ""); rec.Code != http.StatusForbidden || errorOf(t, rec).Code != "not_admin" {
		t.Errorf("the admin route after the mark was taken away: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCheckHoldsTheAdminPrefixAndTheMarkTogether(t *testing.T) {
	ok := func(http.ResponseWriter, *http.Request, Call) {}
	for _, c := range []struct {
		what string
		rt   Route
	}{
		{"an admin path not marked", Route{Method: "GET", Path: AdminPrefix + "people", Summary: "x", Handle: ok}},
		{"a mark outside the prefix", Route{Method: "GET", Path: Prefix + "people", Summary: "x", Handle: ok, Admin: true}},
		{"a step-up route not marked admin", Route{Method: "GET", Path: Prefix + "people", Summary: "x", Handle: ok, StepUp: true}},
		{"an admin door", Route{Method: "GET", Path: AdminPrefix + "people", Summary: "x", Door: true, Admin: true, Open: func(http.ResponseWriter, *http.Request) {}}},
	} {
		if err := Check([]Route{c.rt}); err == nil {
			t.Errorf("Check took %s", c.what)
		}
	}
	if err := Check([]Route{{Method: "GET", Path: AdminPrefix + "people", Summary: "x", Handle: ok, Admin: true}}); err != nil {
		t.Errorf("Check refused an admin route: %v", err)
	}
}
