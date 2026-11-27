package research

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/research/cite"
	"github.com/jonathanmkosar/keystone/internal/subagents"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

const (
	museumQuestion = "When can I visit the harbour museum this week, and how do I get there by ferry?"
	hoursTask      = "Find when the harbour museum is open this week."
	ferryTask      = "Find when the ferry to the harbour museum leaves."
	hoursQuery     = "harbour museum opening hours"
	ferryQuery     = "harbour museum ferry timetable"
)

// The pages the stand-in search service knows. Both searches return the
// page about visiting the harbour, so two parts read one page.
var museumPages = map[string][]web.Result{
	hoursQuery: {
		{Title: "Harbour Museum: opening hours", URL: "https://museum.example/hours", Content: "Open from ten in the morning, Tuesday to Sunday."},
		{Title: "Visiting the harbour", URL: "https://visit.example/harbour", Content: "The museum is closed on Mondays. The ferry stops beside it."},
	},
	ferryQuery: {
		{Title: "North ferry timetable", URL: "https://ferry.example/times", Content: "The north ferry leaves at nine and at half past eleven."},
		{Title: "Visiting the harbour", URL: "https://visit.example/harbour", Content: "The museum is closed on Mondays. The ferry stops beside it."},
	},
}

// museumModel is a model that plans two parts, has each search and report
// with citations, one of them to a source that was never read, and writes a
// report that cites three real sources and one that does not exist, with a
// list of sources of its own at the end.
func museumModel() *researcher {
	return &researcher{
		plan: "Here is the plan.\n\n1. Opening hours: " + hoursTask + "\n2. **Getting there**: " + ferryTask + "\n",
		queries: map[string]string{
			hoursTask: hoursQuery,
			ferryTask: ferryQuery,
		},
		findings: map[string]string{
			hoursTask: "The museum opens at ten, Tuesday to Sunday [1], and is closed on Mondays [2]. Its café is said to be famous [7].",
			ferryTask: "The north ferry leaves at nine and at half past eleven [1], and stops beside the museum [2].",
		},
		report: func(string) string {
			return "## Summary\n\nThe harbour museum is open from ten, Tuesday to Sunday [1], and the north ferry, which leaves at nine, stops beside it [3][2].\n\n" +
				"## Opening hours\n\nIt is closed on Mondays [2]. Entry is free on the first Sunday of the month [8].\n\n" +
				"## Getting there\n\nA second ferry leaves at half past eleven [3].\n\n" +
				"## Sources\n\n- [1] A list the model made up\n- [8] A page nobody read\n"
		},
	}
}

// The item's proof: a test-tenant research task produces a cited report
// within its budget. The artifact exists; every citation in it maps to a
// source that a part's recorded search returned; a citation with no such
// source is taken out; the spend is within the budget; and every call is
// recorded.
func TestAResearchTaskProducesACitedReportWithinItsBudget(t *testing.T) {
	g := newRig(t, museumPages)
	model := museumModel()
	sv := g.serve(model)
	chatID := sv.newChat(g.test.ID)

	// What it would run with is shown before it starts.
	var terms Terms
	sv.call(g.test.ID, "GET", "research", "", http.StatusOK, &terms)
	if terms.BudgetMicros != 3_000_000 || terms.Parts != sv.config.Parts || terms.Open != 0 || terms.OpenMax != sv.config.OpenMax || terms.Paused {
		t.Fatalf("the terms read %+v", terms)
	}

	started := sv.start(g.test.ID, chatID, StartBody{Question: museumQuestion})
	if started.Status != StatusQueued && started.Status != StatusRunning || started.BudgetMicros != 3_000_000 || started.WorkChatID == "" || started.WorkChatID == chatID {
		t.Fatalf("the task started as %+v", started)
	}
	got := sv.ends(g.test.ID, chatID, started.ID)
	if got.Status != StatusDone || got.Error != nil || got.AtBudget {
		t.Fatalf("the task ended as %+v", got)
	}

	// The plan was read as two parts, and each was a sub-agent in a chat of
	// its own that searched the stand-in.
	if len(got.Plan) != 2 || got.Plan[0].Title != "Opening hours" || got.Plan[0].Task != hoursTask || got.Plan[1].Title != "Getting there" || got.Plan[1].Task != ferryTask {
		t.Fatalf("the plan reads %+v", got.Plan)
	}
	if len(got.Parts) != 2 {
		t.Fatalf("the task has %d parts", len(got.Parts))
	}
	for i, a := range got.Parts {
		if a.Status != subagents.StatusDone || a.Position != i+1 || a.ChatID != got.WorkChatID || a.WorkChatID == got.WorkChatID || !a.Outside || a.BudgetMicros != 1_050_000 {
			t.Errorf("part %d reads %+v", i+1, a)
		}
	}
	g.search.mu.Lock()
	queries := strings.Join(g.search.queries, "|")
	g.search.mu.Unlock()
	if !strings.Contains(queries, hoursQuery) || !strings.Contains(queries, ferryQuery) {
		t.Errorf("the stand-in was searched for %q", queries)
	}

	// The report step was given the findings in the research's own numbers:
	// the ferry's timetable is source 3, and the page both parts read is one
	// source, 2. The citation the first part made up is gone.
	sent := model.reportMessages()
	if len(sent) != 1 {
		t.Fatalf("the report step was sent %d times", len(sent))
	}
	for _, want := range []string{
		"The research question:\n" + museumQuestion,
		"closed on Mondays [2]. Its café is said to be famous.",
		"leaves at nine and at half past eleven [3], and stops beside the museum [2].",
		"[1] Harbour Museum: opening hours\nhttps://museum.example/hours",
		"[2] Visiting the harbour\nhttps://visit.example/harbour",
		"[3] North ferry timetable\nhttps://ferry.example/times",
	} {
		if !strings.Contains(sent[0], want) {
			t.Errorf("the report step's message does not hold %q:\n%s", want, sent[0])
		}
	}
	if strings.Contains(sent[0], "[7]") || strings.Contains(sent[0], "[4]") {
		t.Errorf("the report step was sent a source nobody read:\n%s", sent[0])
	}

	// The artifact exists, in the chat the research was started from, as
	// Markdown.
	if got.ArtifactID == "" || got.ArtifactChatID != chatID {
		t.Fatalf("the report's artifact is %q in %q", got.ArtifactID, got.ArtifactChatID)
	}
	var report string
	g.with(g.test.ID, func(s *tenant.Store) {
		a, err := artifacts.Get(context.Background(), s, got.ArtifactID)
		if err != nil || a.Type != artifacts.KindMarkdown || a.ChatID != chatID || a.Title != got.Title {
			t.Fatalf("the artifact reads %+v (%v)", a, err)
		}
		if _, report, err = artifacts.Content(context.Background(), s, got.ArtifactID, 1); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("the report:\n%s", report)
	body, list, ok := strings.Cut(report, "\n## Sources\n")
	if !ok {
		t.Fatalf("the report has no list of sources:\n%s", report)
	}

	// Every citation maps to a source a part's recorded search returned.
	if len(got.Sources) != 3 || got.SourcesRead != 3 {
		t.Fatalf("the report cites %d sources of %d read: %+v", len(got.Sources), got.SourcesRead, got.Sources)
	}
	returned := map[string]bool{}
	for _, results := range museumPages {
		for _, r := range results {
			returned[r.URL] = true
		}
	}
	recorded := ""
	for _, a := range got.Parts {
		for _, m := range sv.messages(g.test.ID, a.WorkChatID) {
			recorded += text(m) + "\n"
			for _, b := range m.Blocks {
				for _, c := range b.Content {
					recorded += c.Text + "\n"
				}
			}
		}
	}
	byN := map[int]cite.Source{}
	for i, src := range got.Sources {
		if src.N != i+1 || !returned[src.URL] || !strings.Contains(recorded, src.URL) {
			t.Errorf("source %d is %+v, which no part's recorded search returned", i+1, src)
		}
		byN[src.N] = src
		if !strings.Contains(list, "["+src.Title+"]("+src.URL+")") {
			t.Errorf("the list of sources does not hold %+v:\n%s", src, list)
		}
	}
	cited := cite.Numbers(body)
	if len(cited) != 3 {
		t.Errorf("the report cites %v", cited)
	}
	for _, n := range cited {
		if _, ok := byN[n]; !ok {
			t.Errorf("the report cites [%d], which names no source", n)
		}
	}
	// The numbers follow the order the report cites them in: the museum's
	// hours, the ferry's timetable, the page about visiting.
	if got.Sources[0].URL != "https://museum.example/hours" || got.Sources[1].URL != "https://ferry.example/times" || got.Sources[2].URL != "https://visit.example/harbour" {
		t.Errorf("the sources are numbered %+v", got.Sources)
	}
	if !strings.Contains(body, "stops beside it [2][3].") || !strings.Contains(body, "A second ferry leaves at half past eleven [2].") {
		t.Errorf("the report's citations were not renumbered:\n%s", body)
	}
	// The invented citation is gone, with the model's own list, and counted.
	if strings.Contains(report, "[8]") || strings.Contains(report, "made up") || strings.Contains(report, "nobody read") ||
		!strings.Contains(body, "first Sunday of the month.") {
		t.Errorf("the invented citation or the model's own list is in the report:\n%s", report)
	}
	if got.Dropped != 2 || !strings.Contains(report, "2 citations named no source that was read, and were taken out.") {
		t.Errorf("%d citations were counted as taken out:\n%s", got.Dropped, report)
	}
	if !strings.HasPrefix(report, "# "+got.Title+"\n") || !strings.Contains(report, "## About this report") {
		t.Errorf("the report is not laid out as expected:\n%s", report)
	}

	// The note in the chat carries the summary, with its citations, and says
	// where the report is. The chat is marked as having read outside content.
	msgs := sv.messages(g.test.ID, chatID)
	last := msgs[len(msgs)-1]
	if last.ID != got.NoteMessageID || last.StopReason != turn.StopTask {
		t.Fatalf("the chat's last message is %+v", last)
	}
	note := text(last)
	for _, want := range []string{"Research “" + got.Title + "” is done.", "open from ten, Tuesday to Sunday [1]", "stops beside it [2][3].",
		"saved in this chat as the artifact “" + got.Title + "”", "It cites 3 of the 3 sources the research read", "2 citations that named no source"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not say %q:\n%s", want, note)
		}
	}
	if got.Summary == "" || !strings.Contains(note, got.Summary) {
		t.Errorf("the summary %q is not in the note", got.Summary)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		ctx := context.Background()
		if c := sv.turns.Carried(ctx, s, chatID); c.Outside == "" || c.Sensitive {
			t.Errorf("the chat that got the note is marked %+v", c)
		}
		if c := sv.turns.Carried(ctx, s, got.WorkChatID); c.Outside == "" {
			t.Errorf("the task's own chat is not marked as having read outside content")
		}

		// Within its budget: the task's own run and its parts' together may
		// never spend more than the budget, and did not.
		j, err := get(ctx, s.DB(), got.ID)
		if err != nil {
			t.Fatal(err)
		}
		run, ok, err := unattended.ForRequest(ctx, s.DB(), j.requestID)
		if err != nil || !ok || run.Kind != unattended.KindTask || run.ID != got.ID {
			t.Fatalf("the task is registered as %+v (%v, %v)", run, ok, err)
		}
		total := run.BudgetMicros
		for _, a := range got.Parts {
			total += a.SpentMicros
		}
		if total > got.BudgetMicros || got.SpentMicros > got.BudgetMicros {
			t.Errorf("the task may spend %d and spent %d of a budget of %d", total, got.SpentMicros, got.BudgetMicros)
		}

		// Every call is recorded: the plan and the report under the task's own
		// request id, and each part's model calls and its search under its own.
		own, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindModel, RequestID: j.requestID})
		if err != nil {
			t.Fatal(err)
		}
		if len(own) != 4 || own[0].Outcome != audit.OutcomeStarted || own[1].Outcome != audit.OutcomeOK || own[3].Outcome != audit.OutcomeOK {
			t.Errorf("the task's own model calls are in the audit log as %+v", own)
		}
		searches, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindTool})
		if err != nil {
			t.Fatal(err)
		}
		started, ended := 0, 0
		for _, e := range searches {
			if e.Action != web.SearchName {
				continue
			}
			if e.Outcome == audit.OutcomeStarted {
				started++
			}
			if e.Outcome == audit.OutcomeOK {
				ended++
			}
		}
		if started != 2 || ended != 2 {
			t.Errorf("the parts' searches are in the audit log %d times started and %d ended", started, ended)
		}
		// No memory pass reads the task's own chat.
		if !sv.research.SkipMemory(nil)(ctx, s, got.WorkChatID) || sv.research.SkipMemory(nil)(ctx, s, chatID) {
			t.Errorf("the memory pass's check does not tell the task's own chat from the person's")
		}
	})

	// The chat lists it, from either side, and another person reaches none
	// of it.
	var listed ListReply
	sv.call(g.test.ID, "GET", "chats/"+chatID+"/research", "", http.StatusOK, &listed)
	if len(listed.Research) != 1 || listed.Research[0].ID != got.ID {
		t.Errorf("the chat lists %+v", listed.Research)
	}
	sv.call(g.test.ID, "GET", "chats/"+got.WorkChatID+"/research", "", http.StatusOK, &listed)
	if len(listed.Research) != 1 || listed.Research[0].ID != got.ID {
		t.Errorf("the task's own chat lists %+v", listed.Research)
	}
	sv.call(g.ordinary.ID, "GET", "chats/"+chatID+"/research", "", http.StatusNotFound, nil)
	sv.call(g.ordinary.ID, "GET", "chats/"+chatID+"/research/"+got.ID, "", http.StatusNotFound, nil)
	sv.call(g.ordinary.ID, "POST", "chats/"+chatID+"/research/"+got.ID+"/stop", "", http.StatusNotFound, nil)
	sv.call(g.ordinary.ID, "POST", "chats/"+chatID+"/research", `{"question":"Whose is this?"}`, http.StatusNotFound, nil)
	otherChat := sv.newChat(g.ordinary.ID)
	sv.call(g.ordinary.ID, "GET", "chats/"+otherChat+"/research/"+got.ID, "", http.StatusNotFound, nil)

	// Stopping one that has ended changes nothing.
	var again Research
	sv.call(g.test.ID, "POST", "chats/"+chatID+"/research/"+got.ID+"/stop", "", http.StatusOK, &again)
	if again.Status != StatusDone {
		t.Errorf("a stop after the end left the task %s", again.Status)
	}
}
