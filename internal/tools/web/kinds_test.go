package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

// The proof of Brave, Exa, Serper and SearXNG (P7-07). Every provider is a
// stand-in served on this machine by httptest, answering in the shape its
// documentation gives (decisions 0750 to 0753). Nothing calls a real search
// service, and every key is invented.

// seen is one request a stand-in was sent.
type seen struct {
	method, path, query, body string
	header                    http.Header
}

// stand serves h on this machine and keeps the last request it was sent.
func stand(t *testing.T, h http.HandlerFunc) (*httptest.Server, *atomic.Value, *atomic.Int32) {
	t.Helper()
	var last atomic.Value
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		last.Store(seen{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: string(raw), header: r.Header.Clone()})
		calls.Add(1)
		h(rw, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &last, &calls
}

// adapterFor makes one kind's adapter for an entry at endpoint, with the
// tests' options.
func adapterFor(kind, endpoint string, edit ...func(*Provider)) Adapter {
	p := Provider{Name: "one", Kind: kind, Endpoint: endpoint, Enabled: true}
	for _, e := range edit {
		e(&p)
	}
	o := testOptions("http://127.0.0.1:9/search")
	o.Now = func() time.Time { return start }
	return kinds[kind].New(p, o)
}

// The four answers below are shaped as each provider's documentation shows
// them. Each holds two results Keystone keeps and three it drops: an address
// that is not http, a result with no extract, and a title that is not text.

const braveGood = `{"type":"search","query":{"original":"harbour museum"},"web":{"type":"search","results":[
 {"title":"Harbour Museum: <strong>opening</strong> hours","url":"https://museum.example/hours","description":"Open from ten, <strong>Tuesday</strong> to Sunday &amp; holidays.","language":"en"},
 {"title":"A script","url":"javascript:alert(1)","description":"Not a page."},
 {"title":"No extract","url":"https://empty.example/","description":""},
 {"title":7,"url":"https://number.example/","description":"A title that is a number."},
 {"title":"Visiting the harbour","url":"https://visit.example/harbour","description":"The museum is closed on Mondays."}]}}`

const exaGood = `{"requestId":"req-invented","results":[
 {"id":"https://museum.example/hours","title":"Harbour Museum: opening hours","url":"https://museum.example/hours","publishedDate":"2026-01-01T00:00:00.000Z","highlights":["Open from ten,","Tuesday to Sunday."],"highlightScores":[0.5,0.4]},
 {"title":"A script","url":"javascript:alert(1)","highlights":["Not a page."]},
 {"title":"No extract","url":"https://empty.example/","highlights":[]},
 {"title":null,"url":"https://number.example/","highlights":["A title that is null."]},
 {"title":"Visiting the harbour","url":"https://visit.example/harbour","summary":"The museum is closed on Mondays."}],
 "searchTime":120,"costDollars":{"total":0.005}}`

const serperGood = `{"searchParameters":{"q":"harbour museum","type":"search","engine":"google"},"organic":[
 {"title":"Harbour Museum: opening hours","link":"https://museum.example/hours","snippet":"Open from ten, Tuesday to Sunday.","position":1},
 {"title":"A script","link":"javascript:alert(1)","snippet":"Not a page.","position":2},
 {"title":"No extract","link":"https://empty.example/","snippet":"","position":3},
 {"title":["a list"],"link":"https://number.example/","snippet":"A title that is a list.","position":4},
 {"title":"Visiting the harbour","link":"https://visit.example/harbour","snippet":"The museum is closed on Mondays.","position":5}],"credits":1}`

const searxngGood = `{"query":"harbour museum","number_of_results":0,"results":[
 {"url":"https://museum.example/hours","title":"Harbour Museum: opening hours","content":"Open from ten, Tuesday to Sunday.","engine":"duckduckgo","engines":["duckduckgo"],"score":1.5,"category":"general"},
 {"url":"javascript:alert(1)","title":"A script","content":"Not a page.","engine":"bing"},
 {"url":"https://empty.example/","title":"No extract","content":"","engine":"bing"},
 {"url":"https://number.example/","title":null,"content":"A title that is null.","engine":"bing"},
 {"url":"https://visit.example/harbour","title":"Visiting the harbour","content":"The museum is closed on Mondays.","engine":"wikipedia"}],
 "answers":[],"corrections":[],"infoboxes":[],"suggestions":[],"unresponsive_engines":[["google","timeout"]]}`

// wantTwo checks the two results every good answer above gives.
func wantTwo(t *testing.T, kind string, got []Result) {
	t.Helper()
	if len(got) != 2 || got[0].URL != "https://museum.example/hours" || got[1].URL != "https://visit.example/harbour" {
		t.Fatalf("%s gave %+v, want the museum's hours and the visit page", kind, got)
	}
	if strings.ContainsAny(got[0].Title+got[0].Content, "<>") || strings.Contains(got[0].Content, "&amp;") {
		t.Errorf("%s left markup in %+v", kind, got[0])
	}
	if got[1].Content != "The museum is closed on Mondays." {
		t.Errorf("%s's second extract is %q", kind, got[1].Content)
	}
}

func TestBravesRequestAndResults(t *testing.T) {
	srv, last, _ := stand(t, func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("X-RateLimit-Limit", "1, 15000")
		rw.Header().Set("X-RateLimit-Policy", "1;w=1, 15000;w=2592000")
		rw.Header().Set("X-RateLimit-Remaining", "0, 1000")
		rw.Header().Set("X-RateLimit-Reset", "1, 1419704")
		fmt.Fprint(rw, braveGood)
	})
	ans, err := adapterFor(KindBrave, srv.URL).Search(context.Background(), "invented-brave-key", Query{Text: "harbour museum", Count: 5})
	if err != nil {
		t.Fatal(err)
	}
	r := last.Load().(seen)
	q, _ := url.ParseQuery(r.query)
	if r.method != http.MethodGet || r.path != "/res/v1/web/search" || q.Get("q") != "harbour museum" || q.Get("count") != "5" {
		t.Errorf("Brave was sent %s %s?%s", r.method, r.path, r.query)
	}
	if r.header.Get("X-Subscription-Token") != "invented-brave-key" || r.header.Get("Authorization") != "" || r.header.Get("Accept") != "application/json" {
		t.Errorf("Brave was sent the headers %v", r.header)
	}
	if strings.Contains(r.query, "invented-brave-key") {
		t.Error("the key was put in the address")
	}
	wantTwo(t, KindBrave, ans.Results)
	if ans.Results[0].Title != "Harbour Museum: opening hours" || ans.Results[0].Content != "Open from ten, Tuesday to Sunday & holidays." {
		t.Errorf("Brave's markup was read as %+v", ans.Results[0])
	}
	if !ans.Quota.Known || ans.Quota.Left != 1000 || !ans.Quota.Reset.Equal(start.Add(1419704*time.Second)) {
		t.Errorf("Brave's headers were read as %+v, want 1000 left until %s", ans.Quota, start.Add(1419704*time.Second))
	}
	// More than Brave gives is asked as its most.
	if _, err := adapterFor(KindBrave, srv.URL).Search(context.Background(), "k", Query{Text: "x", Count: 50}); err != nil {
		t.Fatal(err)
	}
	if q, _ := url.ParseQuery(last.Load().(seen).query); q.Get("count") != "20" {
		t.Errorf("a count of 50 was sent as %q, want 20", q.Get("count"))
	}
}

func TestBravesQuotaHeaders(t *testing.T) {
	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			out.Set(kv[i], kv[i+1])
		}
		return out
	}
	month := start.Add(2000 * time.Second)
	for _, c := range []struct {
		name string
		h    http.Header
		want Quota
	}{
		{"two windows, policy", h("X-RateLimit-Policy", "1;w=1, 15000;w=2592000", "X-RateLimit-Remaining", "1, 700", "X-RateLimit-Reset", "1, 2000"), Quota{Left: 700, Known: true, Reset: month}},
		{"the month listed first", h("X-RateLimit-Policy", "15000;w=2592000, 1;w=1", "X-RateLimit-Remaining", "700, 1", "X-RateLimit-Reset", "2000, 1"), Quota{Left: 700, Known: true, Reset: month}},
		{"no policy: the last window", h("X-RateLimit-Remaining", "1, 700", "X-RateLimit-Reset", "1, 2000"), Quota{Left: 700, Known: true, Reset: month}},
		{"only a second's window", h("X-RateLimit-Policy", "1;w=1", "X-RateLimit-Remaining", "0", "X-RateLimit-Reset", "1"), Quota{}},
		{"no headers", h(), Quota{}},
		{"not numbers", h("X-RateLimit-Remaining", "one, many"), Quota{}},
	} {
		if got := braveQuota(c.h, start); got != c.want {
			t.Errorf("%s: read as %+v, want %+v", c.name, got, c.want)
		}
	}
	if got := braveWait(h("X-RateLimit-Remaining", "0, 700", "X-RateLimit-Reset", "1, 2000")); got != time.Second {
		t.Errorf("a spent second's window waits %s, want 1s", got)
	}
}

func TestExasRequestAndResults(t *testing.T) {
	srv, last, _ := stand(t, func(rw http.ResponseWriter, _ *http.Request) { fmt.Fprint(rw, exaGood) })
	ans, err := adapterFor(KindExa, srv.URL).Search(context.Background(), "invented-exa-key", Query{Text: "harbour museum", Count: 4})
	if err != nil {
		t.Fatal(err)
	}
	r := last.Load().(seen)
	if r.method != http.MethodPost || r.path != "/search" || r.header.Get("x-api-key") != "invented-exa-key" || r.header.Get("Authorization") != "" || r.header.Get("Content-Type") != "application/json" {
		t.Errorf("Exa was sent %s %s with %v", r.method, r.path, r.header)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(r.body), &body); err != nil {
		t.Fatal(err)
	}
	contents, _ := body["contents"].(map[string]any)
	highlights, _ := contents["highlights"].(map[string]any)
	if body["query"] != "harbour museum" || body["numResults"] != float64(4) || highlights["maxCharacters"] != float64(1400) || len(body) != 3 {
		t.Errorf("Exa was sent %s", r.body)
	}
	wantTwo(t, KindExa, ans.Results)
	if ans.Results[0].Content != "Open from ten, … Tuesday to Sunday." {
		t.Errorf("Exa's highlights were read as %q", ans.Results[0].Content)
	}
}

func TestSerpersRequestAndResults(t *testing.T) {
	srv, last, _ := stand(t, func(rw http.ResponseWriter, _ *http.Request) { fmt.Fprint(rw, serperGood) })
	ans, err := adapterFor(KindSerper, srv.URL).Search(context.Background(), "invented-serper-key", Query{Text: "harbour museum", Count: 5})
	if err != nil {
		t.Fatal(err)
	}
	r := last.Load().(seen)
	if r.method != http.MethodPost || r.path != "/search" || r.header.Get("X-API-KEY") != "invented-serper-key" || r.header.Get("Authorization") != "" || r.header.Get("Content-Type") != "application/json" {
		t.Errorf("Serper was sent %s %s with %v", r.method, r.path, r.header)
	}
	if r.body != `{"q":"harbour museum","num":10}` {
		t.Errorf("Serper was sent %s", r.body)
	}
	wantTwo(t, KindSerper, ans.Results)
	if _, err := adapterFor(KindSerper, srv.URL).Search(context.Background(), "k", Query{Text: "x", Count: 12}); err != nil {
		t.Fatal(err)
	}
	if b := last.Load().(seen).body; b != `{"q":"x","num":20}` {
		t.Errorf("twelve results were asked as %s, want num 20", b)
	}
}

func TestSearXNGsRequestAndResults(t *testing.T) {
	srv, last, _ := stand(t, func(rw http.ResponseWriter, _ *http.Request) { fmt.Fprint(rw, searxngGood) })
	ans, err := adapterFor(KindSearXNG, srv.URL+"/searxng/").Search(context.Background(), "", Query{Text: "harbour museum", Count: 5})
	if err != nil {
		t.Fatal(err)
	}
	r := last.Load().(seen)
	q, _ := url.ParseQuery(r.query)
	if r.method != http.MethodGet || r.path != "/searxng/search" || q.Get("q") != "harbour museum" || q.Get("format") != "json" {
		t.Errorf("SearXNG was sent %s %s?%s", r.method, r.path, r.query)
	}
	if r.header.Get("Authorization") != "" || r.header.Get("Accept-Language") == "" {
		t.Errorf("SearXNG was sent the headers %v", r.header)
	}
	// Some engines not answering, with results, is still an answer.
	wantTwo(t, KindSearXNG, ans.Results)
	a := adapterFor(KindSearXNG, srv.URL).(SearXNG)
	if !a.Local || a.sender().httpClient().Transport.(*http.Transport).Proxy != nil {
		t.Error("a SearXNG on this machine would go through the environment's proxy")
	}
	if b := adapterFor(KindSearXNG, "https://search.example", func(p *Provider) { p.Trusted = true }).(SearXNG); b.Local || !b.Allowed {
		t.Errorf("a trusted SearXNG elsewhere was made %+v", b)
	}
}

// TestCountsAndBoundsHoldForEveryKind: results are cut at the count asked,
// an answer too large or not JSON is unavailable, and a good answer's
// extract keeps nothing of its markup.
func TestCountsAndBoundsHoldForEveryKind(t *testing.T) {
	good := map[string]string{KindBrave: braveGood, KindExa: exaGood, KindSerper: serperGood, KindSearXNG: searxngGood}
	for kind, body := range good {
		srv, _, _ := stand(t, func(rw http.ResponseWriter, _ *http.Request) { fmt.Fprint(rw, body) })
		ans, err := adapterFor(kind, srv.URL).Search(context.Background(), "k", Query{Text: "x", Count: 1})
		if err != nil || len(ans.Results) != 1 {
			t.Errorf("%s with a count of 1 gave %d results, %v", kind, len(ans.Results), err)
		}
		big, _, _ := stand(t, func(rw http.ResponseWriter, _ *http.Request) { fmt.Fprint(rw, strings.Repeat(" ", 600000)+body) })
		if _, err := adapterFor(kind, big.URL).Search(context.Background(), "k", Query{Text: "x", Count: 5}); failureOf(err).Why != WhyUnavailable || !strings.Contains(err.Error(), "larger than") {
			t.Errorf("%s with an answer too large gave %v", kind, err)
		}
		junk, _, _ := stand(t, func(rw http.ResponseWriter, _ *http.Request) { fmt.Fprint(rw, "<html>a page</html>") })
		if _, err := adapterFor(kind, junk.URL).Search(context.Background(), "k", Query{Text: "x", Count: 5}); failureOf(err).Why != WhyUnavailable {
			t.Errorf("%s with an answer that is not JSON gave %v", kind, err)
		}
	}
}

// answer is a stand-in that answers with a status, headers and a body. The
// body may name the key it was sent, as a careless service might: {key} is
// replaced with every header that could hold one.
func answer(code int, body string, header ...string) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		for i := 0; i+1 < len(header); i += 2 {
			rw.Header().Set(header[i], header[i+1])
		}
		// Exa's x-api-key and Serper's X-API-KEY are one header to Go.
		keys := r.Header.Get("X-Subscription-Token") + r.Header.Get("X-Api-Key")
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(code)
		fmt.Fprint(rw, strings.ReplaceAll(body, "{key}", keys))
	}
}

// TestEachRefusalAndLimitMovesTheProviderToItsState runs a real search,
// through the fallback, against each answer each provider documents for a
// refusal or a limit, and checks the state the provider is left in, why the
// search says it was passed over, and that no key is in what is shown.
func TestEachRefusalAndLimitMovesTheProviderToItsState(t *testing.T) {
	type c struct {
		name   string
		code   int
		body   string
		header []string
		want   StateName // after one search; failing after web.fail_after
		why    Why
		until  time.Duration // for cooling and out, from now; zero is not checked
	}
	brave := []c{
		{"401", 401, `{"type":"ErrorResponse","error":{"code":"UNAUTHORIZED","detail":"{key}"}}`, nil, StateRefused, WhyRefused, 0},
		{"403", 403, `{"type":"ErrorResponse"}`, nil, StateRefused, WhyRefused, 0},
		{"422 token invalid", 422, `{"error":{"code":"SUBSCRIPTION_TOKEN_INVALID","detail":"The provided subscription token is invalid: {key}","meta":{"component":"authentication"},"status":422},"type":"ErrorResponse"}`, nil, StateRefused, WhyRefused, 0},
		{"422 a bad parameter", 422, `{"error":{"code":"VALIDATION","detail":"count is not a number","meta":{"component":"validation"},"status":422},"type":"ErrorResponse"}`, nil, StateReady, WhyMalformed, 0},
		{"429 a second's window", 429, `{"type":"ErrorResponse","error":{"code":"RATE_LIMITED"}}`, []string{"X-RateLimit-Policy", "1;w=1, 15000;w=2592000", "X-RateLimit-Remaining", "0, 900", "X-RateLimit-Reset", "1, 2000000"}, StateCooling, WhyRate, time.Second},
		{"429 the month spent", 429, `{"type":"ErrorResponse"}`, []string{"X-RateLimit-Policy", "1;w=1, 15000;w=2592000", "X-RateLimit-Remaining", "1, 0", "X-RateLimit-Reset", "1, 1419704"}, StateOut, WhyQuota, 1419704 * time.Second},
		{"402", 402, `{"type":"ErrorResponse"}`, nil, StateOut, WhyQuota, 0},
		{"503", 503, `down`, nil, StateFailing, WhyUnavailable, 0},
	}
	exa := []c{
		{"401", 401, `{"requestId":"r","error":"Invalid API key {key}","tag":"INVALID_API_KEY"}`, nil, StateRefused, WhyRefused, 0},
		{"402 credits", 402, `{"requestId":"r","error":"No more credits","tag":"NO_MORE_CREDITS"}`, nil, StateOut, WhyQuota, 0},
		{"402 budget", 402, `{"requestId":"r","error":"Budget exceeded","tag":"API_KEY_BUDGET_EXCEEDED"}`, nil, StateOut, WhyQuota, 0},
		{"402 pay per request", 402, `{"requestId":"r","error":"Payment required","tag":"X402_PAYMENT_REQUIRED"}`, nil, StateRefused, WhyRefused, 0},
		{"403 a feature", 403, `{"requestId":"r","error":"Not on your plan","tag":"FEATURE_DISABLED"}`, nil, StateRefused, WhyRefused, 0},
		{"403 content", 403, `{"requestId":"r","error":"Refused","tag":"PROHIBITED_CONTENT"}`, nil, StateReady, WhyMalformed, 0},
		{"429", 429, `{"requestId":"r","error":"Slow down","tag":"RATE_LIMIT_EXCEEDED"}`, []string{"Retry-After", "30"}, StateCooling, WhyRate, 30 * time.Second},
		{"400", 400, `{"requestId":"r","error":"Bad body","tag":"INVALID_REQUEST_BODY"}`, nil, StateReady, WhyMalformed, 0},
		{"503", 503, `{"requestId":"r","error":"Overloaded","tag":"SERVICE_OVERLOADED"}`, nil, StateFailing, WhyUnavailable, 0},
	}
	serper := []c{
		{"401", 401, `{"message":"Unauthorized {key}","statusCode":401}`, nil, StateRefused, WhyRefused, 0},
		{"403", 403, `{"message":"Unauthorized. Sign up for a free account.","statusCode":403}`, nil, StateRefused, WhyRefused, 0},
		{"400 credits", 400, `{"message":"Not enough credits","statusCode":400}`, nil, StateOut, WhyQuota, 0},
		{"400 a bad query", 400, `{"message":"Query is empty","statusCode":400}`, nil, StateReady, WhyMalformed, 0},
		{"402", 402, `{"message":"Payment required","statusCode":402}`, nil, StateOut, WhyQuota, 0},
		{"429", 429, `{"message":"Too many requests","statusCode":429}`, []string{"Retry-After", "5"}, StateCooling, WhyRate, 5 * time.Second},
		{"500", 500, `{"message":"error","statusCode":500}`, nil, StateFailing, WhyUnavailable, 0},
	}
	searxng := []c{
		{"403 no JSON", 403, `<html>Forbidden</html>`, nil, StateFailing, WhyUnavailable, 0},
		{"429", 429, `Too Many Requests`, nil, StateCooling, WhyRate, time.Minute},
		{"no engine answered", 200, `{"query":"x","results":[],"unresponsive_engines":[["google","timeout"],["bing","CAPTCHA"]]}`, nil, StateFailing, WhyUnavailable, 0},
		{"502", 502, `bad gateway`, nil, StateFailing, WhyUnavailable, 0},
		{"400", 400, `bad`, nil, StateReady, WhyMalformed, 0},
	}
	for kind, cases := range map[string][]c{KindBrave: brave, KindExa: exa, KindSerper: serper, KindSearXNG: searxng} {
		for _, tc := range cases {
			t.Run(kind+" "+tc.name, func(t *testing.T) {
				w := newWorld(t, testRotation)
				w.provider("first", kind, answer(tc.code, tc.body, tc.header...))
				w.provider("second", KindTavily, ok("second"))
				searches := 1
				if tc.want == StateFailing {
					searches = testRotation.FailAfter
				}
				var text string
				var res tools.Result
				for range searches {
					text, res = w.search("harbour museum")
				}
				if strings.Contains(text, "invented-key-") {
					t.Fatalf("a key is in the result: %q", text)
				}
				v := w.view(w.own, "first")
				if v.State != tc.want {
					t.Errorf("the provider is %s, want %s", v.State, tc.want)
				}
				if tc.until > 0 && !v.Until.Equal(start.Add(tc.until)) {
					t.Errorf("it is %s until %s, want %s", v.State, v.Until, start.Add(tc.until))
				}
				if tc.why == WhyMalformed {
					if res.Status != tools.StatusError || !strings.Contains(text, "not sent to another provider") {
						t.Errorf("a malformed request moved on: %s %q", res.Status, text)
					}
					return
				}
				got, _ := Answered(text)
				if res.Status != tools.StatusOK || got.By != "second" || len(got.After) != 1 || got.After[0] != (Passed{"first", whyWords[tc.why]}) {
					t.Errorf("the search gave %s, %+v; want second after first (%s)", res.Status, got, whyWords[tc.why])
				}
			})
		}
	}
}

// TestAFallbackAcrossKinds: an Exa out of credits is passed over and a
// Brave answers, which says what is left; then the Exa's key is refused
// for good, and a SearXNG on this machine answers after the Brave is out.
func TestAFallbackAcrossKinds(t *testing.T) {
	w := newWorld(t, withRotation(func(r *Rotation) { r.Strategy = StrategyOrder }))
	w.provider("exa1", KindExa, answer(402, `{"error":"No more credits","tag":"NO_MORE_CREDITS"}`))
	brave := w.provider("brave1", KindBrave, func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("X-RateLimit-Remaining", "1, 41")
		rw.Header().Set("X-RateLimit-Reset", "1, 864000")
		fmt.Fprint(rw, braveGood)
	})
	w.provider("house", KindSearXNG, func(rw http.ResponseWriter, _ *http.Request) { fmt.Fprint(rw, searxngGood) })
	text, res := w.search("harbour museum")
	got, _ := Answered(text)
	if res.Status != tools.StatusOK || got.By != "brave1" || len(got.After) != 1 || got.After[0] != (Passed{"exa1", "out of quota"}) {
		t.Fatalf("the search gave %s %+v, want brave1 after exa1 (out of quota): %q", res.Status, got, text)
	}
	if !strings.Contains(text, "[1] Harbour Museum: opening hours") || !strings.Contains(text, "https://visit.example/harbour") {
		t.Errorf("Brave's results were not numbered: %q", text)
	}
	if v := w.view(w.own, "brave1"); v.Left == nil || *v.Left != 41 || v.LeftFrom != "provider" {
		t.Errorf("brave1 has %v left from %q, want 41 from the provider", v.Left, v.LeftFrom)
	}
	brave.set(answer(429, `{}`, "X-RateLimit-Remaining", "1, 0", "X-RateLimit-Reset", "1, 864000"))
	if by := w.answered("harbour museum"); by != "house" {
		t.Errorf("with Brave's month spent the search was answered by %q, want house", by)
	}
	if v := w.view(w.own, "brave1"); v.State != StateOut || !v.Until.Equal(start.Add(864000*time.Second)) {
		t.Errorf("brave1 is %s until %s, want out until its reset", v.State, v.Until)
	}
}

// TestNoKeyLeavesTheFourKinds: each kind's key is sent only in its own
// header, and is in no result, error, view or the audit log, even when the
// service repeats it in a refusal and in every field of a result.
func TestNoKeyLeavesTheFourKinds(t *testing.T) {
	echo := map[string]string{
		KindBrave:  `{"web":{"results":[{"title":"echo {key}","url":"https://echo.example/{key}","description":"the key {key}"}]}}`,
		KindExa:    `{"results":[{"title":"echo {key}","url":"https://echo.example/{key}","highlights":["the key {key}"]}]}`,
		KindSerper: `{"organic":[{"title":"echo {key}","link":"https://echo.example/{key}","snippet":"the key {key}"}]}`,
	}
	for kind, body := range echo {
		t.Run(kind, func(t *testing.T) {
			w := newWorld(t, testRotation)
			w.provider("refusing", kind, answer(401, `{"error":"{key}","message":"{key}","tag":"INVALID_API_KEY"}`))
			w.provider("echoing", kind, answer(200, body))
			text, res := w.search("harbour museum")
			if res.Status != tools.StatusOK || strings.Contains(text, "invented-key-") {
				t.Fatalf("the search gave %s %q", res.Status, text)
			}
			if !strings.Contains(text, "(the key, removed)") {
				t.Errorf("the echoed key was not removed: %q", text)
			}
			views, _ := w.own.List(context.Background())
			raw, _ := json.Marshal(views)
			if strings.Contains(string(raw), "invented-key-") {
				t.Errorf("a view holds a key: %s", raw)
			}
			entries, err := audit.List(context.Background(), w.s, audit.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			raw, _ = json.Marshal(entries)
			if strings.Contains(string(raw), "invented-key-") {
				t.Error("the audit log holds a key")
			}
		})
	}
}

// TestARedirectIsNotFollowed: a key header must reach its provider and
// nothing else, so a redirect is an unavailable provider.
func TestARedirectIsNotFollowed(t *testing.T) {
	other, _, otherCalls := stand(t, func(rw http.ResponseWriter, _ *http.Request) { fmt.Fprint(rw, serperGood) })
	for _, kind := range []string{KindBrave, KindExa, KindSerper, KindSearXNG} {
		srv, _, _ := stand(t, func(rw http.ResponseWriter, r *http.Request) {
			http.Redirect(rw, r, other.URL+"/search", http.StatusTemporaryRedirect)
		})
		_, err := adapterFor(kind, srv.URL).Search(context.Background(), "invented-key", Query{Text: "x", Count: 5})
		if failureOf(err).Why != WhyUnavailable || !strings.Contains(err.Error(), "status 307") {
			t.Errorf("%s with a redirect gave %v", kind, err)
		}
	}
	if otherCalls.Load() != 0 {
		t.Errorf("a redirect was followed %d times", otherCalls.Load())
	}
}

// TestEachKindsTimeoutAndUnreachable: a provider too slow, and one that is
// not there, are unavailable, with words that name the service.
func TestEachKindsTimeoutAndUnreachable(t *testing.T) {
	slow, _, _ := stand(t, func(rw http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	for kind, title := range map[string]string{KindBrave: "Brave", KindExa: "Exa", KindSerper: "Serper", KindSearXNG: "SearXNG"} {
		a := adapterFor(kind, slow.URL)
		const short = 100 * time.Millisecond
		switch x := a.(type) {
		case Brave:
			x.Timeout = short
			a = x
		case Exa:
			x.Timeout = short
			a = x
		case Serper:
			x.Timeout = short
			a = x
		case SearXNG:
			x.Timeout = short
			a = x
		}
		_, err := a.Search(context.Background(), "k", Query{Text: "x", Count: 5})
		if failureOf(err).Why != WhyUnavailable || err.Error() != title+" did not answer within 100ms" {
			t.Errorf("%s too slow gave %v", kind, err)
		}
		_, err = adapterFor(kind, gone.URL).Search(context.Background(), "k", Query{Text: "x", Count: 5})
		if failureOf(err).Why != WhyUnavailable || err.Error() != "Keystone could not reach "+title {
			t.Errorf("%s not there gave %v", kind, err)
		}
	}
}

// TestDefaultEndpointsAreTheSettings: an entry with no endpoint searches at
// its kind's web. setting, and with none set, says so and sends nothing.
func TestDefaultEndpointsAreTheSettings(t *testing.T) {
	srv, last, _ := stand(t, func(rw http.ResponseWriter, _ *http.Request) { fmt.Fprint(rw, `{}`) })
	o := testOptions("http://127.0.0.1:9/search")
	o.Endpoints = Endpoints{Brave: srv.URL + "/brave/res/v1/web/search", Exa: srv.URL + "/exa/search", Serper: srv.URL + "/serper/search"}
	for kind, path := range map[string]string{KindBrave: "/brave/res/v1/web/search", KindExa: "/exa/search", KindSerper: "/serper/search"} {
		a := kinds[kind].New(Provider{Name: "d", Kind: kind, Enabled: true}, o)
		if _, err := a.Search(context.Background(), "k", Query{Text: "x", Count: 5}); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if got := last.Load().(seen).path; got != path {
			t.Errorf("%s with no endpoint searched at %s, want %s", kind, got, path)
		}
		a = kinds[kind].New(Provider{Name: "d", Kind: kind, Enabled: true}, testOptions("http://127.0.0.1:9/search"))
		if _, err := a.Search(context.Background(), "k", Query{Text: "x", Count: 5}); failureOf(err).Why != WhyUnavailable || !strings.Contains(err.Error(), "has no endpoint") {
			t.Errorf("%s with no endpoint anywhere gave %v", kind, err)
		}
	}
	for key, bad := range map[string]string{"web.brave_endpoint": "ftp://x", "web.exa_endpoint": "", "web.serper_endpoint": "https://user@serper.example/search"} {
		values := map[string]string{"web.brave_endpoint": "https://api.search.brave.com/res/v1/web/search", "web.exa_endpoint": "https://api.exa.ai/search", "web.serper_endpoint": "https://google.serper.dev/search"}
		values[key] = bad
		if _, err := readEndpoints(func(k string) (string, bool) { v, ok := values[k]; return v, ok }); err == nil {
			t.Errorf("%s = %q was accepted", key, bad)
		}
	}
}

// TestSearXNGsAddressIsLocalOrTrusted: a SearXNG entry needs an endpoint on
// this machine or the home network, or the admin's trusted mark, which only
// an install entry may carry.
func TestSearXNGsAddressIsLocalOrTrusted(t *testing.T) {
	entry := func(endpoint string, trusted bool) Provider {
		return Provider{Name: "house", Kind: KindSearXNG, Endpoint: endpoint, Enabled: true, Trusted: trusted}
	}
	for _, endpoint := range []string{"http://localhost:8888", "http://127.0.0.1:8888", "http://searx", "http://mini.local:8080", "http://nas.lan", "http://box.home.arpa",
		"http://192.168.1.20:8080", "http://10.0.0.5", "http://100.101.102.103:8080", "http://[::1]:8888", "https://search.internal/searxng"} {
		if err := entry(endpoint, false).Check(); err != nil {
			t.Errorf("a SearXNG at %s was refused: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"https://searx.example", "http://8.8.8.8", "https://search.tail1234.ts.net"} {
		if err := entry(endpoint, false).Check(); !errors.Is(err, errSearXNGAddress) {
			t.Errorf("a SearXNG at %s gave %v, want it refused", endpoint, err)
		}
		if err := entry(endpoint, true).Check(); err != nil {
			t.Errorf("a trusted SearXNG at %s was refused: %v", endpoint, err)
		}
	}
	if err := entry("", false).Check(); err == nil {
		t.Error("a SearXNG with no endpoint was accepted")
	}
	if err := (Provider{Name: "b", Kind: KindBrave, Key: KeyFor("b"), Trusted: true}).Check(); err == nil {
		t.Error("a Brave provider was marked trusted")
	}
	if err := (Provider{Name: "s", Kind: KindSearXNG, Endpoint: "http://localhost", Key: KeyFor("s")}).Check(); err == nil {
		t.Error("a SearXNG was given a key")
	}

	w := newWorld(t, testRotation)
	ctx := context.Background()
	if err := w.own.Add(ctx, entry("https://searx.example", true), ""); !errors.Is(err, errOwnTrusted) {
		t.Errorf("a person marked their own SearXNG trusted: %v", err)
	}
	if err := w.own.Add(ctx, entry("http://localhost:8888", false), ""); err != nil {
		t.Fatal(err)
	}
	if err := w.own.Update(ctx, entry("https://searx.example", true)); !errors.Is(err, errOwnTrusted) {
		t.Errorf("a person's own SearXNG was updated to trusted: %v", err)
	}
	trusted := entry("https://searx.example", true)
	trusted.Name = "admins"
	if err := w.inst.Add(ctx, trusted, ""); err != nil {
		t.Fatalf("the admin could not add a trusted SearXNG: %v", err)
	}
	if v := w.view(w.inst.Providers, "admins"); !v.Trusted || v.KeySet {
		t.Errorf("the install's SearXNG reads back as %+v", v)
	}
	trusted.Trusted = false
	if err := w.inst.Update(ctx, trusted); !errors.Is(err, errSearXNGAddress) {
		t.Errorf("the trusted mark was taken off a SearXNG elsewhere: %v", err)
	}
}

// TestAnInstallDatabaseFromP706GainsTheTrustedColumn: data/admin/search.db
// made before P7-07 has no trusted column, and gains it when it is opened.
func TestAnInstallDatabaseFromP706GainsTheTrustedColumn(t *testing.T) {
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	folder := in.AdminFolder()
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(folder, installFile)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(installSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO search_providers(name, position, kind, endpoint, key_name, enabled, added_at) VALUES('old', 0, 'tavily', '', 'search.old', 1, '2026-11-27T00:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	inst, err := OpenInstall(in)
	if err != nil {
		t.Fatal(err)
	}
	list, err := inst.List(context.Background())
	if err != nil || len(list) != 1 || list[0].Name != "old" || list[0].Trusted {
		t.Fatalf("the old install database read as %+v, %v", list, err)
	}
	// Opening it again finds the column there.
	if _, err := inst.List(context.Background()); err != nil {
		t.Fatal(err)
	}
}
