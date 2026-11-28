package schedules

import (
	"context"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// Auto for unattended work (P14-06), on scheduled runs: the harbour note's
// prompt, with a recorded verdict that sends it to the middle step of the
// test tenant's recorded ladder at medium effort.
func autoHarbourScripts(t *testing.T) seam.Provider {
	t.Helper()
	raw := `{"test_data": true, "scripts": [
	{"name": "the harbour note goes to the middle step", "when": {"decision": true, "user_text_contains": "morning harbour note"}, "steps": [
		{"decision": {"answers": [
			{"question": "step", "choice": "recorded-middle", "probabilities": {"recorded-small": 0.1, "recorded-middle": 0.85, "recorded-large": 0.05}},
			{"question": "effort", "choice": "medium", "probabilities": {"low": 0.2, "medium": 0.7, "high": 0.1}}],
			"usage": {"input_tokens": 230, "output_tokens": 2}}}]},
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

// The item's proof: a scheduled run whose schedule names no model starts
// on Auto and uses the router. Its turn is routed to the step the verdict
// named, the verdict is kept under the run's own request id, and the
// decision call is recorded as the schedule's, with its usage record.
func TestAScheduledRunOnAutoUsesTheRouter(t *testing.T) {
	g := newRig(t, utc(t, "2026-11-27T06:00:00Z"))
	g.set(g.test.ID, KeyTimeZone, "Europe/Lisbon") // UTC in November
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		for key, value := range map[string]string{
			router.KeyLadder:        turn.TestSmallModel + ", " + turn.TestMiddleModel + ", " + turn.TestLargeModel,
			router.KeyDecisionModel: turn.TestDecisionModel,
		} {
			if err := tenantsettings.Set(context.Background(), s, reg, tenantsettings.Admin, key, value); err != nil {
				t.Fatal(err)
			}
		}
	})
	sv := g.serve(autoHarbourScripts(t))
	made := sv.make(g.test.ID, daily("Harbour note", harbourPrompt, "07:00"))
	if made.Model != "" {
		t.Fatalf("the schedule names the model %q", made.Model)
	}

	g.clock.set(utc(t, "2026-11-27T07:00:30Z"))
	sv.ticks(1)
	got := sv.settle(g.test.ID, made.ID, 1)
	run := got.Runs[0]
	if run.Status != "done" || run.ChatID == "" {
		t.Fatalf("the run on Auto ended as %+v", run)
	}
	_, msgs := sv.messages(g.test.ID, run.ChatID)
	last := msgs[len(msgs)-1]
	if text(last) != harbourReply || last.Model != turn.TestMiddleModel || last.Route == nil || !last.Route.Auto || last.Route.Effort != "medium" {
		t.Fatalf("the run's reply is %q from %s, routed %+v", text(last), last.Model, last.Route)
	}

	ctx := context.Background()
	g.with(g.test.ID, func(s *tenant.Store) {
		if on, err := turn.ChatAuto(ctx, s, run.ChatID); err != nil || !on {
			t.Errorf("the run's chat is not on Auto (%v)", err)
		}
		// The run's task has a request id of its own, which its turns, and so
		// its verdict and its decision call, are recorded under.
		var requestID, actor string
		if err := s.DB().QueryRow("SELECT request_id, actor FROM jobs WHERE id = ?", run.TaskID).Scan(&requestID, &actor); err != nil {
			t.Fatal(err)
		}
		verdicts, err := turn.Verdicts(ctx, s, turn.VerdictFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(verdicts) != 1 {
			t.Fatalf("%d verdicts were kept, want 1: %+v", len(verdicts), verdicts)
		}
		v := verdicts[0]
		if v.RequestID != requestID || v.DecisionModel != turn.TestDecisionModel || v.StepAnswered != turn.TestMiddleModel ||
			v.Probability != 0.85 || v.Step != turn.TestMiddleModel || v.Effort != "medium" || v.RulesOnly {
			t.Errorf("the run's verdict is %+v, under %s", v, requestID)
		}
		entries, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindModel, RequestID: requestID})
		if err != nil {
			t.Fatal(err)
		}
		decided, chatted := false, false
		for _, e := range entries {
			if e.Outcome != audit.OutcomeStarted {
				continue
			}
			if e.Actor != actor {
				t.Errorf("a call of the run was recorded as %q, want %q", e.Actor, actor)
			}
			switch e.Action {
			case turn.TestDecisionModel:
				decided = true
			case turn.TestMiddleModel:
				chatted = true
			default:
				t.Errorf("a call of the run went to %s", e.Action)
			}
		}
		if !decided || !chatted {
			t.Errorf("the run's audit entries are %+v", entries)
		}
		recs, err := usage.List(ctx, s, usage.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		decisions := 0
		for _, r := range recs {
			if r.RequestID == requestID && r.Kind == "decision" {
				decisions++
			}
		}
		if decisions != 1 {
			t.Errorf("the run's decision call has %d usage records, want 1", decisions)
		}
	})
}
