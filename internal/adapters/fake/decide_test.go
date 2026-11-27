package fake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The router's two questions, as the scripts in decisions.json answer them.
func verdictAsk(state, model string) seam.DecisionRequest {
	return seam.DecisionRequest{
		Model: model, State: state,
		Questions: []seam.DecisionQuestion{
			{ID: "step", Instructions: "Which is the smallest model that answers this request well?", Type: seam.QuestionChoice, Options: []seam.DecisionOption{
				{ID: "house-chat"}, {ID: "small-hosted"}, {ID: "mid-hosted"},
			}},
			{ID: "effort", Instructions: "How much thinking does this request need?", Type: seam.QuestionChoice, Options: []seam.DecisionOption{
				{ID: "low"}, {ID: "medium"}, {ID: "high"},
			}},
		},
	}
}

func shipped(t *testing.T) *Provider {
	t.Helper()
	scripts, err := Load(os.DirFS(filepath.Join("..", "..", "..", "fixtures", "fake-provider")))
	if err != nil {
		t.Fatal(err)
	}
	return provider(t, Options{}, scripts...)
}

// A recorded decision returns each option's probability, and only a
// decision script answers a decision call.
func TestDecideFromScripts(t *testing.T) {
	p := shipped(t)
	ctx := context.Background()

	res, err := p.Decide(ctx, verdictAsk("Newest message: What day is it today?", "test-decide"))
	if err != nil {
		t.Fatal(err)
	}
	step, _ := res.Answer("step")
	effort, _ := res.Answer("effort")
	if res.Model != "test-decide" || step.Choice != "house-chat" || step.Probabilities["house-chat"] != 0.91 || effort.Choice != "low" {
		t.Errorf("the easy question got %+v", res)
	}
	if len(step.Probabilities) != 3 || res.Usage.InputTokens != 212 {
		t.Errorf("the answer lost an option's probability or its usage: %+v", res)
	}

	res, err = p.Decide(ctx, verdictAsk("Please compare the three garden plans in detail.", "test-decide"))
	if step, _ := res.Answer("step"); err != nil || step.Choice != "small-hosted" || step.Probabilities["small-hosted"] != 0.48 {
		t.Errorf("the hard question got %+v, %v", res, err)
	}

	// A decision model that is down fails as it was scripted to.
	_, err = p.Decide(ctx, verdictAsk("A message no other decision script was written for.", "down-decide"))
	var se *seam.Error
	if !errors.As(err, &se) || se.Kind != seam.ErrUnavailable || !se.Retryable {
		t.Errorf("the decision model that is down gave %v", err)
	}

	// A yes or no, matched by its instructions.
	yn := seam.DecisionRequest{Model: "test-decide", State: "Anything at all.", Questions: []seam.DecisionQuestion{
		{ID: "needs_file", Instructions: "Does the message ask for a file?", Type: seam.QuestionYesNo},
	}}
	res, err = p.Decide(ctx, yn)
	if a, _ := res.Answer("needs_file"); err != nil || a.Choice != "no" || a.Probabilities["yes"] != 0.12 {
		t.Errorf("the yes or no got %+v, %v", res, err)
	}

	// No script: refused as invalid. The catch-all that answers any
	// message answers no decision.
	_, err = p.Decide(ctx, verdictAsk("Nothing scripted says this.", "test-decide"))
	if !errors.As(err, &se) || se.Kind != seam.ErrInvalid || !strings.Contains(se.Message, "no decision script") {
		t.Errorf("an unscripted decision gave %v", err)
	}
	// A script whose answer does not fit the questions asked is refused.
	odd := verdictAsk("what day is it", "test-decide")
	odd.Questions[0].Options = odd.Questions[0].Options[:2]
	if _, err := p.Decide(ctx, odd); !errors.As(err, &se) || se.Kind != seam.ErrInvalid || !strings.Contains(se.Message, "does not answer the request") {
		t.Errorf("a script that does not answer the request gave %v", err)
	}
	// A request that is not valid.
	if _, err := p.Decide(ctx, seam.DecisionRequest{Model: "test-decide", State: "x"}); !errors.As(err, &se) || se.Kind != seam.ErrInvalid {
		t.Errorf("a request with no question gave %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Decide(cancelled, verdictAsk("what day is it", "test-decide")); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled call gave %v", err)
	}

	// And a decision script never answers a turn.
	if _, err := seam.Call(ctx, p, ask("compare the three garden plans")); err != nil {
		// The shipped catch-all answers it, so it must not fail.
		t.Errorf("a turn whose words a decision script holds failed: %v", err)
	}
	only := provider(t, Options{}, Script{Name: "only a decision", When: When{Decision: true}, Steps: []Step{{Error: &seam.Error{Kind: seam.ErrOther, Message: "scripted"}}}})
	if _, err := seam.Call(ctx, only, ask("anything")); !errors.As(err, &se) || !strings.Contains(se.Message, "no script answers") {
		t.Errorf("a decision script answered a turn: %v", err)
	}
}

// A decision script is checked when it is read.
func TestDecisionScriptChecks(t *testing.T) {
	answer := &seam.DecisionResult{Answers: []seam.DecisionAnswer{{Question: "q", Choice: "a", Probabilities: map[string]float64{"a": 1, "b": 0}}}}
	for _, c := range []struct {
		name string
		s    Script
		want string
	}{
		{"two steps", Script{Name: "x", When: When{Decision: true}, Steps: []Step{{Decision: answer}, {Decision: answer}}}, "one step"},
		{"a reply", Script{Name: "x", When: When{Decision: true}, Steps: []Step{{Reply: reply(seam.StopEnd, seam.Usage{}, text("hi"))}}}, "never a reply"},
		{"both", Script{Name: "x", When: When{Decision: true}, Steps: []Step{{Decision: answer, Error: &seam.Error{Kind: seam.ErrOther, Message: "m"}}}}, "not both"},
		{"nothing answered", Script{Name: "x", When: When{Decision: true}, Steps: []Step{{Decision: &seam.DecisionResult{}}}}, "answers no question"},
		{"a decision in a turn", Script{Name: "x", Steps: []Step{{Decision: answer}}}, `"decision": true`},
	} {
		if _, err := New([]Script{c.s}, Options{}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error that says %q", c.name, err, c.want)
		}
	}
	// A decision script and a turn script with the same conditions are two
	// different scripts.
	turn := Script{Name: "turn", Steps: []Step{{Reply: reply(seam.StopEnd, seam.Usage{}, text("hi"))}}}
	dec := Script{Name: "decision", When: When{Decision: true}, Steps: []Step{{Decision: answer}}}
	if _, err := New([]Script{turn, dec}, Options{}); err != nil {
		t.Errorf("a turn script and a decision script with the same conditions were refused: %v", err)
	}
}
