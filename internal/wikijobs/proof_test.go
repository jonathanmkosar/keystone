package wikijobs

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// underTest is a Waymarks the proof runs against: the fake on this
// machine, or a real one started from the binary (real_test.go).
type underTest struct {
	address string
	// tokens are the invented principals' tokens, by name. They live in
	// memory for one test and are never written anywhere.
	tokens  map[string]string
	audit   func() []fakeEntry
	pages   func() []fakePage
	sources func() []fakeSource
}

// The proof of the item, against the fake Waymarks: it always runs.
func TestAFactToldInChatBecomesACitedPageOnTheFakeWaymarks(t *testing.T) {
	f := newFakeWaymarks(t)
	tokens := map[string]string{}
	for _, name := range []string{"keeper", "keystone-reader", "keystone-intake", "keystone-compile"} {
		tokens[name] = f.token(name)
	}
	proveCompile(t, underTest{address: f.server.URL, tokens: tokens, audit: f.entries, pages: f.pageList, sources: f.sourceList})
}

// proveCompile is the item's proof: a fact told in chat appears on a cited
// page after the next compile run, and Waymarks' audit shows each job under
// its own token.
func proveCompile(t *testing.T, wm underTest) { proveCompileFrom(t, wm, 1) }

// proveCompileFrom is proveCompile on a Waymarks that holds before pages
// to start with.
func proveCompileFrom(t *testing.T, wm underTest, before int) {
	g := newRig(t)
	g.set("knowledge.adapter", "waymarks")
	g.set("knowledge.waymarks_address", wm.address)
	g.secret("knowledge.waymarks.reader", wm.tokens["keystone-reader"])
	g.secret("knowledge.waymarks.intake", wm.tokens["keystone-intake"])
	g.serve()

	// Off as shipped, and it cannot run before its own token is stored.
	st := g.state(JobCompile)
	if st.On || st.HasToken || !strings.Contains(st.Problem, "knowledge.waymarks.compile") || st.ScheduleID != "" {
		t.Fatalf("before its token the job reads %+v", st)
	}
	g.call("POST", "wiki-jobs/compile/run", nil, http.StatusConflict, nil)

	// 1. A fact told in chat: the memory pass adds it as a source, under the
	// intake token.
	personChat := g.tell(beehiveMessage)
	var sourceID string
	g.waitFor("the memory pass to add the fact", func() bool {
		for _, s := range wm.sources() {
			if s.Space == "memory" {
				sourceID = strconv.FormatInt(s.ID, 10)
				return true
			}
		}
		return false
	})

	// 2. The job's own token, then the job switched on and run now.
	g.secret("knowledge.waymarks.compile", wm.tokens["keystone-compile"])
	st = JobState{}
	g.call("POST", "wiki-jobs/compile/on", nil, http.StatusOK, &st)
	if !st.On || st.Problem != "" || !st.HasToken || st.ScheduleID == "" || st.Next == nil || st.Model != "house-small" || st.Says == "" {
		t.Fatalf("switched on, the job reads %+v", st)
	}
	var run schedules.Run
	g.call("POST", "wiki-jobs/compile/run", nil, http.StatusCreated, &run)
	st = g.settled(JobCompile, run.ID)
	if st.LastWritten.State != stateApplied {
		t.Fatalf("the run was %s: %s", st.LastWritten.State, st.LastWritten.Summary)
	}

	// 3. The fact is on a cited page, and the two new pages link to each
	// other by the ids Waymarks gave them.
	var hives, garden *fakePage
	for _, p := range wm.pages() {
		switch p.Path {
		case "garden/beehives":
			hives = &p
		case "garden":
			garden = &p
		}
	}
	if hives == nil || garden == nil {
		t.Fatalf("the pages are %+v", wm.pages())
	}
	n, _ := strconv.ParseInt(sourceID, 10, 64)
	if !strings.Contains(hives.Body, beehiveFact) || !strings.Contains(hives.Body, "[[source:"+sourceID+"]]") || !slices.Contains(hives.Sources, n) || hives.Space != "memory" {
		t.Errorf("the beehives page is %+v, citing source %s", *hives, sourceID)
	}
	if !strings.Contains(hives.Body, "[[page:"+strconv.FormatInt(garden.ID, 10)+"|the garden]]") || !strings.Contains(garden.Body, "[[page:"+strconv.FormatInt(hives.ID, 10)+"]]") || strings.Contains(hives.Body+garden.Body, "[[new:") {
		t.Errorf("the links were not given their ids: %q and %q", hives.Body, garden.Body)
	}

	// 4. Waymarks' audit shows each job under its own token.
	by := map[string]map[string]bool{}
	for _, e := range wm.audit() {
		if e.Outcome != "ok" {
			continue
		}
		if by[e.Actor] == nil {
			by[e.Actor] = map[string]bool{}
		}
		by[e.Actor][strings.TrimPrefix(e.Action, "api.")] = true // a real Waymarks names each call api.<route>
	}
	if !by["keystone-intake"]["sources.add"] {
		t.Errorf("the memory pass's source is not under the intake token: %v", by)
	}
	for _, action := range []string{"batches.open", "batches.write", "batches.close"} {
		if !by["keystone-compile"][action] {
			t.Errorf("%s is not under the compile job's token: %v", action, by)
		}
		for actor, did := range by {
			if actor != "keystone-compile" && did[action] {
				t.Errorf("%s did %s", actor, action)
			}
		}
	}
	if !by["keystone-compile"]["sources.text"] || !by["keystone-compile"]["sources.list"] {
		t.Errorf("the job did not read the sources under its own token: %v", by["keystone-compile"])
	}
	if by["keystone-intake"]["sources.text"] || by["keystone-reader"]["sources.add"] {
		t.Errorf("a token did what its role does not: %v", by)
	}

	// 5. Keystone's side.
	g.with(func(s *tenant.Store) {
		ctx := context.Background()
		// Fixed rule 3: the run's chat is held, and no hosted model was sent
		// anything, though one is in the person's fallback chain.
		if held, err := recall.Held(ctx, s, run.ChatID); err != nil || !held {
			t.Errorf("the run's chat is not held: %v %v", held, err)
		}
		if n := g.paid.Load(); n != 0 {
			t.Errorf("the hosted model was called %d times", n)
		}
		// No memory pass read the run's chat; the person's chat had one.
		if passes, err := memory.Passes(ctx, s, run.ChatID); err != nil || len(passes) != 0 {
			t.Errorf("the run's chat had memory passes: %+v %v", passes, err)
		}
		if passes, _ := memory.Passes(ctx, s, personChat); len(passes) == 0 {
			t.Errorf("the person's chat had no memory pass")
		}
		// Fixed rule 4: every model call and tool call of the run, and every
		// write, is in the audit log under the run's request id.
		var requestID string
		if err := s.DB().QueryRowContext(ctx, "SELECT request_id FROM jobs WHERE id = ?", run.TaskID).Scan(&requestID); err != nil {
			t.Fatal(err)
		}
		entries, err := audit.List(ctx, s, audit.Filter{RequestID: requestID})
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]int{}
		for _, e := range entries {
			if e.Outcome == audit.OutcomeStarted {
				continue
			}
			seen[string(e.Kind)+" "+e.Action]++
			if e.Kind == audit.KindModel && e.Action != "house-small" {
				t.Errorf("a call of the run went to %s", e.Action)
			}
			for _, tok := range wm.tokens {
				if strings.Contains(e.Sent+e.Received, tok) {
					t.Errorf("a token is in the audit log, in %s", e.Action)
				}
			}
		}
		for _, want := range []string{"model house-small", "tool list_sources", "tool read_source", "tool waymarks_open_batch", "tool waymarks_write_page", "tool waymarks_close_batch"} {
			if seen[want] == 0 {
				t.Errorf("no %q in the audit log: %v", want, seen)
			}
		}
		if seen["tool waymarks_write_page"] != 3 {
			t.Errorf("the pages were written %d times, want 3 (the first again with its link)", seen["tool waymarks_write_page"])
		}
	})
	// The run's chat shows what it did: its steps, its answer, and the note.
	var c turn.ChatReply
	g.call("GET", "chats/"+run.ChatID, nil, http.StatusOK, &c)
	last := c.Messages[len(c.Messages)-1]
	said := ""
	for _, b := range last.Blocks {
		said += b.Text
	}
	if !strings.Contains(said, "Keystone wrote what") || !strings.Contains(said, "keystone-compile") || !strings.Contains(said, "garden/beehives") {
		t.Errorf("the run's chat ends with %q", said)
	}

	// A second run finds nothing to compile and writes nothing.
	run = schedules.Run{}
	g.call("POST", "wiki-jobs/compile/run", nil, http.StatusCreated, &run)
	st = g.settled(JobCompile, run.ID)
	if st.LastWritten.State != stateApplied || len(wm.pages()) != before+2 {
		t.Errorf("the second run was %+v, and there are %d pages", st.LastWritten, len(wm.pages()))
	}
}
