package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// pauseBound is the most the pause route may take to stop every running
// task: the item's "within seconds".
const pauseBound = 5 * time.Second

// blocking is a provider that, while block is on, writes the start of a
// reply and then waits until its call is cancelled, as a model in the
// middle of a long answer; with block off it answers at once. It counts the
// calls it is in the middle of, and the calls that were cancelled.
type blocking struct {
	block     atomic.Bool
	calls     atomic.Int32
	inFlight  atomic.Int32
	cancelled atomic.Int32
	reply     string
}

func newBlocking(reply string) *blocking {
	p := &blocking{reply: reply}
	p.block.Store(true)
	return p
}

func (p *blocking) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	p.calls.Add(1)
	if !p.block.Load() {
		return answers{p.reply, &atomic.Int32{}}.Stream(ctx, req, emit)
	}
	p.inFlight.Add(1)
	defer p.inFlight.Add(-1)
	for _, e := range []seam.Event{
		{Type: seam.EventStart, Model: req.Model},
		{Type: seam.EventBlockStart, Index: 0, Block: &seam.Block{Type: seam.BlockText}},
		{Type: seam.EventBlockDelta, Index: 0, Text: "Counting the boats along the"},
	} {
		if err := emit(e); err != nil {
			return err
		}
	}
	<-ctx.Done()
	p.cancelled.Add(1)
	return ctx.Err()
}

// The item's proof of the switch: it stops every running task of the
// person's within seconds, through the model call's context, and marks each
// paused with a note, not failed; every call, the cancelled ones too, is
// recorded; a task started meanwhile is held; another person's task goes
// on; and switched off, the paused tasks go on with a new round and the
// held one starts.
func TestThePauseSwitchStopsEveryRunningTask(t *testing.T) {
	g := newRig(t)
	mine := newBlocking("The boats are counted.")
	theirs := newBlocking("The buoys are counted.")
	g.extra = adapters.Set{models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return theirs, nil }}
	g.addModels(houseModel)
	g.set(g.ordinary.ID, tenantsettings.Person, "chats.default_model", houseModel.Name)
	sv := g.serve(mine)

	parent := sv.newChat(g.test.ID)
	boats := sv.start(g.test.ID, parent, "Count the boats in the harbour.", "Boats")
	gulls := sv.start(g.test.ID, parent, "Count the gulls on the pier.", "Gulls")
	other := sv.newChat(g.ordinary.ID)
	buoys := sv.start(g.ordinary.ID, other, "Count the buoys in the bay.", "Buoys")
	g.waitFor("every task's model call to be under way", func() bool { return mine.inFlight.Load() == 2 && theirs.inFlight.Load() == 1 })

	began := time.Now()
	var paused unattended.SwitchReply
	sv.call(g.test.ID, "POST", "agents/pause", "", http.StatusOK, &paused)
	took := time.Since(began)
	if !paused.Paused || paused.Working != 0 || paused.Held != 2 || took > pauseBound {
		t.Fatalf("the pause answered %+v after %v", paused, took)
	}
	if mine.inFlight.Load() != 0 || mine.cancelled.Load() != 2 {
		t.Fatalf("after the pause %d of the person's model calls are under way and %d were cancelled", mine.inFlight.Load(), mine.cancelled.Load())
	}
	if theirs.inFlight.Load() != 1 || theirs.cancelled.Load() != 0 {
		t.Fatalf("one person's switch reached another's task: %d under way, %d cancelled", theirs.inFlight.Load(), theirs.cancelled.Load())
	}
	var otherSwitch unattended.SwitchReply
	sv.call(g.ordinary.ID, "GET", "agents", "", http.StatusOK, &otherSwitch)
	if otherSwitch.Paused || otherSwitch.Working != 1 {
		t.Fatalf("the other person's switch reads %+v", otherSwitch)
	}

	for _, task := range []Task{boats, gulls} {
		var got Task
		sv.call(g.test.ID, "GET", "chats/"+parent+"/tasks/"+task.ID, "", http.StatusOK, &got)
		if got.Status != StatusPaused || got.Error != nil || !strings.HasPrefix(got.Progress, "Paused") {
			t.Fatalf("a paused task reads %+v", got)
		}
		g.waitFor("the pause note in the task's chat", func() bool {
			msgs := sv.messages(g.test.ID, task.WorkChatID)
			return len(msgs) > 0 && strings.Contains(text(msgs[len(msgs)-1]), "every agent was paused")
		})
		// Every call is recorded, the cancelled one too: its started and end
		// entries, and a usage record that says it was stopped.
		j := g.task(g.test.ID, task.ID)
		g.with(g.test.ID, func(s *tenant.Store) {
			entries, err := audit.List(context.Background(), s, audit.Filter{Kind: audit.KindModel, RequestID: j.requestID})
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 || entries[0].Outcome != audit.OutcomeStarted || entries[1].Outcome != audit.OutcomeOK || !strings.Contains(entries[1].Received, `"stopped":true`) {
				t.Errorf("the cancelled call of %q is in the audit log as %+v", task.Title, entries)
			}
			recs, err := usage.List(context.Background(), s, usage.Filter{RequestID: j.requestID})
			if err != nil {
				t.Fatal(err)
			}
			if len(recs) != 1 || recs[0].Outcome != usage.OutcomeStopped {
				t.Errorf("the cancelled call of %q left the usage records %+v", task.Title, recs)
			}
		})
	}

	// A task started while the switch is on is held, and calls nothing.
	before := mine.calls.Load()
	pots := sv.start(g.test.ID, parent, "Count the lobster pots.", "Pots")
	g.waitFor("the new task to be held", func() bool {
		var got Task
		sv.call(g.test.ID, "GET", "chats/"+parent+"/tasks/"+pots.ID, "", http.StatusOK, &got)
		return got.Status == StatusPaused && strings.HasPrefix(got.Progress, "Held")
	})
	time.Sleep(200 * time.Millisecond)
	if n := mine.calls.Load(); n != before {
		t.Fatalf("a task started while every agent is paused called its model: %d calls before, %d after", before, n)
	}

	// Switched off, the paused tasks go on with a new round, and the held
	// one starts.
	mine.block.Store(false)
	var resumed unattended.SwitchReply
	sv.call(g.test.ID, "POST", "agents/resume", "", http.StatusOK, &resumed)
	if resumed.Paused {
		t.Fatalf("the resume answered %+v", resumed)
	}
	for _, task := range []Task{boats, gulls, pots} {
		_, end := sv.follow(g.test.ID, task)
		wantRounds := 2
		if task.ID == pots.ID {
			wantRounds = 1
		}
		if end.Status != StatusDone || end.Result != "The boats are counted." || end.Rounds != wantRounds {
			t.Errorf("after the resume %q ended as %+v", task.Title, end)
		}
	}
	unpause, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	if msgs := sv.messages(g.test.ID, boats.WorkChatID); !containsText(msgs, unpause.UnpauseText) {
		t.Errorf("the round after the pause was not sent with agents.task_unpause_text")
	}
	sv.call(g.ordinary.ID, "POST", "chats/"+other+"/tasks/"+buoys.ID+"/stop", "", http.StatusOK, nil)
}

func containsText(msgs []turn.Message, want string) bool {
	for _, m := range msgs {
		if strings.Contains(text(m), want) {
			return true
		}
	}
	return false
}

// A paused task can be stopped, and ends as stopped.
func TestAPausedTaskCanBeStopped(t *testing.T) {
	g := newRig(t)
	model := newBlocking("unused")
	sv := g.serve(model)
	parent := sv.newChat(g.test.ID)
	started := sv.start(g.test.ID, parent, lighthouseTask, "To be paused")
	g.waitFor("the task's model call", func() bool { return model.inFlight.Load() == 1 })
	sv.call(g.test.ID, "POST", "agents/pause", "", http.StatusOK, nil)
	var stopped Task
	sv.call(g.test.ID, "POST", "chats/"+parent+"/tasks/"+started.ID+"/stop", "", http.StatusOK, &stopped)
	_, end := sv.follow(g.test.ID, started)
	if end.Status != StatusStopped {
		t.Fatalf("a paused task that was stopped ended as %+v", end)
	}
}

// The item's proof at the cap: a background task whose call the monthly cap
// refuses goes on with the local model in the person's chain and says so,
// in its chat and in its note, naming the model that wrote the reply, and
// the audit log holds both calls; with fallback off for unattended work it
// stops and says so, and the local model is not called; and a chat of the
// person's own still falls back, as decision 0160 has it.
func TestAtTheCapATaskFallsBackOrStops(t *testing.T) {
	g := newRig(t)
	var paid, house atomic.Int32
	g.extra = adapters.Set{
		models.AdapterAnthropic:       func(models.Model, string) (seam.Provider, error) { return answers{"From the paid model.", &paid}, nil },
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return answers{"From the house.", &house}, nil },
	}
	g.addModels(paidModel, houseModel)
	g.set(g.ordinary.ID, tenantsettings.Person, "chats.default_model", paidModel.Name)
	g.set(g.ordinary.ID, tenantsettings.Person, "routing.fallback_models", houseModel.Name)
	g.set(g.ordinary.ID, tenantsettings.Admin, "usage.monthly_cap_usd", "0")
	sv := g.serve(nil)
	parent := sv.newChat(g.ordinary.ID)

	started := sv.start(g.ordinary.ID, parent, "Name three harbour birds.", "Harbour birds")
	_, end := sv.follow(g.ordinary.ID, started)
	if end.Status != StatusDone || end.Result != "From the house." || paid.Load() != 0 || house.Load() != 1 {
		t.Fatalf("at the cap the task ended as %+v, after %d paid and %d local calls", end, paid.Load(), house.Load())
	}
	work := sv.messages(g.ordinary.ID, started.WorkChatID)
	if said := text(work[len(work)-1]); !strings.Contains(said, "monthly cap") || !strings.Contains(said, "So house-small, a local model, wrote the reply in place of paid-large.") {
		t.Errorf("the task's chat was told %q", said)
	}
	got := sv.messages(g.ordinary.ID, parent)
	if said := text(got[len(got)-1]); !strings.Contains(said, "From the house.") || !strings.Contains(said, "written by house-small") {
		t.Errorf("the chat the task was started from was told %q", said)
	}
	j := g.task(g.ordinary.ID, started.ID)
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{Kind: audit.KindModel, RequestID: j.requestID})
		if err != nil {
			t.Fatal(err)
		}
		var refused, answered bool
		for _, e := range entries {
			refused = refused || (e.Action == paidModel.Name && e.Outcome == audit.OutcomeRefused && strings.Contains(e.Received, string(seam.ErrSpendCap)) && strings.Contains(e.Received, "monthly cap"))
			answered = answered || (e.Action == houseModel.Name && e.Outcome == audit.OutcomeOK)
		}
		if !refused || !answered {
			t.Errorf("the audit log holds %+v", entries)
		}
		notices, err := unattended.Notices(context.Background(), s, j.requestID)
		if err != nil || len(notices) != 1 || notices[0].FallbackModel != houseModel.Name {
			t.Errorf("the task's notices are %+v (%v)", notices, err)
		}
	})

	// Fallback off: it stops, says so, and the local model is not called.
	g.set(g.ordinary.ID, tenantsettings.Person, unattended.KeyFallback, "false")
	started = sv.start(g.ordinary.ID, parent, "Name three harbour birds.", "Harbour birds again")
	_, end = sv.follow(g.ordinary.ID, started)
	if end.Status != StatusFailed || end.Error == nil || end.Error.Code != string(seam.ErrSpendCap) ||
		!strings.Contains(end.Error.Message, "switched off in your settings") || house.Load() != 1 {
		t.Fatalf("with fallback off the task ended as %+v, after %d local calls", end, house.Load())
	}
	work = sv.messages(g.ordinary.ID, started.WorkChatID)
	if said := text(work[len(work)-1]); !strings.Contains(said, "switched off in your settings") {
		t.Errorf("the stopped task's chat was told %q", said)
	}
	got = sv.messages(g.ordinary.ID, parent)
	if said := text(got[len(got)-1]); !strings.Contains(said, "did not finish") || !strings.Contains(said, "monthly cap") {
		t.Errorf("the chat the stopped task was started from was told %q", said)
	}

	// A chat of the person's own keeps today's behaviour.
	chatID := sv.newChat(g.ordinary.ID)
	body, _ := json.Marshal(map[string]string{"text": "Name three harbour birds."})
	resp := sv.request(g.ordinary.ID, "POST", "chats/"+chatID+"/turns", string(body))
	readEvents(t, resp.Body)
	resp.Body.Close()
	// Its reply, and its title, come from the local model.
	if msgs := sv.messages(g.ordinary.ID, chatID); house.Load() < 2 || paid.Load() != 0 || len(msgs) == 0 || text(msgs[len(msgs)-1]) != "From the house." {
		t.Errorf("a chat at the cap with unattended fallback off did not fall back: %d local calls", house.Load())
	}
}

// A run past its own budget stops and says so: a task given a budget of
// its own through the route, and a scheduled run, whose budget is
// agents.schedule_budget_usd and whose message says it is one.
func TestARunPastItsOwnBudgetStops(t *testing.T) {
	g := newRig(t)
	var paid atomic.Int32
	g.extra = adapters.Set{models.AdapterAnthropic: func(models.Model, string) (seam.Provider, error) { return answers{"From the paid model.", &paid}, nil }}
	g.addModels(paidModel)
	g.set(g.ordinary.ID, tenantsettings.Person, "chats.default_model", paidModel.Name)
	sv := g.serve(nil)
	parent := sv.newChat(g.ordinary.ID)

	var small Task
	sv.call(g.ordinary.ID, "POST", "chats/"+parent+"/tasks", `{"text":"Name three harbour birds.","title":"Small budget","budget_usd":0.001}`, http.StatusCreated, &small)
	_, end := sv.follow(g.ordinary.ID, small)
	if small.BudgetMicros != 1000 || end.Status != StatusFailed || end.Error == nil || end.Error.Code != string(seam.ErrSpendCap) ||
		!strings.Contains(end.Error.Message, "background task “Small budget” has spent $0.00 of its budget of $0.00") || paid.Load() != 0 {
		t.Fatalf("a task with a budget of its own ended as %+v", end)
	}
	got := sv.messages(g.ordinary.ID, parent)
	if said := text(got[len(got)-1]); !strings.Contains(said, "did not finish") || !strings.Contains(said, "budget of $0.00") {
		t.Errorf("the chat was told %q", said)
	}

	g.set(g.ordinary.ID, tenantsettings.Person, KeyBudget, "0.001")
	var large Task
	sv.call(g.ordinary.ID, "POST", "chats/"+parent+"/tasks", `{"text":"Name three harbour birds.","budget_usd":5}`, http.StatusCreated, &large)
	if _, end = sv.follow(g.ordinary.ID, large); end.Status != StatusDone || end.BudgetMicros != 5_000_000 || paid.Load() != 1 {
		t.Fatalf("a task with a budget of five dollars ended as %+v", end)
	}
	sv.call(g.ordinary.ID, "POST", "chats/"+parent+"/tasks", `{"text":"Name three harbour birds.","budget_usd":-1}`, http.StatusBadRequest, nil)

	g.set(g.ordinary.ID, tenantsettings.Person, unattended.KeyScheduleBudget, "0.001")
	runChat := sv.newChat(g.ordinary.ID)
	var run Task
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		var err error
		run, err = sv.jobs.StartIn(context.Background(), s, runChat, "req-a-scheduled-run", "schedule",
			StartBody{Text: "Name three harbour birds.", Title: "Harbour note", Kind: unattended.KindSchedule}, "")
		if err != nil {
			t.Fatal(err)
		}
	})
	if _, end = sv.follow(g.ordinary.ID, run); end.Status != StatusFailed || run.BudgetMicros != 1000 ||
		!strings.Contains(end.Error.Message, "scheduled task “Harbour note” has spent") {
		t.Fatalf("a scheduled run past its budget ended as %+v", end)
	}
	if said := sv.messages(g.ordinary.ID, runChat); !strings.Contains(text(said[len(said)-1]), "scheduled task “Harbour note”") {
		t.Errorf("the run's chat was told %q", text(said[len(said)-1]))
	}
}
