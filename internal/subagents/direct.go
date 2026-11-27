package subagents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

// Sub-agents that Keystone's own code starts (P8-04, research mode): the
// parts of a research task. No model calls run_subagents for them, so what
// the tool checks of a model's call is not asked here: the budget of each
// is the caller's, a share of a budget the person set, and not held to
// agents.subagent_budget_usd (decision 0482 is about what a model may ask
// for); and the limit on what one turn may start does not apply, since the
// caller has its own. Everything else is a sub-agent's: a chat of its own
// with one message, its own request id registered as an unattended run
// with its budget, the supervisor that runs agents.subagents_at_once at a
// time, the switch that pauses every agent, the marks carried down
// (internal/subagents/carry), the step limit, the routes and the lanes.

// Part is one sub-agent a caller that is not a model asks for.
type Part struct {
	// Title names it in a few words, for its lane. Empty means the first
	// words of Task.
	Title string
	// Task is what it is to do, in full, and Context what else it is given.
	Task    string
	Context string
	// BudgetMicros is the most it may spend on models that are not local, in
	// millionths of a US dollar.
	BudgetMicros int64
}

// ErrPaused says no sub-agent was started because the person's switch that
// pauses every agent is on, or could not be read.
var ErrPaused = errors.New("every agent is paused")

// StartParts writes one sub-agent for each part and starts their
// supervisor, which runs under the turn runner, and returns at once. chatID
// is the chat they are started from, whose marks go down to each, and
// turnID a turn of that chat that has ended, which the chat is marked under
// when a part's result brings outside content back. callID names the batch:
// Parts lists it, and a batch already written under it is refused, so a
// caller that was cut short can ask Parts first. model is the model each
// part's turn names; empty is the chat's own.
//
// The supervisor ends the parts that are still working after
// agents.subagent_timeout, as it does for a call of the tool.
func (r *Runner) StartParts(ctx context.Context, s *tenant.Store, chatID, turnID, callID, model string, parts []Part) error {
	t := r.bound()
	if t == nil || r.o.Install == nil {
		return errors.New("sub-agents cannot run on this server")
	}
	if len(parts) == 0 || strings.TrimSpace(callID) == "" {
		return errors.New("a batch of sub-agents needs its name and at least one part")
	}
	for i, p := range parts {
		if strings.TrimSpace(p.Task) == "" || p.BudgetMicros < 0 {
			return fmt.Errorf("part %d needs a task and a budget of 0 or more", i+1)
		}
	}
	if _, err := chat.Get(s, chatID); err != nil {
		return err
	}
	if on, err := unattended.Paused(ctx, s); err != nil || on {
		return ErrPaused
	}
	var already int
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM subagents WHERE chat_id = ? AND call_id = ?", chatID, callID).Scan(&already); err != nil {
		return err
	}
	if already > 0 {
		return fmt.Errorf("the batch %s of chat %s is already written", callID, chatID)
	}
	// The incognito mark goes with the words, as a task's does (P6-06). A
	// mark that cannot be read counts as on.
	incognito, err := memory.Incognito(ctx, s, chatID)
	if err != nil {
		incognito = true
	}
	b := batch{tenantID: s.TenantID(), chatID: chatID, turnID: turnID, key: callKey(s.TenantID(), turnID, callID)}
	var made []string
	drop := func(err error) error {
		for _, id := range made {
			if derr := chat.Delete(s, id); derr != nil {
				r.logf("sub-agents: removing the chat made for a part that did not start: %v", derr)
			}
		}
		for _, id := range b.ids {
			if _, derr := s.DB().ExecContext(context.Background(), "DELETE FROM subagents WHERE id = ?", id); derr != nil {
				r.logf("sub-agents: removing a part that did not start: %v", derr)
			}
		}
		return err
	}
	for i, p := range parts {
		title := r.titleOf(p.Title, p.Task)
		work, err := chat.Create(s)
		if err != nil {
			return drop(err)
		}
		made = append(made, work.ID)
		if _, err := chat.Rename(s, r.o.Turns.Limits, work.ID, workTitlePrefix+title); err != nil {
			return drop(err)
		}
		if incognito {
			if err := memory.SetIncognito(ctx, s, work.ID, true); err != nil {
				return drop(err)
			}
		}
		id, err := newID()
		if err != nil {
			return drop(err)
		}
		requestID := audit.NewRequestID()
		err = inTx(ctx, s, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO subagents(id, chat_id, turn_id, call_id, position, work_chat_id, title, task, context,
				request_id, actor, model, budget_micros, max_steps, status, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?)`,
				id, chatID, turnID, callID, i+1, work.ID, title, strings.TrimSpace(p.Task), strings.TrimSpace(p.Context),
				requestID, api.ActorPerson, strings.TrimSpace(model), p.BudgetMicros, r.o.Config.MaxSteps, stamp()); err != nil {
				return err
			}
			// An unattended run, with its budget, before its turn can be sent
			// (P8-05).
			return unattended.Register(ctx, tx, unattended.Run{Kind: unattended.KindSubagent, ID: id, RequestID: requestID, Title: title, BudgetMicros: p.BudgetMicros})
		})
		if err != nil {
			return drop(err)
		}
		b.ids = append(b.ids, id)
	}
	started := t.Go(func(base context.Context) {
		call, cancel := context.WithTimeout(base, r.o.Config.Timeout)
		defer cancel()
		// What the supervisor would hand a tool is not waited for: the caller
		// reads the parts with Parts.
		r.supervise(base, call, b, make(chan outcome, 1))
	})
	if !started {
		// The rows stay: the next start ends them as interrupted (Recover).
		return errors.New("Keystone is stopping, so the parts were not run")
	}
	return nil
}

// Parts returns the sub-agents one batch of a chat started, by place, each
// with where it is, as the routes show them. A batch that was never
// written has none.
func (r *Runner) Parts(ctx context.Context, s *tenant.Store, chatID, callID string) ([]Subagent, error) {
	got, err := list(ctx, s.DB(), "chat_id = ? AND call_id = ?", chatID, callID)
	if err != nil {
		return nil, err
	}
	out := make([]Subagent, 0, len(got))
	for _, a := range got {
		r.live(ctx, s, &a)
		out = append(out, a.Subagent)
	}
	return out, nil
}

// Ended reports whether a sub-agent has ended: it is done, stopped, failed
// or paused, and will do nothing more.
func Ended(a Subagent) bool { return ended(a.Status) }

// StopParts stops every sub-agent of a batch that has not ended, as Stop
// stops one, all at once, and waits for them to end, up to chats.stop_wait.
// It reports whether every one has ended.
func (r *Runner) StopParts(ctx context.Context, s *tenant.Store, chatID, callID string) (bool, error) {
	open := func() ([]row, error) {
		return list(ctx, s.DB(), "chat_id = ? AND call_id = ? AND status IN ('queued', 'running')", chatID, callID)
	}
	rows, err := open()
	if err != nil || len(rows) == 0 {
		return err == nil, err
	}
	for _, a := range rows {
		if err := change(ctx, s, a.ID, "stop_why = CASE WHEN stop_why = '' THEN ? ELSE stop_why END", byPerson); err != nil && !errors.Is(err, errNoSubagent) {
			return false, err
		}
	}
	r.wake(callKey(s.TenantID(), rows[0].TurnID, callID))
	deadline := time.Now().Add(r.o.Turns.StopWait)
	for {
		if rows, err = open(); err != nil || len(rows) == 0 {
			return err == nil, err
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false, nil
		}
		select {
		case <-time.After(min(time.Until(deadline), r.o.Turns.StreamPoll, 100*time.Millisecond)):
		case <-ctx.Done():
		}
	}
}
