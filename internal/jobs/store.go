package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// errNoTask says the task is not in this tenant's record, or not in the
// chat it was asked for under.
var errNoTask = errors.New("no such task")

// Times are stored as the chat record and the turns store them: text of
// one fixed width, in UTC, so they sort as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

func stamp() string { return time.Now().UTC().Format(timeLayout) }

// row is a task as the runner needs it: what the API shows, and what it
// runs with.
type row struct {
	Task
	requestID      string
	startedRequest string
	actor          string
	model          string
	noteTurnID     string
	stopAsked      bool

	// The switch that pauses every agent (P8-05, pause.go): paused says it
	// holds the task, pausedTurn is the round it stopped, and pauseNote the
	// turn of the note that says so in the task's chat, until it is written.
	// stored is the status in the database, which Status shows as paused.
	paused     bool
	pausedTurn string
	pauseNote  string
	stored     string
}

const jobColumns = `id, chat_id, work_chat_id, title, task, started_by, status, request_id, started_request,
	actor, model, budget_micros, max_rounds, rounds, steps, turn_id, waiting, progress, spent_micros, result,
	error, note_turn_id, note_message_id, stop_requested, last_event, created_at, started_at, ended_at,
	paused, paused_turn, pause_note_turn`

func scanJob(sc interface{ Scan(...any) error }) (row, error) {
	var j row
	var failure, created, started, ended string
	err := sc.Scan(&j.ID, &j.ChatID, &j.WorkChatID, &j.Title, &j.Task.Task, &j.StartedBy, &j.Status, &j.requestID, &j.startedRequest,
		&j.actor, &j.model, &j.BudgetMicros, &j.MaxRounds, &j.Rounds, &j.Steps, &j.TurnID, &j.Waiting, &j.Progress, &j.SpentMicros, &j.Result,
		&failure, &j.noteTurnID, &j.NoteMessageID, &j.stopAsked, &j.LastEventID, &created, &started, &ended,
		&j.paused, &j.pausedTurn, &j.pauseNote)
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, errNoTask
	}
	if err != nil {
		return row{}, err
	}
	j.stored = j.Status
	if j.paused && (j.Status == StatusQueued || j.Status == StatusRunning) {
		j.Status = StatusPaused
	}
	if failure != "" {
		j.Error = &TaskError{}
		if err := json.Unmarshal([]byte(failure), j.Error); err != nil {
			return row{}, fmt.Errorf("task %s: its stored error does not read: %w", j.ID, err)
		}
	}
	if j.Created, err = time.Parse(timeLayout, created); err != nil {
		return row{}, fmt.Errorf("task %s: %w", j.ID, err)
	}
	for _, x := range []struct {
		text string
		into **time.Time
	}{{started, &j.Started}, {ended, &j.Ended}} {
		if x.text == "" {
			continue
		}
		at, err := time.Parse(timeLayout, x.text)
		if err != nil {
			return row{}, fmt.Errorf("task %s: %w", j.ID, err)
		}
		*x.into = &at
	}
	return j, nil
}

// getJob returns one task by its id.
func getJob(ctx context.Context, q queryer, id string) (row, error) {
	return scanJob(q.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id = ?", id))
}

// getInChat returns one task of a chat: one started from it, or working
// in it.
func getInChat(ctx context.Context, s *tenant.Store, chatID, id string) (row, error) {
	return scanJob(s.DB().QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id = ? AND (chat_id = ? OR work_chat_id = ?)", id, chatID, chatID))
}

// listInChat returns the tasks started from a chat or working in it,
// oldest first.
func listInChat(ctx context.Context, s *tenant.Store, chatID string) ([]row, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE chat_id = ? OR work_chat_id = ? ORDER BY seq", chatID, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// unfinished returns the ids of a tenant's tasks that are waiting or
// running, or that have ended and whose result is not yet written in their
// chat.
func unfinished(ctx context.Context, s *tenant.Store) ([]string, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT id FROM jobs WHERE status IN ('queued', 'running') OR note_message_id = 0 ORDER BY seq")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
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

// emitTask adds an event whose data is the task itself, as it stands in tx
// with that event counted, and returns the task.
func emitTask(ctx context.Context, tx *sql.Tx, id, name string) (row, error) {
	res, err := tx.ExecContext(ctx, "UPDATE jobs SET last_event = last_event + 1 WHERE id = ?", id)
	if err != nil {
		return row{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return row{}, errNoTask
	}
	j, err := getJob(ctx, tx, id)
	if err != nil {
		return row{}, err
	}
	raw, err := json.Marshal(j.Task)
	if err != nil {
		return row{}, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO job_events(job_id, seq, name, data) VALUES(?, ?, ?, ?)", id, j.LastEventID, name, string(raw))
	return j, err
}

// change writes to a task's row and sends the task as it then stands, in
// one transaction. A task that is gone, with its chat, is errNoTask.
func change(ctx context.Context, s *tenant.Store, id, set string, args ...any) (row, error) {
	var j row
	err := inTx(ctx, s, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE jobs SET "+set+" WHERE id = ?", append(args, id)...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errNoTask
		}
		j, err = emitTask(ctx, tx, id, EventTask)
		return err
	})
	return j, err
}

// stored is one event as it is kept.
type stored struct {
	seq  int64
	name string
	data string
}

// eventsAfter returns up to limit of a task's events after the one with
// the id after, in order.
func eventsAfter(ctx context.Context, s *tenant.Store, id string, after int64, limit int) ([]stored, error) {
	rows, err := s.DB().QueryContext(ctx,
		"SELECT seq, name, data FROM job_events WHERE job_id = ? AND seq > ? ORDER BY seq LIMIT ?", id, after, limit)
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

func ended(status string) bool {
	return status != StatusQueued && status != StatusRunning && status != StatusPaused
}
