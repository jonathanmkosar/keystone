package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/adminapi"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/installsettings"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/settings"
)

// The admin's invented password, for an account made in a throwaway folder.
const adminSettingsPassword = "an invented admin phrase for settings"

// adminRig is the real handler on a throwaway folder with the test tenant
// and one admin account, each signed in.
type adminRig struct {
	t              *testing.T
	h              http.Handler
	in             *tenant.Install
	person         string
	personCookie   *http.Cookie
	adminCookie    *http.Cookie
	personPassword string
}

func newAdminRig(t *testing.T) *adminRig {
	t.Helper()
	t.Cleanup(func() { settings.UseInstalled(nil) })
	dir := t.TempDir()
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	fx, err := testtenant.Shipped()
	if err != nil {
		t.Fatal(err)
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		t.Fatal(err)
	}
	res, err := testtenant.Load(in, fx, o)
	if err != nil {
		t.Fatal(err)
	}
	db, err := adminaccount.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	acc, err := db.AddAccount("Test Admin")
	if err == nil {
		err = db.SetPassword(acc.ID, adminSettingsPassword, o.SignIn)
	}
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := installsettings.Load(in); err != nil {
		t.Fatal(err)
	}
	c, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	c.Install = in
	h, stop, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	g := &adminRig{t: t, h: h, in: in, person: res.Tenant.ID, personPassword: fx.Password}
	g.personCookie = g.cookie("POST", api.Prefix+"session", api.SignInBody{Tenant: g.person, Password: fx.Password}, api.CookieName)
	g.adminCookie = g.cookie("POST", api.AdminPrefix+"session", adminapi.SignInBody{Account: "Test Admin", Password: adminSettingsPassword}, api.AdminCookieName)
	return g
}

func (g *adminRig) call(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	g.t.Helper()
	var text string
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			g.t.Fatal(err)
		}
		text = string(b)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(text))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	return rec
}

func (g *adminRig) cookie(method, path string, body any, name string) *http.Cookie {
	g.t.Helper()
	rec := g.call(method, path, body, nil)
	for _, c := range rec.Result().Cookies() {
		if c.Name == name && rec.Code == http.StatusOK {
			return c
		}
	}
	g.t.Fatalf("%s %s answered %d: %s", method, path, rec.Code, rec.Body.String())
	return nil
}

// send makes a chat and sends one message, and returns the turn's stream.
func (g *adminRig) send(text string) string {
	g.t.Helper()
	rec := g.call("POST", api.Prefix+"chats", nil, g.personCookie)
	var made turn.ChatReply
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil {
		g.t.Fatalf("a chat could not be made: %d %s", rec.Code, rec.Body.String())
	}
	rec = g.call("POST", api.Prefix+"chats/"+made.Chat.ID+"/turns", turn.SendBody{Text: text}, g.personCookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "event: "+turn.EventEnd+"\n") {
		g.t.Fatalf("the turn did not stream to its end: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func (g *adminRig) change(path string, body adminapi.ChangeBody) (*httptest.ResponseRecorder, adminapi.ChangedReply) {
	g.t.Helper()
	rec := g.call("PUT", path, body, g.adminCookie)
	var reply adminapi.ChangedReply
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
			g.t.Fatal(err)
		}
	}
	return rec, reply
}

func errorCodeOf(rec *httptest.ResponseRecorder) string {
	var e api.ErrorBody
	json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Code
}

// The proof of P15-03: an admin changes a person's tenant-admin setting from
// the browser, and the person's next turn uses it. Here it is the router's
// policy: the test tenant's recorded ladder and decision model, which ship
// empty (decision 0695), so its turns are on Auto only after the change.
func TestAnAdminsChangeReachesThePersonsNextTurn(t *testing.T) {
	g := newAdminRig(t)
	const easy = "Which day is market day?" // the recorded verdicts send it to the small step
	if stream := g.send(easy); strings.Contains(stream, "event: "+turn.EventRoute+"\n") {
		t.Fatal("the test tenant's turn was routed before any ladder was set, so the test proves nothing")
	}
	people := api.AdminPrefix + "people/" + g.person + "/settings"
	for key, value := range map[string]string{
		router.KeyLadder:        turn.TestSmallModel + ", " + turn.TestMiddleModel + ", " + turn.TestLargeModel,
		router.KeyDecisionModel: turn.TestDecisionModel,
	} {
		rec, reply := g.change(people, adminapi.ChangeBody{Key: key, Value: value})
		if rec.Code != http.StatusOK || reply.Setting.Value != value || reply.Change.New != value || reply.Change.Old != "" {
			t.Fatalf("setting %s answered %d %s", key, rec.Code, rec.Body.String())
		}
	}
	stream := g.send(easy)
	if !strings.Contains(stream, "event: "+turn.EventRoute+"\n") || !strings.Contains(stream, turn.TestSmallModel) {
		t.Errorf("the person's next turn was not routed on the ladder the admin set:\n%s", stream)
	}

	// The read lists the router's policy among the person's tenant-admin
	// settings, from the registry, and none of their own.
	rec := g.call("GET", people, nil, g.adminCookie)
	var list adminapi.PersonSettingsReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil {
		t.Fatalf("the person's settings answered %d", rec.Code)
	}
	seen := map[string]string{}
	for _, s := range list.Settings {
		seen[s.Key] = s.Value
		if s.Scope != string(settings.ScopeTenantAdmin) {
			t.Errorf("the admin was shown %s, a %s setting", s.Key, s.Scope)
		}
	}
	if seen[router.KeyDecisionModel] != turn.TestDecisionModel {
		t.Errorf("the person's settings read back as %v", seen)
	}

	// The admin cannot change a person's own setting, nor a fixed rule.
	for key, value := range map[string]string{"appearance.theme": "dark", "rules.approval": "false"} {
		if rec, _ := g.change(people, adminapi.ChangeBody{Key: key, Value: value}); rec.Code != http.StatusForbidden || errorCodeOf(rec) != "not_allowed" {
			t.Errorf("the admin changing %s answered %d %s", key, rec.Code, rec.Body.String())
		}
	}
	// A person's token is refused on the admin's settings routes.
	if rec := g.call("GET", people, nil, &http.Cookie{Name: api.AdminCookieName, Value: g.personCookie.Value}); rec.Code != http.StatusUnauthorized {
		t.Errorf("a person's token in the admin cookie answered %d", rec.Code)
	}
	if rec := g.call("GET", people, nil, g.personCookie); rec.Code != http.StatusForbidden || errorCodeOf(rec) != "not_admin" {
		t.Errorf("a person's session on the admin's settings answered %d %s", rec.Code, errorCodeOf(rec))
	}
}

// The install's settings: kept in keystone.json, in force at once or after a
// restart, and a person's default reaches the person who has not chosen.
func TestTheInstallsSettingsFromTheBrowser(t *testing.T) {
	g := newAdminRig(t)
	install := api.AdminPrefix + "install/settings"
	rec, reply := g.change(install, adminapi.ChangeBody{Key: "usage.report_max_days", Value: "30"})
	if rec.Code != http.StatusOK || reply.Setting.InForce != "30" || reply.Change.New != "30" || reply.Change.Restart {
		t.Fatalf("an install setting answered %d %s", rec.Code, rec.Body.String())
	}
	rec, reply = g.change(install, adminapi.ChangeBody{Key: "server.page_limit", Value: "40"})
	if rec.Code != http.StatusOK || !reply.Setting.Pending || !reply.Change.Restart || reply.Setting.Value != "40" || reply.Setting.InForce == "40" {
		t.Errorf("a restart setting answered %d %s", rec.Code, rec.Body.String())
	}
	values, err := g.in.InstallValues(tenant.SettingsField)
	if err != nil || values["usage.report_max_days"] != "30" || values["server.page_limit"] != "40" {
		t.Errorf("keystone.json holds %v, %v", values, err)
	}
	// A person's default: the person, who has not chosen, sees it in force.
	if rec, _ := g.change(install, adminapi.ChangeBody{Key: "appearance.theme", Value: "dark"}); rec.Code != http.StatusOK {
		t.Fatalf("a person's default answered %d %s", rec.Code, rec.Body.String())
	}
	rec = g.call("GET", api.Prefix+"settings", nil, g.personCookie)
	if !strings.Contains(rec.Body.String(), `"key":"appearance.theme"`) {
		t.Fatalf("the person's settings answered %d", rec.Code)
	}
	var mine struct {
		Settings []struct {
			Key, Value string
			Overridden bool
		}
	}
	json.Unmarshal(rec.Body.Bytes(), &mine)
	for _, s := range mine.Settings {
		if s.Key == "appearance.theme" && (s.Value != "dark" || s.Overridden) {
			t.Errorf("the person sees the theme as %+v", s)
		}
		if s.Key == "usage.report_max_days" && s.Value != "30" {
			t.Errorf("the person sees the install setting as %q", s.Value)
		}
	}
	// What the install cannot set.
	for key, code := range map[string]int{"share.max_links": http.StatusForbidden, "data.dir": http.StatusForbidden, "rules.call_recording": http.StatusForbidden, "no.such_key": http.StatusNotFound} {
		if rec, _ := g.change(install, adminapi.ChangeBody{Key: key, Value: "1"}); rec.Code != code {
			t.Errorf("setting %s for the install answered %d, want %d", key, rec.Code, code)
		}
	}
	if rec, _ := g.change(install, adminapi.ChangeBody{Key: "usage.report_max_days", Value: "a year"}); rec.Code != http.StatusBadRequest || errorCodeOf(rec) != "bad_value" {
		t.Errorf("a value that is not a number answered %d %s", rec.Code, errorCodeOf(rec))
	}
	// Reset, and the list.
	if rec, reply := g.change(install, adminapi.ChangeBody{Key: "usage.report_max_days", Reset: true}); rec.Code != http.StatusOK || reply.Setting.Overridden || reply.Change.Old != "30" {
		t.Errorf("the reset answered %d %s", rec.Code, rec.Body.String())
	}
	rec = g.call("GET", install, nil, g.adminCookie)
	var all adminapi.InstallSettingsReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &all) != nil || len(all.Settings) == 0 {
		t.Fatalf("the install's settings answered %d", rec.Code)
	}
	for _, s := range all.Settings {
		if s.Scope == string(settings.ScopeTenantAdmin) {
			t.Errorf("%s, a tenant-admin setting, is listed for the install", s.Key)
		}
		if s.Key == "server.page_limit" && (!s.Pending || !s.Restart) {
			t.Errorf("the restart setting is listed as %+v", s)
		}
	}
}
