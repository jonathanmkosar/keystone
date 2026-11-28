package subagents

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// Auto for unattended work (P14-06), on sub-agents. The test tenant is
// given its recorded ladder (recorded-small local, recorded-middle trusted,
// recorded-large hosted, all played by the director) and a decision model
// whose every verdict names the hosted step, sure of it.

// deciding is the director with recorded verdicts: it answers decision
// calls as well as chat calls.
type deciding struct {
	*director
	verdicts *fake.Provider
}

func (p deciding) Decide(ctx context.Context, req seam.DecisionRequest) (seam.DecisionResult, error) {
	return p.verdicts.Decide(ctx, req)
}

// hostedVerdicts answer every decision call with the hosted step.
func hostedVerdicts(t *testing.T) *fake.Provider {
	t.Helper()
	scripts, err := fake.Decode([]byte(`{"test_data": true, "scripts": [
	{"name": "every verdict names the hosted step", "when": {"decision": true}, "steps": [
		{"decision": {"answers": [
			{"question": "step", "choice": "recorded-large", "probabilities": {"recorded-small": 0.02, "recorded-middle": 0.03, "recorded-large": 0.95}},
			{"question": "effort", "choice": "high", "probabilities": {"low": 0.02, "medium": 0.08, "high": 0.9}}],
			"usage": {"input_tokens": 240, "output_tokens": 2}}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := fake.New(scripts, fake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// calls returns the model calls recorded under a request id, by model, and
// the requests sent to the decision model.
func (g *rig) calls(tenantID, requestID string) (map[string]int, []string) {
	g.t.Helper()
	got := map[string]int{}
	var asked []string
	g.with(tenantID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{Kind: audit.KindModel, RequestID: requestID})
		if err != nil {
			g.t.Fatal(err)
		}
		for _, e := range entries {
			if e.Outcome == audit.OutcomeStarted {
				got[e.Action]++
				if e.Action == turn.TestDecisionModel {
					asked = append(asked, e.Sent)
				}
			}
		}
	})
	return got, asked
}

// requestOf is a sub-agent's own request id, which its turn runs under.
func (g *rig) requestOf(tenantID, id string) string {
	g.t.Helper()
	var rid string
	g.with(tenantID, func(s *tenant.Store) {
		if err := s.DB().QueryRow("SELECT request_id FROM subagents WHERE id = ?", id).Scan(&rid); err != nil {
			g.t.Fatal(err)
		}
	})
	return rid
}

// The item's proof: a held parent's sub-agent, on Auto, given a verdict
// that names the hosted step, reaches no hosted model. The same sub-agent
// of a chat that is not held reaches the hosted step, so the check would
// notice one that refused everything.
func TestAHeldParentsSubagentOnAutoReachesNoHostedModel(t *testing.T) {
	g := newRig(t)
	g.set(g.test.ID, tenantsettings.Admin, router.KeyLadder, turn.TestSmallModel+", "+turn.TestMiddleModel+", "+turn.TestLargeModel)
	g.set(g.test.ID, tenantsettings.Admin, router.KeyDecisionModel, turn.TestDecisionModel)
	sc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	model := &director{brief: sc.Brief, done: "Back.", asks: map[string]toolCall{
		"Count the boats.": {ToolName, agentsInput("Count the boats in the harbour.")},
	}, sub: func(_ context.Context, _ string, req seam.Request, emit func(seam.Event) error) error {
		return say(emit, req.Model, "Fourteen boats.")
	}}
	sv := g.serve(deciding{director: model, verdicts: hostedVerdicts(t)})
	ctx := context.Background()

	// A chat that is not held: its sub-agent is on Auto and follows the
	// verdict to the hosted step.
	open := sv.newChat(g.test.ID)
	sv.send(g.test.ID, open, "Count the boats.", 20*time.Second)
	subs := sv.subagents(g.test.ID, open)
	if len(subs) != 1 || subs[0].Status != StatusDone || subs[0].Model != "" {
		t.Fatalf("the open chat's sub-agents read %+v", subs)
	}
	a := subs[0]
	calls, _ := g.calls(g.test.ID, g.requestOf(g.test.ID, a.ID))
	if calls[turn.TestLargeModel] == 0 || calls[turn.TestDecisionModel] != 1 {
		t.Fatalf("the open chat's sub-agent made the calls %v, want the hosted step", calls)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		if on, err := turn.ChatAuto(ctx, s, a.WorkChatID); err != nil || !on {
			t.Errorf("a sub-agent with no model named is not on Auto (%v)", err)
		}
		vs, err := turn.Verdicts(ctx, s, turn.VerdictFilter{TurnID: a.WorkTurnID})
		if err != nil || len(vs) != 1 || vs[0].Step != turn.TestLargeModel || vs[0].RequestID != g.requestOf(g.test.ID, a.ID) {
			t.Errorf("the open chat's sub-agent's verdicts are %+v (%v)", vs, err)
		}
	})

	// A held chat: its sub-agent is held from its first call (decision
	// 0483), and the router gives it no hosted step, whatever the verdict.
	held := sv.newChat(g.test.ID)
	sv.send(g.test.ID, held, "Say hello.", 20*time.Second) // a first turn to hold the chat under
	g.with(g.test.ID, func(s *tenant.Store) {
		var turnID string
		if err := s.DB().QueryRow("SELECT id FROM turns WHERE chat_id = ? ORDER BY seq LIMIT 1", held).Scan(&turnID); err != nil {
			t.Fatal(err)
		}
		if err := recall.Hold(ctx, s, turnID, held); err != nil {
			t.Fatal(err)
		}
	})
	sv.send(g.test.ID, held, "Count the boats.", 20*time.Second)
	subs = sv.subagents(g.test.ID, held)
	if len(subs) != 1 || subs[0].Status != StatusDone || !subs[0].Sensitive {
		t.Fatalf("the held chat's sub-agents read %+v", subs)
	}
	a = subs[0]
	rid := g.requestOf(g.test.ID, a.ID)
	calls, asked := g.calls(g.test.ID, rid)
	if calls[turn.TestLargeModel] != 0 {
		t.Errorf("the held chat's sub-agent reached the hosted step: %v", calls)
	}
	if calls[turn.TestSmallModel]+calls[turn.TestMiddleModel] == 0 {
		t.Errorf("the held chat's sub-agent made the calls %v, want a step that may read it", calls)
	}
	if len(asked) != 1 {
		t.Fatalf("the held chat's sub-agent's verdict was asked %d times, so the check proves nothing", len(asked))
	}
	if strings.Contains(asked[0], `"`+turn.TestLargeModel+`"`) {
		t.Errorf("the held chat's sub-agent's verdict was offered the hosted step: %s", asked[0])
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		if on, err := recall.Held(ctx, s, a.WorkChatID); err != nil || !on {
			t.Errorf("the held chat's sub-agent's chat is not held (%v)", err)
		}
		if on, err := turn.ChatAuto(ctx, s, a.WorkChatID); err != nil || !on {
			t.Errorf("the held chat's sub-agent is not on Auto (%v)", err)
		}
		vs, err := turn.Verdicts(ctx, s, turn.VerdictFilter{TurnID: a.WorkTurnID})
		if err != nil || len(vs) != 1 || vs[0].Step == turn.TestLargeModel || vs[0].RequestID != rid {
			t.Errorf("the held chat's sub-agent's verdicts are %+v (%v)", vs, err)
		}
		// No call of anything in the held chat or its sub-agent's went to
		// the hosted step after the hold.
		entries, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindModel})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.RequestID == rid && e.Action == turn.TestLargeModel {
				t.Errorf("a call of the held sub-agent went to the hosted step: %+v", e)
			}
		}
	})
}

// A sub-agent whose call names a model is not on Auto: its chat stays off,
// its turn names the model, and the router keeps no verdict for it.
func TestASubagentThatNamesAModelIsNotOnAuto(t *testing.T) {
	g := newRig(t)
	sc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	model := &director{brief: sc.Brief, done: "Back.", asks: map[string]toolCall{
		"Count the gulls.": {ToolName, map[string]any{"agents": []map[string]any{{"task": "Count the gulls.", "title": "Gulls", "model": houseModel.Name}}}},
	}, sub: func(_ context.Context, _ string, req seam.Request, emit func(seam.Event) error) error {
		return say(emit, req.Model, "Nine gulls.")
	}}
	g.extra = adapters.Set{models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return model, nil }}
	g.addModels(houseModel)
	g.set(g.ordinary.ID, tenantsettings.Person, "chats.default_model", houseModel.Name)
	g.set(g.ordinary.ID, tenantsettings.Admin, router.KeyLadder, houseModel.Name)
	sv := g.serve(nil)

	chatID := sv.newChat(g.ordinary.ID)
	sv.send(g.ordinary.ID, chatID, "Count the gulls.", 20*time.Second)
	subs := sv.subagents(g.ordinary.ID, chatID)
	if len(subs) != 1 || subs[0].Status != StatusDone || subs[0].Model != houseModel.Name {
		t.Fatalf("the sub-agents read %+v", subs)
	}
	ctx := context.Background()
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if on, err := turn.ChatAuto(ctx, s, subs[0].WorkChatID); err != nil || on {
			t.Errorf("a sub-agent that names a model is on Auto (%v)", err)
		}
		vs, err := turn.Verdicts(ctx, s, turn.VerdictFilter{TurnID: subs[0].WorkTurnID})
		if err != nil || len(vs) != 0 {
			t.Errorf("a sub-agent that names a model was routed: %+v (%v)", vs, err)
		}
	})
}
