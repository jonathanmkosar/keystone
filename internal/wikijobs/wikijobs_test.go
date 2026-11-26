package wikijobs

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/wikijobs/scope"
	"github.com/jonathanmkosar/keystone/settings"
)

// The jobs are the agent kit's: its grants, word for word, and what each
// grant lets a job write.
func TestTheJobsAreTheAgentKits(t *testing.T) {
	kit := map[string]string{
		JobCompile:   "*:read,search,write_pages,delete_pages",
		JobLint:      "*:read,search,write_pages,delete_pages,raise_issues",
		JobUpkeep:    "*:read,search,write_pages,delete_pages,raise_issues,close_issues",
		JobFixIssues: "*:read,search,write_pages,close_issues",
	}
	if len(Jobs()) != 5 {
		t.Fatalf("there are %d jobs", len(Jobs()))
	}
	secrets := map[string]bool{}
	for _, j := range Jobs() {
		tools := JobTools(j)
		if len(tools) != len(j.Tools) {
			t.Errorf("%s: %d of its %d tools are built", j.Name, len(tools), len(j.Tools))
		}
		for _, tl := range tools {
			if !tl.ReadOnly || tl.Inside {
				t.Errorf("%s: %s must only read, and bring content from outside", j.Name, tl.Name)
			}
		}
		if j.Store == StoreBuiltin {
			if j.Secret != "" || j.May != (scope.Actions{Write: true}) {
				t.Errorf("the built-in compile job is %+v", j)
			}
			continue
		}
		if j.Grant != kit[j.Name] {
			t.Errorf("%s's grant is %q, the kit's is %q", j.Name, j.Grant, kit[j.Name])
		}
		has := func(a string) bool { return strings.Contains(j.Grant, a) }
		if j.May != (scope.Actions{Write: has("write_pages"), Delete: has("delete_pages"), Raise: has("raise_issues"), Close: has("close_issues")}) {
			t.Errorf("%s may %+v, and its grant is %s", j.Name, j.May, j.Grant)
		}
		if j.Secret != "knowledge.waymarks."+j.Name || secrets[j.Secret] || !strings.HasPrefix(j.Principal, "keystone-") || !strings.HasPrefix(j.Kit, "agent-kit/jobs/") {
			t.Errorf("%s: secret %s, principal %s, kit %s", j.Name, j.Secret, j.Principal, j.Kit)
		}
		secrets[j.Secret] = true
	}
}

// Every job has its settings, off as shipped, with a schedule that reads,
// and instructions a schedule takes.
func TestEveryJobHasItsSettings(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	c, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	sc, err := schedules.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range Jobs() {
		for _, key := range []string{KeyOn(j.Name), KeyWhen(j.Name), KeyModel(j.Name), KeyBudget(j.Name), KeyPrompt(j.Name)} {
			if _, ok := settings.Lookup(reg, key); !ok {
				t.Errorf("no setting %s", key)
			}
		}
		on, _ := settings.Lookup(reg, KeyOn(j.Name))
		when, _ := settings.Lookup(reg, KeyWhen(j.Name))
		if on.Default != "false" {
			t.Errorf("%s is on as shipped", j.Name)
		}
		if _, err := ParseWhen(when.Default); err != nil {
			t.Errorf("%s: %v", j.Name, err)
		}
		p := c.Prompts[j.Name]
		if n := len([]rune(p)); n > sc.PromptMaxChars-200 {
			t.Errorf("%s's instructions have %d characters, and a schedule takes %d", j.Name, n, sc.PromptMaxChars)
		}
		if !strings.Contains(p, "{max_batches}") || !strings.Contains(p, "```json") {
			t.Errorf("%s's instructions do not say how the answer is written", j.Name)
		}
		if j.Kit != "" && !strings.Contains(p, j.Kit) {
			t.Errorf("%s's instructions do not name %s", j.Name, j.Kit)
		}
	}
	if c.MaxBatches != 20 || c.MaxNewPages != 10 || c.Poll <= 0 {
		t.Errorf("the config is %+v", c)
	}
}

func TestWhenAJobRuns(t *testing.T) {
	good := map[string]schedules.Recurrence{
		"hourly 15":            {Kind: schedules.KindHourly, Minute: 15},
		"daily 02:30":          {Kind: schedules.KindDaily, Time: "02:30"},
		"Weekdays 06:00":       {Kind: schedules.KindWeekdays, Time: "06:00"},
		"weekly mon,thu 03:30": {Kind: schedules.KindWeekly, Days: []string{"mon", "thu"}, Time: "03:30"},
	}
	for text, want := range good {
		got, err := ParseWhen(text)
		if err != nil || got.Kind != want.Kind || got.Time != want.Time || got.Minute != want.Minute || !slices.Equal(got.Days, want.Days) {
			t.Errorf("ParseWhen(%q) = %+v, %v", text, got, err)
		}
	}
	for _, bad := range []string{"", "daily", "daily 25:00", "daily 2:30", "hourly 60", "weekly 03:30", "weekly mon,funday 03:30", "once 2026-12-01 09:00", "sometimes"} {
		if _, err := ParseWhen(bad); !errors.Is(err, errWhen) {
			t.Errorf("ParseWhen(%q) was not refused", bad)
		}
	}
}

func TestTheAnswerIsRead(t *testing.T) {
	p, err := ParseAnswer("I filed it.\n\n```json\n{\"batches\": [{\"note\": \"n\", \"writes\": [{\"space\": \"m\", \"path\": \"a\", \"title\": \"A\", \"body\": \"b\", \"sources\": [7]}]}], \"report\": \"done\"}\n```\n")
	if err != nil || len(p.Batches) != 1 || p.Batches[0].Writes[0].Sources[0] != "7" || p.Report != "done" {
		t.Fatalf("read %+v, %v", p, err)
	}
	if p, err := ParseAnswer(`Nothing to do: {"batches": [], "report": "No changes."}`); err != nil || !p.Empty() {
		t.Errorf("read %+v, %v", p, err)
	}
	for _, bad := range []string{"No JSON here.", `{"batches": [{"notes": "a misspelt part"}]}`, `{"batches": "all"}`, "```json\n{\"tool\": \"send_email\"}\n```"} {
		if _, err := ParseAnswer(bad); !errors.Is(err, errAnswer) {
			t.Errorf("ParseAnswer(%q) was not refused: %v", bad, err)
		}
	}
}

// A wiki run's turns are refused every hosted model, at the gate, and run
// no memory pass; any other chat's are not.
func TestTheGuardsOfAWikiRun(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	g.with(func(s *tenant.Store) {
		run, err := chat.Create(s)
		if err != nil {
			t.Fatal(err)
		}
		other, err := chat.Create(s)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec("INSERT INTO wiki_runs(run_id, job, chat_id, state, at) VALUES('r1', 'compile', ?, 'applied', ?)", run.ID, stamp()); err != nil {
			t.Fatal(err)
		}
		for id, c := range map[string]string{"t-run": run.ID, "t-other": other.ID} {
			if _, err := s.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES(?, ?, 'running', 'rq', 'person', '{}', ?)", id, c, stamp()); err != nil {
				t.Fatal(err)
			}
		}
		trusted := paidModel
		trusted.Name, trusted.Class = "trusted-large", models.ClassTrusted
		var asked []string
		inner := func(ctx context.Context, s *tenant.Store, turnID string, m models.Model, most int64) *seam.Error {
			asked = append(asked, turnID+" "+m.Name)
			return nil
		}
		gate := Gate(inner)
		if e := gate(ctx, s, "t-run", paidModel, 100); e == nil || e.Kind != seam.ErrUnavailable || !strings.Contains(e.Message, "local or trusted") {
			t.Errorf("a hosted model was let through for a wiki run: %+v", e)
		}
		for _, m := range []models.Model{houseModel, trusted} {
			if e := gate(ctx, s, "t-run", m, 100); e != nil {
				t.Errorf("%s was refused for a wiki run: %+v", m.Name, e)
			}
		}
		if e := gate(ctx, s, "t-other", paidModel, 100); e != nil {
			t.Errorf("a hosted model was refused for an ordinary chat: %+v", e)
		}
		if !slices.Equal(asked, []string{"t-run house-small", "t-run trusted-large", "t-other paid-large"}) {
			t.Errorf("the inner gate was asked %v", asked)
		}
		if !SkipMemory(ctx, s, run.ID) || SkipMemory(ctx, s, other.ID) {
			t.Errorf("SkipMemory is wrong")
		}
		if _, ok, err := JobOfChat(ctx, s, other.ID); ok || err != nil {
			t.Errorf("an ordinary chat is a wiki run's: %v", err)
		}
		// In a wiki run's chat the job's tools are offered, and the chat is held.
		offer, runs := Connectors(nil).ForCall(ctx, s, run.ID, nil, func(name string) bool { return name == "search" })
		if len(offer) != len(runs) || len(offer) != len(waymarksPageTools)-1 || runs["search"].Name != "" || runs["read_source"].Name == "" {
			t.Errorf("a wiki run's chat was offered %d tools", len(offer))
		}
		if offer, _ := Connectors(nil).ForCall(ctx, s, other.ID, nil, nil); len(offer) != 0 {
			t.Errorf("an ordinary chat was offered the job's tools")
		}
	})
}

// prepared is a person with the fake Waymarks and the compile job's token,
// and a runner that is not started, for applying answers by hand.
func prepared(t *testing.T) (*rig, *fakeWaymarks, *Runner) {
	g := newRig(t)
	f := newFakeWaymarks(t)
	g.set("knowledge.adapter", "waymarks")
	g.set("knowledge.waymarks_address", f.server.URL)
	g.secret("knowledge.waymarks.compile", f.token("keystone-compile"))
	c, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	return g, f, New(Options{Install: g.install, Config: c})
}

// An answer that asks for more than the job's grant writes nothing at all;
// a write Waymarks refuses drops its batch.
func TestAnAnswerIsCheckedBeforeAnythingIsWritten(t *testing.T) {
	g, f, r := prepared(t)
	f.mu.Lock()
	f.sources = append(f.sources, fakeSource{ID: 1, Space: "memory", Name: "Facts", text: "- " + beehiveFact})
	f.mu.Unlock()
	compile, _ := Get(JobCompile)
	run := finished{runID: "r1", requestID: "rq-1"}
	page := `{"space": "memory", "path": "garden/beehives", "title": "Beehives", "body": "A fact [[source:1]].", "sources": [1]}`
	g.with(func(s *tenant.Store) {
		ctx := context.Background()
		for _, c := range []struct{ name, answer, want string }{
			{"an issue raised by the compile job", `{"batches": [{"note": "n", "writes": [` + page + `]}], "issues": [{"raise": {"page_id": 1, "title": "x"}}]}`, "raises an issue"},
			{"a source the job cannot read", `{"batches": [{"note": "n", "writes": [{"space": "memory", "path": "a", "title": "A", "body": "b", "sources": [99]}]}]}`, "may not cite"},
			{"an id that is not Waymarks'", `{"batches": [{"note": "n", "writes": [{"space": "memory", "path": "a", "title": "A", "body": "b", "sources": ["s1"]}]}]}`, "whole number"},
			{"no JSON", "I could not decide.", "not one JSON object"},
		} {
			state, why, written := r.applyAnswer(ctx, s, compile, run, c.answer)
			if state != stateRefused || !strings.Contains(why, c.want) || len(written) != 0 {
				t.Errorf("%s: %s, %q, %v", c.name, state, why, written)
			}
		}
		for _, e := range f.entries() {
			if strings.HasPrefix(e.Action, "batches.") {
				t.Fatalf("a refused answer opened a batch: %+v", e)
			}
		}
		// A change from a stale revision: the batch is dropped, and nothing
		// after it is written.
		stale := `{"batches": [{"note": "first", "writes": [{"page_id": 1, "base_rev": 9, "space": "wiki", "path": "schema", "title": "How this wiki works", "body": "b", "sources": [1]}]}, {"note": "second", "writes": [` + page + `]}]}`
		state, why, _ := r.applyAnswer(ctx, s, compile, run, stale)
		if state != stateFailed || !strings.Contains(why, "batch 1 (first) was dropped") || !strings.Contains(why, "stale") {
			t.Errorf("a stale write: %s, %q", state, why)
		}
		if len(f.pageList()) != 1 {
			t.Errorf("a page was written after a dropped batch")
		}
		dropped := false
		for _, e := range f.entries() {
			dropped = dropped || e.Action == "batches.drop"
		}
		if !dropped {
			t.Errorf("the batch was not dropped")
		}
	})
}

// The built-in store's compile job turns the memory pass's source into a
// cited page, on the local model.
func TestTheBuiltinStoresCompileJob(t *testing.T) {
	g := newRig(t)
	g.set("knowledge.adapter", StoreBuiltin)
	g.serve()
	if st := g.state(JobBuiltinCompile); st.On || st.Problem != "" || st.HasToken {
		t.Fatalf("as shipped the job reads %+v", st)
	}
	if st := g.state(JobCompile); !strings.Contains(st.Problem, `your knowledge store is "built-in"`) {
		t.Errorf("a Waymarks job on the built-in store reads %+v", st)
	}
	g.tell(beehiveMessage)
	var source string
	g.waitFor("the memory pass to add the fact", func() bool {
		var list []knowledge.Source
		g.with(func(s *tenant.Store) { list, _ = builtin.Uncited(context.Background(), s, 10) })
		if len(list) == 1 && strings.Contains(list[0].Text, "beehives") {
			source = list[0].ID
			return true
		}
		return false
	})
	var run schedules.Run
	g.call("POST", "wiki-jobs/builtin_compile/run", nil, http.StatusCreated, &run)
	st := g.settled(JobBuiltinCompile, run.ID)
	if st.LastWritten.State != stateApplied {
		t.Fatalf("the run was %s: %s", st.LastWritten.State, st.LastWritten.Summary)
	}
	g.with(func(s *tenant.Store) {
		ctx := context.Background()
		ks, err := knowledge.Open(ctx, StoreBuiltin, s)
		if err != nil {
			t.Fatal(err)
		}
		cat, err := ks.Catalog(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var found knowledge.Page
		for _, e := range cat {
			if e.Path == "garden/beehives" {
				found, _ = ks.ReadPage(ctx, e.ID)
			}
		}
		if !strings.Contains(found.Body, beehiveFact) || !strings.Contains(found.Body, "[[source:"+source+"]]") || !slices.Contains(found.Sources, source) {
			t.Errorf("the page is %+v, citing %s", found, source)
		}
		if left, _ := builtin.Uncited(ctx, s, 10); len(left) != 0 {
			t.Errorf("sources are still uncited: %+v", left)
		}
	})
	if n := g.paid.Load(); n != 0 {
		t.Errorf("the hosted model was called %d times", n)
	}
}

// Switched on and off through the routes, a job's schedule follows its
// settings; a change of the settings is carried to it once.
func TestAJobsScheduleFollowsItsSettings(t *testing.T) {
	g := newRig(t)
	g.set("knowledge.adapter", StoreBuiltin)
	g.serve()
	var st JobState
	g.call("POST", "wiki-jobs/builtin_compile/on", nil, http.StatusOK, &st)
	if !st.On || st.ScheduleID == "" || st.Says != "Every day at 02:30" || st.Next == nil || st.BudgetUSD != 0.5 {
		t.Fatalf("switched on: %+v", st)
	}
	id := st.ScheduleID
	g.set(KeyWhen(JobBuiltinCompile), "weekly mon,thu 03:30")
	g.set(KeyBudget(JobBuiltinCompile), "0.25")
	g.wiki.Tick(context.Background())
	var sch schedules.ScheduleReply
	g.call("GET", "schedules/"+id, nil, http.StatusOK, &sch)
	if sch.Schedule.Says != "Mondays and Thursdays at 03:30" || sch.Schedule.BudgetUSD == nil || *sch.Schedule.BudgetUSD != 0.25 || !sch.Schedule.Enabled || sch.Schedule.Model != "house-small" {
		t.Errorf("after the settings changed the schedule is %+v", sch.Schedule)
	}
	// A hosted model named for the job keeps it from running, and pauses it.
	g.set(KeyModel(JobBuiltinCompile), "paid-large")
	g.wiki.Tick(context.Background())
	st = g.state(JobBuiltinCompile)
	g.call("GET", "schedules/"+id, nil, http.StatusOK, &sch)
	if !strings.Contains(st.Problem, "hosted") || sch.Schedule.Enabled || st.Next != nil {
		t.Errorf("with a hosted model the job reads %+v, its schedule %+v", st, sch.Schedule)
	}
	g.call("POST", "wiki-jobs/builtin_compile/run", nil, http.StatusConflict, nil)
	g.set(KeyModel(JobBuiltinCompile), "")
	st = JobState{}
	g.call("POST", "wiki-jobs/builtin_compile/off", nil, http.StatusOK, &st)
	g.call("GET", "schedules/"+id, nil, http.StatusOK, &sch)
	if st.On || st.Problem != "" || sch.Schedule.Enabled || st.ScheduleID != id {
		t.Errorf("switched off: %+v, its schedule %+v", st, sch.Schedule)
	}
	g.call("POST", "wiki-jobs/no_such_job/on", nil, http.StatusNotFound, nil)
}
