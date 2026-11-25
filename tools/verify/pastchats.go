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
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// The "past chats" check (P5-05): each chat has a recap, a short summary,
// its topics and the decisions made in it, and a question about an earlier
// chat is answered through the past-chat search, search_chats, which gives
// the chat's recap.
//
// It runs the real server, with the shipped settings, on a throwaway data
// folder, with the expectations of fixtures/test-tenant/past-chats.json.
// The test tenant's recorded turns answer every model call. The check
// proves:
//
//  1. Each of the test tenant's chats named there is given its recorded
//     recap through the recap route, by one call of the kind summary under
//     the route's request id, and the chat list gives its summary.
//  2. Each question of the fixture, sent as the test tenant in a new chat,
//     is answered through search_chats: the stream holds the call, its
//     result names the earlier chat and holds its recap, the reply holds
//     the answer, and the audit log holds the call before and after.
//  3. A turn's end gives its chat a recap, under the turn's request id.
//  4. Fixed rule 3, on an invented tenant with a hosted and a local model
//     that can use tools: a chat that recall held for sensitive content is
//     given its recap by the local model, never by the hosted model the
//     summary rule names, and the recap is sensitive; the search, run for
//     the hosted model, leaves that chat out and counts it, and nothing the
//     hosted model is ever sent holds the recap's marker; run for the local
//     model, it gives the chat, and holds the chat that asked.

// The recaps (P5-05) decide whether anything of a past chat may reach a
// call: recap.Sensitive and recap.MayShow take the plain answers. The
// package enforces rule 3 beside the registry and routing.
func init() {
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/recap")
}

// recapScriptPhrase is what the test tenant's recorded recaps look for in
// the system text. The shipped prompt must hold it.
const recapScriptPhrase = "summary, topics and decisions of one chat"

// pastChatsFixture is fixtures/test-tenant/past-chats.json.
type pastChatsFixture struct {
	TestData bool   `json:"test_data"`
	About    string `json:"about"`
	Recaps   []struct {
		Title     string   `json:"title"`
		Summary   string   `json:"summary_contains"`
		Decisions []string `json:"decisions"`
	} `json:"recaps"`
	Questions []struct {
		Ask    string   `json:"ask"`
		Chat   string   `json:"chat"`
		Result []string `json:"result_contains"`
		Reply  []string `json:"reply_contains"`
	} `json:"questions"`
	Held struct {
		Ask      string `json:"ask"`
		Question string `json:"question"`
		Marker   string `json:"marker"`
	} `json:"held"`
}

func readPastChatsFixture(root string) (pastChatsFixture, error) {
	var fx pastChatsFixture
	path := filepath.Join(root, "fixtures", "test-tenant", "past-chats.json")
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
	case len(fx.Recaps) == 0 || len(fx.Questions) == 0:
		return fx, fmt.Errorf("%s needs recaps and questions", path)
	case fx.Held.Ask == "" || fx.Held.Question == "" || fx.Held.Marker == "":
		return fx, fmt.Errorf("%s needs a held chat: its question, the question about it and its marker", path)
	}
	for _, q := range fx.Questions {
		if q.Ask == "" || q.Chat == "" || len(q.Result) == 0 || len(q.Reply) == 0 {
			return fx, fmt.Errorf("%s: every question has its chat, what the search gives and what the reply says", path)
		}
	}
	return fx, nil
}

// The invented tenant's models. The test tenant's recorded turns answer
// for both, so nothing leaves this machine.
var (
	pastHosted = models.Model{
		Name: "hosted-recap", Label: "Hosted Recap", Adapter: models.AdapterAnthropic, Endpoint: "https://models.example/v1",
		ProviderModel: "example-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassHosted,
		Capabilities: []models.Capability{models.CapTools},
	}
	pastLocal = models.Model{
		Name: "local-recap", Label: "Local Recap", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
		ProviderModel: "small-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
		Capabilities: []models.Capability{models.CapTools},
	}
)

// pastChatsPassword is invented, and guards a folder that is removed.
const pastChatsPassword = "an invented phrase for the past chats check"

func checkPastChats(root string) error {
	fx, err := readPastChatsFixture(root)
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
	g, err := openKnowledgeRig(root, "keystone-pastchats-", adapters.Set{pastHosted.Adapter: c.builder, pastLocal.Adapter: c.builder})
	if err != nil {
		return err
	}
	defer g.done()
	problems, err := pastChatsProblems(g, fx, c)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// sentWith returns the requests a model was sent that hold a phrase.
func (c *counted) sentWith(name, phrase string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, r := range c.sent[name] {
		if strings.Contains(r, phrase) {
			out = append(out, r)
		}
	}
	return out
}

// recapRoute reads or makes a chat's recap through the API.
func (g *knowledgeRig) recapRoute(method, token, chatID string) (turn.RecapReply, string, error) {
	var reply turn.RecapReply
	rec := g.w.request(method, api.Prefix+"chats/"+chatID+"/recap", token, "", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reply) != nil {
		return reply, "", fmt.Errorf("%s the recap of a chat answered %d: %s", method, rec.Code, rec.Body.String())
	}
	return reply, rec.Header().Get("X-Request-Id"), nil
}

// chatList reads a tenant's chats through the API.
func (g *knowledgeRig) chatList(token string) ([]turn.Chat, error) {
	var list turn.ListReply
	rec := g.w.request("GET", api.Prefix+"chats", token, "", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil {
		return nil, fmt.Errorf("the chat list answered %d", rec.Code)
	}
	return list.Chats, nil
}

// kindOf returns a request's usage records of one kind.
func (g *knowledgeRig) kindOf(tenantID, requestID string, kind routing.Kind) ([]usage.Record, error) {
	var out []usage.Record
	err := g.with(tenantID, func(s *tenant.Store) error {
		all, err := usage.List(context.Background(), s, usage.Filter{RequestID: requestID})
		for _, r := range all {
			if r.Kind == string(kind) {
				out = append(out, r)
			}
		}
		return err
	})
	return out, err
}

// searched is what one turn's stream says of its past-chat search: the
// result of the first call to search_chats, and the reply's text.
func searched(stream string) (result string, called bool, reply string) {
	ids := map[string]bool{}
	for _, e := range readStream(stream) {
		switch e.name {
		case turn.EventToolCall:
			var c turn.ToolCallEvent
			if json.Unmarshal([]byte(e.data), &c) == nil && c.Name == "search_chats" {
				ids[c.ID] = true
			}
		case turn.EventToolResult:
			var r turn.ToolResultEvent
			if json.Unmarshal([]byte(e.data), &r) == nil && ids[r.ID] && !called {
				result, called = seam.Text(seam.Message{Blocks: r.Content}), !r.IsError
			}
		case turn.EventText:
			var d turn.DeltaEvent
			if json.Unmarshal([]byte(e.data), &d) == nil {
				reply += d.Text
			}
		}
	}
	return result, called, reply
}

func pastChatsProblems(g *knowledgeRig, fx pastChatsFixture, c *counted) ([]string, error) {
	ctx := context.Background()
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if !strings.Contains(g.defaultText(recap.SettingPrompt), recapScriptPhrase) {
		return nil, fmt.Errorf("the shipped %s does not hold %q, which the test tenant's recorded recaps look for", recap.SettingPrompt, recapScriptPhrase)
	}
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

	// 1. Each chat's recorded recap, through the route.
	for _, want := range fx.Recaps {
		id := idOf[want.Title]
		if id == "" {
			return nil, fmt.Errorf("the test tenant has no chat titled %q", want.Title)
		}
		got, requestID, err := g.recapRoute("POST", token, id)
		if err != nil {
			return nil, err
		}
		r := got.Recap
		if !got.Made || !strings.Contains(r.Summary, want.Summary) || strings.Join(r.Decisions, "\n") != strings.Join(want.Decisions, "\n") || r.Sensitive || r.RequestID != requestID {
			say("the chat %q was given the recap %+v, want a summary holding %q and the decisions %q", want.Title, r, want.Summary, want.Decisions)
		}
		if recs, err := g.kindOf(g.test.ID, requestID, routing.KindSummary); err != nil {
			return nil, err
		} else if len(recs) != 1 {
			say("the recap of %q left %d usage records of the kind summary under its request id, want one", want.Title, len(recs))
		}
	}
	if chats, err = g.chatList(token); err != nil {
		return nil, err
	}
	for _, ch := range chats {
		for _, want := range fx.Recaps {
			if ch.Title == want.Title && !strings.Contains(ch.Summary, want.Summary) {
				say("the chat list gives %q the summary %q", ch.Title, ch.Summary)
			}
		}
	}

	// 2. Each question, answered through the past-chat search.
	for _, q := range fx.Questions {
		t, err := g.send(token, "", q.Ask, "")
		if err != nil {
			return nil, err
		}
		result, called, reply := searched(t.stream)
		if !called {
			say("%q was not answered through search_chats", q.Ask)
			continue
		}
		if !strings.Contains(result, fmt.Sprintf("%q", q.Chat)) || !strings.Contains(result, idOf[q.Chat]) {
			say("the search for %q does not name the chat %q:\n%s", q.Ask, q.Chat, result)
		}
		for _, w := range q.Result {
			if !strings.Contains(result, w) {
				say("the search for %q does not hold %q:\n%s", q.Ask, w, result)
			}
		}
		for _, w := range q.Reply {
			if !strings.Contains(reply, w) {
				say("the reply to %q does not hold %q: %s", q.Ask, w, reply)
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
		if len(entries) != 2 || entries[0].Action != "search_chats" || entries[0].Outcome != audit.OutcomeStarted || entries[1].Outcome != audit.OutcomeOK {
			say("the search for %q is recorded in the audit log as %d entries, want one before and one after", q.Ask, len(entries))
		}
	}

	// 3. A turn's end gives its chat a recap.
	if err := g.set(g.test.ID, turn.KeyRecapAfterIdle, "0s"); err != nil {
		return nil, err
	}
	t, err := g.send(token, "", fx.Questions[0].Ask, "")
	if err != nil {
		return nil, err
	}
	if got, err := g.waitRecap(token, t.chatID); err != nil {
		say("%v", err)
	} else if got.Recap.RequestID != t.requestID || got.Recap.Turns != 1 {
		say("the recap made after a turn is %+v, want one under the turn's request id after one turn", got.Recap)
	}

	// 4. Fixed rule 3, on an invented tenant.
	person, err := g.in.Create("Past Chats Check Person")
	if err != nil {
		return nil, err
	}
	err = g.with(person.ID, func(s *tenant.Store) error {
		o, err := signin.Defaults()
		if err != nil {
			return err
		}
		if err := signin.SetPassword(s, pastChatsPassword, o); err != nil {
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
	if err := g.seed(person.ID); err != nil {
		return nil, err
	}
	if err := g.use(person.ID, builtin.Name); err != nil {
		return nil, err
	}
	if err := g.set(person.ID, memory.SettingOn, "false", turn.KeyTitleAuto, "false", turn.KeySummaryModel, pastHosted.Name); err != nil {
		return nil, err
	}
	other, rec := g.w.signIn(person.ID, pastChatsPassword)
	if other == "" {
		return nil, fmt.Errorf("the invented tenant cannot sign in through the API: status %d", rec.Code)
	}
	marker := fx.Held.Marker
	held, err := g.send(other, "", fx.Held.Ask, pastLocal.Name)
	if err != nil {
		return nil, err
	}
	if h, err := g.held(person.ID, held.chatID); err != nil {
		return nil, err
	} else if !h {
		return nil, fmt.Errorf("the question %q did not hold its chat, so the check proves nothing", fx.Held.Ask)
	}
	made, _, err := g.recapRoute("POST", other, held.chatID)
	if err != nil {
		return nil, err
	}
	if !made.Recap.Sensitive || made.Recap.Model != pastLocal.Name || !strings.Contains(made.Recap.Summary, marker) {
		say("the held chat's recap is %+v, want a sensitive recap by %s holding %q", made.Recap, pastLocal.Name, marker)
	}
	if n := len(c.sentWith(pastHosted.Name, recapScriptPhrase)); n != 0 {
		say("the hosted summary model was asked %d times for a held chat's recap", n)
	}

	// The search, run for the hosted model.
	t, err = g.send(other, "", fx.Held.Question, pastHosted.Name)
	if err != nil {
		return nil, err
	}
	result, called, _ := searched(t.stream)
	switch {
	case !called:
		say("%q on the hosted model was not answered through search_chats", fx.Held.Question)
	case strings.Contains(result, held.chatID) || strings.Contains(result, marker):
		say("the search for the hosted model gave the held chat:\n%s", result)
	case !strings.Contains(result, "left out"):
		say("the search for the hosted model does not say a chat was left out:\n%s", result)
	}
	if n := len(c.sentWith(pastHosted.Name, marker)); n != 0 {
		say("the hosted model was sent the held chat's marker %q in %d requests", marker, n)
	}
	if all, _, err := g.calls(person.ID, t.requestID); err != nil {
		return nil, err
	} else if strings.Contains(all, marker) {
		say("the audit log holds a call to the hosted model with the held chat's marker %q", marker)
	}

	// The search, run for the local model.
	t, err = g.send(other, "", fx.Held.Question, pastLocal.Name)
	if err != nil {
		return nil, err
	}
	if result, called, _ := searched(t.stream); !called || !strings.Contains(result, held.chatID) || !strings.Contains(result, marker) {
		say("the search for the local model did not give the held chat with its recap:\n%s", result)
	}
	if h, err := g.held(person.ID, t.chatID); err != nil {
		return nil, err
	} else if !h {
		say("the chat given a held chat's recap is not held")
	}
	return problems, nil
}

// waitRecap waits for a chat's recap to be made after its turn.
func (g *knowledgeRig) waitRecap(token, chatID string) (turn.RecapReply, error) {
	deadline := time.Now().Add(20 * time.Second)
	for {
		got, _, err := g.recapRoute("GET", token, chatID)
		if err != nil {
			return got, err
		}
		if got.Made {
			return got, nil
		}
		if time.Now().After(deadline) {
			return got, fmt.Errorf("chat %s was not given a recap after its turn", chatID)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
