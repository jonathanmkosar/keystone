package schedules

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

	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/builtin"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/settings"
)

// Invented, and used in throwaway folders only.
const ordinaryPassword = "an ordinary person's phrase for schedules"

// The words of the tests' schedules, and the replies to them. All invented.
const (
	harbourPrompt = "Write the morning harbour note: the tide and the ferry."
	harbourReply  = "Good morning. High water is at noon, and the first ferry leaves at nine."
	gatedPrompt   = "Read the slow tide gauge, then say what it shows."
	gatedReply    = "The tide gauge shows the water rising."
)

func harbourScripts(t *testing.T) seam.Provider {
	t.Helper()
	raw := `{"test_data": true, "scripts": [
	{"name": "the harbour note", "when": {"user_text": "` + harbourPrompt + `"}, "steps": [
		{"reply": {"message": {"role": "assistant", "blocks": [{"type": "text", "text": "` + harbourReply + `"}]},
			"stop_reason": "end", "usage": {"input_tokens": 300, "output_tokens": 20}}}]}]}`
	scripts, err := fake.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	p, err := fake.New(scripts, fake.Options{DeltaSize: 12})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// gated answers every call once its gate is opened, and says when a call
// is waiting at it, as a model in the middle of a long answer.
type gated struct {
	mu      sync.Mutex
	open    chan struct{}
	arrived chan struct{}
	once    sync.Once
	harbour seam.Provider
}

func newGated(t *testing.T) *gated {
	return &gated{open: make(chan struct{}), arrived: make(chan struct{}), harbour: harbourScripts(t)}
}

func (p *gated) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	if text := lastUserText(req); text != gatedPrompt {
		return p.harbour.Stream(ctx, req, emit)
	}
	p.once.Do(func() { close(p.arrived) })
	select {
	case <-p.open:
	case <-ctx.Done():
		return ctx.Err()
	}
	events, err := seam.Events(seam.Response{
		Model:      req.Model,
		Message:    seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: gatedReply}}},
		StopReason: seam.StopEnd,
		Usage:      seam.Usage{InputTokens: 100, OutputTokens: 10},
	})
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

func lastUserText(req seam.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == seam.RoleUser {
			if text := strings.TrimSpace(seam.Text(req.Messages[i])); text != "" {
				return text
			}
		}
	}
	return ""
}

// clock is the scheduler's clock in a test: it says what time it is told.
type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = t
}

// rig is one data folder with the test tenant and an ordinary tenant in
// it. serve starts a server on it, and can be called again to stand for the
// server starting a second time.
type rig struct {
	t        *testing.T
	install  *tenant.Install
	fx       testtenant.Fixtures
	signIn   signin.Options
	test     tenant.Tenant
	ordinary tenant.Tenant
	clock    *testClock
	// config, when changed by a test, replaces the shipped one.
	config Config
}

func newRig(t *testing.T, now time.Time) *rig {
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
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Poll = 20 * time.Millisecond
	g := &rig{t: t, install: in, fx: fx, signIn: o.SignIn, test: loaded.Tenant, ordinary: ordinary, clock: &testClock{at: now}, config: cfg}
	g.with(ordinary.ID, func(s *tenant.Store) {
		if err := signin.SetPassword(s, ordinaryPassword, o.SignIn); err != nil {
			t.Fatal(err)
		}
	})
	return g
}

func (g *rig) with(tenantID string, f func(s *tenant.Store)) {
	g.t.Helper()
	s, err := g.install.Store(tenantID)
	if err != nil {
		g.t.Fatal(err)
	}
	defer s.Close()
	f(s)
}

// set stores one of a person's settings.
func (g *rig) set(tenantID, key, value string) {
	g.t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		g.t.Fatal(err)
	}
	g.with(tenantID, func(s *tenant.Store) {
		if err := tenantsettings.Set(context.Background(), s, reg, tenantsettings.Person, key, value); err != nil {
			g.t.Fatal(err)
		}
	})
}

// served is one start of the server on the rig's data folder, put together
// as internal/app puts it: the turn runner, background tasks, and the
// scheduler, started.
type served struct {
	g      *rig
	turns  *turn.Runner
	jobs   *jobs.Runner
	sched  *Scheduler
	server *httptest.Server
	tokens map[string]string
}

func (g *rig) serve(provider seam.Provider) *served {
	g.t.Helper()
	cfg, err := turn.DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 20 * time.Millisecond
	jc, err := jobs.DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	runner := jobs.NewRunner(jobs.Options{Install: g.install, Config: jc, Turns: cfg})
	reg := tools.New()
	if err := builtin.Add(reg, builtin.Options{Limits: cfg.Limits, ReadChatMessages: cfg.ToolLoop.ReadChatMessages}); err != nil {
		g.t.Fatal(err)
	}
	turns := turn.NewRunner(turn.Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Tools: reg, Gate: jobs.Gate})
	turns.Recover()
	runner.Bind(turns)
	runner.Recover()
	sched := New(Options{Install: g.install, Config: g.config, Jobs: runner, Limits: cfg.Limits, Now: g.clock.now})
	routes := append(turn.Routes(turns), jobs.Routes(runner)...)
	routes = append(routes, Routes(sched)...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, routes)
	if err != nil {
		g.t.Fatal(err)
	}
	sv := &served{g: g, turns: turns, jobs: runner, sched: sched, server: httptest.NewServer(h), tokens: map[string]string{}}
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
	if !sched.Start(turns) {
		g.t.Fatal("the scheduler did not start")
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
func (sv *served) call(tenantID, method, path string, body any, want int, out any) {
	sv.g.t.Helper()
	text := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			sv.g.t.Fatal(err)
		}
		text = string(raw)
	}
	resp := sv.request(tenantID, method, path, text)
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

// make makes a schedule through the route.
func (sv *served) make(tenantID string, body ScheduleBody) Schedule {
	sv.g.t.Helper()
	var got Schedule
	sv.call(tenantID, "POST", "schedules", body, http.StatusCreated, &got)
	return got
}

// read reads a schedule and its runs through the route.
func (sv *served) read(tenantID, id string) ScheduleReply {
	sv.g.t.Helper()
	var got ScheduleReply
	sv.call(tenantID, "GET", "schedules/"+id, nil, http.StatusOK, &got)
	return got
}

// messages returns a chat's current branch.
func (sv *served) messages(tenantID, chatID string) (turn.Chat, []turn.Message) {
	sv.g.t.Helper()
	var got turn.ChatReply
	sv.call(tenantID, "GET", "chats/"+chatID, nil, http.StatusOK, &got)
	return got.Chat, got.Messages
}

// settle waits until a schedule has n runs and the newest has ended with
// its result in its chat, and returns them.
func (sv *served) settle(tenantID, id string, n int) ScheduleReply {
	sv.g.t.Helper()
	var got ScheduleReply
	sv.g.waitFor("the schedule's runs to end", func() bool {
		got = sv.read(tenantID, id)
		if len(got.Runs) != n {
			return false
		}
		r := got.Runs[0]
		if r.Status != jobs.StatusDone && r.Status != jobs.StatusStopped && r.Status != jobs.StatusFailed && r.Status != RunFailed {
			return false
		}
		if r.TaskID == "" {
			return true
		}
		var t jobs.Task
		sv.g.with(tenantID, func(s *tenant.Store) {
			t, _ = jobs.Read(context.Background(), s, r.TaskID)
		})
		return t.NoteMessageID != 0
	})
	return got
}

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

// ticks lets the scheduler look a few more times.
func (sv *served) ticks(n int) {
	for i := 0; i < n; i++ {
		sv.sched.Tick(context.Background())
	}
	time.Sleep(5 * sv.g.config.Poll)
}

func text(m turn.Message) string { return seam.Text(seam.Message{Role: m.Role, Blocks: m.Blocks}) }

func jsonMarshal(v any) (string, error) {
	raw, err := json.Marshal(v)
	return string(raw), err
}

func daily(name, prompt, clock string) ScheduleBody {
	return ScheduleBody{Name: name, Prompt: prompt, Recurrence: Recurrence{Kind: KindDaily, Time: clock}}
}
