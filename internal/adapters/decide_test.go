package adapters

import (
	"context"
	"errors"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/adapters/systemone"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/settings"
)

// A decision model in the registry opens as a decider; a chat model, and a
// model whose adapter gives no decisions, do not.
func TestOpenDecider(t *testing.T) {
	s := store(t)
	lim := limits(t)
	decide := models.Model{Name: "test-decide", Label: "Test decisions", Adapter: models.AdapterFake,
		ContextWindow: 8192, OutputLimit: 1, Capabilities: []models.Capability{models.CapDecision}, Class: models.ClassLocal}
	for _, m := range []models.Model{decide, scripted} {
		if err := models.Add(context.Background(), s, lim, m); err != nil {
			t.Fatal(err)
		}
	}
	answer := &seam.DecisionResult{Answers: []seam.DecisionAnswer{{Question: "simple", Choice: "yes", Probabilities: map[string]float64{"yes": 0.9, "no": 0.1}}}}
	set, err := Fake([]fake.Script{{Name: "yes", When: fake.When{Decision: true}, Steps: []fake.Step{{Decision: answer}}}}, fake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	m, d, err := OpenDecider(context.Background(), s, set, "test-decide")
	if err != nil {
		t.Fatal(err)
	}
	res, err := d.Decide(context.Background(), seam.DecisionRequest{Model: m.Name, State: "An invented state.",
		Questions: []seam.DecisionQuestion{{ID: "simple", Instructions: "Is it simple?", Type: seam.QuestionYesNo}}})
	if err != nil || res.Answers[0].Probabilities["yes"] != 0.9 {
		t.Fatalf("the decider answered %+v, error %v", res, err)
	}
	if _, _, err := OpenDecider(context.Background(), s, set, "scripted"); !errors.Is(err, ErrNotDecider) {
		t.Errorf("a chat model opened as a decider: %v", err)
	}
	plain := Set{models.AdapterFake: func(models.Model, string) (seam.Provider, error) { return replyOnly{}, nil }}
	if _, _, err := OpenDecider(context.Background(), s, plain, "test-decide"); !errors.Is(err, ErrNotDecider) {
		t.Errorf("a provider with no Decide opened as a decider: %v", err)
	}
	for adapter, want := range map[string]bool{
		models.AdapterFake: true, models.AdapterSystemone: true,
		models.AdapterChatCompletions: false, models.AdapterAnthropic: false, models.AdapterResponses: false,
	} {
		if Decides(adapter) != want {
			t.Errorf("Decides(%s) is %v", adapter, !want)
		}
	}
	var _ seam.Decider = (*systemone.Provider)(nil)
	var _ seam.Provider = (*systemone.Provider)(nil)
	var _ seam.Decider = (*fake.Provider)(nil)
}

// The systemone adapter is in the server's set, with the spec's limits.
func TestSystemoneInTheRealSet(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	set, err := Real(reg)
	if err != nil {
		t.Fatal(err)
	}
	if set[models.AdapterSystemone] == nil {
		t.Fatal("the real set has no systemone adapter")
	}
	opts, err := SystemoneOptions(reg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Timeout.String() != "5s" || opts.MaxRequestBytes != 65536 || opts.MaxQuestions != 64 {
		t.Errorf("the shipped options are %+v", opts)
	}
	for _, key := range []string{SettingSystemoneTimeout, SettingSystemoneMaxRequestBytes, SettingSystemoneMaxQuestions} {
		st, _ := settings.Lookup(reg, key)
		if st.Scope != settings.ScopeInstall {
			t.Errorf("%s has the scope %s, want install", key, st.Scope)
		}
	}
	if _, err := SystemoneOptions(nil); err == nil {
		t.Error("options were read from an empty registry")
	}
}
