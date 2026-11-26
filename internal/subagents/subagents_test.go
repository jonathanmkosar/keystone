package subagents

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

const countPrompt = "Count the boats, the gulls and the buoys, all at once."

var countTasks = []string{"Count the boats in the harbour.", "Count the gulls on the pier.", "Count the buoys in the bay."}

// The item's proof: three sub-agents run in parallel lanes and their
// results reach the parent turn. Each sub-agent's model call waits until
// all three are under way at once, so they overlap in time or the test
// fails; the parent's next call carries the three results; each worked in a
// chat of its own, with none of the parent's history, under a request id of
// its own registered as a sub-agent with its budget; and every call is in
// the audit log.
func TestThreeSubagentsRunInParallelAndTheirResultsReachTheParent(t *testing.T) {
	g := newRig(t)
	lanes := &barrier{want: 3}
	sc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	model := &director{brief: sc.Brief, done: "All three are back.", sub: lanes.run,
		asks: map[string]toolCall{countPrompt: {ToolName, agentsInput(countTasks...)}}}
	sv := g.serve(model)
	chatID := sv.newChat(g.test.ID)

	sv.send(g.test.ID, chatID, countPrompt, 20*time.Second)

	// They overlapped: all three were under way at one moment.
	lanes.mu.Lock()
	most, spans := lanes.most, append([]span(nil), lanes.spans...)
	lanes.mu.Unlock()
	if most != 3 || len(spans) != 3 {
		t.Fatalf("at most %d sub-agents' calls were under way at once, of %d calls", most, len(spans))
	}
	var lastBegan, firstEnd time.Time
	for i, s := range spans {
		if i == 0 || s.began.After(lastBegan) {
			lastBegan = s.began
		}
		if i == 0 || s.end.Before(firstEnd) {
			firstEnd = s.end
		}
	}
	// The last to begin began before, or as, the first to end ended (the
	// clock may give both the same reading).
	if lastBegan.After(firstEnd) {
		t.Fatalf("the three calls did not overlap: the last began at %v, the first ended at %v", lastBegan, firstEnd)
	}

	// Their results reached the parent turn: its next call carries all three.
	calls := model.parentCalls()
	if len(calls) != 2 {
		t.Fatalf("the parent made %d model calls, want 2", len(calls))
	}
	carried, ok := lastResult(calls[1])
	if !ok {
		t.Fatalf("the parent's second call carries no tool result")
	}
	for _, task := range countTasks {
		if !strings.Contains(carried, "Counted for \""+task+"\": fourteen.") {
			t.Errorf("the parent's next call does not carry the result of %q:\n%s", task, carried)
		}
	}
	if !strings.Contains(carried, "3 sub-agents ran: 3 done.") {
		t.Errorf("the result does not open with how they ended:\n%s", carried)
	}
	msgs := sv.messages(g.test.ID, chatID)
	if last := text(msgs[len(msgs)-1]); last != "All three are back." {
		t.Errorf("the turn ended with %q", last)
	}

	// Three lanes, each done, each in a chat of its own.
	subs := sv.subagents(g.test.ID, chatID)
	if len(subs) != 3 {
		t.Fatalf("the chat lists %d sub-agents, want 3", len(subs))
	}
	seen := map[string]bool{}
	for i, a := range subs {
		if a.Status != StatusDone || a.Position != i+1 || a.CallID != "call-"+ToolName || a.TurnID == "" || !a.Passed ||
			a.BudgetMicros != 250_000 || a.MaxSteps != sc.MaxSteps || a.Steps != 1 || a.Progress != "Done" {
			t.Errorf("sub-agent %d reads %+v", i+1, a)
		}
		if seen[a.WorkChatID] || a.WorkChatID == chatID {
			t.Errorf("sub-agent %d does not have a chat of its own: %s", i+1, a.WorkChatID)
		}
		seen[a.WorkChatID] = true
		// Its own context: its chat holds its one message and its reply, and
		// nothing of the parent's.
		own := sv.messages(g.test.ID, a.WorkChatID)
		if len(own) != 2 || !strings.Contains(text(own[0]), sc.Brief) || !strings.Contains(text(own[0]), countTasks[i]) || strings.Contains(text(own[0]), countPrompt) {
			t.Errorf("sub-agent %d's chat holds %d messages, the first %q", i+1, len(own), text(own[0]))
		}
	}
	for _, req := range model.agentCalls() {
		if strings.Contains(userText(req), countPrompt) {
			t.Errorf("a sub-agent's call carried the parent's history")
		}
	}

	// Each is an unattended run of its own, and every call is recorded.
	g.with(g.test.ID, func(s *tenant.Store) {
		ctx := context.Background()
		rows, err := list(ctx, s.DB(), "chat_id = ?", chatID)
		if err != nil {
			t.Fatal(err)
		}
		requests := map[string]bool{}
		for _, a := range rows {
			if requests[a.requestID] {
				t.Errorf("two sub-agents share the request id %s", a.requestID)
			}
			requests[a.requestID] = true
			run, ok, err := unattended.ForRequest(ctx, s.DB(), a.requestID)
			if err != nil || !ok || run.Kind != unattended.KindSubagent || run.ID != a.ID || run.BudgetMicros != a.BudgetMicros {
				t.Errorf("sub-agent %s is registered as %+v (%v, %v)", a.ID, run, ok, err)
			}
			entries, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindModel, RequestID: a.requestID})
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 || entries[0].Outcome != audit.OutcomeStarted || entries[1].Outcome != audit.OutcomeOK {
				t.Errorf("sub-agent %s's model call is in the audit log as %+v", a.ID, entries)
			}
		}
		tool, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindTool})
		if err != nil {
			t.Fatal(err)
		}
		var started, ended bool
		for _, e := range tool {
			started = started || (e.Action == ToolName && e.Outcome == audit.OutcomeStarted)
			ended = ended || (e.Action == ToolName && e.Outcome == audit.OutcomeOK)
		}
		if !started || !ended {
			t.Errorf("the call to %s is not in the audit log: %+v", ToolName, tool)
		}
	})

	// One sub-agent, read alone, and by its own chat.
	var one Subagent
	sv.call(g.test.ID, "GET", "chats/"+chatID+"/subagents/"+subs[0].ID, "", http.StatusOK, &one)
	if one.ID != subs[0].ID || one.Result == "" {
		t.Errorf("one sub-agent reads %+v", one)
	}
	var own SubagentsReply
	sv.call(g.test.ID, "GET", "chats/"+subs[1].WorkChatID+"/subagents", "", http.StatusOK, &own)
	if len(own.Subagents) != 1 || own.Subagents[0].ID != subs[1].ID {
		t.Errorf("a sub-agent's own chat lists %+v", own.Subagents)
	}
	var byCall SubagentsReply
	sv.call(g.test.ID, "GET", "chats/"+chatID+"/subagents?call=nothing-of-the-sort", "", http.StatusOK, &byCall)
	if len(byCall.Subagents) != 0 {
		t.Errorf("a call that started none lists %d", len(byCall.Subagents))
	}
	// Another person reaches none of it.
	sv.call(g.ordinary.ID, "GET", "chats/"+chatID+"/subagents", "", http.StatusNotFound, nil)
	sv.call(g.ordinary.ID, "POST", "chats/"+chatID+"/subagents/"+subs[0].ID+"/stop", "", http.StatusNotFound, nil)
}
