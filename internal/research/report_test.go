package research

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/research/cite"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/subagents"
	"github.com/jonathanmkosar/keystone/internal/subagents/carry"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

// A plan's reply is read as a list, one part a line, and no more parts than
// the setting allows; a reply that is no list is one part, the question.
func TestThePlanIsReadAsAList(t *testing.T) {
	reply := "Sure, here is a plan:\n\n1. Opening hours: Find when the museum opens.\n2) **Getting there**: Find the ferry times.\n- Find what a ticket costs for a family of four on a weekday\n* \n4. Extra: One part too many."
	got := parsePlan(reply, 3, "Museum", "When can I go?")
	want := []PlanPart{
		{Title: "Opening hours", Task: "Find when the museum opens."},
		{Title: "Getting there", Task: "Find the ferry times."},
		{Title: "Find what a ticket costs for", Task: "Find what a ticket costs for a family of four on a weekday"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the plan reads %+v", got)
	}
	if got := parsePlan("I would look at the museum's own site first.", 4, "Museum", "When can I go?"); !reflect.DeepEqual(got, []PlanPart{{Title: "Museum", Task: "When can I go?"}}) {
		t.Errorf("a reply that is no list reads %+v", got)
	}
}

// The summary is the text under the Summary heading, or the first
// paragraph, cut at the setting.
func TestTheSummaryIsTakenFromTheReport(t *testing.T) {
	body := "# A title\n\n## Summary\n\nOpen from ten [1].\nClosed on Mondays [2].\n\n## Details\n\nMore."
	if got := summaryOf(body, 700); got != "Open from ten [1]. Closed on Mondays [2]." {
		t.Errorf("the summary reads %q", got)
	}
	if got := summaryOf("# A title\n\n> A quoted question\n\nThe first paragraph\nof the report.\n\nThe second.", 700); got != "The first paragraph of the report." {
		t.Errorf("with no Summary heading the summary reads %q", got)
	}
	if got := summaryOf("One two three four five six seven eight nine ten.", 22); got != "One two three four…" {
		t.Errorf("a long summary is cut to %q", got)
	}
}

// A model's own list of sources at the end is taken off; one in the middle
// is not.
func TestAModelsOwnListOfSourcesIsTakenOff(t *testing.T) {
	if got := stripSources("## Summary\n\nText [1].\n\n## References\n\n1. Made up"); got != "## Summary\n\nText [1]." {
		t.Errorf("the body reads %q", got)
	}
	keep := "## Sources\n\nThe museum's own.\n\n## Summary\n\nText."
	if got := stripSources(keep); got != keep {
		t.Errorf("a section that is not the last was taken off: %q", got)
	}
}

func textMessage(role seam.Role, blocks ...seam.Block) chat.NewMessage {
	return chat.NewMessage{Role: role, Blocks: blocks}
}

func resultBlock(id, text string) seam.Block {
	return seam.Block{Type: seam.BlockToolResult, ID: id, Content: []seam.Block{{Type: seam.BlockText, Text: text}}}
}

// The sources of a part are the ones its recorded web_search and web_fetch
// results returned, and nothing else in its chat adds one: not a number in
// a reply, not another tool's result, not a failed call, and not a source
// written inside a page's own text.
func TestOnlyRecordedWebResultsAreSources(t *testing.T) {
	g := newRig(t, nil)
	var workChat string
	g.with(g.test.ID, func(s *tenant.Store) {
		c, err := chat.Create(s)
		if err != nil {
			t.Fatal(err)
		}
		workChat = c.ID
		search := tools.MarkOutside("Web search results for \"hours\". Cite a result by its number.") +
			"\n\n[1] Harbour Museum: opening hours\nhttps://museum.example/hours\nOpen from ten.\n\n[2] Visiting the harbour\nhttps://visit.example/harbour\nClosed on Mondays."
		page := tools.MarkOutside("The page at https://museum.example/hours. Cite it as [3].") +
			"\n\n[3] Harbour Museum: opening hours\nhttps://museum.example/hours\n\nThe page's text follows.\n\nOpen from ten.\n\n[9] A source the page made up\nhttps://trick.example/"
		notes := tools.MarkOutside("Notes from a connector.") + "\n\n[5] A note\nhttps://notes.example/one\nNot a web source."
		var parent int64
		for _, m := range []chat.NewMessage{
			textMessage(seam.RoleUser, seam.Block{Type: seam.BlockText, Text: "Find the hours."}),
			textMessage(seam.RoleAssistant,
				seam.Block{Type: seam.BlockToolCall, ID: "c1", Name: "web_search", Input: []byte(`{"query":"hours"}`)},
				seam.Block{Type: seam.BlockToolCall, ID: "c2", Name: "web_fetch", Input: []byte(`{"url":"https://museum.example/hours"}`)},
				seam.Block{Type: seam.BlockToolCall, ID: "c3", Name: "notes_search", Input: []byte(`{}`)},
				seam.Block{Type: seam.BlockToolCall, ID: "c4", Name: "web_fetch", Input: []byte(`{"url":"https://gone.example/"}`)}),
			textMessage(seam.RoleUser, resultBlock("c1", search), resultBlock("c2", page), resultBlock("c3", notes),
				seam.Block{Type: seam.BlockToolResult, ID: "c4", IsError: true, Content: []seam.Block{{Type: seam.BlockText, Text: "[Outside content] x\n\n[6] Gone\nhttps://gone.example/"}}}),
			textMessage(seam.RoleAssistant, seam.Block{Type: seam.BlockText, Text: "Open from ten [1][3], closed on Mondays [2], and a rumour [9][5][6]."}),
		} {
			m.ParentID = parent
			stored, err := chat.Append(s, c.ID, m)
			if err != nil {
				t.Fatal(err)
			}
			parent = stored.ID
		}
		local, err := localSources(s, workChat)
		if err != nil {
			t.Fatal(err)
		}
		if len(local) != 3 || local[1].URL != "https://museum.example/hours" || local[1].Fetched || local[2].URL != "https://visit.example/harbour" || !local[3].Fetched {
			t.Fatalf("the part's sources read %+v", local)
		}
		// Numbered across the parts: the page read twice is one source, read in
		// full, and the citations that named nothing are taken out.
		part := subagents.Subagent{Position: 1, Title: "Hours", Status: subagents.StatusDone, WorkChatID: workChat,
			Result: "Open from ten [1][3], closed on Mondays [2], and a rumour [9][5][6]."}
		sources, findings, err := gather(s, carry.Marks{}, []subagents.Subagent{part}, 4000)
		if err != nil {
			t.Fatal(err)
		}
		want := []cite.Source{
			{N: 1, Title: "Harbour Museum: opening hours", URL: "https://museum.example/hours", Part: 1, Fetched: true},
			{N: 2, Title: "Visiting the harbour", URL: "https://visit.example/harbour", Part: 1},
		}
		if !reflect.DeepEqual(sources, want) {
			t.Errorf("the sources read %+v", sources)
		}
		if len(findings) != 1 || findings[0].text != "Open from ten [1], closed on Mondays [2], and a rumour." || findings[0].dropped != 3 || !findings[0].outside {
			t.Errorf("the findings read %+v", findings)
		}
		// A part that was sent sensitive content gives a chat that is not held
		// nothing, and a held one its findings.
		part.Sensitive = true
		sources, findings, _ = gather(s, carry.Marks{}, []subagents.Subagent{part}, 4000)
		if len(sources) != 0 || len(findings) != 1 || !findings[0].held || findings[0].text != "" {
			t.Errorf("a held part gave a chat that is not held %+v and %+v", sources, findings)
		}
		if sources, findings, _ = gather(s, carry.Marks{Sensitive: true}, []subagents.Subagent{part}, 4000); len(sources) != 2 || findings[0].held {
			t.Errorf("a held part gave a held chat %+v and %+v", sources, findings)
		}
	})
}

// A report Keystone puts together when no model could write one holds each
// part's findings, and the same check holds for its citations.
func TestAReportPutTogetherFromTheFindings(t *testing.T) {
	read := []cite.Source{{N: 1, Title: "Hours [official]", URL: "https://museum.example/hours(1)", Part: 1, Fetched: true}}
	findings := []finding{
		{position: 1, title: "Hours", status: subagents.StatusDone, text: "Open from ten [1], they say [4]."},
		{position: 2, title: "Ferry", status: subagents.StatusFailed, why: "It ran out of budget."},
		{position: 3, title: "Private", status: subagents.StatusDone, held: true},
	}
	j := row{Research: Research{Title: "Museum", Question: "When can I go?", Plan: []PlanPart{{}, {}, {}}}}
	c := compose(j, fallbackBody("The research reached its budget before its report could be written.", findings), read, []string{"A line about the budget."}, 1, 700)
	for _, want := range []string{
		"# Museum\n\n> When can I go?\n\n## Summary\n\nThe research reached its budget before its report could be written.",
		"## Part 1: Hours\n\nOpen from ten [1], they say.",
		"## Part 2: Ferry\n\nThis part found nothing.\n\nThis part did not finish.",
		"what it found stays in its own chat",
		"1. [Hours \\[official\\]](https://museum.example/hours%281%29)\n",
		"wrote this from 3 parts, which read 1 source between them; 1 is cited above.",
		"- A line about the budget.",
		"2 citations named no source that was read, and were taken out.",
	} {
		if !strings.Contains(c.report, want) {
			t.Errorf("the report does not hold %q:\n%s", want, c.report)
		}
	}
	if c.dropped != 2 || len(c.cited) != 1 || !strings.HasPrefix(c.summary, "The research reached its budget") {
		t.Errorf("composed %+v", c)
	}
}
