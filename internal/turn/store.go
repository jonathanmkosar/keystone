package turn

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// errNoTurn says the turn is not in this tenant's record, or not in the
// chat it was asked for under.
var errNoTurn = errors.New("no such turn")

// Times are stored as the chat record stores them: text of one fixed
// width, in UTC, so they sort as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

func stamp() string { return time.Now().UTC().Format(timeLayout) }

// row is a turn as the runner needs it: what the API shows, and what it
// was sent with.
type row struct {
	Turn
	requestID string
	actor     string
	input     SendBody
	stopAsked bool
	recalled  *turnRecall // what recall found, once work has searched (P6-05)
	// approvals is the approval policy's state for the turn (P7-02).
	approvals *turnApprovals
	// connectors is what the current call was given from the person's
	// connectors (P7-04, connectors.go).
	connectors *turnConnectors
	// media is what the turn's images decide about its route (P9-02,
	// uploads.go).
	media *turnMedia
}

const turnColumns = `id, chat_id, status, request_id, actor, input, model, user_message_id,
	reply_message_id, stop_reason, error, usage, cost_micros, stop_requested, last_event,
	created_at, started_at, ended_at`

func scanTurn(sc interface{ Scan(...any) error }) (row, error) {
	var t row
	var input, failure, usage, created, started, ended string
	err := sc.Scan(&t.ID, &t.ChatID, &t.Status, &t.requestID, &t.actor, &input, &t.Model, &t.UserMessageID,
		&t.ReplyMessageID, &t.StopReason, &failure, &usage, &t.CostMicros, &t.stopAsked, &t.LastEventID,
		&created, &started, &ended)
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, errNoTurn
	}
	if err != nil {
		return row{}, err
	}
	if err := json.Unmarshal([]byte(input), &t.input); err != nil {
		return row{}, fmt.Errorf("turn %s: its stored input does not read: %w", t.ID, err)
	}
	if err := json.Unmarshal([]byte(usage), &t.Usage); err != nil {
		return row{}, fmt.Errorf("turn %s: its stored usage does not read: %w", t.ID, err)
	}
	if failure != "" {
		t.Error = &TurnError{}
		if err := json.Unmarshal([]byte(failure), t.Error); err != nil {
			return row{}, fmt.Errorf("turn %s: its stored error does not read: %w", t.ID, err)
		}
	}
	if t.Created, err = time.Parse(timeLayout, created); err != nil {
		return row{}, fmt.Errorf("turn %s: %w", t.ID, err)
	}
	for _, x := range []struct {
		text string
		into **time.Time
	}{{started, &t.Started}, {ended, &t.Ended}} {
		if x.text == "" {
			continue
		}
		at, err := time.Parse(timeLayout, x.text)
		if err != nil {
			return row{}, fmt.Errorf("turn %s: %w", t.ID, err)
		}
		*x.into = &at
	}
	return t, nil
}

// getTurn returns one turn of one chat.
func getTurn(ctx context.Context, s *tenant.Store, chatID, id string) (row, error) {
	return scanTurn(s.DB().QueryRowContext(ctx,
		"SELECT "+turnColumns+" FROM turns WHERE id = ? AND chat_id = ?", id, chatID))
}

// openTurns returns a chat's turns that are queued or running, oldest first.
func openTurns(ctx context.Context, s *tenant.Store, chatID string) ([]row, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT "+turnColumns+
		" FROM turns WHERE status IN ('queued', 'running') AND chat_id = ? ORDER BY seq", chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// nextQueued returns the chat's oldest turn that waits.
func nextQueued(ctx context.Context, s *tenant.Store, chatID string) (row, bool, error) {
	t, err := scanTurn(s.DB().QueryRowContext(ctx, "SELECT "+turnColumns+
		" FROM turns WHERE status = 'queued' AND chat_id = ? ORDER BY seq LIMIT 1", chatID))
	if errors.Is(err, errNoTurn) {
		return row{}, false, nil
	}
	return t, err == nil, err
}

// busyChats returns the ids of the chats with a turn queued or running.
func busyChats(ctx context.Context, s *tenant.Store) (map[string]bool, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT DISTINCT chat_id FROM turns WHERE status IN ('queued', 'running')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// lastModel returns the model of the chat's newest turn that had one.
func lastModel(ctx context.Context, s *tenant.Store, chatID string) (string, error) {
	var name string
	err := s.DB().QueryRowContext(ctx,
		"SELECT model FROM turns WHERE chat_id = ? AND model <> '' ORDER BY seq DESC LIMIT 1", chatID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// inTx runs f in one transaction, and rolls back if f fails.
func inTx(ctx context.Context, s *tenant.Store, f func(tx *sql.Tx) error) error {
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// errQueueFull says the chat has as many turns waiting as it may.
var errQueueFull = errors.New("the chat's queue is full")

// enqueue writes a new turn, waiting, with its first event. queueMax is
// the most turns that may wait behind the one that runs.
func enqueue(ctx context.Context, s *tenant.Store, chatID, requestID, actor string, body SendBody, queueMax int) (row, error) {
	id, err := newID()
	if err != nil {
		return row{}, err
	}
	input, err := json.Marshal(body)
	if err != nil {
		return row{}, err
	}
	var out row
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		var open int
		if err := tx.QueryRow("SELECT count(*) FROM turns WHERE chat_id = ? AND status IN ('queued', 'running')", chatID).Scan(&open); err != nil {
			return err
		}
		if open > queueMax {
			return errQueueFull
		}
		if _, err := tx.Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES(?, ?, 'queued', ?, ?, ?, ?)",
			id, chatID, requestID, actor, string(input), stamp()); err != nil {
			return err
		}
		out, err = emitTurn(tx, id, EventTurn)
		return err
	})
	return out, err
}

// emit adds one event to a turn inside tx and returns its id.
func emit(tx *sql.Tx, turnID, name string, data any) (int64, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return 0, err
	}
	var seq int64
	err = tx.QueryRow("UPDATE turns SET last_event = last_event + 1 WHERE id = ? RETURNING last_event", turnID).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errNoTurn
	}
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec("INSERT INTO turn_events(turn_id, seq, name, data) VALUES(?, ?, ?, ?)", turnID, seq, name, string(raw))
	return seq, err
}

// emitTurn adds an event whose data is the turn itself, as it stands in
// tx with that event counted.
func emitTurn(tx *sql.Tx, turnID, name string) (row, error) {
	if _, err := tx.Exec("UPDATE turns SET last_event = last_event + 1 WHERE id = ?", turnID); err != nil {
		return row{}, err
	}
	t, err := scanTurn(tx.QueryRow("SELECT "+turnColumns+" FROM turns WHERE id = ?", turnID))
	if err != nil {
		return row{}, err
	}
	raw, err := json.Marshal(t.Turn)
	if err != nil {
		return row{}, err
	}
	_, err = tx.Exec("INSERT INTO turn_events(turn_id, seq, name, data) VALUES(?, ?, ?, ?)", turnID, t.LastEventID, name, string(raw))
	return t, err
}

// emitOne adds one event in a transaction of its own.
func emitOne(s *tenant.Store, turnID, name string, data any) error {
	return inTx(context.Background(), s, func(tx *sql.Tx) error {
		_, err := emit(tx, turnID, name, data)
		return err
	})
}

// stored is one event as it is kept.
type stored struct {
	seq  int64
	name string
	data string
}

// eventsAfter returns up to limit of a turn's events after the one with
// the id after, in order.
func eventsAfter(ctx context.Context, s *tenant.Store, turnID string, after int64, limit int) ([]stored, error) {
	rows, err := s.DB().QueryContext(ctx,
		"SELECT seq, name, data FROM turn_events WHERE turn_id = ? AND seq > ? ORDER BY seq LIMIT ?", turnID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []stored
	for rows.Next() {
		var e stored
		if err := rows.Scan(&e.seq, &e.name, &e.data); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// finish closes a turn that is still open, with its last events: an error
// event when it failed, then end. It reports whether this call closed it.
// A turn that has already ended is left as it is.
func finish(s *tenant.Store, turnID, status string, failure *TurnError) (bool, error) {
	return finishFrom(s, turnID, status, failure, "status IN ('queued', 'running')")
}

// finishQueued stops a turn that still waits. It reports false when the
// turn has started, or ended, in the meantime.
func finishQueued(s *tenant.Store, turnID string) (bool, error) {
	return finishFrom(s, turnID, StatusStopped, nil, "status = 'queued'")
}

func finishFrom(s *tenant.Store, turnID, status string, failure *TurnError, open string) (bool, error) {
	closed := false
	err := inTx(context.Background(), s, func(tx *sql.Tx) error {
		text := ""
		if failure != nil {
			raw, err := json.Marshal(failure)
			if err != nil {
				return err
			}
			text = string(raw)
		}
		res, err := tx.Exec("UPDATE turns SET status = ?, error = ?, ended_at = ? WHERE id = ? AND "+open,
			status, text, stamp(), turnID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		closed = true
		if failure != nil {
			if _, err := emit(tx, turnID, EventError, failure); err != nil {
				return err
			}
		}
		_, err = emitTurn(tx, turnID, EventEnd)
		return err
	})
	return closed, err
}

// meta is what a turn recorded about a message it wrote.
type meta struct {
	turnID     string
	model      string
	stopReason string
	usage      *seam.Usage
	costMicros int64
	location   *SentLocation // the person's message was sent with it (P10-06, location.go)
}

// noteMessage records that a turn wrote a message.
func noteMessage(s *tenant.Store, messageID int64, m meta) error {
	usage := ""
	if m.usage != nil {
		raw, err := json.Marshal(m.usage)
		if err != nil {
			return err
		}
		usage = string(raw)
	}
	_, err := s.DB().Exec("INSERT INTO turn_messages(message_id, turn_id, model, stop_reason, usage, cost_micros) VALUES(?, ?, ?, ?, ?, ?)",
		messageID, m.turnID, m.model, m.stopReason, usage, m.costMicros)
	return err
}

// metaOf returns what turns recorded about a chat's messages, by message id.
func metaOf(ctx context.Context, s *tenant.Store, chatID string) (map[int64]meta, error) {
	rows, err := s.DB().QueryContext(ctx, `SELECT tm.message_id, tm.turn_id, tm.model, tm.stop_reason, tm.usage, tm.cost_micros
		FROM turn_messages tm JOIN turns t ON t.id = tm.turn_id WHERE t.chat_id = ?`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]meta{}
	for rows.Next() {
		var id int64
		var m meta
		var usage string
		if err := rows.Scan(&id, &m.turnID, &m.model, &m.stopReason, &usage, &m.costMicros); err != nil {
			return nil, err
		}
		if usage != "" {
			m.usage = &seam.Usage{}
			if err := json.Unmarshal([]byte(usage), m.usage); err != nil {
				return nil, fmt.Errorf("message %d: its stored usage does not read: %w", id, err)
			}
		}
		out[id] = m
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, withLocations(ctx, s, chatID, out) // P10-06: the locations the person's messages were sent with
}

// withMeta is a message of the chat record with what its turn recorded.
func withMeta(m chat.Message, x meta) Message {
	return Message{
		ID: m.ID, ChatID: m.ChatID, ParentID: m.ParentID, Role: m.Role, Blocks: m.Blocks,
		Created: m.Created, Siblings: m.Siblings,
		TurnID: x.turnID, Model: x.model, StopReason: x.stopReason, Usage: x.usage, CostMicros: x.costMicros,
		Location: x.location,
	}
}
