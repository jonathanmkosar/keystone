package turn

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// What background tasks (P8-01, internal/jobs) use of the runner. A task
// works through ordinary turns of a chat of its own, so it is built on
// turns and not beside them: what is here lets code that has no client
// send a turn, follow it and stop it, write a note in a chat between its
// turns, and carry a chat's marks into another chat. Nothing here changes
// how a turn runs. docs/architecture/jobs.md is the whole of it.

// ErrClosing says the runner is closing, so nothing was done.
var ErrClosing = errors.New("the runner is closing")

// ErrNoTurn says there is no such turn in the chat it was asked for under.
var ErrNoTurn = errNoTurn

// StopTask is the stop reason of a note a background task writes in the
// chat it was started from: its result, or why it ended without one.
const StopTask = "task"

// A Gate is asked before each model call a turn sends to a model that is
// not local, after the monthly cap has let it through. turnID is the turn
// the call is for, and mostMicros the most the call could cost. A non-nil
// answer stops the call as the cap does, so a route falls back to its next
// model, and the turn fails with the error's kind when none is left.
type Gate func(ctx context.Context, s *tenant.Store, turnID string, m models.Model, mostMicros int64) *seam.Error

// gate is the monthly spend cap, then the caller's own gate (P8-01: a
// background task's budget), as a route's gate.
func (r *Runner) gate(s *tenant.Store, t row) func(context.Context, models.Model, seam.Request) *seam.Error {
	capped := r.capGate(s)
	return func(ctx context.Context, m models.Model, req seam.Request) *seam.Error {
		if e := capped(ctx, m, req); e != nil || r.o.Gate == nil || !usage.Counts(m.Class) {
			return e
		}
		return r.o.Gate(ctx, s, t.ID, m, mostCost(m, req, r.o.Config.Budget))
	}
}

// Carry is what a chat's record says must go with words taken from it into
// another chat, so that no fixed rule is lost on the way.
type Carry struct {
	// Outside is why the words come from a chat that read outside content,
	// as a clause a card gives ("this task was started from …"). Empty
	// when it did not. Every tool that changes something asks in a chat
	// that is marked (fixed rule 2).
	Outside string
	// Sensitive says the words come from a chat that was sent content from
	// a sensitive space. A chat that is marked is held to models that may
	// read it (fixed rule 3).
	Sensitive bool
}

// Carried reads a chat's marks: whether it has read outside content, and
// why, and whether it is held for sensitive content. A mark that cannot be
// read counts as set.
func (r *Runner) Carried(ctx context.Context, s *tenant.Store, chatID string) Carry {
	var c Carry
	if outside, why := r.readOutside(ctx, s, row{Turn: Turn{ChatID: chatID}}); outside {
		c.Outside = why
	}
	held, err := recall.Held(ctx, s, chatID)
	if err != nil {
		r.logf("turns: tenant %s: reading whether chat %s was sent sensitive content, so it is taken that it was: %v", s.TenantID(), chatID, err)
	}
	c.Sensitive = held || err != nil
	return c
}

// mark writes a carry's marks on a chat, under one of its turns.
func mark(ctx context.Context, s *tenant.Store, chatID, turnID, tool string, c Carry) error {
	if c.Outside != "" {
		if _, err := s.DB().ExecContext(ctx, "INSERT INTO tool_outside(turn_id, chat_id, tool, why, at) VALUES(?, ?, ?, ?, ?)",
			turnID, chatID, tool, c.Outside, stamp()); err != nil {
			return err
		}
	}
	if c.Sensitive {
		return recall.Hold(ctx, s, turnID, chatID)
	}
	return nil
}

// holding runs f with a chat's queue held: no turn of the chat runs while f
// does, and a turn sent meanwhile waits until f returns. It first waits for
// a turn that is running to end, and for the turns queued behind it.
func (r *Runner) holding(ctx context.Context, tenantID, chatID string, f func() error) error {
	key := tenantID + "/" + chatID
	for {
		r.mu.Lock()
		if r.closing {
			r.mu.Unlock()
			return ErrClosing
		}
		if !r.working[key] {
			r.working[key] = true
			r.wg.Add(1)
			r.mu.Unlock()
			break
		}
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.base.Done():
			return ErrClosing
		case <-time.After(r.o.Config.StreamPoll):
		}
	}
	defer func() {
		r.mu.Lock()
		delete(r.working, key)
		r.mu.Unlock()
		r.wg.Done()
		r.kick(tenantID, chatID) // a turn sent while it was held
	}()
	return f()
}

// Send writes a turn as POST chats/{id}/turns does, for code that has no
// client: the turn waits in the chat's queue and runs on its own, and its
// model calls and tool calls are recorded under requestID and actor. A
// carry's marks are written on the chat before the turn can run.
func (r *Runner) Send(ctx context.Context, s *tenant.Store, chatID, requestID, actor string, body SendBody, carry Carry) (Turn, error) {
	if r.o.Install == nil {
		return Turn{}, errors.New("the runner has no data folder")
	}
	if r.isClosing() {
		return Turn{}, ErrClosing
	}
	if err := checkSend(s, chatID, &body); err != nil {
		return Turn{}, err
	}
	send := func() (row, error) {
		t, err := enqueue(ctx, s, chatID, requestID, actor, body, r.o.Config.QueueMax)
		if err != nil {
			return row{}, err
		}
		if err := mark(ctx, s, chatID, t.ID, "start_background_task", carry); err != nil {
			if _, ferr := finishQueued(s, t.ID); ferr != nil {
				r.logf("turns: %s closing turn %s, whose chat could not be marked: %v", requestID, t.ID, ferr)
			}
			return row{}, err
		}
		return t, nil
	}
	if carry == (Carry{}) {
		t, err := send()
		if err != nil {
			return Turn{}, err
		}
		r.kick(s.TenantID(), chatID)
		return t.Turn, nil
	}
	var t row
	err := r.holding(ctx, s.TenantID(), chatID, func() (err error) {
		t, err = send()
		return err
	})
	return t.Turn, err
}

// Get returns one turn of a chat.
func Get(ctx context.Context, s *tenant.Store, chatID, turnID string) (Turn, error) {
	t, err := getTurn(ctx, s, chatID, turnID)
	return t.Turn, err
}

// Latest returns the newest turn of a chat that was sent under requestID,
// and how many such turns the chat has. ok is false when it has none.
func Latest(ctx context.Context, s *tenant.Store, chatID, requestID string) (t Turn, count int, ok bool, err error) {
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM turns WHERE chat_id = ? AND request_id = ?", chatID, requestID).Scan(&count); err != nil {
		return Turn{}, 0, false, err
	}
	if count == 0 {
		return Turn{}, 0, false, nil
	}
	got, err := scanTurn(s.DB().QueryRowContext(ctx, "SELECT "+turnColumns+
		" FROM turns WHERE chat_id = ? AND request_id = ? ORDER BY seq DESC LIMIT 1", chatID, requestID))
	if err != nil {
		return Turn{}, 0, false, err
	}
	return got.Turn, count, true, nil
}

// Calls returns how many model calls the turns of a chat sent under
// requestID have finished: the assistant messages they wrote with a model.
func Calls(ctx context.Context, s *tenant.Store, chatID, requestID string) (int, error) {
	var n int
	err := s.DB().QueryRowContext(ctx, `SELECT count(*) FROM turn_messages tm JOIN turns t ON t.id = tm.turn_id
		WHERE t.chat_id = ? AND t.request_id = ? AND tm.model <> ''`, chatID, requestID).Scan(&n)
	return n, err
}

// Pending returns the tool a call of the turn waits for the person's
// approval to run, or "" when none waits.
func Pending(ctx context.Context, s *tenant.Store, turnID string) (string, error) {
	var tool string
	err := s.DB().QueryRowContext(ctx, "SELECT tool FROM tool_approvals WHERE turn_id = ? AND status = ? ORDER BY asked_at LIMIT 1", turnID, ApprovalPending).Scan(&tool)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return tool, err
}

// ReplyText returns the text of the last assistant message a turn wrote,
// or "" when it wrote none.
func ReplyText(s *tenant.Store, t Turn) (string, error) {
	if t.ReplyMessageID == 0 {
		return "", nil
	}
	m, err := chat.GetMessage(s, t.ChatID, t.ReplyMessageID)
	if err != nil {
		return "", err
	}
	return seam.Text(seam.Message{Role: m.Role, Blocks: m.Blocks}), nil
}

// Stop stops a turn as the stop route does, and returns it as it then
// stands.
func (r *Runner) Stop(ctx context.Context, s *tenant.Store, chatID, turnID string) (Turn, error) {
	t, err := r.stopTurn(ctx, s, chatID, turnID)
	return t.Turn, err
}

// Changed returns a channel that is closed when the turn next has news: a
// new event, or its end. Ask for it before reading the turn, so news that
// comes in between is not missed.
func (r *Runner) Changed(tenantID, turnID string) <-chan struct{} {
	return r.waitFor(tenantID + "/" + turnID)
}

// Event is one event a turn streamed, as it is kept.
type Event struct {
	ID   int64
	Name string
	Data json.RawMessage
}

// Events returns up to limit of a turn's events after the one with the id
// after, in order.
func Events(ctx context.Context, s *tenant.Store, turnID string, after int64, limit int) ([]Event, error) {
	got, err := eventsAfter(ctx, s, turnID, after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Event, len(got))
	for i, e := range got {
		out[i] = Event{ID: e.seq, Name: e.name, Data: json.RawMessage(e.data)}
	}
	return out, nil
}

// Go runs f in a goroutine of its own, with a context that ends when the
// runner closes, and Close waits for it as it waits for the turns. It
// reports false, and runs nothing, once the runner is closing.
func (r *Runner) Go(f func(ctx context.Context)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return false
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		f(r.base)
	}()
	return true
}

// ChatModel returns the name of the model a chat's next turn would use
// when it names none, or "" when there is none or the person is the test
// tenant, whose turns name no model.
func (r *Runner) ChatModel(ctx context.Context, s *tenant.Store, chatID string) string {
	c, err := r.choice(ctx, s, chatID)
	if err != nil || c.Model == nil || c.Source == SourceTest {
		return ""
	}
	return c.Model.Name
}

// Note writes a message Keystone itself says in a chat, as a turn of the
// chat that calls no model: a row in turns with its events, and the
// message, with the stop reason StopTask, under the last message of the
// chat's current branch. The chat's queue is held while it is written, so
// it never lands in the middle of a turn, and a carry's marks are on the
// chat before another turn of it can run.
//
// turnID is chosen by the caller and kept before Note is called. A note
// already written under it is returned as it is, and one begun and not
// finished before a restart is written again, so a note is written once.
func (r *Runner) Note(ctx context.Context, tenantID, chatID, turnID, requestID, actor string, blocks []seam.Block, carry Carry) (Message, error) {
	if r.o.Install == nil {
		return Message{}, errors.New("the runner has no data folder")
	}
	if turnID == "" || len(blocks) == 0 {
		return Message{}, errors.New("a note needs its turn's id and something to say")
	}
	var out Message
	err := r.holding(ctx, tenantID, chatID, func() error {
		s, err := r.o.Install.Store(tenantID)
		if err != nil {
			return err
		}
		defer s.Close()
		if _, err := chat.Get(s, chatID); err != nil {
			return err
		}
		var written int64
		err = s.DB().QueryRowContext(ctx, `SELECT tm.message_id FROM turn_messages tm JOIN turns t ON t.id = tm.turn_id
			WHERE tm.turn_id = ? AND t.chat_id = ?`, turnID, chatID).Scan(&written)
		switch {
		case err == nil:
			if _, err := finish(s, turnID, StatusDone, nil); err != nil {
				return err
			}
			m, err := chat.GetMessage(s, chatID, written)
			if err != nil {
				return err
			}
			out = withMeta(m, meta{turnID: turnID, stopReason: StopTask})
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		// A note begun before a restart and not written: it starts again.
		if _, err := s.DB().ExecContext(ctx, "DELETE FROM turns WHERE id = ? AND chat_id = ?", turnID, chatID); err != nil {
			return err
		}
		var t row
		err = inTx(ctx, s, func(tx *sql.Tx) error {
			now := stamp()
			if _, err := tx.Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, stop_reason, created_at, started_at) VALUES(?, ?, 'running', ?, ?, '{}', ?, ?, ?)",
				turnID, chatID, requestID, actor, StopTask, now, now); err != nil {
				return err
			}
			var err error
			t, err = emitTurn(tx, turnID, EventTurn)
			return err
		})
		if err != nil {
			return err
		}
		failed := func(err error) error {
			if _, ferr := finish(s, turnID, StatusFailed, &TurnError{Code: CodeFailed, Message: "The note could not be written."}); ferr != nil {
				r.logf("turns: %s closing the note turn %s: %v", requestID, turnID, ferr)
			}
			return err
		}
		if err := mark(ctx, s, chatID, turnID, "background_task", carry); err != nil {
			return failed(err)
		}
		path, err := chat.Path(s, chatID)
		if err != nil {
			return failed(err)
		}
		var parent int64
		if len(path) > 0 {
			parent = path[len(path)-1].ID
		}
		msg, err := r.store(s, t, parent, seam.RoleAssistant, blocks, meta{stopReason: StopTask})
		if err != nil {
			return failed(err)
		}
		if err := emitOne(s, turnID, EventMessage, msg); err != nil {
			return failed(err)
		}
		if _, err := finish(s, turnID, StatusDone, nil); err != nil {
			return err
		}
		r.wake(tenantID + "/" + turnID)
		out = msg
		return nil
	})
	return out, err
}
