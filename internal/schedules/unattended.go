package schedules

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

// Unattended spend and the pause switch, as schedules meet them (P8-05,
// internal/unattended):
//
//   - The switch that pauses every agent and schedule, agents.paused, holds
//     every schedule as the person's own switch, agents.schedules_paused,
//     does (decision 0343): no schedule starts a run, and the due times
//     that pass meanwhile are moved on and not made up. Each schedule shows
//     as held under either. SchedulesReply.Paused stays the schedules'
//     own switch, which Pause all and Resume all set.
//   - Each schedule may have a budget of its own for each of its runs; with
//     none, a run's budget is the person's agents.schedule_budget_usd. A
//     run is registered as a scheduled run, so its notices say so.

// heldAll reads both switches that hold every schedule: the person's own
// for schedules, and the one that pauses every agent and schedule. A
// switch that cannot be read counts as on, and the error is returned.
func (sc *Scheduler) heldAll(ctx context.Context, s *tenant.Store) (bool, error) {
	held, err := sc.held(ctx, s)
	if err != nil {
		return true, err
	}
	paused, err := unattended.Paused(ctx, s)
	return held || paused, err
}

// Rearm moves on, past now, the next due time of each of the person's
// enabled schedules whose due time has passed: nothing paused is made up.
// The route that turns the switch that pauses every agent off calls it
// first (unattended.Options.Rearm), as Hold does for the schedules' own
// switch.
func (sc *Scheduler) Rearm(ctx context.Context, s *tenant.Store) error {
	sc.firing.Lock()
	defer sc.firing.Unlock()
	z, now := sc.zone(ctx, s), sc.now()
	list, err := listSchedules(ctx, s, "enabled = 1 AND next_at <> ''")
	if err != nil {
		return err
	}
	for _, r := range list {
		if !r.next.After(now) {
			sc.rearm(ctx, s, r, now, z)
		}
	}
	sc.Wake()
	return nil
}

// checkBudget refuses a schedule's own budget that is not a number of
// dollars, 0 or more.
func checkBudget(usd *float64) error {
	if usd == nil {
		return nil
	}
	if _, err := unattended.Micros(*usd); err != nil {
		return fmt.Errorf("%w: %s", errInvalid, strings.TrimPrefix(err.Error(), unattended.ErrInvalid.Error()+": "))
	}
	return nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// setBudget writes a schedule's own budget for each run: usd, or none when
// it is nil, which is the person's agents.schedule_budget_usd.
func setBudget(ctx context.Context, q execer, id string, usd *float64) error {
	n := int64(-1)
	if usd != nil {
		var err error
		if n, err = unattended.Micros(*usd); err != nil {
			return fmt.Errorf("%w: %v", errInvalid, err)
		}
	}
	_, err := q.ExecContext(ctx, "UPDATE schedules SET budget_micros = ? WHERE id = ?", n, id)
	return err
}

// budgetOf returns a schedule's own budget for each run, in millionths of a
// dollar, or -1 when it has none.
func budgetOf(ctx context.Context, q queryer, id string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, "SELECT budget_micros FROM schedules WHERE id = ?", id).Scan(&n)
	return n, err
}

// budgets fills a schedule's budget: its own, if it has one, and the one
// each of its runs has.
func (sc *Scheduler) budgets(ctx context.Context, s *tenant.Store, out *Schedule) error {
	own, err := budgetOf(ctx, s.DB(), out.ID)
	if err != nil {
		return err
	}
	if own >= 0 {
		usd := float64(own) / 1e6
		out.BudgetUSD, out.RunBudgetMicros = &usd, own
		return nil
	}
	out.RunBudgetMicros, err = unattended.Budget(ctx, s, unattended.KindSchedule)
	return err
}

// runBody is the task a run starts: the schedule's prompt, named by the
// schedule, as a scheduled run, with the schedule's own budget when it has
// one.
func runBody(ctx context.Context, s *tenant.Store, r row) (jobs.StartBody, error) {
	own, err := budgetOf(ctx, s.DB(), r.id)
	if err != nil {
		return jobs.StartBody{}, err
	}
	b := jobs.StartBody{Text: r.prompt, Title: r.name, Kind: unattended.KindSchedule}
	if own >= 0 {
		usd := float64(own) / 1e6
		b.BudgetUSD = &usd
	}
	return b, nil
}
