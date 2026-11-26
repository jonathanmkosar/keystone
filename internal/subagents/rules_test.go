package subagents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// pauseBound is the most the switch may take to stop every sub-agent: the
// plan's "within seconds".
const pauseBound = 5 * time.Second

// sendAsync sends a message and reads the turn's stream in a goroutine of
// its own, which fails nothing: the channel gets the events, or nothing
// when the request could not be made.
func (sv *served) sendAsync(tenantID, chatID, text string) <-chan []event {
	body, _ := json.Marshal(map[string]string{"text": text})
	done := make(chan []event, 1)
	go func() {
		req, err := http.NewRequest("POST", sv.server.URL+api.Prefix+"chats/"+chatID+"/turns", strings.NewReader(string(body)))
		if err != nil {
			done <- nil
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: api.CookieName, Value: sv.tokens[tenantID]})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- nil
			return
		}
		defer resp.Body.Close()
		done <- readEvents(sv.g.t, resp.Body)
	}()
	return done
}

func (sv *served) wait(done <-chan []event, what string) {
	sv.g.t.Helper()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		sv.g.t.Fatalf("waited too long for %s", what)
	}
}

// row reads a sub-agent's row.
func (g *rig) row(tenantID, id string) row {
	g.t.Helper()
	var a row
	g.with(tenantID, func(s *tenant.Store) {
		var err error
		if a, err = get(context.Background(), s.DB(), id); err != nil {
			g.t.Fatal(err)
		}
	})
	return a
}

// A sub-agent past its budget stops and says so: a model may ask for a
// budget only within the person's limit, a call that could pass it is not
// sent, and its result says why. With a local model in the chain it goes on
// with that model and says so; with fallback off for unattended work it
// stops. The budget is the run registry's (P8-05).
func TestASubagentPastItsBudgetStopsAndSaysSo(t *testing.T) {
	g := newRig(t)
	var paid atomic.Int32
	sc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	const prompt = "Ask a helper on the paid model."
	model := &director{brief: sc.Brief, done: "Told.",
		sub: func(_ context.Context, _ string, req seam.Request, emit func(seam.Event) error) error {
			return say(emit, req.Model, "From the house.")
		},
		asks: map[string]toolCall{prompt: {ToolName, map[string]any{"agents": []map[string]any{
			{"task": "Name three harbour birds.", "title": "Harbour birds", "model": paidModel.Name, "budget_usd": 5},
		}}}},
	}
	g.extra = adapters.Set{
		models.AdapterAnthropic:       func(models.Model, string) (seam.Provider, error) { return counting{"From the paid model.", &paid}, nil },
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return model, nil },
	}
	g.addModels(paidModel, houseModel)
	g.set(g.ordinary.ID, tenantsettings.Person, "chats.default_model", houseModel.Name)
	g.set(g.ordinary.ID, tenantsettings.Person, unattended.KeySubagentBudget, "0.001")
	sv := g.serve(nil)

	chatID := sv.newChat(g.ordinary.ID)
	sv.send(g.ordinary.ID, chatID, prompt, 20*time.Second)
	subs := sv.subagents(g.ordinary.ID, chatID)
	if len(subs) != 1 {
		t.Fatalf("%d sub-agents started", len(subs))
	}
	a := subs[0]
	if a.BudgetMicros != 1000 || a.Model != paidModel.Name {
		t.Errorf("a budget of five dollars asked for was not held to the person's tenth of a cent: %+v", a)
	}
	if a.Status != StatusFailed || a.Error == nil || a.Error.Code != string(seam.ErrSpendCap) ||
		!strings.Contains(a.Error.Message, "sub-agent “Harbour birds” has spent $0.00 of its budget of $0.00") || paid.Load() != 0 {
		t.Fatalf("a sub-agent past its budget ended as %+v, after %d paid calls", a, paid.Load())
	}
	carried := model.carried()
	if !strings.Contains(carried, "did not finish") || !strings.Contains(carried, "budget of $0.00") {
		t.Errorf("the parent was told:\n%s", carried)
	}

	// With a local model in the chain it goes on with it, and says so.
	g.set(g.ordinary.ID, tenantsettings.Person, "routing.fallback_models", houseModel.Name)
	chatID = sv.newChat(g.ordinary.ID)
	sv.send(g.ordinary.ID, chatID, prompt, 20*time.Second)
	a = sv.subagents(g.ordinary.ID, chatID)[0]
	if a.Status != StatusDone || a.Result != "From the house." || !strings.Contains(a.Notice, "written by house-small") || paid.Load() != 0 {
		t.Fatalf("with a local model in the chain the sub-agent ended as %+v", a)
	}
	own := sv.messages(g.ordinary.ID, a.WorkChatID)
	if said := text(own[len(own)-1]); !strings.Contains(said, "So house-small, a local model, wrote the reply in place of paid-large.") {
		t.Errorf("the sub-agent's chat was told %q", said)
	}
	if carried = model.carried(); !strings.Contains(carried, "From the house.") || !strings.Contains(carried, "written by house-small") {
		t.Errorf("the parent was told:\n%s", carried)
	}

	// With fallback off it stops, and the local model is not asked.
	g.set(g.ordinary.ID, tenantsettings.Person, unattended.KeyFallback, "false")
	before := len(model.agentCalls())
	chatID = sv.newChat(g.ordinary.ID)
	sv.send(g.ordinary.ID, chatID, prompt, 20*time.Second)
	a = sv.subagents(g.ordinary.ID, chatID)[0]
	if a.Status != StatusFailed || a.Error == nil || !strings.Contains(a.Error.Message, "switched off in your settings") || len(model.agentCalls()) != before {
		t.Fatalf("with fallback off the sub-agent ended as %+v", a)
	}
}

// The switch that pauses every agent stops every running sub-agent within
// seconds, through its model call's context, and each ends as paused; the
// parent turn goes on with their results at once; every cancelled call is
// recorded; and while the switch is on, no sub-agent starts.
func TestThePauseSwitchStopsSubagents(t *testing.T) {
	g := newRig(t)
	lanes := &blocker{}
	sc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	model := &director{brief: sc.Brief, done: "They were paused.", sub: lanes.run,
		asks: map[string]toolCall{countPrompt: {ToolName, agentsInput(countTasks...)}}}
	sv := g.serve(model)
	chatID := sv.newChat(g.test.ID)
	done := sv.sendAsync(g.test.ID, chatID, countPrompt)
	g.waitFor("the three sub-agents' calls to be under way", func() bool { return lanes.inFlight.Load() == 3 })

	began := time.Now()
	var paused unattended.SwitchReply
	sv.call(g.test.ID, "POST", "agents/pause", "", http.StatusOK, &paused)
	if !paused.Paused {
		t.Fatalf("the pause answered %+v", paused)
	}
	sv.wait(done, "the parent turn to end after the pause")
	if took := time.Since(began); took > pauseBound || lanes.inFlight.Load() != 0 || lanes.cancelled.Load() != 3 {
		t.Fatalf("%v after the pause, %d sub-agents' calls are under way and %d were cancelled", took, lanes.inFlight.Load(), lanes.cancelled.Load())
	}
	subs := sv.subagents(g.test.ID, chatID)
	for _, a := range subs {
		if a.Status != StatusPaused || a.Error != nil {
			t.Errorf("a paused sub-agent reads %+v", a)
		}
		r := g.row(g.test.ID, a.ID)
		g.with(g.test.ID, func(s *tenant.Store) {
			entries, err := audit.List(context.Background(), s, audit.Filter{Kind: audit.KindModel, RequestID: r.requestID})
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 || entries[1].Outcome != audit.OutcomeOK || !strings.Contains(entries[1].Received, `"stopped":true`) {
				t.Errorf("the cancelled call of %q is in the audit log as %+v", a.Title, entries)
			}
			recs, err := usage.List(context.Background(), s, usage.Filter{RequestID: r.requestID})
			if err != nil {
				t.Fatal(err)
			}
			if len(recs) != 1 || recs[0].Outcome != usage.OutcomeStopped {
				t.Errorf("the cancelled call of %q left the usage records %+v", a.Title, recs)
			}
		})
	}
	if carried := model.carried(); !strings.Contains(carried, "3 sub-agents ran: 3 paused.") {
		t.Errorf("the parent was told:\n%s", carried)
	}

	// While the switch is on, the tool starts none and says why.
	other := sv.newChat(g.test.ID)
	sv.send(g.test.ID, other, countPrompt, 20*time.Second)
	if res, ok := toolResult(sv.messages(g.test.ID, other), ToolName); !ok || !strings.Contains(res, "every agent of the person's is paused") {
		t.Errorf("while paused the tool answered %q", res)
	}
	if n := len(sv.subagents(g.test.ID, other)); n != 0 {
		t.Errorf("%d sub-agents started while every agent was paused", n)
	}
}

// A sub-agent's side effect asks the person, and runs once when approved;
// its lane says it waits. Outside content travels both ways (fixed rule 2):
// a sub-agent that read some marks the chat it was started from, and its
// result opens with the mark; a sub-agent of a chat that read some asks for
// every side effect, whatever the tool's policy.
func TestASideEffectAsksAndOutsideContentTravelsBothWays(t *testing.T) {
	g := newRig(t)
	var lit atomic.Int32
	object := json.RawMessage(`{"type":"object"}`)
	g.tools = []tools.Tool{
		{Name: "light_lamp", Description: "Lights the harbour lamp.", InputSchema: object, Inside: true,
			Run: func(context.Context, tools.Input) (string, error) { lit.Add(1); return "The lamp is lit.", nil }},
		{Name: "read_notice", Description: "Reads the notice board by the ferry.", InputSchema: object, ReadOnly: true,
			Run: func(context.Context, tools.Input) (string, error) { return "Notice: the ferry is late.", nil }},
	}
	sc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	sub := func(_ context.Context, task string, req seam.Request, emit func(seam.Event) error) error {
		if res, ok := lastResult(req); ok {
			return say(emit, req.Model, "Done: "+strings.TrimSpace(res))
		}
		switch {
		case strings.Contains(task, "lamp"):
			return ask(emit, req.Model, "lamp-1", "light_lamp", map[string]any{})
		case strings.Contains(task, "notice"):
			return ask(emit, req.Model, "notice-1", "read_notice", map[string]any{})
		}
		return say(emit, req.Model, "Nothing to do.")
	}
	model := &director{brief: sc.Brief, done: "Back.", sub: sub, asks: map[string]toolCall{
		"Light the lamp.":  {ToolName, agentsInput("Light the harbour lamp.")},
		"Read the notice.": {ToolName, agentsInput("Read the notice board.")},
	}}
	sv := g.serve(model)

	// 1. The side effect asks; the lane waits; approved, it runs once.
	chatID := sv.newChat(g.test.ID)
	done := sv.sendAsync(g.test.ID, chatID, "Light the lamp.")
	var waiting Subagent
	g.waitFor("the sub-agent to wait for the person", func() bool {
		subs := sv.subagents(g.test.ID, chatID)
		if len(subs) == 1 && subs[0].Waiting {
			waiting = subs[0]
			return true
		}
		return false
	})
	if waiting.Progress != "Waiting for your approval to run light_lamp" || lit.Load() != 0 {
		t.Fatalf("a waiting sub-agent reads %+v, and the lamp was lit %d times", waiting, lit.Load())
	}
	var asked turn.ApprovalsReply
	sv.call(g.test.ID, "GET", "chats/"+waiting.WorkChatID+"/turns/"+waiting.WorkTurnID+"/approvals", "", http.StatusOK, &asked)
	if len(asked.Approvals) != 1 || asked.Approvals[0].Tool != "light_lamp" || asked.Approvals[0].Status != turn.ApprovalPending {
		t.Fatalf("the sub-agent's turn asked %+v", asked.Approvals)
	}
	sv.call(g.test.ID, "POST", "chats/"+waiting.WorkChatID+"/turns/"+waiting.WorkTurnID+"/approvals/"+asked.Approvals[0].ID+"/approve", "", http.StatusOK, nil)
	sv.wait(done, "the turn to end after the approval")
	a := sv.subagents(g.test.ID, chatID)[0]
	if a.Status != StatusDone || a.Result != "Done: The lamp is lit." || lit.Load() != 1 || a.Outside {
		t.Fatalf("after the approval the sub-agent reads %+v, and the lamp was lit %d times", a, lit.Load())
	}

	// 2. Outside content comes back marked, and marks the chat.
	noticed := sv.newChat(g.test.ID)
	sv.send(g.test.ID, noticed, "Read the notice.", 20*time.Second)
	res, ok := toolResult(sv.messages(g.test.ID, noticed), ToolName)
	if !ok || !strings.HasPrefix(res, tools.OutsideMark) || !strings.Contains(res, "Notice: the ferry is late.") {
		t.Fatalf("a result that brings outside content back reads %q", res)
	}
	if a := sv.subagents(g.test.ID, noticed)[0]; !a.Outside || !a.Passed {
		t.Errorf("a sub-agent that read outside content reads %+v", a)
	}
	marked := 0
	g.with(g.test.ID, func(s *tenant.Store) {
		s.DB().QueryRow("SELECT count(*) FROM tool_outside WHERE chat_id = ? AND tool = ?", noticed, ToolName).Scan(&marked)
	})
	if marked != 1 {
		t.Fatalf("the chat a sub-agent brought outside content back to has %d marks", marked)
	}

	// 3. And goes down: with light_lamp set to always, a sub-agent of that
	// chat still asks, because the chat read outside content; one of a clean
	// chat does not.
	sv.call(g.test.ID, "PUT", "tools/light_lamp/policy", `{"policy":"always"}`, http.StatusOK, nil)
	done = sv.sendAsync(g.test.ID, noticed, "Light the lamp.")
	g.waitFor("the sub-agent of the marked chat to ask", func() bool {
		subs := sv.subagents(g.test.ID, noticed)
		if len(subs) == 2 && subs[1].Waiting {
			waiting = subs[1]
			return true
		}
		return false
	})
	sv.call(g.test.ID, "GET", "chats/"+waiting.WorkChatID+"/turns/"+waiting.WorkTurnID+"/approvals", "", http.StatusOK, &asked)
	if len(asked.Approvals) != 1 || asked.Approvals[0].Reason != "outside" {
		t.Fatalf("the sub-agent of a chat that read outside content asked %+v", asked.Approvals)
	}
	sv.call(g.test.ID, "POST", "chats/"+waiting.WorkChatID+"/turns/"+waiting.WorkTurnID+"/approvals/"+asked.Approvals[0].ID+"/decline", "", http.StatusOK, nil)
	sv.wait(done, "the turn to end after the decline")
	if lit.Load() != 1 {
		t.Fatalf("a declined call lit the lamp")
	}
	clean := sv.newChat(g.test.ID)
	sv.send(g.test.ID, clean, "Light the lamp.", 20*time.Second)
	if a := sv.subagents(g.test.ID, clean)[0]; a.Status != StatusDone || lit.Load() != 2 {
		t.Fatalf("with the policy always, a clean chat's sub-agent reads %+v, and the lamp was lit %d times", a, lit.Load())
	}
}

// A sub-agent of a held chat is held (fixed rule 3), and the result of a
// sub-agent that was sent sensitive content reaches only a held parent: a
// parent that is not held is told it stays in the sub-agent's chat.
func TestSensitiveContentStaysWhereItMayBeRead(t *testing.T) {
	g := newRig(t)
	object := json.RawMessage(`{"type":"object"}`)
	// A tool that stands for a lookup that gave the chat sensitive content:
	// it holds the chat its turn runs in.
	g.tools = []tools.Tool{{Name: "read_private_note", Description: "Reads a note from a private space.", InputSchema: object, ReadOnly: true, Inside: true,
		Run: func(ctx context.Context, in tools.Input) (string, error) {
			if err := recall.Hold(ctx, in.Store, runningTurn(ctx, in.Store, in.ChatID, ""), in.ChatID); err != nil {
				return "", err
			}
			return "The spare key is under the blue pot.", nil
		}}}
	sc, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	sub := func(_ context.Context, task string, req seam.Request, emit func(seam.Event) error) error {
		if res, ok := lastResult(req); ok {
			return say(emit, req.Model, "Found: "+strings.TrimSpace(res))
		}
		if strings.Contains(task, "private") {
			return ask(emit, req.Model, "note-1", "read_private_note", map[string]any{})
		}
		return say(emit, req.Model, "Nothing private here.")
	}
	model := &director{brief: sc.Brief, done: "Back.", sub: sub, asks: map[string]toolCall{
		"Check the private note.": {ToolName, agentsInput("Read the private note.")},
		"Check the tide.":         {ToolName, agentsInput("Check the tide table.")},
	}}
	sv := g.serve(model)

	// A chat that is not held is not given a held sub-agent's result.
	chatID := sv.newChat(g.test.ID)
	sv.send(g.test.ID, chatID, "Check the private note.", 20*time.Second)
	a := sv.subagents(g.test.ID, chatID)[0]
	if a.Status != StatusDone || !a.Sensitive || a.Passed {
		t.Fatalf("a sub-agent that was sent sensitive content reads %+v", a)
	}
	res, _ := toolResult(sv.messages(g.test.ID, chatID), ToolName)
	if strings.Contains(res, "blue pot") || !strings.Contains(res, "stays in its own chat") {
		t.Fatalf("a parent that is not held was told %q", res)
	}
	for _, req := range model.parentCalls() {
		if strings.Contains(userText(req), "blue pot") {
			t.Fatalf("the sensitive result reached a call of the parent")
		}
	}

	// A sub-agent of a held chat is held, and a held parent is given the
	// held result.
	held := sv.newChat(g.test.ID)
	sv.send(g.test.ID, held, "Check the tide.", 20*time.Second) // a first turn to hold the chat under
	g.with(g.test.ID, func(s *tenant.Store) {
		var turnID string
		if err := s.DB().QueryRow("SELECT id FROM turns WHERE chat_id = ? ORDER BY seq LIMIT 1", held).Scan(&turnID); err != nil {
			t.Fatal(err)
		}
		if err := recall.Hold(context.Background(), s, turnID, held); err != nil {
			t.Fatal(err)
		}
	})
	sv.send(g.test.ID, held, "Check the private note.", 20*time.Second)
	subs := sv.subagents(g.test.ID, held)
	if len(subs) != 2 {
		t.Fatalf("%d sub-agents in the held chat", len(subs))
	}
	if a := subs[1]; !a.Sensitive || !a.Passed || a.Status != StatusDone {
		t.Fatalf("a held chat's sub-agent reads %+v", a)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		if on, err := recall.Held(context.Background(), s, subs[1].WorkChatID); err != nil || !on {
			t.Errorf("a held chat's sub-agent's chat is not held (%v)", err)
		}
	})
	msgs := sv.messages(g.test.ID, held)
	var last string
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == seam.BlockToolResult {
				last = seam.Text(seam.Message{Blocks: b.Content})
			}
		}
	}
	if !strings.Contains(last, "blue pot") {
		t.Errorf("a held parent was not given the held result: %q", last)
	}
}
