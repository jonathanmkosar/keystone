package turn

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
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

// Chat recaps and the past-chat search (P5-05). Every chat, name and fact
// here is invented.

// recapPhrase is in the shipped recap prompt, and in no other call.
const recapPhrase = "summary, topics and decisions of one chat"

// The test tenant's recorded questions about earlier chats, in
// fixtures/test-tenant/turns/past-chats.json.
const (
	walkQuestion  = "What did I decide about the lighthouse walk?"
	ferryQuestion = "What times did you find for the north ferry?"
	sleepQuestion = "Have we talked about sleep before?"
	sleepAsk      = "What helped with the insomnia, and what did the clinic letter say?"
)

// serveRecaps starts a server as serveTools does, with the built-in tools
// and the recap routes beside the chat and turn routes. A nil provider is
// the fake playing the shipped recorded turns.
func (g *rig) serveRecaps(provider seam.Provider) *served {
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
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, append(Routes(runner), RecapRoutes(runner)...))
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

// chatTitled returns the id of a tenant's chat with a title.
func (g *rig) chatTitled(tenantID, title string) string {
	g.t.Helper()
	var id string
	g.with(tenantID, func(s *tenant.Store) {
		chats, err := chat.List(s, chat.Filter{})
		if err != nil {
			g.t.Fatal(err)
		}
		for _, c := range chats {
			if c.Title == title {
				id = c.ID
			}
		}
	})
	if id == "" {
		g.t.Fatalf("no chat is titled %q", title)
	}
	return id
}

// recapOf reads a chat's recap through the route.
func (sv *served) recapOf(tenantID, chatID string) RecapReply {
	sv.g.t.Helper()
	var reply RecapReply
	sv.call(tenantID, "GET", "chats/"+chatID+"/recap", "", http.StatusOK, &reply)
	return reply
}

// waitRecap waits until a chat has a recap made after its turns numbered
// turns had ended, and returns it.
func (sv *served) waitRecap(tenantID, chatID string, turns int) RecapReply {
	sv.g.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		got := sv.recapOf(tenantID, chatID)
		if got.Made && got.Recap.Turns >= turns {
			return got
		}
		if time.Now().After(deadline) {
			sv.g.t.Fatalf("chat %s was not given a recap after %d turns: %+v", chatID, turns, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// makeRecap asks for a chat's recap through the route, and returns it with
// the request id it was made under.
func (sv *served) makeRecap(tenantID, chatID string, want int) (RecapReply, string) {
	sv.g.t.Helper()
	resp := sv.request(tenantID, "POST", "chats/"+chatID+"/recap", "", nil)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		sv.g.t.Fatalf("POST recap answered %d, want %d: %s", resp.StatusCode, want, raw)
	}
	var reply RecapReply
	if want == http.StatusOK {
		if err := json.Unmarshal(raw, &reply); err != nil {
			sv.g.t.Fatal(err)
		}
	}
	return reply, resp.Header.Get("X-Request-Id")
}

func ask(text, model string) string {
	body, _ := json.Marshal(SendBody{Text: text, Model: model})
	return string(body)
}

// A chat is given its recap after its first turn, by the model routing
// gives the summary task, under the turn's request id; not after the next;
// and again once chats.recap_after_turns more turns have ended.
func TestAChatIsGivenItsRecapWhenItIsDue(t *testing.T) {
	g := newRig(t)
	g.setFor(g.test.ID, tenantsettings.Person, KeyRecapAfterIdle, "0s")
	g.setFor(g.test.ID, tenantsettings.Person, KeyRecapAfterTurns, "2")
	sv := g.serveRecaps(nil)
	id := sv.newChat(g.test.ID)
	if got := sv.recapOf(g.test.ID, id); got.Made || got.Recap.Topics == nil || got.Recap.Decisions == nil {
		t.Fatalf("a new chat has a recap, or lists that are not lists: %+v", got)
	}

	events, _, requestID := sv.send(g.test.ID, id, ask(bakery, ""))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	first := sv.waitRecap(g.test.ID, id, 1)
	r := first.Recap
	if !strings.Contains(r.Summary, "Tide and Crumb") || strings.Join(r.Topics, "|") != "bakery names|the harbour" || len(r.Decisions) != 0 ||
		r.Turns != 1 || r.Model != TestModelName || r.Sensitive || r.RequestID != requestID {
		t.Fatalf("the recap after the first turn is %+v", r)
	}
	if got := kinds(g, g.test.ID, requestID)[string(routing.KindSummary)]; len(got) != 1 || got[0].Outcome != "ok" {
		t.Fatalf("the recap's call has the usage records %+v, want one of the kind summary under the turn's request id", got)
	}
	var list ListReply
	sv.call(g.test.ID, "GET", "chats", "", http.StatusOK, &list)
	for _, c := range list.Chats {
		if (c.ID == id) != (c.Summary != "") {
			t.Errorf("the list gives chat %q the summary %q", c.Title, c.Summary)
		}
	}

	// One more turn is not enough.
	sv.send(g.test.ID, id, ask("And one more thing.", ""))
	time.Sleep(300 * time.Millisecond)
	if got := sv.recapOf(g.test.ID, id); got.Recap.Turns != 1 || !got.Recap.Made.Equal(r.Made) {
		t.Fatalf("a chat one turn past its recap was given another: %+v", got.Recap)
	}
	// Two more are.
	sv.send(g.test.ID, id, ask("And another.", ""))
	if got := sv.waitRecap(g.test.ID, id, 3); got.Recap.ThroughID <= r.ThroughID {
		t.Fatalf("the newer recap stands for no newer message: %+v", got.Recap)
	}
}

// The recap waits until the chat has rested for chats.recap_after_idle,
// and a turn switched off makes none.
func TestARecapWaitsForTheChatToRest(t *testing.T) {
	g := newRig(t)
	g.setFor(g.test.ID, tenantsettings.Person, KeyRecapAfterIdle, "1s")
	sv := g.serveRecaps(nil)
	id := sv.newChat(g.test.ID)
	sv.send(g.test.ID, id, ask(bakery, ""))
	if got := sv.recapOf(g.test.ID, id); got.Made {
		t.Fatal("the recap was made before the chat had rested")
	}
	sv.waitRecap(g.test.ID, id, 1)

	g.setFor(g.test.ID, tenantsettings.Person, KeyRecapAfterIdle, "0s")
	g.setFor(g.test.ID, tenantsettings.Person, KeyRecap, "false")
	off := sv.newChat(g.test.ID)
	sv.send(g.test.ID, off, ask(bakery, ""))
	time.Sleep(300 * time.Millisecond)
	if got := sv.recapOf(g.test.ID, off); got.Made {
		t.Fatalf("a chat was given a recap with recaps switched off: %+v", got)
	}
}

// The routes: a recap read and made on asking, with the recorded recap of
// one of the test tenant's chats; a chat with no message has none to make;
// another tenant's chat is not found.
func TestTheRecapRoutes(t *testing.T) {
	g := newRig(t)
	sv := g.serveRecaps(nil)
	walk := g.chatTitled(g.test.ID, "Planning the lighthouse walk")
	if got := sv.recapOf(g.test.ID, walk); got.Made {
		t.Fatalf("a loaded chat has a recap: %+v", got)
	}
	got, requestID := sv.makeRecap(g.test.ID, walk, http.StatusOK)
	if !got.Made || !strings.Contains(got.Recap.Summary, "old lighthouse on Saturday") || len(got.Recap.Decisions) != 2 ||
		got.Recap.Decisions[0] != "Walk to the old lighthouse on Saturday." || got.Recap.RequestID != requestID || got.Recap.ThroughID == 0 {
		t.Fatalf("the lighthouse chat's recap is %+v", got)
	}
	if again := sv.recapOf(g.test.ID, walk); again.Recap.Summary != got.Recap.Summary {
		t.Fatalf("the recap read back is %+v", again)
	}
	if recs := kinds(g, g.test.ID, requestID)[string(routing.KindSummary)]; len(recs) != 1 {
		t.Fatalf("the route's recap has %d usage records of the kind summary under its request id", len(recs))
	}
	// Made again with nothing said since, it is the same recap.
	if same, _ := sv.makeRecap(g.test.ID, walk, http.StatusOK); !same.Recap.Made.Equal(got.Recap.Made) {
		t.Errorf("a recap was made again for a chat with nothing new: %+v", same.Recap)
	}
	sv.makeRecap(g.test.ID, sv.newChat(g.test.ID), http.StatusBadRequest)
	resp := sv.request(g.ordinary.ID, "GET", "chats/"+walk+"/recap", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("another tenant's chat's recap answered %d", resp.StatusCode)
	}
	resp = sv.request(g.ordinary.ID, "POST", "chats/"+walk+"/recap", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("making another tenant's chat's recap answered %d", resp.StatusCode)
	}
}

// The item's proof: the test tenant's questions about earlier chats are
// answered through the past-chat search, which gives each chat's recap.
func TestAQuestionAboutAnEarlierChatIsAnsweredThroughTheTool(t *testing.T) {
	g := newRig(t)
	sv := g.serveRecaps(nil)
	for _, title := range []string{"Planning the lighthouse walk", "North ferry times"} {
		sv.makeRecap(g.test.ID, g.chatTitled(g.test.ID, title), http.StatusOK)
	}
	for _, c := range []struct {
		ask, call string
		result    []string
		reply     string
	}{
		{walkQuestion, "call_past_walk", []string{`"Planning the lighthouse walk"`, "Summary: The person planned a walk to the old lighthouse", "Decisions:\n- Walk to the old lighthouse on Saturday."}, "walk to the old lighthouse on Saturday"},
		{ferryQuestion, "call_past_ferry", []string{`"North ferry times"`, "Topics: the north ferry; Tuesday sailings; timetables", "09:00"}, "at 09:00 and at 11:30"},
	} {
		events, _, requestID := sv.send(g.test.ID, sv.newChat(g.test.ID), ask(c.ask, ""))
		if end := last(t, events); end.Status != StatusDone {
			t.Fatalf("%q ended as %+v", c.ask, end)
		}
		res, ok := results(t, events)[c.call]
		if !ok || res.IsError {
			t.Fatalf("%q: the search has no result, or an error: %+v", c.ask, res)
		}
		for _, want := range c.result {
			if !strings.Contains(said(res), want) {
				t.Errorf("%q: the search's result does not hold %q:\n%s", c.ask, want, said(res))
			}
		}
		if text := joined(t, events, EventText); !strings.Contains(text, c.reply) {
			t.Errorf("%q: the reply does not hold %q: %s", c.ask, c.reply, text)
		}
		if entries := g.toolEntries(requestID); len(entries) != 2 || entries[0].Action != builtin.SearchChats || entries[1].Outcome != "ok" {
			t.Errorf("%q: the search is recorded as %+v", c.ask, entries)
		}
	}
}

// recapEars plays the shipped recorded turns for registry models, and keeps
// every request each model is sent.
type recapEars struct {
	mu   sync.Mutex
	sent map[string][]string
	p    seam.Provider
}

func (h *recapEars) builder(m models.Model, _ string) (seam.Provider, error) {
	return recapEar{h, m.Name}, nil
}

func (h *recapEars) all(name string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.sent[name], "\n")
}

type recapEar struct {
	h    *recapEars
	name string
}

func (f recapEar) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	raw, err := seam.EncodeRequest(req)
	if err != nil {
		return err
	}
	f.h.mu.Lock()
	f.h.sent[f.name] = append(f.h.sent[f.name], string(raw))
	f.h.mu.Unlock()
	return f.h.p.Stream(ctx, req, emit)
}

// Two models that can use tools, one local and one hosted, both answered
// by the shipped recorded turns.
var (
	recapHouse = models.Model{
		Name: "house-recap", Label: "House Recap", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
		ProviderModel: "small-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
		Capabilities: []models.Capability{models.CapTools},
	}
	recapPaid = models.Model{
		Name: "paid-recap", Label: "Paid Recap", Adapter: models.AdapterAnthropic, Endpoint: "https://api.provider-one.example",
		ProviderModel: "example-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassHosted,
		Capabilities: []models.Capability{models.CapTools},
	}
)

func heldRig(t *testing.T) (*rig, *served, *recapEars) {
	g := newRig(t)
	p, err := testtenant.Provider(g.fx, fake.Options{DeltaSize: 12})
	if err != nil {
		t.Fatal(err)
	}
	h := &recapEars{sent: map[string][]string{}, p: p}
	g.extra = adapters.Set{recapHouse.Adapter: h.builder, recapPaid.Adapter: h.builder}
	g.addModels(recapHouse, recapPaid)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	g.set(tenantsettings.Person, KeySummaryModel, recapPaid.Name)
	return g, g.serveRecaps(nil), h
}

// Fixed rule 3. A chat that was sent sensitive content is given its recap
// by no hosted model, even the one the summary rule names, and the recap is
// sensitive. The past-chat search leaves it out for a call to the hosted
// model, which never sees a word of it, and gives it to a call to the
// local model, which holds the chat that asked.
func TestAHeldChatsRecapReachesNoModelThatMayNotReadIt(t *testing.T) {
	g, sv, h := heldRig(t)
	held := sv.newChat(g.ordinary.ID)
	events, turnID, _ := sv.send(g.ordinary.ID, held, ask(sleepAsk, recapHouse.Name))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if err := recall.Hold(context.Background(), s, turnID, held); err != nil {
			t.Fatal(err)
		}
	})
	made, _ := sv.makeRecap(g.ordinary.ID, held, http.StatusOK)
	if !made.Recap.Sensitive || made.Recap.Model != recapHouse.Name || !strings.Contains(made.Recap.Summary, "Melatonin") {
		t.Fatalf("the held chat's recap is %+v", made.Recap)
	}
	if strings.Contains(h.all(recapPaid.Name), recapPhrase) {
		t.Fatal("the hosted summary model was asked for a held chat's recap")
	}

	// The hosted model's call: the held chat is left out, and counted.
	asking := sv.newChat(g.ordinary.ID)
	events, _, _ = sv.send(g.ordinary.ID, asking, ask(sleepQuestion, recapPaid.Name))
	res := results(t, events)["call_past_sleep"]
	if res.IsError || !strings.Contains(said(res), "1 chat matched and is left out") || strings.Contains(said(res), held) || strings.Contains(said(res), "Melatonin") {
		t.Fatalf("the search for the hosted model gave:\n%s", said(res))
	}
	if sent := h.all(recapPaid.Name); strings.Contains(sent, "Melatonin") || strings.Contains(sent, held) {
		t.Fatal("the hosted model was sent the held chat's recap")
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if h, _ := recall.Held(context.Background(), s, asking); h {
			t.Error("a chat that was given nothing sensitive is held")
		}
	})

	// The local model's call: the held chat is given, and the chat that
	// asked is held from then on.
	local := sv.newChat(g.ordinary.ID)
	events, _, _ = sv.send(g.ordinary.ID, local, ask(sleepQuestion, recapHouse.Name))
	res = results(t, events)["call_past_sleep"]
	if res.IsError || !strings.Contains(said(res), held) || !strings.Contains(said(res), "Melatonin helped") {
		t.Fatalf("the search for the local model gave:\n%s", said(res))
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if h, err := recall.Held(context.Background(), s, local); err != nil || !h {
			t.Errorf("the chat given a held chat's recap is not held (%v)", err)
		}
	})
	// And from then on it goes to no hosted model.
	events, _, _ = sv.send(g.ordinary.ID, local, ask("And anything else?", recapPaid.Name))
	if end := last(t, events); end.Error == nil || end.Error.Code != CodeSensitive {
		t.Fatalf("the chat given a held chat's recap went to the hosted model: %+v", end)
	}
}

// read_chat follows the same rule: a held chat is not read for the hosted
// model, and is for the local one.
func TestReadChatLeavesAHeldChatOutForAHostedModel(t *testing.T) {
	g := newRig(t)
	var held string
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		c, err := chat.Create(s)
		if err != nil {
			t.Fatal(err)
		}
		held = c.ID
		if _, err := chat.Append(s, held, chat.NewMessage{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Melatonin helped in March."}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES('turn-held', ?, 'done', 'req-held', 'person', '{}', '2026-11-25T10:00:00.000Z')", held); err != nil {
			t.Fatal(err)
		}
		if err := recall.Hold(context.Background(), s, "turn-held", held); err != nil {
			t.Fatal(err)
		}
		if _, err := recap.Save(context.Background(), s, recap.Recap{ChatID: held, Summary: "Sleep, and what helped.", Model: recapHouse.Name, Sensitive: true}); err != nil {
			t.Fatal(err)
		}
	})
	p := scripted(t, `{"test_data":true,"scripts":[
		{"name":"read it","when":{"user_text":"Read the chat about sleep."},"steps":[
			{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_read","name":"read_chat","input":{"chat_id":"`+held+`"}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
			{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"Done."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
		{"name":"anything else","when":{},"steps":[{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"A plain answer."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]}
	]}`)
	h := &recapEars{sent: map[string][]string{}, p: p}
	g.extra = adapters.Set{recapHouse.Adapter: h.builder, recapPaid.Adapter: h.builder}
	g.addModels(recapHouse, recapPaid)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	sv := g.serveRecaps(nil)

	events, _, _ := sv.send(g.ordinary.ID, sv.newChat(g.ordinary.ID), ask("Read the chat about sleep.", recapPaid.Name))
	res := results(t, events)["call_read"]
	if !res.IsError || !strings.Contains(said(res), "holds content from a sensitive space") || strings.Contains(said(res), "Melatonin") {
		t.Fatalf("read_chat for the hosted model gave: %+v %s", res, said(res))
	}
	if strings.Contains(h.all(recapPaid.Name), "Melatonin") {
		t.Fatal("the hosted model was sent the held chat")
	}
	local := sv.newChat(g.ordinary.ID)
	events, _, _ = sv.send(g.ordinary.ID, local, ask("Read the chat about sleep.", recapHouse.Name))
	if res := results(t, events)["call_read"]; res.IsError || !strings.Contains(said(res), "Melatonin helped in March.") || !strings.Contains(said(res), "Summary: Sleep, and what helped.") {
		t.Fatalf("read_chat for the local model gave: %s", said(res))
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if h, _ := recall.Held(context.Background(), s, local); !h {
			t.Error("the chat that read a held chat is not held")
		}
	})
}
