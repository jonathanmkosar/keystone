package wikijobs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/schedules"
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

// Everything in these tests is invented: the person, the words, the
// models and the tokens.
const (
	keeperPassword = "an invented phrase for the wiki jobs"
	beehiveMessage = "I keep two beehives at the allotment by the harbour."
	beehiveFact    = "The person keeps two beehives at the allotment by the harbour."
)

var (
	houseModel = models.Model{
		Name: "house-small", Label: "House Small", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
		ProviderModel: "small-1", ContextWindow: 32768, OutputLimit: 2048, Class: models.ClassLocal,
		Capabilities: []models.Capability{models.CapTools},
	}
	paidModel = models.Model{
		Name: "paid-large", Label: "Paid Large", Adapter: models.AdapterAnthropic, Endpoint: "https://api.provider-one.example",
		ProviderModel: "example-large-1", ContextWindow: 32768, OutputLimit: 2048, Class: models.ClassHosted,
		Capabilities: []models.Capability{models.CapTools}, Price: models.Price{Input: 2, Output: 10},
	}
)

// scripted plays the person's local model: it answers a chat, pulls the
// fact out of it for the memory pass, and works as a wiki job, calling the
// job's tools and ending with the answer the job's instructions ask for.
type scripted struct {
	mu    sync.Mutex
	calls []seam.Request
}

func (p *scripted) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	p.mu.Lock()
	p.calls = append(p.calls, req)
	p.mu.Unlock()
	msg := p.answer(req)
	stop := seam.StopEnd
	for _, b := range msg.Blocks {
		if b.Type == seam.BlockToolCall {
			stop = seam.StopToolCall
		}
	}
	events, err := seam.Events(seam.Response{Model: req.Model, Message: msg, StopReason: stop, Usage: seam.Usage{InputTokens: 200, OutputTokens: 40}})
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := emit(e); err != nil {
			return err
		}
	}
	return nil
}

func says(text string) seam.Message {
	return seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}}
}

func calls(names ...string) seam.Message {
	m := seam.Message{Role: seam.RoleAssistant}
	for i := 0; i+1 < len(names); i += 2 {
		m.Blocks = append(m.Blocks, seam.Block{Type: seam.BlockToolCall, ID: fmt.Sprintf("call_%d_%s", i, names[i]), Name: names[i], Input: json.RawMessage(names[i+1])})
	}
	return m
}

// results returns the text of each tool result in a request, by tool name.
func results(req seam.Request) map[string]string {
	names := map[string]string{}
	out := map[string]string{}
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			switch b.Type {
			case seam.BlockToolCall:
				names[b.ID] = b.Name
			case seam.BlockToolResult:
				var text strings.Builder
				for _, c := range b.Content {
					text.WriteString(c.Text)
				}
				out[names[b.ID]] = text.String()
			}
		}
	}
	return out
}

func allText(req seam.Request) string {
	var b strings.Builder
	for _, s := range req.System {
		b.WriteString(s.Text + "\n")
	}
	for _, m := range req.Messages {
		b.WriteString(seam.Text(m) + "\n")
	}
	return b.String()
}

func (p *scripted) answer(req seam.Request) seam.Message {
	text := allText(req)
	got := results(req)
	switch {
	case strings.Contains(text, "long-term memory of a personal assistant"):
		if strings.Contains(text, "beehives") {
			return says(`{"facts": ["` + beehiveFact + `"]}`)
		}
		return says(`{"facts": []}`)
	case strings.Contains(text, "You are the compile job of this person's Waymarks wiki"):
		if got["list_sources"] == "" {
			return calls("whoami", `{}`, "read_catalog", `{}`, "list_sources", `{}`)
		}
		if strings.Contains(got["read_catalog"], "garden/beehives") {
			return says("Every source is cited already.\n```json\n{\"batches\": [], \"report\": \"No changes.\"}\n```")
		}
		id := firstID(got["list_sources"])
		if got["read_source"] == "" {
			return calls("read_source", `{"source_id": `+id+`}`)
		}
		if !strings.Contains(got["read_source"], "beehives") {
			return says(`No source holds a fact to file. ` + "```json\n{\"batches\": [], \"report\": \"No changes.\"}\n```")
		}
		return says("I filed the beehives under the garden.\n\n```json\n" + `{"batches": [{"note": "File the beehives from chat", "writes": [` +
			`{"ref": "hives", "space": "memory", "path": "garden/beehives", "title": "Beehives", "body": "` + beehiveFact + ` [[source:` + id + `]] Part of [[new:garden|the garden]].", "properties": {"kind": "topic"}, "sources": [` + id + `]},` +
			`{"ref": "garden", "space": "memory", "path": "garden", "title": "Garden", "body": "The allotment by the harbour. See [[new:hives]] [[source:` + id + `]].", "sources": [` + id + `]}]}],` +
			` "report": "Two pages, citing source ` + id + `."}` + "\n```")
	case strings.Contains(text, "You are the compile job of this person's built-in knowledge store"):
		if got["list_uncompiled_sources"] == "" {
			return calls("list_uncompiled_sources", `{}`)
		}
		id := firstID(got["list_uncompiled_sources"])
		return says("```json\n" + `{"batches": [{"note": "File the beehives", "writes": [{"space": "memory", "path": "garden/beehives", "title": "Beehives", "body": "` + beehiveFact + ` [[source:` + id + `]]", "sources": ["` + id + `"]}]}], "report": "One page."}` + "\n```")
	}
	return says("Noted: two beehives at the allotment.")
}

// firstID is the id of the source that holds the beehives, from a list.
func firstID(list string) string {
	var items []map[string]any
	if json.Unmarshal([]byte(list), &items) == nil {
		for _, it := range items {
			raw, _ := json.Marshal(it)
			if strings.Contains(string(raw), "beehives") || len(items) == 1 {
				if id := idText(it["id"]); id != "" {
					return id
				}
			}
		}
		if len(items) > 0 {
			return idText(items[len(items)-1]["id"])
		}
	}
	return "0"
}

// counting is a hosted model that must never be called by a wiki run.
type counting struct{ n *atomic.Int32 }

func (c counting) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	c.n.Add(1)
	events, _ := seam.Events(seam.Response{Model: req.Model, Message: says("From the paid model."), StopReason: seam.StopEnd, Usage: seam.Usage{InputTokens: 10, OutputTokens: 5}})
	for _, e := range events {
		if err := emit(e); err != nil {
			return err
		}
	}
	return nil
}

// rig is one data folder with one invented person, served as internal/app
// serves it: the turn runner with the wiki jobs' connectors, gate and
// memory switch, background tasks, the scheduler and the wiki jobs.
type rig struct {
	t        *testing.T
	install  *tenant.Install
	person   tenant.Tenant
	signIn   signin.Options
	model    *scripted
	paid     atomic.Int32
	turns    *turn.Runner
	jobs     *jobs.Runner
	sched    *schedules.Scheduler
	wiki     *Runner
	server   *httptest.Server
	session  string
	settings []settings.Setting
}

func newRig(t *testing.T) *rig {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		t.Fatal(err)
	}
	person, err := in.Create("Harbour Keeper")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	g := &rig{t: t, install: in, person: person, signIn: o.SignIn, model: &scripted{}, settings: reg}
	g.with(func(s *tenant.Store) {
		if err := signin.SetPassword(s, keeperPassword, o.SignIn); err != nil {
			t.Fatal(err)
		}
		for _, m := range []models.Model{houseModel, paidModel} {
			if err := models.Add(context.Background(), s, models.Limits{MaxModels: 5}, m); err != nil {
				t.Fatal(err)
			}
		}
	})
	g.set("chats.default_model", "house-small")
	g.set("routing.memory_model", "house-small")
	g.set("routing.fallback_models", "paid-large")
	return g
}

func (g *rig) with(f func(s *tenant.Store)) {
	g.t.Helper()
	s, err := g.install.Store(g.person.ID)
	if err != nil {
		g.t.Fatal(err)
	}
	defer s.Close()
	f(s)
}

func (g *rig) set(key, value string) {
	g.t.Helper()
	g.with(func(s *tenant.Store) {
		if err := tenantsettings.Set(context.Background(), s, g.settings, tenantsettings.Person, key, value); err != nil {
			g.t.Fatalf("setting %s: %v", key, err)
		}
	})
}

func (g *rig) secret(name, value string) {
	g.t.Helper()
	g.with(func(s *tenant.Store) {
		if err := s.Secrets().Set(name, value); err != nil {
			g.t.Fatal(err)
		}
	})
}

func (g *rig) serve() {
	t := g.t
	t.Helper()
	cfg, err := turn.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.StreamPoll = 20 * time.Millisecond
	jc, err := jobs.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	g.jobs = jobs.NewRunner(jobs.Options{Install: g.install, Config: jc, Turns: cfg})
	reg := tools.New()
	if err := builtin.Add(reg, builtin.Options{Limits: cfg.Limits, ReadChatMessages: cfg.ToolLoop.ReadChatMessages}); err != nil {
		t.Fatal(err)
	}
	set := adapters.Set{
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return g.model, nil },
		models.AdapterAnthropic:       func(models.Model, string) (seam.Provider, error) { return counting{&g.paid}, nil },
	}
	g.turns = turn.NewRunner(turn.Options{Install: g.install, Config: cfg, PageLimit: 50, Adapters: set, Tools: reg,
		Gate: Gate(jobs.Gate), Moved: unattended.Moved, Connectors: Connectors(nil), SkipMemory: SkipMemory})
	g.turns.Recover()
	g.jobs.Bind(g.turns)
	g.jobs.Recover()
	sc, err := schedules.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	sc.Poll = 20 * time.Millisecond
	g.sched = schedules.New(schedules.Options{Install: g.install, Config: sc, Jobs: g.jobs, Limits: cfg.Limits})
	wc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	wc.Poll = 20 * time.Millisecond
	g.wiki = New(Options{Install: g.install, Config: wc, Schedules: g.sched})
	routes := append(turn.Routes(g.turns), jobs.Routes(g.jobs)...)
	routes = append(routes, schedules.Routes(g.sched)...)
	routes = append(routes, Routes(g.wiki)...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, routes)
	if err != nil {
		t.Fatal(err)
	}
	g.server = httptest.NewServer(h)
	t.Cleanup(func() {
		g.server.CloseClientConnections()
		g.server.Close()
		g.turns.Close()
	})
	g.with(func(s *tenant.Store) {
		token, _, err := signin.SignIn(s, keeperPassword, g.signIn)
		if err != nil {
			t.Fatal(err)
		}
		g.session = token
	})
	if !g.sched.Start(g.turns) || !g.wiki.Start(g.turns) {
		t.Fatal("the scheduler or the wiki jobs did not start")
	}
}

// call makes one call of the API as the person, checks its status, and
// reads its answer into out.
func (g *rig) call(method, path string, body any, want int, out any) string {
	g.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, g.server.URL+api.Prefix+path, rd)
	if err != nil {
		g.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.session})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		g.t.Fatalf("%s %s answered %d, want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			g.t.Fatalf("%s %s: %v in %s", method, path, err, raw)
		}
	}
	return string(raw)
}

// tell sends a message in a new chat and waits for its reply.
func (g *rig) tell(text string) string {
	g.t.Helper()
	var c turn.ChatReply
	g.call("POST", "chats", nil, http.StatusCreated, &c)
	g.call("POST", "chats/"+c.Chat.ID+"/turns", map[string]string{"text": text}, http.StatusOK, nil)
	return c.Chat.ID
}

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

// state reads one job through the route.
func (g *rig) state(job string) JobState {
	g.t.Helper()
	var got JobsReply
	g.call("GET", "wiki-jobs", nil, http.StatusOK, &got)
	for _, j := range got.Jobs {
		if j.Name == job {
			return j
		}
	}
	g.t.Fatalf("no job %s in the list", job)
	return JobState{}
}

// settled waits until a job's newest run has ended and Keystone has written
// what it asked for.
func (g *rig) settled(job, runID string) JobState {
	g.t.Helper()
	var st JobState
	g.waitFor("the "+job+" run to be written", func() bool {
		st = g.state(job)
		return st.LastWritten != nil && st.LastWritten.RunID == runID && st.LastWritten.State != stateApplying
	})
	return st
}
