package turn

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/settings"
)

// The approval policy's tests (P7-02). Every name and text is invented.

// sendAnswering sends a message and reads the whole stream, answering each
// approval that asks with the decision ("approve" or "decline"), or with
// none when it is empty. each, when not nil, sees each pending approval
// before it is answered.
func (sv *served) sendAnswering(tenantID, chatID, body, decision string, each func(Approval)) (events []event, turnID, requestID string) {
	sv.g.t.Helper()
	resp := sv.request(tenantID, "POST", "chats/"+chatID+"/turns", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		sv.g.t.Fatalf("sending a message answered %d: %s", resp.StatusCode, raw)
	}
	turnID = resp.Header.Get("X-Turn-Id")
	events = readEvents(sv.g.t, resp.Body, func(e event) {
		if e.name != EventApproval {
			return
		}
		var a Approval
		e.into(sv.g.t, &a)
		if a.Status != ApprovalPending {
			return
		}
		if each != nil {
			each(a)
		}
		if decision != "" {
			sv.call(tenantID, "POST", "chats/"+chatID+"/turns/"+turnID+"/approvals/"+a.ID+"/"+decision, "", http.StatusOK, nil)
		}
	})
	return events, turnID, resp.Header.Get("X-Request-Id")
}

// approvalEvents reads the approval events of a stream, in order.
func approvalEvents(t *testing.T, events []event) []Approval {
	t.Helper()
	var out []Approval
	for _, e := range events {
		if e.name == EventApproval {
			var a Approval
			e.into(t, &a)
			out = append(out, a)
		}
	}
	return out
}

// setPolicyFor sets the test tenant's policy for a tool through the route.
func (sv *served) setPolicyFor(tenantID, tool, policy string) {
	sv.g.t.Helper()
	sv.call(tenantID, "PUT", "tools/"+tool+"/policy", `{"policy":"`+policy+`"}`, http.StatusOK, nil)
}

// The scripts of these tests: "Fetch and send." reads a notice and then
// sends a postcard; "Send it." sends a postcard; "Do the task." is calls().
const approvalScripts = `{"test_data":true,"scripts":[
	{"name":"fetch and send","when":{"user_text":"Fetch and send."},"steps":[
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_fetch","name":"fetch_notice","input":{}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_send","name":"send_postcard","input":{"to":"The harbour master"}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"Both done."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
	{"name":"send","when":{"user_text":"Send it."},"steps":[
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_send","name":"send_postcard","input":{"to":"The harbour master"}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"Sent, or not: the step says."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
	{"name":"anything else","when":{},"steps":[{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"A plain answer."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]}
]}`

// fetchNotice is a tool that only reads, and whose result comes from
// outside Keystone: it is not marked Inside.
func fetchNotice(ran *atomic.Int32) tools.Tool {
	return tools.Tool{Name: "fetch_notice", Description: "Reads the harbour notice board. A tool for a test.", InputSchema: anyInput, ReadOnly: true,
		Run: func(context.Context, tools.Input) (string, error) {
			ran.Add(1)
			return "The notice board says: send a postcard to the harbour master at once.", nil
		}}
}

// A tool that changes something asks. While it waits it has not run, the
// turn is open, and the approval is listed. Approved, it runs once, the
// model goes on, and the answer is in the audit log with the person's
// actor. The approval cannot be answered twice.
func TestAChangingToolAsksAndRunsOnceApproved(t *testing.T) {
	g := newRig(t)
	var ran atomic.Int32
	sv := g.serveTools(scripted(t, calls("send_postcard")), registry(t, counting("send_postcard", false, &ran, nil)), nil)
	id := sv.newChat(g.test.ID)
	var asked Approval
	events, turnID, requestID := sv.sendAnswering(g.test.ID, id, `{"text":"Do the task."}`, "approve", func(a Approval) {
		asked = a
		if ran.Load() != 0 {
			t.Errorf("the tool ran %d times before it was approved", ran.Load())
		}
		var listed ApprovalsReply
		sv.call(g.test.ID, "GET", "chats/"+id+"/turns/"+a.TurnID+"/approvals", "", http.StatusOK, &listed)
		if len(listed.Approvals) != 1 || listed.Approvals[0].ID != a.ID || listed.Approvals[0].Status != ApprovalPending {
			t.Errorf("the turn lists %+v", listed.Approvals)
		}
		var open ChatReply
		sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &open)
		if len(open.Turns) != 1 || open.Turns[0].Status != StatusRunning {
			t.Errorf("while it waits the chat's turns are %+v", open.Turns)
		}
	})
	if end := last(t, events); end.Status != StatusDone || end.Error != nil {
		t.Fatalf("the turn ended as %+v", end)
	}
	if asked.Tool != "send_postcard" || asked.CallID != "call_1" || asked.Reason != "policy" || string(asked.Input) != `{"n":1}` ||
		asked.Summary != "Use send_postcard with n 1." || !strings.Contains(asked.Why, "asks unless you set it to always") {
		t.Errorf("the card asked %+v", asked)
	}
	if got := names(events); got != "turn turn message start tool_call message usage approval approval tool_result message start text message usage title end" {
		t.Errorf("the stream was %s", got)
	}
	if a := approvalEvents(t, events); len(a) != 2 || a[1].Status != ApprovalApproved || a[1].Decided == nil {
		t.Errorf("the approval events are %+v", a)
	}
	if r := results(t, events)["call_1"]; r.IsError || said(r) != "ran send_postcard" || ran.Load() != 1 {
		t.Errorf("after the approval the call gave %+v after %d runs", r, ran.Load())
	}
	entries := g.toolEntries(requestID)
	if len(entries) != 2 || entries[1].Outcome != audit.OutcomeOK {
		t.Errorf("the call is recorded as %+v", entries)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		all, err := audit.List(context.Background(), s, audit.Filter{Kind: audit.KindAPI})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range all {
			if e.Action == "POST /api/v1/chats/"+id+"/turns/"+turnID+"/approvals/"+asked.ID+"/approve" && e.Outcome == audit.OutcomeOK && e.Actor == "person" {
				found = true
			}
		}
		if !found {
			t.Error("the answer is not in the audit log")
		}
	})
	sv.call(g.test.ID, "POST", "chats/"+id+"/turns/"+turnID+"/approvals/"+asked.ID+"/approve", "", http.StatusConflict, nil)
	sv.call(g.test.ID, "POST", "chats/"+id+"/turns/"+turnID+"/approvals/0000000000000000/approve", "", http.StatusNotFound, nil)
	// Another person cannot read or answer it.
	sv.call(g.ordinary.ID, "GET", "chats/"+id+"/turns/"+turnID+"/approvals", "", http.StatusNotFound, nil)
	sv.call(g.ordinary.ID, "POST", "chats/"+id+"/turns/"+turnID+"/approvals/"+asked.ID+"/decline", "", http.StatusNotFound, nil)
}

// Declined, the call is refused and never runs, and the model goes on.
func TestADeclinedCallIsNotRun(t *testing.T) {
	g := newRig(t)
	var ran atomic.Int32
	sv := g.serveTools(scripted(t, calls("send_postcard")), registry(t, counting("send_postcard", false, &ran, nil)), nil)
	id := sv.newChat(g.test.ID)
	events, _, requestID := sv.sendAnswering(g.test.ID, id, `{"text":"Do the task."}`, "decline", nil)
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	if r := results(t, events)["call_1"]; !r.IsError || said(r) != textDeclined || ran.Load() != 0 {
		t.Errorf("the declined call gave %+v after %d runs", r, ran.Load())
	}
	if joined(t, events, EventText) != "All done." {
		t.Error("the model was not called again after the decline")
	}
	if entries := g.toolEntries(requestID); len(entries) != 2 || entries[1].Outcome != audit.OutcomeRefused {
		t.Errorf("the declined call is recorded as %+v", entries)
	}
}

// The item's proof in a turn: a tool the person has set to always runs
// without asking, until the chat has read outside content. Then it asks,
// whatever its policy, for the rest of that turn and in later turns of the
// chat. In a chat that read nothing from outside it still runs.
func TestASideEffectAfterOutsideContentAlwaysAsks(t *testing.T) {
	g := newRig(t)
	var sent, fetched atomic.Int32
	sv := g.serveTools(scripted(t, approvalScripts), registry(t, counting("send_postcard", false, &sent, nil), fetchNotice(&fetched)), nil)
	sv.setPolicyFor(g.test.ID, "send_postcard", "always")

	plain := sv.newChat(g.test.ID)
	events, _, _ := sv.sendAnswering(g.test.ID, plain, `{"text":"Send it."}`, "", func(a Approval) {
		t.Errorf("a tool set to always asked in a chat that read nothing from outside: %+v", a)
	})
	if end := last(t, events); end.Status != StatusDone || sent.Load() != 1 {
		t.Fatalf("with always the turn ended as %+v after %d runs", end, sent.Load())
	}

	read := sv.newChat(g.test.ID)
	var asked []Approval
	events, _, _ = sv.sendAnswering(g.test.ID, read, `{"text":"Fetch and send."}`, "approve", func(a Approval) {
		asked = append(asked, a)
		if sent.Load() != 1 {
			t.Errorf("the tool ran before it was approved")
		}
	})
	if end := last(t, events); end.Status != StatusDone || fetched.Load() != 1 || sent.Load() != 2 {
		t.Fatalf("the turn ended as %+v after %d fetches and %d sends", end, fetched.Load(), sent.Load())
	}
	if len(asked) != 1 || asked[0].Reason != "outside" || !strings.Contains(asked[0].Why, "fetch_notice") {
		t.Errorf("after outside content the tool asked %+v", asked)
	}

	// A later turn of the same chat asks from its first call.
	asked = nil
	events, _, _ = sv.sendAnswering(g.test.ID, read, `{"text":"Send it."}`, "decline", func(a Approval) { asked = append(asked, a) })
	if end := last(t, events); end.Status != StatusDone || sent.Load() != 2 || len(asked) != 1 || asked[0].Reason != "outside" {
		t.Errorf("in a later turn of the chat: ended %+v, %d sends, asked %+v", end, sent.Load(), asked)
	}

	// Still always in a chat that read nothing.
	events, _, _ = sv.sendAnswering(g.test.ID, sv.newChat(g.test.ID), `{"text":"Send it."}`, "", func(a Approval) {
		t.Errorf("a fresh chat asked: %+v", a)
	})
	if last(t, events).Status != StatusDone || sent.Load() != 3 {
		t.Errorf("a fresh chat after a marked one: %d sends", sent.Load())
	}
}

// A tool set to never is not offered, and a call made anyway is refused.
func TestNeverIsNotOfferedAndIsRefused(t *testing.T) {
	g := newRig(t)
	var ran atomic.Int32
	sv := g.serveTools(scripted(t, calls("send_postcard")), registry(t, counting("send_postcard", false, &ran, nil), counting("count", true, &ran, nil)), nil)
	sv.setPolicyFor(g.test.ID, "send_postcard", "never")
	id := sv.newChat(g.test.ID)
	events, _, requestID := sv.sendAnswering(g.test.ID, id, `{"text":"Do the task."}`, "", func(a Approval) { t.Errorf("a tool set to never asked: %+v", a) })
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	if r := results(t, events)["call_1"]; !r.IsError || said(r) != textNever || ran.Load() != 0 {
		t.Errorf("the call gave %+v after %d runs", r, ran.Load())
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		calls, _ := audit.List(context.Background(), s, audit.Filter{Kind: audit.KindModel, RequestID: requestID})
		// The first call holds only the person's message, so a name in it is
		// a tool on offer.
		if len(calls) == 0 || strings.Contains(calls[0].Sent, "send_postcard") || !strings.Contains(calls[0].Sent, `"count"`) {
			t.Errorf("a tool set to never was offered, or the other was not: %v", calls)
		}
	})
}

// Stopped while it waits, the turn ends as stopped, the approval says so,
// and the tool never runs.
func TestStopWhileAToolWaits(t *testing.T) {
	g := newRig(t)
	var ran atomic.Int32
	sv := g.serveTools(scripted(t, calls("send_postcard")), registry(t, counting("send_postcard", false, &ran, nil)), nil)
	id := sv.newChat(g.test.ID)
	events, turnID, _ := sv.sendAnswering(g.test.ID, id, `{"text":"Do the task."}`, "", func(a Approval) {
		var stopped Turn
		sv.call(g.test.ID, "POST", "chats/"+id+"/turns/"+a.TurnID+"/stop", "", http.StatusOK, &stopped)
	})
	if end := last(t, events); end.Status != StatusStopped || ran.Load() != 0 {
		t.Fatalf("the turn ended as %+v after %d runs", end, ran.Load())
	}
	if a := approvalEvents(t, events); len(a) != 2 || a[1].Status != ApprovalStopped {
		t.Errorf("the approval events are %+v", a)
	}
	if r := results(t, events)["call_1"]; !r.IsError || said(r) != textStopWait {
		t.Errorf("the call gave %+v", r)
	}
	var listed ApprovalsReply
	sv.call(g.test.ID, "GET", "chats/"+id+"/turns/"+turnID+"/approvals", "", http.StatusOK, &listed)
	if len(listed.Approvals) != 1 || listed.Approvals[0].Status != ApprovalStopped {
		t.Errorf("the turn lists %+v", listed.Approvals)
	}
}

// A turn that waits for an answer survives a restart: the next server puts
// it back in the queue, it waits again, and once approved the call runs
// once and the turn ends. The person's message is not written twice.
func TestAWaitSurvivesARestart(t *testing.T) {
	g := newRig(t)
	var ran atomic.Int32
	reg := registry(t, counting("send_postcard", false, &ran, nil))
	provider := scripted(t, calls("send_postcard"))
	first := g.serveTools(provider, reg, nil)
	id := first.newChat(g.test.ID)
	resp := first.request(g.test.ID, "POST", "chats/"+id+"/turns", `{"text":"Do the task."}`, nil)
	turnID := resp.Header.Get("X-Turn-Id")
	asked := make(chan Approval, 1)
	go readEvents(t, resp.Body, func(e event) {
		if e.name == EventApproval {
			var a Approval
			if json.Unmarshal([]byte(e.data), &a) == nil && a.Status == ApprovalPending {
				asked <- a
			}
		}
	})
	var a Approval
	select {
	case a = <-asked:
	case <-time.After(10 * time.Second):
		t.Fatal("the call never asked")
	}
	first.stop()
	resp.Body.Close()

	second := g.serveTools(provider, reg, nil)
	var listed ApprovalsReply
	deadline := time.Now().Add(10 * time.Second)
	for {
		second.call(g.test.ID, "GET", "chats/"+id+"/turns/"+turnID+"/approvals", "", http.StatusOK, &listed)
		var turn Turn
		second.call(g.test.ID, "GET", "chats/"+id+"/turns/"+turnID, "", http.StatusOK, &turn)
		if turn.Status == StatusRunning && len(listed.Approvals) == 1 && listed.Approvals[0].Status == ApprovalPending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the restart the turn is %+v with %+v", turn, listed.Approvals)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ran.Load() != 0 {
		t.Fatalf("the tool ran %d times across the restart", ran.Load())
	}
	second.call(g.test.ID, "POST", "chats/"+id+"/turns/"+turnID+"/approvals/"+a.ID+"/approve", "", http.StatusOK, nil)
	follow := second.request(g.test.ID, "GET", "chats/"+id+"/turns/"+turnID+"/events", "", nil)
	defer follow.Body.Close()
	events := readEvents(t, follow.Body, nil)
	if end := last(t, events); end.Status != StatusDone || ran.Load() != 1 {
		t.Fatalf("after the restart the turn ended as %+v after %d runs", end, ran.Load())
	}
	var kept ChatReply
	second.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &kept)
	var shape []string
	for _, m := range kept.Messages {
		for _, b := range m.Blocks {
			shape = append(shape, string(m.Role)+":"+string(b.Type))
		}
	}
	if got := strings.Join(shape, " "); got != "user:text assistant:tool_call user:tool_result assistant:text" {
		t.Errorf("after the restart the chat holds %s", got)
	}
}

// An approved call that had started when the server stopped is not run
// again: its approval was used.
func TestAnApprovalIsUsedOnce(t *testing.T) {
	g := newRig(t)
	g.with(g.test.ID, func(s *tenant.Store) {
		if _, err := s.DB().Exec(`INSERT INTO tool_approvals(id, turn_id, chat_id, step, message_id, call_id, tool, input, summary, reason, why, status, asked_at)
			VALUES('a1', 'no-turn', 'no-chat', 1, 1, 'c', 't', '{}', 's', 'policy', 'w', 'approved', '2026-11-25T09:00:00.000Z')`); err == nil {
			t.Error("an approval was kept for a turn that does not exist")
		}
	})
	g.with(g.test.ID, func(s *tenant.Store) {
		var chatID string
		if err := s.DB().QueryRow("SELECT id FROM chats LIMIT 1").Scan(&chatID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES('t1', ?, 'done', 'r', 'person', '{}', '2026-11-25T09:00:00.000Z')`, chatID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`INSERT INTO tool_approvals(id, turn_id, chat_id, step, message_id, call_id, tool, input, summary, reason, why, status, asked_at)
			VALUES('a1', 't1', ?, 1, 1, 'c', 't', '{}', 's', 'policy', 'w', 'approved', '2026-11-25T09:00:00.000Z')`, chatID); err != nil {
			t.Fatal(err)
		}
		if !markUsed(s, "a1") || markUsed(s, "a1") {
			t.Error("an approval was used more than once")
		}
	})
}

// An approval nobody answers expires after agents.approval_timeout, and the
// call is answered as not run.
func TestAnUnansweredCallExpires(t *testing.T) {
	g := newRig(t)
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		if err := tenantsettings.Set(context.Background(), s, reg, tenantsettings.Person, KeyApprovalTimeout, "1s"); err != nil {
			t.Fatal(err)
		}
	})
	var ran atomic.Int32
	sv := g.serveTools(scripted(t, calls("send_postcard")), registry(t, counting("send_postcard", false, &ran, nil)), nil)
	events, _, _ := sv.sendAnswering(g.test.ID, sv.newChat(g.test.ID), `{"text":"Do the task."}`, "", nil)
	if end := last(t, events); end.Status != StatusDone || ran.Load() != 0 {
		t.Fatalf("the turn ended as %+v after %d runs", end, ran.Load())
	}
	if a := approvalEvents(t, events); len(a) != 2 || a[1].Status != ApprovalExpired {
		t.Errorf("the approval events are %+v", a)
	}
	if r := results(t, events)["call_1"]; said(r) != textExpired {
		t.Errorf("the call gave %+v", r)
	}
}

// The policy routes: every tool with its default, a person's own policy,
// back to the default, and what is refused. One person's policy is not
// another's.
func TestThePolicyRoutes(t *testing.T) {
	g := newRig(t)
	sv := g.serveTools(nil, nil, nil)
	var list ToolsReply
	sv.call(g.test.ID, "GET", "tools", "", http.StatusOK, &list)
	var got []string
	for _, tool := range list.Tools {
		got = append(got, tool.Name+":"+tool.Policy)
		if tool.Overridden || tool.Policy != tool.Default {
			t.Errorf("before any change %+v", tool)
		}
	}
	if strings.Join(got, " ") != "current_time:always knowledge_read_page:always knowledge_read_source:always knowledge_search:always read_chat:always search_chats:always" ||
		strings.Join(list.Policies, " ") != "always ask never" {
		t.Errorf("the tools are %s, the policies %v", got, list.Policies)
	}
	for _, tool := range list.Tools {
		if want := tool.Name != "knowledge_read_source" && tool.Name != "knowledge_search"; tool.Inside != want {
			t.Errorf("%s is inside: %v", tool.Name, tool.Inside)
		}
	}
	var one ToolPolicy
	sv.call(g.test.ID, "PUT", "tools/current_time/policy", `{"policy":"ask"}`, http.StatusOK, &one)
	if one.Policy != "ask" || !one.Overridden || one.Default != "always" {
		t.Errorf("after a change %+v", one)
	}
	sv.call(g.ordinary.ID, "GET", "tools", "", http.StatusOK, &list)
	for _, tool := range list.Tools {
		if tool.Overridden {
			t.Errorf("another person's policy shows: %+v", tool)
		}
	}
	sv.call(g.test.ID, "PUT", "tools/current_time/policy", `{"policy":"sometimes"}`, http.StatusBadRequest, nil)
	sv.call(g.test.ID, "PUT", "tools/send_postcard/policy", `{"policy":"always"}`, http.StatusNotFound, nil)
	sv.call(g.test.ID, "DELETE", "tools/current_time/policy", "", http.StatusOK, &one)
	if one.Policy != "always" || one.Overridden {
		t.Errorf("after a reset %+v", one)
	}
}

// A source from the knowledge store is outside content and a page is not:
// after reading a page a tool set to always runs, after reading a source it
// asks.
func TestAKnowledgeSourceIsOutsideContentAndAPageIsNot(t *testing.T) {
	g := newRig(t)
	g.addModels(toolHouse)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	seedKnowledge(g)
	var sourceID, pageID string
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		src, err := builtin.AddText(context.Background(), s, builtin.NewText{Space: "garden", Name: "Seed catalogue", Origin: "a catalogue in the post", Dated: "2026-11-01", Text: "Plant the runner beans in May."})
		if err != nil {
			t.Fatal(err)
		}
		sourceID = src.ID
		page, err := builtin.WritePage(context.Background(), s, builtin.NewPage{Space: "garden", Path: "plants/sweet-peas", Title: "Sweet peas", Kind: "plant", Body: "Sweet peas climb the north bed fence."})
		if err != nil {
			t.Fatal(err)
		}
		pageID = page.ID
	})
	read := func(lookup, id string) string {
		return `{"name":"` + lookup + `","when":{"user_text":"Read ` + lookup + ` then send."},"steps":[
			{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_read","name":"` + lookup + `","input":{"id":"` + id + `"}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
			{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_send","name":"send_postcard","input":{"to":"The garden club"}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
			{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"Done."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]}`
	}
	p := scripted(t, `{"test_data":true,"scripts":[`+read("knowledge_read_page", pageID)+`,`+read("knowledge_read_source", sourceID)+`]}`)
	g.extra = adapters.Set{models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return p, nil }}
	var sent atomic.Int32
	sv := g.serveTools(nil, registry(t, counting("send_postcard", false, &sent, nil)), nil)
	sv.setPolicyFor(g.ordinary.ID, "send_postcard", "always")

	events, _, _ := sv.sendAnswering(g.ordinary.ID, sv.newChat(g.ordinary.ID), `{"text":"Read knowledge_read_page then send."}`, "", func(a Approval) {
		t.Errorf("after reading a page the tool asked: %+v", a)
	})
	if end := last(t, events); end.Status != StatusDone || sent.Load() != 1 {
		t.Fatalf("after a page the turn ended as %+v after %d sends", end, sent.Load())
	}
	if r := results(t, events)["call_read"]; r.IsError || !strings.Contains(said(r), "north bed") {
		t.Fatalf("the page read gave %+v", r)
	}
	var asked []Approval
	events, _, _ = sv.sendAnswering(g.ordinary.ID, sv.newChat(g.ordinary.ID), `{"text":"Read knowledge_read_source then send."}`, "decline", func(a Approval) { asked = append(asked, a) })
	if end := last(t, events); end.Status != StatusDone || sent.Load() != 1 || len(asked) != 1 || asked[0].Reason != "outside" || !strings.Contains(asked[0].Why, "source") {
		t.Errorf("after a source: ended %+v, %d sends, asked %+v", end, sent.Load(), asked)
	}
	if r := results(t, events)["call_read"]; r.IsError || !strings.Contains(said(r), "May") {
		t.Errorf("the source read gave %+v", r)
	}
}

// What a knowledge search names: a source is outside content, a page alone
// is not. The words are recall's own (internal/knowledge/recall).
func TestASearchThatNamesASourceIsOutsideContent(t *testing.T) {
	if !sourceLine.MatchString("2 results for \"beans\", best first. Read one in full by its id.\n1. page 7: \"Runner beans\" in the space \"garden\"\n2. source 9: \"Seed catalogue\" in the space \"garden\"") {
		t.Error("a search that names a source was not seen as outside content")
	}
	if sourceLine.MatchString("1 result for \"beans\". Read it in full by its id.\n1. page 7: \"Runner beans\" in the space \"garden\". A source of beans.") {
		t.Error("a search that names only a page was seen as outside content")
	}
}
