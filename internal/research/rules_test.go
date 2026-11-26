package research

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/subagents"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

func ptr(f float64) *float64 { return &f }

// send sends a message in a chat and reads the turn's stream to its end.
func (sv *served) send(tenantID, chatID, text string) {
	sv.g.t.Helper()
	resp := sv.request(tenantID, "POST", "chats/"+chatID+"/turns", `{"text":"`+text+`"}`)
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
}

// hold marks a chat as one that was sent content from a sensitive space,
// under its first turn.
func (sv *served) hold(tenantID, chatID string) {
	sv.g.t.Helper()
	sv.g.with(tenantID, func(s *tenant.Store) {
		var turnID string
		if err := s.DB().QueryRow("SELECT id FROM turns WHERE chat_id = ? ORDER BY seq LIMIT 1", chatID).Scan(&turnID); err != nil {
			sv.g.t.Fatal(err)
		}
		if err := recall.Hold(context.Background(), s, turnID, chatID); err != nil {
			sv.g.t.Fatal(err)
		}
	})
}

// onePart is a model whose plan is one part, the opening hours.
func onePart() *researcher {
	m := museumModel()
	m.plan = "1. Opening hours: " + hoursTask
	m.report = func(string) string {
		return "## Summary\n\nThe harbour museum is open from ten, Tuesday to Sunday [1].\n\n## Details\n\nIt is closed on Mondays [2]."
	}
	return m
}

// A task at its budget stops and says so. With a budget too small for its
// plan it ends with nothing and says why, and the paid model is never
// called. With a budget that its plan and its part fit in and its report
// does not, it stops with what it has: the report is put together from the
// part's findings, cited and checked like any other, and the report and the
// note say it stopped at its budget. With a local model in the person's
// chain it goes on with that, and says which model wrote the report.
func TestAResearchTaskAtItsBudgetStopsAndSaysSo(t *testing.T) {
	g := newRig(t, museumPages)
	g.config = func(c *Config) { c.ReportSharePercent = 10 }
	paid, house := onePart(), onePart()
	// The part reads a great deal: its findings cost five cents of the six.
	paid.found = &seam.Usage{InputTokens: 20_000, OutputTokens: 1000}
	g.extra = adapters.Set{
		models.AdapterAnthropic:       func(models.Model, string) (seam.Provider, error) { return paid, nil },
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return house, nil },
	}
	g.addModels(paidModel, houseModel)
	g.set(g.ordinary.ID, tenantsettings.Person, "chats.default_model", paidModel.Name)
	sv := g.serve(nil)
	who := g.ordinary.ID

	// Too small for its plan: nothing is sent to the paid model.
	chatID := sv.newChat(who)
	none := sv.ends(who, chatID, sv.start(who, chatID, StartBody{Question: museumQuestion, BudgetUSD: ptr(0.00001)}).ID)
	if none.Status != StatusFailed || none.Error == nil || none.Error.Code != string(seam.ErrSpendCap) || !none.AtBudget ||
		!strings.Contains(none.Error.Message, "stopped at its budget before it found anything") || !strings.Contains(none.Error.Message, "budget of $0.00") ||
		paid.calls.Load() != 0 || none.ArtifactID != "" || none.SpentMicros != 0 {
		t.Fatalf("a task with no budget for its plan ended as %+v (%+v) after %d paid calls", none, none.Error, paid.calls.Load())
	}
	msgs := sv.messages(who, chatID)
	if note := text(msgs[len(msgs)-1]); !strings.Contains(note, "did not finish") || !strings.Contains(note, "stopped at its budget") {
		t.Errorf("the note of a task with no budget says %q", note)
	}

	// Enough for its plan and its part, not for its report: it stops with
	// what it has.
	chatID = sv.newChat(who)
	got := sv.ends(who, chatID, sv.start(who, chatID, StartBody{Question: museumQuestion, BudgetUSD: ptr(0.06)}).ID)
	plans, parts, reports := paid.count()
	if got.Status != StatusDone || !got.AtBudget || got.BudgetMicros != 60_000 || plans != 1 || parts != 2 || reports != 0 {
		t.Fatalf("a task whose report its budget refused ended as %+v, after %d plan, %d part and %d report calls", got, plans, parts, reports)
	}
	if got.SpentMicros <= 0 || got.SpentMicros > got.BudgetMicros {
		t.Errorf("it spent %d of a budget of %d", got.SpentMicros, got.BudgetMicros)
	}
	if len(got.Parts) != 1 || got.Parts[0].Status != subagents.StatusDone || got.Parts[0].BudgetMicros != 54_000 {
		t.Errorf("its part reads %+v", got.Parts)
	}
	var report string
	g.with(who, func(s *tenant.Store) {
		var err error
		if _, report, err = artifacts.Content(context.Background(), s, got.ArtifactID, 1); err != nil {
			t.Fatal(err)
		}
		// The runs together could never have spent more than the budget.
		j, err := get(context.Background(), s.DB(), got.ID)
		if err != nil {
			t.Fatal(err)
		}
		run, _, err := unattended.ForRequest(context.Background(), s.DB(), j.requestID)
		if err != nil || run.BudgetMicros+got.Parts[0].SpentMicros != got.BudgetMicros {
			t.Errorf("the task's own run may spend %d beside its part's %d, of %d (%v)", run.BudgetMicros, got.Parts[0].SpentMicros, got.BudgetMicros, err)
		}
	})
	for _, want := range []string{
		"The research reached its budget before its report could be written.",
		"## Part 1: Opening hours",
		"The museum opens at ten, Tuesday to Sunday [1], and is closed on Mondays [2]. Its café is said to be famous.",
		"1. [Harbour Museum: opening hours](https://museum.example/hours)",
		"The research reached its budget of $0.06, having spent $0.06",
		"1 citation named no source that was read, and was taken out.",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report of a task that stopped at its budget does not say %q:\n%s", want, report)
		}
	}
	msgs = sv.messages(who, chatID)
	if note := text(msgs[len(msgs)-1]); !strings.Contains(note, "is done. The research reached its budget of $0.06") || !strings.Contains(note, "this report is what it had found") {
		t.Errorf("the note of a task that stopped at its budget says %q", note)
	}

	// With a local model in the chain, the report is written by it, and the
	// task says so.
	g.set(who, tenantsettings.Person, "routing.fallback_models", houseModel.Name)
	chatID = sv.newChat(who)
	got = sv.ends(who, chatID, sv.start(who, chatID, StartBody{Question: museumQuestion, BudgetUSD: ptr(0.06)}).ID)
	_, _, reports = paid.count()
	_, _, local := house.count()
	if got.Status != StatusDone || !got.AtBudget || reports != 0 || local != 1 || !strings.Contains(got.Notice, "written by "+houseModel.Name) {
		t.Fatalf("with a local model in the chain the task ended as %+v, after %d paid and %d local report calls", got, reports, local)
	}
	if got.SpentMicros > got.BudgetMicros || len(got.Sources) != 2 || !strings.Contains(got.Summary, "open from ten, Tuesday to Sunday [1]") {
		t.Errorf("the report a local model wrote reads %+v", got)
	}

	// A budget that is not a number of dollars is refused.
	sv.call(who, "POST", "chats/"+chatID+"/research", `{"question":"Anything?","budget_usd":-1}`, http.StatusBadRequest, nil)
}

// A chat held for sensitive content runs research only on models that may
// read it, and its web tools follow the held-chat rule: the task's own chat
// and its part's are held, the hosted model is never called, nothing is
// searched, and the report is a sensitive artifact of the held chat.
func TestAHeldChatsResearchStaysWithModelsThatMayReadIt(t *testing.T) {
	g := newRig(t, museumPages)
	var hosted atomic.Int32
	house := onePart()
	g.extra = adapters.Set{
		models.AdapterAnthropic: func(models.Model, string) (seam.Provider, error) {
			return counting{"From the hosted model.", &hosted}, nil
		},
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return house, nil },
	}
	g.addModels(paidModel, houseModel)
	g.set(g.ordinary.ID, tenantsettings.Person, "chats.default_model", paidModel.Name)
	g.set(g.ordinary.ID, tenantsettings.Person, "routing.fallback_models", houseModel.Name)
	sv := g.serve(nil)
	who := g.ordinary.ID

	chatID := sv.newChat(who)
	sv.send(who, chatID, "Good morning.") // a first turn to hold the chat under
	sv.hold(who, chatID)
	before := hosted.Load()
	if before == 0 {
		t.Fatalf("the chat's own turn did not reach the hosted model")
	}

	got := sv.ends(who, chatID, sv.start(who, chatID, StartBody{Question: museumQuestion}).ID)
	if got.Status != StatusDone || got.ArtifactChatID != chatID {
		t.Fatalf("a held chat's research ended as %+v (%+v)", got, got.Error)
	}
	if hosted.Load() != before {
		t.Errorf("the hosted model was called %d times by a held chat's research", hosted.Load()-before)
	}
	if plans, parts, reports := house.count(); plans != 1 || parts != 2 || reports != 1 {
		t.Errorf("the local model made %d plan, %d part and %d report calls", plans, parts, reports)
	}
	g.search.mu.Lock()
	searched := len(g.search.queries)
	g.search.mu.Unlock()
	if searched != 0 {
		t.Errorf("a held chat's research searched the web %d times", searched)
	}
	g.with(who, func(s *tenant.Store) {
		ctx := context.Background()
		for _, id := range []string{got.WorkChatID, got.Parts[0].WorkChatID, chatID} {
			if on, err := recall.Held(ctx, s, id); err != nil || !on {
				t.Errorf("chat %s of a held chat's research is not held (%v)", id, err)
			}
		}
		a, err := artifacts.Get(ctx, s, got.ArtifactID)
		if err != nil || !a.Sensitive {
			t.Errorf("the report of a held chat's research reads %+v (%v)", a, err)
		}
	})
	// The part's search was refused, so nothing was read, and no citation
	// stands.
	if len(got.Sources) != 0 || got.SourcesRead != 0 || got.Dropped == 0 || strings.Contains(got.Summary, "[1]") {
		t.Errorf("with nothing read the report cites %+v and %d citations were taken out: %q", got.Sources, got.Dropped, got.Summary)
	}
	own := sv.messages(who, got.Parts[0].WorkChatID)
	refused := false
	for _, m := range own {
		for _, b := range m.Blocks {
			for _, c := range b.Content {
				refused = refused || strings.Contains(c.Text, "Web search is off in this chat")
			}
		}
	}
	if !refused {
		t.Errorf("the part's search was not refused as a held chat's is")
	}
}

// A task whose own chat was sent sensitive content, started from a chat
// that is not held, keeps its report and its summary in its own chat: the
// chat it was started from is told so, in Keystone's words alone.
func TestAHeldTasksReportStaysInItsOwnChat(t *testing.T) {
	g := newRig(t, museumPages)
	model := onePart()
	sv := g.serve(model)
	chatID := sv.newChat(g.test.ID)
	var workChat atomic.Value
	// The task's own chat is held while its part works.
	model.block = func(ctx context.Context, step, _ string) error {
		if step != "part" {
			return nil
		}
		id, _ := workChat.Load().(string)
		g.with(g.test.ID, func(s *tenant.Store) {
			var turnID string
			if err := s.DB().QueryRow("SELECT id FROM turns WHERE chat_id = ? ORDER BY seq LIMIT 1", id).Scan(&turnID); err != nil {
				t.Error(err)
				return
			}
			if err := recall.Hold(ctx, s, turnID, id); err != nil {
				t.Error(err)
			}
		})
		return nil
	}
	started := sv.start(g.test.ID, chatID, StartBody{Question: museumQuestion})
	workChat.Store(started.WorkChatID)
	got := sv.ends(g.test.ID, chatID, started.ID)
	if got.Status != StatusDone || got.ArtifactChatID != got.WorkChatID || got.ArtifactID == "" {
		t.Fatalf("a held task ended as %+v", got)
	}
	msgs := sv.messages(g.test.ID, chatID)
	note := text(msgs[len(msgs)-1])
	if !strings.Contains(note, "its report stays in its own chat") || strings.Contains(note, "open from ten") || strings.Contains(note, "Mondays") {
		t.Errorf("the chat that is not held was told %q", note)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		ctx := context.Background()
		if on, err := recall.Held(ctx, s, chatID); err != nil || on {
			t.Errorf("the chat the task was started from is held (%v)", err)
		}
		a, err := artifacts.Get(ctx, s, got.ArtifactID)
		if err != nil || a.ChatID != got.WorkChatID || !a.Sensitive {
			t.Errorf("the held task's report reads %+v (%v)", a, err)
		}
		if c := sv.turns.Carried(ctx, s, chatID); c.Outside != "" {
			t.Errorf("the chat that got only Keystone's words is marked as having read outside content")
		}
	})
}
