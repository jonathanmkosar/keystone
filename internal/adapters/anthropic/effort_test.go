package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The recorded exchanges in fixtures/anthropic/effort.json show each effort
// on the wire. These check what a recording cannot: the token count's body,
// and the options the adapter refuses.

func TestATokenCountCarriesNoEffort(t *testing.T) {
	opts := testOptions()
	opts.EffortModels = []string{"example-large-*"}
	p, err := New(testEntry("http://127.0.0.1:1"), testKey, opts)
	if err != nil {
		t.Fatal(err)
	}
	req := textRequest("Plan three dinners for the week.")
	req.Effort = seam.EffortHigh
	for _, stream := range []bool{true, false} {
		raw, err := p.body(req, stream)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			OutputConfig *struct {
				Effort string `json:"effort"`
			} `json:"output_config"`
			Thinking *wireThinking `json:"thinking"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		switch {
		case stream && (body.OutputConfig == nil || body.OutputConfig.Effort != "high"):
			t.Errorf("the call does not carry the effort: %s", raw)
		case !stream && body.OutputConfig != nil:
			t.Errorf("the token count carries the effort, which does not change the input: %s", raw)
		case body.Thinking == nil || body.Thinking.Type != "adaptive":
			t.Errorf("the thinking is not the same in the call and the count: %s", raw)
		}
	}
}

func TestANegativeEffortBudgetIsRefused(t *testing.T) {
	for _, set := range []func(*Options){
		func(o *Options) { o.EffortBudgetLow = -1 },
		func(o *Options) { o.EffortBudgetMedium = -1 },
		func(o *Options) { o.EffortBudgetHigh = -1 },
	} {
		opts := testOptions()
		set(&opts)
		if _, err := New(testEntry("http://127.0.0.1:1"), testKey, opts); err == nil {
			t.Errorf("options with a negative budget were taken: %+v", opts)
		}
	}
}

// A budget under the least the API takes asks for no thinking, as a
// thinking_tokens under it does, and the effort still goes to a model
// that takes it.
func TestABudgetTooSmallToSendAsksForNoThinking(t *testing.T) {
	opts := testOptions()
	opts.EffortBudgetLow = 500
	opts.BudgetThinkingModels = []string{"example-large-*"}
	entry := testEntry("http://127.0.0.1:1")
	entry.Capabilities = []models.Capability{models.CapThinking}
	p, err := New(entry, testKey, opts)
	if err != nil {
		t.Fatal(err)
	}
	req := textRequest("Plan three dinners for the week.")
	req.Effort = seam.EffortLow
	raw, err := p.body(req, true)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["thinking"]; ok {
		t.Errorf("a budget of 500 asked for thinking: %s", raw)
	}
	if _, ok := body["output_config"]; ok {
		t.Errorf("a model that takes no effort was sent one: %s", raw)
	}
}
