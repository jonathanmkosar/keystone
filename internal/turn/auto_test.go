package turn

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/builtin"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// The messages of the test tenant's recorded verdicts
// (fixtures/test-tenant/turns/router.json).
const (
	autoEasy   = "Which day is market day?"
	autoPlan   = "Plan a two-day walk along the coast."
	autoHard   = "Prove that the tide tables repeat."
	autoUnsure = "Compare the three ferry companies."
	autoHeld   = "A held question for the large step."
)

// serveAuto starts a server as serveTools does, with the built-in tools,
// and with Auto's route and the inspector's beside the chat routes.
func (g *rig) serveAuto() *served {
	g.t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 50 * time.Millisecond
	provider, err := testtenant.Provider(g.fx, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	reg := tools.New()
	if err := builtin.Add(reg, builtin.Options{Limits: cfg.Limits, ReadChatMessages: cfg.ToolLoop.ReadChatMessages}); err != nil {
		g.t.Fatal(err)
	}
	set, err := adapters.Fake(g.fx.Scripts, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	for name, build := range g.extra {
		set[name] = build
	}
	runner := NewRunner(Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Adapters: set, Tools: reg})
	runner.Recover()
	routes := append(append(append(Routes(runner), ApprovalRoutes(runner)...), AutoRoutes(runner)...), InspectorRoutes(runner)...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, routes)
	if err != nil {
		g.t.Fatal(err)
	}
	sv := &served{g: g, runner: runner, server: httptest.NewServer(h), tokens: map[string]string{}}
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

// setFor stores a setting for a tenant as the admin of their install.
func (g *rig) adminSet(tenantID, key, value string) {
	g.t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		g.t.Fatal(err)
	}
	g.with(tenantID, func(s *tenant.Store) {
		if err := tenantsettings.Set(context.Background(), s, reg, tenantsettings.Admin, key, value); err != nil {
			g.t.Fatal(err)
		}
	})
}

// recordedLadder gives the test tenant its recorded ladder and verdicts.
func (g *rig) recordedLadder() {
	g.adminSet(g.test.ID, router.KeyLadder, TestSmallModel+", "+TestMiddleModel+", "+TestLargeModel)
	g.adminSet(g.test.ID, router.KeyDecisionModel, TestDecisionModel)
}

// routes reads the route events of a stream.
func routesOf(t *testing.T, events []event) []TurnRoute {
	t.Helper()
	var out []TurnRoute
	for _, e := range events {
		if e.name == EventRoute {
			var r TurnRoute
			e.into(t, &r)
			out = append(out, r)
		}
	}
	return out
}

// say sends one message as text, with the fields of extra, and returns
// the stream, the turn's last route and how it ended.
func (sv *served) say(tenantID, chatID, text string, extra func(*SendBody)) ([]event, TurnRoute, Turn) {
	sv.g.t.Helper()
	b := SendBody{Text: text}
	if extra != nil {
		extra(&b)
	}
	body, _ := json.Marshal(b)
	events, _, _ := sv.send(tenantID, chatID, string(body))
	var route TurnRoute
	if rs := routesOf(sv.g.t, events); len(rs) > 0 {
		route = rs[len(rs)-1]
	}
	return events, route, last(sv.g.t, events)
}

func (sv *served) modelChoice(tenantID, chatID string) ModelChoice {
	sv.g.t.Helper()
	var c ModelChoice
	sv.call(tenantID, "GET", "chats/"+chatID+"/model", "", http.StatusOK, &c)
	return c
}

func (g *rig) modelCalls(tenantID, requestID string) []audit.Entry {
	g.t.Helper()
	var got []audit.Entry
	g.with(tenantID, func(s *tenant.Store) {
		var err error
		if got, err = audit.List(context.Background(), s, audit.Filter{Kind: audit.KindModel, RequestID: requestID}); err != nil {
			g.t.Fatal(err)
		}
	})
	return got
}

// The item's proof: the test tenant's recorded turns reach every step of
// its ladder, each message routed afresh, so a hard message followed by an
// easy one goes back down.
func TestAutoReachesEveryStepAndGoesBackDown(t *testing.T) {
	g := newRig(t)
	g.recordedLadder()
	sv := g.serveAuto()
	id := sv.newChat(g.test.ID)

	c := sv.modelChoice(g.test.ID, id)
	if !c.Auto || len(c.Ladder) != 3 || c.Ladder[0].Name != TestSmallModel || c.Ladder[2].Class != "hosted" || !c.MaySwitchOff || c.Held {
		t.Fatalf("a new chat's model choice is %+v", c)
	}

	type want struct {
		text, model string
		step        int
		effort      string
		reason      string
	}
	for i, w := range []want{
		{autoEasy, TestSmallModel, 1, "low", "Recorded turns, small (test data), effort low, decision 0.92"},
		{autoHard, TestLargeModel, 3, "high", "Recorded turns, large (test data), effort high, decision 0.88; switched from Recorded turns, small (test data)"},
		{autoEasy + " Again, please.", TestSmallModel, 1, "low", "Recorded turns, small (test data), effort low, decision 0.92; switched from Recorded turns, large (test data)"},
		{autoPlan, TestMiddleModel, 2, "medium", "Recorded turns, middle (test data), effort medium, decision 0.81; switched from Recorded turns, small (test data)"},
		{autoUnsure, TestLargeModel, 3, "medium", "Moved up: decision unsure (0.45); switched from Recorded turns, middle (test data)"},
	} {
		events, route, end := sv.say(g.test.ID, id, w.text, nil)
		if end.Status != StatusDone || end.Model != w.model {
			t.Fatalf("message %d ended as %+v", i+1, end)
		}
		if !route.Auto || route.Model != w.model || route.Step != w.step || route.Steps != 3 || route.Effort != w.effort || route.Reason != w.reason || route.VerdictID == "" {
			t.Errorf("message %d was routed %+v, want %+v", i+1, route, w)
		}
		if end.Route == nil || end.Route.Step != w.step || end.Route.VerdictID != route.VerdictID {
			t.Errorf("message %d's turn keeps the route %+v", i+1, end.Route)
		}
		if got := names(events); !strings.HasPrefix(got, "turn route turn message start") {
			t.Errorf("message %d's stream was %s", i+1, got)
		}
	}

	var verdicts []Verdict
	g.with(g.test.ID, func(s *tenant.Store) {
		var err error
		if verdicts, err = Verdicts(context.Background(), s, VerdictFilter{}); err != nil {
			t.Fatal(err)
		}
		recs, err := usage.List(context.Background(), s, usage.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		decisions := 0
		for _, r := range recs {
			if r.Kind == "decision" && r.Model == TestDecisionModel {
				decisions++
			}
		}
		if decisions != 5 {
			t.Errorf("%d decision calls have a usage record, want 5", decisions)
		}
	})
	if len(verdicts) != 5 {
		t.Fatalf("%d verdicts were kept, want 5", len(verdicts))
	}
	first, unsure := verdicts[0], verdicts[4]
	if first.DecisionModel != TestDecisionModel || first.StepAnswered != TestSmallModel || first.Probability != 0.92 || first.Shown.Turns != 0 || first.RulesOnly || first.Shown.Tools == 0 {
		t.Errorf("the first verdict is %+v", first)
	}
	if !unsure.MovedUp || unsure.StepAnswered != TestMiddleModel || unsure.Step != TestLargeModel || unsure.Shown.Turns != 4 || unsure.Shown.PreviousStep != TestMiddleModel {
		t.Errorf("the unsure verdict is %+v", unsure)
	}

	// Each reply carries its route for the footnote, and the choice names
	// the last step.
	var open ChatReply
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &open)
	replies := 0
	for _, m := range open.Messages {
		if m.Role == seam.RoleAssistant {
			replies++
			if m.Route == nil || m.Route.Model != m.Model {
				t.Errorf("reply %d carries the route %+v", m.ID, m.Route)
			}
		}
	}
	if replies != 5 {
		t.Errorf("the chat has %d replies, want 5", replies)
	}
	if c := sv.modelChoice(g.test.ID, id); c.LastStep != TestLargeModel || !c.Auto {
		t.Errorf("after the turns the choice is %+v", c)
	}
}

// The item's proof: every tool step of one turn stays on its model, with
// the effort the router chose on every call.
func TestAutoToolStepsStayOnOneModel(t *testing.T) {
	g := newRig(t)
	g.recordedLadder()
	sv := g.serveAuto()
	id := sv.newChat(g.test.ID)

	body, _ := json.Marshal(SendBody{Text: severalSteps})
	events, _, requestID := sv.send(g.test.ID, id, string(body))
	route := routesOf(t, events)
	if len(route) != 1 || route[0].Model != TestMiddleModel || route[0].Effort != "medium" {
		t.Fatalf("the turn was routed %+v", route)
	}
	starts := 0
	for _, e := range events {
		switch e.name {
		case EventStart:
			var s StartEvent
			e.into(t, &s)
			starts++
			if s.Model != TestMiddleModel {
				t.Errorf("step %d started on %s", s.Step, s.Model)
			}
		case EventUsage:
			var u UsageEvent
			e.into(t, &u)
			if u.Model != TestMiddleModel {
				t.Errorf("step %d was answered by %s", u.Step, u.Model)
			}
		}
	}
	if starts != 3 {
		t.Fatalf("the turn made %d calls, want 3: %s", starts, names(events))
	}
	chatCalls := 0
	for _, e := range g.modelCalls(g.test.ID, requestID) {
		if e.Outcome != audit.OutcomeStarted || e.Action == TestDecisionModel {
			continue
		}
		chatCalls++
		var sent seam.Request
		if err := json.Unmarshal([]byte(e.Sent), &sent); err != nil {
			t.Fatal(err)
		}
		if e.Action != TestMiddleModel || sent.Model != TestMiddleModel || sent.Effort != seam.EffortMedium {
			t.Errorf("a call of the turn went to %s (%s) with the effort %q", e.Action, sent.Model, sent.Effort)
		}
	}
	if chatCalls != 3 {
		t.Errorf("the audit log holds %d chat calls of the turn, want 3", chatCalls)
	}
}

// The item's proof: a held chat on Auto, given a verdict that names the
// hosted step, reaches no hosted model.
func TestAHeldChatOnAutoReachesNoHostedModel(t *testing.T) {
	g := newRig(t)
	g.recordedLadder()
	sv := g.serveAuto()
	id := sv.newChat(g.test.ID)

	_, _, first := sv.say(g.test.ID, id, autoEasy, nil)
	g.with(g.test.ID, func(s *tenant.Store) {
		if err := recall.Hold(context.Background(), s, first.ID, id); err != nil {
			t.Fatal(err)
		}
	})
	if c := sv.modelChoice(g.test.ID, id); !c.Held {
		t.Fatalf("a chat that was sent sensitive content is not held: %+v", c)
	}
	events, route, end := sv.say(g.test.ID, id, autoHeld, nil)
	if end.Status != StatusDone || end.Model == TestLargeModel || route.Model == TestLargeModel {
		t.Fatalf("the held chat's turn ended as %+v, routed %+v", end, route)
	}
	for _, e := range events {
		if e.name == EventStart || e.name == EventUsage {
			if strings.Contains(e.data, TestLargeModel) {
				t.Errorf("the held chat's stream names the hosted step: %s %s", e.name, e.data)
			}
		}
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{Kind: audit.KindModel})
		if err != nil {
			t.Fatal(err)
		}
		asked := false
		for _, e := range entries {
			if e.Action == TestLargeModel {
				t.Errorf("the hosted step was called: %+v", e)
			}
			if e.Action == TestDecisionModel && strings.Contains(e.Sent, "held question") {
				asked = true
				if strings.Contains(e.Sent, `"`+TestLargeModel+`"`) {
					t.Errorf("the held chat's verdict was offered the hosted step: %s", e.Sent)
				}
			}
		}
		if !asked {
			t.Error("the held chat's verdict was not asked, so the check proves nothing")
		}
		vs, err := Verdicts(context.Background(), s, VerdictFilter{TurnID: end.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(vs) != 1 || vs[0].Step == TestLargeModel || !vs[0].RulesOnly {
			t.Errorf("the held chat's verdict is %+v", vs)
		}
	})
}

// The local machine is down for one invented person: a hosted step
// answers with the notice, and a held chat waits with local_unavailable.
var (
	autoLocal = models.Model{
		Name: "auto-local", Label: "House chat", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
		ProviderModel: "small-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal,
	}
	autoHosted = models.Model{
		Name: "auto-hosted", Label: "Cloud chat", Adapter: models.AdapterAnthropic, Endpoint: "https://api.provider-one.example",
		ProviderModel: "example-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassHosted,
	}
)

// The item's proof: with the local machine not answering, the reply
// carries the notice, and a held chat fails with local_unavailable.
func TestAutoWithTheLocalMachineNotAnswering(t *testing.T) {
	g := newRig(t)
	var localCalls, hostedCalls atomic.Int32
	g.extra = adapters.Set{
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return down{&localCalls}, nil },
		models.AdapterAnthropic: func(models.Model, string) (seam.Provider, error) {
			return counted{answers{chat: "A reply from the hosted step."}, &hostedCalls}, nil
		},
	}
	g.addModels(autoLocal, autoHosted)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	g.adminSet(g.ordinary.ID, router.KeyLadder, "auto-local, auto-hosted")
	sv := g.serveAuto()
	id := sv.newChat(g.ordinary.ID)

	// The local step is chosen, does not answer, and the message is
	// routed again to the hosted step, with the notice.
	events, route, end := sv.say(g.ordinary.ID, id, "Hello there.", nil)
	rs := routesOf(t, events)
	if end.Status != StatusDone || len(rs) != 2 || rs[0].Model != autoLocal.Name || route.Model != autoHosted.Name || route.Notice != NoticeLocalUnavailable {
		t.Fatalf("the turn ended as %+v, routed %+v: %s", end, rs, names(events))
	}
	if end.Route == nil || end.Route.Notice != NoticeLocalUnavailable {
		t.Errorf("the turn keeps the route %+v", end.Route)
	}
	var open ChatReply
	sv.call(g.ordinary.ID, "GET", "chats/"+id, "", http.StatusOK, &open)
	reply := open.Messages[len(open.Messages)-1]
	if reply.Route == nil || reply.Route.Notice != NoticeLocalUnavailable || reply.Model != autoHosted.Name {
		t.Errorf("the reply is %+v with the route %+v", reply, reply.Route)
	}
	if localCalls.Load() != 1 || hostedCalls.Load() != 1 {
		t.Errorf("the local step was called %d times and the hosted %d, want once each", localCalls.Load(), hostedCalls.Load())
	}

	// The next message knows the machine is down and asks it nothing.
	_, route, end = sv.say(g.ordinary.ID, id, "And again.", nil)
	if end.Status != StatusDone || route.Model != autoHosted.Name || route.Notice != NoticeLocalUnavailable || localCalls.Load() != 1 {
		t.Errorf("the second message was routed %+v after %d local calls", route, localCalls.Load())
	}

	// A held chat, on a server that has not yet seen the machine down: its
	// one step is local, does not answer, and the turn waits for it.
	held := sv.newChat(g.ordinary.ID)
	_, _, first := sv.say(g.ordinary.ID, held, "Start of a private chat.", nil)
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if err := recall.Hold(context.Background(), s, first.ID, held); err != nil {
			t.Fatal(err)
		}
	})
	sv.stop()
	sv = g.serveAuto()
	before := hostedCalls.Load()
	_, _, end = sv.say(g.ordinary.ID, held, "A private question.", nil)
	if end.Status != StatusFailed || end.Error == nil || end.Error.Code != CodeLocalUnavailable || !end.Error.Retryable {
		t.Errorf("the held chat's turn ended as %+v", end)
	}
	if hostedCalls.Load() != before {
		t.Errorf("the hosted step was called %d times in a held chat", hostedCalls.Load()-before)
	}
}

// Try a stronger model: the turn is written again on a new branch one step
// up, the verdict is marked, and the top step and a turn not on Auto are
// refused.
func TestTryAStrongerModel(t *testing.T) {
	g := newRig(t)
	g.recordedLadder()
	sv := g.serveAuto()
	id := sv.newChat(g.test.ID)

	_, _, small := sv.say(g.test.ID, id, autoEasy, nil)
	stronger := func(turnID string) ([]event, Turn) {
		t.Helper()
		resp := sv.request(g.test.ID, "POST", "chats/"+id+"/turns/"+turnID+"/stronger", "", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("stronger answered %d: %s", resp.StatusCode, raw)
		}
		events := readEvents(t, resp.Body, nil)
		return events, last(t, events)
	}
	events, middle := stronger(small.ID)
	rs := routesOf(t, events)
	if middle.Status != StatusDone || len(rs) != 1 || rs[0].Model != TestMiddleModel || rs[0].StrongerOf != small.ID ||
		!strings.HasPrefix(rs[0].Reason, "Stronger model asked for") || rs[0].Effort != "low" {
		t.Fatalf("the stronger turn ended as %+v, routed %+v", middle, rs)
	}
	var open ChatReply
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &open)
	reply := open.Messages[len(open.Messages)-1]
	if reply.Model != TestMiddleModel || len(reply.Siblings) != 2 || reply.ParentID != small.UserMessageID {
		t.Errorf("the stronger reply is %+v", reply)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		vs, err := Verdicts(context.Background(), s, VerdictFilter{TurnID: small.ID})
		if err != nil || len(vs) != 1 || !vs[0].StrongerAsked {
			t.Errorf("the first turn's verdict is %+v, %v", vs, err)
		}
	})
	_, large := stronger(middle.ID)
	if large.Model != TestLargeModel {
		t.Errorf("a stronger model than the middle step is %s", large.Model)
	}
	if code, body := sv.status(g.test.ID, "POST", "chats/"+id+"/turns/"+large.ID+"/stronger"); code != http.StatusConflict || !strings.Contains(body, CodeNoStronger) {
		t.Errorf("a stronger model than the top step answered %d: %s", code, body)
	}

	// A turn on the model picked is not on Auto.
	_, route, plain := sv.say(g.test.ID, id, "Thank you.", func(b *SendBody) { b.Model = TestModelName })
	if route.Auto || plain.Route != nil {
		t.Errorf("a turn on the model picked was routed %+v", plain.Route)
	}
	if code, body := sv.status(g.test.ID, "POST", "chats/"+id+"/turns/"+plain.ID+"/stronger"); code != http.StatusConflict || !strings.Contains(body, CodeNotAuto) {
		t.Errorf("a stronger model for a turn not on Auto answered %d: %s", code, body)
	}
}

// A named model takes a chat off Auto, and auto puts it back; a chat made
// with router.auto_default off starts off Auto; the inspector shows the
// verdict.
func TestTheChatsPickAndAuto(t *testing.T) {
	g := newRig(t)
	g.recordedLadder()
	sv := g.serveAuto()
	id := sv.newChat(g.test.ID)

	events, _, end := sv.say(g.test.ID, id, autoEasy, func(b *SendBody) { b.Model = TestModelName })
	if len(routesOf(t, events)) != 0 || end.Model != TestModelName {
		t.Errorf("a turn with a model named was routed: %s, %+v", names(events), end)
	}
	if c := sv.modelChoice(g.test.ID, id); c.Auto {
		t.Errorf("a chat with a model named is still on Auto: %+v", c)
	}
	_, route, end := sv.say(g.test.ID, id, autoHard, func(b *SendBody) { b.Auto = true })
	if !route.Auto || end.Model != TestLargeModel {
		t.Errorf("a turn sent with auto ended as %+v", end)
	}
	if c := sv.modelChoice(g.test.ID, id); !c.Auto {
		t.Errorf("a chat sent auto is not on Auto: %+v", c)
	}
	both, _ := json.Marshal(SendBody{Text: autoEasy, Auto: true, Model: TestModelName})
	if code, _ := sv.statusBody(g.test.ID, "POST", "chats/"+id+"/turns", string(both)); code != http.StatusBadRequest {
		t.Errorf("a message with auto and a model answered %d", code)
	}

	in := sv.inspectTurn(g.test.ID, id, end.ID)
	if in.Verdict == nil || in.Verdict.Step != TestLargeModel || in.Verdict.Reason == "" || in.Verdict.ID != end.Route.VerdictID {
		t.Errorf("the inspector shows the verdict %+v", in.Verdict)
	}

	g.adminSet(g.test.ID, KeyAutoDefault, "false")
	if c := sv.modelChoice(g.test.ID, sv.newChat(g.test.ID)); c.Auto || len(c.Ladder) != 3 {
		t.Errorf("a chat made with router.auto_default off has the choice %+v", c)
	}
	g.adminSet(g.test.ID, router.KeyLadder, "")
	if c := sv.modelChoice(g.test.ID, id); c.Auto || len(c.Ladder) != 0 {
		t.Errorf("with no ladder the choice is %+v", c)
	}
}
