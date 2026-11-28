package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminview"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// peopleRig is the admin rig with the people routes, given a view with the
// switch's hooks.
func peopleRig(t *testing.T) (*rig, string) {
	t.Helper()
	g := newRig(t, false)
	noop := func(context.Context, *tenant.Store) error { return nil }
	view := adminview.NewWith(g.in, adminview.Options{Pause: noop, Rearm: noop, Resume: noop, SetupCodeFor: time.Hour, SampleEvery: time.Hour})
	g.routes = append(g.routes, PeopleRoutes(PeopleOptions{View: view, Log: api.NewLog(nil)})...)
	var err error
	if g.h, err = api.Handler(api.Options{Install: g.in, MaxBodyBytes: 1 << 16, Heartbeat: time.Second, Admin: g.o}, g.routes); err != nil {
		t.Fatal(err)
	}
	token, rec := g.adminSignIn(SignInBody{Account: "Test Admin", Password: passwordAdmin})
	if token == "" {
		t.Fatalf("the admin cannot sign in: %d", rec.Code)
	}
	return g, token
}

// The people routes: add a person with a code, disable and enable them, a
// person's usage and the install, each refused to a person's session, and
// each in both logs.
func TestThePeopleRoutes(t *testing.T) {
	g, admin := peopleRig(t)

	rec := g.do("POST", api.AdminPrefix+"people", "", admin, AddPersonBody{Name: "Test Person Three"})
	var added AddedReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &added) != nil || added.Person.Name != "Test Person Three" || len(added.SetupCode.Code) != 24 {
		t.Fatalf("adding a person: %d %s", rec.Code, rec.Body)
	}
	if rec := g.do("POST", api.AdminPrefix+"people", "", admin, AddPersonBody{Name: ""}); rec.Code != http.StatusBadRequest {
		t.Errorf("a person with no name: %d", rec.Code)
	}

	rec = g.do("POST", api.AdminPrefix+"people/"+g.two+"/disable", "", admin, nil)
	var changed PersonReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &changed) != nil || changed.Person.Enabled {
		t.Fatalf("disabling: %d %s", rec.Code, rec.Body)
	}
	// The disabled person's sign-in says so; a wrong password does not.
	if rec := g.do("POST", api.Prefix+"session", "", "", api.SignInBody{Tenant: g.two, Password: passwordTwo}); rec.Code != http.StatusForbidden {
		t.Errorf("a disabled person's sign-in: %d %s", rec.Code, rec.Body)
	}
	if rec := g.do("POST", api.Prefix+"session", "", "", api.SignInBody{Tenant: g.two, Password: "not the phrase at all"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("a wrong password for a disabled person: %d", rec.Code)
	}
	if rec := g.do("POST", api.AdminPrefix+"people/"+g.two+"/setup-code", "", admin, nil); rec.Code != http.StatusConflict {
		t.Errorf("a setup code for a disabled person: %d", rec.Code)
	}
	if rec := g.do("POST", api.AdminPrefix+"people/"+g.two+"/enable", "", admin, nil); rec.Code != http.StatusOK {
		t.Errorf("enabling: %d %s", rec.Code, rec.Body)
	}
	if g.personSignIn(g.two, passwordTwo) == "" {
		t.Error("an enabled person cannot sign in")
	}

	rec = g.do("GET", api.AdminPrefix+"people/"+g.one+"/usage?from=2026-11-01&to=2026-11-28", "", admin, nil)
	var use adminview.PersonUsage
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &use) != nil || use.ID != g.one || use.Period.From != "2026-11-01" {
		t.Errorf("a person's usage: %d %s", rec.Code, rec.Body)
	}
	if rec := g.do("GET", api.AdminPrefix+"people/"+g.one+"/usage?from=2026-11-28&to=2026-11-01", "", admin, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("a period that ends before it starts: %d", rec.Code)
	}
	if rec := g.do("GET", api.AdminPrefix+"people/0123456789abcdef/usage", "", admin, nil); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown person's usage: %d", rec.Code)
	}
	rec = g.do("GET", api.AdminPrefix+"install", "", admin, nil)
	var inst adminview.Install
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &inst) != nil || inst.People.Total != 3 {
		t.Errorf("the install: %d %s", rec.Code, rec.Body)
	}

	// A person's session is refused on every one of them.
	person := g.personSignIn(g.one, passwordOne)
	for _, p := range []struct{ method, path string }{
		{"POST", "people"}, {"GET", "people/" + g.two}, {"POST", "people/" + g.two + "/setup-code"}, {"POST", "people/" + g.two + "/disable"},
		{"POST", "people/" + g.two + "/enable"}, {"GET", "people/" + g.two + "/usage"}, {"GET", "install"},
	} {
		if rec := g.do(p.method, api.AdminPrefix+p.path, person, "", map[string]string{"name": "Intruder"}); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with a person's session: %d", p.method, p.path, rec.Code)
		}
	}
	if err := api.Check(g.routes); err != nil {
		t.Errorf("the routes: %v", err)
	}
}
