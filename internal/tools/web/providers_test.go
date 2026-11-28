package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

// The proof of search providers and the fallback (P7-06). Every provider
// here is a stand-in served on this machine by httptest, shaped as Tavily
// (a search at /search, a usage check at /usage), or a kind registered
// below for the tests alone. Nothing calls a real search service, and
// every key is invented.

// Two kinds for the tests: plain, a provider with no free check, and
// local, a provider with no key and no quota, as a SearXNG would be.
func init() {
	register(Kind{Name: "plain", Title: "Plain", New: func(p Provider, o Options) Adapter { return plainAdapter{newTavily(p, o).(Tavily)} }})
	register(Kind{Name: "local", Title: "Local", NoKey: true, Unmetered: true, New: func(p Provider, o Options) Adapter { return plainAdapter{newTavily(p, o).(Tavily)} }})
	register(Kind{Name: "headers", Title: "Headers", New: func(p Provider, o Options) Adapter { return headerAdapter{newTavily(p, o).(Tavily)} }})
}

// plainAdapter is Tavily's search without its usage check.
type plainAdapter struct{ t Tavily }

func (a plainAdapter) Search(ctx context.Context, key string, q Query) (Answer, error) {
	return a.t.Search(ctx, key, q)
}

// headerAdapter searches as Tavily does, and reads what is left from the
// answer's rate limit headers, as Brave's adapter will (P7-07).
type headerAdapter struct{ t Tavily }

func (a headerAdapter) Search(ctx context.Context, key string, q Query) (Answer, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.t.Endpoint, strings.NewReader(fmt.Sprintf(`{"query":%q}`, q.Text)))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Answer{}, &Failure{Why: WhyUnavailable, Text: "unreachable"}
	}
	defer resp.Body.Close()
	quota := HeaderQuota(resp.Header, start)
	if resp.StatusCode == 429 {
		return Answer{Quota: quota}, &Failure{Why: WhyQuota, Reset: quota.Reset, Text: "out"}
	}
	var answer tavilyAnswer
	_ = json.NewDecoder(resp.Body).Decode(&answer)
	var out []Result
	for _, r := range answer.Results {
		title, _ := r.Title.(string)
		u, _ := r.URL.(string)
		content, _ := r.Content.(string)
		out = append(out, Result{Title: title, URL: u, Content: content})
	}
	return Answer{Results: out, Quota: quota}, nil
}

// start is the tests' first moment: November 10, 2026, at noon UTC.
var start = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// world is one install with one person, the web tools, the install's
// providers and a clock of its own.
type world struct {
	t     *testing.T
	in    *tenant.Install
	s     *tenant.Store
	clock *clock
	o     Options
	reg   *tools.Registry
	own   *Providers
	inst  *Install

	mu   sync.Mutex
	hits []string // the providers that were sent a search, in order
}

func newWorld(t *testing.T, rot Rotation) *world {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c := &clock{t: start}
	o := testOptions("http://127.0.0.1:9/search")
	o.Rotation, o.Install, o.Now, o.Rand = rot, in, c.now, func() float64 { return 0.5 }
	inst, err := OpenInstall(in)
	if err != nil {
		t.Fatal(err)
	}
	inst.now = c.now
	w := &world{t: t, in: in, s: s, clock: c, o: o, reg: tools.New(), inst: inst}
	w.own = Own(s, inst)
	w.own.now = c.now
	if err := add(w.reg, o, fetcher{timeout: time.Second, maxBytes: 1 << 20, maxChars: 1000, allow: public}); err != nil {
		t.Fatal(err)
	}
	return w
}

// search runs web_search as a turn would, in chatID.
func (w *world) searchIn(chatID, query string) (string, tools.Result) {
	w.t.Helper()
	res := w.reg.Run(context.Background(), tools.Call{Store: w.s, RequestID: "req-" + query, Actor: "person", ChatID: chatID, ID: "call_1", Name: SearchName, Input: json.RawMessage(fmt.Sprintf(`{"query":%q}`, query))})
	return seam.Text(seam.Message{Blocks: res.Block.Content}), res
}

func (w *world) search(query string) (string, tools.Result) { return w.searchIn("", query) }

// answered runs a search and returns which provider answered it, or "" when
// none did.
func (w *world) answered(query string) string {
	w.t.Helper()
	text, res := w.search(query)
	if res.Status != tools.StatusOK {
		return ""
	}
	a, ok := Answered(text)
	if !ok {
		w.t.Fatalf("a search's result has no line that says who answered: %q", text)
	}
	return a.By
}

func (w *world) sweep() int { return newFinder(w.o).sweep(context.Background(), w.in) }

// standProvider is a stand-in search service, shaped as Tavily.
type standProvider struct {
	*httptest.Server
	name   string
	calls  atomic.Int32
	usages atomic.Int32
	auth   atomic.Value
	mu     sync.Mutex
	answer http.HandlerFunc
	usage  http.HandlerFunc
}

func (sp *standProvider) set(h http.HandlerFunc) {
	sp.mu.Lock()
	sp.answer = h
	sp.mu.Unlock()
}

func (sp *standProvider) setUsage(h http.HandlerFunc) {
	sp.mu.Lock()
	sp.usage = h
	sp.mu.Unlock()
}

// serve starts a stand-in that answers searches with h.
func (w *world) serve(name string, h http.HandlerFunc) *standProvider {
	sp := &standProvider{name: name, answer: h, usage: usageLeft(500)}
	sp.Server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		sp.auth.Store(r.Header.Get("Authorization"))
		sp.mu.Lock()
		answer, usage := sp.answer, sp.usage
		sp.mu.Unlock()
		if r.URL.Path == "/usage" {
			sp.usages.Add(1)
			usage(rw, r)
			return
		}
		sp.calls.Add(1)
		w.mu.Lock()
		w.hits = append(w.hits, name)
		w.mu.Unlock()
		answer(rw, r)
	}))
	w.t.Cleanup(sp.Close)
	return sp
}

// provider starts a stand-in and adds it as the person's own provider, of
// the kind given, with an invented key. edit may change the entry first.
func (w *world) provider(name, kind string, h http.HandlerFunc, edit ...func(*Provider)) *standProvider {
	return w.addTo(w.own, name, kind, h, edit...)
}

// installProvider is provider for the install's providers.
func (w *world) installProvider(name, kind string, h http.HandlerFunc, edit ...func(*Provider)) *standProvider {
	return w.addTo(w.inst.Providers, name, kind, h, edit...)
}

func (w *world) addTo(ps *Providers, name, kind string, h http.HandlerFunc, edit ...func(*Provider)) *standProvider {
	w.t.Helper()
	sp := w.serve(name, h)
	p := Provider{Name: name, Kind: kind, Endpoint: sp.URL, Enabled: true}
	for _, e := range edit {
		e(&p)
	}
	key := "invented-key-" + name
	if kinds[kind].NoKey {
		key = ""
	}
	if err := ps.Add(context.Background(), p, key); err != nil {
		w.t.Fatal(err)
	}
	return sp
}

func (w *world) hitsSoFar() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.hits...)
}

// view returns one provider as the screens show it.
func (w *world) view(ps *Providers, name string) View {
	w.t.Helper()
	list, err := ps.List(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	for _, v := range list {
		if v.Name == name {
			return v
		}
	}
	w.t.Fatalf("there is no provider %s", name)
	return View{}
}

// ok answers a search with one result that names the provider.
func ok(name string) http.HandlerFunc {
	return func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(rw, `{"results":[{"title":"From %s","url":"https://%s.example/","content":"An answer from %s."}]}`, name, name, name)
	}
}

// status answers with a status and the request's key repeated, as a
// careless service might.
func status(code int, header ...string) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		for i := 0; i+1 < len(header); i += 2 {
			rw.Header().Set(header[i], header[i+1])
		}
		http.Error(rw, "no: "+r.Header.Get("Authorization"), code)
	}
}

// usageLeft answers Tavily's usage check with left credits on the plan.
func usageLeft(left int) http.HandlerFunc {
	return func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(rw, `{"key":{"usage":12,"limit":null},"account":{"current_plan":"Researcher","plan_usage":%d,"plan_limit":1000,"paygo_usage":0,"paygo_limit":null}}`, 1000-left)
	}
}

func withRotation(edit func(*Rotation)) Rotation {
	r := testRotation
	edit(&r)
	return r
}

func TestAProviderOutOfQuotaIsPassedOverAndTheNextAnswers(t *testing.T) {
	w := newWorld(t, testRotation)
	a := w.provider("alpha", KindTavily, status(432))
	b := w.provider("bravo", KindTavily, ok("bravo"))
	text, res := w.search("harbour museum")
	if res.Status != tools.StatusOK || !strings.Contains(text, "From bravo") {
		t.Fatalf("the search gave %s %q, want bravo's result", res.Status, text)
	}
	got, _ := Answered(text)
	if got.By != "bravo" || len(got.After) != 1 || got.After[0] != (Passed{"alpha", "out of quota"}) {
		t.Errorf("the record says %+v, want bravo after alpha, out of quota", got)
	}
	if v := w.view(w.own, "alpha"); v.State != StateOut || v.Left == nil || *v.Left != 0 {
		t.Errorf("alpha is %s with %v left, want out with nothing left", v.State, v.Left)
	}
	// The next search goes straight to bravo.
	if by := w.answered("harbour museum"); by != "bravo" || a.calls.Load() != 1 || b.calls.Load() != 2 {
		t.Errorf("the second search was answered by %q, alpha had %d searches and bravo %d; want bravo, 1 and 2", by, a.calls.Load(), b.calls.Load())
	}
}

func TestTheOrderFollowsEachStrategy(t *testing.T) {
	paid := func(p *Provider) { p.CostPerSearch = 0.005 }
	free := func(p *Provider) { p.FreeQuota, p.MonthlyQuota = 2, 100 }

	t.Run("order", func(t *testing.T) {
		w := newWorld(t, withRotation(func(r *Rotation) { r.Strategy = StrategyOrder }))
		w.provider("alpha", KindTavily, ok("alpha"), paid)
		w.provider("bravo", KindTavily, ok("bravo"), free)
		if by := w.answered("a"); by != "alpha" {
			t.Errorf("with order, %q answered, want alpha, the first", by)
		}
	})

	t.Run("free quota first", func(t *testing.T) {
		w := newWorld(t, testRotation)
		w.provider("alpha", KindTavily, ok("alpha"), paid)
		w.provider("bravo", KindTavily, ok("bravo"), free)
		var got []string
		for range 3 {
			got = append(got, w.answered("a"))
		}
		if strings.Join(got, " ") != "bravo bravo alpha" {
			t.Errorf("with free_first, the searches went to %v, want bravo's two free searches and then alpha", got)
		}
		if v := w.view(w.own, "bravo"); v.Searches != 2 || v.CostMicros != 0 {
			t.Errorf("bravo counts %d searches costing %d, want 2 costing nothing", v.Searches, v.CostMicros)
		}
		if v := w.view(w.own, "alpha"); v.Searches != 1 || v.CostMicros != 5000 {
			t.Errorf("alpha counts %d searches costing %d micros, want 1 costing 5000", v.Searches, v.CostMicros)
		}
	})

	t.Run("weighted", func(t *testing.T) {
		w := newWorld(t, withRotation(func(r *Rotation) { r.Strategy = StrategyWeighted; r.MaxAttempts = 4; r.FailAfter = 10 }))
		w.provider("local", "local", status(500))
		w.provider("alpha", KindTavily, status(500), func(p *Provider) { p.MonthlyQuota = 100 })
		w.provider("bravo", KindTavily, status(500), func(p *Provider) { p.MonthlyQuota = 100 })
		w.provider("charlie", KindTavily, status(500))
		// alpha has a tenth of its quota left, bravo all of it, and charlie
		// an unknown quota, which counts as half.
		if _, err := own(w.s).change(context.Background(), "alpha", func(s State) State { s.Used, s.Window = 90, monthStart(start); return s }); err != nil {
			t.Fatal(err)
		}
		w.search("a")
		// Drawn at half the total each time: bravo (of 1.6), then charlie (of
		// 0.6), then alpha; the provider with no quota comes last.
		if got := strings.Join(w.hitsSoFar(), " "); got != "bravo charlie alpha local" {
			t.Errorf("with weighted, the providers were tried as %q, want bravo charlie alpha local", got)
		}
	})
}

func TestWeightedSpreadsByTheShareOfQuotaLeft(t *testing.T) {
	r := testRotation
	r.Strategy = StrategyWeighted
	group := []candidate{
		{p: Provider{Name: "alpha", MonthlyQuota: 100}, st: State{Used: 75}}, // a quarter left
		{p: Provider{Name: "bravo", MonthlyQuota: 100}},                      // all of it
		{p: Provider{Name: "charlie"}},                                       // not known: 0.5
	}
	firsts := map[string]int{}
	const n = 1000
	for i := range n {
		at := (float64(i) + 0.5) / n
		order := r.order(append([]candidate(nil), group...), func() float64 { return at })
		firsts[order[0].p.Name]++
	}
	// Shares 0.25, 1 and 0.5 of 1.75.
	for name, want := range map[string]int{"alpha": 143, "bravo": 571, "charlie": 286} {
		if got := firsts[name]; got < want-2 || got > want+2 {
			t.Errorf("%s went first %d times in %d, want about %d", name, got, n, want)
		}
	}
}

func TestARateLimitCoolsAProviderForTheWaitItGave(t *testing.T) {
	w := newWorld(t, testRotation)
	a := w.provider("alpha", KindTavily, status(429, "Retry-After", "120"))
	w.provider("bravo", KindTavily, ok("bravo"))
	if by := w.answered("a"); by != "bravo" {
		t.Fatalf("%q answered, want bravo", by)
	}
	if v := w.view(w.own, "alpha"); v.State != StateCooling || !v.Until.Equal(start.Add(120*time.Second)) {
		t.Errorf("alpha is %s until %s, want cooling for the 120 seconds it gave", v.State, v.Until)
	}
	w.clock.add(119 * time.Second)
	w.answered("a")
	if a.calls.Load() != 1 {
		t.Errorf("alpha was sent a search while it cooled")
	}
	a.set(ok("alpha"))
	w.clock.add(2 * time.Second)
	if by := w.answered("a"); by != "alpha" {
		t.Errorf("after its wait, %q answered, want alpha back in the rotation", by)
	}

	// No wait given: web.cooling_for. A wait of web.cooling_max or more is
	// a spent quota, until the wait has passed.
	a.set(status(429))
	w.answered("a")
	if v := w.view(w.own, "alpha"); v.State != StateCooling || !v.Until.Equal(w.clock.now().Add(time.Minute)) {
		t.Errorf("with no wait given, alpha is %s until %s, want cooling for web.cooling_for", v.State, v.Until)
	}
	w.clock.add(time.Minute)
	a.set(status(429, "Retry-After", "3600"))
	w.answered("a")
	if v := w.view(w.own, "alpha"); v.State != StateOut || !v.Until.Equal(w.clock.now().Add(time.Hour)) {
		t.Errorf("with an hour's wait, alpha is %s until %s, want out for the hour", v.State, v.Until)
	}
}

func TestThreeFailuresPutAProviderInFailing(t *testing.T) {
	w := newWorld(t, testRotation)
	a := w.provider("alpha", KindTavily, status(502))
	w.provider("bravo", KindTavily, ok("bravo"))
	for i := 1; i <= 3; i++ {
		w.answered("a")
		want := StateReady
		if i == 3 {
			want = StateFailing
		}
		if v := w.view(w.own, "alpha"); v.State != want {
			t.Errorf("after %d failures alpha is %s, want %s", i, v.State, want)
		}
	}
	w.answered("a")
	if a.calls.Load() != 3 {
		t.Errorf("alpha had %d searches, want 3: a failing provider is not tried", a.calls.Load())
	}
	// A success between failures starts the count again.
	w2 := newWorld(t, testRotation)
	c := w2.provider("charlie", KindTavily, status(503))
	w2.provider("delta", KindTavily, ok("delta"))
	w2.answered("a")
	w2.answered("a")
	c.set(ok("charlie"))
	w2.answered("a")
	c.set(status(503))
	w2.answered("a")
	w2.answered("a")
	if v := w2.view(w2.own, "charlie"); v.State != StateReady {
		t.Errorf("two failures, a success and two more left charlie %s, want ready", v.State)
	}
}

func TestARefusedKeyIsNeverTestedAgainUntilItIsChanged(t *testing.T) {
	w := newWorld(t, testRotation)
	a := w.provider("alpha", KindTavily, status(401))
	w.provider("bravo", KindTavily, ok("bravo"))
	w.answered("a")
	if v := w.view(w.own, "alpha"); v.State != StateRefused {
		t.Fatalf("alpha is %s, want refused", v.State)
	}
	for range 5 {
		w.clock.add(48 * time.Hour)
		if n := w.sweep(); n != 0 {
			t.Errorf("the sweep ran %d checks of a refused key", n)
		}
		w.answered("a")
	}
	if a.calls.Load() != 1 || a.usages.Load() != 0 {
		t.Errorf("a refused key was used again: %d searches and %d checks", a.calls.Load(), a.usages.Load())
	}
	// A new key brings it back, whichever way it was stored.
	a.set(ok("alpha"))
	if err := w.own.SetKey(context.Background(), "alpha", "another-invented-key"); err != nil {
		t.Fatal(err)
	}
	if by := w.answered("a"); by != "alpha" || a.auth.Load() != "Bearer another-invented-key" {
		t.Errorf("after the key changed, %q answered, want alpha with the new key", by)
	}
}

func TestBackAtAKnownReset(t *testing.T) {
	w := newWorld(t, testRotation)
	a := w.provider("alpha", KindTavily, status(432), func(p *Provider) { p.Resets = "1" })
	w.provider("bravo", KindTavily, ok("bravo"))
	text, _ := w.search("a")
	dec1 := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if v := w.view(w.own, "alpha"); v.State != StateOut || !v.Until.Equal(dec1) {
		t.Fatalf("alpha is %s until %s, want out until December 1", v.State, v.Until)
	}
	if text, _ = w.search("b"); !strings.Contains(text, "Answered by bravo.") {
		t.Errorf("the second search's record is %q", text)
	}
	a.set(ok("alpha"))
	w.clock.set(dec1.Add(-time.Second))
	w.answered("a")
	if a.calls.Load() != 1 {
		t.Error("alpha was tried before its reset")
	}
	w.clock.set(dec1)
	if by := w.answered("a"); by != "alpha" {
		t.Errorf("at its reset, %q answered, want alpha back", by)
	}
	// Every candidate out says so, one line each, with the date.
	w2 := newWorld(t, testRotation)
	w2.provider("alpha", KindTavily, status(432), func(p *Provider) { p.Resets = "1" })
	w2.search("a")
	text, res := w2.search("a")
	if res.Status != tools.StatusError || !strings.Contains(text, "\nalpha: out of quota until December 1") {
		t.Errorf("with every provider out, the search gave %s %q", res.Status, text)
	}
}

func TestBackAfterAFreeCheckWithQuotaLeft(t *testing.T) {
	w := newWorld(t, testRotation)
	a := w.provider("alpha", KindTavily, status(432))
	w.provider("bravo", KindTavily, ok("bravo"))
	a.setUsage(usageLeft(0))
	w.answered("a")
	w.clock.add(29 * time.Minute)
	if n := w.sweep(); n != 0 || a.usages.Load() != 0 {
		t.Fatalf("a check ran before web.test_first: %d", n)
	}
	w.clock.add(time.Minute)
	if n := w.sweep(); n != 1 || a.usages.Load() != 1 {
		t.Fatalf("at web.test_first the sweep ran %d checks, want 1", n)
	}
	if v := w.view(w.own, "alpha"); v.State != StateOut || v.Left == nil || *v.Left != 0 || v.LeftFrom != "provider" {
		t.Errorf("after a check with nothing left alpha is %s with %v left from %q, want out with 0 from the provider", v.State, v.Left, v.LeftFrom)
	}
	// The wait doubles: the next check is an hour later.
	w.clock.add(59 * time.Minute)
	if n := w.sweep(); n != 0 {
		t.Errorf("a check ran before the doubled wait")
	}
	a.setUsage(usageLeft(50))
	w.clock.add(time.Minute)
	if n := w.sweep(); n != 1 {
		t.Errorf("after the doubled wait the sweep ran %d checks, want 1", n)
	}
	v := w.view(w.own, "alpha")
	if v.State != StateReady || v.Left == nil || *v.Left != 50 || v.LeftFrom != "provider" {
		t.Errorf("after a check with quota left alpha is %s with %v left, want ready with 50 from the provider", v.State, v.Left)
	}
	a.set(ok("alpha"))
	if by := w.answered("a"); by != "alpha" {
		t.Errorf("%q answered, want alpha back", by)
	}
	if v := w.view(w.own, "alpha"); v.Left == nil || *v.Left != 49 {
		t.Errorf("after one more search %v is left, want 49", v.Left)
	}
}

func TestOneRealSearchIsLetThroughAloneAndTheWaitDoubles(t *testing.T) {
	w := newWorld(t, withRotation(func(r *Rotation) { r.TestMax = 2 * time.Hour }))
	b := w.provider("bravo", KindTavily, ok("bravo"))
	a := w.provider("alpha", "plain", ok("alpha"))
	w.provider("zulu", KindTavily, ok("zulu")) // never reached: bravo answers
	// alpha runs out while it answers last in line.
	a.set(status(432))
	if err := w.own.Order(context.Background(), []string{"alpha", "bravo", "zulu"}); err != nil {
		t.Fatal(err)
	}
	w.answered("a")
	if err := w.own.Order(context.Background(), []string{"bravo", "alpha", "zulu"}); err != nil {
		t.Fatal(err)
	}
	waits := []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour, 2 * time.Hour}
	for i, wait := range waits {
		w.clock.add(wait - time.Minute)
		before := a.calls.Load()
		w.answered("a")
		if a.calls.Load() != before {
			t.Fatalf("test %d: alpha was tried before its wait of %s", i+1, wait)
		}
		w.clock.add(time.Minute)
		hits := len(w.hitsSoFar())
		text, _ := w.search("a")
		order := w.hitsSoFar()[hits:]
		if len(order) != 2 || order[0] != "alpha" || order[1] != "bravo" {
			t.Fatalf("test %d: the search went to %v, want alpha first, alone, then bravo", i+1, order)
		}
		if !strings.Contains(text, "Answered by bravo, after alpha (out of quota).") {
			t.Errorf("test %d: the record is %q", i+1, text)
		}
	}
	// With quota again, the test search is answered by alpha and it is back.
	a.set(ok("alpha"))
	w.clock.add(2 * time.Hour)
	if by := w.answered("a"); by != "alpha" {
		t.Errorf("%q answered the test search, want alpha", by)
	}
	if v := w.view(w.own, "alpha"); v.State != StateReady {
		t.Errorf("alpha is %s after a test search it answered, want ready", v.State)
	}
	if b.calls.Load() == 0 {
		t.Error("bravo answered nothing")
	}
}

func TestNeverTwoTestsAtOnce(t *testing.T) {
	w := newWorld(t, testRotation)
	a := w.provider("alpha", "plain", status(432))
	w.provider("bravo", KindTavily, ok("bravo"))
	w.answered("a")
	w.clock.add(30 * time.Minute)
	hold, held := make(chan struct{}), make(chan struct{}, 4)
	a.set(func(rw http.ResponseWriter, r *http.Request) {
		held <- struct{}{}
		<-hold
		ok("alpha")(rw, r)
	})
	done := make(chan string)
	go func() {
		text, _ := w.search("first")
		done <- text
	}()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the first search did not reach alpha")
	}
	// While alpha's test runs, a second search does not test it too.
	if by := w.answered("second"); by != "bravo" || a.calls.Load() != 2 {
		t.Errorf("a second search during the test was answered by %q and alpha had %d searches; want bravo and 2", by, a.calls.Load())
	}
	close(hold)
	if text := <-done; !strings.Contains(text, "Answered by alpha.") {
		t.Errorf("the test search gave %q", text)
	}

	// Free checks: two sweeps at once run one check.
	w2 := newWorld(t, testRotation)
	c := w2.provider("charlie", KindTavily, status(432))
	w2.answered("a")
	w2.clock.add(30 * time.Minute)
	hold2, held2 := make(chan struct{}), make(chan struct{}, 4)
	c.setUsage(func(rw http.ResponseWriter, r *http.Request) {
		held2 <- struct{}{}
		<-hold2
		usageLeft(10)(rw, r)
	})
	ran := make(chan int)
	go func() { ran <- w2.sweep() }()
	<-held2
	if n := w2.sweep(); n != 0 {
		t.Errorf("a second sweep ran %d checks while the first was checking", n)
	}
	close(hold2)
	if n := <-ran; n != 1 || c.usages.Load() != 1 {
		t.Errorf("the sweeps ran %d checks, want one", c.usages.Load())
	}
}

func TestWhatIsLeftIsReadFromTheProvider(t *testing.T) {
	// Tavily's usage check: the least of the key's own limit and the plan's
	// credits with pay-as-you-go.
	for _, c := range []struct {
		body string
		want Quota
	}{
		{`{"key":{"usage":150,"limit":1000},"account":{"plan_usage":500,"plan_limit":15000}}`, Quota{Left: 850, Known: true}},
		{`{"key":{"usage":150,"limit":null},"account":{"plan_usage":990,"plan_limit":1000,"paygo_usage":25,"paygo_limit":100}}`, Quota{Left: 85, Known: true}},
		{`{"key":{"usage":3},"account":{"plan_usage":1200,"plan_limit":1000}}`, Quota{Left: 0, Known: true}},
		{`{"key":{},"account":{}}`, Quota{}},
	} {
		var u tavilyUsage
		if err := json.Unmarshal([]byte(c.body), &u); err != nil {
			t.Fatal(err)
		}
		if got := u.quota(); got != c.want {
			t.Errorf("the usage %s reads as %+v, want %+v", c.body, got, c.want)
		}
	}
	// Brave's headers: one value for each window, the month's last.
	h := http.Header{}
	h.Set("X-RateLimit-Limit", "1, 2000")
	h.Set("X-RateLimit-Remaining", "1, 1500")
	h.Set("X-RateLimit-Reset", "1, 86400")
	if q := HeaderQuota(h, start); q.Left != 1500 || !q.Known || !q.Reset.Equal(start.Add(24*time.Hour)) {
		t.Errorf("Brave's headers read as %+v, want 1500 left, resetting in a day", q)
	}
	if q := HeaderQuota(http.Header{}, start); q.Known {
		t.Errorf("no headers read as %+v", q)
	}

	// An answer's quota is what is left, less the searches since, and none
	// left takes the provider out until the reset it gave.
	w := newWorld(t, testRotation)
	left := 3
	var mu sync.Mutex
	a := w.provider("alpha", "headers", func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		left--
		n := left
		mu.Unlock()
		rw.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("1, %d", n))
		rw.Header().Set("X-RateLimit-Reset", "1, 3600")
		ok("alpha")(rw, r)
	})
	w.provider("bravo", KindTavily, ok("bravo"))
	w.answered("a")
	if v := w.view(w.own, "alpha"); v.Left == nil || *v.Left != 2 || v.LeftFrom != "provider" {
		t.Errorf("after one search alpha has %v left from %q, want 2 from the provider", v.Left, v.LeftFrom)
	}
	w.answered("a")
	w.answered("a") // the last: 0 left
	if v := w.view(w.own, "alpha"); v.State != StateOut || !v.Until.Equal(start.Add(time.Hour)) {
		t.Errorf("with nothing left alpha is %s until %s, want out until the reset it gave", v.State, v.Until)
	}
	if by := w.answered("a"); by != "bravo" || a.calls.Load() != 3 {
		t.Errorf("%q answered and alpha had %d searches, want bravo and 3", by, a.calls.Load())
	}
	// Keystone's own count, with no report: the monthly quota less the count.
	w2 := newWorld(t, withRotation(func(r *Rotation) { r.QuotaReserve = 1 }))
	w2.provider("charlie", KindTavily, ok("charlie"), func(p *Provider) { p.MonthlyQuota = 3 })
	w2.provider("delta", KindTavily, ok("delta"))
	w2.answered("a")
	if v := w2.view(w2.own, "charlie"); v.Left == nil || *v.Left != 2 || v.LeftFrom != "count" {
		t.Errorf("charlie has %v left from %q, want 2 by Keystone's count", v.Left, v.LeftFrom)
	}
	w2.answered("a") // one left: at web.quota_reserve, so out until the month's end
	if v := w2.view(w2.own, "charlie"); v.State != StateOut || !v.Until.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("at the reserve charlie is %s until %s, want out until the month's end", v.State, v.Until)
	}
	if by := w2.answered("a"); by != "delta" {
		t.Errorf("%q answered, want delta", by)
	}
}

func TestAPersonsOwnProviderIsTriedBeforeTheInstalls(t *testing.T) {
	w := newWorld(t, withRotation(func(r *Rotation) { r.Strategy = StrategyFreeFirst }))
	i := w.installProvider("house", KindTavily, ok("house"), func(p *Provider) { p.FreeQuota, p.MonthlyQuota = 100, 100 })
	o := w.provider("mine", KindTavily, ok("mine"), func(p *Provider) { p.CostPerSearch = 0.01 })
	if by := w.answered("a"); by != "mine" {
		t.Errorf("%q answered, want the person's own provider before the install's free one", by)
	}
	o.set(status(432))
	text, _ := w.search("a")
	if !strings.Contains(text, "Answered by house, after mine (out of quota).") {
		t.Errorf("with the person's own out, the result is %q, want the install's to answer", text)
	}
	if i.auth.Load() != "Bearer invented-key-house" {
		t.Error("the install's provider was not sent the install's key")
	}
	if v := w.view(w.inst.Providers, "house"); v.Group != GroupInstall || v.Searches != 1 || !v.KeySet {
		t.Errorf("the install's provider shows %+v", v)
	}
	// The names are one space.
	if err := w.own.Add(context.Background(), Provider{Name: "house", Kind: KindTavily, Enabled: true}, "x"); err == nil {
		t.Error("a person's provider took the name of the install's")
	}
	if err := w.inst.Add(context.Background(), Provider{Name: "mine", Kind: KindTavily, Enabled: true}, "x"); err == nil {
		t.Error("an install provider took the name of a person's own")
	}
}

func TestNoKeyIsEverShown(t *testing.T) {
	w := newWorld(t, testRotation)
	w.installProvider("house", KindTavily, status(401))
	w.provider("mine", KindTavily, func(rw http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		fmt.Fprintf(rw, `{"results":[{"title":"echo %s","url":"https://echo.example/","content":"the key %s"}]}`, key, key)
	})
	const ownKey, installKey = "invented-key-mine", "invented-key-house"
	ctx := context.Background()
	var shown []string
	text, _ := w.search("a")
	shown = append(shown, text)
	w.view(w.own, "mine")
	if err := w.own.Order(ctx, []string{"mine"}); err != nil {
		t.Fatal(err)
	}
	// mine out, so the install's provider is tried and refuses its key.
	if _, err := own(w.s).change(ctx, "mine", func(s State) State { return testRotation.out(s, "out of quota", time.Time{}, start) }); err != nil {
		t.Fatal(err)
	}
	text, _ = w.search("b")
	shown = append(shown, text)
	if !strings.Contains(text, "house: Tavily refused the key the admin stored for it") {
		t.Errorf("the install's refusal says %q", text)
	}
	for _, ps := range []*Providers{w.own, w.inst.Providers} {
		list, err := ps.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(list)
		shown = append(shown, string(raw), fmt.Sprintf("%+v %#v", list, ps))
	}
	entries, err := audit.List(ctx, w.s, audit.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	shown = append(shown, fmt.Sprintf("%+v", entries))
	for _, folder := range []string{w.inst.folder, filepath.Join(filepath.Dir(w.inst.folder), "tenants")} {
		filepath.WalkDir(folder, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				raw, _ := os.ReadFile(p)
				shown = append(shown, string(raw))
			}
			return nil
		})
	}
	for _, s := range shown {
		for _, key := range []string{ownKey, installKey} {
			if strings.Contains(s, key) {
				t.Errorf("a key is shown: %.200q", s)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(w.inst.folder, "secrets.bin")); err != nil {
		t.Errorf("the install vault is not in data/admin/: %v", err)
	}
}

func TestEverySearchIsInTheAuditLogWithItsProviders(t *testing.T) {
	w := newWorld(t, testRotation)
	w.provider("alpha", KindTavily, status(432))
	w.provider("bravo", KindTavily, status(503))
	w.provider("charlie", KindTavily, ok("charlie"))
	text, _ := w.search("harbour")
	entries, err := audit.List(context.Background(), w.s, audit.Filter{Kind: audit.KindTool, RequestID: "req-harbour"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[1].Outcome != audit.OutcomeOK || !strings.Contains(entries[1].Received, "Answered by charlie, after alpha (out of quota), bravo (unavailable).") {
		t.Errorf("the search's audit entries are %+v", entries)
	}
	if a, ok := Answered(text); !ok || a.By != "charlie" || len(a.After) != 2 || a.After[1] != (Passed{"bravo", "unavailable"}) {
		t.Errorf("Answered read %+v, %v from the result", a, ok)
	}
	// None answers: every provider and why, in the entry.
	w2 := newWorld(t, testRotation)
	w2.provider("delta", KindTavily, status(432))
	w2.provider("echo", KindTavily, status(500))
	w2.search("again")
	entries, err = audit.List(context.Background(), w2.s, audit.Filter{Kind: audit.KindTool, RequestID: "req-again"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[1].Outcome == audit.OutcomeOK ||
		!strings.Contains(entries[1].Received, "delta: Tavily says a usage or rate limit was reached") || !strings.Contains(entries[1].Received, "echo: Tavily is unavailable") {
		t.Errorf("a search no provider answered is in the audit log as %+v", entries)
	}
}

func TestWhenNoProviderAnswersEachSaysWhy(t *testing.T) {
	w := newWorld(t, withRotation(func(r *Rotation) { r.MaxAttempts = 2 }))
	w.provider("alpha", KindTavily, status(503))
	w.provider("bravo", KindTavily, status(401))
	c := w.provider("charlie", KindTavily, ok("charlie"))
	w.provider("delta", KindTavily, ok("delta"), func(p *Provider) { p.Enabled = false })
	if err := w.own.Add(context.Background(), Provider{Name: "echo", Kind: KindTavily, Enabled: true}, ""); err != nil {
		t.Fatal(err)
	}
	text, res := w.search("a")
	if res.Status != tools.StatusError {
		t.Fatalf("the search gave %s %q", res.Status, text)
	}
	for _, want := range []string{
		noneAnswered,
		"\nalpha: Tavily is unavailable (it answered with status 503)",
		"\nbravo: Tavily refused the key stored as search.bravo. Check the key",
		"\ncharlie: not tried, since web.max_attempts providers were tried before it",
		"\necho: no key is stored as search.echo",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the result has no %q: %q", want, text)
		}
	}
	if strings.Contains(text, "delta") {
		t.Error("a provider that is switched off is named")
	}
	// A provider that is not tried this time says why.
	c.set(status(500))
	text, _ = w.search("b")
	if !strings.Contains(text, "\nbravo: the key stored as search.bravo was refused. Store a new one") {
		t.Errorf("the next result has no line for the refused key: %q", text)
	}
}

func TestAMalformedRequestDoesNotMoveOn(t *testing.T) {
	w := newWorld(t, testRotation)
	w.provider("alpha", KindTavily, status(400))
	b := w.provider("bravo", KindTavily, ok("bravo"))
	text, res := w.search("a")
	if res.Status != tools.StatusError || !strings.Contains(text, "malformed") || b.calls.Load() != 0 {
		t.Errorf("a malformed request gave %s %q and bravo had %d searches; want an error and none", res.Status, text, b.calls.Load())
	}
	if v := w.view(w.own, "alpha"); v.State != StateReady {
		t.Errorf("a malformed request left alpha %s, want ready", v.State)
	}
}

func TestAtMostMaxAttemptsProvidersAreTried(t *testing.T) {
	w := newWorld(t, testRotation)
	for _, n := range []string{"alpha", "bravo", "charlie", "delta"} {
		w.provider(n, KindTavily, status(500))
	}
	w.search("a")
	if got := strings.Join(w.hitsSoFar(), " "); got != "alpha bravo charlie" {
		t.Errorf("the search tried %q, want the first three", got)
	}
}

func TestWebToolsStayOffInAHeldChatWhicheverProvider(t *testing.T) {
	w := newWorld(t, testRotation)
	a := w.provider("mine", KindTavily, ok("mine"))
	b := w.installProvider("house", KindTavily, ok("house"))
	c, err := chat.Create(w.s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.s.DB().Exec("INSERT INTO turns (id, chat_id, status, request_id, actor, input, created_at) VALUES ('turn-held', ?, 'done', 'req', 'person', '{}', '2026-11-25T10:00:00Z')", c.ID); err != nil {
		t.Fatal(err)
	}
	if err := recall.Hold(context.Background(), w.s, "turn-held", c.ID); err != nil {
		t.Fatal(err)
	}
	text, res := w.searchIn(c.ID, "harbour")
	if res.Status != tools.StatusError || !strings.Contains(text, "off in this chat") || a.calls.Load()+b.calls.Load() != 0 {
		t.Errorf("a search in a held chat gave %s %q and reached a provider", res.Status, text)
	}
}

func TestTavilyMovesToTheFirstOwnProviderAndStillAnswers(t *testing.T) {
	// An install from before P7-06: a person with provider.tavily, and no
	// provider entries.
	tav := newStandIn(t, harbour)
	w := newWorld(t, testRotation)
	w.o.SearchEndpoint = tav.URL + "/search"
	w.reg = tools.New()
	if err := add(w.reg, w.o, fetcher{allow: public}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	w.provider("later", KindTavily, ok("later")) // added from the screen before the key was looked at
	if err := w.s.Secrets().Set(KeyName, testKey); err != nil {
		t.Fatal(err)
	}
	text, res := w.search("harbour museum")
	if res.Status != tools.StatusOK || !strings.Contains(text, "Harbour Museum: opening hours") || !strings.HasSuffix(text, "Answered by tavily.") {
		t.Fatalf("after the move the search gave %s %q, want Tavily's results", res.Status, text)
	}
	if tav.auth.Load() != "Bearer "+testKey {
		t.Error("Tavily was not sent the person's key")
	}
	list, err := w.own.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "tavily" || list[0].Kind != KindTavily || list[0].Key != KeyName || !list[0].KeySet || list[0].Endpoint != "" {
		t.Fatalf("the person's providers are %+v, want tavily first, with its key kept as provider.tavily", list)
	}
	// Once: a person who removes it does not get it back.
	if err := w.own.Remove(ctx, "tavily"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := w.s.Secrets().Get(KeyName); !ok {
		t.Error("removing the moved provider removed provider.tavily")
	}
	w.answered("a")
	if list, _ := w.own.List(ctx); len(list) != 1 {
		t.Errorf("the moved provider came back: %+v", list)
	}

	// At start: the sweep's first pass moves every person's key, before any
	// search.
	made, err := w.in.Create("Test Person Two")
	if err != nil {
		t.Fatal(err)
	}
	two, err := w.in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	if err := two.Secrets().Set(KeyName, "invented-key-two"); err != nil {
		t.Fatal(err)
	}
	w.sweep()
	if list, err := Own(two, nil).List(ctx); err != nil || len(list) != 1 || list[0].Name != "tavily" {
		t.Errorf("after the sweep the second person's providers are %+v, %v", list, err)
	}
}

func TestAnEntryIsChecked(t *testing.T) {
	ok := Provider{Name: "alpha", Kind: KindTavily, Key: "search.alpha", Enabled: true}
	if err := ok.Check(); err != nil {
		t.Fatal(err)
	}
	for what, edit := range map[string]func(*Provider){
		"a name with a capital":        func(p *Provider) { p.Name = "Alpha" },
		"a SearXNG given a key":        func(p *Provider) { p.Kind, p.Endpoint = KindSearXNG, "http://localhost:8888" },
		"no kind at all":               func(p *Provider) { p.Kind = "bing" },
		"a day of the month past 31":   func(p *Provider) { p.Resets = "32" },
		"more free than monthly quota": func(p *Provider) { p.MonthlyQuota, p.FreeQuota = 10, 20 },
		"an ftp endpoint":              func(p *Provider) { p.Endpoint = "ftp://search.example" },
		"an endpoint with a user":      func(p *Provider) { p.Endpoint = "https://me:pw@search.example" },
		"another key's name":           func(p *Provider) { p.Key = "provider.anthropic" },
		"a negative cost":              func(p *Provider) { p.CostPerSearch = -1 },
	} {
		p := ok
		edit(&p)
		if err := p.Check(); err == nil {
			t.Errorf("an entry with %s was accepted", what)
		}
	}
	moved := Provider{Name: "tavily", Kind: KindTavily, Key: KeyName, Enabled: true}
	if err := moved.Check(); err != nil {
		t.Errorf("the moved entry was refused: %v", err)
	}
}

func TestTheStateRules(t *testing.T) {
	r := testRotation
	p := Provider{Name: "alpha", Kind: KindTavily}
	now := start
	// Each failed test doubles the wait, up to web.test_max.
	s := r.out(State{}, "out of quota", time.Time{}, now)
	var waits []time.Duration
	for range 8 {
		s = r.testFailed(s, now)
		waits = append(waits, s.TestWait)
	}
	if waits[0] != time.Hour || waits[1] != 2*time.Hour || waits[5] != 24*time.Hour || waits[7] != 24*time.Hour {
		t.Errorf("the waits after failed tests are %v", waits)
	}
	// A failing provider's first test is web.fail_cooldown away.
	s = State{}
	for range 3 {
		s = r.failed(p, s, &Failure{Why: WhyUnavailable}, "", now)
	}
	if s.State != StateFailing || !s.TestDue.Equal(now.Add(15*time.Minute)) {
		t.Errorf("after three failures the state is %+v", s)
	}
	// A rolling quota runs 30 days from its first use.
	roll := Provider{Name: "beta", Kind: KindTavily, MonthlyQuota: 1, Resets: ResetsRolling}
	s = r.succeeded(roll, State{}, Quota{}, now)
	if s.State != StateOut || !s.Until.Equal(now.Add(30*24*time.Hour)) {
		t.Errorf("a rolling quota spent is %s until %s", s.State, s.Until)
	}
	if s = settle(roll, s, now.Add(30*24*time.Hour), ""); s.State != StateReady || s.Used != 0 {
		t.Errorf("after 30 days the rolling quota is %+v", s)
	}
	// A day of the month that a month does not have is its last day.
	feb := Provider{Name: "gamma", Kind: KindTavily, Resets: "31"}
	if got := periodEnd(feb, time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC)); !got.Equal(time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("a quota that resets on the 31st ends on %s after January 31", got)
	}
}
