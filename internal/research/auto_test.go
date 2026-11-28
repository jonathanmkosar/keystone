package research

import (
	"context"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// Auto for unattended work (P14-06): a research task is a background task
// and its parts are sub-agents, so with the test tenant's recorded ladder
// its own chat and each part's start on Auto, and each of their turns is
// routed, with recorded verdicts that keep everything on the local step.
type decidingResearcher struct {
	*researcher
	verdicts *fake.Provider
}

func (p decidingResearcher) Decide(ctx context.Context, req seam.DecisionRequest) (seam.DecisionResult, error) {
	return p.verdicts.Decide(ctx, req)
}

func TestAResearchTaskAndItsPartsRunOnAuto(t *testing.T) {
	g := newRig(t, museumPages)
	g.set(g.test.ID, tenantsettings.Admin, router.KeyLadder, turn.TestSmallModel+", "+turn.TestMiddleModel+", "+turn.TestLargeModel)
	g.set(g.test.ID, tenantsettings.Admin, router.KeyDecisionModel, turn.TestDecisionModel)
	scripts, err := fake.Decode([]byte(`{"test_data": true, "scripts": [
	{"name": "everything is easy", "when": {"decision": true}, "steps": [
		{"decision": {"answers": [
			{"question": "step", "choice": "recorded-small", "probabilities": {"recorded-small": 0.9, "recorded-middle": 0.07, "recorded-large": 0.03}},
			{"question": "effort", "choice": "low", "probabilities": {"low": 0.85, "medium": 0.13, "high": 0.02}}],
			"usage": {"input_tokens": 220, "output_tokens": 2}}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	verdicts, err := fake.New(scripts, fake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	sv := g.serve(decidingResearcher{researcher: museumModel(), verdicts: verdicts})
	chatID := sv.newChat(g.test.ID)

	started := sv.start(g.test.ID, chatID, StartBody{Question: museumQuestion})
	got := sv.ends(g.test.ID, chatID, started.ID)
	if got.Status != StatusDone || len(got.Parts) != 2 {
		t.Fatalf("the research task on Auto ended as %+v", got)
	}

	ctx := context.Background()
	g.with(g.test.ID, func(s *tenant.Store) {
		all, err := turn.Verdicts(ctx, s, turn.VerdictFilter{})
		if err != nil {
			t.Fatal(err)
		}
		in := map[string]int{}
		for _, v := range all {
			var chat string
			if err := s.DB().QueryRow("SELECT chat_id FROM turns WHERE id = ?", v.TurnID).Scan(&chat); err != nil {
				t.Fatal(err)
			}
			in[chat]++
			if v.Step != turn.TestSmallModel {
				t.Errorf("a verdict chose %s", v.Step)
			}
		}
		chats := []string{got.WorkChatID, got.Parts[0].WorkChatID, got.Parts[1].WorkChatID}
		for i, id := range chats {
			if on, err := turn.ChatAuto(ctx, s, id); err != nil || !on {
				t.Errorf("chat %d of the research task is not on Auto (%v)", i+1, err)
			}
			if in[id] == 0 {
				t.Errorf("chat %d of the research task has no verdict: %v", i+1, in)
			}
		}
		if in[got.WorkChatID] < 2 {
			t.Errorf("the research task's own chat was routed %d times, want its plan and its report", in[got.WorkChatID])
		}
	})
}
