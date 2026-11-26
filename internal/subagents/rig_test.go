package subagents

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/builtin"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/settings"
)

// Invented, and used in throwaway folders only.
const ordinaryPassword = "an ordinary person's phrase for sub-agents"

// rig is one data folder with the test tenant and an ordinary tenant in
// it. serve starts a server on it, put together as internal/app puts it.
type rig struct {
	t        *testing.T
	install  *tenant.Install
	fx       testtenant.Fixtures
	signIn   signin.Options
	test     tenant.Tenant // the test tenant
	ordinary tenant.Tenant // a tenant with no models until a test adds them
	// extra are adapters a test adds to the fake.
	extra adapters.Set
	// tools are the tools a test adds to the built-in ones.
	tools []tools.Tool
	// config changes the shipped settings of sub-agents for a test.
	config func(*Config)
}

func newRig(t *testing.T) *rig {
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
	g := &rig{t: t, install: in, fx: fx, signIn: o.SignIn, test: loaded.Tenant, ordinary: ordinary}
	g.with(ordinary.ID, func(s *tenant.Store) {
		if err := signin.SetPassword(s, ordinaryPassword, o.SignIn); err != nil {
			t.Fatal(err)
		}
	})
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
	g      *rig
	turns  *turn.Runner
	subs   *Runner
	config Config
	server *httptest.Server
	tokens map[string]string
}

// serve starts a server whose test tenant is answered by provider.
func (g *rig) serve(provider seam.Provider) *served {
	g.t.Helper()
	cfg, err := turn.DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 50 * time.Millisecond
	sc, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	if g.config != nil {
		g.config(&sc)
	}
	subs := NewRunner(Options{Install: g.install, Config: sc, Turns: cfg})
	reg := tools.New()
	if err := builtin.Add(reg, builtin.Options{Limits: cfg.Limits, ReadChatMessages: cfg.ToolLoop.ReadChatMessages}); err != nil {
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
		Gate: unattended.Gate, Moved: unattended.Moved, MaxSteps: subs.MaxSteps, SkipMemory: subs.SkipMemory(nil)})
	runner.Recover()
	subs.Bind(runner)
	subs.Recover()
	routes := append(turn.Routes(runner), turn.ApprovalRoutes(runner)...)
	routes = append(routes, Routes(subs)...)
	uc, err := unattended.DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	routes = append(routes, unattended.Routes(unattended.Options{Config: uc})...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, routes)
	if err != nil {
		g.t.Fatal(err)
	}
	sv := &served{g: g, turns: runner, subs: subs, config: sc, server: httptest.NewServer(h), tokens: map[string]string{}}
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

// event is one server-sent event as a client reads it.
type event struct {
	name string
	data string
}

func readEvents(t *testing.T, body io.Reader) []event {
	t.Helper()
	var all []event
	var cur event
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.name = line[7:]
		case strings.HasPrefix(line, "data: "):
			cur.data = line[6:]
		case line == "" && cur.name != "":
			all = append(all, cur)
			cur = event{}
		}
	}
	return all
}

// send sends a message in a chat and reads the turn's stream to its end,
// within the time given.
func (sv *served) send(tenantID, chatID, text string, within time.Duration) []event {
	sv.g.t.Helper()
	body, _ := json.Marshal(map[string]string{"text": text})
	done := make(chan []event, 1)
	go func() {
		resp := sv.request(tenantID, "POST", "chats/"+chatID+"/turns", string(body))
		defer resp.Body.Close()
		done <- readEvents(sv.g.t, resp.Body)
	}()
	select {
	case events := <-done:
		return events
	case <-time.After(within):
		sv.g.t.Fatalf("the turn did not end within %v", within)
	}
	return nil
}

// messages returns a chat's current branch.
func (sv *served) messages(tenantID, chatID string) []turn.Message {
	sv.g.t.Helper()
	var got turn.ChatReply
	sv.call(tenantID, "GET", "chats/"+chatID, "", http.StatusOK, &got)
	return got.Messages
}

// subagents returns the sub-agents a chat's turns started.
func (sv *served) subagents(tenantID, chatID string) []Subagent {
	sv.g.t.Helper()
	var got SubagentsReply
	sv.call(tenantID, "GET", "chats/"+chatID+"/subagents", "", http.StatusOK, &got)
	return got.Subagents
}

func text(m turn.Message) string { return seam.Text(seam.Message{Role: m.Role, Blocks: m.Blocks}) }

// waitFor polls until ok is true, or fails the test.
func (g *rig) waitFor(what string, ok func() bool) {
	g.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			g.t.Fatalf("waited too long for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// toolResult returns the text of the result of the first call to name in
// a chat's messages, and whether it had one.
func toolResult(msgs []turn.Message, name string) (string, bool) {
	var callID string
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == seam.BlockToolCall && b.Name == name && callID == "" {
				callID = b.ID
			}
			if b.Type == seam.BlockToolResult && callID != "" && b.ID == callID {
				return seam.Text(seam.Message{Blocks: b.Content}), true
			}
		}
	}
	return "", false
}
