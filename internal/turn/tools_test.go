package turn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/builtin"
	"github.com/jonathanmkosar/keystone/settings"
)

// The tool loop's tests (P7-01). They use the rig of turn_test.go, with a
// server that is given a registry of tools.

// serveTools starts a server as serve does, with tools. A nil provider is
// the fake playing the shipped recorded turns. A nil registry is the
// built-in tools under the shipped settings. tune may change the settings
// the server runs with.
func (g *rig) serveTools(provider seam.Provider, reg *tools.Registry, tune func(*Config)) *served {
	g.t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 50 * time.Millisecond
	if tune != nil {
		tune(&cfg)
	}
	if provider == nil {
		if provider, err = testtenant.Provider(g.fx, fake.Options{DeltaSize: 12}); err != nil {
			g.t.Fatal(err)
		}
	}
	if reg == nil {
		reg = tools.New()
		if err := builtin.Add(reg, builtin.Options{Limits: cfg.Limits, ReadChatMessages: cfg.ToolLoop.ReadChatMessages}); err != nil {
			g.t.Fatal(err)
		}
	}
	set, err := adapters.Fake(g.fx.Scripts, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	for name, build := range g.extra {
		set[name] = build
	}
	runner := NewRunner(Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Adapters: set, Tools: reg})
	runner.Recover()
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, append(Routes(runner), ApprovalRoutes(runner)...))
	if err != nil {
		g.t.Fatal(err)
	}
	sv := &served{g: g, runner: runner, server: httptest.NewServer(h), tokens: map[string]string{}}
	g.t.Cleanup(sv.stop)
	for id, password := range map[string]string{g.test.ID: g.fx.Password, g.ordinary.ID: ordinaryPassword} {
		g.with(id, func(s *tenant.Store) {
			token, _, err := signin.SignIn(s, password, g.signIn)
			if err != nil {
				g.t.Fatal(err)
			}
			sv.tokens[id] = token
		})
	}
	return sv
}

// scripted returns a fake provider playing the scripts of one file.
func scripted(t *testing.T, file string) seam.Provider {
	t.Helper()
	scripts, err := fake.Decode([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	p, err := fake.New(scripts, fake.Options{DeltaSize: 12})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// calls is a script file in which the model asks for the named tools, one
// in each step, then answers. Any other message gets a plain answer.
func calls(names ...string) string {
	var steps []string
	for i, name := range names {
		steps = append(steps, `{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_`+itoa(i+1)+`","name":"`+name+`","input":{"n":`+itoa(i+1)+`}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}}`)
	}
	steps = append(steps, `{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"All done."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}`)
	return `{"test_data":true,"scripts":[
		{"name":"a task","when":{"user_text":"Do the task."},"steps":[` + strings.Join(steps, ",") + `]},
		{"name":"anything else","when":{},"steps":[{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"A plain answer."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]}
	]}`
}

var anyInput = json.RawMessage(`{"type":"object","properties":{}}`)

// counting is a tool that counts its runs and gives back a fixed text.
func counting(name string, readOnly bool, ran *atomic.Int32, run func(context.Context) (string, error)) tools.Tool {
	return tools.Tool{Name: name, Description: "A tool for a test.", InputSchema: anyInput, ReadOnly: readOnly, Run: func(ctx context.Context, _ tools.Input) (string, error) {
		ran.Add(1)
		if run != nil {
			return run(ctx)
		}
		return "ran " + name, nil
	}}
}

func registry(t *testing.T, all ...tools.Tool) *tools.Registry {
	t.Helper()
	r := tools.New()
	for _, tool := range all {
		if err := r.Add(tool); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// results reads the tool_result events of a stream, by the call's id.
func results(t *testing.T, events []event) map[string]ToolResultEvent {
	t.Helper()
	out := map[string]ToolResultEvent{}
	for _, e := range events {
		if e.name == EventToolResult {
			var r ToolResultEvent
			e.into(t, &r)
			out[r.ID] = r
		}
	}
	return out
}

func said(r ToolResultEvent) string { return seam.Text(seam.Message{Blocks: r.Content}) }

func (g *rig) toolEntries(requestID string) []audit.Entry {
	g.t.Helper()
	var got []audit.Entry
	g.with(g.test.ID, func(s *tenant.Store) {
		var err error
		if got, err = audit.List(context.Background(), s, audit.Filter{Kind: audit.KindTool, RequestID: requestID}); err != nil {
			g.t.Fatal(err)
		}
	})
	return got
}

const severalSteps = "What have I asked you about the lighthouse, and what is the date today?"

// The item's proof: a scripted task of several steps completes. The fake
// model searches the person's chats, reads the result, asks for the time,
// reads that, and answers. Each step is in the stream as it happens, in
// the chat record, and in the audit log under the turn's request id.
func TestAScriptedTaskOfSeveralStepsCompletes(t *testing.T) {
	g := newRig(t)
	sv := g.serveTools(nil, nil, nil)
	id := sv.newChat(g.test.ID)

	body, _ := json.Marshal(SendBody{Text: severalSteps})
	events, turnID, requestID := sv.send(g.test.ID, id, string(body))
	want := "turn turn message " +
		"start text tool_call message usage tool_result message " +
		"start text tool_call message usage tool_result message " +
		"start text message usage title end"
	if got := names(events); got != want {
		t.Fatalf("the stream was\n\t%s\nwant\n\t%s", got, want)
	}
	if end := last(t, events); end.Status != StatusDone || end.StopReason != string(seam.StopEnd) || end.Error != nil || end.ID != turnID {
		t.Errorf("the turn ended as %+v", end)
	}

	// Each call names its tool and its step, and each result is the tool's own.
	var called []string
	for _, e := range events {
		if e.name == EventToolCall {
			var c ToolCallEvent
			e.into(t, &c)
			called = append(called, itoa(c.Step)+":"+c.Name)
		}
	}
	if got := strings.Join(called, " "); got != "1:search_chats 2:current_time" {
		t.Errorf("the calls were %s", got)
	}
	got := results(t, events)
	if r := got["call_search"]; r.IsError || !strings.Contains(said(r), "Planning the lighthouse walk") || strings.Contains(said(r), id) {
		t.Errorf("the search gave %+v", r)
	}
	if r := got["call_time"]; r.IsError || !strings.Contains(said(r), "In UTC it is "+time.Now().UTC().Format("2006-01-02")) {
		t.Errorf("the time gave %+v", r)
	}

	// The record holds every step, so a reload shows them.
	var kept ChatReply
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &kept)
	var shape []string
	for _, m := range kept.Messages {
		for _, b := range m.Blocks {
			shape = append(shape, string(m.Role)+":"+string(b.Type))
		}
	}
	if got, want := strings.Join(shape, " "), "user:text assistant:text assistant:tool_call user:tool_result assistant:text assistant:tool_call user:tool_result assistant:text"; got != want {
		t.Errorf("the record holds\n\t%s\nwant\n\t%s", got, want)
	}
	if r := kept.Messages[2].Blocks[0]; r.ID != "call_search" || !strings.Contains(seam.Text(seam.Message{Blocks: r.Content}), "Planning the lighthouse walk") {
		t.Errorf("the stored search result is %+v", r)
	}

	// The audit log: three model calls and two tool calls, each before
	// and after, under the request id of the call that sent the message.
	entries := g.toolEntries(requestID)
	var outcomes []string
	for _, e := range entries {
		outcomes = append(outcomes, e.Action+":"+string(e.Outcome))
	}
	if got, want := strings.Join(outcomes, " "), "search_chats:started search_chats:ok current_time:started current_time:ok"; got != want {
		t.Fatalf("the tool calls are recorded as\n\t%s\nwant\n\t%s", got, want)
	}
	if entries[0].Sent != `{"query":"lighthouse"}` || !strings.Contains(entries[1].Received, "Planning the lighthouse walk") || entries[0].Actor == "" {
		t.Errorf("the search is recorded as sent %q, received %q", entries[0].Sent, entries[1].Received)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		modelCalls, err := audit.List(context.Background(), s, audit.Filter{Kind: audit.KindModel, RequestID: requestID})
		if err != nil {
			t.Fatal(err)
		}
		if len(modelCalls) != 6 {
			t.Fatalf("the turn's model calls left %d entries, want 6", len(modelCalls))
		}
		// What the model was sent names the tools on offer, in order of name.
		var sent seam.Request
		if err := json.Unmarshal([]byte(modelCalls[0].Sent), &sent); err != nil {
			t.Fatal(err)
		}
		var offered []string
		for _, tool := range sent.Tools {
			offered = append(offered, tool.Name)
		}
		if got := strings.Join(offered, " "); got != "current_time read_chat search_chats" {
			t.Errorf("the model was offered %q", got)
		}
		// The context builder counted them.
		ctxs, err := Contexts(context.Background(), s, turnID)
		if err != nil || len(ctxs) != 3 {
			t.Fatalf("%d context records, %v", len(ctxs), err)
		}
		for _, p := range ctxs[0].Record.Parts {
			if p.Part == "tools" && (p.Tokens == 0 || p.Kept != 3) {
				t.Errorf("the tools part of the context is %+v", p)
			}
		}
	})
}

// A person who switches tools off is offered none, and a call the model
// makes anyway is declined, recorded, and answered.
func TestToolsCanBeSwitchedOff(t *testing.T) {
	g := newRig(t)
	g.with(g.test.ID, func(s *tenant.Store) {
		reg, err := settings.Registry()
		if err != nil {
			t.Fatal(err)
		}
		if err := tenantsettings.Set(context.Background(), s, reg, tenantsettings.Person, KeyToolsEnabled, "false"); err != nil {
			t.Fatal(err)
		}
	})
	var ran atomic.Int32
	sv := g.serveTools(scripted(t, calls("count")), registry(t, counting("count", true, &ran, nil)), nil)
	id := sv.newChat(g.test.ID)
	events, _, requestID := sv.send(g.test.ID, id, `{"text":"Do the task."}`)
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	if r := results(t, events)["call_1"]; !r.IsError || !strings.Contains(said(r), "switched off") || ran.Load() != 0 {
		t.Errorf("with tools off the call gave %+v after %d runs", r, ran.Load())
	}
	entries := g.toolEntries(requestID)
	if len(entries) != 2 || entries[1].Outcome != audit.OutcomeRefused {
		t.Errorf("the declined call is recorded as %+v", entries)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		modelCalls, _ := audit.List(context.Background(), s, audit.Filter{Kind: audit.KindModel, RequestID: requestID})
		if len(modelCalls) == 0 || strings.Contains(modelCalls[0].Sent, `"tools"`) {
			t.Error("with tools off the model was still offered tools")
		}
	})
}

// At the step limit the turn ends cleanly: the last calls are answered and
// not run, a note the person can read is the turn's last message, and the
// chat can go on.
func TestATurnEndsAtItsStepLimitWithANote(t *testing.T) {
	g := newRig(t)
	var ran atomic.Int32
	sv := g.serveTools(scripted(t, calls("count", "count", "count")), registry(t, counting("count", true, &ran, nil)), func(c *Config) { c.MaxSteps = 2 })
	id := sv.newChat(g.test.ID)
	events, _, requestID := sv.send(g.test.ID, id, `{"text":"Do the task."}`)

	want := "turn turn message " +
		"start tool_call message usage tool_result message " +
		"start tool_call message usage tool_result message message " +
		"title end"
	if got := names(events); got != want {
		t.Fatalf("the stream was\n\t%s\nwant\n\t%s", got, want)
	}
	end := last(t, events)
	if end.Status != StatusDone || end.Error != nil || end.StopReason != StopStepLimit {
		t.Errorf("a turn at its step limit ended as %+v", end)
	}
	if ran.Load() != 1 {
		t.Errorf("the tool ran %d times, and only the first step's call may run", ran.Load())
	}
	got := results(t, events)
	if r := got["call_1"]; r.IsError || said(r) != "ran count" {
		t.Errorf("the first call gave %+v", r)
	}
	if r := got["call_2"]; !r.IsError || !strings.Contains(said(r), "most steps") {
		t.Errorf("the call at the limit gave %+v", r)
	}
	var kept ChatReply
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &kept)
	note := kept.Messages[len(kept.Messages)-1]
	if len(kept.Messages) != 6 || note.Role != seam.RoleAssistant || note.StopReason != StopStepLimit || note.ID != end.ReplyMessageID ||
		!strings.Contains(note.Blocks[0].Text, "reached the most steps one turn may take") {
		t.Errorf("the chat ends with %+v", note)
	}
	if entries := g.toolEntries(requestID); len(entries) != 4 || entries[1].Outcome != audit.OutcomeOK || entries[3].Outcome != audit.OutcomeRefused {
		t.Errorf("the calls are recorded as %+v", entries)
	}
	// The record is still one a provider takes.
	events, _, _ = sv.send(g.test.ID, id, `{"text":"And now?"}`)
	if end := last(t, events); end.Status != StatusDone || joined(t, events, EventText) != "A plain answer." {
		t.Errorf("the message after a step limit ended as %+v", end)
	}
}

// Fixed rule 2 in a turn: a tool that changes something does not run
// without a person's approval. Since P7-02 it asks; declined, its function
// never runs, the model is told why, and the turn goes on.
func TestAToolThatChangesSomethingDoesNotRunInATurn(t *testing.T) {
	g := newRig(t)
	var ran atomic.Int32
	sv := g.serveTools(nil, registry(t, counting("send_postcard", false, &ran, nil)), nil)
	id := sv.newChat(g.test.ID)
	events, _, requestID := sv.sendAnswering(g.test.ID, id, `{"text":"Send a postcard to the lighthouse keeper."}`, "decline", func(Approval) {
		if ran.Load() != 0 {
			t.Errorf("a tool that changes something ran %d times while it waited for an answer", ran.Load())
		}
	})
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	if ran.Load() != 0 {
		t.Fatalf("a tool that changes something ran %d times with no approval", ran.Load())
	}
	if r := results(t, events)["call_postcard"]; !r.IsError || !strings.Contains(said(r), "approval") || !strings.Contains(said(r), "was not run") {
		t.Errorf("the model was told %+v", r)
	}
	if entries := g.toolEntries(requestID); len(entries) != 2 || entries[0].Action != "send_postcard" || entries[1].Outcome != audit.OutcomeRefused {
		t.Errorf("the refusal is recorded as %+v", entries)
	}
	if !strings.Contains(joined(t, events, EventText), "No postcard was sent.") {
		t.Error("the model was not called again after the refusal")
	}
}

// A tool that fails or panics becomes an error result, not a broken turn.
func TestAToolThatBreaksDoesNotBreakTheTurn(t *testing.T) {
	g := newRig(t)
	var ran atomic.Int32
	breaks := counting("breaks", true, &ran, func(context.Context) (string, error) { panic("a slip on the quay") })
	fails := counting("fails", true, &ran, func(context.Context) (string, error) { return "", context.DeadlineExceeded })
	sv := g.serveTools(scripted(t, calls("breaks", "fails", "missing")), registry(t, breaks, fails), nil)
	id := sv.newChat(g.test.ID)
	events, _, requestID := sv.send(g.test.ID, id, `{"text":"Do the task."}`)
	if end := last(t, events); end.Status != StatusDone || end.Error != nil {
		t.Fatalf("the turn ended as %+v", end)
	}
	got := results(t, events)
	for id, says := range map[string]string{"call_1": "broke while it ran", "call_2": "Context deadline exceeded.", "call_3": `no tool named "missing"`} {
		if r := got[id]; !r.IsError || !strings.Contains(said(r), says) || strings.Contains(said(r), "quay") {
			t.Errorf("%s gave %+v", id, r)
		}
	}
	if joined(t, events, EventText) != "All done." || ran.Load() != 2 {
		t.Errorf("after %d runs the reply was %q", ran.Load(), joined(t, events, EventText))
	}
	if entries := g.toolEntries(requestID); len(entries) != 6 {
		t.Errorf("three calls left %d audit entries, want 6", len(entries))
	}
}

// Stop cancels a running tool. The turn ends as stopped, and the call is
// in the record with a result that says it was stopped.
func TestStopCancelsARunningTool(t *testing.T) {
	g := newRig(t)
	var ran atomic.Int32
	started, cancelled := make(chan struct{}), make(chan struct{})
	waits := counting("waits", true, &ran, func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return "", ctx.Err()
	})
	// Two calls in one reply: the second must not run after the stop.
	file := `{"test_data":true,"scripts":[{"name":"a task","when":{},"steps":[{"reply":{"message":{"role":"assistant","blocks":[
		{"type":"tool_call","id":"call_1","name":"waits","input":{}},
		{"type":"tool_call","id":"call_2","name":"waits","input":{}}
	]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}}]}]}`
	sv := g.serveTools(scripted(t, file), registry(t, waits), nil)
	id := sv.newChat(g.test.ID)

	resp := sv.request(g.test.ID, "POST", "chats/"+id+"/turns", `{"text":"Wait for me."}`, nil)
	defer resp.Body.Close()
	turnID := resp.Header.Get("X-Turn-Id")
	done := make(chan []event, 1)
	go func() { done <- readEvents(t, resp.Body, nil) }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the tool was never run")
	}
	var stopped Turn
	sv.call(g.test.ID, "POST", "chats/"+id+"/turns/"+turnID+"/stop", "", http.StatusOK, &stopped)
	select {
	case <-cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("stop did not cancel the tool")
	}
	if stopped.Status != StatusStopped {
		t.Errorf("the stop route answered %+v", stopped)
	}
	var events []event
	select {
	case events = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream did not close after the stop")
	}
	if got, want := names(events), "turn turn message start tool_call tool_call message usage tool_result tool_result message end"; got != want {
		t.Fatalf("the stopped stream was\n\t%s\nwant\n\t%s", got, want)
	}
	if end := last(t, events); end.Status != StatusStopped || end.Error != nil {
		t.Errorf("the stopped turn ended as %+v", end)
	}
	got := results(t, events)
	if r := got["call_1"]; !r.IsError || !strings.Contains(said(r), "stopped while this tool was running") {
		t.Errorf("the stopped tool gave %+v", r)
	}
	if r := got["call_2"]; !r.IsError || !strings.Contains(said(r), "stopped before this tool ran") || ran.Load() != 1 {
		t.Errorf("the call after the stop gave %+v after %d runs", r, ran.Load())
	}
	var kept ChatReply
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &kept)
	if len(kept.Messages) != 3 || len(kept.Messages[2].Blocks) != 2 || kept.Messages[2].Blocks[0].Type != seam.BlockToolResult {
		t.Errorf("after the stop the chat holds %+v", kept.Messages)
	}
}

// One person's tools never reach another person's chats: the ordinary
// tenant's turn cannot be given the test tenant's chat by its id.
func TestAToolReadsOnlyItsOwnTenant(t *testing.T) {
	g := newRig(t)
	var theirs string
	g.with(g.test.ID, func(s *tenant.Store) {
		if err := s.DB().QueryRow("SELECT id FROM chats LIMIT 1").Scan(&theirs); err != nil {
			t.Fatal(err)
		}
	})
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.New()
	if err := builtin.Add(reg, builtin.Options{ReadChatMessages: cfg.ToolLoop.ReadChatMessages, Limits: cfg.Limits}); err != nil {
		t.Fatal(err)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		for name, input := range map[string]string{builtin.ReadChat: `{"chat_id":"` + theirs + `"}`, builtin.SearchChats: `{"query":"lighthouse"}`} {
			res := reg.Run(context.Background(), tools.Call{Store: s, RequestID: "req-1", Actor: "person", ID: "c", Name: name, Input: json.RawMessage(input)})
			text := seam.Text(seam.Message{Blocks: res.Block.Content})
			if strings.Contains(text, "Planning the lighthouse walk") || strings.Contains(text, "windproof") {
				t.Errorf("%s reached another person's chat: %q", name, text)
			}
		}
	})
}
