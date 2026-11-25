package audit

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// testAPI is a small stand-in for the API shell: a mux with a few routes,
// wrapped once by Wrap. A request names its tenant in the X-Test-Session
// header, which stands in for the session P2-02 builds.
type testAPI struct {
	srv    *httptest.Server
	served atomic.Int64 // how many times a route's handler ran
	a, b   *tenant.Store
}

func newTestAPI(t *testing.T, routes func(mux *http.ServeMux)) *testAPI {
	t.Helper()
	_, a, b := twoStores(t)
	api := &testAPI{a: a, b: b}
	who := func(r *http.Request) (Caller, error) {
		switch r.Header.Get("X-Test-Session") {
		case "one":
			return Caller{Store: a, Actor: "person"}, nil
		case "two":
			return Caller{Store: b, Actor: "person"}, nil
		}
		return Caller{}, errors.New("no session")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/chats", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"chats":[]}`)
	})
	mux.HandleFunc("POST /api/chats", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /api/chats/{id}", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "there is no such chat", http.StatusNotFound)
	})
	mux.HandleFunc("DELETE /api/chats/{id}", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not allowed", http.StatusForbidden)
	})
	mux.HandleFunc("GET /api/broken", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "it broke", http.StatusInternalServerError)
	})
	mux.HandleFunc("GET /api/quiet", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("GET /api/request-id", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, RequestID(r.Context()))
	})
	if routes != nil {
		routes(mux)
	}
	counted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.served.Add(1)
		mux.ServeHTTP(w, r)
	})
	api.srv = httptest.NewServer(Wrap(who, counted))
	t.Cleanup(api.srv.Close)
	return api
}

// call makes one request as the named session and returns the status, the
// body and the request id the server answered with.
func (api *testAPI) call(t *testing.T, session, method, path string, body string, headers ...string) (status int, text, requestID string) {
	t.Helper()
	req, err := http.NewRequest(method, api.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if session != "" {
		req.Header.Set("X-Test-Session", session)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := api.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(raw), resp.Header.Get("X-Request-Id")
}

func TestEveryAPICallLandsInTheAuditLog(t *testing.T) {
	api := newTestAPI(t, nil)
	calls := []struct {
		method, path string
		status       int
		outcome      Outcome
	}{
		{"GET", "/api/chats", 200, OutcomeOK},
		{"POST", "/api/chats", 201, OutcomeOK},
		{"GET", "/api/chats/c1", 404, OutcomeRefused},
		{"DELETE", "/api/chats/c1", 403, OutcomeRefused},
		{"GET", "/api/broken", 500, OutcomeError},
		{"GET", "/api/quiet", 200, OutcomeOK},
		{"GET", "/api/no-such-route", 404, OutcomeRefused},
		{"PUT", "/api/chats", 405, OutcomeRefused},
	}
	var ids []string
	for _, c := range calls {
		status, _, id := api.call(t, "one", c.method, c.path, "")
		if status != c.status {
			t.Fatalf("%s %s: status %d, want %d", c.method, c.path, status, c.status)
		}
		if id == "" {
			t.Fatalf("%s %s: the answer names no request id", c.method, c.path)
		}
		ids = append(ids, id)
	}

	log := mustList(t, api.a, Filter{})
	if len(log) != 2*len(calls) {
		t.Fatalf("%d calls left %d entries, want two for each: %+v", len(calls), len(log), log)
	}
	seen := map[string]bool{}
	for i, c := range calls {
		started, ended := log[2*i], log[2*i+1]
		action := c.method + " " + c.path
		for _, e := range []Entry{started, ended} {
			if e.Kind != KindAPI || e.Actor != "person" || e.Action != action || e.RequestID != ids[i] || e.At.IsZero() {
				t.Errorf("%s: the entry is %+v", action, e)
			}
		}
		if started.Outcome != OutcomeStarted || started.Status != 0 {
			t.Errorf("%s: the first entry is not a started one: %+v", action, started)
		}
		if ended.Outcome != c.outcome || ended.Status != c.status {
			t.Errorf("%s: ended as %s %d, want %s %d", action, ended.Outcome, ended.Status, c.outcome, c.status)
		}
		if seen[ids[i]] {
			t.Errorf("request id %s was used twice", ids[i])
		}
		seen[ids[i]] = true
		if got := mustList(t, api.a, Filter{RequestID: ids[i]}); len(got) != 2 {
			t.Errorf("%s: %d entries carry its request id, want 2", action, len(got))
		}
	}

	// One tenant's calls are in that tenant's log and no other.
	if other := mustList(t, api.b, Filter{}); len(other) != 0 {
		t.Errorf("tenant two's log holds tenant one's calls: %+v", other)
	}
	api.call(t, "two", "GET", "/api/chats", "")
	if got := mustList(t, api.b, Filter{}); len(got) != 2 {
		t.Errorf("tenant two's own call left %d entries, want 2", len(got))
	}
	if got := mustList(t, api.a, Filter{}); len(got) != 2*len(calls) {
		t.Errorf("tenant two's call reached tenant one's log")
	}
}

func TestTheHandlerSeesTheRequestID(t *testing.T) {
	api := newTestAPI(t, nil)
	_, body, id := api.call(t, "one", "GET", "/api/request-id", "")
	if body == "" || body != id {
		t.Errorf("the handler saw request id %q and the answer carries %q", body, id)
	}
	// A request id the client sends is not taken as the call's id.
	_, body, id = api.call(t, "one", "GET", "/api/request-id", "", "X-Request-Id", "chosen-by-the-client")
	if body == "chosen-by-the-client" || id == "chosen-by-the-client" {
		t.Error("the client chose the request id")
	}
}

func TestWrapKeepsARequestIDAlreadySet(t *testing.T) {
	_, a, _ := twoStores(t)
	who := func(*http.Request) (Caller, error) { return Caller{Store: a, Actor: "admin"}, nil }
	h := Wrap(who, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "/api/chats", nil)
	req = req.WithContext(WithRequestID(req.Context(), "set-by-the-shell"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	log := mustList(t, a, Filter{RequestID: "set-by-the-shell"})
	if len(log) != 2 || log[0].Actor != "admin" || rec.Header().Get("X-Request-Id") != "set-by-the-shell" {
		t.Errorf("the request id already set was not kept: %+v", log)
	}
}

func TestACallWithNoSessionIsNotServed(t *testing.T) {
	api := newTestAPI(t, nil)
	for _, session := range []string{"", "three"} {
		status, _, _ := api.call(t, session, "GET", "/api/chats", "")
		if status != http.StatusUnauthorized {
			t.Errorf("session %q: status %d, want 401", session, status)
		}
	}
	if n := api.served.Load(); n != 0 {
		t.Errorf("a route ran %d times with no session", n)
	}
	for _, s := range []*tenant.Store{api.a, api.b} {
		if got := mustList(t, s, Filter{}); len(got) != 0 {
			t.Errorf("a call with no session is in a tenant's log: %+v", got)
		}
	}
}

func TestACallThatCannotBeRecordedIsNotServed(t *testing.T) {
	api := newTestAPI(t, nil)
	if status, _, _ := api.call(t, "one", "GET", "/api/chats", ""); status != 200 {
		t.Fatalf("the first call: status %d", status)
	}
	ran := api.served.Load()

	// The log can no longer be written.
	if err := api.a.Close(); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]string{{"GET", "/api/chats"}, {"POST", "/api/chats"}} {
		status, body, id := api.call(t, "one", c[0], c[1], "")
		if status != http.StatusInternalServerError {
			t.Errorf("%s %s with no log: status %d, want 500", c[0], c[1], status)
		}
		if !strings.Contains(body, "not run") || !strings.Contains(body, id) {
			t.Errorf("%s %s: the answer does not say the call was not run: %s", c[0], c[1], body)
		}
	}
	if got := api.served.Load(); got != ran {
		t.Errorf("a route ran %d times though its call could not be recorded", got-ran)
	}
	// The other tenant is not affected.
	if status, _, _ := api.call(t, "two", "GET", "/api/chats", ""); status != 200 {
		t.Errorf("tenant two: status %d, want 200", status)
	}
}

func TestNoCredentialFromARequestReachesTheLog(t *testing.T) {
	api := newTestAPI(t, nil)
	keys := madeUpKeys()
	status, _, _ := api.call(t, "one", "POST", "/api/chats?access_token=words-in-the-query&k="+keys["tavily"],
		`{"password":"words-in-the-body","key":"`+keys["anthropic"]+`"}`,
		"Authorization", "Bearer "+keys["openai"],
		"Cookie", "session=words-in-the-cookie",
		"X-Api-Key", keys["openrouter"],
	)
	if status != http.StatusCreated {
		t.Fatalf("status %d, want 201", status)
	}
	// A key in the path itself is removed by its shape.
	api.call(t, "one", "GET", "/api/chats/"+keys["github"], "")

	raw := rawLog(t, api.a)
	for name, key := range keys {
		for _, n := range needles(key) {
			if strings.Contains(raw, n) {
				t.Errorf("the %s key reached the log", name)
			}
		}
	}
	for _, words := range []string{"words-in-the-query", "words-in-the-body", "words-in-the-cookie", "access_token"} {
		if strings.Contains(raw, words) {
			t.Errorf("%s reached the log", words)
		}
	}
	if !strings.Contains(raw, "POST /api/chats\n") || !strings.Contains(raw, "GET /api/chats/"+removed) {
		t.Errorf("the calls themselves are not in the log:\n%s", raw)
	}
}

func TestAStreamPassesThroughAndIsRecorded(t *testing.T) {
	release := make(chan struct{})
	api := newTestAPI(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/stream", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: one\n\n")
			w.(http.Flusher).Flush()
			<-release
			fmt.Fprint(w, "data: two\n\n")
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Errorf("the response controller cannot flush through the wrapper: %v", err)
			}
		})
	})
	req, _ := http.NewRequest("GET", api.srv.URL+"/api/stream", nil)
	req.Header.Set("X-Test-Session", "one")
	resp, err := api.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewReader(resp.Body)

	// The first event arrives while the handler is still running.
	first, err := lines.ReadString('\n')
	if err != nil || first != "data: one\n" {
		t.Fatalf("the first event: %q, %v", first, err)
	}
	if log := mustList(t, api.a, Filter{}); len(log) != 1 || log[0].Outcome != OutcomeStarted {
		t.Errorf("a stream in flight should have its started entry and no other: %+v", log)
	}
	close(release)
	rest, err := io.ReadAll(lines)
	if err != nil || !strings.Contains(string(rest), "data: two") {
		t.Fatalf("the rest of the stream: %q, %v", rest, err)
	}
	log := waitForEntries(t, api.a, 2)
	if log[1].Outcome != OutcomeOK || log[1].Status != 200 || log[1].Action != "GET /api/stream" {
		t.Errorf("the stream ended as %+v", log[1])
	}
}

func TestACallIsRecordedWhenTheClientLeaves(t *testing.T) {
	api := newTestAPI(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/slow", func(w http.ResponseWriter, r *http.Request) {
			w.(http.Flusher).Flush()
			<-r.Context().Done() // the client has gone
		})
	})
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", api.srv.URL+"/api/slow", nil)
	req.Header.Set("X-Test-Session", "one")
	resp, err := api.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()

	log := waitForEntries(t, api.a, 2)
	if log[0].Outcome != OutcomeStarted || log[1].Outcome != OutcomeOK || log[1].RequestID != log[0].RequestID {
		t.Errorf("the call was not recorded to its end: %+v", log)
	}
}

func TestAPanicIsRecordedAsAnError(t *testing.T) {
	api := newTestAPI(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/panic", func(w http.ResponseWriter, r *http.Request) {
			panic(http.ErrAbortHandler) // the one panic net/http does not print
		})
	})
	req, _ := http.NewRequest("GET", api.srv.URL+"/api/panic", nil)
	req.Header.Set("X-Test-Session", "one")
	if resp, err := api.srv.Client().Do(req); err == nil {
		resp.Body.Close()
		t.Error("a handler that panicked gave an answer")
	}
	log := waitForEntries(t, api.a, 2)
	if log[1].Outcome != OutcomeError || log[1].Status != 500 || log[1].Received == "" {
		t.Errorf("the panic was recorded as %+v", log[1])
	}
}

func TestACallThatCannotBeFinishedKeepsItsStartedEntry(t *testing.T) {
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tn, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	who := func(*http.Request) (Caller, error) { return Caller{Store: s, Actor: "person"}, nil }
	h := Wrap(who, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Close() // the log goes away while the call runs
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/chats", nil))

	again, err := in.Store(tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	log := mustList(t, again, Filter{})
	if len(log) != 1 || log[0].Outcome != OutcomeStarted || log[0].Action != "POST /api/chats" {
		t.Errorf("want the started entry alone, got %+v", log)
	}
}

// waitForEntries waits for the log to hold n entries. The entry that ends a
// call is written after the answer is sent, so a client can be a moment
// ahead of it.
func waitForEntries(t *testing.T, s *tenant.Store, n int) []Entry {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		log := mustList(t, s, Filter{})
		if len(log) >= n {
			if len(log) > n {
				t.Fatalf("the log holds %d entries, want %d: %+v", len(log), n, log)
			}
			return log
		}
		if time.Now().After(deadline) {
			t.Fatalf("the log holds %d entries after five seconds, want %d: %+v", len(log), n, log)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
