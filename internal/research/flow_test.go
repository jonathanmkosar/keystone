package research

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/subagents"
)

// Stop ends a task that is under way: its parts are stopped, nothing more
// is called, no report is written, and the chat is told.
func TestStopEndsATaskAndItsParts(t *testing.T) {
	g := newRig(t, museumPages)
	model := museumModel()
	var holding atomic.Int32
	model.block = func(ctx context.Context, step, _ string) error {
		if step == "part" {
			return untilCancelled(ctx, &holding)
		}
		return nil
	}
	sv := g.serve(model)
	chatID := sv.newChat(g.test.ID)
	started := sv.start(g.test.ID, chatID, StartBody{Question: museumQuestion, Title: "Museum visit"})
	g.waitFor("both parts to be at work", func() bool { return holding.Load() == 2 })

	// While it works, the task says where it is.
	now := sv.one(g.test.ID, chatID, started.ID)
	if now.Status != StatusRunning || now.Stage != StageParts || len(now.Parts) != 2 || len(now.Plan) != 2 || !strings.Contains(now.Progress, "0 of 2 parts done") {
		t.Errorf("a task at work reads %+v", now)
	}

	var stopped Research
	sv.call(g.test.ID, "POST", "chats/"+chatID+"/research/"+started.ID+"/stop", "", http.StatusOK, &stopped)
	got := sv.ends(g.test.ID, chatID, started.ID)
	if stopped.Status != StatusStopped || got.Status != StatusStopped || got.ArtifactID != "" || got.Error != nil {
		t.Fatalf("a stopped task reads %+v, then %+v", stopped, got)
	}
	for _, a := range got.Parts {
		if a.Status != subagents.StatusStopped {
			t.Errorf("a part of a stopped task ended as %s", a.Status)
		}
	}
	if _, _, reports := model.count(); reports != 0 {
		t.Errorf("a stopped task wrote a report")
	}
	msgs := sv.messages(g.test.ID, chatID)
	if note := text(msgs[len(msgs)-1]); !strings.Contains(note, "Research “Museum visit” was stopped before its report was written.") {
		t.Errorf("the note of a stopped task says %q", note)
	}
}

// The switch that pauses every agent holds a task started while it is on,
// stops the step of one that is under way, and lets both go on when it
// goes off.
func TestThePauseSwitchHoldsATaskAndLetsItGoOn(t *testing.T) {
	g := newRig(t, museumPages)
	model := museumModel()
	var holding atomic.Int32
	var first atomic.Bool
	first.Store(true)
	// The first call of the plan step waits until it is cancelled.
	model.block = func(ctx context.Context, step, _ string) error {
		if step == "plan" && first.CompareAndSwap(true, false) {
			return untilCancelled(ctx, &holding)
		}
		return nil
	}
	sv := g.serve(model)
	chatID := sv.newChat(g.test.ID)
	started := sv.start(g.test.ID, chatID, StartBody{Question: museumQuestion})
	g.waitFor("the plan step to be at work", func() bool { return holding.Load() == 1 })

	sv.call(g.test.ID, "POST", "agents/pause", "", http.StatusOK, nil)
	g.waitFor("the task to be held", func() bool { return sv.one(g.test.ID, chatID, started.ID).Status == StatusPaused })
	held := sv.one(g.test.ID, chatID, started.ID)
	if !strings.Contains(held.Progress, "Paused: every agent is paused") {
		t.Errorf("a paused task says %q", held.Progress)
	}
	// One started meanwhile is held, and calls nothing.
	other := sv.newChat(g.test.ID)
	second := sv.start(g.test.ID, other, StartBody{Question: "What does the ferry cost?", Title: "Ferry fares"})
	g.waitFor("the second task to be held", func() bool { return sv.one(g.test.ID, other, second.ID).Status == StatusPaused })
	var terms Terms
	sv.call(g.test.ID, "GET", "research", "", http.StatusOK, &terms)
	if !terms.Paused || terms.Open != 2 {
		t.Errorf("while every agent is paused the terms read %+v", terms)
	}
	before := model.calls.Load()
	time.Sleep(300 * time.Millisecond)
	if model.calls.Load() != before {
		t.Errorf("a model was called while every agent was paused")
	}

	sv.call(g.test.ID, "POST", "agents/resume", "", http.StatusOK, nil)
	got := sv.ends(g.test.ID, chatID, started.ID)
	if got.Status != StatusDone || len(got.Sources) != 3 {
		t.Fatalf("after the switch went off the task ended as %+v (%+v)", got, got.Error)
	}
	if plans, _, reports := model.count(); plans < 2 || reports < 1 {
		t.Errorf("the plan step was sent %d times and the report %d", plans, reports)
	}
	if again := sv.ends(g.test.ID, other, second.ID); again.Status != StatusDone {
		t.Errorf("the task held from its start ended as %+v", again)
	}
}

// A restart while the parts work ends the parts, as it ends any sub-agent,
// and the task goes on to its report with what they had: the part that had
// finished is in it, and it says which did not.
func TestATaskGoesOnAfterARestartWithWhatItsPartsHad(t *testing.T) {
	g := newRig(t, museumPages)
	model := museumModel()
	var holding atomic.Int32
	model.block = func(ctx context.Context, step, task string) error {
		if step == "part" && task == ferryTask {
			return untilCancelled(ctx, &holding)
		}
		return nil
	}
	model.report = func(string) string {
		return "## Summary\n\nThe harbour museum is open from ten, Tuesday to Sunday [1]. When the ferry leaves was not found."
	}
	sv := g.serve(model)
	chatID := sv.newChat(g.test.ID)
	started := sv.start(g.test.ID, chatID, StartBody{Question: museumQuestion})
	g.waitFor("the first part to finish and the second to be at work", func() bool {
		now := sv.one(g.test.ID, chatID, started.ID)
		return holding.Load() == 1 && len(now.Parts) == 2 && now.Parts[0].Status == subagents.StatusDone
	})
	sv.stop() // as stopping the process would

	model.block = nil
	sv = g.serve(model)
	got := sv.ends(g.test.ID, chatID, started.ID)
	if got.Status != StatusDone || len(got.Parts) != 2 || got.Parts[0].Status != subagents.StatusDone || got.Parts[1].Status != subagents.StatusFailed ||
		got.Parts[1].Error == nil || got.Parts[1].Error.Code != subagents.CodeInterrupted {
		t.Fatalf("after a restart the task ended as %+v", got)
	}
	sent := model.reportMessages()
	if len(sent) != 1 || !strings.Contains(sent[0], "Part 2 of 2, “Getting there” (did not finish):") || !strings.Contains(sent[0], "closed on Mondays [2]") {
		t.Fatalf("the report step was sent %q", sent)
	}
	if !strings.Contains(got.Notice, "The part “Getting there” did not finish") || len(got.Sources) != 1 || got.SourcesRead != 2 {
		t.Errorf("the task says %q, and cites %d of %d sources", got.Notice, len(got.Sources), got.SourcesRead)
	}
	if plans, _, _ := model.count(); plans != 1 {
		t.Errorf("the plan step was sent %d times", plans)
	}
}

// What a start refuses: no question, a start from a task's own chat, one
// task too many.
func TestWhatAStartRefuses(t *testing.T) {
	g := newRig(t, museumPages)
	g.config = func(c *Config) { c.OpenMax = 1 }
	model := museumModel()
	var holding atomic.Int32
	model.block = func(ctx context.Context, step, _ string) error {
		if step == "plan" {
			return untilCancelled(ctx, &holding)
		}
		return nil
	}
	sv := g.serve(model)
	chatID := sv.newChat(g.test.ID)
	sv.call(g.test.ID, "POST", "chats/"+chatID+"/research", `{"question":"  "}`, http.StatusBadRequest, nil)
	sv.call(g.test.ID, "POST", "chats/"+chatID+"/research", `{"question":"Anything?","nothing":1}`, http.StatusBadRequest, nil)
	sv.call(g.test.ID, "POST", "chats/no-such-chat/research", `{"question":"Anything?"}`, http.StatusNotFound, nil)

	started := sv.start(g.test.ID, chatID, StartBody{Question: museumQuestion})
	g.waitFor("the plan step to be at work", func() bool { return holding.Load() == 1 })
	sv.call(g.test.ID, "POST", "chats/"+chatID+"/research", `{"question":"One more?"}`, http.StatusTooManyRequests, nil)
	sv.call(g.test.ID, "POST", "chats/"+started.WorkChatID+"/research", `{"question":"From its own chat?"}`, http.StatusBadRequest, nil)
	sv.call(g.test.ID, "GET", "chats/"+chatID+"/research/0000000000000000", "", http.StatusNotFound, nil)

	// Stopping the plan's turn in the task's own chat stops the task.
	var stopped Research
	sv.call(g.test.ID, "POST", "chats/"+started.WorkChatID+"/research/"+started.ID+"/stop", "", http.StatusOK, &stopped)
	if got := sv.ends(g.test.ID, chatID, started.ID); got.Status != StatusStopped {
		t.Errorf("a task stopped from its own chat ended as %+v", got)
	}
}
