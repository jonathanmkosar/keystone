package figures

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// timeLayout is the stored form of a time in the tables figures reads: it
// sorts as text in the order of time.
const timeLayout = "2006-01-02T15:04:05.000Z"

func stamp(t time.Time) string { return t.UTC().Format(timeLayout) }

// RunBudget is one background task or scheduled run under way, as numbers:
// its request id, its budget and what it has spent on models that are not
// local, summed from its usage records as its budget's gate sums them.
type RunBudget struct {
	RequestID    string
	BudgetMicros int64
	SpentMicros  int64
}

// RunBudgets are the person's runs under way, oldest first: the tasks
// queued or running, which includes those the pause switch holds. They are
// read from the tasks' rows, never their titles or their words.
func RunBudgets(ctx context.Context, s *tenant.Store) ([]RunBudget, error) {
	rows, err := s.DB().QueryContext(ctx, `SELECT request_id, budget_micros FROM jobs WHERE status IN ('queued', 'running') ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	var out []RunBudget
	for rows.Next() {
		var b RunBudget
		if err := rows.Scan(&b.RequestID, &b.BudgetMicros); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		recs, err := usage.List(ctx, s, usage.Filter{RequestID: out[i].RequestID})
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			if usage.Counts(r.Class) {
				out[i].SpentMicros += r.CostMicros
			}
		}
	}
	return out, nil
}

// Crossed returns the keys of a person's limits whose watch level is
// crossed now, in Operations' order: the monthly cap, the memory reserve,
// their database, files and artifacts, then each run under way as run and
// its request id after a colon. It is what Operations' watch route lists.
func Crossed(ctx context.Context, s *tenant.Store, reg []settings.Setting, cfg Settings, now time.Time) ([]string, error) {
	readings, err := Measure(ctx, s, reg, cfg, now)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, r := range readings {
		if _, _, crossed := Judge(r.Key, r.Used, r.Of, r.Watch, r.Problem); crossed {
			out = append(out, r.Key)
		}
	}
	runs, err := RunBudgets(ctx, s)
	if err != nil {
		return nil, err
	}
	for _, b := range runs {
		if _, _, crossed := Judge(LimitRun, b.SpentMicros, b.BudgetMicros, cfg.Watch[LimitRun], ""); crossed {
			out = append(out, LimitRun+":"+b.RequestID)
		}
	}
	return out, nil
}

// Jobs is a person's background tasks and scheduled runs as two numbers.
type Jobs struct {
	// Failed is how many failed within the health window.
	Failed int `json:"failed"`
	// Stuck is how many are open, not paused and not waiting for the
	// person, and began their current round longer ago than
	// operations.stuck_after.
	Stuck int `json:"stuck"`
}

// ReadJobs counts a person's failed and stuck tasks as Operations lists
// them, from the tasks' rows.
func ReadJobs(ctx context.Context, s *tenant.Store, cfg Settings, now time.Time) (Jobs, error) {
	var out Jobs
	if err := s.DB().QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE status = 'failed' AND ended_at <> '' AND ended_at >= ?`,
		stamp(now.Add(-cfg.HealthWindow))).Scan(&out.Failed); err != nil {
		return Jobs{}, err
	}
	rows, err := s.DB().QueryContext(ctx, `SELECT created_at, started_at,
			coalesce((SELECT CASE WHEN t.started_at <> '' THEN t.started_at ELSE t.created_at END FROM turns t WHERE t.id = jobs.turn_id), '')
		FROM jobs WHERE status IN ('queued', 'running') AND paused = 0 AND waiting = 0`)
	if err != nil {
		return Jobs{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var created, started, round string
		if err := rows.Scan(&created, &started, &round); err != nil {
			return Jobs{}, err
		}
		began := created
		switch {
		case round != "":
			began = round
		case started != "":
			began = started
		}
		at, err := time.Parse(timeLayout, began)
		if err != nil {
			return Jobs{}, fmt.Errorf("figures: a task's time does not read: %w", err)
		}
		if now.Sub(at) > cfg.StuckAfter {
			out.Stuck++
		}
	}
	return out, rows.Err()
}

// Schedules is how many schedules a person has, on or off.
func Schedules(ctx context.Context, s *tenant.Store) (int, error) {
	var n int
	err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM schedules").Scan(&n)
	return n, err
}

// Newest returns the time of the person's newest snapshot of their limits,
// and false when they have none.
func Newest(ctx context.Context, s *tenant.Store) (time.Time, bool, error) {
	var at sql.NullString
	if err := s.DB().QueryRowContext(ctx, "SELECT max(at) FROM operations_snapshots").Scan(&at); err != nil {
		return time.Time{}, false, fmt.Errorf("operations: %w", err)
	}
	if !at.Valid || at.String == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(timeLayout, at.String)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("operations: the newest snapshot has a time that cannot be read")
	}
	return t, true, nil
}
