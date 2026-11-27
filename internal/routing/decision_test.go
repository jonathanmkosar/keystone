package routing

import (
	"errors"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/models"
)

// P14-02: a decision model takes decision calls and nothing else, and a
// sensitive decision call is held by the same guard as every other kind:
// it never reaches a hosted decision model, whatever the rules name.
func TestPlanRoutesDecisionCalls(t *testing.T) {
	reg := append([]models.Model{
		model("house-decide", models.ClassLocal, models.CapDecision),
		model("cloud-decide", models.ClassHosted, models.CapDecision),
		model("friend-decide", models.ClassTrusted, models.CapDecision),
	}, registry...)
	rules := Rules{Fallback: []string{"house-small", "house-decide", "friend-decide"}, MaxFallbacks: 5}

	route, err := Plan(rules, Ask{Kind: KindDecision, Model: "cloud-decide"}, reg)
	if err != nil || names(route.Models) != "cloud-decide house-decide friend-decide" {
		t.Errorf("a plain decision call is routed to %q (%v)", names(route.Models), err)
	}
	// Only decision models: the chat model in the chain is withheld.
	for _, w := range route.Withheld {
		if w.Name == "house-small" && w.Why != WithheldCapability {
			t.Errorf("a chat model was withheld from a decision route as %s", w.Why)
		}
	}

	// Sensitive: the hosted decision model is withheld, as sensitive.
	route, err = Plan(rules, Ask{Kind: KindDecision, Model: "cloud-decide", Sensitive: true}, reg)
	if err != nil || names(route.Models) != "house-decide friend-decide" {
		t.Errorf("a sensitive decision call is routed to %q (%v)", names(route.Models), err)
	}
	for _, m := range route.Models {
		if !m.MayReadSensitive() {
			t.Errorf("%s is in a sensitive decision route", m.Name)
		}
	}
	// A rule that names the hosted one changes nothing.
	ruled := Rules{Models: map[Kind]string{KindDecision: "cloud-decide"}}
	if _, err := Plan(ruled, Ask{Kind: KindDecision, Model: "cloud-decide", Sensitive: true}, reg); !errors.Is(err, ErrSensitive) {
		t.Errorf("a sensitive decision call named only the hosted decision model and got %v", err)
	}

	// A decision model is never on a chat, title, summary, memory or
	// embedding route, even when it is the model named.
	for _, kind := range []Kind{KindChat, KindTitle, KindSummary, KindMemory, KindEmbedding} {
		route, _ := Plan(Rules{Fallback: []string{"house-small", "house-embed"}, MaxFallbacks: 5}, Ask{Kind: kind, Model: "house-decide"}, reg)
		for _, m := range route.Models {
			if m.Has(models.CapDecision) {
				t.Errorf("%s: the decision model %s is on the route", kind, m.Name)
			}
		}
	}
	// And a chat model never takes a decision call.
	if _, err := Plan(Rules{}, Ask{Kind: KindDecision, Model: "house-small"}, reg); !errors.Is(err, ErrNoModel) {
		t.Errorf("a chat model took a decision call: %v", err)
	}
}
