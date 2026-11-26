package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/google"
	"github.com/jonathanmkosar/keystone/internal/google/googletest"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/settings"
)

// The Google connectors' tests (P12-02), against the stand-in Google on
// this machine. Every name, address and value is invented.

// googleOptions are the options with Google's addresses at the stand-in.
func googleOptions(t *testing.T, stand *googletest.Server) Options {
	t.Helper()
	srv := httptest.NewServer(stand)
	t.Cleanup(srv.Close)
	o := options(t)
	o.Google.Endpoints = stand.Endpoints(srv.URL)
	o.Google.OnThisMachine = true
	return o
}

// setUpGoogle gives a person the invented OAuth client, as the admin would.
func setUpGoogle(t *testing.T, s *tenant.Store) {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	if err := tenantsettings.Set(bg(), s, reg, tenantsettings.Admin, KeyGoogleClientID, googletest.ClientID); err != nil {
		t.Fatal(err)
	}
	if err := s.Secrets().Set(GoogleClientSecret, googletest.ClientSecret); err != nil {
		t.Fatal(err)
	}
}

// googleRouteRig is the route rig with the given options.
func googleRouteRig(t *testing.T, o Options) *routeRig {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	so, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	g := &routeRig{t: t, install: in, tokens: map[string]string{}}
	for i, person := range []struct{ name, phrase string }{{"Test Person One", phraseOne}, {"Test Person Two", phraseTwo}} {
		made, err := in.Create(person.name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		signin.SetPassword(s, person.phrase, so)
		token, _, err := signin.SignIn(s, person.phrase, so)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
		g.tokens[made.ID] = token
		if i == 0 {
			g.one = made.ID
		} else {
			g.two = made.ID
		}
	}
	if g.h, err = api.Handler(api.Options{Install: in, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, Routes(o)); err != nil {
		t.Fatal(err)
	}
	return g
}

// post calls a route as the page would: with the page's Origin.
func (g *routeRig) post(who, path string, body, out any) *httptest.ResponseRecorder {
	g.t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", api.Prefix+path, strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://example.com")
	r.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.tokens[who]})
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r)
	g.said = append(g.said, rec.Body.String())
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			g.t.Fatalf("POST %s: %v\n%s", path, err, rec.Body)
		}
	}
	return rec
}

// connectGoogle starts a sign-in for one person, consents at the stand-in,
// and finishes it.
func (g *routeRig) connectGoogle(stand *googletest.Server, who string, services ...string) GoogleStatus {
	g.t.Helper()
	var start GoogleStartReply
	g.want(g.post(who, "google/start", GoogleStartBody{Services: services}, &start), http.StatusOK, "")
	back, err := stand.Authorize(start.URL)
	if err != nil {
		g.t.Fatal(err)
	}
	u, _ := url.Parse(back)
	var st GoogleStatus
	g.want(g.post(who, "google/finish", GoogleFinishBody{State: u.Query().Get("state"), Code: u.Query().Get("code")}, &st), http.StatusOK, "")
	return st
}

func TestAGoogleConnectorIsChecked(t *testing.T) {
	good := Connector{Name: "gmail", Transport: TransportGoogle, Secret: GoogleSecret, Enabled: true, Outside: true}
	if err := Check(good); err != nil {
		t.Errorf("a Gmail connector was refused: %v", err)
	}
	for what, c := range map[string]Connector{
		"another name": {Name: "drive", Transport: TransportGoogle, Secret: GoogleSecret},
		"an address":   {Name: "gmail", Transport: TransportGoogle, Secret: GoogleSecret, URL: "https://mcp.example/mcp"},
		"a command":    {Name: "calendar", Transport: TransportGoogle, Secret: GoogleSecret, Command: "calc.exe"},
		"no secret":    {Name: "calendar", Transport: TransportGoogle},
	} {
		if err := Check(c); err == nil {
			t.Errorf("a Google connector with %s was taken", what)
		}
	}
}

// The migration that brings the google transport keeps every connector,
// tool and switch, and their foreign keys still cascade.
func TestTheGoogleMigrationKeepsWhatWasThere(t *testing.T) {
	s, _ := stores(t)
	if _, err := Add(bg(), s, Connector{Name: "notes", Transport: TransportHTTP, URL: "https://mcp.example/mcp", Enabled: true, Outside: true}, ByPerson, 20); err != nil {
		t.Fatal(err)
	}
	if err := saveGoogleTools(bg(), s, Connector{Name: "notes", Transport: TransportHTTP}, 10); err == nil {
		t.Error("an http connector was given Google's tools")
	}
	if err := SaveTools(bg(), s, "notes", googleListed(google.Gmail), "", 10); err != nil {
		t.Fatal(err)
	}
	made, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetSwitch(bg(), s, made.ID, "notes", false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../tenant/migrations/20261125T190000_google_connectors.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	// The part that makes the connector tables again: the new tables after it
	// are there already.
	rebuild := string(raw)[:strings.Index(string(raw), "CREATE TABLE google_accounts")]
	if _, err := tx.Exec(rebuild); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	k, err := Get(bg(), s, "notes")
	if err != nil || len(k.Tools) != 4 || k.URL != "https://mcp.example/mcp" {
		t.Fatalf("after the migration the connector is %+v, %v", k, err)
	}
	if sw, _ := Switches(bg(), s, made.ID); len(sw) != 1 || sw["notes"] {
		t.Errorf("after the migration the switches are %v", sw)
	}
	if _, err := Add(bg(), s, Connector{Name: "calendar", Transport: TransportGoogle, Secret: GoogleSecret, Enabled: true}, ByPerson, 20); err != nil {
		t.Errorf("a Google connector was not taken after the migration: %v", err)
	}
	if err := Remove(bg(), s, "notes"); err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB().QueryRow("SELECT count(*) FROM connector_tools WHERE connector = 'notes'").Scan(&n)
	var m int
	s.DB().QueryRow("SELECT count(*) FROM chat_connectors WHERE connector = 'notes'").Scan(&m)
	if n != 0 || m != 0 {
		t.Errorf("removing the connector left %d tools and %d switches", n, m)
	}
}

func TestConnectGoogle(t *testing.T) {
	stand := googletest.New()
	o := googleOptions(t, stand)
	g := googleRouteRig(t, o)

	// Not set up: what is missing is named, and nothing starts.
	var st GoogleStatus
	g.want(g.call(g.one, "GET", "google", nil, &st), http.StatusOK, "")
	if st.SetUp || len(st.Missing) != 2 || st.Connected || len(st.Services) != 2 || st.RedirectPath != GoogleRedirectPath {
		t.Errorf("before it is set up the status is %+v", st)
	}
	g.want(g.post(g.one, "google/start", GoogleStartBody{}, nil), http.StatusConflict, "not_set_up")
	g.with(g.one, func(s *tenant.Store) { setUpGoogle(t, s) })
	g.want(g.call(g.one, "GET", "google", nil, &st), http.StatusOK, "")
	if !st.SetUp || len(st.Missing) != 0 {
		t.Errorf("once set up the status is %+v", st)
	}

	// The start: a page's Origin gives the return address, and with none
	// and no setting there is none.
	g.want(g.call(g.one, "POST", "google/start", GoogleStartBody{}, nil), http.StatusBadRequest, "no_redirect")
	g.want(g.post(g.one, "google/start", GoogleStartBody{Services: []string{"drive"}}, nil), http.StatusBadRequest, "bad_request")
	var start GoogleStartReply
	g.want(g.post(g.one, "google/start", GoogleStartBody{}, &start), http.StatusOK, "")
	if start.Redirect != "http://example.com/settings/connectors/google" {
		t.Errorf("the return address is %q", start.Redirect)
	}
	u, _ := url.Parse(start.URL)
	if q := u.Query(); q.Get("code_challenge_method") != "S256" || q.Get("client_id") != googletest.ClientID || !strings.Contains(q.Get("scope"), google.ScopeCalendar) || q.Get("redirect_uri") != start.Redirect {
		t.Errorf("the sign-in address is %s", start.URL)
	}

	// The browser comes back. A state that was not started here, and one
	// started for another person, finish nothing.
	back, err := stand.Authorize(start.URL)
	if err != nil {
		t.Fatal(err)
	}
	bu, _ := url.Parse(back)
	state, code := bu.Query().Get("state"), bu.Query().Get("code")
	g.want(g.post(g.one, "google/finish", GoogleFinishBody{State: "not-a-state", Code: code}, nil), http.StatusBadRequest, "bad_state")
	g.with(g.two, func(s *tenant.Store) { setUpGoogle(t, s) })
	g.want(g.post(g.two, "google/finish", GoogleFinishBody{State: state, Code: code}, nil), http.StatusBadRequest, "bad_state")
	g.want(g.post(g.one, "google/finish", GoogleFinishBody{State: state, Code: code}, &st), http.StatusOK, "")
	if !st.Connected || st.Email != googletest.Email || !st.Services[0].Added || !st.Services[1].Added || !st.Services[0].Granted || st.Services[0].Tools != 4 || st.Services[1].Tools != 5 {
		t.Fatalf("after the sign-in the status is %+v", st)
	}
	g.want(g.post(g.one, "google/finish", GoogleFinishBody{State: state, Code: code}, nil), http.StatusBadRequest, "bad_state")
	var refresh string
	g.with(g.one, func(s *tenant.Store) {
		v, ok, _ := s.Secrets().Get(GoogleSecret)
		if !ok || !strings.HasPrefix(v, "invented-refresh-") {
			t.Errorf("the vault holds %q", v)
		}
		refresh = v
		k, err := Get(bg(), s, "gmail")
		if err != nil || k.Transport != TransportGoogle || !k.Enabled || !k.Outside || k.Secret != GoogleSecret {
			t.Errorf("the Gmail connector is %+v, %v", k, err)
		}
	})
	var list ListReply
	g.want(g.call(g.one, "GET", "connectors", nil, &list), http.StatusOK, "")
	if len(list.Connectors) != 2 || list.Connectors[0].Name != "calendar" || !list.Connectors[1].TokenPresent {
		t.Errorf("the connectors are %+v", list.Connectors)
	}

	// Refused at Google, and a partial grant.
	var again GoogleStartReply
	g.want(g.post(g.two, "google/start", GoogleStartBody{}, &again), http.StatusOK, "")
	au, _ := url.Parse(again.URL)
	g.want(g.post(g.two, "google/finish", GoogleFinishBody{State: au.Query().Get("state"), Error: "access_denied"}, nil), http.StatusBadRequest, "refused")
	stand.Grant = append([]string{"openid", "email"}, google.Gmail.Scopes()...)
	st = g.connectGoogle(stand, g.two)
	if !st.Connected || !st.Services[0].Added || st.Services[1].Added || st.Services[1].Granted {
		t.Errorf("after a sign-in that allowed Gmail alone the status is %+v", st)
	}
	stand.Grant = nil

	// Check now asks Google for a token.
	var row Row
	g.want(g.call(g.one, "POST", "connectors/gmail/check", nil, &row), http.StatusOK, "")
	if row.Problem != "" || len(row.Tools) != 4 {
		t.Errorf("the check gave %+v", row)
	}

	// Disconnect revokes the sign-in at Google and leaves nothing behind.
	g.want(g.call(g.one, "DELETE", "google", nil, &st), http.StatusOK, "")
	if st.Connected || st.Services[0].Added || st.Services[1].Added || !stand.Revoked(refresh) {
		t.Errorf("after the disconnect the status is %+v, revoked %v", st, stand.Revoked(refresh))
	}
	g.with(g.one, func(s *tenant.Store) {
		if _, ok, _ := s.Secrets().Get(GoogleSecret); ok {
			t.Error("the sign-in is still in the vault")
		}
	})
	for _, said := range g.said {
		for _, held := range []string{refresh, googletest.ClientSecret, code, "invented-access-"} {
			if strings.Contains(said, held) {
				t.Errorf("a route answered with a token or a secret: %s", said)
			}
		}
	}
}

// signedIn connects a person to the stand-in through the routes and
// returns the person's store's tenant id and the rig.
func signedIn(t *testing.T, stand *googletest.Server, o Options) *routeRig {
	t.Helper()
	g := googleRouteRig(t, o)
	g.with(g.one, func(s *tenant.Store) { setUpGoogle(t, s) })
	if st := g.connectGoogle(stand, g.one); !st.Connected {
		t.Fatalf("the sign-in gave %+v", st)
	}
	return g
}

func tokenCalls(stand *googletest.Server) int {
	n := 0
	for _, c := range stand.Calls() {
		if c.Path == "/token" {
			n++
		}
	}
	return n
}

func TestGoogleToolsAreOfferedAndRunThroughThePool(t *testing.T) {
	stand := googletest.New()
	o := googleOptions(t, stand)
	g := signedIn(t, stand, o)
	pool := NewPool(o, t.Logf)
	defer pool.Close()
	g.with(g.one, func(s *tenant.Store) {
		made, err := chat.Create(s)
		if err != nil {
			t.Fatal(err)
		}
		offer, run := pool.ForCall(bg(), s, made.ID, nil, nil)
		if got := offered(offer); got != "calendar_create_event calendar_delete_event calendar_get_event calendar_list_events calendar_update_event gmail_draft gmail_read gmail_search gmail_send load_tools" {
			t.Fatalf("the call is offered %s", got)
		}
		send := run["gmail_send"]
		if send.ReadOnly || send.Inside || !send.Reaches(json.RawMessage(`{}`)) || send.Describe == nil {
			t.Errorf("gmail_send runs as %+v", send)
		}
		if run["gmail_search"].ReachesOthers != nil || !run["gmail_search"].ReadOnly {
			t.Error("gmail_search reaches people or changes something")
		}
		if create := run["calendar_create_event"]; create.Reaches(json.RawMessage(`{"title":"x","guests":[]}`)) || !create.Reaches(json.RawMessage(`{"guests":["a@example.com"]}`)) {
			t.Error("calendar_create_event does not reach exactly its guests")
		}
		before := tokenCalls(stand)
		for i := 0; i < 2; i++ {
			text, err := run["gmail_search"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{"query":"tide"}`), ChatID: made.ID})
			if err != nil || !tools.IsOutside(resultBlock(text)) || !strings.Contains(text, "Tide tables for December") {
				t.Fatalf("the search gave %q, %v", text, err)
			}
		}
		if n := tokenCalls(stand) - before; n != 0 {
			t.Errorf("two calls with a fresh access token asked for %d more", n)
		}
		// Known lists every tool with its flags, for the approvals screen.
		for _, info := range pool.Known(bg(), s) {
			if (info.Name == "gmail_send" || strings.HasPrefix(info.Name, "calendar_") && !info.ReadOnly) != info.ReachesOthers {
				t.Errorf("%s is listed as %+v", info.Name, info)
			}
		}
		// Another person's store is refused.
		g.with(g.two, func(other *tenant.Store) {
			if _, err := run["gmail_search"].Run(bg(), tools.Input{Store: other, Args: json.RawMessage(`{}`)}); err == nil {
				t.Error("a Google tool ran for another person")
			}
		})
	})
}

func TestAnAccessTokenIsRefreshedBeforeItStopsWorking(t *testing.T) {
	stand := googletest.New()
	stand.AccessTTL = 30 * time.Second // inside the minute's margin, so never kept
	o := googleOptions(t, stand)
	g := signedIn(t, stand, o)
	pool := NewPool(o, t.Logf)
	defer pool.Close()
	g.with(g.one, func(s *tenant.Store) {
		made, _ := chat.Create(s)
		_, run := pool.ForCall(bg(), s, made.ID, nil, nil)
		before := tokenCalls(stand)
		for i := 0; i < 2; i++ {
			if _, err := run["calendar_list_events"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{"from":"2026-11-26","to":"2026-12-01"}`)}); err != nil {
				t.Fatal(err)
			}
		}
		if n := tokenCalls(stand) - before; n != 2 {
			t.Errorf("two calls with a token about to expire asked for %d new ones", n)
		}
	})
}

// A token is taken out of whatever Google sends back: results, errors, and
// the problem a check keeps. A revoked sign-in asks the person to connect
// again.
func TestGoogleNeverRepeatsATokenAndAGoneSignInSaysSo(t *testing.T) {
	stand := googletest.New()
	o := googleOptions(t, stand)
	g := signedIn(t, stand, o)
	pool := NewPool(o, t.Logf)
	defer pool.Close()
	var refresh string
	g.with(g.one, func(s *tenant.Store) { refresh, _, _ = s.Secrets().Get(GoogleSecret) })
	stand.Echo = refresh + " " + googletest.ClientSecret
	g.with(g.one, func(s *tenant.Store) {
		made, _ := chat.Create(s)
		_, run := pool.ForCall(bg(), s, made.ID, nil, nil)
		var said []string
		text, err := run["gmail_read"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{"id":"m1a2b3"}`)})
		said = append(said, text, errText(err))
		_, err = run["gmail_read"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{"id":"nosuchmessage"}`)})
		said = append(said, errText(err))
		if !strings.Contains(strings.Join(said, " "), removed) {
			t.Errorf("the echoed token was not seen to be taken out: %q", said)
		}
		for _, x := range said {
			if strings.Contains(x, refresh) || strings.Contains(x, googletest.ClientSecret) {
				t.Errorf("a result or an error holds a token: %q", x)
			}
		}
		// Revoked at Google: the next refresh is refused.
		o.Google.tokens.drop(s.TenantID() + "/" + GoogleSecret)
		if err := google.Revoke(context.Background(), o.googleHTTP(), o.Google.Endpoints, refresh); err != nil {
			t.Fatal(err)
		}
		_, err = run["gmail_search"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{}`)})
		if err == nil || !strings.Contains(err.Error(), "connect Google again") {
			t.Errorf("a revoked sign-in gave %v", err)
		}
		k, err := o.Check(bg(), s, "calendar")
		if err != nil || !strings.Contains(k.Problem, "connect Google again") || strings.Contains(k.Problem, refresh) || len(k.Tools) != 5 {
			t.Errorf("the check of a revoked sign-in kept %+v, %v", k, err)
		}
	})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Google's addresses are dialled as web_fetch dials: one on this machine
// is refused unless the stand-in is meant.
func TestGoogleIsReachedAtPublicAddressesOnly(t *testing.T) {
	stand := googletest.New()
	o := googleOptions(t, stand)
	g := signedIn(t, stand, o)
	o.Google.OnThisMachine = false
	o.Google.tokens = newTokenCache()
	pool := NewPool(o, t.Logf)
	defer pool.Close()
	g.with(g.one, func(s *tenant.Store) {
		made, _ := chat.Create(s)
		_, run := pool.ForCall(bg(), s, made.ID, nil, nil)
		before := len(stand.Calls())
		if _, err := run["gmail_search"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{}`)}); err == nil {
			t.Error("a Google address on this machine was reached")
		}
		if len(stand.Calls()) != before {
			t.Error("the stand-in was called")
		}
	})
}
