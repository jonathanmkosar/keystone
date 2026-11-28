package router

import (
	"testing"

	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/settings"
)

// The package itself imports neither the settings nor the budget (fixed
// rule 3); its tests may, to hold it to them.

// The router's settings are in the registry with the spec's names, types,
// defaults and scopes, and Load reads the defaults.
func TestTheSettingsAreTheSpecs(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][3]string{
		KeyLadder:          {"string", "", "tenant-admin"},
		KeyDecisionModel:   {"string", "", "tenant-admin"},
		KeyStepNotes:       {"string", "", "tenant-admin"},
		KeyMinConfidence:   {"float", "0.6", "install"},
		KeyVerdictTimeout:  {"duration", "2s", "install"},
		KeyVerdictMaxChars: {"int", "8000", "install"},
		KeyDefaultEffort:   {"string", "medium", "install"},
		KeyMaxEffort:       {"string", "high", "install"},
		KeyLocalDownFor:    {"duration", "2m", "install"},
	}
	if len(Keys()) != len(want) {
		t.Errorf("Keys has %d keys, want %d", len(Keys()), len(want))
	}
	for _, key := range Keys() {
		st, ok := settings.Lookup(reg, key)
		w := want[key]
		if !ok || st.Type != w[0] || st.Default != w[1] || string(st.Scope) != w[2] || st.Lane != "models" {
			t.Errorf("%s is %+v, want %s, default %q, %s, in models", key, st, w[0], w[1], w[2])
		}
	}
	s, err := Load(func(key string) (string, bool) {
		st, ok := settings.Lookup(reg, key)
		return st.Default, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Ladder) != 0 || s.DecisionModel != "" || s.DefaultEffort != seam.EffortMedium || s.MaxEffort != seam.EffortHigh {
		t.Errorf("the defaults load as %+v", s)
	}
}

// fitsWindow is the budget's own check on the same numbers.
func TestTheWindowIsTheBudgets(t *testing.T) {
	for _, m := range usable {
		for _, tokens := range []int{0, 1, 4000, 28672, 28673, 32000, 190000, 191808, 191809, 999999} {
			for _, reply := range []int{0, 1, 768, 4096, 8192, 100000} {
				_, err := budget.Measure(m, seam.Request{MaxOutputTokens: reply}, budget.Count{Tokens: tokens})
				if got := fitsWindow(m, Facts{Tokens: tokens, ReplyTokens: reply}); got != (err == nil) {
					t.Errorf("%s, %d tokens, reply %d: the router says fits %v and the budget says %v", m.Name, tokens, reply, got, err)
				}
			}
		}
	}
}
