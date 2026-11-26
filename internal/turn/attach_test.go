package turn

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/attach"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/builtin"
)

// Attached chats and handoff (P5-06). Every chat, name and fact here is
// invented.

// The test tenant's recorded questions, in
// fixtures/test-tenant/turns/attached-chats.json.
const (
	handoffQuestion = "What did we settle in the chat this one continues from?"
	signalQuestion  = "Where does the phone signal drop on the way to the lighthouse?"
	walkTitle       = "Planning the lighthouse walk"
)

// serveAttach starts a server as serveRecaps does, with the routes of
// attached chats beside the chat, turn and recap routes. A nil provider is
// the fake playing the shipped recorded turns.
func (g *rig) serveAttach(provider seam.Provider) *served {
	g.t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 50 * time.Millisecond
	if provider == nil {
		if provider, err = testtenant.Provider(g.fx, fake.Options{DeltaSize: 12}); err != nil {
			g.t.Fatal(err)
		}
	}
	reg := tools.New()
	if err := builtin.Add(reg, builtin.Options{Limits: cfg.Limits, ReadChatMessages: cfg.ToolLoop.ReadChatMessages}); err != nil {
		g.t.Fatal(err)
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
	routes := append(Routes(runner), RecapRoutes(runner)...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, append(routes, AttachRoutes(runner)...))
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

// status makes a call and returns its status and its body.
func (sv *served) status(tenantID, method, path string) (int, string) {
	sv.g.t.Helper()
	resp := sv.request(tenantID, method, path, "", nil)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// attached reads a chat's attached chats through the route.
func (sv *served) attached(tenantID, chatID string) AttachedReply {
	sv.g.t.Helper()
	var reply AttachedReply
	sv.call(tenantID, "GET", "chats/"+chatID+"/attached", "", http.StatusOK, &reply)
	return reply
}

// chatSent returns what each chat call of a request was sent, as the audit
// log holds it: not the calls that made a recap.
func chatSent(t *testing.T, g *rig, tenantID, requestID string) []string {
	t.Helper()
	var out []string
	g.with(tenantID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Outcome == audit.OutcomeStarted && !strings.Contains(e.Sent, recapPhrase) {
				out = append(out, e.Sent)
			}
		}
	})
	return out
}

// carries reads one call's request and says whether its system text holds
// a phrase, and whether it offers open_attached_chat.
func carries(t *testing.T, sent, phrase string) (inSystem, offered bool) {
	t.Helper()
	req, err := seam.DecodeRequest([]byte(sent))
	if err != nil {
		t.Fatalf("a request in the audit log does not read: %v", err)
	}
	for _, b := range req.System {
		inSystem = inSystem || strings.Contains(b.Text, phrase)
	}
	for _, d := range req.Tools {
		offered = offered || d.Name == attach.ToolName
	}
	return inSystem, offered
}

// attachedPart is the attached part of what a turn's first call was built
// from.
func attachedPart(t *testing.T, g *rig, tenantID, turnID string) contextbuild.PartRecord {
	t.Helper()
	var part contextbuild.PartRecord
	g.with(tenantID, func(s *tenant.Store) {
		calls, err := Contexts(context.Background(), s, turnID)
		if err != nil || len(calls) == 0 {
			t.Fatalf("the turn's stored contexts are %+v (%v)", calls, err)
		}
		part = calls[0].Record.Part(contextbuild.PartAttached)
	})
	return part
}

// The item's first proof: a handoff chat answers a question about the chat
// it continues from. The parent's summary is made in the turn, by the
// recap code, under the turn's request id, and is in the request the model
// gets; the screen's links run both ways.
func TestAHandoffChatAnswersAboutItsParent(t *testing.T) {
	g := newRig(t)
	sv := g.serveAttach(nil)
	walk := g.chatTitled(g.test.ID, walkTitle)

	var made HandoffReply
	sv.call(g.test.ID, "POST", "chats/"+walk+"/handoff", "", http.StatusCreated, &made)
	child := made.Chat.Chat.ID
	if child == "" || child == walk || made.Parent.ID != walk || made.Parent.Title != walkTitle || len(made.Chat.Messages) != 0 || made.Model != TestModelName {
		t.Fatalf("the handoff answered %+v", made)
	}
	if got := sv.attached(g.test.ID, child); got.Parent == nil || got.Parent.ID != walk || len(got.Attached) != 1 || !got.Attached[0].Parent || got.Attached[0].Title != walkTitle {
		t.Fatalf("the new chat's attached chats are %+v", got)
	}
	if got := sv.attached(g.test.ID, walk); len(got.Handoffs) != 1 || got.Handoffs[0].ID != child || got.Parent != nil {
		t.Fatalf("the parent's handoffs are %+v", got)
	}

	events, turnID, requestID := sv.send(g.test.ID, child, ask(handoffQuestion, ""))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	if text := joined(t, events, EventText); !strings.Contains(text, "walking to the old lighthouse on Saturday") {
		t.Fatalf("the handoff chat did not answer from its parent: %s", text)
	}
	sent := chatSent(t, g, g.test.ID, requestID)
	if len(sent) == 0 || !strings.Contains(sent[0], "continues from an earlier chat") || !strings.Contains(sent[0], "Walk to the old lighthouse on Saturday.") || !strings.Contains(sent[0], walk) {
		t.Fatalf("the parent's summary is not in the request the model got:\n%v", sent)
	}
	if recs := kinds(g, g.test.ID, requestID)[string(routing.KindSummary)]; len(recs) != 1 || recs[0].Outcome != "ok" {
		t.Fatalf("the parent's summary made in the turn has the usage records %+v, want one", recs)
	}
	if got := sv.recapOf(g.test.ID, walk); !got.Made || got.Recap.RequestID != requestID {
		t.Fatalf("the parent's recap is %+v, want one made under the turn's request id", got)
	}
	part := attachedPart(t, g, g.test.ID, turnID)
	if part.State != contextbuild.StateKept || len(part.Items) != 1 || part.Items[0].Label != `continued from "`+walkTitle+`"` || !part.Items[0].Pinned {
		t.Fatalf("the inspector's record of the attached part is %+v", part)
	}

	// A second turn makes no new summary: the parent has not moved on.
	_, _, again := sv.send(g.test.ID, child, ask(handoffQuestion, ""))
	if recs := kinds(g, g.test.ID, again)[string(routing.KindSummary)]; len(recs) != 0 {
		t.Fatalf("a second turn made the parent's summary again: %+v", recs)
	}
	// The parent stays attached.
	if code, _ := sv.status(g.test.ID, "DELETE", "chats/"+child+"/attached/"+walk); code != http.StatusBadRequest {
		t.Fatalf("taking the parent off answered %d", code)
	}
	// A chat with no messages is not handed off.
	if code, _ := sv.status(g.test.ID, "POST", "chats/"+sv.newChat(g.test.ID)+"/handoff"); code != http.StatusBadRequest {
		t.Fatalf("handing off an empty chat answered %d", code)
	}
}

// The item's second proof: an attached chat's detail, which its summary
// does not hold, is reached through open_attached_chat, which is recorded
// as every tool call is.
func TestAnAttachedChatsDetailIsReachable(t *testing.T) {
	g := newRig(t)
	sv := g.serveAttach(nil)
	walk := g.chatTitled(g.test.ID, walkTitle)
	asking := sv.newChat(g.test.ID)
	var reply AttachedReply
	sv.call(g.test.ID, "PUT", "chats/"+asking+"/attached/"+walk, "", http.StatusOK, &reply)
	if len(reply.Attached) != 1 || reply.Attached[0].ID != walk || reply.Attached[0].Parent || reply.Max != 5 {
		t.Fatalf("attaching answered %+v", reply)
	}

	events, turnID, requestID := sv.send(g.test.ID, asking, ask(signalQuestion, ""))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	res, ok := results(t, events)["call_open_attached"]
	if !ok || res.IsError || !strings.Contains(said(res), "the signal drops after the second gate") || !strings.Contains(said(res), "Page 1 of 1") || !strings.Contains(said(res), walk) {
		t.Fatalf("open_attached_chat gave %+v:\n%s", res, said(res))
	}
	if text := joined(t, events, EventText); !strings.Contains(text, "second gate") {
		t.Fatalf("the reply does not hold the detail: %s", text)
	}
	if entries := g.toolEntries(requestID); len(entries) != 2 || entries[0].Action != attach.ToolName || entries[0].Outcome != audit.OutcomeStarted || entries[1].Outcome != audit.OutcomeOK {
		t.Fatalf("the tool call is recorded as %+v", entries)
	}
	sent := chatSent(t, g, g.test.ID, requestID)
	if len(sent) == 0 {
		t.Fatal("the turn's chat calls are not in the audit log")
	}
	if inSystem, offered := carries(t, sent[0], "attached their chat"); !inSystem || !offered {
		t.Fatalf("the first call does not carry the attached chat (%v) or offer the tool (%v):\n%s", inSystem, offered, sent[0])
	}
	if part := attachedPart(t, g, g.test.ID, turnID); len(part.Items) != 1 || part.Items[0].Label != `attached chat "`+walkTitle+`"` || part.Items[0].Pinned {
		t.Fatalf("the attached part is %+v", part)
	}

	// Taken off, it is carried no more and the tool is not offered.
	sv.call(g.test.ID, "DELETE", "chats/"+asking+"/attached/"+walk, "", http.StatusOK, &reply)
	if len(reply.Attached) != 0 {
		t.Fatalf("after taking it off the chat has %+v", reply.Attached)
	}
	_, _, requestID = sv.send(g.test.ID, asking, ask("And anything else?", ""))
	if sent := chatSent(t, g, g.test.ID, requestID); len(sent) == 0 {
		t.Fatal("the turn's chat calls are not in the audit log")
	} else if inSystem, offered := carries(t, sent[0], walkTitle); inSystem || offered {
		t.Fatalf("a chat with nothing attached still carries it (%v) or offers the tool (%v):\n%s", inSystem, offered, sent[0])
	}
}

// The item's third proof, fixed rule 1: a chat of another tenant cannot be
// attached, from either end, and is not found.
func TestAnotherTenantsChatCannotBeAttached(t *testing.T) {
	g := newRig(t)
	sv := g.serveAttach(nil)
	walk := g.chatTitled(g.test.ID, walkTitle)
	theirs := sv.newChat(g.ordinary.ID)
	for _, c := range []struct{ tenant, method, path string }{
		{g.ordinary.ID, "PUT", "chats/" + theirs + "/attached/" + walk},
		{g.test.ID, "PUT", "chats/" + walk + "/attached/" + theirs},
		{g.ordinary.ID, "GET", "chats/" + walk + "/attached"},
		{g.ordinary.ID, "POST", "chats/" + walk + "/handoff"},
		{g.ordinary.ID, "DELETE", "chats/" + walk + "/attached/" + theirs},
	} {
		if code, body := sv.status(c.tenant, c.method, c.path); code != http.StatusNotFound {
			t.Errorf("%s %s answered %d: %s", c.method, c.path, code, body)
		}
	}
	for _, id := range []string{g.test.ID, g.ordinary.ID} {
		g.with(id, func(s *tenant.Store) {
			var n int
			if err := s.DB().QueryRow("SELECT count(*) FROM chat_attachments").Scan(&n); err != nil || n != 0 {
				t.Errorf("tenant %s has %d links (%v)", id, n, err)
			}
		})
	}
}

// The routes: attaching twice changes nothing, a chat is not attached to
// itself, chats.attach_max holds, and the summary of an attached chat is
// given.
func TestTheAttachRoutes(t *testing.T) {
	g := newRig(t)
	g.setFor(g.test.ID, tenantsettings.Person, attach.SettingMax, "1")
	sv := g.serveAttach(nil)
	walk := g.chatTitled(g.test.ID, walkTitle)
	ferry := g.chatTitled(g.test.ID, "North ferry times")
	sv.makeRecap(g.test.ID, walk, http.StatusOK)
	id := sv.newChat(g.test.ID)
	if got := sv.attached(g.test.ID, id); len(got.Attached) != 0 || got.Handoffs == nil || got.Max != 1 || got.Incognito {
		t.Fatalf("a new chat's attached chats are %+v", got)
	}
	var reply AttachedReply
	sv.call(g.test.ID, "PUT", "chats/"+id+"/attached/"+walk, "", http.StatusOK, &reply)
	sv.call(g.test.ID, "PUT", "chats/"+id+"/attached/"+walk, "", http.StatusOK, &reply)
	if len(reply.Attached) != 1 || !strings.Contains(reply.Attached[0].Summary, "old lighthouse") || reply.Attached[0].Sensitive {
		t.Fatalf("attaching twice gave %+v", reply)
	}
	if code, body := sv.status(g.test.ID, "PUT", "chats/"+id+"/attached/"+ferry); code != http.StatusConflict || !strings.Contains(body, `"too_many"`) {
		t.Fatalf("a chat past chats.attach_max answered %d: %s", code, body)
	}
	if code, _ := sv.status(g.test.ID, "PUT", "chats/"+id+"/attached/"+id); code != http.StatusBadRequest {
		t.Fatalf("a chat attached to itself answered %d", code)
	}
	if code, _ := sv.status(g.test.ID, "PUT", "chats/"+id+"/attached/0000000000000000"); code != http.StatusNotFound {
		t.Fatalf("a chat that is not there answered %d", code)
	}
	sv.call(g.test.ID, "DELETE", "chats/"+id+"/attached/"+ferry, "", http.StatusOK, &reply)
	if len(reply.Attached) != 1 {
		t.Fatalf("taking off a chat that is not attached changed the list: %+v", reply)
	}
}

// An incognito chat is outside attaching and handoff, and an attached chat
// made incognito later is carried no more.
func TestAnIncognitoChatIsNotAttached(t *testing.T) {
	g := newRig(t)
	sv := g.serveAttach(nil)
	walk := g.chatTitled(g.test.ID, walkTitle)
	ferry := g.chatTitled(g.test.ID, "North ferry times")
	quiet := sv.newChat(g.test.ID)
	g.with(g.test.ID, func(s *tenant.Store) {
		if err := memory.SetIncognito(context.Background(), s, quiet, true); err != nil {
			t.Fatal(err)
		}
	})
	for _, path := range []string{"chats/" + quiet + "/attached/" + walk, "chats/" + walk + "/attached/" + quiet} {
		if code, body := sv.status(g.test.ID, "PUT", path); code != http.StatusConflict || !strings.Contains(body, `"incognito"`) {
			t.Errorf("PUT %s answered %d: %s", path, code, body)
		}
	}
	sv.send(g.test.ID, quiet, ask("A quiet question.", ""))
	if code, body := sv.status(g.test.ID, "POST", "chats/"+quiet+"/handoff"); code != http.StatusConflict || !strings.Contains(body, `"incognito"`) {
		t.Errorf("handing off an incognito chat answered %d: %s", code, body)
	}

	asking := sv.newChat(g.test.ID)
	sv.call(g.test.ID, "PUT", "chats/"+asking+"/attached/"+ferry, "", http.StatusOK, nil)
	g.with(g.test.ID, func(s *tenant.Store) {
		if err := memory.SetIncognito(context.Background(), s, ferry, true); err != nil {
			t.Fatal(err)
		}
	})
	if got := sv.attached(g.test.ID, asking); len(got.Attached) != 1 || !got.Attached[0].Incognito {
		t.Fatalf("the screen is not told the attached chat is incognito: %+v", got)
	}
	_, _, requestID := sv.send(g.test.ID, asking, ask("And anything else?", ""))
	if sent := chatSent(t, g, g.test.ID, requestID); len(sent) == 0 {
		t.Fatal("the turn's chat calls are not in the audit log")
	} else if inSystem, offered := carries(t, sent[0], "North ferry times"); inSystem || offered {
		t.Fatalf("an attached chat made incognito is still carried (%v) or opened (%v):\n%s", inSystem, offered, sent[0])
	}
}

// The item's fourth proof, fixed rule 3: an attached chat that holds
// sensitive content is held back from a call that may reach a hosted
// model, which is told only that one was left out, and its tool gives
// nothing of it. A call whose every model may read it is given it, through
// the context and the tool, and the chat it went into is held from then on.
func TestASensitiveAttachedChatIsHeldBack(t *testing.T) {
	g := newRig(t)
	var held string
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		c, err := chat.Create(s)
		if err != nil {
			t.Fatal(err)
		}
		held = c.ID
		msg, err := chat.Append(s, held, chat.NewMessage{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Melatonin helped in March."}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES('turn-held', ?, 'done', 'req-held', 'person', '{}', '2026-11-25T10:00:00.000Z')", held); err != nil {
			t.Fatal(err)
		}
		if err := recall.Hold(context.Background(), s, "turn-held", held); err != nil {
			t.Fatal(err)
		}
		if _, err := recap.Save(context.Background(), s, recap.Recap{ChatID: held, Summary: "Sleep: Melatonin helped.", Model: recapHouse.Name, Sensitive: true, ThroughID: msg.ID}); err != nil {
			t.Fatal(err)
		}
	})
	p := scripted(t, `{"test_data":true,"scripts":[
		{"name":"open it","when":{"user_text":"What helped me sleep, as the attached chat says?"},"steps":[
			{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_open","name":"open_attached_chat","input":{}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
			{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"Done."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
		{"name":"anything else","when":{},"steps":[{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"A plain answer."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]}
	]}`)
	h := &recapEars{sent: map[string][]string{}, p: p}
	g.extra = adapters.Set{recapHouse.Adapter: h.builder, recapPaid.Adapter: h.builder}
	g.addModels(recapHouse, recapPaid)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	g.set(tenantsettings.Person, KeyRecap, "false")
	// A hosted fallback: a call on the local model may reach it unless the
	// route keeps to models that may read sensitive content.
	g.set(tenantsettings.Person, KeyFallbackModels, recapPaid.Name)
	sv := g.serveAttach(nil)
	const question = "What helped me sleep, as the attached chat says?"

	// The hosted model's call: left out, counted, and the tool refuses it.
	hosted := sv.newChat(g.ordinary.ID)
	sv.call(g.ordinary.ID, "PUT", "chats/"+hosted+"/attached/"+held, "", http.StatusOK, nil)
	events, _, _ := sv.send(g.ordinary.ID, hosted, ask(question, recapPaid.Name))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the hosted turn ended as %+v", end)
	}
	res := results(t, events)["call_open"]
	if !res.IsError || !strings.Contains(said(res), "holds content from a sensitive space") || strings.Contains(said(res), "Melatonin") {
		t.Fatalf("open_attached_chat for the hosted model gave %+v: %s", res, said(res))
	}
	sent := h.all(recapPaid.Name)
	if strings.Contains(sent, "Melatonin") || strings.Contains(sent, held) || !strings.Contains(sent, "1 chat attached to this one is left out") {
		t.Fatalf("the hosted model was sent the held chat, or not told one was left out:\n%s", sent)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if h, _ := recall.Held(context.Background(), s, hosted); h {
			t.Error("a chat that was given nothing sensitive is held")
		}
	})

	// The local model's call: given, through the context and the tool, and
	// the chat is held from then on.
	local := sv.newChat(g.ordinary.ID)
	sv.call(g.ordinary.ID, "PUT", "chats/"+local+"/attached/"+held, "", http.StatusOK, nil)
	events, _, requestID := sv.send(g.ordinary.ID, local, ask(question, recapHouse.Name))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the local turn ended as %+v", end)
	}
	if res := results(t, events)["call_open"]; res.IsError || !strings.Contains(said(res), "Melatonin helped in March.") {
		t.Fatalf("open_attached_chat for the local model gave %+v: %s", res, said(res))
	}
	if sent := chatSent(t, g, g.ordinary.ID, requestID); len(sent) == 0 || !strings.Contains(sent[0], "Sleep: Melatonin helped.") {
		t.Fatalf("the local model's call does not carry the held chat's summary:\n%v", sent)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if h, err := recall.Held(context.Background(), s, local); err != nil || !h {
			t.Errorf("the chat given a held attached chat is not held (%v)", err)
		}
	})
	events, _, _ = sv.send(g.ordinary.ID, local, ask("And anything else?", recapPaid.Name))
	if end := last(t, events); end.Error == nil || end.Error.Code != CodeSensitive {
		t.Fatalf("the chat given a held attached chat went to the hosted model: %+v", end)
	}
	// Carried in the context alone, with no call to the tool, it holds the
	// chat all the same.
	plain := sv.newChat(g.ordinary.ID)
	sv.call(g.ordinary.ID, "PUT", "chats/"+plain+"/attached/"+held, "", http.StatusOK, nil)
	events, _, _ = sv.send(g.ordinary.ID, plain, ask("Tell me something.", recapHouse.Name))
	if _, called := results(t, events)["call_open"]; called || last(t, events).Status != StatusDone {
		t.Fatalf("the plain turn ended as %+v", last(t, events))
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if h, err := recall.Held(context.Background(), s, plain); err != nil || !h {
			t.Errorf("the chat whose call carried a held attached chat is not held (%v)", err)
		}
	})
	if strings.Contains(h.all(recapPaid.Name), "Melatonin") {
		t.Fatal("the hosted model was ever sent the held chat")
	}
}
