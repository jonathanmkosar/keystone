package adapters

import (
	"context"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/settings"
)

func TestAnthropicOptionsComeFromTheSettings(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	opts, err := AnthropicOptions(reg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Version != "2023-06-01" || opts.MaxRetries != 2 || !opts.CacheControl {
		t.Errorf("the options are not the registry's defaults: %+v", opts)
	}
	if opts.RetryBaseDelay != 500*time.Millisecond || opts.RetryMaxDelay != 20*time.Second ||
		opts.HeaderTimeout != 5*time.Minute || opts.IdleTimeout != 5*time.Minute {
		t.Errorf("the waits are not the registry's defaults: %+v", opts)
	}
	if len(opts.BudgetThinkingModels) == 0 {
		t.Error("no model is listed as taking a thinking budget")
	}
	if len(opts.EffortModels) == 0 {
		t.Error("no model is listed as taking an effort")
	}
	if opts.EffortBudgetLow != 2048 || opts.EffortBudgetMedium != 8192 || opts.EffortBudgetHigh != 24576 {
		t.Errorf("the effort budgets are not the registry's defaults: %d, %d, %d",
			opts.EffortBudgetLow, opts.EffortBudgetMedium, opts.EffortBudgetHigh)
	}
	if _, err := AnthropicOptions(nil); err == nil {
		t.Error("options were read from an empty registry")
	}
}

// A model added to a tenant's registry with the anthropic adapter opens,
// once the builder is in the set. No call is made here: the recorded
// exchanges in the adapter's own folder do that.
func TestAnAnthropicEntryOpens(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	m := hosted
	m.Adapter = models.AdapterAnthropic
	m.Endpoint = "https://api.provider-one.example"
	if err := models.Add(ctx, s, limits(t), m); err != nil {
		t.Fatal(err)
	}
	if err := s.Secrets().Set(m.Key, "an invented value, not a real key"); err != nil {
		t.Fatal(err)
	}
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	set := Set{}
	if _, _, err := Open(ctx, s, set, m.Name); err == nil {
		t.Fatal("the entry opened with no builder in the set")
	}
	if err := AddAnthropic(set, reg); err != nil {
		t.Fatal(err)
	}
	got, p, err := Open(ctx, s, set, m.Name)
	if err != nil || p == nil || got.Name != m.Name {
		t.Fatalf("the entry did not open: %v", err)
	}
}
