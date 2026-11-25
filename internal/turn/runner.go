package turn

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Runner serves the chat and turn routes and works the turns.
//
// It keeps nothing a restart would lose: a turn and its events are rows in
// the tenant's database. What it holds in memory is who is working which
// chat, how to cancel a running turn, and who is waiting to be told that a
// turn has a new event.
type Runner struct {
	o Options

	base   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	closing bool
	working map[string]bool               // tenant/chat: a goroutine is working this chat's queue
	live    map[string]context.CancelFunc // tenant/turn: the running turn's cancel
	waiting map[string]chan struct{}      // tenant/turn: closed when the turn has news

	compaction compactions // which chats are being summarised (P5-02, compact.go)

	memory memoryWorkers // which chats have a memory worker at work (P6-06, memory.go)

	once     sync.Once
	setupErr error
	reg      []settings.Setting
	set      adapters.Set
	test     seam.Provider
}

// NewRunner returns a runner. It opens nothing until a route is called or
// Recover is.
func NewRunner(o Options) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{
		o: o, base: ctx, cancel: cancel,
		working: map[string]bool{},
		live:    map[string]context.CancelFunc{},
		waiting: map[string]chan struct{}{},
	}
}

// setup reads what the runner needs once: the settings registry, the test
// tenant's provider and the adapters.
func (r *Runner) setup() error {
	r.once.Do(func() {
		if r.reg, r.setupErr = settings.Registry(); r.setupErr != nil {
			return
		}
		r.test, r.set = r.o.TestProvider, r.o.Adapters
		if r.test == nil {
			fx, err := testtenant.Shipped()
			if err != nil {
				r.setupErr = err
				return
			}
			opts, err := fake.DefaultOptions()
			if err != nil {
				r.setupErr = err
				return
			}
			if r.test, r.setupErr = testtenant.Provider(fx, opts); r.setupErr != nil {
				return
			}
		}
		if r.set == nil {
			p := r.test
			r.set = adapters.Set{models.AdapterFake: func(models.Model, string) (seam.Provider, error) { return p, nil }}
		}
	})
	return r.setupErr
}

func (r *Runner) logf(format string, args ...any) { r.o.Log.Printf(format, args...) }

// Close stops the runner's goroutines and waits for them. A turn that was
// queued or running is left as it is in the database, for Recover to
// finish when a runner next starts on this data folder.
func (r *Runner) Close() {
	r.mu.Lock()
	r.closing = true
	r.mu.Unlock()
	r.cancel()
	r.wg.Wait()
}

func (r *Runner) isClosing() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closing
}

// Recover finishes what a stopped server left behind, for every tenant. A
// turn that was running is closed as failed, with what it had streamed
// kept. A turn that was waiting is run. The server calls it once, when it
// starts.
func (r *Runner) Recover() {
	if r.o.Install == nil {
		return
	}
	for _, t := range r.o.Install.Tenants() {
		if err := r.recoverTenant(t.ID); err != nil {
			r.logf("turns: recovering tenant %s: %v", t.ID, err)
		}
	}
}

func (r *Runner) recoverTenant(tenantID string) error {
	s, err := r.o.Install.Store(tenantID)
	if err != nil {
		return err
	}
	defer s.Close()
	rows, err := s.DB().Query("SELECT id, chat_id, status FROM turns WHERE status IN ('queued', 'running') ORDER BY seq")
	if err != nil {
		return err
	}
	type open struct{ id, chat, status string }
	var all []open
	for rows.Next() {
		var o open
		if err := rows.Scan(&o.id, &o.chat, &o.status); err != nil {
			rows.Close()
			return err
		}
		all = append(all, o)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	queued := map[string]bool{}
	for _, o := range all {
		if o.status == StatusQueued {
			queued[o.chat] = true
			continue
		}
		t, err := getTurn(context.Background(), s, o.chat, o.id)
		if err != nil {
			return err
		}
		if r.requeuePaused(s, t) { // P7-02: a turn that waited for an approval waits again (approvals.go)
			queued[o.chat] = true
			continue
		}
		r.keepStreamed(s, t)
		if _, err := finish(s, t.ID, StatusFailed, &TurnError{
			Code: CodeInterrupted, Message: "Keystone stopped while this reply was being written. Send the message again to get a whole reply.", Retryable: true,
		}); err != nil {
			return err
		}
	}
	for chatID := range queued {
		r.kick(tenantID, chatID)
	}
	return nil
}

// keepStreamed writes the text an interrupted turn had streamed, and had
// not yet stored, as an assistant message. Thinking is not kept: without
// its signature no provider takes it back.
func (r *Runner) keepStreamed(s *tenant.Store, t row) {
	if t.UserMessageID == 0 {
		return
	}
	events, err := eventsAfter(context.Background(), s, t.ID, 0, int(t.LastEventID)+1)
	if err != nil {
		r.logf("turns: reading the events of turn %s: %v", t.ID, err)
		return
	}
	// The text of the last model call, by block, unless its message was stored.
	var order []int
	text := map[int]string{}
	for _, e := range events {
		switch e.name {
		case EventStart, EventMessage:
			order, text = nil, map[int]string{}
		case EventText:
			var d DeltaEvent
			if json.Unmarshal([]byte(e.data), &d) != nil {
				continue
			}
			if _, seen := text[d.Index]; !seen {
				order = append(order, d.Index)
			}
			text[d.Index] += d.Text
		}
	}
	var blocks []seam.Block
	for _, i := range order {
		if text[i] != "" {
			blocks = append(blocks, seam.Block{Type: seam.BlockText, Text: text[i]})
		}
	}
	if len(blocks) == 0 {
		return
	}
	tail := t.UserMessageID
	var last sql.NullInt64
	if err := s.DB().QueryRow("SELECT max(message_id) FROM turn_messages WHERE turn_id = ?", t.ID).Scan(&last); err == nil && last.Int64 > tail {
		tail = last.Int64
	}
	msg, err := r.store(s, t, tail, seam.RoleAssistant, blocks, meta{model: t.Model, stopReason: StatusFailed})
	if err != nil {
		r.logf("turns: keeping what turn %s had streamed: %v", t.ID, err)
		return
	}
	if err := emitOne(s, t.ID, EventMessage, msg); err != nil {
		r.logf("turns: keeping what turn %s had streamed: %v", t.ID, err)
	}
}

// kick starts a goroutine that works a chat's queue, unless one is at work.
func (r *Runner) kick(tenantID, chatID string) {
	key := tenantID + "/" + chatID
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing || r.working[key] {
		return
	}
	r.working[key] = true
	r.wg.Add(1)
	go r.drain(tenantID, chatID)
}

// drain works a chat's turns, oldest first, until none waits. It has a
// store of its own, because the store a call came with closes when the
// call ends and a turn goes on after that.
func (r *Runner) drain(tenantID, chatID string) {
	defer r.wg.Done()
	key := tenantID + "/" + chatID
	leave := func() {
		r.mu.Lock()
		delete(r.working, key)
		r.mu.Unlock()
	}
	s, err := r.o.Install.Store(tenantID)
	if err != nil {
		leave()
		r.logf("turns: opening the store of tenant %s: %v", tenantID, err)
		return
	}
	defer s.Close()
	for {
		// Looking and leaving happen under the lock kick takes, so a turn
		// that arrives as this goroutine leaves is never missed.
		r.mu.Lock()
		t, ok, err := nextQueued(r.base, s, chatID)
		if !ok || r.closing {
			delete(r.working, key)
			r.mu.Unlock()
			if err != nil && !errors.Is(err, context.Canceled) {
				r.logf("turns: reading the queue of a chat of tenant %s: %v", tenantID, err)
			}
			return
		}
		r.mu.Unlock()
		r.runTurn(s, t)
	}
}

func (r *Runner) waitFor(key string) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := r.waiting[key]
	if ch == nil {
		ch = make(chan struct{})
		r.waiting[key] = ch
	}
	return ch
}

func (r *Runner) wake(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch := r.waiting[key]; ch != nil {
		close(ch)
		delete(r.waiting, key)
	}
}

// runTurn takes one waiting turn to its end.
func (r *Runner) runTurn(s *tenant.Store, t row) {
	key := s.TenantID() + "/" + t.ID
	res, err := s.DB().Exec("UPDATE turns SET status = 'running', started_at = ? WHERE id = ? AND status = 'queued'", stamp(), t.ID)
	if err != nil {
		r.logf("turns: %s starting turn %s: %v", t.requestID, t.ID, err)
		// It cannot be left waiting, or the queue would turn on it for ever.
		if _, err := finish(s, t.ID, StatusFailed, &TurnError{Code: CodeFailed, Message: "The turn could not be started."}); err != nil {
			r.logf("turns: %s closing turn %s: %v", t.requestID, t.ID, err)
		}
		r.wake(key)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // stopped, or deleted with its chat, while it waited
	}
	ctx, cancel := context.WithCancel(audit.WithRequestID(r.base, t.requestID))
	defer cancel()
	r.mu.Lock()
	r.live[key] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.live, key)
		r.mu.Unlock()
		r.wake(key)
	}()
	// A stop that came between the two steps above found nothing to cancel.
	var asked bool
	if err := s.DB().QueryRow("SELECT stop_requested FROM turns WHERE id = ?", t.ID).Scan(&asked); err == nil && asked {
		cancel()
	}

	failure, stopped := r.work(ctx, s, t)
	if r.isClosing() {
		return // left as it is, for Recover
	}
	status := StatusDone
	switch {
	case stopped:
		status, failure = StatusStopped, nil
	case failure != nil:
		status = StatusFailed
	}
	if _, err := finish(s, t.ID, status, failure); err != nil && !errors.Is(err, errNoTurn) {
		r.logf("turns: %s closing turn %s: %v", t.requestID, t.ID, err)
	}
	r.memoryLater(s, t, status) // P6-06: the memory pass, queued as the turn ends and run beside it (memory.go)
}

// work is the turn itself: the model is opened, the person's message is
// written, the model is called until it stops asking for tools, and the
// chat is given a title. It returns why the turn failed, or that it was
// stopped, or neither when the reply is whole.
func (r *Runner) work(ctx context.Context, s *tenant.Store, t row) (failure *TurnError, stopped bool) {
	key := s.TenantID() + "/" + t.ID
	say := func(name string, data any) {
		if err := emitOne(s, t.ID, name, data); err != nil && !errors.Is(err, errNoTurn) {
			r.logf("turns: %s writing a %s event of turn %s: %v", t.requestID, name, t.ID, err)
		}
		r.wake(key)
	}
	broke := func(what string, err error) (*TurnError, bool) {
		r.logf("turns: %s turn %s: %s: %v", t.requestID, t.ID, what, err)
		return &TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."}, false
	}
	if ctx.Err() != nil {
		return nil, true
	}

	t.recalled = r.searchKnowledge(ctx, s, t) // before the route: what it finds decides the route (P6-05)
	m, provider, secrets, failure := r.open(ctx, s, t, t.input.Model)
	if failure != nil {
		return failure, false
	}
	t.Model = m.Name
	err := inTx(context.Background(), s, func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE turns SET model = ? WHERE id = ?", m.Name, t.ID); err != nil {
			return err
		}
		_, err := emitTurn(tx, t.ID, EventTurn)
		return err
	})
	if err != nil {
		return broke("naming its model", err)
	}
	r.wake(key)

	t.approvals = r.approvalState(s, t) // P7-02: a turn that paused for an approval goes on where it was
	tail, written, failure := r.place(s, t)
	if failure != nil {
		return failure, false
	}
	t.UserMessageID = tail
	if _, err := s.DB().Exec("UPDATE turns SET user_message_id = ? WHERE id = ?", tail, t.ID); err != nil {
		return broke("noting the person's message", err)
	}
	if written != nil {
		say(EventMessage, *written)
	}

	maxOutput, thinking := r.number(ctx, s, KeyMaxOutputTokens), r.number(ctx, s, KeyThinkingTokens)
	if !m.Has(models.CapThinking) {
		thinking = 0
	}
	reply := ""
	for step := 1; ; step++ {
		if t.approvals.resume != nil { // P7-02: the step the turn paused at is answered first (approvals.go)
			var again bool
			if step, tail, again, failure, stopped = r.resumeStep(ctx, s, &t, m, provider, say); failure != nil || stopped || r.isClosing() {
				return failure, stopped
			}
			if !again {
				break
			}
			continue
		}
		messages, err := r.branch(ctx, s, t.ChatID, tail, m.Name)
		if err != nil {
			return broke("reading the chat", err)
		}
		req, err := r.build(ctx, s, t, step, m, provider, messages, maxOutput, thinking)
		if err != nil {
			if ctx.Err() != nil {
				return nil, true // stopped while the provider counted
			}
			return broke("building the context", err)
		}
		out := r.call(ctx, s, t, step, m, provider, secrets, req, say)

		blocks, stopReason := out.partial.Blocks, StatusFailed
		var usage *seam.Usage
		switch {
		case out.err == nil:
			blocks, stopReason, usage = out.resp.Message.Blocks, string(out.resp.StopReason), &out.resp.Usage
		case out.stopped:
			stopReason = StatusStopped
		}
		if out.closing {
			return nil, false
		}
		if len(blocks) > 0 {
			msg, err := r.store(s, t, tail, seam.RoleAssistant, blocks, meta{model: out.model.Name, stopReason: stopReason, usage: usage, costMicros: out.cost})
			if err != nil {
				return broke("storing the reply", err)
			}
			tail = msg.ID
			say(EventMessage, msg)
		}
		if out.err != nil {
			return out.failure, out.stopped
		}
		say(EventUsage, UsageEvent{Step: step, Model: out.model.Name, StopReason: stopReason, Usage: out.resp.Usage, CostMicros: out.cost})
		t.Usage, t.CostMicros = t.Usage.Add(out.resp.Usage), t.CostMicros+out.cost
		total, _ := json.Marshal(t.Usage)
		if _, err := s.DB().Exec("UPDATE turns SET usage = ?, cost_micros = ?, stop_reason = ? WHERE id = ?", string(total), t.CostMicros, stopReason, t.ID); err != nil {
			return broke("noting its usage", err)
		}
		reply = seam.Text(out.resp.Message)

		calls := seam.ToolCalls(out.resp.Message)
		if out.resp.StopReason != seam.StopToolCall || len(calls) == 0 {
			break
		}
		// The tool loop (P7-01, tools.go): every call is answered and the
		// model is called again, until the turn's step limit.
		var again bool
		if tail, again, failure, stopped = r.toolStep(ctx, s, t, step, tail, calls, say); failure != nil || stopped || r.isClosing() {
			return failure, stopped
		}
		if !again {
			break
		}
	}

	if title, ok := r.title(ctx, s, t, m, provider, secrets, reply); ok {
		say(EventTitle, TitleEvent{ChatID: t.ChatID, Title: title})
	}
	return nil, false
}

// store appends a message the turn wrote to the chat record, with what the
// turn records about it, and notes an assistant message as the turn's reply.
func (r *Runner) store(s *tenant.Store, t row, parent int64, role seam.Role, blocks []seam.Block, x meta) (Message, error) {
	msg, err := chat.Append(s, t.ChatID, chat.NewMessage{ParentID: parent, Role: role, Blocks: blocks})
	if err != nil && errors.Is(err, chat.ErrInvalid) && role == seam.RoleAssistant {
		// What a stopped call leaves may hold a thinking block that never
		// got its signature. The words that were streamed are still kept.
		var kept []seam.Block
		for _, b := range blocks {
			if b.Type != seam.BlockThinking {
				kept = append(kept, b)
			}
		}
		if len(kept) == 0 || len(kept) == len(blocks) {
			return Message{}, err
		}
		msg, err = chat.Append(s, t.ChatID, chat.NewMessage{ParentID: parent, Role: role, Blocks: kept})
	}
	if err != nil {
		return Message{}, err
	}
	x.turnID = t.ID
	if err := noteMessage(s, msg.ID, x); err != nil {
		return Message{}, err
	}
	if role == seam.RoleAssistant {
		if _, err := s.DB().Exec("UPDATE turns SET reply_message_id = ? WHERE id = ?", msg.ID, t.ID); err != nil {
			return Message{}, err
		}
	}
	return withMeta(msg, x), nil
}

// place puts the person's message in the record and returns the id of the
// message the reply goes under. written is the message when one was
// written: a retry writes none.
func (r *Runner) place(s *tenant.Store, t row) (tail int64, written *Message, failure *TurnError) {
	gone := func(err error) (int64, *Message, *TurnError) {
		if errors.Is(err, chat.ErrNotFound) || errors.Is(err, chat.ErrInvalid) {
			return 0, nil, &TurnError{Code: string(seam.ErrInvalid), Message: "The message could not be added to the chat: " + err.Error()}
		}
		r.logf("turns: %s turn %s: writing the person's message: %v", t.requestID, t.ID, err)
		return 0, nil, &TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."}
	}
	if t.approvals != nil && t.approvals.resume != nil {
		return t.UserMessageID, nil, nil // P7-02: going on after a pause, the message is in the record
	}
	in := t.input
	var parent int64
	switch {
	case in.RetryOf != 0:
		old, err := chat.GetMessage(s, t.ChatID, in.RetryOf)
		if err != nil {
			return gone(err)
		}
		if old.Role != seam.RoleAssistant || old.ParentID == 0 {
			return gone(fmt.Errorf("%w: message %d is not a reply", chat.ErrInvalid, old.ID))
		}
		if err := chat.Select(s, t.ChatID, old.ParentID); err != nil {
			return gone(err)
		}
		return old.ParentID, nil, nil
	case in.EditOf != 0:
		old, err := chat.GetMessage(s, t.ChatID, in.EditOf)
		if err != nil {
			return gone(err)
		}
		if old.Role != seam.RoleUser {
			return gone(fmt.Errorf("%w: message %d is not the person's", chat.ErrInvalid, old.ID))
		}
		if parent = old.ParentID; parent != 0 {
			if err := chat.Select(s, t.ChatID, parent); err != nil {
				return gone(err)
			}
		}
	default:
		path, err := chat.Path(s, t.ChatID)
		if err != nil {
			return gone(err)
		}
		if len(path) > 0 {
			parent = path[len(path)-1].ID
		}
	}
	msg, err := r.store(s, t, parent, seam.RoleUser, in.content(), meta{})
	if err != nil {
		return gone(err)
	}
	return msg.ID, &msg, nil
}

// content is the body's message as blocks.
func (b SendBody) content() []seam.Block {
	if len(b.Blocks) > 0 {
		return b.Blocks
	}
	return []seam.Block{{Type: seam.BlockText, Text: b.Text}}
}

// branch returns the messages from the chat's first down to tail, as the
// request a model is sent.
func (r *Runner) branch(ctx context.Context, s *tenant.Store, chatID string, tail int64, model string) ([]seam.Message, error) {
	path, err := chat.Path(s, chatID)
	if err != nil {
		return nil, err
	}
	at := -1
	for i, m := range path {
		if m.ID == tail {
			at = i
		}
	}
	if at >= 0 {
		path = path[:at+1]
	} else {
		// The person stepped to another branch while the turn ran. The
		// turn's own branch is read upwards from its last message.
		path = nil
		for id := tail; id != 0; {
			m, err := chat.GetMessage(s, chatID, id)
			if err != nil {
				return nil, err
			}
			path = append([]chat.Message{m}, path...)
			id = m.ParentID
		}
	}
	metas, err := metaOf(ctx, s, chatID)
	if err != nil {
		return nil, err
	}
	return buildMessages(path, metas, model), nil
}

// buildMessages turns a branch of the record into the messages of a
// request that any provider takes. The record holds what happened, and
// that includes replies that were stopped or broke off, so:
//
//   - a thinking block goes back only to the model that wrote it, and only
//     with its signature;
//   - a tool call with no result in the next message is left out, and so is
//     a result whose call was;
//   - a message left with nothing is dropped, and two messages in a row
//     from the same side become one.
func buildMessages(path []chat.Message, metas map[int64]meta, model string) []seam.Message {
	var out []seam.Message
	open := map[string]bool{}
	for i, m := range path {
		answered := map[string]bool{}
		if i+1 < len(path) && path[i+1].Role == seam.RoleUser {
			for _, b := range path[i+1].Blocks {
				if b.Type == seam.BlockToolResult {
					answered[b.ID] = true
				}
			}
		}
		var blocks []seam.Block
		for _, b := range m.Blocks {
			switch b.Type {
			case seam.BlockThinking:
				if b.Signature == "" || metas[m.ID].model != model {
					continue
				}
			case seam.BlockToolCall:
				if !answered[b.ID] || open[b.ID] {
					continue
				}
				open[b.ID] = true
			case seam.BlockToolResult:
				if !open[b.ID] {
					continue
				}
				delete(open, b.ID)
			}
			blocks = append(blocks, b)
		}
		if len(blocks) == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			out[n-1].Blocks = append(append([]seam.Block(nil), out[n-1].Blocks...), blocks...)
			continue
		}
		out = append(out, seam.Message{Role: m.Role, Blocks: blocks})
	}
	return out
}

// called is how one model call went.
type called struct {
	resp    seam.Response // the whole reply, when err is nil
	partial seam.Message  // what had arrived, otherwise
	cost    int64
	err     error
	failure *TurnError // why it failed, for the person
	stopped bool       // the person stopped it
	closing bool       // the runner is closing: nothing was written

	// model is the model the call ended on, which a fallback may have
	// changed (P3-08).
	model models.Model
}

// call makes one model call and streams it. The audit log gets an entry
// before the call is made and an entry for its end, both under the request
// id of the API call that sent the turn. A call whose first entry cannot
// be written is not made.
func (r *Runner) call(ctx context.Context, s *tenant.Store, t row, step int, m models.Model, p seam.Provider, secrets []string, req seam.Request, say func(string, any)) called {
	bg := context.Background()
	fail := func(what string, err error) called {
		r.logf("turns: %s turn %s: %s: %s", t.requestID, t.ID, what, audit.Scrub(err.Error(), secrets...))
		return called{err: err, failure: &TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."}}
	}
	sent, err := seam.EncodeRequest(req)
	if err != nil {
		return fail("building the request", err)
	}
	entry := audit.Entry{Kind: audit.KindModel, RequestID: t.requestID, Actor: t.actor, Action: m.Name, Outcome: audit.OutcomeStarted, Sent: string(sent)}
	if _, err := audit.Append(bg, s, entry, secrets...); err != nil {
		return fail("recording the model call", err)
	}
	began := time.Now()
	say(EventStart, StartEvent{Step: step, Model: m.Name})

	var col seam.Collector
	kinds := map[int]seam.BlockType{}
	err = p.Stream(ctx, req, func(e seam.Event) error {
		if err := col.Add(e); err != nil {
			return err
		}
		delta := func(text string) {
			switch kinds[e.Index] {
			case seam.BlockText:
				say(EventText, DeltaEvent{Step: step, Index: e.Index, Text: text})
			case seam.BlockThinking:
				say(EventThinking, DeltaEvent{Step: step, Index: e.Index, Text: text})
			}
		}
		switch e.Type {
		case seam.EventBlockStart:
			kinds[e.Index] = e.Block.Type
			if e.Block.Text != "" {
				delta(e.Block.Text)
			}
		case seam.EventBlockDelta:
			if e.Text != "" {
				delta(e.Text)
			}
		case seam.EventBlockStop:
			if kinds[e.Index] == seam.BlockToolCall {
				if done := col.Partial().Blocks; len(done) > 0 && done[len(done)-1].Type == seam.BlockToolCall {
					b := done[len(done)-1]
					say(EventToolCall, ToolCallEvent{Step: step, Index: e.Index, ID: b.ID, Name: b.Name, Input: b.Input})
				}
			}
		}
		return nil
	})
	out := called{err: err, partial: col.Partial()}
	if err == nil {
		if out.resp, out.err = col.Response(); out.err != nil {
			out.err = &seam.Error{Kind: seam.ErrUnavailable, Message: "the stream ended before its stop event", Retryable: true}
		}
	}
	m = answered(p, m) // the route's model that answered, or failed last (P3-08)
	out.model, entry.Action = m, m.Name
	entry.Sent, entry.DurationMS = "", time.Since(began).Milliseconds()
	var modelErr *seam.Error
	switch {
	case out.err == nil:
		u := out.resp.Usage
		out.cost = m.Price.CostMicros(u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
		received, _ := json.Marshal(out.resp)
		entry.Outcome, entry.Received, entry.Usage, entry.CostMicros = audit.OutcomeOK, string(received), audit.Usage(u), out.cost
	case ctx.Err() != nil:
		if r.isClosing() {
			// The server is stopping. The call's started entry stands with
			// nothing after it, which is what the audit log says of a call
			// that never finished.
			out.closing = true
			return out
		}
		out.stopped = true
		received, _ := json.Marshal(map[string]any{"stopped": true, "partial": out.partial})
		entry.Outcome, entry.Received = audit.OutcomeOK, string(received)
	case errors.As(out.err, &modelErr):
		out.failure = &TurnError{Code: string(modelErr.Kind), Message: audit.Scrub(modelErr.Message, secrets...), Retryable: modelErr.Retryable}
		received, _ := json.Marshal(modelErr)
		entry.Outcome, entry.Received = audit.OutcomeError, string(received)
		if modelErr.Kind == seam.ErrTooLong || modelErr.Kind == seam.ErrSpendCap {
			entry.Outcome = audit.OutcomeRefused
		}
	default:
		out.failure = fail("calling the model", out.err).failure
		entry.Outcome, entry.Received = audit.OutcomeError, `{"kind":"other","message":"the call failed inside Keystone"}`
	}
	if _, err := audit.Append(bg, s, entry, secrets...); err != nil {
		// A call that cannot be recorded counts as a failed call.
		r.logf("turns: %s turn %s: recording the end of the model call: %v", t.requestID, t.ID, err)
		if out.err == nil {
			out.err = err
			out.partial = out.resp.Message
			out.failure = &TurnError{Code: CodeFailed, Message: "The model call could not be recorded, so its reply was not used."}
		}
	}
	r.used(s, t, step, m, entry, out.stopped) // the call's usage record (P3-08)
	return out
}

// number reads a whole-number setting the person may have set for
// themselves. A value that does not read counts as zero.
func (r *Runner) number(ctx context.Context, s *tenant.Store, key string) int {
	n := 0
	fmt.Sscanf(r.setting(ctx, s, key), "%d", &n)
	if n < 0 {
		return 0
	}
	return n
}

// setting reads the value in force for a tenant: its own, or the default.
func (r *Runner) setting(ctx context.Context, s *tenant.Store, key string) string {
	v, err := tenantsettings.Get(ctx, s, r.reg, key)
	if err != nil {
		r.logf("turns: reading the %s setting of tenant %s: %v", key, s.TenantID(), err)
		return ""
	}
	return strings.TrimSpace(v)
}
