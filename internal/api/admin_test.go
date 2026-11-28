package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/audit"
)

// An invented admin password, used in a temporary folder only.
const adminPassword = "test admin's own phrase"

func adminOptions() adminaccount.Options {
	return adminaccount.Options{SignIn: testOptions(), SessionFor: time.Hour, IdleFor: 10 * time.Minute}
}

// adminFixture is the shell's fixture with an admin route that says what its
// handler was given, the person's route to the admin entries of their log,
// and one admin account, signed in.
type adminFixture struct {
	*fixture
	account, token string
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	f := newFixture(t)
	f.routes = append(f.routes,
		Route{Method: "GET", Path: AdminPrefix + "probe", Admin: true, Summary: "Says what the handler was given.",
			Handle: func(w http.ResponseWriter, r *http.Request, c Call) {
				WriteJSON(w, http.StatusOK, map[string]any{"store": c.Store != nil, "admin": c.Admin != nil, "account": c.AdminSession.AccountID, "request_id": c.RequestID})
			}},
		AdminEntriesRoute(5, NewLog(f.log)),
	)
	var err error
	if f.h, err = Handler(Options{Install: f.in, Log: NewLog(f.log), MaxBodyBytes: 1024, Heartbeat: 10 * time.Millisecond, Admin: adminOptions()}, f.routes); err != nil {
		t.Fatal(err)
	}
	db, err := adminaccount.Open(f.in.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	acc, err := db.AddAccount("Test Admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetPassword(acc.ID, adminPassword, testOptions()); err != nil {
		t.Fatal(err)
	}
	token, _, _, err := db.SignIn(acc.ID, adminPassword, adminOptions())
	if err != nil {
		t.Fatal(err)
	}
	return &adminFixture{fixture: f, account: acc.ID, token: token}
}

// call makes one call with the given cookies, either of which may be empty.
func (f *adminFixture) call(method, path, personCookie, adminCookie string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, path, nil)
	if personCookie != "" {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: personCookie})
	}
	if adminCookie != "" {
		r.AddCookie(&http.Cookie{Name: AdminCookieName, Value: adminCookie})
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	return rec
}

func (f *adminFixture) adminLog(requestID string) []audit.Entry {
	f.t.Helper()
	db, err := adminaccount.Open(f.in.Dir())
	if err != nil {
		f.t.Fatal(err)
	}
	defer db.Close()
	got, err := db.Log(context.Background(), audit.Filter{RequestID: requestID})
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func (f *adminFixture) personLog(id, requestID string) []audit.Entry {
	f.t.Helper()
	var out []audit.Entry
	for _, e := range f.entries(id) {
		if e.RequestID == requestID {
			out = append(out, e)
		}
	}
	return out
}

func TestAnAdminRouteRunsForAnAdminAlone(t *testing.T) {
	f := newAdminFixture(t)
	person, _ := f.signIn(f.one, passwordOne)
	if person == "" {
		t.Fatal("the fixture's person cannot sign in")
	}

	// The admin's token, in the admin cookie: the route runs with the
	// admin's session and no store, and the call is in the admin log alone.
	rec := f.call("GET", AdminPrefix+"probe", "", f.token)
	var got map[string]any
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("the admin's call answered %d: %s", rec.Code, rec.Body)
	}
	if got["store"] != false || got["admin"] != true || got["account"] != f.account {
		t.Errorf("the admin route was given %v", got)
	}
	id := rec.Header().Get("X-Request-Id")
	if log := f.adminLog(id); len(log) != 2 || log[0].Actor != adminaccount.ActorPrefix+f.account || log[0].Outcome != audit.OutcomeStarted || log[1].Status != http.StatusOK {
		t.Errorf("the admin's call is in the admin log as %+v", log)
	}
	for _, p := range []string{f.one, f.two} {
		if n := len(f.personLog(p, id)); n != 0 {
			t.Errorf("the admin's call left %d entries in a person's log", n)
		}
	}
	if !strings.Contains(f.log.String(), id+" GET "+AdminPrefix+"probe 200") || !strings.Contains(f.log.String(), "admin="+f.account) {
		t.Errorf("the server's log does not name the admin's call:\n%s", f.log)
	}

	// Signed in as both in one browser, each cookie is read on its own side.
	if rec := f.call("GET", AdminPrefix+"probe", person, f.token); rec.Code != http.StatusOK {
		t.Errorf("an admin route with both cookies answered %d", rec.Code)
	}
	if rec := f.call("GET", Prefix+"probe/x", person, f.token); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), f.one) {
		t.Errorf("a person's route with both cookies answered %d: %s", rec.Code, rec.Body)
	}
}

func TestAPersonsTokenIsRefusedOnEveryAdminPath(t *testing.T) {
	f := newAdminFixture(t)
	person, _ := f.signIn(f.one, passwordOne)
	for _, path := range []string{AdminPrefix + "probe", AdminPrefix + "no/such/route", strings.TrimSuffix(AdminPrefix, "/")} {
		// In the person's cookie: refused on the person's side and recorded
		// in their own log, and in no other.
		rec := f.call("GET", path, person, "")
		if rec.Code != http.StatusForbidden || errorOf(t, rec).Code != "not_admin" {
			t.Errorf("%s with a person's session answered %d %s", path, rec.Code, rec.Body)
		}
		id := rec.Header().Get("X-Request-Id")
		if log := f.personLog(f.one, id); len(log) != 2 || log[0].Actor != ActorPerson || log[1].Outcome != audit.OutcomeRefused {
			t.Errorf("%s, refused to a person, is in their log as %+v", path, log)
		}
		if n := len(f.adminLog(id)) + len(f.personLog(f.two, id)); n != 0 {
			t.Errorf("%s, refused to a person, left %d entries in another log", path, n)
		}
		// In the admin cookie: not an admin's token, so no session at all.
		rec = f.call("GET", path, "", person)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with a person's token in the admin cookie answered %d", path, rec.Code)
		}
		if n := len(f.adminLog(rec.Header().Get("X-Request-Id"))); n != 0 {
			t.Errorf("%s with a person's token in the admin cookie wrote %d admin entries", path, n)
		}
	}
}

func TestAnAdminsTokenIsRefusedOnEveryPersonsRoute(t *testing.T) {
	f := newAdminFixture(t)
	before := len(f.entries(f.one)) + len(f.entries(f.two))
	for _, rt := range f.routes {
		if rt.Admin || rt.Door {
			continue
		}
		for where, cookies := range map[string][2]string{"the person's cookie": {f.token, ""}, "the admin cookie": {"", f.token}} {
			rec := f.call(rt.Method, url(rt), cookies[0], cookies[1])
			if rec.Code != http.StatusForbidden || errorOf(t, rec).Code != "admin_account" {
				t.Errorf("%s %s with the admin's token in %s answered %d %s", rt.Method, rt.Path, where, rec.Code, rec.Body)
				continue
			}
			if log := f.adminLog(rec.Header().Get("X-Request-Id")); len(log) != 2 || log[1].Outcome != audit.OutcomeRefused || log[1].Status != http.StatusForbidden {
				t.Errorf("%s %s, refused to the admin, is in the admin log as %+v", rt.Method, rt.Path, log)
			}
		}
	}
	if after := len(f.entries(f.one)) + len(f.entries(f.two)); after != before {
		t.Errorf("an admin's calls to person's routes wrote %d entries in people's logs", after-before)
	}
}

func TestAnAdminSessionThatIsNotThere(t *testing.T) {
	f := newAdminFixture(t)
	for what, token := range map[string]string{
		"a token of the admin form that is no session": "ksa_" + strings.Repeat("0", 64),
		"something else": "ksa_nothing",
		"no token":       "",
	} {
		rec := f.call("GET", AdminPrefix+"probe", "", token)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s answered %d", what, rec.Code)
		}
		if n := len(f.adminLog(rec.Header().Get("X-Request-Id"))); n != 0 {
			t.Errorf("%s wrote %d admin entries", what, n)
		}
	}
	// A session signed out from another process is refused at its next call.
	db, err := adminaccount.Open(f.in.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SignOutAll(f.account); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if rec := f.call("GET", AdminPrefix+"probe", "", f.token); rec.Code != http.StatusUnauthorized {
		t.Errorf("a signed-out admin session answered %d", rec.Code)
	}
}

func TestCheckHoldsTheAdminPrefix(t *testing.T) {
	handle := func(http.ResponseWriter, *http.Request, Call) {}
	open := func(http.ResponseWriter, *http.Request) {}
	for _, bad := range []Route{
		{Method: "GET", Path: AdminPrefix + "x", Summary: "s", Handle: handle},
		{Method: "GET", Path: Prefix + "x", Summary: "s", Handle: handle, Admin: true},
	} {
		if err := Check([]Route{bad}); err == nil {
			t.Errorf("%s with Admin %v was taken", bad.Path, bad.Admin)
		}
	}
	if err := Check([]Route{{Method: "POST", Path: AdminPrefix + "session", Summary: "s", Open: open, Door: true, Admin: true}}); err != nil {
		t.Errorf("an admin door was refused: %v", err)
	}
	in := newFixture(t).in
	if _, err := Handler(Options{Install: in, MaxBodyBytes: 1, Heartbeat: time.Second}, []Route{{Method: "GET", Path: AdminPrefix + "x", Summary: "s", Handle: handle, Admin: true}}); err == nil {
		t.Error("a handler with admin routes and no admin session limits was made")
	}
}

func TestTheAdminCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	SetAdminCookie(rec, httptest.NewRequest("POST", AdminPrefix+"session", nil), "ksa_x", time.Now().Add(time.Hour), false)
	c := rec.Result().Cookies()[0]
	if c.Name != AdminCookieName || c.Path != AdminPrefix || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("the admin cookie is %+v", c)
	}
}

func TestAPersonSeesTheAdminEntriesOfTheirOwnLog(t *testing.T) {
	f := newAdminFixture(t)
	person, _ := f.signIn(f.one, passwordOne)
	s, err := f.in.Store(f.one)
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{adminaccount.ActorPrefix + f.account, "person", adminaccount.ActorPrefix + f.account} {
		if _, err := audit.Append(context.Background(), s, audit.Entry{Kind: audit.KindAPI, RequestID: "invented", Actor: actor, Action: "GET " + AdminPrefix + "people", Outcome: audit.OutcomeOK}); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	rec := f.call("GET", Prefix+"audit/admin", person, "")
	var reply AuditReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reply) != nil {
		t.Fatalf("the route answered %d: %s", rec.Code, rec.Body)
	}
	if len(reply.Entries) != 2 {
		t.Fatalf("the person sees %d admin entries, want 2: %+v", len(reply.Entries), reply.Entries)
	}
	for _, e := range reply.Entries {
		if !strings.HasPrefix(e.Actor, adminaccount.ActorPrefix) {
			t.Errorf("an entry of actor %q is listed", e.Actor)
		}
	}
	// Another person sees none of them.
	two, _ := f.signIn(f.two, passwordTwo)
	rec = f.call("GET", Prefix+"audit/admin", two, "")
	if json.Unmarshal(rec.Body.Bytes(), &reply) != nil || len(reply.Entries) != 0 {
		t.Errorf("another person sees %s", rec.Body)
	}
}
