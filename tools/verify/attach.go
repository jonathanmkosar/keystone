package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// The "attached chats" check (P5-06): a chat can attach other chats of the
// same person, and a handoff opens a fresh chat that continues from one.
// Each call carries every attached chat as its recap, and the model opens
// the rest with open_attached_chat.
//
// It runs the real server, with the shipped settings, on a throwaway data
// folder, with the expectations of fixtures/test-tenant/attached-chats.json
// and the held chat of fixtures/test-tenant/past-chats.json. The test
// tenant's recorded turns answer every model call. The check proves:
//
//  1. A handoff chat answers a question about the chat it continues from:
//     the parent's summary is in the call the model is sent, and the reply
//     answers from it. The parent lists the handoff.
//  2. An attached chat's detail, which its summary does not hold, is reached
//     through open_attached_chat, recorded before and after in the audit
//     log.
//  3. Fixed rule 1: a chat of another tenant cannot be attached, from
//     either end.
//  4. Fixed rule 3, on an invented tenant with a hosted and a local model:
//     an attached chat that recall held for sensitive content reaches no
//     call to the hosted model, which is told only that one was left out;
//     a call to the local model carries it, and its chat is held.

// The attached chats (P5-06) decide whether an attached chat may reach a
// call: attach.MayShow takes the plain answer and asks internal/recap. The
// package enforces rule 3 beside the registry and routing, and keeps its
// links inside one tenant's store.
func init() {
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/attach")
}

// attachedFixture is fixtures/test-tenant/attached-chats.json.
type attachedFixture struct {
	TestData bool   `json:"test_data"`
	About    string `json:"about"`
	Handoff  struct {
		Chat  string   `json:"chat"`
		Ask   string   `json:"ask"`
		Call  []string `json:"call_contains"`
		Reply []string `json:"reply_contains"`
	} `json:"handoff"`
	Attached struct {
		Chat   string   `json:"chat"`
		Ask    string   `json:"ask"`
		Result []string `json:"result_contains"`
		Reply  []string `json:"reply_contains"`
	} `json:"attached"`
}

func readAttachedFixture(root string) (attachedFixture, error) {
	var fx attachedFixture
	path := filepath.Join(root, "fixtures", "test-tenant", "attached-chats.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fx, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fx); err != nil {
		return fx, fmt.Errorf("%s does not read: %w", path, err)
	}
	switch {
	case !fx.TestData:
		return fx, fmt.Errorf("%s is not marked as test data", path)
	case fx.Handoff.Chat == "" || fx.Handoff.Ask == "" || len(fx.Handoff.Call) == 0 || len(fx.Handoff.Reply) == 0:
		return fx, fmt.Errorf("%s needs a handoff: its chat, its question, what the call carries and what the reply says", path)
	case fx.Attached.Chat == "" || fx.Attached.Ask == "" || len(fx.Attached.Result) == 0 || len(fx.Attached.Reply) == 0:
		return fx, fmt.Errorf("%s needs an attached chat: its chat, its question, what the tool gives and what the reply says", path)
	}
	return fx, nil
}

// attachPassword is invented, and guards a folder that is removed.
const attachPassword = "an invented phrase for the attached chats check"

func checkAttached(root string) error {
	fx, err := readAttachedFixture(root)
	if err != nil {
		return err
	}
	past, err := readPastChatsFixture(root)
	if err != nil {
		return err
	}
	shipped, err := testtenant.Shipped()
	if err != nil {
		return err
	}
	p, err := testtenant.Provider(shipped, fake.Options{})
	if err != nil {
		return err
	}
	c := &counted{sent: map[string][]string{}, p: p}
	g, err := openKnowledgeRig(root, "keystone-attached-", adapters.Set{pastHosted.Adapter: c.builder, pastLocal.Adapter: c.builder})
	if err != nil {
		return err
	}
	defer g.done()
	problems, err := attachedProblems(g, fx, past, c)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// chatCalls returns what each chat call of a request was sent: the model
// calls that were not a recap's.
func (g *knowledgeRig) chatCalls(tenantID, requestID string) ([]string, error) {
	_, byModel, err := g.calls(tenantID, requestID)
	var out []string
	for _, sent := range byModel {
		for _, s := range sent {
			if !strings.Contains(s, recapScriptPhrase) {
				out = append(out, s)
			}
		}
	}
	return out, err
}

// systemOf is the system text of one request the audit log holds.
func systemOf(sent string) string {
	req, err := seam.DecodeRequest([]byte(sent))
	if err != nil {
		return ""
	}
	var parts []string
	for _, b := range req.System {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "\n")
}

// toolResult is what one turn's stream says of the first call to a tool:
// its result, whether it was not an error, and the reply's text.
func toolResult(stream, name string) (result string, ok bool, reply string) {
	ids := map[string]bool{}
	seen := false
	for _, e := range readStream(stream) {
		switch e.name {
		case turn.EventToolCall:
			var tc turn.ToolCallEvent
			if json.Unmarshal([]byte(e.data), &tc) == nil && tc.Name == name {
				ids[tc.ID] = true
			}
		case turn.EventToolResult:
			var r turn.ToolResultEvent
			if json.Unmarshal([]byte(e.data), &r) == nil && ids[r.ID] && !seen {
				result, ok, seen = seam.Text(seam.Message{Blocks: r.Content}), !r.IsError, true
			}
		case turn.EventText:
			var d turn.DeltaEvent
			if json.Unmarshal([]byte(e.data), &d) == nil {
				reply += d.Text
			}
		}
	}
	return result, ok, reply
}

// attachRoute calls a route of attached chats and reads its answer.
func (g *knowledgeRig) attachRoute(method, token, path string, want int, out any) error {
	rec := g.w.request(method, api.Prefix+path, token, "", nil)
	if rec.Code != want {
		return fmt.Errorf("%s %s answered %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	if out != nil {
		return json.Unmarshal(rec.Body.Bytes(), out)
	}
	return nil
}

func attachedProblems(g *knowledgeRig, fx attachedFixture, past pastChatsFixture, c *counted) ([]string, error) {
	ctx := context.Background()
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	token, rec := g.w.signIn(g.test.ID, g.testPassword)
	if token == "" {
		return nil, fmt.Errorf("the test tenant cannot sign in through the API: status %d", rec.Code)
	}
	chats, err := g.chatList(token)
	if err != nil {
		return nil, err
	}
	idOf := map[string]string{}
	for _, ch := range chats {
		idOf[ch.Title] = ch.ID
	}
	parent, attached := idOf[fx.Handoff.Chat], idOf[fx.Attached.Chat]
	if parent == "" || attached == "" {
		return nil, fmt.Errorf("the test tenant has no chat titled %q or %q", fx.Handoff.Chat, fx.Attached.Chat)
	}

	// 1. A handoff chat answers about its parent.
	var made turn.HandoffReply
	if err := g.attachRoute("POST", token, "chats/"+parent+"/handoff", http.StatusCreated, &made); err != nil {
		return nil, err
	}
	t, err := g.send(token, made.Chat.Chat.ID, fx.Handoff.Ask, "")
	if err != nil {
		return nil, err
	}
	calls, err := g.chatCalls(g.test.ID, t.requestID)
	if err != nil {
		return nil, err
	}
	if len(calls) == 0 {
		say("the handoff chat's turn made no chat call")
	} else {
		for _, w := range fx.Handoff.Call {
			if !strings.Contains(systemOf(calls[0]), w) {
				say("the handoff chat's call does not carry %q from the chat it continues from", w)
			}
		}
	}
	_, _, answered := toolResult(t.stream, "")
	for _, w := range fx.Handoff.Reply {
		if !strings.Contains(answered, w) {
			say("the handoff chat's reply does not hold %q: %s", w, answered)
		}
	}
	var links turn.AttachedReply
	if err := g.attachRoute("GET", token, "chats/"+parent+"/attached", http.StatusOK, &links); err != nil {
		return nil, err
	}
	if len(links.Handoffs) != 1 || links.Handoffs[0].ID != made.Chat.Chat.ID {
		say("the parent lists the handoffs %+v, want the new chat", links.Handoffs)
	}

	// 2. An attached chat's detail, through open_attached_chat.
	t, err = g.send(token, "", "A new chat.", "")
	if err != nil {
		return nil, err
	}
	asking := t.chatID
	if err := g.attachRoute("PUT", token, "chats/"+asking+"/attached/"+attached, http.StatusOK, nil); err != nil {
		return nil, err
	}
	if t, err = g.send(token, asking, fx.Attached.Ask, ""); err != nil {
		return nil, err
	}
	result, ok, reply := toolResult(t.stream, "open_attached_chat")
	if !ok {
		say("%q was not answered through open_attached_chat: %s", fx.Attached.Ask, result)
	}
	for _, w := range fx.Attached.Result {
		if !strings.Contains(result, w) {
			say("open_attached_chat does not give %q:\n%s", w, result)
		}
	}
	for _, w := range fx.Attached.Reply {
		if !strings.Contains(reply, w) {
			say("the reply to %q does not hold %q: %s", fx.Attached.Ask, w, reply)
		}
	}
	var entries []audit.Entry
	err = g.with(g.test.ID, func(s *tenant.Store) error {
		var err error
		entries, err = audit.List(ctx, s, audit.Filter{Kind: audit.KindTool, RequestID: t.requestID})
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(entries) != 2 || entries[0].Action != "open_attached_chat" || entries[0].Outcome != audit.OutcomeStarted || entries[1].Outcome != audit.OutcomeOK {
		say("open_attached_chat is recorded in the audit log as %d entries, want one before and one after", len(entries))
	}

	// 3. Fixed rule 1, from either end.
	person, err := g.in.Create("Attached Chats Check Person")
	if err != nil {
		return nil, err
	}
	err = g.with(person.ID, func(s *tenant.Store) error {
		o, err := signin.Defaults()
		if err != nil {
			return err
		}
		if err := signin.SetPassword(s, attachPassword, o); err != nil {
			return err
		}
		lim, err := adapters.Limits(g.reg)
		if err != nil {
			return err
		}
		for _, m := range []models.Model{pastHosted, pastLocal} {
			if err := models.Add(ctx, s, lim, m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	other, rec := g.w.signIn(person.ID, attachPassword)
	if other == "" {
		return nil, fmt.Errorf("the invented tenant cannot sign in through the API: status %d", rec.Code)
	}
	theirs, err := g.send(other, "", "A chat of my own.", pastLocal.Name)
	if err != nil {
		return nil, err
	}
	for _, x := range []struct{ token, path string }{
		{other, "chats/" + theirs.chatID + "/attached/" + parent},
		{token, "chats/" + parent + "/attached/" + theirs.chatID},
	} {
		if r := g.w.request("PUT", api.Prefix+x.path, x.token, "", nil); r.Code != http.StatusNotFound {
			say("PUT %s across two tenants answered %d, want 404", x.path, r.Code)
		}
	}

	// 4. Fixed rule 3, on the invented tenant.
	if err := g.seed(person.ID); err != nil {
		return nil, err
	}
	if err := g.use(person.ID, builtin.Name); err != nil {
		return nil, err
	}
	if err := g.set(person.ID, memory.SettingOn, "false", turn.KeyTitleAuto, "false", turn.KeyRecap, "false"); err != nil {
		return nil, err
	}
	marker := past.Held.Marker
	held, err := g.send(other, "", past.Held.Ask, pastLocal.Name)
	if err != nil {
		return nil, err
	}
	if h, err := g.held(person.ID, held.chatID); err != nil {
		return nil, err
	} else if !h {
		return nil, fmt.Errorf("the question %q did not hold its chat, so the check proves nothing", past.Held.Ask)
	}
	if made, _, err := g.recapRoute("POST", other, held.chatID); err != nil {
		return nil, err
	} else if !strings.Contains(made.Recap.Summary, marker) {
		return nil, fmt.Errorf("the held chat's recap does not hold %q, so the check proves nothing", marker)
	}
	before := len(c.sentWith(pastHosted.Name, marker))

	onHosted, err := g.send(other, "", "Tell me something.", pastHosted.Name)
	if err != nil {
		return nil, err
	}
	if err := g.attachRoute("PUT", other, "chats/"+onHosted.chatID+"/attached/"+held.chatID, http.StatusOK, nil); err != nil {
		return nil, err
	}
	if onHosted, err = g.send(other, onHosted.chatID, "And what about the attached chat?", pastHosted.Name); err != nil {
		return nil, err
	}
	if n := len(c.sentWith(pastHosted.Name, marker)); n != before {
		say("the hosted model was sent the held attached chat's marker %q in %d requests", marker, n-before)
	}
	if all, _, err := g.calls(person.ID, onHosted.requestID); err != nil {
		return nil, err
	} else if strings.Contains(all, marker) || !strings.Contains(all, "chat attached to this one is left out") {
		say("the hosted model's call holds the held attached chat's marker %q, or does not say one was left out", marker)
	}
	if h, err := g.held(person.ID, onHosted.chatID); err != nil {
		return nil, err
	} else if h {
		say("the chat whose hosted call left the held attached chat out is held")
	}

	onLocal, err := g.send(other, "", "Tell me something.", pastLocal.Name)
	if err != nil {
		return nil, err
	}
	if err := g.attachRoute("PUT", other, "chats/"+onLocal.chatID+"/attached/"+held.chatID, http.StatusOK, nil); err != nil {
		return nil, err
	}
	if onLocal, err = g.send(other, onLocal.chatID, "And what about the attached chat?", pastLocal.Name); err != nil {
		return nil, err
	}
	if calls, err := g.chatCalls(person.ID, onLocal.requestID); err != nil {
		return nil, err
	} else if len(calls) == 0 || !strings.Contains(systemOf(calls[0]), marker) {
		say("the local model's call does not carry the held attached chat's recap")
	}
	if h, err := g.held(person.ID, onLocal.chatID); err != nil {
		return nil, err
	} else if !h {
		say("the chat whose call carried a held attached chat is not held")
	}
	return problems, nil
}
