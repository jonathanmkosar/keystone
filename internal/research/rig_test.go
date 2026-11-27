package research

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/subagents"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/builtin"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/settings"
)

// Invented, and used in throwaway folders only.
const (
	ordinaryPassword = "an ordinary person's phrase for research"
	searchKey        = "tvly-invented-key-for-research-tests"
)

// searchStandIn is a stand-in search service on this machine. It answers
// each search with the results its query has in pages, and keeps the
// queries it was sent.
type searchStandIn struct {
	*httptest.Server
	mu      sync.Mutex
	pages   map[string][]web.Result
	queries []string
}

func newSearchStandIn(t *testing.T, pages map[string][]web.Result) *searchStandIn {
	st := &searchStandIn{pages: pages}
	st.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var body struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(raw, &body)
		st.mu.Lock()
		st.queries = append(st.queries, body.Query)
		results := st.pages[body.Query]
		st.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+searchKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		out := []map[string]string{}
		for _, p := range results {
			out = append(out, map[string]string{"title": p.Title, "url": p.URL, "content": p.Content})
		}
		json.NewEncoder(w).Encode(map[string]any{"results": out})
	}))
	t.Cleanup(st.Close)
	return st
}

// rig is one data folder with the test tenant and an ordinary tenant in
// it. serve starts a server on it, put together as internal/app puts it.
type rig struct {
	t        *testing.T
	install  *tenant.Install
	fx       testtenant.Fixtures
	signIn   signin.Options
	test     tenant.Tenant
	ordinary tenant.Tenant
	search   *searchStandIn
	// extra are adapters a test adds to the fake.
	extra adapters.Set
	// tools are the tools a test adds to the built-in ones.
	tools []tools.Tool
	// config changes the shipped settings of research for a test.
	config func(*Config)
}

func newRig(t *testing.T, pages map[string][]web.Result) *rig {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
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
	loaded, err := testtenant.Load(in, fx, o)
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := in.Create("Ordinary Person")
	if err != nil {
		t.Fatal(err)
	}
	g := &rig{t: t, install: in, fx: fx, signIn: o.SignIn, test: loaded.Tenant, ordinary: ordinary, search: newSearchStandIn(t, pages)}
	g.with(ordinary.ID, func(s *tenant.Store) {
		if err := signin.SetPassword(s, ordinaryPassword, o.SignIn); err != nil {
			t.Fatal(err)
		}
	})
	// Both people have an invented search key in their vault.
	for _, id := range []string{loaded.Tenant.ID, ordinary.ID} {
		g.with(id, func(s *tenant.Store) {
			if err := s.Secrets().Set(web.KeyName, searchKey); err != nil {
				t.Fatal(err)
			}
		})
	}
	return g
}

// with runs f on a tenant's store.
func (g *rig) with(tenantID string, f func(s *tenant.Store)) {
	g.t.Helper()
	s, err := g.install.Store(tenantID)
	if err != nil {
		g.t.Fatal(err)
	}
	defer s.Close()
	f(s)
}

// set stores a setting for a tenant, as the person or the admin.
func (g *rig) set(tenantID string, by tenantsettings.Role, key, value string) {
	g.t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		g.t.Fatal(err)
	}
	g.with(tenantID, func(s *tenant.Store) {
		if err := tenantsettings.Set(context.Background(), s, reg, by, key, value); err != nil {
			g.t.Fatal(err)
		}
	})
}

// addModels registers models for the ordinary tenant.
func (g *rig) addModels(ms ...models.Model) {
	g.t.Helper()
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		for _, m := range ms {
			if err := models.Add(context.Background(), s, models.Limits{MaxModels: 5}, m); err != nil {
				g.t.Fatal(err)
			}
		}
	})
}

// served is one start of the server on the rig's data folder.
type served struct {
	g        *rig
	turns    *turn.Runner
	subs     *subagents.Runner
	research *Runner
	config   Config
	server   *httptest.Server
	tokens   map[string]string
}

func defaults(t *testing.T) func(key string) (string, bool) {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	return func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	}
}

// serve starts a server whose test tenant is answered by provider.
func (g *rig) serve(provider seam.Provider) *served {
	g.t.Helper()
	cfg, err := turn.DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 50 * time.Millisecond
	sc, err := subagents.DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	rc, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	if g.config != nil {
		g.config(&rc)
	}
	ao, err := artifacts.ReadOptions(defaults(g.t))
	if err != nil {
		g.t.Fatal(err)
	}
	wo, err := web.ReadOptions(defaults(g.t))
	if err != nil {
		g.t.Fatal(err)
	}
	wo.SearchEndpoint = g.search.URL // the stand-in on this machine
	subs := subagents.NewRunner(subagents.Options{Install: g.install, Config: sc, Turns: cfg})
	res := NewRunner(Options{Install: g.install, Config: rc, Turns: cfg, Subagents: subs, Artifacts: ao, PartChars: sc.ResultChars})
	reg := tools.New()
	if err := builtin.Add(reg, builtin.Options{Limits: cfg.Limits, ReadChatMessages: cfg.ToolLoop.ReadChatMessages}); err != nil {
		g.t.Fatal(err)
	}
	if err := web.Add(reg, wo); err != nil {
		g.t.Fatal(err)
	}
	if err := artifacts.Add(reg, ao); err != nil {
		g.t.Fatal(err)
	}
	for _, tl := range g.tools {
		if err := reg.Add(tl); err != nil {
			g.t.Fatal(err)
		}
	}
	if err := subs.AddTool(reg); err != nil {
		g.t.Fatal(err)
	}
	set := adapters.Set{}
	for name, build := range g.extra {
		set[name] = build
	}
	runner := turn.NewRunner(turn.Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Adapters: set, Tools: reg,
		Gate: unattended.Gate, Moved: unattended.Moved, MaxSteps: subs.MaxSteps, SkipMemory: res.SkipMemory(subs.SkipMemory(nil))})
	runner.Recover()
	subs.Bind(runner)
	subs.Recover()
	res.Bind(runner)
	res.Recover()
	routes := append(turn.Routes(runner), turn.ApprovalRoutes(runner)...)
	routes = append(routes, subagents.Routes(subs)...)
	routes = append(routes, Routes(res)...)
	routes = append(routes, artifacts.Routes(ao)...)
	uc, err := unattended.DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	routes = append(routes, unattended.Routes(unattended.Options{Config: uc})...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, routes)
	if err != nil {
		g.t.Fatal(err)
	}
	sv := &served{g: g, turns: runner, subs: subs, research: res, config: rc, server: httptest.NewServer(h), tokens: map[string]string{}}
	g.t.Cleanup(sv.stop)
	for id, password := range map[string]string{g.test.ID: g.fx.Password, g.ordinary.ID: ordinaryPassword} {
		g.with(id, func(s *tenant.Store) {
			token, _, err := signin.SignIn(s, password, g.signIn)
			if err != nil {
				g.t.Fatal(err)
			}
			sv.tokens[id] = token
		})
	}
	return sv
}

// stop ends this start of the server, as stopping the process would.
func (sv *served) stop() {
	sv.server.CloseClientConnections()
	sv.server.Close()
	sv.turns.Close()
}

func (sv *served) request(tenantID, method, path, body string) *http.Response {
	sv.g.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, sv.server.URL+api.Prefix+path, rd)
	if err != nil {
		sv.g.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: api.CookieName, Value: sv.tokens[tenantID]})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		sv.g.t.Fatal(err)
	}
	return resp
}

// call makes a call that answers with JSON, checks its status and reads the
// answer into out.
func (sv *served) call(tenantID, method, path, body string, want int, out any) {
	sv.g.t.Helper()
	resp := sv.request(tenantID, method, path, body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		sv.g.t.Fatalf("%s %s answered %d, want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			sv.g.t.Fatalf("%s %s: %v in %s", method, path, err, raw)
		}
	}
}

func (sv *served) newChat(tenantID string) string {
	sv.g.t.Helper()
	var made turn.ChatReply
	sv.call(tenantID, "POST", "chats", "", http.StatusCreated, &made)
	return made.Chat.ID
}

// messages returns a chat's current branch.
func (sv *served) messages(tenantID, chatID string) []turn.Message {
	sv.g.t.Helper()
	var got turn.ChatReply
	sv.call(tenantID, "GET", "chats/"+chatID, "", http.StatusOK, &got)
	return got.Messages
}

func text(m turn.Message) string { return seam.Text(seam.Message{Role: m.Role, Blocks: m.Blocks}) }

// start starts a research task from a chat.
func (sv *served) start(tenantID, chatID string, body StartBody) Research {
	sv.g.t.Helper()
	raw, _ := json.Marshal(body)
	var got Research
	sv.call(tenantID, "POST", "chats/"+chatID+"/research", string(raw), http.StatusCreated, &got)
	return got
}

// one reads one research task.
func (sv *served) one(tenantID, chatID, id string) Research {
	sv.g.t.Helper()
	var got Research
	sv.call(tenantID, "GET", "chats/"+chatID+"/research/"+id, "", http.StatusOK, &got)
	return got
}

// ends waits for a research task to end and its note to be written, and
// returns it.
func (sv *served) ends(tenantID, chatID, id string) Research {
	sv.g.t.Helper()
	var got Research
	sv.g.waitFor("the research task to end", func() bool {
		got = sv.one(tenantID, chatID, id)
		over := got.Status == StatusDone || got.Status == StatusStopped || got.Status == StatusFailed
		return over && got.NoteMessageID != 0 && (got.Status != StatusDone || got.ArtifactID != "")
	})
	return got
}

// waitFor polls until ok is true, or fails the test.
func (g *rig) waitFor(what string, ok func() bool) {
	g.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			g.t.Fatalf("waited too long for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
