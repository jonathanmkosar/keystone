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
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// The "memory pass" check (P6-06): after a turn, the durable facts of the
// exchange land in the person's knowledge store as one source linked back
// to the chat, and the memory pass's spend stops at its own reserve.
//
// It runs the real server, with the shipped settings, on a throwaway data
// folder: the test tenant, with the knowledge of
// fixtures/test-tenant/knowledge/knowledge.json in its built-in store, and
// the messages and facts of fixtures/test-tenant/knowledge/memory.json.
// The test tenant's recorded turns answer each message and each memory
// pass. The check proves:
//
//  1. Each chat of the fixture produces its expected facts, in the memory
//     space of the built-in store, as one source whose origin is the chat
//     and the turn, through one model call recorded with the kind memory,
//     and the chat's memory route shows the same.
//  2. A message with nothing durable adds nothing.
//  3. An incognito chat queues no pass, and the chat made ordinary again
//     does.
//  4. A chat that was sent sensitive content has its facts kept nowhere
//     while no sensitive space is set, and no model reads its exchange; with
//     the sensitive space set, they land there, labelled sensitive.
//  5. On an invented tenant with a hosted model and a local one: with no
//     reserve the memory call to the hosted model is refused and the chat
//     is answered; with a reserve the hosted model reads the exchange, at a
//     cost; once that cost reaches the reserve the next memory call is
//     refused; and with the reserve and the monthly cap both reached, the
//     local model behind the hosted one reads it, at no cost.
//  6. On that tenant, a held chat's memory pass never reaches the hosted
//     model the memory rule names, and its facts land in the sensitive
//     space.

// The memory pass (P6-06) decides where the facts of a chat that was sent
// sensitive content may land, and refuses them from a model that may not
// read such content: memory.Target and memory.Add take the plain answers.
// The package enforces rule 3 beside the registry and routing.
func init() {
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/knowledge/memory")
}

// memoryScriptPhrase is what the test tenant's recorded memory turns look
// for in the system text. The shipped prompt must hold it.
const memoryScriptPhrase = "long-term memory of a personal assistant"

// memoryFixture is fixtures/test-tenant/knowledge/memory.json.
type memoryFixture struct {
	TestData bool   `json:"test_data"`
	About    string `json:"about"`
	Chats    []struct {
		Ask   string   `json:"ask"`
		Facts []string `json:"facts"`
	} `json:"chats"`
	Nothing []string `json:"nothing"`
	Held    struct {
		Ask   string   `json:"ask"`
		Space string   `json:"space"`
		Facts []string `json:"facts"`
	} `json:"held"`
}

func readMemoryFixture(root string) (memoryFixture, error) {
	var fx memoryFixture
	path := filepath.Join(root, "fixtures", "test-tenant", "knowledge", "memory.json")
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
	case len(fx.Chats) < 2 || len(fx.Nothing) == 0:
		return fx, fmt.Errorf("%s needs two chats with facts and a message with none", path)
	case fx.Held.Ask == "" || fx.Held.Space == "" || len(fx.Held.Facts) == 0:
		return fx, fmt.Errorf("%s needs a held chat: its question, its sensitive space and its facts", path)
	}
	for _, c := range fx.Chats {
		if c.Ask == "" || len(c.Facts) == 0 {
			return fx, fmt.Errorf("%s: every chat has a question and its facts", path)
		}
	}
	return fx, nil
}

// The invented tenant's models. The test tenant's recorded turns answer
// for both, so nothing leaves this machine.
var (
	memoryPaid = models.Model{
		Name: "paid-memory", Label: "Paid Memory", Adapter: models.AdapterAnthropic, Endpoint: "https://models.example/v1",
		ProviderModel: "example-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassHosted,
		Price: models.Price{Input: 2, Output: 10},
	}
	memoryHouse = models.Model{
		Name: "house-memory", Label: "House Memory", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
		ProviderModel: "small-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
	}
)

// memoryCallCost is what one recorded memory turn costs on memoryPaid: 400
// tokens in at 2 and 40 out at 10, in millionths of a dollar.
const memoryCallCost = 1200

// memoryPassword is invented, and guards a folder that is removed.
const memoryPassword = "an invented phrase for the memory check"

// counted plays the test tenant's recorded turns for registry models, and
// keeps every request each model is sent.
type counted struct {
	mu   sync.Mutex
	sent map[string][]string
	p    seam.Provider
}

func (c *counted) builder(m models.Model, _ string) (seam.Provider, error) {
	return countedFor{c, m.Name}, nil
}

type countedFor struct {
	c    *counted
	name string
}

func (f countedFor) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	raw, err := seam.EncodeRequest(req)
	if err != nil {
		return err
	}
	f.c.mu.Lock()
	f.c.sent[f.name] = append(f.c.sent[f.name], string(raw))
	f.c.mu.Unlock()
	return f.c.p.Stream(ctx, req, emit)
}

// memoryCalls returns the memory calls a model was sent.
func (c *counted) memoryCalls(name string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, r := range c.sent[name] {
		if strings.Contains(r, memoryScriptPhrase) {
			out = append(out, r)
		}
	}
	return out
}

func checkMemory(root string) error {
	mf, err := readMemoryFixture(root)
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
	g, err := openKnowledgeRig(root, "keystone-memory-", adapters.Set{memoryPaid.Adapter: c.builder, memoryHouse.Adapter: c.builder})
	if err != nil {
		return err
	}
	defer g.done()
	problems, err := memoryProblems(g, mf, c)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// waitPass waits for a turn's memory pass to end, and returns it.
func (g *knowledgeRig) waitPass(tenantID, turnID string) (memory.Pass, error) {
	deadline := time.Now().Add(20 * time.Second)
	for {
		var p memory.Pass
		var ok bool
		err := g.with(tenantID, func(s *tenant.Store) error {
			var err error
			p, ok, err = memory.Get(context.Background(), s, turnID)
			return err
		})
		if err != nil {
			return p, err
		}
		if ok && p.Status != memory.StatusQueued && p.Status != memory.StatusRunning {
			return p, nil
		}
		if time.Now().After(deadline) {
			return p, fmt.Errorf("turn %s's memory pass did not end in time: %+v (queued: %v)", turnID, p, ok)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// memoryRecords returns a request's usage records of the kind memory.
func (g *knowledgeRig) memoryRecords(tenantID, requestID string) ([]usage.Record, error) {
	var out []usage.Record
	err := g.with(tenantID, func(s *tenant.Store) error {
		all, err := usage.List(context.Background(), s, usage.Filter{RequestID: requestID})
		for _, r := range all {
			if r.Kind == memory.Kind {
				out = append(out, r)
			}
		}
		return err
	})
	return out, err
}

// builtinSource reads a source back from a tenant's built-in store.
func (g *knowledgeRig) builtinSource(tenantID, id string) (knowledge.Source, error) {
	var src knowledge.Source
	err := g.with(tenantID, func(s *tenant.Store) error {
		ks, err := knowledge.Open(context.Background(), builtin.Name, s)
		if err != nil {
			return err
		}
		defer ks.Close()
		src, err = ks.ReadSource(context.Background(), id)
		return err
	})
	return src, err
}

// held says whether a chat was sent sensitive content.
func (g *knowledgeRig) held(tenantID, chatID string) (bool, error) {
	var h bool
	err := g.with(tenantID, func(s *tenant.Store) error {
		var err error
		h, err = recall.Held(context.Background(), s, chatID)
		return err
	})
	return h, err
}

func sameFacts(got, want []string) bool {
	return strings.Join(got, "\n") == strings.Join(want, "\n")
}

func memoryProblems(g *knowledgeRig, mf memoryFixture, c *counted) ([]string, error) {
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if !strings.Contains(g.defaultText(memory.SettingPrompt), memoryScriptPhrase) {
		return nil, fmt.Errorf("the shipped %s does not hold %q, which the test tenant's recorded memory turns look for", memory.SettingPrompt, memoryScriptPhrase)
	}
	if mf.Held.Ask != g.fx.Sensitive.Ask {
		return nil, fmt.Errorf("the memory fixture's held question is not the knowledge fixture's question about the sensitive space, so it would recall nothing sensitive")
	}
	space := g.defaultText(memory.SettingSpace)
	token, rec := g.w.signIn(g.test.ID, g.testPassword)
	if token == "" {
		return nil, fmt.Errorf("the test tenant cannot sign in through the API: status %d", rec.Code)
	}
	if err := g.use(g.test.ID, builtin.Name); err != nil {
		return nil, err
	}
	memoryRoute := func(token, chatID string) (turn.MemoryReply, error) {
		var reply turn.MemoryReply
		rec := g.w.request("GET", api.Prefix+"chats/"+chatID+"/memory", token, "", nil)
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reply) != nil {
			return reply, fmt.Errorf("the chat's memory route answered %d", rec.Code)
		}
		return reply, nil
	}

	// 1. Each chat's facts land in the built-in store, linked to the chat.
	for _, ch := range mf.Chats {
		t, err := g.send(token, "", ch.Ask, "")
		if err != nil {
			return nil, err
		}
		p, err := g.waitPass(g.test.ID, t.turnID)
		if err != nil {
			return nil, err
		}
		if p.Status != memory.StatusDone || !sameFacts(p.Facts, ch.Facts) || p.Space != space || p.Sensitive || p.SourceID == "" {
			say("the test tenant's chat %q gave the pass %+v, want the facts %q in the space %q", ch.Ask, p, ch.Facts, space)
			continue
		}
		src, err := g.builtinSource(g.test.ID, p.SourceID)
		if err != nil {
			return nil, err
		}
		if chatID, turnID, ok := memory.ParseOrigin(src.Origin); !ok || chatID != t.chatID || turnID != t.turnID {
			say("the source of %q has the origin %q, which does not point back to its chat and turn", ch.Ask, src.Origin)
		}
		for _, f := range ch.Facts {
			if !strings.Contains(src.Text, f) {
				say("the source of %q does not hold the fact %q", ch.Ask, f)
			}
		}
		if src.Space != space || src.Label.Sensitive() {
			say("the source of %q is in the space %q, labelled %s", ch.Ask, src.Space, src.Label)
		}
		if records, err := g.memoryRecords(g.test.ID, t.requestID); err != nil {
			return nil, err
		} else if len(records) != 1 || records[0].Outcome != usage.OutcomeOK {
			say("the pass of %q left %d usage records of the kind memory, want one that is ok", ch.Ask, len(records))
		}
		err = g.with(g.test.ID, func(s *tenant.Store) error {
			entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: t.requestID, Kind: audit.KindModel})
			found := false
			for _, e := range entries {
				found = found || (strings.Contains(e.Sent, memoryScriptPhrase) && strings.Contains(e.Sent, ch.Ask))
			}
			if err == nil && !found {
				say("the audit log holds no memory call for %q that was sent the exchange", ch.Ask)
			}
			return err
		})
		if err != nil {
			return nil, err
		}
		reply, err := memoryRoute(token, t.chatID)
		if err != nil {
			return nil, err
		}
		if len(reply.Passes) != 1 || reply.Passes[0].SourceID != p.SourceID || !sameFacts(reply.Passes[0].Facts, ch.Facts) {
			say("the memory route of the chat %q answered %+v", ch.Ask, reply)
		}
	}

	// 2. A message with nothing durable adds nothing.
	for _, ask := range mf.Nothing {
		t, err := g.send(token, "", ask, "")
		if err != nil {
			return nil, err
		}
		p, err := g.waitPass(g.test.ID, t.turnID)
		if err != nil {
			return nil, err
		}
		if p.Status != memory.StatusDone || len(p.Facts) != 0 || p.SourceID != "" {
			say("%q has nothing durable in it and its pass ended as %+v", ask, p)
		}
	}

	// 3. An incognito chat queues no pass. Its turns are worked one at a
	// time, so the incognito turn's pass, had it been queued, would come
	// before the next one's.
	made := g.w.request("POST", api.Prefix+"chats", token, "", nil)
	var chatReply turn.ChatReply
	if made.Code != http.StatusCreated || json.Unmarshal(made.Body.Bytes(), &chatReply) != nil {
		return nil, fmt.Errorf("a chat cannot be made through the API: status %d", made.Code)
	}
	chatID := chatReply.Chat.ID
	if rec := g.w.request("PUT", api.Prefix+"chats/"+chatID+"/incognito", token, "", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"incognito":true`) {
		say("making a chat incognito answered %d: %s", rec.Code, rec.Body.String())
	}
	quiet, err := g.send(token, chatID, mf.Chats[0].Ask, "")
	if err != nil {
		return nil, err
	}
	if rec := g.w.request("DELETE", api.Prefix+"chats/"+chatID+"/incognito", token, "", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"incognito":false`) {
		say("making a chat ordinary again answered %d: %s", rec.Code, rec.Body.String())
	}
	loud, err := g.send(token, chatID, mf.Chats[1].Ask, "")
	if err != nil {
		return nil, err
	}
	if p, err := g.waitPass(g.test.ID, loud.turnID); err != nil {
		return nil, err
	} else if p.Status != memory.StatusDone || !sameFacts(p.Facts, mf.Chats[1].Facts) {
		say("the turn after the chat was made ordinary again gave the pass %+v", p)
	}
	reply, err := memoryRoute(token, chatID)
	if err != nil {
		return nil, err
	}
	if len(reply.Passes) != 1 || reply.Passes[0].TurnID != loud.turnID {
		say("an incognito turn queued a memory pass: the chat's passes are %+v", reply.Passes)
	}
	if records, err := g.memoryRecords(g.test.ID, quiet.requestID); err != nil {
		return nil, err
	} else if len(records) != 0 {
		say("an incognito turn made %d memory calls", len(records))
	}

	// 4. A held chat: nowhere while no sensitive space is set, and no model
	// reads it; then the sensitive space, labelled sensitive.
	t, err := g.send(token, "", mf.Held.Ask, "")
	if err != nil {
		return nil, err
	}
	if h, err := g.held(g.test.ID, t.chatID); err != nil {
		return nil, err
	} else if !h {
		say("%q did not hold its chat, so the held chat's pass proves nothing", mf.Held.Ask)
	}
	if p, err := g.waitPass(g.test.ID, t.turnID); err != nil {
		return nil, err
	} else if p.Status != memory.StatusSkipped || p.SourceID != "" || len(p.Facts) != 0 {
		say("with no sensitive space set, a held chat's pass ended as %+v", p)
	}
	if records, err := g.memoryRecords(g.test.ID, t.requestID); err != nil {
		return nil, err
	} else if len(records) != 0 {
		say("with no sensitive space set, a held chat's exchange was read by a model %d times", len(records))
	}
	if err := g.set(g.test.ID, memory.SettingSensitiveSpace, mf.Held.Space); err != nil {
		return nil, err
	}
	if t, err = g.send(token, "", mf.Held.Ask, ""); err != nil {
		return nil, err
	}
	p, err := g.waitPass(g.test.ID, t.turnID)
	if err != nil {
		return nil, err
	}
	if p.Status != memory.StatusDone || p.Space != mf.Held.Space || !p.Sensitive || !sameFacts(p.Facts, mf.Held.Facts) {
		say("with the sensitive space set, a held chat's pass ended as %+v", p)
	} else if src, err := g.builtinSource(g.test.ID, p.SourceID); err != nil {
		return nil, err
	} else if src.Space != mf.Held.Space || !src.Label.Sensitive() {
		say("a held chat's facts landed in the space %q, labelled %s", src.Space, src.Label)
	}

	more, err := memoryReserveProblems(g, mf, c)
	if err != nil {
		return nil, err
	}
	return append(problems, more...), nil
}

// memoryReserveProblems is steps 5 and 6, on an invented tenant with a
// hosted model and a local one.
func memoryReserveProblems(g *knowledgeRig, mf memoryFixture, c *counted) ([]string, error) {
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	ctx := context.Background()
	person, err := g.in.Create("Memory Check Person")
	if err != nil {
		return nil, err
	}
	err = g.with(person.ID, func(s *tenant.Store) error {
		o, err := signin.Defaults()
		if err != nil {
			return err
		}
		if err := signin.SetPassword(s, memoryPassword, o); err != nil {
			return err
		}
		lim, err := adapters.Limits(g.reg)
		if err != nil {
			return err
		}
		for _, m := range []models.Model{memoryPaid, memoryHouse} {
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
	if err := g.set(person.ID, turn.KeyTitleAuto, "false", memory.SettingReserve, "0"); err != nil {
		return nil, err
	}
	token, rec := g.w.signIn(person.ID, memoryPassword)
	if token == "" {
		return nil, fmt.Errorf("the invented tenant cannot sign in through the API: status %d", rec.Code)
	}
	pass := func(ask, model string) (recallSent, memory.Pass, error) {
		t, err := g.send(token, "", ask, model)
		if err != nil {
			return t, memory.Pass{}, err
		}
		if !strings.Contains(t.stream, `"status":"done"`) {
			say("a turn on %s with a memory reserve did not end as done: the chat must go on", model)
		}
		p, err := g.waitPass(person.ID, t.turnID)
		return t, p, err
	}
	spent := func() (int64, error) {
		var n int64
		err := g.with(person.ID, func(s *tenant.Store) error {
			var err error
			n, err = usage.SpentOn(ctx, s, usage.MonthOf(time.Now()), memory.Kind)
			return err
		})
		return n, err
	}

	// 5a. No reserve: the hosted model is not sent the memory call.
	t, p, err := pass(mf.Chats[0].Ask, memoryPaid.Name)
	if err != nil {
		return nil, err
	}
	if p.Status != memory.StatusSkipped || !strings.Contains(p.Reason, "reserve") {
		say("with no reserve, a memory pass on the hosted model ended as %+v", p)
	}
	if n := len(c.memoryCalls(memoryPaid.Name)); n != 0 {
		say("with no reserve, the hosted model was sent %d memory calls", n)
	}
	if records, err := g.memoryRecords(person.ID, t.requestID); err != nil {
		return nil, err
	} else if len(records) != 1 || records[0].Outcome != usage.OutcomeRefused || records[0].CostMicros != 0 {
		say("the refused memory call left the usage records %+v, want one that is refused and cost nothing", records)
	}

	// 5b. A reserve: the hosted model reads the exchange, at a cost.
	if err := g.set(person.ID, memory.SettingReserve, "1"); err != nil {
		return nil, err
	}
	if _, p, err = pass(mf.Chats[1].Ask, memoryPaid.Name); err != nil {
		return nil, err
	}
	if p.Status != memory.StatusDone || p.Model != memoryPaid.Name || !sameFacts(p.Facts, mf.Chats[1].Facts) {
		say("with a reserve, a memory pass on the hosted model ended as %+v", p)
	}
	if n, err := spent(); err != nil {
		return nil, err
	} else if n != memoryCallCost {
		say("the month's memory passes cost %d millionths of a dollar, want the one call's %d", n, memoryCallCost)
	}

	// 5c. The reserve reached: the next memory call is refused, the chat is not.
	if err := g.set(person.ID, memory.SettingReserve, fmt.Sprintf("%.6f", float64(memoryCallCost)/1e6)); err != nil {
		return nil, err
	}
	if _, p, err = pass(mf.Chats[0].Ask, memoryPaid.Name); err != nil {
		return nil, err
	}
	if p.Status != memory.StatusSkipped || !strings.Contains(p.Reason, "reserve") {
		say("with the reserve reached, a memory pass on the hosted model ended as %+v", p)
	}
	if n := len(c.memoryCalls(memoryPaid.Name)); n != 1 {
		say("the hosted model was sent %d memory calls, want only the one the reserve allowed", n)
	}

	// 5d. The reserve and the monthly cap reached, with the local model
	// behind the hosted one the memory rule names: the local model reads
	// it, at no cost.
	err = g.with(person.ID, func(s *tenant.Store) error {
		return tenantsettings.Set(ctx, s, g.reg, tenantsettings.Admin, turn.KeyMonthlyCap, "0")
	})
	if err != nil {
		return nil, err
	}
	if err := g.set(person.ID, turn.KeyMemoryModel, memoryPaid.Name); err != nil {
		return nil, err
	}
	if t, p, err = pass(mf.Chats[0].Ask, memoryHouse.Name); err != nil {
		return nil, err
	}
	if p.Status != memory.StatusDone || p.Model != memoryHouse.Name || !sameFacts(p.Facts, mf.Chats[0].Facts) {
		say("with the reserve and the cap reached, the memory pass did not fall to the local model: %+v", p)
	}
	if records, err := g.memoryRecords(person.ID, t.requestID); err != nil {
		return nil, err
	} else {
		for _, r := range records {
			if r.CostMicros != 0 || (r.Outcome == usage.OutcomeOK && r.Class != models.ClassLocal) {
				say("with the reserve and the cap reached, a memory call is recorded as %s on %s (%s) at a cost of %d", r.Outcome, r.Model, r.Class, r.CostMicros)
			}
		}
	}
	if n := len(c.memoryCalls(memoryPaid.Name)); n != 1 {
		say("with the reserve and the cap reached, the hosted model was sent memory calls: %d in all", n)
	}
	if n, err := spent(); err != nil {
		return nil, err
	} else if n != memoryCallCost {
		say("after the reserve was reached the month's memory passes cost %d, want still %d", n, memoryCallCost)
	}

	// 6. A held chat on this tenant: the hosted model the memory rule names
	// never reads it, and its facts land in the sensitive space.
	if err := g.set(person.ID, memory.SettingSensitiveSpace, mf.Held.Space); err != nil {
		return nil, err
	}
	c.mu.Lock()
	before := len(c.sent[memoryPaid.Name])
	c.mu.Unlock()
	if t, p, err = pass(mf.Held.Ask, memoryHouse.Name); err != nil {
		return nil, err
	}
	if h, err := g.held(person.ID, t.chatID); err != nil {
		return nil, err
	} else if !h {
		say("on the invented tenant %q did not hold its chat, so the check proves nothing", mf.Held.Ask)
	}
	if p.Status != memory.StatusDone || p.Model != memoryHouse.Name || p.Space != mf.Held.Space || !p.Sensitive || !sameFacts(p.Facts, mf.Held.Facts) {
		say("a held chat's memory pass on the invented tenant ended as %+v", p)
	}
	c.mu.Lock()
	for _, r := range c.sent[memoryPaid.Name][before:] {
		if strings.Contains(r, "insomnia") || marker(r, g.fx.Sensitive.Markers) != "" {
			say("the hosted model was sent a call from the held chat")
		}
	}
	c.mu.Unlock()
	return problems, nil
}
