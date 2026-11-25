package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// Every person, place and fact in these tests is invented.

// The messages of the test tenant's recorded memory turns, in
// fixtures/test-tenant/turns/memory.json.
const (
	beesAsk   = "I keep two beehives at the allotment and look in on them every Sunday morning. What should I check for in late autumn?"
	sisterAsk = "My sister is coming to stay for the first week of December, and she does not eat meat. What could I cook for her first night?"
)

// memoryPhrase is in the shipped memory prompt, and in no other call.
const memoryPhrase = "long-term memory of a personal assistant"

// serveMemory starts a server as serve does, with the memory routes beside
// the chat and turn routes, and the test tenant on its recorded turns.
func (g *rig) serveMemory() *served {
	g.t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 50 * time.Millisecond
	provider, err := testtenant.Provider(g.fx, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	set, err := adapters.Fake(g.fx.Scripts, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	for name, build := range g.extra {
		set[name] = build
	}
	runner := NewRunner(Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Adapters: set})
	runner.Recover()
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, append(Routes(runner), MemoryRoutes(runner)...))
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

// setFor stores a setting for any tenant of the rig.
func (g *rig) setFor(tenantID string, by tenantsettings.Role, key, value string) {
	g.t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		g.t.Fatal(err)
	}
	g.with(tenantID, func(s *tenant.Store) {
		if err := tenantsettings.Set(context.Background(), s, reg, by, key, value); err != nil {
			g.t.Fatal(err)
		}
	})
}

// waitPass waits for a turn's memory pass to end, and returns it.
func waitPass(g *rig, tenantID, turnID string) memory.Pass {
	g.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var p memory.Pass
		var ok bool
		g.with(tenantID, func(s *tenant.Store) {
			var err error
			if p, ok, err = memory.Get(context.Background(), s, turnID); err != nil {
				g.t.Fatal(err)
			}
		})
		if ok && p.Status != memory.StatusQueued && p.Status != memory.StatusRunning {
			return p
		}
		if time.Now().After(deadline) {
			g.t.Fatalf("turn %s's memory pass did not end: %+v (queued: %v)", turnID, p, ok)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// kinds returns the usage records of a request, by kind.
func kinds(g *rig, tenantID, requestID string) map[string][]usage.Record {
	g.t.Helper()
	out := map[string][]usage.Record{}
	g.with(tenantID, func(s *tenant.Store) {
		records, err := usage.List(context.Background(), s, usage.Filter{RequestID: requestID})
		if err != nil {
			g.t.Fatal(err)
		}
		for _, r := range records {
			out[r.Kind] = append(out[r.Kind], r)
		}
	})
	return out
}

// sourceOf reads a source back from a tenant's built-in store.
func sourceOf(g *rig, tenantID, id string) knowledge.Source {
	g.t.Helper()
	var src knowledge.Source
	g.with(tenantID, func(s *tenant.Store) {
		ks, err := knowledge.Open(context.Background(), builtin.Name, s)
		if err != nil {
			g.t.Fatal(err)
		}
		defer ks.Close()
		if src, err = ks.ReadSource(context.Background(), id); err != nil {
			g.t.Fatalf("source %s: %v", id, err)
		}
	})
	return src
}

// The item's proof in the test tenant: a chat whose exchange holds durable
// facts adds them, after the turn, to the built-in store as one source that
// points back to the chat, through a model call recorded with the kind
// memory. A message with nothing durable adds nothing.
func TestTheMemoryPassAddsFactsAfterTheTurn(t *testing.T) {
	g := newRig(t)
	g.setFor(g.test.ID, tenantsettings.Person, knowledge.SettingAdapter, builtin.Name)
	sv := g.serveMemory()

	id := sv.newChat(g.test.ID)
	body, _ := json.Marshal(SendBody{Text: beesAsk})
	events, turnID, requestID := sv.send(g.test.ID, id, string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	p := waitPass(g, g.test.ID, turnID)
	want := []string{"The person keeps two beehives at the allotment.", "The person looks in on the beehives every Sunday morning."}
	if p.Status != memory.StatusDone || p.Space != "memory" || p.Sensitive || p.SourceID == "" || strings.Join(p.Facts, "|") != strings.Join(want, "|") || p.Model != TestModelName {
		t.Fatalf("the pass ended as %+v", p)
	}
	src := sourceOf(g, g.test.ID, p.SourceID)
	if src.Origin != memory.Origin(id, turnID) || src.Space != "memory" || src.Label != knowledge.LabelNormal || !strings.Contains(src.Text, want[0]) || !strings.Contains(src.Text, want[1]) {
		t.Errorf("the source is %+v", src)
	}
	if got := kinds(g, g.test.ID, requestID); len(got[string(routing.KindMemory)]) != 1 || got[string(routing.KindMemory)][0].Outcome != usage.OutcomeOK || len(got[string(routing.KindChat)]) != 1 {
		t.Errorf("the request's usage records are %+v", got)
	}
	var reply MemoryReply
	sv.call(g.test.ID, "GET", "chats/"+id+"/memory", "", http.StatusOK, &reply)
	if reply.ChatID != id || reply.Incognito || len(reply.Passes) != 1 || reply.Passes[0].SourceID != p.SourceID || len(reply.Passes[0].Facts) != 2 {
		t.Errorf("the memory route answered %+v", reply)
	}

	// A message with nothing durable in it.
	id = sv.newChat(g.test.ID)
	body, _ = json.Marshal(SendBody{Text: bakery})
	_, turnID, _ = sv.send(g.test.ID, id, string(body))
	if p := waitPass(g, g.test.ID, turnID); p.Status != memory.StatusDone || len(p.Facts) != 0 || p.SourceID != "" {
		t.Errorf("a message with nothing durable gave %+v", p)
	}

	// With no store, or the pass switched off, nothing is queued.
	for key, value := range map[string]string{knowledge.SettingAdapter: knowledge.None, memory.SettingOn: "false"} {
		g.setFor(g.test.ID, tenantsettings.Person, knowledge.SettingAdapter, builtin.Name)
		g.setFor(g.test.ID, tenantsettings.Person, memory.SettingOn, "true")
		g.setFor(g.test.ID, tenantsettings.Person, key, value)
		id = sv.newChat(g.test.ID)
		body, _ = json.Marshal(SendBody{Text: beesAsk})
		sv.send(g.test.ID, id, string(body))
		body, _ = json.Marshal(SendBody{Text: sisterAsk})
		sv.send(g.test.ID, id, string(body))
		sv.call(g.test.ID, "GET", "chats/"+id+"/memory", "", http.StatusOK, &reply)
		if len(reply.Passes) != 0 {
			t.Errorf("with %s set to %s a pass was queued: %+v", key, value, reply.Passes)
		}
	}
}

// memoryAnswers answers a chat with a short reply and a memory pass with
// facts, keeps every request by the model it went to, and, with hold set,
// keeps a memory call waiting until hold is closed.
type memoryAnswers struct {
	mu    *sync.Mutex
	got   map[string][]string
	name  string
	facts string
	hold  chan struct{}
}

func isMemory(req seam.Request) bool {
	return len(req.System) > 0 && strings.Contains(req.System[0].Text, memoryPhrase)
}

func (p memoryAnswers) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	raw, _ := seam.EncodeRequest(req)
	p.mu.Lock()
	p.got[p.name] = append(p.got[p.name], string(raw))
	p.mu.Unlock()
	text := "A reply."
	if isMemory(req) {
		if p.hold != nil {
			select {
			case <-p.hold:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		text = p.facts
	}
	events, err := seam.Events(seam.Response{
		Model:      req.Model,
		Message:    seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}},
		StopReason: seam.StopEnd,
		Usage:      seam.Usage{InputTokens: 100, OutputTokens: 20},
	})
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := emit(e); err != nil {
			return err
		}
	}
	return nil
}

// ears is the requests each model was sent, as memoryAnswers keeps them.
type heardBy struct {
	mu  sync.Mutex
	got map[string][]string
}

func (h *heardBy) builder(facts string, hold chan struct{}) func(models.Model, string) (seam.Provider, error) {
	return func(m models.Model, _ string) (seam.Provider, error) {
		return memoryAnswers{mu: &h.mu, got: h.got, name: m.Name, facts: facts, hold: hold}, nil
	}
}

// memoryCalls counts the memory calls a model was sent, and returns them.
func (h *heardBy) memoryCalls(name string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, r := range h.got[name] {
		if strings.Contains(r, memoryPhrase) {
			out = append(out, r)
		}
	}
	return out
}

const twoFacts = `{"facts": ["The person grows runner beans.", "The person waters the beans every evening."]}`

// The reply is never held for its memory pass: the turn ends while the
// memory call waits, and the facts land when it answers.
func TestAReplyIsNotHeldForItsMemoryPass(t *testing.T) {
	g := newRig(t)
	g.addModels(houseModel)
	hold := make(chan struct{})
	h := &heardBy{got: map[string][]string{}}
	g.extra = adapters.Set{models.AdapterChatCompletions: h.builder(twoFacts, hold)}
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	g.set(tenantsettings.Person, knowledge.SettingAdapter, builtin.Name)
	sv := g.serveMemory()

	id := sv.newChat(g.ordinary.ID)
	body, _ := json.Marshal(SendBody{Text: "When do the runner beans go in?"})
	events, turnID, _ := sv.send(g.ordinary.ID, id, string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(h.memoryCalls(houseModel.Name)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		p, ok, err := memory.Get(context.Background(), s, turnID)
		if err != nil || !ok || p.Status != memory.StatusRunning {
			t.Errorf("while its call waits, the pass is %+v (%v, %v)", p, ok, err)
		}
	})
	close(hold)
	if p := waitPass(g, g.ordinary.ID, turnID); p.Status != memory.StatusDone || len(p.Facts) != 2 || p.Model != houseModel.Name {
		t.Errorf("the pass ended as %+v", p)
	}
}

// An incognito chat queues no memory pass for any of its turns, and runs
// none it had queued. The flag is a property of the chat with a route.
func TestAnIncognitoChatRunsNoMemoryPass(t *testing.T) {
	g := newRig(t)
	g.setFor(g.test.ID, tenantsettings.Person, knowledge.SettingAdapter, builtin.Name)
	sv := g.serveMemory()
	id := sv.newChat(g.test.ID)

	var reply MemoryReply
	sv.call(g.test.ID, "PUT", "chats/"+id+"/incognito", "", http.StatusOK, &reply)
	if !reply.Incognito || reply.ChatID != id {
		t.Fatalf("making the chat incognito answered %+v", reply)
	}
	body, _ := json.Marshal(SendBody{Text: beesAsk})
	events, quiet, requestID := sv.send(g.test.ID, id, string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("an incognito turn ended as %+v", end)
	}
	sv.call(g.test.ID, "DELETE", "chats/"+id+"/incognito", "", http.StatusOK, &reply)
	if reply.Incognito {
		t.Fatalf("making the chat ordinary answered %+v", reply)
	}
	// The chat's turns are worked one at a time, so the incognito turn's
	// pass, had it been queued, would be there before this one's.
	body, _ = json.Marshal(SendBody{Text: sisterAsk})
	_, turnID, _ := sv.send(g.test.ID, id, string(body))
	if p := waitPass(g, g.test.ID, turnID); p.Status != memory.StatusDone || len(p.Facts) != 2 {
		t.Fatalf("the ordinary turn's pass ended as %+v", p)
	}
	sv.call(g.test.ID, "GET", "chats/"+id+"/memory", "", http.StatusOK, &reply)
	if len(reply.Passes) != 1 || reply.Passes[0].TurnID != turnID {
		t.Errorf("the chat's passes are %+v; the incognito turn was %s", reply.Passes, quiet)
	}
	if got := kinds(g, g.test.ID, requestID); len(got[memory.Kind]) != 0 {
		t.Errorf("the incognito turn made %d memory calls", len(got[memory.Kind]))
	}

	// A pass that waits when the chat is made incognito is skipped.
	g.with(g.test.ID, func(s *tenant.Store) {
		if _, err := s.DB().Exec("UPDATE memory_passes SET status = 'queued', facts = '[]' WHERE turn_id = ?", turnID); err != nil {
			t.Fatal(err)
		}
	})
	sv.call(g.test.ID, "PUT", "chats/"+id+"/incognito", "", http.StatusOK, &reply)
	sv.runner.memoryWork(g.test.ID, id)
	if p := waitPass(g, g.test.ID, turnID); p.Status != memory.StatusSkipped || p.Reason != memory.ReasonIncognito {
		t.Errorf("a waiting pass in a chat made incognito ended as %+v", p)
	}

	// A chat that is not the person's is not found.
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		path := "chats/no-such-chat/incognito"
		if method == "GET" {
			path = "chats/no-such-chat/memory"
		}
		resp := sv.request(g.test.ID, method, path, "", nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s answered %d", method, path, resp.StatusCode)
		}
	}
	other := sv.newChat(g.ordinary.ID)
	resp := sv.request(g.test.ID, "PUT", "chats/"+other+"/incognito", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("another tenant's chat was made incognito: %d", resp.StatusCode)
	}
}

// The reserve (decision 0280): a memory call to a model that is not local
// is refused once the month's memory passes and the call would pass it,
// while the chat goes on under the monthly cap; a local model behind it
// runs at no cost, also with the cap reached.
func TestTheReserveHoldsTheMemoryPass(t *testing.T) {
	g := newRig(t)
	g.addModels(paidModel, houseModel)
	h := &heardBy{got: map[string][]string{}}
	g.extra = adapters.Set{
		models.AdapterAnthropic:       h.builder(twoFacts, nil),
		models.AdapterChatCompletions: h.builder(twoFacts, nil),
	}
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	g.set(tenantsettings.Person, knowledge.SettingAdapter, builtin.Name)
	g.set(tenantsettings.Person, memory.SettingReserve, "0")
	sv := g.serveMemory()
	send := func(model string) (string, string) {
		t.Helper()
		body, _ := json.Marshal(SendBody{Text: "When do the runner beans go in?", Model: model})
		events, turnID, requestID := sv.send(g.ordinary.ID, sv.newChat(g.ordinary.ID), string(body))
		if end := last(t, events); end.Status != StatusDone {
			t.Fatalf("a turn on %s ended as %+v", model, end)
		}
		return turnID, requestID
	}

	// 1. No reserve: the chat is answered, the memory call is refused.
	turnID, requestID := send(paidModel.Name)
	p := waitPass(g, g.ordinary.ID, turnID)
	if p.Status != memory.StatusSkipped || !strings.Contains(p.Reason, "reserve") {
		t.Errorf("with no reserve the pass ended as %+v", p)
	}
	if n := len(h.memoryCalls(paidModel.Name)); n != 0 {
		t.Errorf("with no reserve the hosted model was sent %d memory calls", n)
	}
	if got := kinds(g, g.ordinary.ID, requestID); len(got[memory.Kind]) != 1 || got[memory.Kind][0].Outcome != usage.OutcomeRefused || got[memory.Kind][0].CostMicros != 0 {
		t.Errorf("the refused memory call is recorded as %+v", got[memory.Kind])
	}

	// 2. A reserve: the hosted model reads the exchange, at a cost.
	g.set(tenantsettings.Person, memory.SettingReserve, "1")
	turnID, requestID = send(paidModel.Name)
	if p := waitPass(g, g.ordinary.ID, turnID); p.Status != memory.StatusDone || len(p.Facts) != 2 || p.Model != paidModel.Name {
		t.Fatalf("with a reserve the pass ended as %+v", p)
	}
	rec := kinds(g, g.ordinary.ID, requestID)[memory.Kind]
	if len(rec) != 1 || rec[0].Outcome != usage.OutcomeOK || rec[0].CostMicros != 400 {
		t.Errorf("the memory call is recorded as %+v", rec)
	}

	// 3. The reserve reached: the next memory call is refused, the chat is not.
	g.set(tenantsettings.Person, memory.SettingReserve, "0.0004")
	turnID, _ = send(paidModel.Name)
	if p := waitPass(g, g.ordinary.ID, turnID); p.Status != memory.StatusSkipped || !strings.Contains(p.Reason, "reserve is $0.00") {
		t.Errorf("with the reserve reached the pass ended as %+v", p)
	}
	if n := len(h.memoryCalls(paidModel.Name)); n != 1 {
		t.Errorf("the hosted model was sent %d memory calls, want the one the reserve allowed", n)
	}

	// 4. The reserve and the monthly cap reached, with a local model behind
	// the hosted one the memory rule names: the local model reads it, free.
	g.set(tenantsettings.Admin, KeyMonthlyCap, "0")
	g.set(tenantsettings.Person, KeyMemoryModel, paidModel.Name)
	for _, reserve := range []string{"0.0004", "1"} {
		g.set(tenantsettings.Person, memory.SettingReserve, reserve)
		turnID, requestID = send(houseModel.Name)
		if p := waitPass(g, g.ordinary.ID, turnID); p.Status != memory.StatusDone || p.Model != houseModel.Name || len(p.Facts) != 2 {
			t.Errorf("with a reserve of %s and the cap reached, the pass ended as %+v", reserve, p)
		}
		var cost int64
		for _, r := range kinds(g, g.ordinary.ID, requestID)[memory.Kind] {
			cost += r.CostMicros
		}
		if cost != 0 {
			t.Errorf("with a reserve of %s, the memory pass on the local model cost %d", reserve, cost)
		}
	}
	if n := len(h.memoryCalls(paidModel.Name)); n != 1 {
		t.Errorf("with the cap reached the hosted model was sent memory calls: %d in all", n)
	}
}

// Fixed rule 3: the facts of a chat that was sent sensitive content land in
// a space the store says is sensitive, read by a model that may read it,
// or nowhere, and then no model reads the exchange.
func TestAHeldChatsFactsLandInASensitiveSpaceOrNowhere(t *testing.T) {
	g := newRig(t)
	g.addModels(paidModel, houseModel)
	h := &heardBy{got: map[string][]string{}}
	g.extra = adapters.Set{
		models.AdapterAnthropic:       h.builder(`{"facts": ["The person slept badly in March."]}`, nil),
		models.AdapterChatCompletions: h.builder(`{"facts": ["The person slept badly in March."]}`, nil),
	}
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	g.set(tenantsettings.Person, KeyMemoryModel, paidModel.Name)
	seedKnowledge(g)
	sv := g.serveMemory()

	id := sv.newChat(g.ordinary.ID)
	say := func(text string) (string, string) {
		t.Helper()
		body, _ := json.Marshal(SendBody{Text: text, Model: houseModel.Name})
		events, turnID, requestID := sv.send(g.ordinary.ID, id, string(body))
		if end := last(t, events); end.Status != StatusDone {
			t.Fatalf("%q ended as %+v", text, end)
		}
		return turnID, requestID
	}

	// No sensitive space: the exchange is not read at all.
	turnID, requestID := say("What helped with the insomnia?")
	p := waitPass(g, g.ordinary.ID, turnID)
	if p.Status != memory.StatusSkipped || !strings.Contains(p.Reason, memory.SettingSensitiveSpace) {
		t.Errorf("with no sensitive space the pass ended as %+v", p)
	}
	if got := kinds(g, g.ordinary.ID, requestID); len(got[memory.Kind]) != 0 {
		t.Errorf("with no sensitive space a memory call was made: %+v", got[memory.Kind])
	}

	// A space the store does not say is sensitive is nowhere too.
	g.set(tenantsettings.Person, memory.SettingSensitiveSpace, "garden")
	turnID, _ = say("Thank you.")
	if p := waitPass(g, g.ordinary.ID, turnID); p.Status != memory.StatusSkipped || !strings.Contains(p.Reason, "garden") {
		t.Errorf("with an ordinary space named the pass ended as %+v", p)
	}

	// The sensitive space: the local model reads it, never the hosted one
	// the memory rule names, and the facts are sensitive.
	g.set(tenantsettings.Person, memory.SettingSensitiveSpace, "health")
	turnID, _ = say("Can you say that again?")
	p = waitPass(g, g.ordinary.ID, turnID)
	if p.Status != memory.StatusDone || p.Space != "health" || !p.Sensitive || p.Model != houseModel.Name || len(p.Facts) != 1 {
		t.Fatalf("with the sensitive space the pass ended as %+v", p)
	}
	if src := sourceOf(g, g.ordinary.ID, p.SourceID); src.Space != "health" || !src.Label.Sensitive() {
		t.Errorf("the source landed as %+v", src)
	}
	h.mu.Lock()
	for _, r := range h.got[paidModel.Name] {
		if strings.Contains(r, "insomnia") || strings.Contains(r, "Melatonin") {
			t.Errorf("the hosted model was sent the held chat: %s", r)
		}
	}
	h.mu.Unlock()
	if n := len(h.memoryCalls(paidModel.Name)); n != 0 {
		t.Errorf("the hosted model the memory rule names was sent %d memory calls in a held chat", n)
	}
}

// A pass a stop left running is marked interrupted and not run again; a
// pass left queued is run with the chat's next turn; an answer that is not
// the facts asked for fails the pass and adds nothing.
func TestPassesAfterAStopAndAnswersThatDoNotRead(t *testing.T) {
	g := newRig(t)
	g.addModels(houseModel)
	h := &heardBy{got: map[string][]string{}}
	g.extra = adapters.Set{models.AdapterChatCompletions: h.builder("Here are some facts about beans.", nil)}
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	g.set(tenantsettings.Person, knowledge.SettingAdapter, builtin.Name)
	sv := g.serveMemory()
	id := sv.newChat(g.ordinary.ID)
	say := func(text string) string {
		t.Helper()
		body, _ := json.Marshal(SendBody{Text: text})
		events, turnID, _ := sv.send(g.ordinary.ID, id, string(body))
		if end := last(t, events); end.Status != StatusDone {
			t.Fatalf("%q ended as %+v", text, end)
		}
		return turnID
	}

	first := say("When do the runner beans go in?")
	if p := waitPass(g, g.ordinary.ID, first); p.Status != memory.StatusFailed || !strings.Contains(p.Reason, "not the facts") || p.SourceID != "" {
		t.Errorf("an answer that is not facts ended the pass as %+v", p)
	}
	second := say("And the sweet peas?")
	waitPass(g, g.ordinary.ID, second)

	// As a stop leaves them: the first running, the second queued.
	sv.stop()
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		for turnID, status := range map[string]string{first: "running", second: "queued"} {
			if _, err := s.DB().Exec("UPDATE memory_passes SET status = ?, reason = '', ended_at = '' WHERE turn_id = ?", status, turnID); err != nil {
				t.Fatal(err)
			}
		}
	})
	g.extra = adapters.Set{models.AdapterChatCompletions: h.builder(twoFacts, nil)}
	sv = g.serveMemory()
	third := say("Thank you.")
	if p := waitPass(g, g.ordinary.ID, first); p.Status != memory.StatusInterrupted || p.Reason != memory.ReasonInterrupted {
		t.Errorf("a pass left running ended as %+v", p)
	}
	for _, turnID := range []string{second, third} {
		if p := waitPass(g, g.ordinary.ID, turnID); p.Status != memory.StatusDone || len(p.Facts) != 2 {
			t.Errorf("turn %s's pass ended as %+v", turnID, p)
		}
	}
	var reply MemoryReply
	sv.call(g.ordinary.ID, "GET", "chats/"+id+"/memory", "", http.StatusOK, &reply)
	if got := fmt.Sprint(len(reply.Passes)); got != "3" || reply.Passes[0].TurnID != first || reply.Passes[2].TurnID != third {
		t.Errorf("the chat's passes are %+v", reply.Passes)
	}
}
