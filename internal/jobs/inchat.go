package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// A task that works in the chat it was started from (P8-03, scheduled
// tasks). A scheduled run makes a chat of its own, titled by its schedule
// and date, and its work is that chat's turns: the run is the chat. So the
// task's work chat and the chat it was started from are one chat, and its
// result is already there as the chat's last reply. When it is done no note
// is written, and the reply stands as its result; when it was stopped or
// failed, a note in the chat says why, as for any task.
//
// Everything else is a task's: the rounds, the budget, the request id of
// its own, stop, approvals and recovery after a restart.

// The errors a caller outside the package needs to tell apart.
var (
	// ErrNoTask says there is no such task in this tenant's record.
	ErrNoTask = errNoTask
	// ErrTooMany says the person has agents.task_open_max tasks waiting or
	// running, so another was not started.
	ErrTooMany = errTooMany
	// ErrInvalid says the task was refused: it was empty, or the chat
	// cannot have one.
	ErrInvalid = errInvalid
)

// StartIn writes a task that works in chatID itself, and starts its work.
// The chat must be in the person's record, and is usually new: its first
// round's message is the task's words. One task works in a chat: when one
// already does, it is returned as it is and nothing new starts, so a caller
// that was cut short may call again. startedRequest is the call or the
// event that started it, actor who it runs for, and model the model its
// rounds name, or "" for the chat's own.
func (r *Runner) StartIn(ctx context.Context, s *tenant.Store, chatID, startedRequest, actor string, body StartBody, model string) (Task, error) {
	text := strings.TrimSpace(body.Text)
	if text == "" {
		return Task{}, fmt.Errorf("%w: the task is empty: say what to do", errInvalid)
	}
	if _, err := chat.Get(s, chatID); err != nil {
		return Task{}, err
	}
	have, err := scanJob(s.DB().QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE work_chat_id = ? ORDER BY seq LIMIT 1", chatID))
	switch {
	case err == nil:
		return have.Task, nil
	case !errors.Is(err, errNoTask):
		return Task{}, err
	}
	budget, err := r.budget(ctx, s)
	if err != nil {
		return Task{}, err
	}
	if model == "" {
		if t := r.bound(); t != nil {
			model = t.ChatModel(ctx, s, chatID)
		}
	}
	id, err := newID()
	if err != nil {
		return Task{}, err
	}
	var j row
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		var open int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE status IN ('queued', 'running')").Scan(&open); err != nil {
			return err
		}
		if open >= r.o.Config.OpenMax {
			return fmt.Errorf("%w: %d are waiting or running, the most one person may have", errTooMany, open)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO jobs(id, chat_id, work_chat_id, title, task, started_by, status, request_id,
			started_request, actor, model, budget_micros, max_rounds, progress, created_at)
			VALUES(?, ?, ?, ?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, chatID, chatID, r.titleOf(body.Title, text), text, StartedByPerson, audit.NewRequestID(), startedRequest, actor, model,
			budget, r.o.Config.MaxRounds, "Waiting to start", stamp()); err != nil {
			return err
		}
		j, err = emitTask(ctx, tx, id, EventTask)
		return err
	})
	if err != nil {
		return Task{}, err
	}
	r.kick(s.TenantID(), id)
	return j.Task, nil
}

// Read returns one task of the tenant's by its id, as the routes show it.
func Read(ctx context.Context, s *tenant.Store, id string) (Task, error) {
	j, err := getJob(ctx, s.DB(), id)
	return j.Task, err
}

// inPlace reports whether a task works in the chat it was started from.
func inPlace(j row) bool { return j.ChatID == j.WorkChatID }

// deliverInPlace ends a task that worked in the chat it was started from
// and is done: its last round's reply is its result, already in the chat,
// so that message is noted as the task's and no note is written. It reports
// false, and does nothing, for a task that did not finish or gave no reply:
// deliver writes a note that says so.
func (r *Runner) deliverInPlace(ctx context.Context, s *tenant.Store, j row) bool {
	if j.Status != StatusDone {
		return false
	}
	cur, _, ok, err := turn.Latest(ctx, s, j.WorkChatID, j.requestID)
	if err != nil || !ok || cur.ReplyMessageID == 0 {
		return false
	}
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET note_message_id = ? WHERE id = ?", cur.ReplyMessageID, j.ID); err != nil {
			return err
		}
		_, err := emitTask(ctx, tx, j.ID, EventEnd)
		return err
	})
	if err != nil && !errors.Is(err, errNoTask) {
		r.logf("tasks: %s noting the result of task %s: %v", j.requestID, j.ID, err)
		return true
	}
	r.wake(s.TenantID() + "/" + j.ID + "/events")
	return true
}

// inPlaceNote is what the note of a task that worked in the chat it was
// started from says: the chat above it holds what the task did.
func inPlaceNote(j row) string {
	switch j.Status {
	case StatusDone:
		return fmt.Sprintf("“%s” is done. It gave no result in words: the steps above have what it did.", j.Title)
	case StatusStopped:
		return fmt.Sprintf("“%s” was stopped before it finished. The steps above have what it did.", j.Title)
	}
	why := "It failed."
	if j.Error != nil && j.Error.Message != "" {
		why = j.Error.Message
	}
	return fmt.Sprintf("“%s” did not finish. %s", j.Title, why)
}
