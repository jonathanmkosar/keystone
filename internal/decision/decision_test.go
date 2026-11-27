package decision

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Every model, state and number here is invented.

func model(name string, class models.Class, caps ...models.Capability) models.Model {
	return models.Model{Name: name, Label: name, Class: class, ContextWindow: 8192, OutputLimit: 1, Capabilities: caps,
		Price: models.Price{Input: 2, Output: 10}}
}

var (
	houseDecide  = model("house-decide", models.ClassLocal, models.CapDecision)
	cloudDecide  = model("cloud-decide", models.ClassHosted, models.CapDecision)
	friendDecide = model("friend-decide", models.ClassTrusted, models.CapDecision)
	houseChat    = model("house-chat", models.ClassLocal, models.CapTools)
	usable       = []models.Model{houseDecide, cloudDecide, friendDecide, houseChat}
)

func store(t *testing.T) *tenant.Store {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ask() seam.DecisionRequest {
	return seam.DecisionRequest{State: "Newest message: what day is it?", Questions: []seam.DecisionQuestion{
		{ID: "step", Instructions: "Which model?", Type: seam.QuestionChoice, Options: []seam.DecisionOption{{ID: "house-chat"}, {ID: "mid-hosted"}}},
	}}
}

// decider answers every call the same way, or fails, and counts calls.
type decider struct {
	calls  atomic.Int32
	err    error
	answer func(seam.DecisionRequest) seam.DecisionResult
}

func (d *decider) Decide(_ context.Context, req seam.DecisionRequest) (seam.DecisionResult, error) {
	d.calls.Add(1)
	if d.err != nil {
		return seam.DecisionResult{}, d.err
	}
	if d.answer != nil {
		return d.answer(req), nil
	}
	return seam.DecisionResult{Answers: []seam.DecisionAnswer{
		{Question: "step", Choice: "house-chat", Probabilities: map[string]float64{"house-chat": 0.9, "mid-hosted": 0.1}},
	}, Usage: seam.Usage{InputTokens: 1000, OutputTokens: 100}}, nil
}

// The proof's third line: a decision call with sensitive state never
// reaches a hosted entry.
func TestChooseKeepsSensitiveCallsOffHostedModels(t *testing.T) {
	m, err := Choose("cloud-decide", false, usable, nil)
	if err != nil || m.Name != "cloud-decide" {
		t.Errorf("a plain call to the hosted decision model gave %s, %v", m.Name, err)
	}
	if _, err := Choose("cloud-decide", true, usable, nil); !errors.Is(err, ErrNoModel) || !errors.Is(err, routing.ErrSensitive) {
		t.Errorf("a sensitive call reached the hosted decision model: %v", err)
	}
	for _, name := range []string{"house-decide", "friend-decide"} {
		if m, err := Choose(name, true, usable, nil); err != nil || m.Name != name {
			t.Errorf("a sensitive call to %s gave %s, %v", name, m.Name, err)
		}
	}
	// Not named, not a decision model, not there, or not openable: no model.
	for _, name := range []string{"", "house-chat", "not-there"} {
		if _, err := Choose(name, false, usable, nil); !errors.Is(err, ErrNoModel) {
			t.Errorf("Choose(%q) gave %v", name, err)
		}
	}
	none := func(models.Model) bool { return false }
	if _, err := Choose("house-decide", false, usable, none); !errors.Is(err, ErrNoModel) {
		t.Errorf("a model this build cannot open was chosen: %v", err)
	}
}

// The proof's fourth line: every decision call has its audit entries and
// its usage record, whether it was answered, refused or failed.
func TestEveryCallIsRecorded(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	type want struct {
		outcome audit.Outcome
		usage   usage.Outcome
		calls   int32
		cost    int64
	}
	cases := []struct {
		name string
		m    models.Model
		d    *decider
		c    Call
		opts Options
		want want
	}{
		{"answered on a local model", houseDecide, &decider{}, Call{RequestID: "req-local"}, Options{},
			want{audit.OutcomeOK, usage.OutcomeOK, 1, 0}},
		{"answered on a trusted model", friendDecide, &decider{}, Call{RequestID: "req-trusted"}, Options{},
			want{audit.OutcomeOK, usage.OutcomeOK, 1, 3000}},
		{"sensitive to a hosted model", cloudDecide, &decider{}, Call{RequestID: "req-held", Sensitive: true}, Options{},
			want{audit.OutcomeRefused, usage.OutcomeRefused, 0, 0}},
		{"stopped by the gate", friendDecide, &decider{}, Call{RequestID: "req-gate"},
			Options{Gate: func(context.Context, models.Model, seam.DecisionRequest) *seam.Error {
				return &seam.Error{Kind: seam.ErrSpendCap, Message: "over the cap"}
			}},
			want{audit.OutcomeRefused, usage.OutcomeRefused, 0, 0}},
		{"over the adapter's limit", houseDecide, &decider{err: &seam.Error{Kind: seam.ErrTooLong, Message: "too many questions"}}, Call{RequestID: "req-limit"}, Options{},
			want{audit.OutcomeRefused, usage.OutcomeRefused, 1, 0}},
		{"the endpoint down", houseDecide, &decider{err: &seam.Error{Kind: seam.ErrUnavailable, Message: "not answering", Retryable: true}}, Call{RequestID: "req-down"}, Options{},
			want{audit.OutcomeError, usage.OutcomeError, 1, 0}},
		{"an answer that does not answer", houseDecide, &decider{answer: func(seam.DecisionRequest) seam.DecisionResult {
			return seam.DecisionResult{Answers: []seam.DecisionAnswer{{Question: "other", Choice: "a", Probabilities: map[string]float64{"a": 1}}}}
		}}, Call{RequestID: "req-odd"}, Options{},
			want{audit.OutcomeError, usage.OutcomeError, 1, 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.c.Actor, c.c.Request = "router", ask()
			res, err := Run(ctx, s, Opened{Model: c.m, Decider: c.d}, c.c, c.opts)
			if c.want.outcome == audit.OutcomeOK {
				if err != nil || res.Model != c.m.Name || res.Answers[0].Probabilities["house-chat"] != 0.9 {
					t.Errorf("the call gave %+v, %v", res, err)
				}
			} else if err == nil {
				t.Error("the call did not fail")
			}
			if c.d.calls.Load() != c.want.calls {
				t.Errorf("the decider was called %d times, want %d", c.d.calls.Load(), c.want.calls)
			}
			entries, err := audit.List(ctx, s, audit.Filter{RequestID: c.c.RequestID})
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 || entries[0].Outcome != audit.OutcomeStarted || entries[1].Outcome != c.want.outcome {
				t.Fatalf("the audit log holds %+v, want a started entry and an end entry %s", entries, c.want.outcome)
			}
			for _, e := range entries {
				if e.Kind != audit.KindModel || e.Action != c.m.Name || e.Actor != "router" {
					t.Errorf("an entry is %+v", e)
				}
			}
			if !strings.Contains(entries[0].Sent, "what day is it") || !strings.Contains(entries[0].Sent, `"questions"`) {
				t.Errorf("the started entry did not keep the request: %s", entries[0].Sent)
			}
			if entries[1].CostMicros != c.want.cost {
				t.Errorf("the end entry cost %d, want %d", entries[1].CostMicros, c.want.cost)
			}
			recs, err := usage.List(ctx, s, usage.Filter{RequestID: c.c.RequestID})
			if err != nil {
				t.Fatal(err)
			}
			if len(recs) != 1 || recs[0].Kind != "decision" || recs[0].Outcome != c.want.usage || recs[0].Model != c.m.Name || recs[0].Class != c.m.Class || recs[0].CostMicros != c.want.cost {
				t.Errorf("the usage records are %+v, want one of the kind decision, %s, costing %d", recs, c.want.usage, c.want.cost)
			}
		})
	}
}

// A call whose endpoint gave no count is estimated, and marked so. A
// request that is not valid, or names another model, is no call at all.
func TestEstimateAndRequestsThatAreNoCall(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	silent := &decider{answer: func(seam.DecisionRequest) seam.DecisionResult {
		return seam.DecisionResult{Answers: []seam.DecisionAnswer{{Question: "step", Choice: "house-chat", Probabilities: map[string]float64{"house-chat": 0.6, "mid-hosted": 0.4}}}}
	}}
	est := func(models.Model, seam.DecisionRequest) seam.Usage { return seam.Usage{InputTokens: 50} }
	res, err := Run(ctx, s, Opened{Model: friendDecide, Decider: silent}, Call{RequestID: "req-est", Actor: "router", Request: ask()}, Options{Estimate: est})
	if err != nil || !res.Usage.Estimated || res.Usage.InputTokens != 50 {
		t.Errorf("the estimate gave %+v, %v", res.Usage, err)
	}
	recs, _ := usage.List(ctx, s, usage.Filter{RequestID: "req-est"})
	if len(recs) != 1 || !recs[0].Estimated || recs[0].CostMicros != 100 {
		t.Errorf("the estimated record is %+v", recs)
	}

	bad := ask()
	bad.Questions = nil
	other := ask()
	other.Model = "someone-else"
	for _, req := range []seam.DecisionRequest{bad, other} {
		d := &decider{}
		_, err := Run(ctx, s, Opened{Model: houseDecide, Decider: d}, Call{RequestID: "req-none", Request: req}, Options{})
		var se *seam.Error
		if !errors.As(err, &se) || se.Kind != seam.ErrInvalid || d.calls.Load() != 0 {
			t.Errorf("a request that is no call gave %v after %d calls", err, d.calls.Load())
		}
	}
	if entries, _ := audit.List(ctx, s, audit.Filter{RequestID: "req-none"}); len(entries) != 0 {
		t.Errorf("a request that is no call was recorded: %+v", entries)
	}
	if text := Text(ask()); !strings.Contains(text, "what day is it") || !strings.Contains(text, "- mid-hosted") {
		t.Errorf("the text is %q", text)
	}
	if r := AsRequest(houseDecide, ask()); r.MaxOutputTokens != 1 || seam.ValidateRequest(r) != nil {
		t.Errorf("the request for the budget is %+v: %v", r, seam.ValidateRequest(r))
	}
}
