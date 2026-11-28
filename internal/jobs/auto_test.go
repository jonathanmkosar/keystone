package jobs

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Auto for unattended work (P14-06), on background tasks. The test tenant
// is given its recorded ladder (a local, a trusted and a hosted step, all
// played by the fake provider) and a decision model whose verdicts are
// written here: the lighthouse task is hard, so the hosted step; anything
// else, the carry-on round among them, is easy, so the local step.

// verdictScripts are recorded verdicts, as decision scripts of the fake.
const verdictScripts = `
	{"name": "the lighthouse task is hard", "when": {"decision": true, "user_text_contains": "the lighthouse walk"}, "steps": [
		{"decision": {"answers": [
			{"question": "step", "choice": "recorded-large", "probabilities": {"recorded-small": 0.03, "recorded-middle": 0.07, "recorded-large": 0.9}},
			{"question": "effort", "choice": "high", "probabilities": {"low": 0.05, "medium": 0.15, "high": 0.8}}],
			"usage": {"input_tokens": 230, "output_tokens": 2}}}]},
	{"name": "anything else is easy", "when": {"decision": true}, "steps": [
		{"decision": {"answers": [
			{"question": "step", "choice": "recorded-small", "probabilities": {"recorded-small": 0.9, "recorded-middle": 0.07, "recorded-large": 0.03}},
			{"question": "effort", "choice": "low", "probabilities": {"low": 0.85, "medium": 0.13, "high": 0.02}}],
			"usage": {"input_tokens": 220, "output_tokens": 2}}}]},`

// autoScripts are the tasks' scripts with the recorded verdicts beside them.
func autoScripts(t *testing.T) string {
	return strings.Replace(taskScripts(t), `"scripts": [`, `"scripts": [`+verdictScripts, 1)
}

// recordedLadder gives the test tenant its recorded ladder and decision
// model, as its admin.
func (g *rig) recordedLadder() {
	g.set(g.test.ID, tenantsettings.Admin, router.KeyLadder, turn.TestSmallModel+", "+turn.TestMiddleModel+", "+turn.TestLargeModel)
	g.set(g.test.ID, tenantsettings.Admin, router.KeyDecisionModel, turn.TestDecisionModel)
}

// verdictsOf returns the router's verdicts of a chat's turns, oldest first.
func (g *rig) verdictsOf(tenantID, chatID string) []turn.Verdict {
	g.t.Helper()
	var out []turn.Verdict
	g.with(tenantID, func(s *tenant.Store) {
		all, err := turn.Verdicts(context.Background(), s, turn.VerdictFilter{})
		if err != nil {
			g.t.Fatal(err)
		}
		for _, v := range all {
			var in string
			if err := s.DB().QueryRow("SELECT chat_id FROM turns WHERE id = ?", v.TurnID).Scan(&in); err != nil {
				g.t.Fatal(err)
			}
			if in == chatID {
				out = append(out, v)
			}
		}
	})
	return out
}

// The item's proof: a background task on Auto uses the router. Each round
// is routed afresh, so a hard first round on the hosted step is followed by
// an easy carry-on round on the local step; each routing is a verdict under
// the task's own request id, and each decision call is recorded with its
// usage record.
func TestABackgroundTaskOnAutoIsRoutedEachRound(t *testing.T) {
	g := newRig(t)
	g.maxSteps = 1 // so the task takes two rounds
	g.recordedLadder()
	sv := g.serve(scripted(t, autoScripts(t)))
	parent := sv.newChat(g.test.ID)

	started := sv.start(g.test.ID, parent, lighthouseTask, "Lighthouse notes")
	_, end := sv.follow(g.test.ID, started)
	if end.Status != StatusDone || end.Rounds != 2 || end.Result != lighthouseDone {
		t.Fatalf("the task on Auto ended as %+v", end)
	}
	j := g.task(g.test.ID, started.ID)
	if j.model != "" {
		t.Errorf("a task on Auto names the model %q", j.model)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		if on, err := turn.ChatAuto(context.Background(), s, started.WorkChatID); err != nil || !on {
			t.Errorf("the task's chat is not on Auto (%v)", err)
		}
	})

	verdicts := g.verdictsOf(g.test.ID, started.WorkChatID)
	if len(verdicts) != 2 {
		t.Fatalf("the task's rounds have %d verdicts, want 2: %+v", len(verdicts), verdicts)
	}
	for i, want := range []struct{ step, effort string }{{turn.TestLargeModel, "high"}, {turn.TestSmallModel, "low"}} {
		v := verdicts[i]
		if v.RequestID != j.requestID || v.DecisionModel != turn.TestDecisionModel || v.Step != want.step || v.Effort != want.effort || v.RulesOnly {
			t.Errorf("round %d's verdict is %+v, want %s at %s under %s", i+1, v, want.step, want.effort, j.requestID)
		}
	}

	// Every chat call of a round went to its step, with its effort; the two
	// decision calls are recorded under the task's request id and actor.
	g.with(g.test.ID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{Kind: audit.KindModel, RequestID: j.requestID})
		if err != nil {
			t.Fatal(err)
		}
		calls := map[string]int{}
		for _, e := range entries {
			if e.Outcome == audit.OutcomeStarted {
				calls[e.Action]++
				if e.Actor != j.actor {
					t.Errorf("a call of the task was recorded as %q, want %q", e.Actor, j.actor)
				}
			}
		}
		if calls[turn.TestLargeModel] == 0 || calls[turn.TestSmallModel] == 0 || calls[turn.TestDecisionModel] != 2 || calls[turn.TestMiddleModel] != 0 {
			t.Errorf("the task's model calls were %v", calls)
		}
		recs, err := usage.List(context.Background(), s, usage.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		decisions := 0
		for _, r := range recs {
			if r.RequestID == j.requestID && r.Kind == "decision" && r.Model == turn.TestDecisionModel {
				decisions++
			}
		}
		if decisions != 2 {
			t.Errorf("%d of the task's decision calls have a usage record, want 2", decisions)
		}
	})

	// Each round's reply carries its route, as a person's message on Auto
	// does.
	routed := 0
	for _, m := range sv.messages(g.test.ID, started.WorkChatID) {
		if m.Role == seam.RoleAssistant && m.Route != nil {
			routed++
		}
	}
	if routed < 2 {
		t.Errorf("%d of the task's replies carry a route, want at least 2", routed)
	}
}

// A run whose caller names a model is not on Auto: its chat stays off, its
// rounds name the model, and the router keeps no verdict for it. With no
// ladder at all, a task names the model it did before Auto.
func TestANamedModelOrNoLadderKeepsATaskOffAuto(t *testing.T) {
	g := newRig(t)
	g.recordedLadder()
	sv := g.serve(scripted(t, autoScripts(t)))

	var named Task
	var runChat string
	g.with(g.test.ID, func(s *tenant.Store) {
		made, err := chat.Create(s)
		if err != nil {
			t.Fatal(err)
		}
		runChat = made.ID
		named, err = sv.jobs.StartIn(context.Background(), s, runChat, "req-a-named-run", "schedule",
			StartBody{Text: lighthouseTask, Title: "Named", Kind: unattended.KindSchedule}, "the-test-model")
		if err != nil {
			t.Fatal(err)
		}
	})
	if _, end := sv.follow(g.test.ID, named); end.Status != StatusDone {
		t.Fatalf("the run with a named model ended as %+v", end)
	}
	if j := g.task(g.test.ID, named.ID); j.model != "the-test-model" {
		t.Errorf("the run names %q", j.model)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		if on, err := turn.ChatAuto(context.Background(), s, runChat); err != nil || on {
			t.Errorf("a run with a named model is on Auto (%v)", err)
		}
	})
	if vs := g.verdictsOf(g.test.ID, runChat); len(vs) != 0 {
		t.Errorf("a run with a named model was routed: %+v", vs)
	}

	// No ladder: no verdict, and the chat off Auto.
	g.set(g.test.ID, tenantsettings.Admin, router.KeyLadder, "")
	parent := sv.newChat(g.test.ID)
	plain := sv.start(g.test.ID, parent, lighthouseTask, "No ladder")
	if _, end := sv.follow(g.test.ID, plain); end.Status != StatusDone {
		t.Fatalf("the task with no ladder ended as %+v", end)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		if on, err := turn.ChatAuto(context.Background(), s, plain.WorkChatID); err != nil || on {
			t.Errorf("a task with no ladder is on Auto (%v)", err)
		}
	})
	if vs := g.verdictsOf(g.test.ID, plain.WorkChatID); len(vs) != 0 {
		t.Errorf("a task with no ladder was routed: %+v", vs)
	}
}

// The cap on Auto, as decision 0392 says: the step the router chose is the
// ask of the same gated route, so at the monthly cap a hosted step is not
// called, and the task goes on with the local model in the person's chain
// with the notice, or stops when fallback is off.
func TestATaskOnAutoAtTheCapFallsBackOrStops(t *testing.T) {
	g := newRig(t)
	var paid, house atomic.Int32
	g.extra = adapters.Set{
		models.AdapterAnthropic:       func(models.Model, string) (seam.Provider, error) { return answers{"From the paid model.", &paid}, nil },
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return answers{"From the house.", &house}, nil },
	}
	g.addModels(paidModel, houseModel)
	g.set(g.ordinary.ID, tenantsettings.Admin, router.KeyLadder, paidModel.Name)
	g.set(g.ordinary.ID, tenantsettings.Person, "routing.fallback_models", houseModel.Name)
	g.set(g.ordinary.ID, tenantsettings.Admin, "usage.monthly_cap_usd", "0")
	sv := g.serve(nil)
	parent := sv.newChat(g.ordinary.ID)

	started := sv.start(g.ordinary.ID, parent, "Name three harbour birds.", "Harbour birds")
	_, end := sv.follow(g.ordinary.ID, started)
	if end.Status != StatusDone || end.Result != "From the house." || paid.Load() != 0 || house.Load() != 1 {
		t.Fatalf("at the cap the task on Auto ended as %+v, after %d paid and %d local calls", end, paid.Load(), house.Load())
	}
	verdicts := g.verdictsOf(g.ordinary.ID, started.WorkChatID)
	if len(verdicts) != 1 || verdicts[0].Step != paidModel.Name || verdicts[0].RulesOnlyWhy != "one_candidate" {
		t.Fatalf("the task's verdicts are %+v", verdicts)
	}
	work := sv.messages(g.ordinary.ID, started.WorkChatID)
	if said := text(work[len(work)-1]); !strings.Contains(said, "monthly cap") || !strings.Contains(said, "So house-small, a local model, wrote the reply in place of paid-large.") {
		t.Errorf("the task's chat was told %q", said)
	}
	j := g.task(g.ordinary.ID, started.ID)
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		notices, err := unattended.Notices(context.Background(), s, j.requestID)
		if err != nil || len(notices) != 1 || notices[0].FallbackModel != houseModel.Name {
			t.Errorf("the task's notices are %+v (%v)", notices, err)
		}
	})

	// Fallback off: it stops, and the local model is not called.
	g.set(g.ordinary.ID, tenantsettings.Person, unattended.KeyFallback, "false")
	started = sv.start(g.ordinary.ID, parent, "Name three harbour birds.", "Harbour birds again")
	_, end = sv.follow(g.ordinary.ID, started)
	if end.Status != StatusFailed || end.Error == nil || end.Error.Code != string(seam.ErrSpendCap) || house.Load() != 1 || paid.Load() != 0 {
		t.Fatalf("with fallback off the task on Auto ended as %+v, after %d local calls", end, house.Load())
	}
	if vs := g.verdictsOf(g.ordinary.ID, started.WorkChatID); len(vs) != 1 {
		t.Errorf("the stopped task's verdicts are %+v", vs)
	}
}
