package unattended

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// Invented models and an invented sub-agent: the sub-agent is the stand-in
// for P8-02, which registers sub-agents through the same seam.
var (
	paid  = models.Model{Name: "paid-large", Class: models.ClassHosted, Price: models.Price{Input: 2, Output: 10}}
	house = models.Model{Name: "house-small", Class: models.ClassLocal}
)

// person makes a data folder with one person, and returns their store.
func person(t *testing.T, name string) (*tenant.Install, *tenant.Store) {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	p, err := in.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return in, s
}

// turnOf writes a turn of a new chat sent under requestID, as a run's turn
// would be, and returns its id.
func turnOf(t *testing.T, s *tenant.Store, requestID string) string {
	t.Helper()
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES(?, ?, 'running', ?, 'person', '{}', ?)",
		id, c.ID, requestID, stamp()); err != nil {
		t.Fatal(err)
	}
	return id
}

func set(t *testing.T, s *tenant.Store, by tenantsettings.Role, key, value string) {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	if err := tenantsettings.Set(context.Background(), s, reg, by, key, value); err != nil {
		t.Fatal(err)
	}
}

func spend(t *testing.T, s *tenant.Store, requestID string, micros int64) {
	t.Helper()
	if _, err := usage.Add(context.Background(), s, usage.Record{RequestID: requestID, Kind: "chat", Model: paid.Name, Class: paid.Class,
		Outcome: usage.OutcomeOK, InputTokens: 10, OutputTokens: 10, CostMicros: micros}); err != nil {
		t.Fatal(err)
	}
}

// A run of any kind gets a budget by registering: a sub-agent, the stand-in
// for P8-02, is refused a call that could take it past its budget, with a
// message that names it, and a turn that is no run's is let through.
func TestARegisteredRunHasItsOwnBudget(t *testing.T) {
	ctx := context.Background()
	_, s := person(t, "Budget Person")

	n, err := Budget(ctx, s, KindSubagent)
	if err != nil || n != 250_000 {
		t.Fatalf("a sub-agent's budget as shipped is %d (%v), want 250000", n, err)
	}
	set(t, s, tenantsettings.Person, KeySubagentBudget, "0.01")
	if n, _ = Budget(ctx, s, KindSubagent); n != 10_000 {
		t.Fatalf("a sub-agent's budget after the person set a cent is %d", n)
	}
	for k, want := range map[Kind]int64{KindTask: 1_000_000, KindSchedule: 1_000_000} {
		if got, err := Budget(ctx, s, k); err != nil || got != want {
			t.Errorf("the %s budget as shipped is %d (%v), want %d", k, got, err, want)
		}
	}

	run := Run{Kind: KindSubagent, ID: "sub-1", RequestID: "req-subagent-1", Title: "Tide tables", BudgetMicros: 10_000}
	if err := Register(ctx, s.DB(), run); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ForRequest(ctx, s.DB(), run.RequestID)
	if err != nil || !ok || got != run {
		t.Fatalf("the run reads back as %+v, %v, %v", got, ok, err)
	}
	turnID := turnOf(t, s, run.RequestID)
	if got, ok, _ := ForTurn(ctx, s, turnID); !ok || got.ID != "sub-1" {
		t.Fatalf("the run's turn is found as %+v, %v", got, ok)
	}

	if e := Gate(ctx, s, turnID, paid, 6_000); e != nil {
		t.Fatalf("a call within the budget was refused: %v", e)
	}
	spend(t, s, run.RequestID, 6_000)
	e := Gate(ctx, s, turnID, paid, 6_000)
	if e == nil || e.Kind != seam.ErrSpendCap || !strings.Contains(e.Message, "sub-agent “Tide tables” has spent $0.01 of its budget of $0.01") {
		t.Fatalf("a call past the budget was answered %v", e)
	}
	if e := Gate(ctx, s, turnOf(t, s, "req-a-chat"), paid, 6_000); e != nil {
		t.Fatalf("a turn that is no run's was refused: %v", e)
	}

	for _, bad := range []Run{
		{Kind: "robot", RequestID: "r1"},
		{Kind: KindTask, RequestID: " "},
		{Kind: KindTask, RequestID: "r2", BudgetMicros: -1},
	} {
		if err := Register(ctx, s.DB(), bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("registering %+v answered %v", bad, err)
		}
	}
	for _, usd := range []float64{-1} {
		if _, err := Micros(usd); !errors.Is(err, ErrInvalid) {
			t.Errorf("a budget of %v answered %v", usd, err)
		}
	}
}

// At the cap or its budget, an unattended run goes on to the next model and
// keeps a notice, or stops when the person switched fallback off; a chat,
// and any other failure, are let through.
func TestMovedFallsBackOrStops(t *testing.T) {
	ctx := context.Background()
	_, s := person(t, "Moving Person")
	run := Run{Kind: KindTask, ID: "task-1", RequestID: "req-task-1", Title: "Harbour birds", BudgetMicros: 1_000}
	if err := Register(ctx, s.DB(), run); err != nil {
		t.Fatal(err)
	}
	turnID := turnOf(t, s, run.RequestID)
	capped := routing.Attempt{Model: paid, Refused: true, Error: &seam.Error{Kind: seam.ErrSpendCap, Message: "The call to paid-large was not sent. This month's spending is at the cap."}}

	if e := Moved(ctx, s, turnOf(t, s, "req-chat"), capped, house); e != nil {
		t.Fatalf("a chat at the cap was stopped: %v", e)
	}
	down := routing.Attempt{Model: paid, Error: &seam.Error{Kind: seam.ErrUnavailable, Message: "down"}}
	if e := Moved(ctx, s, turnID, down, house); e != nil {
		t.Fatalf("an endpoint that was down stopped the run: %v", e)
	}
	if got, _ := Notices(ctx, s, run.RequestID); len(got) != 0 {
		t.Fatalf("notices before any refusal: %+v", got)
	}

	// Fallback on, as shipped: the call goes on and a notice is kept. It is
	// told only once the model it went on to has written in the turn.
	if e := Moved(ctx, s, turnID, capped, house); e != nil {
		t.Fatalf("at the cap with fallback on, the run was stopped: %v", e)
	}
	if got, _ := Notices(ctx, s, run.RequestID); len(got) != 0 {
		t.Fatalf("a fallback whose model has not written yet is told: %+v", got)
	}
	m, err := chat.Append(s, chatOf(t, s, turnID), chat.NewMessage{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Gulls, terns and cormorants."}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec("INSERT INTO turn_messages(message_id, turn_id, model) VALUES(?, ?, ?)", m.ID, turnID, house.Name); err != nil {
		t.Fatal(err)
	}
	got, _ := Notices(ctx, s, run.RequestID)
	if len(got) != 1 || got[0].Kind != NoticeFallback || got[0].Model != paid.Name || got[0].FallbackModel != house.Name {
		t.Fatalf("the notices are %+v", got)
	}
	summary, _ := Summary(ctx, s, run.RequestID)
	if !strings.Contains(summary, "house-small") || !strings.Contains(summary, "at the cap") {
		t.Errorf("the summary is %q", summary)
	}

	// The run's chat is told once, under a note turn kept before it is written.
	var notes []string
	var turns []string
	write := func(noteTurn, text string) error {
		turns, notes = append(turns, noteTurn), append(notes, text)
		return nil
	}
	if err := Tell(ctx, s, run.RequestID, turnID, true, write); err != nil {
		t.Fatal(err)
	}
	if err := Tell(ctx, s, run.RequestID, turnID, true, write); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || turns[0] == "" || !strings.Contains(notes[0], "So house-small, a local model, wrote the reply in place of paid-large.") {
		t.Fatalf("the chat was told %q", notes)
	}

	// Fallback off: the call ends, and says why.
	set(t, s, tenantsettings.Person, KeyFallback, "false")
	e := Moved(ctx, s, turnID, capped, house)
	if e == nil || e.Kind != seam.ErrSpendCap || !strings.Contains(e.Message, "switched off in your settings, so the background task stopped") {
		t.Fatalf("at the cap with fallback off the run was answered %v", e)
	}
	got, _ = Notices(ctx, s, run.RequestID)
	if len(got) != 2 || got[1].Kind != NoticeStop {
		t.Fatalf("after the stop the notices are %+v", got)
	}
	notes = nil
	if err := Tell(ctx, s, run.RequestID, turnID, false, write); err != nil || len(notes) != 0 {
		t.Fatalf("a run told of its stop elsewhere was told here too: %q, %v", notes, err)
	}
}

func chatOf(t *testing.T, s *tenant.Store, turnID string) string {
	t.Helper()
	var id string
	if err := s.DB().QueryRow("SELECT chat_id FROM turns WHERE id = ?", turnID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// The switch's routes: pause waits for the work to stop and says how many
// are held, resume moves the schedules on before the switch goes off, and
// one person's switch is theirs alone.
func TestTheSwitchsRoutes(t *testing.T) {
	in, one := person(t, "Switch Person One")
	two, err := in.Create("Switch Person Two")
	if err != nil {
		t.Fatal(err)
	}
	o, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	const phrase = "an invented phrase for the switch"
	tokens := map[string]string{}
	for _, id := range []string{one.TenantID(), two.ID} {
		s, err := in.Store(id)
		if err != nil {
			t.Fatal(err)
		}
		if err := signin.SetPassword(s, phrase, o); err != nil {
			t.Fatal(err)
		}
		if tokens[id], _, err = signin.SignIn(s, phrase, o); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}

	// A stand-in for the task runner: two runs that stop a little after
	// they are told to look at the switch.
	var order []string
	working := map[string]int{one.TenantID(): 2, two.ID: 1}
	stopAt := time.Time{}
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	routes := Routes(Options{
		Config: cfg,
		Runs: func(_ context.Context, s *tenant.Store) (Count, error) {
			if s.TenantID() == one.TenantID() && !stopAt.IsZero() && time.Now().After(stopAt) {
				working[s.TenantID()] = 0
			}
			on, _ := Paused(context.Background(), s)
			n := Count{Working: working[s.TenantID()]}
			if on {
				n.Held = 2 - n.Working
			}
			return n, nil
		},
		Pause: func(_ context.Context, s *tenant.Store) error {
			order = append(order, "pause "+s.TenantID())
			stopAt = time.Now().Add(150 * time.Millisecond)
			return nil
		},
		Rearm: func(_ context.Context, s *tenant.Store) error {
			if on, _ := Paused(context.Background(), s); !on {
				t.Error("the schedules were moved on after the switch went off")
			}
			order = append(order, "rearm")
			return nil
		},
		Resume: func(_ context.Context, s *tenant.Store) error { order = append(order, "resume"); return nil },
	})
	h, err := api.Handler(api.Options{Install: in, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, routes)
	if err != nil {
		t.Fatal(err)
	}
	sv := httptest.NewServer(h)
	defer sv.Close()
	call := func(tenantID, method, path string) SwitchReply {
		t.Helper()
		req, _ := http.NewRequest(method, sv.URL+api.Prefix+path, nil)
		req.AddCookie(&http.Cookie{Name: api.CookieName, Value: tokens[tenantID]})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out SwitchReply
		if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil {
			t.Fatalf("%s %s answered %d", method, path, resp.StatusCode)
		}
		return out
	}

	if got := call(one.TenantID(), "GET", "agents"); got.Paused || got.Working != 2 {
		t.Fatalf("before the pause the switch reads %+v", got)
	}
	began := time.Now()
	got := call(one.TenantID(), "POST", "agents/pause")
	if !got.Paused || got.Working != 0 || got.Held != 2 || time.Since(began) < 100*time.Millisecond {
		t.Fatalf("the pause answered %+v after %v", got, time.Since(began))
	}
	if other := call(two.ID, "GET", "agents"); other.Paused || other.Working != 1 {
		t.Fatalf("one person's pause reached another: %+v", other)
	}
	got = call(one.TenantID(), "POST", "agents/resume")
	if got.Paused || strings.Join(order, ", ") != "pause "+one.TenantID()+", rearm, resume" {
		t.Fatalf("the resume answered %+v, in the order %v", got, order)
	}
}
