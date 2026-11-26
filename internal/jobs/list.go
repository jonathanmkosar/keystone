package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// A list of every task a person has, across chats, for Operations (P11-03).
// It reads the jobs table as the routes do, through scanJob, so a task the
// pause switch holds reads as paused here too. It changes nothing.

// Listed is one task in a list across chats: the task as the routes show
// it, with what a reader outside a chat needs beside it.
type Listed struct {
	Task
	// RequestID is the task's own request id: every turn of the task is
	// sent under it, so its model calls, tool calls and usage records carry
	// it, and its spend and its trace are read by it.
	RequestID string `json:"request_id"`
	// Scheduled says the task is the run of a scheduled task.
	Scheduled bool `json:"scheduled"`
	// RoundStarted is when its current round's turn began, or was made
	// while it waits in its chat's queue. Absent before its first round.
	RoundStarted *time.Time `json:"round_started,omitempty"`
}

// Filter narrows List. The zero Filter lists every task.
type Filter struct {
	// Open keeps the tasks that are waiting or running, which includes
	// those the pause switch holds.
	Open bool
	// Status keeps the tasks whose stored status is this one: done,
	// stopped or failed. A task the switch holds is stored as queued or
	// running.
	Status string
	// EndedSince keeps the tasks that ended at or after this time.
	EndedSince time.Time
	// Limit is the most tasks to return, the newest. Zero means all.
	Limit int
}

// List returns a tenant's tasks across all its chats, newest first.
func List(ctx context.Context, s *tenant.Store, f Filter) ([]Listed, error) {
	var where []string
	var args []any
	if f.Open {
		where = append(where, "status IN ('queued', 'running')")
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if !f.EndedSince.IsZero() {
		where = append(where, "ended_at <> '' AND ended_at >= ?")
		args = append(args, f.EndedSince.UTC().Format(timeLayout))
	}
	q := "SELECT " + jobColumns + `,
		EXISTS(SELECT 1 FROM schedule_runs r WHERE r.job_id = jobs.id),
		coalesce((SELECT CASE WHEN t.started_at <> '' THEN t.started_at ELSE t.created_at END FROM turns t WHERE t.id = jobs.turn_id), '')
		FROM jobs`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY seq DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Listed
	for rows.Next() {
		var scheduled bool
		var round string
		j, err := scanJob(withMore{rows, []any{&scheduled, &round}})
		if err != nil {
			return nil, err
		}
		l := Listed{Task: j.Task, RequestID: j.requestID, Scheduled: scheduled}
		if round != "" {
			at, err := time.Parse(timeLayout, round)
			if err != nil {
				return nil, fmt.Errorf("task %s: its round's time does not read: %w", j.ID, err)
			}
			l.RoundStarted = &at
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Tally returns how many of a tenant's tasks are in each status, as the
// routes show it: queued, running, paused, done, stopped and failed, with
// waiting beside them for the open tasks a call of which waits for the
// person's approval. A status with no task is absent.
func Tally(ctx context.Context, s *tenant.Store) (map[string]int, error) {
	rows, err := s.DB().QueryContext(ctx, `SELECT status, paused, waiting, count(*) FROM jobs GROUP BY status, paused, waiting`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var paused, waiting bool
		var n int
		if err := rows.Scan(&status, &paused, &waiting, &n); err != nil {
			return nil, err
		}
		open := status == StatusQueued || status == StatusRunning
		if paused && open {
			status = StatusPaused
		}
		out[status] += n
		if waiting && open {
			out["waiting"] += n
		}
	}
	return out, rows.Err()
}

// withMore scans a row of jobColumns and then more columns.
type withMore struct {
	rows *sql.Rows
	more []any
}

func (w withMore) Scan(dest ...any) error { return w.rows.Scan(append(dest, w.more...)...) }
