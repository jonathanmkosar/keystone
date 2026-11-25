package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The route walks. They call every route Keystone serves, in memory, with
// the shipped settings, and are part of three checks: tenant isolation
// (routeProbes), calls recorded (checkRecorded) and no keys in output
// (keysInTheAPI). The list of routes is app.Routes, which is also what the
// binary serves, so a route cannot be served and not walked.

// doorRoutes is every route that may be served to someone who has not
// signed in, and why. A route marked as a door in the code and not listed
// here fails the tenant isolation check, so a door cannot be added without
// a line in this file.
var doorRoutes = map[string]string{
	"GET /api/v1/signin/people": "the sign-in screen asks who is signing in, before there is a session",
	"POST /api/v1/session":      "signing in is how a session begins",
}

// Invented passwords, used in throwaway folders only.
const (
	walkPasswordOne = "walk person one's phrase"
	walkPasswordTwo = "walk person two's phrase"
)

var routePart = regexp.MustCompile(`\{[a-z_]+\}`)

// walker calls routes on one data folder.
type walker struct {
	h      http.Handler
	routes []api.Route
	log    *bytes.Buffer
}

func newWalker(in *tenant.Install) (*walker, error) {
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	w.routes = app.Routes(c)
	if w.h, err = app.Handler(c); err != nil {
		return nil, err
	}
	return w, nil
}

func routeName(rt api.Route) string { return rt.Method + " " + rt.Path }

// call makes one call to a route, with token as its session cookie when
// token is not empty. extra changes the request before it is sent. A route
// that streams without end is stopped after a few seconds.
func (w *walker) call(rt api.Route, token string, extra func(*http.Request)) *httptest.ResponseRecorder {
	return w.request(rt.Method, routePart.ReplaceAllString(rt.Path, "x"), token, "{}", extra)
}

func (w *walker) request(method, path, token, body string, extra func(*http.Request)) *httptest.ResponseRecorder {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var r *http.Request
	if method == "GET" || method == "DELETE" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.AddCookie(&http.Cookie{Name: api.CookieName, Value: token})
	}
	if extra != nil {
		extra(r)
	}
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, r.WithContext(ctx))
	return rec
}

// signIn signs a tenant in through the door, as a client does, and returns
// the token from the cookie.
func (w *walker) signIn(id, password string) (string, *httptest.ResponseRecorder) {
	body, _ := json.Marshal(api.SignInBody{Tenant: id, Password: password})
	rec := w.request("POST", api.Prefix+"session", "", string(body), nil)
	for _, c := range rec.Result().Cookies() {
		if c.Name == api.CookieName && rec.Code == http.StatusOK {
			return c.Value, rec
		}
	}
	return "", rec
}

func logOf(s *tenant.Store) ([]audit.Entry, error) {
	return audit.List(context.Background(), s, audit.Filter{})
}

// routeProbes is the route walk of the tenant isolation check. It proves,
// for every route that is not a listed door route, that the route is
// refused without a session, with a session re-addressed to another tenant
// and with a session that has been signed out, and that a call with tenant
// one's session is written in tenant one's audit log and leaves tenant
// two's log as it was: the tenant came from the session and nothing else.
func routeProbes(in *tenant.Install, one, two *tenant.Store) (reached []string, err error) {
	o, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	if err := signin.SetPassword(one, walkPasswordOne, o); err != nil {
		return nil, err
	}
	if err := signin.SetPassword(two, walkPasswordTwo, o); err != nil {
		return nil, err
	}
	w, err := newWalker(in)
	if err != nil {
		return nil, err
	}

	// The doors are the listed ones and no others.
	doors := map[string]bool{}
	for _, rt := range w.routes {
		if rt.Door {
			doors[routeName(rt)] = true
			if _, ok := doorRoutes[routeName(rt)]; !ok {
				reached = append(reached, routeName(rt)+" is served without a session and is not on the list of door routes in tools/verify/routes.go")
			}
		}
	}
	for name := range doorRoutes {
		if !doors[name] {
			return nil, fmt.Errorf("%s is listed as a door route in tools/verify/routes.go and is not one: take it off the list", name)
		}
	}

	// The sign-in door opens one tenant, the one whose password it was given.
	if token, rec := w.signIn(two.TenantID(), walkPasswordOne); token != "" || rec.Code != http.StatusUnauthorized {
		reached = append(reached, fmt.Sprintf("tenant one's password signed in to tenant two through the API (status %d)", rec.Code))
	}
	token, rec := w.signIn(one.TenantID(), walkPasswordOne)
	if token == "" {
		return nil, fmt.Errorf("tenant one cannot sign in through the API: status %d", rec.Code)
	}
	if id, _ := signin.TenantOf(token); id != one.TenantID() {
		return nil, errors.New("the API gave tenant one a session that names another tenant")
	}
	var me api.SessionReply
	if rec := w.request("GET", api.Prefix+"session", token, "", nil); json.Unmarshal(rec.Body.Bytes(), &me) != nil || me.Person.ID != one.TenantID() {
		reached = append(reached, fmt.Sprintf("with tenant one's session the API says the caller is %q", me.Person.ID))
	}
	forged := strings.Replace(token, one.TenantID(), two.TenantID(), 1)

	// count returns how many entries each tenant's log holds.
	count := func() (int, int, error) {
		a, err := logOf(one)
		if err != nil {
			return 0, 0, err
		}
		b, err := logOf(two)
		return len(a), len(b), err
	}
	walked := 0
	for _, rt := range w.routes {
		if rt.Door {
			continue
		}
		walked++
		name := routeName(rt)
		haveOne, haveTwo, err := count()
		if err != nil {
			return nil, err
		}
		for what, tok := range map[string]string{"no session": "", "a session re-addressed to tenant two": forged, "something that is not a token": "kss_nothing"} {
			// A request may name another tenant anywhere it likes: it changes nothing.
			rec := w.call(rt, tok, func(r *http.Request) {
				r.Header.Set("X-Tenant", two.TenantID())
				r.URL.RawQuery = "tenant=" + two.TenantID()
			})
			if rec.Code != http.StatusUnauthorized {
				reached = append(reached, fmt.Sprintf("%s was served with %s (status %d)", name, what, rec.Code))
			}
		}
		if a, b, err := count(); err != nil {
			return nil, err
		} else if a != haveOne || b != haveTwo {
			reached = append(reached, name+" wrote to an audit log on calls that had no session")
		}

		rec := w.call(rt, token, func(r *http.Request) {
			r.Header.Set("X-Tenant", two.TenantID())
			r.URL.RawQuery = "tenant=" + two.TenantID()
		})
		if rec.Code == http.StatusUnauthorized {
			return nil, fmt.Errorf("%s refused tenant one's own session", name)
		}
		a, b, err := count()
		if err != nil {
			return nil, err
		}
		if a < haveOne+2 {
			reached = append(reached, name+", called with tenant one's session, is not in tenant one's audit log")
		}
		if b != haveTwo {
			reached = append(reached, name+", called with tenant one's session, wrote to tenant two's audit log")
		}
		// A route may end the session it was called with. Sign in again.
		if _, err := signin.Check(one, token); err != nil {
			if token, _ = w.signIn(one.TenantID(), walkPasswordOne); token == "" {
				return nil, errors.New("tenant one cannot sign in again through the API")
			}
			forged = strings.Replace(token, one.TenantID(), two.TenantID(), 1)
		}
	}
	if walked == 0 {
		return nil, errors.New("the API has no route that needs a session, so the walk proved nothing")
	}

	// A path that is no route is behind the session too.
	if rec := w.request("GET", api.Prefix+"no/such/route", "", "", nil); rec.Code != http.StatusUnauthorized {
		reached = append(reached, fmt.Sprintf("a path that is no route answered %d to a caller with no session", rec.Code))
	}

	// Signed out through the API, the session opens no route.
	if rec := w.request("DELETE", api.Prefix+"session", token, "", nil); rec.Code != http.StatusOK {
		return nil, fmt.Errorf("tenant one cannot sign out through the API: status %d", rec.Code)
	}
	for _, rt := range w.routes {
		if rt.Door {
			continue
		}
		if rec := w.call(rt, token, nil); rec.Code != http.StatusUnauthorized {
			reached = append(reached, fmt.Sprintf("%s was served with a signed-out session (status %d)", routeName(rt), rec.Code))
		}
	}
	return reached, nil
}

// checkRecorded is the standing check "calls recorded", for API calls: the
// fourth fixed rule says every call is recorded. It calls every route with
// a session and reads the tenant's audit log for the request id the answer
// came with. There must be an entry written before the call ran and an
// entry for its end, with the status the caller was given. A sign-in has no
// session to be recorded under, so it is looked for in the log of the
// tenant it was for. A door route that is for no tenant is looked for in
// the server's own log.
//
// Model calls and tool calls join this check with the items that make them.
func checkRecorded(string) error {
	dir, err := os.MkdirTemp("", "keystone-recorded-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.Open(dir)
	if err != nil {
		return err
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		return err
	}
	store, err := in.Store(made.ID)
	if err != nil {
		return err
	}
	defer store.Close()
	o, err := signin.Defaults()
	if err != nil {
		return err
	}
	if err := signin.SetPassword(store, walkPasswordOne, o); err != nil {
		return err
	}
	w, err := newWalker(in)
	if err != nil {
		return err
	}
	// entries returns the tenant's entries for one request.
	entries := func(rec *httptest.ResponseRecorder) ([]audit.Entry, error) {
		id := rec.Header().Get("X-Request-Id")
		if id == "" {
			return nil, errors.New("an answer came with no request id")
		}
		return audit.List(context.Background(), store, audit.Filter{RequestID: id})
	}
	var problems []string

	// Sign-ins: one refused and one good, each in the tenant's log.
	_, refused := w.signIn(made.ID, "not the phrase at all")
	token, good := w.signIn(made.ID, walkPasswordOne)
	if token == "" {
		return fmt.Errorf("the test person cannot sign in through the API: status %d", good.Code)
	}
	for what, rec := range map[string]*httptest.ResponseRecorder{"a refused sign-in": refused, "a sign-in": good} {
		got, err := entries(rec)
		if err != nil {
			return err
		}
		if len(got) != 1 || got[0].Status != rec.Code || got[0].Kind != audit.KindAPI || got[0].Action != "POST /api/v1/session" {
			problems = append(problems, fmt.Sprintf("%s (status %d) is in the tenant's audit log as %d entries", what, rec.Code, len(got)))
		}
	}

	for _, rt := range w.routes {
		name := routeName(rt)
		if rt.Door {
			rec := w.call(rt, "", nil)
			if id := rec.Header().Get("X-Request-Id"); id == "" || !strings.Contains(w.log.String(), id+" "+rt.Method+" ") {
				problems = append(problems, name+", a door route, is not in the server's log")
			}
			continue
		}
		rec := w.call(rt, token, nil)
		got, err := entries(rec)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		switch {
		case len(got) != 2:
			problems = append(problems, fmt.Sprintf("%s left %d audit entries, want one before the call and one for its end", name, len(got)))
		case got[0].Outcome != audit.OutcomeStarted:
			problems = append(problems, name+": the first entry was not written before the call ran")
		case got[1].Outcome == audit.OutcomeStarted || got[1].Status != rec.Code:
			problems = append(problems, fmt.Sprintf("%s answered %d and its end is recorded as %s %d", name, rec.Code, got[1].Outcome, got[1].Status))
		case !strings.HasPrefix(got[0].Action, rt.Method+" "+api.Prefix) || got[0].Action != got[1].Action:
			problems = append(problems, fmt.Sprintf("%s is recorded as %q", name, got[0].Action))
		}
		if !strings.Contains(w.log.String(), rec.Header().Get("X-Request-Id")) {
			problems = append(problems, name+" is not in the server's log")
		}
		if _, err := signin.Check(store, token); err != nil {
			if token, _ = w.signIn(made.ID, walkPasswordOne); token == "" {
				return errors.New("the test person cannot sign in again through the API")
			}
		}
	}

	// A call that is no route, made with a session, is recorded as well.
	rec := w.request("GET", api.Prefix+"no/such/route", token, "", nil)
	if got, err := entries(rec); err != nil {
		return err
	} else if rec.Code != http.StatusNotFound || len(got) != 2 {
		problems = append(problems, fmt.Sprintf("a call to a path that is no route answered %d and left %d audit entries", rec.Code, len(got)))
	}
	// A model call made for a turn is recorded too. See turns.go.
	more, err := modelCallsRecorded()
	if err != nil {
		return err
	}
	problems = append(problems, more...)
	// So is each tool call a turn makes (P7-01). See toolloop.go.
	tooled, err := toolCallsRecorded()
	if err != nil {
		return err
	}
	problems = append(problems, tooled...)
	// So is a web search, whose answer cites what it returned (P7-03). See websearch.go.
	searched, err := webSearchRecorded()
	if err != nil {
		return err
	}
	problems = append(problems, searched...)
	// So is a connector's tool, offered by name, loaded and called, and its
	// tool that changes something asks after its outside content (P7-04). See connectors.go.
	connected, err := connectorsRecorded()
	if err != nil {
		return err
	}
	problems = append(problems, connected...)
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// keysInTheAPI is the API's part of the "no keys in output" check. A value
// is stored as a tenant's secret. The same value is then sent to every
// route wherever a client can put one: as a wrong password, in the query
// string, in a header and in the body. It returns every answer, the
// server's log and every file in the data folder. It also looks for the
// session token itself, which may appear in the sign-in answer's cookie
// and nowhere else.
func keysInTheAPI(_, dir, value string) ([]keyOutput, error) {
	data := filepath.Join(dir, "data")
	in, err := tenant.OpenWithKeeper(data, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		return nil, err
	}
	store, err := in.Store(made.ID)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	if err := store.Secrets().Set(keyCheckName, value); err != nil {
		return nil, err
	}
	// The check is worth nothing unless the value really is stored.
	if got, ok, err := store.Secrets().Get(keyCheckName); err != nil || !ok || got != value {
		return nil, fmt.Errorf("a value was stored and did not come back: found %v, %v", ok, err)
	}
	o, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	if err := signin.SetPassword(store, walkPasswordOne, o); err != nil {
		return nil, err
	}
	w, err := newWalker(in)
	if err != nil {
		return nil, err
	}

	var out []keyOutput
	answer := func(where string, rec *httptest.ResponseRecorder, withHeaders bool) {
		text := rec.Body.String()
		if withHeaders {
			text += fmt.Sprint(rec.Header())
		}
		out = append(out, keyOutput{where, []byte(text)})
	}
	_, rec := w.signIn(made.ID, value)
	answer("the answer to a sign-in with the value as the password", rec, true)
	token, rec := w.signIn(made.ID, walkPasswordOne)
	if token == "" {
		return out, fmt.Errorf("the test person cannot sign in through the API: status %d", rec.Code)
	}
	// The cookie is where the token belongs. The body is not.
	answer("the body of the sign-in answer", rec, false)

	body, _ := json.Marshal(map[string]string{"note": value, "password": value})
	for _, rt := range w.routes {
		for who, tok := range map[string]string{"a signed-in caller": token, "a caller with no session": ""} {
			rec := w.request(rt.Method, routePart.ReplaceAllString(rt.Path, "x"), tok, string(body), func(r *http.Request) {
				r.URL.RawQuery = "note=" + strings.ReplaceAll(value, " ", "+")
				r.Header.Set("X-Note", value)
				r.Header.Set("Authorization", "Bearer "+value)
			})
			// Signing out clears the cookie and does not send the token back.
			answer("the answer of "+routeName(rt)+" to "+who, rec, true)
		}
		if _, err := signin.Check(store, token); err != nil {
			if token, _ = w.signIn(made.ID, walkPasswordOne); token == "" {
				return out, errors.New("the test person cannot sign in again through the API")
			}
		}
	}
	out = append(out, keyOutput{"the server's log", append([]byte(nil), w.log.Bytes()...)})
	// The audit log, as the API returns it and as it is on disk.
	answer("the audit log from the API", w.request("GET", api.Prefix+"audit", token, "", nil), true)
	files, err := folderFiles(data)
	if err != nil {
		return out, err
	}
	out = append(out, files...)

	// Anything shaped like a session token, looked for in the same places.
	var where []string
	for _, o := range out {
		if sessionTokenShape.Match(o.data) {
			where = append(where, o.where)
		}
	}
	if len(where) > 0 {
		return out, fmt.Errorf("a session token is in %s", strings.Join(where, ", "))
	}
	return out, nil
}

var sessionTokenShape = regexp.MustCompile(`kss_[a-f0-9]{16}_[a-f0-9]{64}`)
