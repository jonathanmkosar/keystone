package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

// Unattended spend and the pause switch, as background tasks meet them
// (P8-05). The switch is the person's setting agents.paused
// (internal/unattended). A task's goroutine reads it at every look, so the
// switch holds tasks whichever way it was turned on: the pause route wakes
// every task at once, and Settings → General is seen within
// chats.stream_poll.
//
//   - Switched on, a task with a round under way has the round stopped, as
//     the stop route stops it: the model call is cancelled through its
//     context and recorded as stopped, and what it had written is kept.
//     The task is marked paused, not failed, and a note in its chat says
//     why. A task that has not started is held.
//   - Switched off, a paused task goes on with a new round, sent with
//     agents.task_unpause_text, as a round a restart cut short goes on with
//     agents.task_resume_text; a held one starts.
//
// A stop the person asks for wins over the switch: a paused task can be
// stopped, and ends as stopped.

// whyUnpause is a round sent after the switch stopped the round before and
// went off again.
const whyUnpause = "unpause"

// pause is the switch as a task's goroutine sees it, asked at every look
// after a stop the person asked for. done says it did what the task needed
// at this look, and wait whether to wait for news before looking again.
func (r *Runner) pause(ctx context.Context, s *tenant.Store, j row) (done, wait bool) {
	on, err := unattended.Paused(ctx, s)
	if err != nil && ctx.Err() == nil {
		r.logf("tasks: %s reading %s, so the task is held: %v", j.requestID, unattended.KeyPaused, err)
	}
	switch {
	case on && !j.paused:
		r.hold(ctx, s, j)
		return true, false
	case on:
		r.notePause(ctx, s, j)
		return true, true
	case j.paused:
		if _, err := r.change(ctx, s, j.ID, "paused = 0, progress = ?", "Going on"); err != nil && !errors.Is(err, errNoTask) {
			r.logf("tasks: %s letting task %s go on: %v", j.requestID, j.ID, err)
			return true, true
		}
		return true, false
	case j.pausedTurn != "":
		return true, r.goOn(ctx, s, j)
	}
	return false, false
}

// hold stops a task's round, if one is under way, and marks the task
// paused.
func (r *Runner) hold(ctx context.Context, s *tenant.Store, j row) {
	// A round the switch stopped before, and that no new round has followed
	// yet, is still the one to follow.
	stopped, note := j.pausedTurn, ""
	words := "Held: every agent is paused. It starts when they are resumed."
	cur, _, ok, err := turn.Latest(ctx, s, j.WorkChatID, j.requestID)
	if err != nil {
		if ctx.Err() == nil {
			r.logf("tasks: %s reading the round of task %s to pause it: %v", j.requestID, j.ID, err)
		}
		return
	}
	if ok && (cur.Status == turn.StatusQueued || cur.Status == turn.StatusRunning) {
		r.stopRound(ctx, s, j)
		stopped = cur.ID
		words = "Paused: every agent is paused. It goes on when they are resumed."
		if note, err = newID(); err != nil {
			r.logf("tasks: %s pausing task %s: %v", j.requestID, j.ID, err)
			note = ""
		}
	} else if j.Rounds > 0 {
		words = "Paused: every agent is paused. It goes on when they are resumed."
	}
	if _, err := r.change(ctx, s, j.ID, "paused = 1, paused_turn = ?, pause_note_turn = ?, waiting = 0, progress = ?", stopped, note, words); err != nil && !errors.Is(err, errNoTask) {
		r.logf("tasks: %s pausing task %s: %v", j.requestID, j.ID, err)
	}
}

// notePause writes, once, the note that tells a paused task's chat its
// round was stopped by the switch. It is written under a request id of its
// own, so it is never taken for one of the task's rounds.
func (r *Runner) notePause(ctx context.Context, s *tenant.Store, j row) {
	if j.pauseNote == "" {
		return
	}
	text := "Paused: every agent was paused while this background task was working, so its last step was cut short. What it had done is kept. It goes on when they are resumed."
	if inPlace(j) {
		text = "Paused: every agent was paused while this was running, so its last step was cut short. What it had done is kept. It goes on when they are resumed."
	}
	blocks := []seam.Block{{Type: seam.BlockText, Text: text}}
	if _, err := r.bound().Note(ctx, s.TenantID(), j.WorkChatID, j.pauseNote, audit.NewRequestID(), j.actor, blocks, turn.Carry{}); err != nil {
		if !errors.Is(err, chat.ErrNotFound) && !errors.Is(err, turn.ErrClosing) && ctx.Err() == nil {
			r.logf("tasks: %s writing the pause note of task %s, which is tried again: %v", j.requestID, j.ID, err)
			return
		}
	}
	if _, err := r.change(ctx, s, j.ID, "pause_note_turn = ''"); err != nil && !errors.Is(err, errNoTask) {
		r.logf("tasks: %s noting the pause note of task %s: %v", j.requestID, j.ID, err)
	}
}

// goOn follows the round the switch stopped with a new one, once the
// switch is off, and reports whether to wait before looking again. The
// stopped round is kept on the task until the new one is sent, so a round
// that cannot be sent yet is tried again and the task is never taken for
// one the person stopped.
func (r *Runner) goOn(ctx context.Context, s *tenant.Store, j row) bool {
	cur, rounds, ok, err := turn.Latest(ctx, s, j.WorkChatID, j.requestID)
	if err != nil {
		if ctx.Err() == nil {
			r.logf("tasks: %s reading the round of task %s: %v", j.requestID, j.ID, err)
		}
		return true
	}
	clear := func() {
		if _, err := r.change(ctx, s, j.ID, "paused_turn = ''"); err != nil && !errors.Is(err, errNoTask) {
			r.logf("tasks: %s noting task %s goes on: %v", j.requestID, j.ID, err)
		}
	}
	if !ok || cur.ID != j.pausedTurn || cur.Status != turn.StatusStopped {
		clear() // the round ended by itself, or another is under way
		return false
	}
	if rounds >= j.MaxRounds {
		r.finish(ctx, s, j, StatusFailed, &TaskError{Code: CodeRoundLimit,
			Message: fmt.Sprintf("It took %d rounds, the most a background task may take, and had not finished.", j.MaxRounds)}, "")
		return false
	}
	if !r.send(ctx, s, j, whyUnpause, rounds+1) {
		return true
	}
	clear()
	return false
}

// tell writes in a task's chat, once its round has ended, a note of each
// call of the round the monthly cap or the task's budget refused, naming
// the model that wrote the reply in its place (decision 0160). A task that
// works in the chat it was started from is told of a stop by the note of
// its end, so a stop is left out there.
func (r *Runner) tell(ctx context.Context, s *tenant.Store, j row, cur turn.Turn) {
	err := unattended.Tell(ctx, s, j.requestID, cur.ID, !inPlace(j), func(noteTurn, text string) error {
		blocks := []seam.Block{{Type: seam.BlockText, Text: text}}
		_, err := r.bound().Note(ctx, s.TenantID(), j.WorkChatID, noteTurn, audit.NewRequestID(), j.actor, blocks, turn.Carry{})
		if errors.Is(err, chat.ErrNotFound) {
			return nil
		}
		return err
	})
	if err != nil && !errors.Is(err, turn.ErrClosing) && ctx.Err() == nil {
		r.logf("tasks: %s telling the chat of task %s that a call was refused, which is tried again: %v", j.requestID, j.ID, err)
	}
}

// withNotices adds to the note a task writes in the chat it was started
// from what its notices came to: which model wrote its work when the cap
// or its budget refused the one it was given.
func (r *Runner) withNotices(ctx context.Context, s *tenant.Store, j row, text string) string {
	if inPlace(j) {
		return text
	}
	more, err := unattended.Summary(ctx, s, j.requestID)
	if err != nil {
		r.logf("tasks: %s reading the notices of task %s: %v", j.requestID, j.ID, err)
	}
	if more == "" {
		return text
	}
	return strings.TrimSpace(text) + "\n\n" + more
}

// PauseAll makes every open task of the person's look at the switch now,
// instead of at its next look: the pause route calls it once the switch is
// on (unattended.Options.Pause). A task with no goroutine at work gets one.
func (r *Runner) PauseAll(ctx context.Context, s *tenant.Store) error { return r.wakeOpen(ctx, s) }

// ResumeAll does the same once the switch is off, so the paused tasks go
// on now (unattended.Options.Resume).
func (r *Runner) ResumeAll(ctx context.Context, s *tenant.Store) error { return r.wakeOpen(ctx, s) }

func (r *Runner) wakeOpen(ctx context.Context, s *tenant.Store) error {
	rows, err := s.DB().QueryContext(ctx, "SELECT id FROM jobs WHERE status IN ('queued', 'running') ORDER BY seq")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		r.wake(s.TenantID() + "/" + id)
		r.kick(s.TenantID(), id)
	}
	return nil
}

// Runs counts the person's open tasks (unattended.Options.Runs): those with
// work under way, which are the ones not yet held and the held ones whose
// stopped round has not yet ended, and those the switch holds.
func (r *Runner) Runs(ctx context.Context, s *tenant.Store) (unattended.Count, error) {
	var n unattended.Count
	err := s.DB().QueryRowContext(ctx, `SELECT
		count(*) FILTER (WHERE j.paused = 0 OR EXISTS (SELECT 1 FROM turns t WHERE t.id = j.paused_turn AND t.status IN ('queued', 'running'))),
		count(*) FILTER (WHERE j.paused = 1)
		FROM jobs j WHERE j.status IN ('queued', 'running')`).Scan(&n.Working, &n.Held)
	return n, err
}
