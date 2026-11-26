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
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// The standing check "unattended spend" (P8-05). The plan's words: at the
// monthly cap, unattended work falls back to local models or stops, and
// says so; a per-tenant switch pauses all agents and schedules at once. The
// item's proof: at the cap, unattended work falls back or stops and says
// so; the pause switch stops every running job within seconds.
//
// On the real server, put together as internal/app puts it, with the
// shipped settings, on a throwaway data folder. Two invented people each
// have models the fake provider answers for: a hosted one with a price, and
// local ones, which can be made to wait until their call is cancelled.
//
//  1. At the cap, with a local model in the chain, a background task goes
//     on with it, says so in its chat and in its note, naming the model,
//     and the audit log holds the refused call and the local one.
//  2. With fallback off for unattended work, the same task stops, says so,
//     and the local model is not called.
//  3. A task given a budget of its own stops at it, and says so.
//  4. The person's pause stops every one of their running tasks within
//     pauseWithin, through the model call's context: each is paused, not
//     failed, each cancelled call is recorded, the other person's task goes
//     on, and when the switch goes off they finish.
//
// The switch's routes are walked with every other route by the tenant
// isolation, calls recorded and no keys in output checks (routes.go).

func init() {
	componentDocs["internal/unattended"] = "docs/architecture/unattended-spend.md"
	networkAllowed["internal/unattended"] = "the routes of the switch that pauses every agent, served by the server; it opens no connection"
}

// pauseWithin is the most the pause may take to stop every running task.
const pauseWithin = 5 * time.Second

// waiting is the fake provider for the check: it answers at once, unless
// hold is on for a model, when it writes the start of a reply and waits
// until its call is cancelled. It counts calls by model.
type waiting struct {
	reply string
	mu    sync.Mutex
	hold  map[string]bool
	calls map[string]int
	busy  map[string]int
	ended map[string]int
}

func newWaiting(reply string) *waiting {
	return &waiting{reply: reply, hold: map[string]bool{}, calls: map[string]int{}, busy: map[string]int{}, ended: map[string]int{}}
}

func (p *waiting) count(m map[string]int, model string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return m[model]
}

func (p *waiting) setHold(model string, on bool) {
	p.mu.Lock()
	p.hold[model] = on
	p.mu.Unlock()
}

func (p *waiting) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	p.mu.Lock()
	p.calls[req.Model]++
	hold := p.hold[req.Model]
	if hold {
		p.busy[req.Model]++
	}
	p.mu.Unlock()
	if !hold {
		events, err := seam.Events(seam.Response{
			Model: req.Model, Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: p.reply}}},
			StopReason: seam.StopEnd, Usage: seam.Usage{InputTokens: 1000, OutputTokens: 200},
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
	defer func() {
		p.mu.Lock()
		p.busy[req.Model]--
		p.ended[req.Model]++
		p.mu.Unlock()
	}()
	for _, e := range []seam.Event{
		{Type: seam.EventStart, Model: req.Model},
		{Type: seam.EventBlockStart, Index: 0, Block: &seam.Block{Type: seam.BlockText}},
		{Type: seam.EventBlockDelta, Index: 0, Text: "Counting the boats along the"},
	} {
		if err := emit(e); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func checkUnattended(string) error {
	problems, err := unattendedProblems()
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

func unattendedProblems() (problems []string, err error) {
	dir, err := os.MkdirTemp("", "keystone-unattended-")
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
	ctx := context.Background()
	paid := models.Model{
		Name: "paid-example", Label: "Paid Example", Adapter: models.AdapterAnthropic, Endpoint: "https://models.example",
		ProviderModel: "example-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassHosted, Price: models.Price{Input: 2, Output: 10},
	}
	house := models.Model{
		Name: "house-example", Label: "House Example", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
		ProviderModel: "small-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal,
	}
	other := house
	other.Name, other.Label = "house-other", "House Other"

	one, err := in.Create("Unattended Person One")
	if err != nil {
		return nil, err
	}
	two, err := in.Create("Unattended Person Two")
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
	setup := func(id, password string, ms ...models.Model) error {
		s, err := in.Store(id)
		if err != nil {
			return err
		}
		defer s.Close()
		if err := signin.SetPassword(s, password, so); err != nil {
			return err
		}
		for _, m := range ms {
			if err := models.Add(ctx, s, lim, m); err != nil {
				return err
			}
		}
		return nil
	}
	if err := setup(one.ID, walkPasswordOne, paid, house); err != nil {
		return nil, err
	}
	if err := setup(two.ID, walkPasswordTwo, other); err != nil {
		return nil, err
	}
	set := func(id string, by tenantsettings.Role, key, value string) error {
		s, err := in.Store(id)
		if err != nil {
			return err
		}
		defer s.Close()
		return tenantsettings.Set(ctx, s, reg, by, key, value)
	}
	for _, x := range []struct {
		id         string
		by         tenantsettings.Role
		key, value string
	}{
		{one.ID, tenantsettings.Person, "chats.default_model", paid.Name},
		{one.ID, tenantsettings.Person, "routing.fallback_models", house.Name},
		{one.ID, tenantsettings.Admin, "usage.monthly_cap_usd", "0"},
		{two.ID, tenantsettings.Person, "chats.default_model", other.Name},
	} {
		if err := set(x.id, x.by, x.key, x.value); err != nil {
			return nil, err
		}
	}

	var paidCalls atomic.Int32
	local := newWaiting("From the house.")
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	c.Adapters = adapters.Set{
		models.AdapterAnthropic: func(models.Model, string) (seam.Provider, error) {
			return tallied{newWaiting("From the paid model."), &paidCalls}, nil
		},
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return local, nil },
	}
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	tokenOne, rec := w.signIn(one.ID, walkPasswordOne)
	if tokenOne == "" {
		return nil, fmt.Errorf("the first person cannot sign in: status %d", rec.Code)
	}
	tokenTwo, rec := w.signIn(two.ID, walkPasswordTwo)
	if tokenTwo == "" {
		return nil, fmt.Errorf("the second person cannot sign in: status %d", rec.Code)
	}
	s, err := in.Store(one.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	newChat := func(token string) (string, error) {
		rec := w.request("POST", api.Prefix+"chats", token, "", nil)
		var made turn.ChatReply
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil {
			return "", fmt.Errorf("a chat cannot be made: status %d", rec.Code)
		}
		return made.Chat.ID, nil
	}
	start := func(token, chatID, body string) (jobs.Task, error) {
		rec := w.request("POST", api.Prefix+"chats/"+chatID+"/tasks", token, body, nil)
		var t jobs.Task
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &t) != nil {
			return jobs.Task{}, fmt.Errorf("a background task cannot be started: status %d: %s", rec.Code, rec.Body.String())
		}
		return t, nil
	}
	read := func(token string, t jobs.Task) jobs.Task {
		var got jobs.Task
		json.Unmarshal(w.request("GET", api.Prefix+"chats/"+t.ChatID+"/tasks/"+t.ID, token, "", nil).Body.Bytes(), &got)
		return got
	}
	until := func(what string, ok func() bool) error {
		deadline := time.Now().Add(tasksWaitUp)
		for !ok() {
			if time.Now().After(deadline) {
				return fmt.Errorf("waited too long for %s", what)
			}
			time.Sleep(20 * time.Millisecond)
		}
		return nil
	}
	ended := func(token string, t jobs.Task) (jobs.Task, error) {
		var got jobs.Task
		err := until("the background task to end", func() bool {
			got = read(token, t)
			return got.Status != "" && got.Status != jobs.StatusQueued && got.Status != jobs.StatusRunning && got.Status != jobs.StatusPaused && got.NoteMessageID != 0
		})
		return got, err
	}
	lastWords := func(token, chatID string) string {
		var got turn.ChatReply
		if json.Unmarshal(w.request("GET", api.Prefix+"chats/"+chatID, token, "", nil).Body.Bytes(), &got) != nil || len(got.Messages) == 0 {
			return ""
		}
		m := got.Messages[len(got.Messages)-1]
		return seam.Text(seam.Message{Role: m.Role, Blocks: m.Blocks})
	}
	requestOf := func(t jobs.Task) string {
		var id string
		s.DB().QueryRow("SELECT request_id FROM jobs WHERE id = ?", t.ID).Scan(&id)
		return id
	}
	parent, err := newChat(tokenOne)
	if err != nil {
		return nil, err
	}

	// 1. At the cap, the task goes on with the local model and says so.
	task, err := start(tokenOne, parent, `{"text":"Name three harbour birds.","title":"Harbour birds"}`)
	if err != nil {
		return nil, err
	}
	end, err := ended(tokenOne, task)
	if err != nil {
		return nil, err
	}
	if end.Status != jobs.StatusDone || end.Result != "From the house." || paidCalls.Load() != 0 {
		problems = append(problems, fmt.Sprintf("at the cap with a local model in the chain, the task ended as %s with %q after %d paid calls", end.Status, end.Result, paidCalls.Load()))
	}
	if said := lastWords(tokenOne, task.WorkChatID); !strings.Contains(said, "monthly cap") || !strings.Contains(said, house.Name+", a local model, wrote the reply") {
		problems = append(problems, fmt.Sprintf("at the cap, the task's chat was told %q, and must be told the cap stopped the call and which model wrote the reply", said))
	}
	if said := lastWords(tokenOne, parent); !strings.Contains(said, "written by "+house.Name) {
		problems = append(problems, fmt.Sprintf("at the cap, the chat the task was started from was told %q, and must be told which model wrote its work", said))
	}
	entries, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindModel, RequestID: requestOf(task)})
	if err != nil {
		return nil, err
	}
	var refused, answered bool
	for _, e := range entries {
		refused = refused || (e.Action == paid.Name && e.Outcome == audit.OutcomeRefused && strings.Contains(e.Received, string(seam.ErrSpendCap)))
		answered = answered || (e.Action == house.Name && e.Outcome == audit.OutcomeOK)
	}
	if !refused || !answered {
		problems = append(problems, fmt.Sprintf("at the cap, the task's audit log must hold the refused call to %s and the call to %s: %d entries", paid.Name, house.Name, len(entries)))
	}

	// 2. With fallback off, it stops and says so.
	if err := set(one.ID, tenantsettings.Person, unattended.KeyFallback, "false"); err != nil {
		return nil, err
	}
	before := local.count(local.calls, house.Name)
	if task, err = start(tokenOne, parent, `{"text":"Name three harbour birds.","title":"Harbour birds again"}`); err != nil {
		return nil, err
	}
	if end, err = ended(tokenOne, task); err != nil {
		return nil, err
	}
	if end.Status != jobs.StatusFailed || end.Error == nil || end.Error.Code != string(seam.ErrSpendCap) || !strings.Contains(end.Error.Message, "switched off") {
		problems = append(problems, fmt.Sprintf("at the cap with fallback off, the task ended as %s (%+v), and must stop with %s and say why", end.Status, end.Error, seam.ErrSpendCap))
	}
	if n := local.count(local.calls, house.Name); n != before {
		problems = append(problems, fmt.Sprintf("at the cap with fallback off, the local model was called %d times", n-before))
	}
	if said := lastWords(tokenOne, parent); !strings.Contains(said, "did not finish") || !strings.Contains(said, "switched off") {
		problems = append(problems, fmt.Sprintf("at the cap with fallback off, the chat was told %q", said))
	}

	// 3. A task given a budget of its own stops at it.
	for _, x := range []struct{ key, value string }{{"usage.monthly_cap_usd", "100"}, {"routing.fallback_models", ""}} {
		by := tenantsettings.Person
		if x.key == "usage.monthly_cap_usd" {
			by = tenantsettings.Admin
		}
		if err := set(one.ID, by, x.key, x.value); err != nil {
			return nil, err
		}
	}
	if task, err = start(tokenOne, parent, `{"text":"Name three harbour birds.","title":"A small budget","budget_usd":0.001}`); err != nil {
		return nil, err
	}
	if end, err = ended(tokenOne, task); err != nil {
		return nil, err
	}
	if end.Status != jobs.StatusFailed || end.Error == nil || end.Error.Code != string(seam.ErrSpendCap) ||
		!strings.Contains(end.Error.Message, "budget of $0.00") || paidCalls.Load() != 0 {
		problems = append(problems, fmt.Sprintf("a task past its own budget ended as %s (%+v) after %d paid calls, and must stop and say so", end.Status, end.Error, paidCalls.Load()))
	}

	// 4. The pause stops every running task of the person's within seconds.
	if err := set(one.ID, tenantsettings.Person, "chats.default_model", house.Name); err != nil {
		return nil, err
	}
	local.setHold(house.Name, true)
	local.setHold(other.Name, true)
	boats, err := start(tokenOne, parent, `{"text":"Count the boats in the harbour.","title":"Boats"}`)
	if err != nil {
		return nil, err
	}
	gulls, err := start(tokenOne, parent, `{"text":"Count the gulls on the pier.","title":"Gulls"}`)
	if err != nil {
		return nil, err
	}
	theirChat, err := newChat(tokenTwo)
	if err != nil {
		return nil, err
	}
	buoys, err := start(tokenTwo, theirChat, `{"text":"Count the buoys in the bay.","title":"Buoys"}`)
	if err != nil {
		return nil, err
	}
	if err := until("every task's model call to be under way", func() bool {
		return local.count(local.busy, house.Name) == 2 && local.count(local.busy, other.Name) == 1
	}); err != nil {
		return nil, err
	}
	began := time.Now()
	rec = w.request("POST", api.Prefix+"agents/pause", tokenOne, "", nil)
	took := time.Since(began)
	var paused unattended.SwitchReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &paused) != nil {
		return nil, fmt.Errorf("the pause answered %d", rec.Code)
	}
	if !paused.Paused || paused.Working != 0 || paused.Held != 2 || took > pauseWithin {
		problems = append(problems, fmt.Sprintf("the pause answered %+v after %v, and must stop both running tasks within %v", paused, took, pauseWithin))
	}
	if busy, gone := local.count(local.busy, house.Name), local.count(local.ended, house.Name); busy != 0 || gone != 2 {
		problems = append(problems, fmt.Sprintf("after the pause %d of the person's model calls are under way and %d ended, and both must have been cancelled", busy, gone))
	}
	if busy := local.count(local.busy, other.Name); busy != 1 {
		problems = append(problems, "one person's pause reached another person's task")
	}
	for _, t := range []jobs.Task{boats, gulls} {
		if got := read(tokenOne, t); got.Status != jobs.StatusPaused || got.Error != nil {
			problems = append(problems, fmt.Sprintf("after the pause the task %q is %s, and must be paused", t.Title, got.Status))
		}
		recs, err := usage.List(ctx, s, usage.Filter{RequestID: requestOf(t)})
		if err != nil {
			return nil, err
		}
		if len(recs) != 1 || recs[0].Outcome != usage.OutcomeStopped {
			problems = append(problems, fmt.Sprintf("the cancelled call of %q left %d usage records, and must leave one that says it was stopped", t.Title, len(recs)))
		}
	}
	if rec := w.request("GET", api.Prefix+"agents", tokenTwo, "", nil); !strings.Contains(rec.Body.String(), `"paused":false`) {
		problems = append(problems, "one person's pause turned another person's switch on")
	}
	p, err := unrecordedCalls(ctx, s, "the paused person")
	if err != nil {
		return nil, err
	}
	problems = append(problems, p...)

	// Off again, the paused tasks go on and finish.
	local.setHold(house.Name, false)
	if rec := w.request("POST", api.Prefix+"agents/resume", tokenOne, "", nil); rec.Code != http.StatusOK {
		return nil, fmt.Errorf("the resume answered %d", rec.Code)
	}
	for _, t := range []jobs.Task{boats, gulls} {
		end, err := ended(tokenOne, t)
		if err != nil {
			return nil, err
		}
		if end.Status != jobs.StatusDone || end.Rounds != 2 {
			problems = append(problems, fmt.Sprintf("after the resume the task %q ended as %s after %d rounds, and must finish in a second round", t.Title, end.Status, end.Rounds))
		}
	}
	w.request("POST", api.Prefix+"chats/"+theirChat+"/tasks/"+buoys.ID+"/stop", tokenTwo, "", nil)
	return problems, nil
}
