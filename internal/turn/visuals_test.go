package turn

import (
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// Inline visuals (P9-04). Every question and reply here is the test
// tenant's, and invented.

const (
	chartQuestion   = "Chart the kayak hires at the boathouse for the season."
	diagramQuestion = "Draw how a kayak hire goes, from arriving to handing the paddle back."
)

func shippedSetting(t *testing.T, key string) string {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := settings.Lookup(reg, key)
	if !ok {
		t.Fatalf("%s is not in the registry", key)
	}
	return s.Default
}

// The model is told about the two formats in the standing instructions,
// straight after the standing text, the same in every call, inside the
// part of the call a provider keeps; and the test tenant's recorded turns
// answer with a chart and with a diagram.
func TestTheStandingInstructionsSayHowToDrawAndStayTheSame(t *testing.T) {
	g := newRig(t)
	sv := g.serve(nil)
	id := sv.newChat(g.test.ID)
	rules := shippedSetting(t, contextbuild.KeySystemText)
	how := shippedSetting(t, KeyVisualsInstructions)
	for _, word := range []string{"mermaid", "chart", "bar, line, area, pie or scatter", `"series"`, `"points"`, "clearer than words alone"} {
		if !strings.Contains(how, word) {
			t.Errorf("the shipped %s does not say %q", KeyVisualsInstructions, word)
		}
	}

	events, _, requestID := sv.send(g.test.ID, id, ask(chartQuestion, ""))
	if got := joined(t, events, EventText); !strings.Contains(got, "```chart\n{") || !strings.Contains(got, `"type": "bar"`) {
		t.Fatalf("the recorded reply holds no chart block: %q", got)
	}
	_, first := sentSystem(t, g, g.test.ID, requestID)
	start := standing(t, first)
	if len(start) == 0 || start[0] != rules+"\n\n"+how {
		t.Fatalf("the standing start does not begin with the standing text and then how to draw: %q", start)
	}

	events, _, requestID = sv.send(g.test.ID, id, ask(diagramQuestion, ""))
	if got := joined(t, events, EventText); !strings.Contains(got, "```mermaid\nflowchart LR") {
		t.Fatalf("the recorded reply holds no diagram block: %q", got)
	}
	_, second := sentSystem(t, g, g.test.ID, requestID)
	again := standing(t, second)
	if len(again) != len(start) {
		t.Fatalf("the second call's standing start has %d parts, and the first had %d", len(again), len(start))
	}
	for i := range start {
		if again[i] != start[i] {
			t.Errorf("part %d of the standing start changed between two calls of one chat:\n%q\n%q", i, start[i], again[i])
		}
	}
}

// Switched off, the model is told nothing; with the words emptied, nothing;
// and a person's own words are the ones sent.
func TestThePersonsSettingsDecideWhatTheModelIsTold(t *testing.T) {
	g := newRig(t)
	sv := g.serve(nil)
	rules := shippedSetting(t, contextbuild.KeySystemText)
	how := shippedSetting(t, KeyVisualsInstructions)
	system := func() string {
		id := sv.newChat(g.test.ID)
		_, _, requestID := sv.send(g.test.ID, id, ask(chartQuestion, ""))
		_, req := sentSystem(t, g, g.test.ID, requestID)
		return standing(t, req)[0]
	}

	g.setFor(g.test.ID, tenantsettings.Person, KeyVisualsDraw, "false")
	if got := system(); got != rules || strings.Contains(got, "mermaid") {
		t.Errorf("with visuals.draw off the standing text is %q", got)
	}
	g.setFor(g.test.ID, tenantsettings.Person, KeyVisualsDraw, "true")
	g.setFor(g.test.ID, tenantsettings.Person, KeyVisualsInstructions, "")
	if got := system(); got != rules {
		t.Errorf("with no instructions the standing text is %q", got)
	}
	g.setFor(g.test.ID, tenantsettings.Person, KeyVisualsInstructions, "  Draw a chart only when asked for one.  ")
	if got := system(); got != rules+"\n\nDraw a chart only when asked for one." {
		t.Errorf("with the person's own words the standing text is %q", got)
	}
	if strings.Contains(system(), how) {
		t.Error("the shipped words were sent beside the person's own")
	}
}
