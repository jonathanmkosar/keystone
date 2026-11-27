package settings

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestEverySettingHasATitleAndASummary is half of the proof of P11-04: the
// settings screen shows a setting by its title and its short line, so every
// setting compiled into the binary has both, in the shape CheckWords asks.
func TestEverySettingHasATitleAndASummary(t *testing.T) {
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range CheckWords(reg) {
		t.Error(p)
	}
	// The words are beside the description and never in its place.
	for _, s := range reg {
		if s.Summary == s.Description && len(strings.Fields(s.Description)) > summaryMaxWords {
			t.Errorf("%s: the summary is the whole description", s.Key)
		}
	}
}

// A setting with no title and no summary still loads: a lane that adds one
// breaks nothing that runs, and CheckWords is what says so.
func TestASettingWithoutWordsLoadsAndIsReported(t *testing.T) {
	reg, err := Read(fstest.MapFS{"a.json": file("a", good)})
	if err != nil {
		t.Fatalf("a setting with no title and no summary was refused: %v", err)
	}
	problems := strings.Join(CheckWords(reg), "\n")
	for _, want := range []string{"settings/a.json: a.b has no title", "settings/a.json: a.b has no summary", `"summary" line under its "key" line`} {
		if !strings.Contains(problems, want) {
			t.Errorf("CheckWords says %q, and should say %q", problems, want)
		}
	}
}

func TestCheckWordsRefuses(t *testing.T) {
	ok := Setting{Key: "a.b", Title: "Budget for each run", Summary: "The most one run may spend on paid models."}
	if p := CheckWords([]Setting{ok}); len(p) != 0 {
		t.Fatalf("a good title and summary were refused: %v", p)
	}
	cases := []struct {
		name           string
		title, summary string
		want           string
	}{
		{"a long title", "The budget for each and every scheduled run of a task", ok.Summary, "title of 53 characters"},
		{"a title in lower case", "budget", ok.Summary, "does not start with a capital"},
		{"a title that is a sentence", "Budget for each run.", ok.Summary, "ends in a full stop"},
		{"a title made from the key", "Schedule budget_usd", ok.Summary, "title with a key in it"},
		{"a title with a space after it", "Budget ", ok.Summary, "space at an end"},
		{"a long summary", ok.Title, "The most one run of a scheduled task may spend on models that are not local ones, in dollars.", "summary of 93 characters"},
		{"a summary of many short words", ok.Title, "It is the most a run may pay for a job of its own in a day.", "summary of 17 words"},
		{"a summary of one word", ok.Title, "Budget.", "summary of 1 words"},
		{"a summary with no full stop", ok.Title, "The most one run may spend", "does not end in a full stop"},
		{"a summary that names a key", ok.Title, "The most a run may spend, under usage.monthly_cap_usd.", "key or a placeholder"},
		{"a summary with a placeholder", ok.Title, "What {name} becomes in the heading.", "key or a placeholder"},
	}
	for _, c := range cases {
		p := strings.Join(CheckWords([]Setting{{Key: "a.b", Title: c.title, Summary: c.summary}}), "\n")
		if !strings.Contains(p, c.want) {
			t.Errorf("%s: CheckWords says %q, and should say %q", c.name, p, c.want)
		}
	}
	twice := CheckWords([]Setting{ok, {Key: "a.c", Title: "Budget For Each Run", Summary: ok.Summary}})
	if len(twice) != 1 || !strings.Contains(twice[0], "and so has a.b") {
		t.Errorf("two settings with one title: CheckWords says %v", twice)
	}
}
