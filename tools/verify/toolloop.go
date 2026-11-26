package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/approval"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// The tool loop's parts of verify (P7-01).
//
//   - toolCallsRecorded is the tool calls' part of "calls recorded": the
//     test tenant's scripted task of several steps runs on the real server,
//     and each tool call must be in the audit log before and after, under
//     the request id of the call that sent the message, and in the chat
//     record.
//   - checkApproval is the standing check "approval", for the second fixed
//     rule: a tool that changes something is registered on the real server
//     and the model calls it, and it must not run.
//
// Both run the server as it is put together in internal/app, with the
// shipped settings, on a throwaway data folder, as the test tenant, whose
// model is always its recorded turns in fixtures/test-tenant/turns.

// The messages of fixtures/test-tenant/turns/tool-steps.json.
const (
	toolStepsMessage = "What have I asked you about the lighthouse, and what is the date today?"
	postcardMessage  = "Send a postcard to the lighthouse keeper."
	postcardTool     = "send_postcard"
)

// toolRig is the real server with the test tenant loaded beside an
// ordinary tenant, and the test tenant signed in.
type toolRig struct {
	w     *walker
	test  *tenant.Store
	other *tenant.Store
	token string
	close func()
}

// openToolRig starts the server. reg is the registry of tools it runs
// with: nil means the server's own, as app.Tools makes it.
func openToolRig(reg *tools.Registry) (rig *toolRig, err error) {
	dir, err := os.MkdirTemp("", "keystone-tool-loop-")
	if err != nil {
		return nil, err
	}
	var undo []func()
	done := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		os.RemoveAll(dir)
	}
	defer func() {
		if err != nil {
			done()
		}
	}()
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	other, err := in.Create("Ordinary Person")
	if err != nil {
		return nil, err
	}
	fx, err := testtenant.Shipped()
	if err != nil {
		return nil, err
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		return nil, err
	}
	loaded, err := testtenant.Load(in, fx, o)
	if err != nil {
		return nil, err
	}
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log, c.Tools = in, w.log, reg
	h, stop, err := app.Open(c)
	if err != nil {
		return nil, err
	}
	// The goroutine that ran a turn has the tenant's database open until
	// it is done. It is waited for before the folder is removed.
	w.h, undo = h, append(undo, stop)
	rig = &toolRig{w: w}
	if rig.test, err = in.Store(loaded.Tenant.ID); err != nil {
		return nil, err
	}
	undo = append(undo, func() { rig.test.Close() })
	if rig.other, err = in.Store(other.ID); err != nil {
		return nil, err
	}
	undo = append(undo, func() { rig.other.Close() })
	token, rec := w.signIn(loaded.Tenant.ID, fx.Password)
	if token == "" {
		return nil, fmt.Errorf("the test tenant cannot sign in through the API: status %d", rec.Code)
	}
	rig.token, rig.close = token, done
	return rig, nil
}

// sseEvent is one event of a stream, as it was sent.
type sseEvent struct {
	name string
	data string
}

func readStream(stream string) []sseEvent {
	var out []sseEvent
	for _, chunk := range strings.Split(strings.ReplaceAll(stream, "\r\n", "\n"), "\n\n") {
		var e sseEvent
		for _, line := range strings.Split(chunk, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				e.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				e.data = strings.TrimPrefix(line, "data: ")
			}
		}
		if e.name != "" {
			out = append(out, e)
		}
	}
	return out
}

// sent is one message sent as the test tenant in a new chat.
type sent struct {
	chatID    string
	requestID string
	events    []sseEvent
	end       turn.Turn
	calls     []turn.ToolCallEvent
	results   map[string]turn.ToolResultEvent
}

// send sends text as the first message of a new chat and reads the stream
// to its end.
func (r *toolRig) send(text string) (sent, error) {
	rec := r.w.request("POST", api.Prefix+"chats", r.token, "", nil)
	var made turn.ChatReply
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil || made.Chat.ID == "" {
		return sent{}, fmt.Errorf("the test tenant cannot make a chat through the API: status %d", rec.Code)
	}
	body, _ := json.Marshal(turn.SendBody{Text: text})
	rec = r.w.request("POST", api.Prefix+"chats/"+made.Chat.ID+"/turns", r.token, string(body), nil)
	out := sent{chatID: made.Chat.ID, requestID: rec.Header().Get("X-Request-Id"), events: readStream(rec.Body.String()), results: map[string]turn.ToolResultEvent{}}
	if rec.Code != http.StatusOK || len(out.events) == 0 || out.events[len(out.events)-1].name != turn.EventEnd {
		return sent{}, fmt.Errorf("the turn for %q did not stream to its end: status %d", text, rec.Code)
	}
	for _, e := range out.events {
		var err error
		switch e.name {
		case turn.EventEnd:
			err = json.Unmarshal([]byte(e.data), &out.end)
		case turn.EventToolCall:
			var c turn.ToolCallEvent
			err = json.Unmarshal([]byte(e.data), &c)
			out.calls = append(out.calls, c)
		case turn.EventToolResult:
			var res turn.ToolResultEvent
			err = json.Unmarshal([]byte(e.data), &res)
			out.results[res.ID] = res
		}
		if err != nil {
			return sent{}, fmt.Errorf("the %s event of the turn for %q is not its shape: %w", e.name, text, err)
		}
	}
	return out, nil
}

func resultText(r turn.ToolResultEvent) string { return seam.Text(seam.Message{Blocks: r.Content}) }

// entriesOf returns one kind of the test tenant's audit entries for one
// request.
func (r *toolRig) entriesOf(kind audit.Kind, requestID string) ([]audit.Entry, error) {
	return audit.List(context.Background(), r.test, audit.Filter{Kind: kind, RequestID: requestID})
}

// toolCallsRecorded runs the test tenant's scripted task of several steps:
// the fake model searches the person's chats, reads the result, asks for
// the time, reads that, and answers.
func toolCallsRecorded() (problems []string, err error) {
	r, err := openToolRig(nil)
	if err != nil {
		return nil, err
	}
	defer r.close()
	got, err := r.send(toolStepsMessage)
	if err != nil {
		return nil, err
	}
	if got.end.Status != turn.StatusDone || got.end.StopReason != string(seam.StopEnd) {
		problems = append(problems, fmt.Sprintf("the test tenant's task of several steps ended as %s with the stop reason %q", got.end.Status, got.end.StopReason))
	}
	var called []string
	for _, c := range got.calls {
		called = append(called, c.Name)
		res, ok := got.results[c.ID]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("the call to %s has no tool_result in the stream", c.Name))
		case res.IsError || strings.TrimSpace(resultText(res)) == "":
			problems = append(problems, fmt.Sprintf("the call to %s gave an error or nothing: %q", c.Name, resultText(res)))
		}
	}
	if strings.Join(called, " ") != "search_chats current_time" {
		problems = append(problems, fmt.Sprintf("the task of several steps called %q, and the recorded turns call search_chats, then current_time", strings.Join(called, " ")))
	}

	// The audit log: each tool call before and after, under the request id.
	entries, err := r.entriesOf(audit.KindTool, got.requestID)
	if err != nil {
		return nil, err
	}
	if len(entries) != 2*len(got.calls) {
		problems = append(problems, fmt.Sprintf("%d tool calls left %d audit entries under the request id of the call that sent the message, want one before each call and one for its end", len(got.calls), len(entries)))
	} else {
		for i, c := range got.calls {
			before, after := entries[2*i], entries[2*i+1]
			switch {
			case before.Action != c.Name || after.Action != c.Name:
				problems = append(problems, fmt.Sprintf("the call to %s is recorded as %q and %q", c.Name, before.Action, after.Action))
			case before.Outcome != audit.OutcomeStarted || before.Sent == "":
				problems = append(problems, fmt.Sprintf("the call to %s was not recorded before it ran, with what it was sent", c.Name))
			case after.Outcome != audit.OutcomeOK || after.Received == "":
				problems = append(problems, fmt.Sprintf("the end of the call to %s is recorded as %s, with %d bytes of what came back", c.Name, after.Outcome, len(after.Received)))
			}
		}
	}
	modelCalls, err := r.entriesOf(audit.KindModel, got.requestID)
	if err != nil {
		return nil, err
	}
	if steps := len(got.calls) + 1; len(modelCalls) != 2*steps {
		problems = append(problems, fmt.Sprintf("a task of %d model calls left %d model entries, want two for each", steps, len(modelCalls)))
	}

	// The chat record holds every step, so a reload shows them.
	rec := r.w.request("GET", api.Prefix+"chats/"+got.chatID, r.token, "", nil)
	var kept turn.ChatReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &kept) != nil {
		return nil, fmt.Errorf("the chat of the task cannot be read back: status %d", rec.Code)
	}
	stored := map[seam.BlockType]int{}
	for _, m := range kept.Messages {
		for _, b := range m.Blocks {
			stored[b.Type]++
		}
	}
	if stored[seam.BlockToolCall] != len(got.calls) || stored[seam.BlockToolResult] != len(got.calls) {
		problems = append(problems, fmt.Sprintf("the chat record holds %d tool calls and %d results, and the stream had %d of each", stored[seam.BlockToolCall], stored[seam.BlockToolResult], len(got.calls)))
	}

	if theirs, err := logOf(r.other); err != nil {
		return nil, err
	} else if len(theirs) != 0 {
		problems = append(problems, fmt.Sprintf("the test tenant's tool calls wrote %d entries to another tenant's audit log", len(theirs)))
	}
	return problems, nil
}

// checkApproval is the standing check "approval": a tool that changes
// something cannot run without a person's approval, and until approvals
// are built (P7-02) it cannot run at all.
//
//  1. The rule's own test refuses a tool that changes something with no
//     approval, and lets one that only reads run.
//  2. The real server is given its own tools and one more that changes
//     something, send_postcard, which counts its runs. The test tenant's
//     recorded turn calls it. The tool must not run, the model must be
//     told why, the refusal must be in the audit log, and the turn must
//     end as done with the model's answer.
func checkApproval(string) error {
	var problems []string

	// 1. The rule itself.
	if approval.Check(false, approval.Approval{}) == nil {
		problems = append(problems, "approval.Check lets a tool that changes something run with no approval")
	}
	if err := approval.Check(true, approval.Approval{}); err != nil {
		problems = append(problems, "approval.Check refuses a tool that only reads: "+err.Error())
	}

	// 2. On the real server.
	c, err := app.Defaults()
	if err != nil {
		return err
	}
	reg, err := app.Tools(c)
	if err != nil {
		return err
	}
	var ran atomic.Int32
	err = reg.Add(tools.Tool{
		Name:        postcardTool,
		Description: "Sends a postcard. A tool for the approval check, which must never run.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"to":{"type":"string"},"message":{"type":"string"}}}`),
		ReadOnly:    false,
		Run: func(context.Context, tools.Input) (string, error) {
			ran.Add(1)
			return "The postcard was sent.", nil
		},
	})
	if err != nil {
		return err
	}
	r, err := openToolRig(reg)
	if err != nil {
		return err
	}
	defer r.close()
	// Since P7-02 the call asks and the turn waits for the person. They
	// decline while it waits, so the call still has no approval, and the
	// parts below read as they did.
	answered := answerWhenAsked(r.w, r.token, r.test, "decline", &ran)
	got, err := r.send(postcardMessage)
	if err != nil {
		return err
	}
	if asked, ranWhileWaiting, status := answered(); asked.ID == "" || asked.Tool != postcardTool || ranWhileWaiting != 0 || status != http.StatusOK {
		problems = append(problems, fmt.Sprintf("a tool that changes something did not ask and wait for the person: asked %q, %d runs while it waited, the decline answered %d", asked.Tool, ranWhileWaiting, status))
	}
	if n := ran.Load(); n != 0 {
		problems = append(problems, fmt.Sprintf("a tool that changes something ran %d times with no approval", n))
	}
	var call *turn.ToolCallEvent
	for i := range got.calls {
		if got.calls[i].Name == postcardTool {
			call = &got.calls[i]
		}
	}
	if call == nil {
		return errors.New("the test tenant's recorded turn did not call " + postcardTool + ", so the approval rule was not tried")
	}
	if res, ok := got.results[call.ID]; !ok || !res.IsError || !strings.Contains(resultText(res), "approval") {
		problems = append(problems, fmt.Sprintf("the model was not told the tool needs an approval: %q", resultText(res)))
	}
	entries, err := r.entriesOf(audit.KindTool, got.requestID)
	if err != nil {
		return err
	}
	if len(entries) != 2 || entries[0].Action != postcardTool || entries[1].Outcome != audit.OutcomeRefused {
		problems = append(problems, fmt.Sprintf("the refused call left %d audit entries, want one before it and one that says it was refused", len(entries)))
	}
	if got.end.Status != turn.StatusDone {
		problems = append(problems, fmt.Sprintf("the turn with the refused call ended as %s, and a refusal is a result the model reads, not a broken turn", got.end.Status))
	}

	// 3. The approval policy (P7-02, approvals.go): a side effect after
	// outside content always asks, whatever the tool's setting.
	more, err := checkApprovalPolicy()
	if err != nil {
		return err
	}
	problems = append(problems, more...)
	// A call that reaches other people asks under every policy (P12-02). See google.go.
	problems = append(problems, googleRule()...)

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}
