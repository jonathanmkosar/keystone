package jobs

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
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
	"github.com/jonathanmkosar/keystone/settings"
)

// Invented, and used in throwaway folders only.
const ordinaryPassword = "an ordinary person's phrase for tasks"

// rig is one data folder with the test tenant and an ordinary tenant in
// it. serve starts a server on it, and can be called again to stand for the
// server starting a second time.
type rig struct {
	t        *testing.T
	install  *tenant.Install
	fx       testtenant.Fixtures
	signIn   signin.Options
	test     tenant.Tenant // the test tenant
	ordinary tenant.Tenant // a tenant with no models until a test adds them
	// extra are adapters a test adds to the fake.
	extra adapters.Set
	// maxSteps and maxRounds, when not zero, replace the shipped limits.
	maxSteps, maxRounds int
	// tools are the tools a test adds to the built-in ones.
	tools []tools.Tool
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

// served is one start of the server on the rig's data folder: the turn
// routes, the approval routes and the task routes, put together as
// internal/app puts them.
type served struct {
	g      *rig
	turns  *turn.Runner
	jobs   *Runner
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
	if g.maxSteps > 0 {
		cfg.MaxSteps = g.maxSteps
	}
	jc, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	if g.maxRounds > 0 {
		jc.MaxRounds = g.maxRounds
	}
	jobs := NewRunner(Options{Install: g.install, Config: jc, Turns: cfg})
	reg := tools.New()
	if err := builtin.Add(reg, builtin.Options{Limits: cfg.Limits, ReadChatMessages: cfg.ToolLoop.ReadChatMessages}); err != nil {
		g.t.Fatal(err)
	}
	for _, tl := range g.tools {
		if err := reg.Add(tl); err != nil {
			g.t.Fatal(err)
		}
	}
	if err := jobs.AddTool(reg); err != nil {
		g.t.Fatal(err)
	}
	set := adapters.Set{}
	for name, build := range g.extra {
		set[name] = build
	}
	runner := turn.NewRunner(turn.Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Adapters: set, Tools: reg, Gate: Gate})
	runner.Recover()
	jobs.Bind(runner)
	jobs.Recover()
	routes := append(turn.Routes(runner), turn.ApprovalRoutes(runner)...)
	routes = append(routes, Routes(jobs)...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, routes)
	if err != nil {
		g.t.Fatal(err)
	}
	sv := &served{g: g, turns: runner, jobs: jobs, server: httptest.NewServer(h), tokens: map[string]string{}}
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

// stop ends this start of the server, as stopping the process would: the
// turns and tasks it was working are left as they are in the database.
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

// start starts a task from a chat through the route.
func (sv *served) start(tenantID, chatID, text, title string) Task {
	sv.g.t.Helper()
	body, _ := json.Marshal(StartBody{Text: text, Title: title})
	var t Task
	sv.call(tenantID, "POST", "chats/"+chatID+"/tasks", string(body), http.StatusCreated, &t)
	return t
}

// event is one server-sent event as a client reads it.
type event struct {
	id   string
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
		case strings.HasPrefix(line, "id: "):
			cur.id = line[4:]
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

// follow reads a task's stream to its end, and returns its events and the
// task as it ended.
func (sv *served) follow(tenantID string, task Task) ([]event, Task) {
	sv.g.t.Helper()
	done := make(chan []event, 1)
	go func() {
		resp := sv.request(tenantID, "GET", "chats/"+task.ChatID+"/tasks/"+task.ID+"/events", "")
		defer resp.Body.Close()
		done <- readEvents(sv.g.t, resp.Body)
	}()
	var events []event
	select {
	case events = <-done:
	case <-time.After(20 * time.Second):
		sv.g.t.Fatalf("task %s did not end", task.ID)
	}
	if len(events) == 0 || events[len(events)-1].name != EventEnd {
		sv.g.t.Fatalf("the task's stream did not end with an end event: %d events", len(events))
	}
	var end Task
	if err := json.Unmarshal([]byte(events[len(events)-1].data), &end); err != nil {
		sv.g.t.Fatal(err)
	}
	return events, end
}

// messages returns a chat's current branch.
func (sv *served) messages(tenantID, chatID string) []turn.Message {
	sv.g.t.Helper()
	var got turn.ChatReply
	sv.call(tenantID, "GET", "chats/"+chatID, "", http.StatusOK, &got)
	return got.Messages
}

func text(m turn.Message) string { return seam.Text(seam.Message{Role: m.Role, Blocks: m.Blocks}) }

// chanAfter is how long a test waits for something it is sure of.
func chanAfter(*testing.T) <-chan time.Time { return time.After(10 * time.Second) }

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

// approveWhenAsked approves, through the route, the first call of the
// tenant's that waits for an answer. The function it returns waits for
// that, and says which tool asked, why, and how many times ran had counted
// while it waited.
func (sv *served) approveWhenAsked(tenantID string, ran *atomic.Int32) func() (turn.Approval, int32) {
	type found struct {
		a   turn.Approval
		ran int32
	}
	done := make(chan found, 1)
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var a turn.Approval
			var err error
			sv.g.with(tenantID, func(s *tenant.Store) {
				err = s.DB().QueryRow("SELECT id, turn_id, chat_id, tool, reason, why FROM tool_approvals WHERE status = 'pending' ORDER BY asked_at LIMIT 1").
					Scan(&a.ID, &a.TurnID, &a.ChatID, &a.Tool, &a.Reason, &a.Why)
			})
			if err == nil {
				var n int32
				if ran != nil {
					n = ran.Load()
				}
				resp := sv.request(tenantID, "POST", "chats/"+a.ChatID+"/turns/"+a.TurnID+"/approvals/"+a.ID+"/approve", "")
				resp.Body.Close()
				done <- found{a, n}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		done <- found{}
	}()
	return func() (turn.Approval, int32) {
		f := <-done
		return f.a, f.ran
	}
}

// scripted is the fake provider playing scripts written in a test.
func scripted(t *testing.T, raw string) seam.Provider {
	t.Helper()
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

// slow is a provider that writes the start of a reply and then waits until
// it is cancelled, as a model does in the middle of a long answer.
// Several calls may share one: the channels say what the first did.
type slow struct {
	writing   chan struct{} // closed when the first words are out
	cancelled chan struct{} // closed when the call's context ends
	wrote     sync.Once
	ended     sync.Once
}

func newSlow() *slow { return &slow{writing: make(chan struct{}), cancelled: make(chan struct{})} }

func (p *slow) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	for _, e := range []seam.Event{
		{Type: seam.EventStart, Model: req.Model},
		{Type: seam.EventBlockStart, Index: 0, Block: &seam.Block{Type: seam.BlockText}},
		{Type: seam.EventBlockDelta, Index: 0, Text: "Half of the lighthouse no"},
	} {
		if err := emit(e); err != nil {
			return err
		}
	}
	p.wrote.Do(func() { close(p.writing) })
	<-ctx.Done()
	p.ended.Do(func() { close(p.cancelled) })
	return ctx.Err()
}

// answers is a provider that always gives the same reply, with the same
// usage, and counts its calls.
type answers struct {
	reply string
	calls *atomic.Int32
}

func (p answers) Stream(_ context.Context, req seam.Request, emit func(seam.Event) error) error {
	p.calls.Add(1)
	events, err := seam.Events(seam.Response{
		Model:      req.Model,
		Message:    seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: p.reply}}},
		StopReason: seam.StopEnd,
		Usage:      seam.Usage{InputTokens: 1000, OutputTokens: 200},
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

// task reads a task's row.
func (g *rig) task(tenantID, id string) row {
	g.t.Helper()
	var j row
	g.with(tenantID, func(s *tenant.Store) {
		var err error
		if j, err = getJob(context.Background(), s.DB(), id); err != nil {
			g.t.Fatal(err)
		}
	})
	return j
}

// chatExists reports whether a chat is in a tenant's record.
func (g *rig) chatExists(tenantID, id string) bool {
	ok := false
	g.with(tenantID, func(s *tenant.Store) {
		_, err := chat.Get(s, id)
		ok = err == nil
	})
	return ok
}
