package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Invented people and passwords, used in a temporary folder only.
const (
	passwordOne = "test person one's phrase"
	passwordTwo = "test person two's phrase"
)

// safeBuffer is a log the test can read while the server writes.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type fixture struct {
	t        *testing.T
	in       *tenant.Install
	one, two string // tenant ids
	h        http.Handler
	routes   []Route
	log      *safeBuffer
}

func testOptions() signin.Options {
	return signin.Options{
		PasswordIterations: 50, PasswordMinLength: 12, SessionLifetime: time.Hour,
		FailuresBeforeWait: 3, FailureWait: time.Minute,
		PasskeyRPID: "localhost", PasskeyOrigin: "http://localhost:7900", PasskeyChallengeLifetime: time.Minute,
	}
}

// newFixture makes two tenants with passwords and the shell's handler, with
// three more routes: one that says whose store it was given, one that
// panics, and a stream that waits.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, in: in, log: &safeBuffer{}}
	o := testOptions()
	for i, p := range []struct{ name, password string }{{"Test Person One", passwordOne}, {"Test Person Two", passwordTwo}} {
		made, err := in.Create(p.name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := signin.SetPassword(s, p.password, o); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if i == 0 {
			f.one = made.ID
		} else {
			f.two = made.ID
		}
	}
	log := NewLog(f.log)
	f.routes = ShellRoutes(Shell{Install: in, SignIn: o, ListPeople: true, PageLimit: 5, Version: "test",
		Routes: func() []Route { return f.routes }, Log: log})
	f.routes = append(f.routes,
		Route{Method: "GET", Path: Prefix + "probe/{id}", Summary: "Says whose store the handler was given.",
			Handle: func(w http.ResponseWriter, r *http.Request, c Call) {
				WriteJSON(w, http.StatusOK, map[string]string{"tenant": c.Store.TenantID(), "session": c.Session.TenantID, "id": r.PathValue("id"), "request_id": c.RequestID})
			}},
		Route{Method: "POST", Path: Prefix + "probe/panic", Summary: "Panics.",
			Handle: func(http.ResponseWriter, *http.Request, Call) { panic("an invented failure") }},
		Route{Method: "POST", Path: Prefix + "probe/echo", Summary: "Reads a body.",
			Handle: func(w http.ResponseWriter, r *http.Request, c Call) {
				var body struct {
					Note string `json:"note"`
				}
				if ReadJSON(w, r, &body) {
					WriteJSON(w, http.StatusOK, map[string]int{"length": len(body.Note)})
				}
			}},
		Route{Method: "GET", Path: Prefix + "probe/quiet", Summary: "A stream that says one thing and waits.",
			Handle: func(w http.ResponseWriter, r *http.Request, c Call) {
				s, err := OpenStream(w, r)
				if err != nil {
					return
				}
				defer s.Close()
				s.Send(Event{ID: "1", Name: "hello", Data: "there"})
				<-r.Context().Done()
			}},
	)
	f.h, err = Handler(Options{Install: in, Log: log, MaxBodyBytes: 1024, Heartbeat: 10 * time.Millisecond}, f.routes)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// do makes one call in memory. token is sent as the session cookie.
func (f *fixture) do(method, path, token, body string, header ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	}
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	return rec
}

// signIn signs a tenant in through the door and returns the token from
// the cookie.
func (f *fixture) signIn(id, password string) (string, *httptest.ResponseRecorder) {
	f.t.Helper()
	body, _ := json.Marshal(SignInBody{Tenant: id, Password: password})
	rec := f.do("POST", Prefix+"session", "", string(body))
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			return c.Value, rec
		}
	}
	return "", rec
}

func (f *fixture) entries(id string) []audit.Entry {
	f.t.Helper()
	s, err := f.in.Store(id)
	if err != nil {
		f.t.Fatal(err)
	}
	defer s.Close()
	got, err := audit.List(context.Background(), s, audit.Filter{})
	if err != nil {
		f.t.Fatal(err)
	}
	return got
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) ErrorBody {
	t.Helper()
	var e ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("the answer is not an error body: %v\n%s", err, rec.Body.String())
	}
	if e.RequestID == "" || e.RequestID != rec.Header().Get("X-Request-Id") {
		t.Errorf("the error names request %q and the header says %q", e.RequestID, rec.Header().Get("X-Request-Id"))
	}
	return e
}

// url gives a route's path with something in each {name}.
func url(rt Route) string {
	return pathParam.ReplaceAllString(rt.Path, "x")
}

func TestEveryRouteButTheDoorNeedsASession(t *testing.T) {
	f := newFixture(t)
	doors := 0
	for _, rt := range f.routes {
		if rt.Door {
			doors++
			continue
		}
		rec := f.do(rt.Method, url(rt), "", "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with no session: status %d, want 401", rt.Method, rt.Path, rec.Code)
			continue
		}
		if e := errorOf(t, rec); e.Code != "no_session" {
			t.Errorf("%s %s with no session: code %q", rt.Method, rt.Path, e.Code)
		}
	}
	if doors != 2 {
		t.Errorf("the shell has %d door routes, want the two for sign-in", doors)
	}
	// A path that is no route is behind the session too.
	if rec := f.do("GET", "/api/v1/nothing/here", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("a path that is no route, with no session: status %d, want 401", rec.Code)
	}
	if n := len(f.entries(f.one)) + len(f.entries(f.two)); n != 0 {
		t.Errorf("calls with no session left %d audit entries", n)
	}
}

func TestSignInGivesACookieScriptsCannotRead(t *testing.T) {
	f := newFixture(t)
	token, rec := f.signIn(f.one, passwordOne)
	if rec.Code != http.StatusOK || token == "" {
		t.Fatalf("sign-in: status %d, token %q\n%s", rec.Code, token, rec.Body.String())
	}
	var ck *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			ck = c
		}
	}
	if !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/api/" || ck.Expires.IsZero() {
		t.Errorf("the session cookie is %+v: want HttpOnly, SameSite=Strict, the API's path and an end", ck)
	}
	if ck.Secure {
		t.Errorf("the cookie is marked for HTTPS on a plain request with the setting off")
	}
	if strings.Contains(rec.Body.String(), token) || strings.Contains(rec.Body.String(), "kss_") {
		t.Errorf("the sign-in answer holds the token: %s", rec.Body.String())
	}
	var reply SessionReply
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || reply.Person.ID != f.one || reply.Person.Name != "Test Person One" {
		t.Errorf("the sign-in answer is %+v (%v)", reply, err)
	}

	// The same person, asked through the session.
	rec = f.do("GET", Prefix+"session", token, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || rec.Code != 200 || reply.Person.ID != f.one {
		t.Errorf("who am I: status %d, %+v (%v)", rec.Code, reply, err)
	}

	// Refusals: a wrong password, tenant two's password, an id that is not there.
	for name, body := range map[string]SignInBody{
		"a wrong password":           {f.one, "not the phrase at all"},
		"another tenant's password":  {f.one, passwordTwo},
		"a tenant that is not there": {"0000000000000000", passwordOne},
		"something that is no id":    {"../" + f.one, passwordOne},
	} {
		raw, _ := json.Marshal(body)
		rec := f.do("POST", Prefix+"session", "", string(raw))
		if rec.Code != http.StatusUnauthorized || len(rec.Result().Cookies()) != 0 {
			t.Errorf("sign-in with %s: status %d, cookies %v", name, rec.Code, rec.Result().Cookies())
		} else if e := errorOf(t, rec); e.Code != "refused" || e.Error != "sign-in refused" {
			t.Errorf("sign-in with %s said %+v", name, e)
		}
	}

	// The sign-ins for tenant one are in tenant one's log: one ok, two refused.
	var ok, refused int
	for _, e := range f.entries(f.one) {
		if e.Action == "POST /api/v1/session" && e.Outcome == audit.OutcomeOK {
			ok++
		}
		if e.Action == "POST /api/v1/session" && e.Outcome == audit.OutcomeRefused && e.Status == 401 {
			refused++
		}
		if raw, _ := json.Marshal(e); strings.Contains(string(raw), passwordOne) || strings.Contains(string(raw), token) {
			t.Errorf("an audit entry holds the password or the token: %s", raw)
		}
	}
	if ok != 1 || refused != 2 {
		t.Errorf("tenant one's log has %d good sign-ins and %d refused, want 1 and 2", ok, refused)
	}
	if n := len(f.entries(f.two)); n != 0 {
		t.Errorf("tenant two's log has %d entries from tenant one's sign-ins", n)
	}
}

func TestSignInWaitsAfterWrongPasswords(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 3; i++ {
		f.signIn(f.one, "not the phrase at all")
	}
	_, rec := f.signIn(f.one, passwordOne)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("after three wrong passwords: status %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if token, rec := f.signIn(f.two, passwordTwo); token == "" {
		t.Errorf("tenant one's wrong passwords kept tenant two out: %d", rec.Code)
	}
}

func TestAHandlerIsGivenItsSessionsTenantAndNoOther(t *testing.T) {
	f := newFixture(t)
	one, _ := f.signIn(f.one, passwordOne)
	two, _ := f.signIn(f.two, passwordTwo)
	before := len(f.entries(f.two))

	rec := f.do("GET", Prefix+"probe/abc?tenant="+f.two, one, "", "X-Tenant", f.two)
	var got map[string]string
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 200 || got["tenant"] != f.one || got["session"] != f.one || got["id"] != "abc" {
		t.Fatalf("with tenant one's session the handler was given %v (status %d)", got, rec.Code)
	}
	if got["request_id"] == "" || got["request_id"] != rec.Header().Get("X-Request-Id") {
		t.Errorf("the handler saw request %q and the header says %q", got["request_id"], rec.Header().Get("X-Request-Id"))
	}
	// The call is in tenant one's log, started and ended, and not in two's.
	mine, err := func() ([]audit.Entry, error) {
		s, err := f.in.Store(f.one)
		if err != nil {
			return nil, err
		}
		defer s.Close()
		return audit.List(context.Background(), s, audit.Filter{RequestID: got["request_id"]})
	}()
	if err != nil || len(mine) != 2 || mine[0].Outcome != audit.OutcomeStarted || mine[1].Outcome != audit.OutcomeOK ||
		mine[0].Action != "GET /api/v1/probe/abc" || mine[0].Actor != ActorPerson {
		t.Errorf("tenant one's log for the call: %+v (%v)", mine, err)
	}
	if n := len(f.entries(f.two)); n != before {
		t.Errorf("tenant one's call added %d entries to tenant two's log", n-before)
	}

	// The token re-addressed to tenant two, and tenant two's own.
	forged := strings.Replace(one, f.one, f.two, 1)
	if rec := f.do("GET", Prefix+"probe/abc", forged, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("a token re-addressed to tenant two: status %d, want 401", rec.Code)
	}
	rec = f.do("GET", Prefix+"probe/abc", two, "")
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got["tenant"] != f.two {
		t.Errorf("with tenant two's session the handler was given %v", got)
	}
}

func TestSignOutEndsTheSession(t *testing.T) {
	f := newFixture(t)
	token, _ := f.signIn(f.one, passwordOne)
	rec := f.do("DELETE", Prefix+"session", token, "")
	if rec.Code != 200 {
		t.Fatalf("sign-out: status %d %s", rec.Code, rec.Body.String())
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		cleared = cleared || (c.Name == CookieName && c.Value == "" && c.MaxAge < 0)
	}
	if !cleared {
		t.Errorf("sign-out did not tell the browser to forget the cookie")
	}
	if rec := f.do("GET", Prefix+"session", token, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("a signed-out session: status %d, want 401", rec.Code)
	}
}

func TestAWriteFromAnotherSiteIsRefused(t *testing.T) {
	f := newFixture(t)
	token, _ := f.signIn(f.one, passwordOne)
	rec := f.do("DELETE", Prefix+"session", token, "", "Sec-Fetch-Site", "cross-site")
	if rec.Code != http.StatusForbidden || errorOf(t, rec).Code != "cross_origin" {
		t.Errorf("a write from another site: status %d %s", rec.Code, rec.Body.String())
	}
	rec = f.do("DELETE", Prefix+"session", token, "", "Origin", "https://elsewhere.example")
	if rec.Code != http.StatusForbidden {
		t.Errorf("a write with another site's origin: status %d", rec.Code)
	}
	if rec := f.do("GET", Prefix+"session", token, ""); rec.Code != 200 {
		t.Errorf("the refused writes signed the person out: %d", rec.Code)
	}
}

func TestBodiesAreLimitedAndChecked(t *testing.T) {
	f := newFixture(t)
	token, _ := f.signIn(f.one, passwordOne)
	big := `{"note":"` + strings.Repeat("x", 2000) + `"}`
	if rec := f.do("POST", Prefix+"probe/echo", token, big); rec.Code != http.StatusRequestEntityTooLarge || errorOf(t, rec).Code != "too_large" {
		t.Errorf("a body over the limit: status %d", rec.Code)
	}
	for _, bad := range []string{`{"note":`, `{"nope":"x"}`, `{"note":"a"}{"note":"b"}`, `[]`} {
		if rec := f.do("POST", Prefix+"probe/echo", token, bad); rec.Code != http.StatusBadRequest || errorOf(t, rec).Code != "bad_request" {
			t.Errorf("the body %s: status %d", bad, rec.Code)
		}
	}
	if rec := f.do("POST", Prefix+"probe/echo", token, `{"note":"four"}`); rec.Code != 200 {
		t.Errorf("a good body: status %d", rec.Code)
	}
}

func TestAPanicIsAnsweredAndRecorded(t *testing.T) {
	f := newFixture(t)
	token, _ := f.signIn(f.one, passwordOne)
	rec := f.do("POST", Prefix+"probe/panic", token, "")
	if rec.Code != http.StatusInternalServerError || errorOf(t, rec).Code != "failed" {
		t.Errorf("a handler that panics: status %d %s", rec.Code, rec.Body.String())
	}
	all := f.entries(f.one)
	if last := all[len(all)-1]; last.Outcome != audit.OutcomeError || last.Action != "POST /api/v1/probe/panic" {
		t.Errorf("the panic was recorded as %+v", last)
	}
	if !strings.Contains(f.log.String(), "panicked") {
		t.Errorf("the server's log does not say the handler panicked")
	}
}

func TestTheLogNamesEachCallAndHoldsNoToken(t *testing.T) {
	f := newFixture(t)
	token, _ := f.signIn(f.one, passwordOne)
	rec := f.do("GET", Prefix+"probe/abc?note=a+private+note", token, "", "X-Note", "another private note")
	f.do("GET", Prefix+"probe/"+token, token, "")
	log := f.log.String()
	if !strings.Contains(log, rec.Header().Get("X-Request-Id")+" GET /api/v1/probe/abc 200") || !strings.Contains(log, "tenant="+f.one) {
		t.Errorf("the log does not name the call:\n%s", log)
	}
	for _, private := range []string{token, passwordOne, "private"} {
		if strings.Contains(log, private) {
			t.Errorf("the log holds %q:\n%s", private, log)
		}
	}
}

func TestTheAuditRouteReadsAPageAtATime(t *testing.T) {
	f := newFixture(t)
	token, _ := f.signIn(f.one, passwordOne)
	other, _ := f.signIn(f.two, passwordTwo)
	for i := 0; i < 4; i++ {
		f.do("GET", Prefix+"probe/abc", token, "")
	}
	var seen int
	var after int64
	for page := 0; page < 20; page++ {
		rec := f.do("GET", Prefix+"audit?after="+jsonNumber(after), token, "")
		var reply AuditReply
		if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || rec.Code != 200 {
			t.Fatalf("the audit route: status %d (%v) %s", rec.Code, err, rec.Body.String())
		}
		if len(reply.Entries) > 5 {
			t.Fatalf("a page of %d entries with a page limit of 5", len(reply.Entries))
		}
		seen += len(reply.Entries)
		if reply.NextAfter == 0 {
			break
		}
		after = reply.NextAfter
	}
	// One sign-in and four calls of two entries each, and then the reads.
	if seen < 9 {
		t.Errorf("the pages held %d entries, want 9 or more", seen)
	}
	rec := f.do("GET", Prefix+"audit?limit=500", other, "")
	var reply AuditReply
	json.Unmarshal(rec.Body.Bytes(), &reply)
	for _, e := range reply.Entries {
		if strings.Contains(e.Action, "probe") {
			t.Errorf("tenant two read tenant one's entry: %+v", e)
		}
	}
	for _, bad := range []string{"after=x", "limit=0", "kind=other"} {
		if rec := f.do("GET", Prefix+"audit?"+bad, token, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("audit?%s: status %d, want 400", bad, rec.Code)
		}
	}
}

func jsonNumber(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

func TestAStreamArrivesAsItIsSent(t *testing.T) {
	f := newFixture(t)
	token, _ := f.signIn(f.one, passwordOne)
	srv := httptest.NewServer(f.h)
	defer srv.Close()

	get := func(path, last string) (*http.Response, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+path, nil)
		req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
		if last != "" {
			req.Header.Set("Last-Event-ID", last)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp, cancel
	}

	resp, cancel := get(Prefix+"stream/test?count=2", "")
	raw := new(strings.Builder)
	bufio.NewReader(resp.Body).WriteTo(raw)
	cancel()
	want := "id: 1\nevent: tick\ndata: {\"n\":1}\n\nid: 2\nevent: tick\ndata: {\"n\":2}\n\nevent: done\ndata: {\"n\":2}\n\n"
	if raw.String() != want || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("X-Request-Id") == "" {
		t.Errorf("the test stream sent %q with headers %v", raw.String(), resp.Header)
	}

	// A client that saw event 1 asks for the rest.
	resp, cancel = get(Prefix+"stream/test?count=2", "1")
	raw.Reset()
	bufio.NewReader(resp.Body).WriteTo(raw)
	cancel()
	if strings.Contains(raw.String(), "id: 1\n") || !strings.Contains(raw.String(), "id: 2\n") {
		t.Errorf("after Last-Event-ID 1 the stream sent %q", raw.String())
	}

	// A quiet stream sends its first event before it ends, then heartbeats.
	resp, cancel = get(Prefix+"probe/quiet", "")
	lines := bufio.NewReader(resp.Body)
	var hello, beat bool
	for !(hello && beat) {
		line, err := lines.ReadString('\n')
		if err != nil {
			t.Fatalf("the quiet stream ended early (hello %v, beat %v): %v", hello, beat, err)
		}
		hello = hello || line == "event: hello\n"
		beat = beat || line == ": beat\n"
	}
	cancel() // the client leaves
	resp.Body.Close()

	// The call's end is recorded although the client left.
	deadline := time.Now().Add(5 * time.Second)
	for {
		all := f.entries(f.one)
		last := all[len(all)-1]
		if last.Action == "GET /api/v1/probe/quiet" && last.Outcome == audit.OutcomeOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stream a client left was not recorded as ended: %+v", last)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAnEventIsOneLineEach(t *testing.T) {
	rec := httptest.NewRecorder()
	s, err := OpenStream(rec, httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(Event{Name: "two\nlines", Data: 1}); err == nil {
		t.Errorf("an event name with a line end was sent")
	}
	if err := s.Send(Event{Name: "text", Data: "one\ntwo"}); err != nil {
		t.Error(err)
	}
	s.Close()
	s.Close() // closing twice is harmless
	if err := s.Send(Event{Name: "late", Data: 1}); err == nil {
		t.Errorf("an event was sent on a closed stream")
	}
	if got := rec.Body.String(); got != "event: text\ndata: \"one\\ntwo\"\n\n" {
		t.Errorf("the stream holds %q", got)
	}
}

func TestCheckRefusesABadRoute(t *testing.T) {
	handle := func(http.ResponseWriter, *http.Request, Call) {}
	open := func(http.ResponseWriter, *http.Request) {}
	good := Route{Method: "GET", Path: Prefix + "things/{id}", Summary: "A thing.", Handle: handle}
	if err := Check([]Route{good}); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]Route{
		"outside the prefix":        {{Method: "GET", Path: "/things", Summary: "x", Handle: handle}},
		"a method that is not one":  {{Method: "FETCH", Path: Prefix + "things", Summary: "x", Handle: handle}},
		"listed twice":              {good, good},
		"no summary":                {{Method: "GET", Path: Prefix + "things", Handle: handle}},
		"no handler":                {{Method: "GET", Path: Prefix + "things", Summary: "x"}},
		"open without being a door": {{Method: "GET", Path: Prefix + "things", Summary: "x", Open: open}},
		"a door with a handler":     {{Method: "GET", Path: Prefix + "things", Summary: "x", Door: true, Open: open, Handle: handle}},
		"a door with no handler":    {{Method: "GET", Path: Prefix + "things", Summary: "x", Door: true}},
	} {
		if err := Check(bad); err == nil {
			t.Errorf("a route %s was accepted", name)
		}
		if _, err := Handler(Options{Install: &tenant.Install{}, MaxBodyBytes: 1, Heartbeat: time.Second}, bad); err == nil {
			t.Errorf("a handler was built with a route %s", name)
		}
	}
}

func TestTheDocumentDescribesTheRoutes(t *testing.T) {
	f := newFixture(t)
	raw, err := Document(f.routes, "test")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Document(f.routes, "test")
	if !bytes.Equal(raw, again) {
		t.Errorf("the document differs from one build to the next")
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Summary    string           `json:"summary"`
			Security   *[]any           `json:"security"`
			Parameters []map[string]any `json:"parameters"`
			Responses  map[string]struct {
				Content map[string]map[string]any `json:"content"`
			} `json:"responses"`
			RequestBody map[string]any `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, rt := range f.routes {
		op, ok := doc.Paths[rt.Path][strings.ToLower(rt.Method)]
		if !ok || op.Summary != rt.Summary {
			t.Errorf("%s %s is not in the document", rt.Method, rt.Path)
		}
		if door := op.Security != nil && len(*op.Security) == 0; door != rt.Door {
			t.Errorf("%s %s: the document says door=%v and the route says %v", rt.Method, rt.Path, door, rt.Door)
		}
	}
	if doc.Paths[Prefix+"session"]["post"].RequestBody == nil {
		t.Errorf("sign-in has no request body in the document")
	}
	if p := doc.Paths[Prefix+"probe/{id}"]["get"].Parameters; len(p) != 1 || p[0]["name"] != "id" || p[0]["in"] != "path" {
		t.Errorf("the path parameter is described as %v", p)
	}
	stream := doc.Paths[Prefix+"stream/test"]["get"].Responses["200"].Content["text/event-stream"]
	if events, _ := stream["x-keystone-events"].([]any); len(events) != 2 {
		t.Errorf("the test stream's events are described as %v", stream)
	}
	// The same document comes from the route, to a signed-in caller.
	token, _ := f.signIn(f.one, passwordOne)
	if rec := f.do("GET", Prefix+"openapi.json", token, ""); !bytes.Equal(rec.Body.Bytes(), raw) {
		t.Errorf("the route serves a different document")
	}
}

func TestSchemaFollowsTheJSONTags(t *testing.T) {
	type inner struct {
		When time.Time `json:"when"`
	}
	type outer struct {
		Name   string          `json:"name"`
		Count  int             `json:"count,omitempty"`
		Parts  []inner         `json:"parts"`
		Labels map[string]bool `json:"labels"`
		Raw    json.RawMessage `json:"raw"`
		Next   *outer          `json:"next,omitempty"`
		Hidden string          `json:"-"`
		secret string
	}
	raw, _ := json.Marshal(schemaOf(reflect.TypeOf(outer{}), 0))
	got := string(raw)
	for _, want := range []string{`"required":["name","parts","labels","raw"]`, `"format":"date-time"`, `"additionalProperties":{"type":"boolean"}`, `"count":{"type":"integer"}`} {
		if !strings.Contains(got, want) {
			t.Errorf("the schema lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Hidden") || strings.Contains(got, "secret") {
		t.Errorf("the schema shows a field JSON does not: %s", got)
	}
	_ = outer{}.secret
}
