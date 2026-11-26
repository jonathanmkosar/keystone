package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/research"
	"github.com/jonathanmkosar/keystone/internal/research/cite"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/subagents"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/settings"
)

// The standing check "research mode" (P8-04). The plan's words: a long
// multi-source report built by sub-agents. The item's proof: a test-tenant
// research task produces a cited report within its budget.
//
// On the real server, put together as internal/app puts it, with the
// shipped settings and prompts, on throwaway data folders. Nothing leaves
// the machine: the search provider is a stand-in served on it, and
// web_fetch is not called.
//
//  1. The test tenant's recorded research
//     (fixtures/test-tenant/turns/research.json) runs: a plan of two parts,
//     a sub-agent for each that searches the stand-in, and a report. The
//     report is a Markdown artifact in the chat it was asked in; every
//     citation in it names a source the stand-in returned to a part's
//     recorded search; the two citations the recording makes up, one in a
//     part's findings and one in the report, are gone and counted; the
//     chat's last message is the note with the summary; the task's own run
//     and its parts could together spend no more than its budget; and its
//     model calls and its parts' searches are in the audit log.
//  2. An invented person's task on a hosted model with a price, given a
//     budget too small for its plan, stops and says so, and the hosted
//     model is never called.
//
// internal/research/cite, the check on citations, reads no setting. The
// research routes are walked with every other route by the tenant
// isolation, calls recorded and no keys in output checks (routes.go).

func init() {
	componentDocs["internal/research"] = "docs/architecture/research-mode.md"
	componentDocs["internal/research/cite"] = "docs/architecture/research-mode.md"
	networkAllowed["internal/research"] = "the research routes, served by the server; it opens no connection, and its parts search through internal/tools/web"
}

// The question of fixtures/test-tenant/turns/research.json: any question
// is answered by the recording, which goes by words of the shipped prompts.
const researchQuestion = "When is the harbour museum open this week, and what is on show there?"

// How long a research task may take in this check.
const researchWaitUp = 30 * time.Second

func checkResearch(string) error {
	problems, err := researchRecorded()
	if err != nil {
		return err
	}
	more, err := researchAtItsBudget()
	if err != nil {
		return err
	}
	if problems = append(problems, more...); len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// researchEnds starts a research task through the API and waits for it to
// end, with its note written and, when it is done, its report saved.
func researchEnds(w *walker, token, chatID string, body research.StartBody) (research.Research, error) {
	raw, _ := json.Marshal(body)
	rec := w.request("POST", api.Prefix+"chats/"+chatID+"/research", token, string(raw), nil)
	var got research.Research
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		return got, fmt.Errorf("a research task cannot be started: status %d: %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(researchWaitUp)
	for {
		rec := w.request("GET", api.Prefix+"chats/"+chatID+"/research/"+got.ID, token, "", nil)
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			return got, fmt.Errorf("a research task cannot be read: status %d", rec.Code)
		}
		over := got.Status == research.StatusDone || got.Status == research.StatusStopped || got.Status == research.StatusFailed
		if over && got.NoteMessageID != 0 && (got.Status != research.StatusDone || got.ArtifactID != "") {
			return got, nil
		}
		if time.Now().After(deadline) {
			return got, fmt.Errorf("waited too long for a research task to end: it is %s, %q", got.Status, got.Progress)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// researchRecorded is step 1.
func researchRecorded() (problems []string, err error) {
	stand := newWebStandIn()
	defer stand.Close()
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	c.Web.SearchEndpoint = stand.URL + "/search"
	reg, err := app.Tools(c)
	if err != nil {
		return nil, err
	}
	r, err := openToolRig(reg)
	if err != nil {
		return nil, err
	}
	defer r.close()
	const key = "an invented search key for verify, not a real one"
	if err := r.test.Secrets().Set(web.KeyName, key); err != nil {
		return nil, err
	}
	ctx := context.Background()
	rec := r.w.request("POST", api.Prefix+"chats", r.token, "", nil)
	var made turn.ChatReply
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil {
		return nil, fmt.Errorf("a chat cannot be made: status %d", rec.Code)
	}
	chatID := made.Chat.ID

	// The budget is shown before a task starts.
	var terms research.Terms
	rec = r.w.request("GET", api.Prefix+"research", r.token, "", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &terms) != nil || terms.BudgetMicros <= 0 || terms.Parts < 1 {
		problems = append(problems, fmt.Sprintf("GET research does not say what a task would run with: status %d, %+v", rec.Code, terms))
	}

	got, err := researchEnds(r.w, r.token, chatID, research.StartBody{Question: researchQuestion})
	if err != nil {
		return nil, err
	}
	if got.Status != research.StatusDone || got.AtBudget || got.BudgetMicros != terms.BudgetMicros {
		return append(problems, fmt.Sprintf("the recorded research ended as %s (%+v), and must be done within its budget", got.Status, got.Error)), nil
	}
	if len(got.Plan) != 2 || len(got.Parts) != 2 {
		problems = append(problems, fmt.Sprintf("the recorded research has %d parts planned and %d sub-agents, and the recording plans two", len(got.Plan), len(got.Parts)))
	}
	seen := map[string]bool{got.WorkChatID: true, chatID: true}
	for _, a := range got.Parts {
		if a.Status != subagents.StatusDone || seen[a.WorkChatID] || !a.Outside {
			problems = append(problems, fmt.Sprintf("the part %q ended as %s in chat %s, and each must finish in a chat of its own that read outside content", a.Title, a.Status, a.WorkChatID))
		}
		seen[a.WorkChatID] = true
	}
	if n := stand.calls.Load(); n != 2 || stand.auth.Load() != "Bearer "+key {
		problems = append(problems, fmt.Sprintf("the stand-in search provider had %d searches, and each of the two parts must search once with the stored key", n))
	}

	// The artifact exists, and every citation maps to a source the stand-in
	// returned to a part's recorded search.
	if got.ArtifactID == "" || got.ArtifactChatID != chatID {
		return append(problems, "the recorded research saved no artifact in the chat it was asked in"), nil
	}
	a, err := artifacts.Get(ctx, r.test, got.ArtifactID)
	if err != nil {
		return nil, err
	}
	_, report, err := artifacts.Content(ctx, r.test, got.ArtifactID, a.Latest)
	if err != nil {
		return nil, err
	}
	if a.Type != artifacts.KindMarkdown || a.ChatID != chatID {
		problems = append(problems, fmt.Sprintf("the report is a %s artifact of chat %s, and must be Markdown in the chat it was asked in", a.Type, a.ChatID))
	}
	returned := map[string]bool{}
	for _, s := range webStandInResults {
		returned[s.URL] = true
	}
	recorded := map[string]bool{}
	for _, part := range got.Parts {
		rec := r.w.request("GET", api.Prefix+"chats/"+part.WorkChatID, r.token, "", nil)
		var kept turn.ChatReply
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &kept) != nil {
			return nil, fmt.Errorf("a part's chat cannot be read back: status %d", rec.Code)
		}
		for _, m := range kept.Messages {
			for _, b := range m.Blocks {
				if b.Type != seam.BlockToolResult || b.IsError {
					continue
				}
				for _, s := range web.Sources(seam.Text(seam.Message{Blocks: b.Content})) {
					recorded[s.URL] = true
				}
			}
		}
	}
	body, list, ok := strings.Cut(report, "\n## Sources\n")
	if !ok {
		problems = append(problems, "the report has no list of sources")
	}
	if len(got.Sources) != len(webStandInResults) || got.SourcesRead != len(webStandInResults) {
		problems = append(problems, fmt.Sprintf("the report cites %d sources of %d read, and the stand-in returned %d", len(got.Sources), got.SourcesRead, len(webStandInResults)))
	}
	byN := map[int]cite.Source{}
	for i, s := range got.Sources {
		if s.N != i+1 || !returned[s.URL] || !recorded[s.URL] {
			problems = append(problems, fmt.Sprintf("source [%d] %s was not returned by a part's recorded search", s.N, s.URL))
		}
		if !strings.Contains(list, "]("+s.URL+")") {
			problems = append(problems, fmt.Sprintf("the report's list of sources does not hold %s", s.URL))
		}
		byN[s.N] = s
	}
	cited := cite.Numbers(body)
	if len(cited) < 2 {
		problems = append(problems, fmt.Sprintf("the report cites %d sources, and the recording cites two that were read", len(cited)))
	}
	for _, n := range cited {
		if _, ok := byN[n]; !ok {
			problems = append(problems, fmt.Sprintf("the report cites [%d], which names no source a part's recorded search returned", n))
		}
	}
	// The recording makes up [6] in a part's findings and [5] in the report.
	if got.Dropped != 2 || strings.Contains(report, "[5]") || strings.Contains(report, "[6]") || !strings.Contains(report, "2 citations named no source that was read") {
		problems = append(problems, fmt.Sprintf("%d invented citations were taken out and counted, and the recording makes up two", got.Dropped))
	}

	// The note is the chat's last message, with the summary, and the chat
	// has now read outside content.
	rec = r.w.request("GET", api.Prefix+"chats/"+chatID, r.token, "", nil)
	var kept turn.ChatReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &kept) != nil || len(kept.Messages) == 0 {
		return nil, fmt.Errorf("the chat of the research cannot be read back: status %d", rec.Code)
	}
	note := kept.Messages[len(kept.Messages)-1]
	said := seam.Text(seam.Message{Blocks: note.Blocks})
	if note.ID != got.NoteMessageID || note.StopReason != turn.StopTask || got.Summary == "" || !strings.Contains(said, got.Summary) || !strings.Contains(said, "saved in this chat as the artifact") {
		problems = append(problems, "the chat's last message is not the research's note with its summary and where its report is")
	}
	var marked int
	if err := r.test.DB().QueryRow("SELECT count(*) FROM tool_outside WHERE chat_id = ?", chatID).Scan(&marked); err != nil {
		return nil, err
	}
	if marked == 0 {
		problems = append(problems, "the chat that got the research's note is not marked as having read outside content")
	}

	// Within its budget: what the task's own run may spend, and what its
	// parts spent, are together no more than the budget.
	var requestID string
	if err := r.test.DB().QueryRow("SELECT request_id FROM research WHERE id = ?", got.ID).Scan(&requestID); err != nil {
		return nil, err
	}
	run, ok, err := unattended.ForRequest(ctx, r.test.DB(), requestID)
	if err != nil {
		return nil, err
	}
	total := run.BudgetMicros
	for _, part := range got.Parts {
		total += part.SpentMicros
	}
	if !ok || total > got.BudgetMicros || got.SpentMicros > got.BudgetMicros {
		problems = append(problems, fmt.Sprintf("the task and its parts may spend %d and spent %d, of a budget of %d", total, got.SpentMicros, got.BudgetMicros))
	}

	// Every call is recorded: the plan and the report under the task's own
	// request id, and each part's search before and after.
	own, err := r.entriesOf(audit.KindModel, requestID)
	if err != nil {
		return nil, err
	}
	if len(own) != 4 {
		problems = append(problems, fmt.Sprintf("the task's plan and report left %d audit entries under its request id, want one before and one after each", len(own)))
	}
	searches, err := audit.List(ctx, r.test, audit.Filter{Kind: audit.KindTool})
	if err != nil {
		return nil, err
	}
	began, ended := 0, 0
	for _, e := range searches {
		if e.Action != web.SearchName {
			continue
		}
		if e.Outcome == audit.OutcomeStarted {
			began++
		}
		if e.Outcome == audit.OutcomeOK {
			ended++
		}
		if strings.Contains(e.Sent+e.Received, key) {
			problems = append(problems, "the search key is in the audit log")
		}
	}
	if began != 2 || ended != 2 {
		problems = append(problems, fmt.Sprintf("the parts' searches are in the audit log %d times before and %d after, want two of each", began, ended))
	}
	p, err := unrecordedCalls(ctx, r.test, "the test tenant with a research task")
	if err != nil {
		return nil, err
	}
	return append(problems, p...), nil
}

// researchAtItsBudget is step 2.
func researchAtItsBudget() (problems []string, err error) {
	dir, err := os.MkdirTemp("", "keystone-research-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	paid := models.Model{
		Name: "paid-example", Label: "Paid Example", Adapter: models.AdapterAnthropic, Endpoint: "https://models.example",
		ProviderModel: "example-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassHosted, Price: models.Price{Input: 2, Output: 10},
		Capabilities: []models.Capability{models.CapTools},
	}
	person, err := in.Create("Research Person One")
	if err != nil {
		return nil, err
	}
	so, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	lim, err := adapters.Limits(reg)
	if err != nil {
		return nil, err
	}
	s, err := in.Store(person.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if err := signin.SetPassword(s, walkPasswordOne, so); err != nil {
		return nil, err
	}
	if err := models.Add(ctx, s, lim, paid); err != nil {
		return nil, err
	}
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, "chats.default_model", paid.Name); err != nil {
		return nil, err
	}
	// The person's own budget for a research task, a thousandth of a cent.
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, research.KeyBudget, "0.00001"); err != nil {
		return nil, err
	}
	var paidCalls atomic.Int32
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	c.Adapters = adapters.Set{
		models.AdapterAnthropic: func(models.Model, string) (seam.Provider, error) {
			return tallied{newWaiting("1. Hours: Find the museum's hours."), &paidCalls}, nil
		},
	}
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	token, rec := w.signIn(person.ID, walkPasswordOne)
	if token == "" {
		return nil, fmt.Errorf("the person cannot sign in: status %d", rec.Code)
	}
	rec = w.request("POST", api.Prefix+"chats", token, "", nil)
	var made turn.ChatReply
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil {
		return nil, fmt.Errorf("a chat cannot be made: status %d", rec.Code)
	}
	got, err := researchEnds(w, token, made.Chat.ID, research.StartBody{Question: researchQuestion})
	if err != nil {
		return nil, err
	}
	if got.Status != research.StatusFailed || got.Error == nil || got.Error.Code != string(seam.ErrSpendCap) || !got.AtBudget ||
		!strings.Contains(got.Error.Message, "stopped at its budget") || !strings.Contains(got.Error.Message, "budget of $0.00") ||
		got.BudgetMicros != 10 || got.SpentMicros != 0 || paidCalls.Load() != 0 {
		problems = append(problems, fmt.Sprintf("a research task with no budget for its plan ended as %s (%+v) after %d paid calls, and must stop at its budget and say so without calling the paid model",
			got.Status, got.Error, paidCalls.Load()))
	}
	rec = w.request("GET", api.Prefix+"chats/"+made.Chat.ID, token, "", nil)
	var kept turn.ChatReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &kept) != nil || len(kept.Messages) == 0 {
		return nil, fmt.Errorf("the chat of the research cannot be read back: status %d", rec.Code)
	}
	if said := seam.Text(seam.Message{Blocks: kept.Messages[len(kept.Messages)-1].Blocks}); !strings.Contains(said, "did not finish") || !strings.Contains(said, "stopped at its budget") {
		problems = append(problems, "the chat of a research task that stopped at its budget was not told why")
	}
	p, err := unrecordedCalls(ctx, s, "the person whose research stopped at its budget")
	if err != nil {
		return nil, err
	}
	return append(problems, p...), nil
}
