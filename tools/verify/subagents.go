package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/subagents"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/settings"
)

// The standing check "sub-agents" (P8-02). The plan's words: sub-agents run
// in parallel with their own context, each shown as a lane in the chat;
// each sub-agent has its own budget; a per-tenant switch pauses all agents.
// The item's proof: three sub-agents run in parallel lanes and their
// results reach the parent turn.
//
// On the real server, put together as internal/app puts it, with the
// shipped settings, on a throwaway data folder. An invented person has a
// local model the fake provider answers for, which asks for sub-agents and
// answers as each of them, and a hosted one with a price.
//
//  1. Three sub-agents overlap in time: each one's model call waits until
//     all three are under way at once. The parent's next call carries their
//     three results, each worked in a chat of its own with none of the
//     parent's history, under a request id of its own registered as a
//     sub-agent, and every call is in the audit log.
//  2. A sub-agent on the hosted model past its budget stops, and the parent
//     is told why; the hosted model is never called.
//  3. The pause switch stops three running sub-agents within pauseWithin,
//     through their model calls' context, each ends as paused, and the
//     parent turn goes on.
//
// The guard internal/subagents/carry, which decides what passes between a
// chat and its sub-agents, is a guard of fixed rules 2 and 3 (fixedrules.go
// reads the lines below). The sub-agent routes are walked with every other
// route by the tenant isolation, calls recorded and no keys in output checks
// (routes.go).

func init() {
	componentDocs["internal/subagents"] = "docs/architecture/sub-agents.md"
	componentDocs["internal/subagents/carry"] = "docs/architecture/sub-agents.md"
	componentDocs["internal/turn/subagents.go"] = "docs/architecture/sub-agents.md"
	networkAllowed["internal/subagents"] = "the sub-agent routes, served by the server; it opens no connection"
	fixedRuleGuards["rules.approval"] = append(fixedRuleGuards["rules.approval"], "internal/subagents/carry")
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/subagents/carry")
}

// The person's words that start each step's sub-agents.
const (
	subagentsCount  = "Count the boats, the gulls and the buoys at once."
	subagentsPaid   = "Ask a helper on the paid model."
	subagentsResult = "All of them are back."
)

var subagentsTasks = []string{"Count the boats in the harbour.", "Count the gulls on the pier.", "Count the buoys in the bay."}

// laneModel is the fake model of the check. The parent's call asks for
// sub-agents, or, after their results, answers; a sub-agent's call (its
// message holds the brief) waits until want calls are under way at once,
// or blocks until it is cancelled while block is on.
type laneModel struct {
	brief string
	want  int
	block atomic.Bool

	mu        sync.Mutex
	in, most  int
	cancelled int
	parent    []seam.Request
	agents    []seam.Request
}

func (p *laneModel) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	var person strings.Builder
	for _, m := range req.Messages {
		if m.Role == seam.RoleUser {
			person.WriteString(seam.Text(m) + "\n")
		}
	}
	reply := func(blocks []seam.Block, stop seam.StopReason) error {
		events, err := seam.Events(seam.Response{Model: req.Model, Message: seam.Message{Role: seam.RoleAssistant, Blocks: blocks},
			StopReason: stop, Usage: seam.Usage{InputTokens: 1000, OutputTokens: 200}})
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
	if strings.Contains(person.String(), p.brief) {
		p.mu.Lock()
		p.agents = append(p.agents, req)
		p.in++
		p.most = max(p.most, p.in)
		p.mu.Unlock()
		defer func() {
			p.mu.Lock()
			p.in--
			p.mu.Unlock()
		}()
		if p.block.Load() {
			if err := emit(seam.Event{Type: seam.EventStart, Model: req.Model}); err != nil {
				return err
			}
			<-ctx.Done()
			p.mu.Lock()
			p.cancelled++
			p.mu.Unlock()
			return ctx.Err()
		}
		deadline := time.Now().Add(4 * time.Second)
		for {
			p.mu.Lock()
			ok := p.most >= p.want
			p.mu.Unlock()
			if ok || time.Now().After(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
			}
		}
		_, task, _ := strings.Cut(person.String(), "Your task:\n")
		task, _, _ = strings.Cut(task, "\n")
		return reply([]seam.Block{{Type: seam.BlockText, Text: "Counted for “" + strings.TrimSpace(task) + "”: fourteen."}}, seam.StopEnd)
	}
	p.mu.Lock()
	p.parent = append(p.parent, req)
	p.mu.Unlock()
	last := req.Messages[len(req.Messages)-1]
	for _, b := range last.Blocks {
		if b.Type == seam.BlockToolResult {
			return reply([]seam.Block{{Type: seam.BlockText, Text: subagentsResult}}, seam.StopEnd)
		}
	}
	var agents []map[string]any
	switch said := seam.Text(last); {
	case strings.Contains(said, subagentsCount):
		for _, t := range subagentsTasks {
			agents = append(agents, map[string]any{"task": t, "title": strings.TrimSuffix(t, ".")})
		}
	case strings.Contains(said, subagentsPaid):
		agents = []map[string]any{{"task": "Name three harbour birds.", "title": "Harbour birds", "model": "paid-example"}}
	default:
		return reply([]seam.Block{{Type: seam.BlockText, Text: "Nothing to add."}}, seam.StopEnd)
	}
	input, _ := json.Marshal(map[string]any{"agents": agents})
	return reply([]seam.Block{{Type: seam.BlockToolCall, ID: "call-lanes", Name: subagents.ToolName, Input: input}}, seam.StopToolCall)
}

// carried is the tool result the parent's newest call that carries one was
// sent.
func (p *laneModel) carried() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.parent) - 1; i >= 0; i-- {
		var b strings.Builder
		found := false
		for _, bl := range p.parent[i].Messages[len(p.parent[i].Messages)-1].Blocks {
			if bl.Type == seam.BlockToolResult {
				found = true
				b.WriteString(seam.Text(seam.Message{Blocks: bl.Content}))
			}
		}
		if found {
			return b.String()
		}
	}
	return ""
}

func checkSubagents(string) error {
	problems, err := subagentProblems()
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

func subagentProblems() (problems []string, err error) {
	dir, err := os.MkdirTemp("", "keystone-subagents-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	sc, err := subagents.DefaultConfig()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	house := models.Model{
		Name: "house-example", Label: "House Example", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
		ProviderModel: "small-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal, Capabilities: []models.Capability{models.CapTools},
	}
	paid := models.Model{
		Name: "paid-example", Label: "Paid Example", Adapter: models.AdapterAnthropic, Endpoint: "https://models.example",
		ProviderModel: "example-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassHosted, Price: models.Price{Input: 2, Output: 10},
		Capabilities: []models.Capability{models.CapTools},
	}
	person, err := in.Create("Sub-agent Person One")
	if err != nil {
		return nil, err
	}
	so, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	lim, err := adapters.Limits(reg)
	if err != nil {
		return nil, err
	}
	s, err := in.Store(person.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if err := signin.SetPassword(s, walkPasswordOne, so); err != nil {
		return nil, err
	}
	for _, m := range []models.Model{house, paid} {
		if err := models.Add(ctx, s, lim, m); err != nil {
			return nil, err
		}
	}
	for key, value := range map[string]string{"chats.default_model": house.Name, unattended.KeySubagentBudget: "0.001"} {
		if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, key, value); err != nil {
			return nil, err
		}
	}

	model := &laneModel{brief: sc.Brief, want: 3}
	var paidCalls atomic.Int32
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	c.Adapters = adapters.Set{
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return model, nil },
		models.AdapterAnthropic: func(models.Model, string) (seam.Provider, error) {
			return tallied{newWaiting("From the paid model."), &paidCalls}, nil
		},
	}
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	token, rec := w.signIn(person.ID, walkPasswordOne)
	if token == "" {
		return nil, fmt.Errorf("the person cannot sign in: status %d", rec.Code)
	}
	newChat := func() (string, error) {
		rec := w.request("POST", api.Prefix+"chats", token, "", nil)
		var made turn.ChatReply
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil {
			return "", fmt.Errorf("a chat cannot be made: status %d", rec.Code)
		}
		return made.Chat.ID, nil
	}
	send := func(chatID, text string) string {
		body, _ := json.Marshal(turn.SendBody{Text: text})
		return w.request("POST", api.Prefix+"chats/"+chatID+"/turns", token, string(body), nil).Body.String()
	}
	lanes := func(chatID string) []subagents.Subagent {
		var got subagents.SubagentsReply
		json.Unmarshal(w.request("GET", api.Prefix+"chats/"+chatID+"/subagents", token, "", nil).Body.Bytes(), &got)
		return got.Subagents
	}
	requestOf := func(id string) string {
		var rid string
		s.DB().QueryRow("SELECT request_id FROM subagents WHERE id = ?", id).Scan(&rid)
		return rid
	}

	// 1. Three in parallel, and their results reach the parent turn.
	chatID, err := newChat()
	if err != nil {
		return nil, err
	}
	if stream := send(chatID, subagentsCount); !strings.Contains(stream, "event: "+turn.EventEnd+"\n") {
		return nil, fmt.Errorf("the turn that starts three sub-agents did not stream to its end")
	}
	model.mu.Lock()
	most := model.most
	model.mu.Unlock()
	if most != 3 {
		problems = append(problems, fmt.Sprintf("at most %d of three sub-agents' model calls were under way at once, and all three must overlap", most))
	}
	carried := model.carried()
	for _, t := range subagentsTasks {
		if !strings.Contains(carried, "Counted for “"+t+"”: fourteen.") {
			problems = append(problems, fmt.Sprintf("the parent turn's next call does not carry the result of the sub-agent asked to %q", t))
		}
	}
	got := lanes(chatID)
	if len(got) != 3 {
		problems = append(problems, fmt.Sprintf("the chat lists %d sub-agents, and must list three", len(got)))
	}
	seenChats, seenRequests := map[string]bool{}, map[string]bool{}
	for _, a := range got {
		rid := requestOf(a.ID)
		if a.Status != subagents.StatusDone || a.CallID != "call-lanes" || seenChats[a.WorkChatID] || seenRequests[rid] {
			problems = append(problems, fmt.Sprintf("sub-agent %q ended as %s, in chat %s under request %s, and each must finish in a chat and under a request id of its own", a.Title, a.Status, a.WorkChatID, rid))
		}
		seenChats[a.WorkChatID], seenRequests[rid] = true, true
		run, ok, err := unattended.ForRequest(ctx, s.DB(), rid)
		if err != nil || !ok || run.Kind != unattended.KindSubagent || run.BudgetMicros != a.BudgetMicros {
			problems = append(problems, fmt.Sprintf("sub-agent %q is not registered as a sub-agent with its budget", a.Title))
		}
		entries, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindModel, RequestID: rid})
		if err != nil {
			return nil, err
		}
		if len(entries) != 2 {
			problems = append(problems, fmt.Sprintf("sub-agent %q's model call has %d audit entries, and must have its two", a.Title, len(entries)))
		}
	}
	model.mu.Lock()
	for _, req := range model.agents {
		for _, m := range req.Messages {
			if strings.Contains(seam.Text(m), subagentsCount) {
				problems = append(problems, "a sub-agent's call carried the parent chat's history")
			}
		}
	}
	model.mu.Unlock()

	// 2. Past its budget, a sub-agent stops and says so.
	if chatID, err = newChat(); err != nil {
		return nil, err
	}
	send(chatID, subagentsPaid)
	if got = lanes(chatID); len(got) != 1 || got[0].Status != subagents.StatusFailed || got[0].Error == nil ||
		got[0].Error.Code != string(seam.ErrSpendCap) || !strings.Contains(got[0].Error.Message, "budget of $0.00") || paidCalls.Load() != 0 {
		problems = append(problems, fmt.Sprintf("a sub-agent past its budget ended as %+v after %d paid calls, and must stop and say why without calling the paid model", got, paidCalls.Load()))
	}
	if carried = model.carried(); !strings.Contains(carried, "did not finish") || !strings.Contains(carried, "budget of $0.00") {
		problems = append(problems, "the parent of a sub-agent past its budget was not told why it stopped")
	}

	// 3. The pause switch stops them within seconds.
	model.block.Store(true)
	if chatID, err = newChat(); err != nil {
		return nil, err
	}
	streamed := make(chan string, 1)
	go func() { streamed <- send(chatID, subagentsCount) }()
	deadline := time.Now().Add(tasksWaitUp)
	for {
		model.mu.Lock()
		under := model.in
		model.mu.Unlock()
		if under == 3 {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("waited too long for three sub-agents' model calls to be under way")
		}
		time.Sleep(20 * time.Millisecond)
	}
	began := time.Now()
	if rec := w.request("POST", api.Prefix+"agents/pause", token, "", nil); rec.Code != http.StatusOK {
		return nil, fmt.Errorf("the pause answered %d", rec.Code)
	}
	stream := <-streamed
	took := time.Since(began)
	model.mu.Lock()
	under, cancelled := model.in, model.cancelled
	model.mu.Unlock()
	if took > pauseWithin || under != 0 || cancelled != 3 || !strings.Contains(stream, "event: "+turn.EventEnd+"\n") {
		problems = append(problems, fmt.Sprintf("%v after the pause %d sub-agents' calls are under way, %d were cancelled, and the parent turn must go on within %v", took, under, cancelled, pauseWithin))
	}
	for _, a := range lanes(chatID) {
		if a.Status != subagents.StatusPaused {
			problems = append(problems, fmt.Sprintf("after the pause sub-agent %q is %s, and must be paused", a.Title, a.Status))
		}
	}
	if carried = model.carried(); !strings.Contains(carried, "3 paused") {
		problems = append(problems, "the parent turn was not told its sub-agents were paused")
	}
	p, err := unrecordedCalls(ctx, s, "the person with sub-agents")
	if err != nil {
		return nil, err
	}
	problems = append(problems, p...)
	w.request("POST", api.Prefix+"agents/resume", token, "", nil)
	return problems, nil
}
