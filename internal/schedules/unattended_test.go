package schedules

import (
	"context"
	"net/http"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

// The switch that pauses every agent and schedule (P8-05) holds every
// schedule as the schedules' own switch does: each shows as held while the
// schedules' switch stays off, nothing runs, and once it is off with the
// due times moved on, nothing held is made up.
func TestTheSwitchThatPausesEveryAgentHoldsEverySchedule(t *testing.T) {
	g := newRig(t, utc(t, "2026-11-24T09:00:00Z"))
	g.set(g.test.ID, KeyTimeZone, "Europe/Lisbon")
	lisbon := zone(t, "Europe/Lisbon")
	sv := g.serve(harbourScripts(t))
	one := sv.make(g.test.ID, daily("Harbour note", harbourPrompt, "07:00"))

	g.set(g.test.ID, unattended.KeyPaused, "true")
	var list SchedulesReply
	sv.call(g.test.ID, "GET", "schedules", nil, http.StatusOK, &list)
	if list.Paused || len(list.Schedules) != 1 || !list.Schedules[0].Held || list.Schedules[0].Next != nil {
		t.Fatalf("with every agent paused the list is %+v", list)
	}
	g.clock.set(utc(t, "2026-11-26T10:10:00Z"))
	sv.ticks(3)
	if runs := sv.read(g.test.ID, one.ID).Runs; len(runs) != 0 {
		t.Fatalf("a schedule ran %d times while every agent was paused", len(runs))
	}

	// As the resume route does: the due times are moved on, then the switch
	// goes off.
	g.with(g.test.ID, func(s *tenant.Store) {
		if err := sv.sched.Rearm(context.Background(), s); err != nil {
			t.Fatal(err)
		}
		if err := unattended.SetPaused(context.Background(), s, false); err != nil {
			t.Fatal(err)
		}
	})
	got := sv.read(g.test.ID, one.ID)
	if got.Schedule.Held || got.Schedule.Next == nil || !got.Schedule.Next.Equal(at(t, lisbon, "2026-11-27 07:00")) {
		t.Fatalf("after every agent was resumed the schedule is %+v", got.Schedule)
	}
	sv.ticks(3)
	if runs := sv.read(g.test.ID, one.ID).Runs; len(runs) != 0 {
		t.Fatalf("a time that passed while every agent was paused was made up: %d runs", len(runs))
	}
}

// A schedule may have a budget of its own for each run; with none, a run's
// budget is the person's agents.schedule_budget_usd. A run is a scheduled
// run in the run registry, with that budget.
func TestAScheduleHasABudgetForEachRun(t *testing.T) {
	g := newRig(t, utc(t, "2026-11-24T09:00:00Z"))
	sv := g.serve(harbourScripts(t))

	five := 5.0
	body := daily("Harbour note", harbourPrompt, "07:00")
	body.BudgetUSD = &five
	made := sv.make(g.test.ID, body)
	if made.BudgetUSD == nil || *made.BudgetUSD != 5 || made.RunBudgetMicros != 5_000_000 {
		t.Fatalf("a schedule with a budget of its own is %+v", made)
	}

	g.set(g.test.ID, unattended.KeyScheduleBudget, "2")
	var changed Schedule
	sv.call(g.test.ID, "PUT", "schedules/"+made.ID, daily("Harbour note", harbourPrompt, "07:00"), http.StatusOK, &changed)
	if changed.BudgetUSD != nil || changed.RunBudgetMicros != 2_000_000 {
		t.Fatalf("a schedule changed to have no budget of its own is %+v", changed)
	}
	minus := -1.0
	bad := daily("Harbour note", harbourPrompt, "07:00")
	bad.BudgetUSD = &minus
	sv.call(g.test.ID, "POST", "schedules", bad, http.StatusBadRequest, nil)

	var run Run
	sv.call(g.test.ID, "POST", "schedules/"+made.ID+"/run", nil, http.StatusCreated, &run)
	sv.settle(g.test.ID, made.ID, 1)
	g.with(g.test.ID, func(s *tenant.Store) {
		task, err := jobs.Read(context.Background(), s, run.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		var kind string
		var budget int64
		if err := s.DB().QueryRow("SELECT kind, budget_micros FROM unattended_runs WHERE ref_id = ?", task.ID).Scan(&kind, &budget); err != nil {
			t.Fatal(err)
		}
		if task.BudgetMicros != 2_000_000 || kind != string(unattended.KindSchedule) || budget != 2_000_000 {
			t.Fatalf("the run's task has a budget of %d, and is registered as a %s with %d", task.BudgetMicros, kind, budget)
		}
	})
}
