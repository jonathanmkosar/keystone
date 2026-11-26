package subagents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// The limits from the settings: no more than agents.subagents_at_once of a
// call work at once, and the rest wait for a lane; a turn starts no more
// than agents.subagents_per_turn; a sub-agent cannot start sub-agents; and
// a call with no task or an unknown model is refused before anything runs.
func TestTheLimitsHoldAndASubagentStartsNone(t *testing.T) {
	g := newRig(t)
	g.config = func(c *Config) { c.AtOnce, c.PerTurn = 1, 3 }
	lanes := &barrier{want: 1}
	sc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	sub := func(ctx context.Context, task string, req seam.Request, emit func(seam.Event) error) error {
		if strings.Contains(task, "nested") {
			if res, ok := lastResult(req); ok {
				return say(emit, req.Model, "Told: "+strings.TrimSpace(res))
			}
			return ask(emit, req.Model, "nested-1", ToolName, agentsInput("Count the crabs."))
		}
		return lanes.run(ctx, task, req, emit)
	}
	model := &director{brief: sc.Brief, done: "Back.", sub: sub, asks: map[string]toolCall{
		"One at a time.":  {ToolName, agentsInput(countTasks...)},
		"Too many.":       {ToolName, agentsInput("One.", "Two.", "Three.", "Four.")},
		"Nest them.":      {ToolName, agentsInput("Start a nested helper.")},
		"No task.":        {ToolName, map[string]any{"agents": []map[string]any{{"title": "Nothing"}}}},
		"None at all.":    {ToolName, map[string]any{"agents": []map[string]any{}}},
		"An odd model.":   {ToolName, map[string]any{"agents": []map[string]any{{"task": "Count the oars.", "model": "no-such-model"}}}},
		"A wrong budget.": {ToolName, map[string]any{"agents": []map[string]any{{"task": "Count the oars.", "budget_usd": -1}}}},
	}}
	sv := g.serve(model)

	chatID := sv.newChat(g.test.ID)
	sv.send(g.test.ID, chatID, "One at a time.", 20*time.Second)
	lanes.mu.Lock()
	most, n := lanes.most, len(lanes.spans)
	lanes.mu.Unlock()
	if most != 1 || n != 3 {
		t.Fatalf("with one lane, %d sub-agents worked at once, of %d", most, n)
	}
	for _, a := range sv.subagents(g.test.ID, chatID) {
		if a.Status != StatusDone {
			t.Errorf("a sub-agent that waited for its lane reads %+v", a)
		}
	}

	for prompt, want := range map[string]string{
		"Too many.":       "one turn may start 3 in all, and this one has started 0, so it may start 3 more",
		"No task.":        "sub-agent 1 has no task",
		"None at all.":    "give at least one sub-agent",
		"An odd model.":   `there is no model named "no-such-model"`,
		"A wrong budget.": "budget_usd is a number of US dollars, 0 or more",
	} {
		other := sv.newChat(g.test.ID)
		sv.send(g.test.ID, other, prompt, 20*time.Second)
		// The registry makes an error a sentence, with a capital first.
		if res, ok := toolResult(sv.messages(g.test.ID, other), ToolName); !ok || !strings.Contains(strings.ToLower(res), strings.ToLower(want)) {
			t.Errorf("%q: the tool answered %q, want it to say %q", prompt, res, want)
		}
		if subs := sv.subagents(g.test.ID, other); len(subs) != 0 {
			t.Errorf("%q started %d sub-agents", prompt, len(subs))
		}
	}

	nested := sv.newChat(g.test.ID)
	sv.send(g.test.ID, nested, "Nest them.", 20*time.Second)
	subs := sv.subagents(g.test.ID, nested)
	if len(subs) != 1 || subs[0].Status != StatusDone || !strings.Contains(subs[0].Result, "A sub-agent cannot start sub-agents") {
		t.Fatalf("a sub-agent that asked for sub-agents reads %+v", subs)
	}
	if own := sv.subagents(g.test.ID, subs[0].WorkChatID); len(own) != 1 {
		t.Errorf("the sub-agent's chat started sub-agents of its own: %+v", own)
	}
}

// A lane's Stop stops its sub-agent, and the call goes on with the others;
// the parent is told it was stopped by the person.
func TestALaneStopsOneSubagent(t *testing.T) {
	g := newRig(t)
	slow := &blocker{}
	sc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	sub := func(ctx context.Context, task string, req seam.Request, emit func(seam.Event) error) error {
		if strings.Contains(task, "boats") {
			return slow.run(ctx, task, req, emit)
		}
		return say(emit, req.Model, "Counted the gulls.")
	}
	model := &director{brief: sc.Brief, done: "Back.", sub: sub,
		asks: map[string]toolCall{"Boats and gulls.": {ToolName, agentsInput(countTasks[0], countTasks[1])}}}
	sv := g.serve(model)
	chatID := sv.newChat(g.test.ID)
	done := sv.sendAsync(g.test.ID, chatID, "Boats and gulls.")
	var boats Subagent
	g.waitFor("the gulls to be counted while the boats are under way", func() bool {
		subs := sv.subagents(g.test.ID, chatID)
		if len(subs) == 2 && subs[1].Status == StatusDone && slow.inFlight.Load() == 1 {
			boats = subs[0]
			return true
		}
		return false
	})
	if boats.Status != StatusRunning || boats.Progress == "" {
		t.Fatalf("the sub-agent under way reads %+v", boats)
	}
	var stopped Subagent
	sv.call(g.test.ID, "POST", "chats/"+chatID+"/subagents/"+boats.ID+"/stop", "", http.StatusOK, &stopped)
	if stopped.Status != StatusStopped {
		t.Fatalf("the stopped sub-agent reads %+v", stopped)
	}
	sv.wait(done, "the turn to end after one lane was stopped")
	if slow.cancelled.Load() != 1 {
		t.Errorf("the stopped sub-agent's call was not cancelled")
	}
	carried := model.carried()
	if !strings.Contains(carried, "2 sub-agents ran: 1 done, 1 stopped.") || !strings.Contains(carried, "stopped by the person") || !strings.Contains(carried, "Counted the gulls.") {
		t.Errorf("the parent was told:\n%s", carried)
	}
	// Stopping one that has ended changes nothing.
	var again Subagent
	sv.call(g.test.ID, "POST", "chats/"+chatID+"/subagents/"+boats.ID+"/stop", "", http.StatusOK, &again)
	if again.Status != StatusStopped {
		t.Errorf("a second stop reads %+v", again)
	}
}

// At agents.subagent_max_steps a sub-agent stops, with what it had written,
// and the parent is told it did not finish. The step limit is the
// sub-agent's and not the parent's.
func TestAtItsStepLimitASubagentStopsAndSaysSo(t *testing.T) {
	g := newRig(t)
	g.config = func(c *Config) { c.MaxSteps = 2 }
	sc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	step := 0
	sub := func(_ context.Context, _ string, req seam.Request, emit func(seam.Event) error) error {
		step++
		return emitAll(emit, seam.Response{
			Model: req.Model, StopReason: seam.StopToolCall, Usage: seam.Usage{InputTokens: 100, OutputTokens: 20},
			Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{
				{Type: seam.BlockText, Text: "Still looking at the tide tables."},
				{Type: seam.BlockToolCall, ID: "clock-" + string(rune('a'+step)), Name: "current_time", Input: json.RawMessage(`{}`)},
			}},
		})
	}
	model := &director{brief: sc.Brief, done: "Back.", sub: sub,
		asks: map[string]toolCall{"Look it up.": {ToolName, agentsInput("Find the next high tide.")}}}
	sv := g.serve(model)
	chatID := sv.newChat(g.test.ID)
	sv.send(g.test.ID, chatID, "Look it up.", 20*time.Second)
	a := sv.subagents(g.test.ID, chatID)[0]
	if a.Status != StatusFailed || a.Error == nil || a.Error.Code != CodeStepLimit || a.Steps != 2 || a.MaxSteps != 2 || a.Result != "Still looking at the tide tables." {
		t.Fatalf("a sub-agent at its step limit reads %+v", a)
	}
	carried := model.carried()
	if !strings.Contains(carried, "did not finish") || !strings.Contains(carried, "It made 2 model calls, the most a sub-agent may") || !strings.Contains(carried, "What it had written:\nStill looking") {
		t.Errorf("the parent was told:\n%s", carried)
	}
}

// Stopping the turn that started them stops its sub-agents, and each ends
// as stopped with it.
func TestStoppingTheParentTurnStopsItsSubagents(t *testing.T) {
	g := newRig(t)
	lanes := &blocker{}
	sc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	model := &director{brief: sc.Brief, done: "Back.", sub: lanes.run, asks: map[string]toolCall{countPrompt: {ToolName, agentsInput(countTasks...)}}}
	sv := g.serve(model)
	chatID := sv.newChat(g.test.ID)
	done := sv.sendAsync(g.test.ID, chatID, countPrompt)
	g.waitFor("the three sub-agents' calls to be under way", func() bool { return lanes.inFlight.Load() == 3 })
	var turnID string
	g.with(g.test.ID, func(s *tenant.Store) {
		if err := s.DB().QueryRow("SELECT id FROM turns WHERE chat_id = ? AND status = 'running'", chatID).Scan(&turnID); err != nil {
			t.Fatal(err)
		}
	})
	sv.call(g.test.ID, "POST", "chats/"+chatID+"/turns/"+turnID+"/stop", "", http.StatusOK, nil)
	sv.wait(done, "the stopped turn's stream to end")
	g.waitFor("every sub-agent to end with the turn", func() bool {
		for _, a := range sv.subagents(g.test.ID, chatID) {
			if a.Status != StatusStopped {
				return false
			}
		}
		return lanes.cancelled.Load() == 3
	})
	for _, a := range sv.subagents(g.test.ID, chatID) {
		if r := g.row(g.test.ID, a.ID); r.stopWhy != byParent {
			t.Errorf("a sub-agent of a stopped turn was stopped by %q", r.stopWhy)
		}
	}
}

// A restart in the middle ends the sub-agents as interrupted, as the turn
// that started them is closed as failed: nothing waits for their results.
func TestARestartEndsSubagentsAsInterrupted(t *testing.T) {
	g := newRig(t)
	lanes := &blocker{}
	sc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	model := &director{brief: sc.Brief, done: "Back.", sub: lanes.run, asks: map[string]toolCall{countPrompt: {ToolName, agentsInput(countTasks...)}}}
	first := g.serve(model)
	chatID := first.newChat(g.test.ID)
	first.sendAsync(g.test.ID, chatID, countPrompt)
	g.waitFor("the three sub-agents' calls to be under way", func() bool { return lanes.inFlight.Load() == 3 })
	first.stop()

	second := g.serve(model)
	subs := second.subagents(g.test.ID, chatID)
	if len(subs) != 3 {
		t.Fatalf("%d sub-agents after the restart", len(subs))
	}
	for _, a := range subs {
		if a.Status != StatusFailed || a.Error == nil || a.Error.Code != CodeInterrupted {
			t.Errorf("after a restart a sub-agent reads %+v", a)
		}
		var wt turn.Turn
		second.call(g.test.ID, "GET", "chats/"+a.WorkChatID+"/turns/"+a.WorkTurnID, "", http.StatusOK, &wt)
		if wt.Status == turn.StatusQueued || wt.Status == turn.StatusRunning {
			t.Errorf("after a restart a sub-agent's turn is %s", wt.Status)
		}
	}
}
