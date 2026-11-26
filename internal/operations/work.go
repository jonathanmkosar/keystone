package operations

import (
	"context"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

// TaskLine is one background task or scheduled run, as Operations lists it.
type TaskLine struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Scheduled says it is the run of a scheduled task.
	Scheduled bool `json:"scheduled"`
	// Status is queued, running, paused, done, stopped or failed, and
	// Waiting says a call of its round waits for the person's approval.
	Status   string `json:"status"`
	Waiting  bool   `json:"waiting"`
	Progress string `json:"progress,omitempty"`
	// ChatID is the chat it was started from, and WorkChatID its own,
	// where its steps are; for a scheduled run they are the same.
	ChatID     string `json:"chat_id"`
	WorkChatID string `json:"work_chat_id"`
	// TurnID is its round's turn, in WorkChatID.
	TurnID string `json:"turn_id,omitempty"`
	Rounds int    `json:"rounds"`
	Steps  int    `json:"steps"`
	// BudgetMicros and SpentMicros are its budget and what it has spent on
	// models that are not local.
	BudgetMicros int64 `json:"budget_micros"`
	SpentMicros  int64 `json:"spent_micros"`
	// Error is why it failed, for a person, passed through audit.Scrub.
	Error   string     `json:"error,omitempty"`
	Created time.Time  `json:"created"`
	Ended   *time.Time `json:"ended,omitempty"`
	// RoundStarted is when its current round began.
	RoundStarted *time.Time `json:"round_started,omitempty"`
}

// JobsPart is the person's background tasks and scheduled runs.
type JobsPart struct {
	// Paused is the switch that pauses every agent and schedule.
	Paused bool `json:"paused"`
	// Counts is how many tasks are in each state: queued, running, paused,
	// done, stopped and failed, and waiting for the open ones a call of
	// which waits for the person's approval.
	Counts map[string]int `json:"counts"`
	// Open is how many are queued, running or paused.
	Open int `json:"open"`
	// Newest are the newest tasks, up to operations.jobs_shown.
	Newest []TaskLine `json:"newest"`
	// Stuck are the open tasks, not paused and not waiting for the person,
	// whose round began longer ago than operations.stuck_after.
	Stuck      []TaskLine `json:"stuck"`
	StuckAfter string     `json:"stuck_after"`
	// Failed are the tasks that failed within the health window, newest
	// first, up to operations.jobs_shown, and FailedCount how many did.
	Failed      []TaskLine `json:"failed"`
	FailedCount int        `json:"failed_count"`
}

func taskLine(t jobs.Listed) TaskLine {
	l := TaskLine{
		ID: t.ID, Title: t.Title, Scheduled: t.Scheduled, Status: t.Status, Waiting: t.Waiting, Progress: t.Progress,
		ChatID: t.ChatID, WorkChatID: t.WorkChatID, TurnID: t.TurnID, Rounds: t.Rounds, Steps: t.Steps,
		BudgetMicros: t.BudgetMicros, SpentMicros: t.SpentMicros, Created: t.Created, Ended: t.Ended, RoundStarted: t.RoundStarted,
	}
	if t.Error != nil {
		l.Error = audit.Scrub(t.Error.Message)
	}
	return l
}

func lines(ts []jobs.Listed) []TaskLine {
	out := make([]TaskLine, 0, len(ts))
	for _, t := range ts {
		out = append(out, taskLine(t))
	}
	return out
}

// readJobs is the person's tasks by state, the newest, the stuck and the
// ones that failed within the health window.
func readJobs(ctx context.Context, s *tenant.Store, cfg Settings, now time.Time) (JobsPart, error) {
	part := JobsPart{StuckAfter: cfg.StuckAfter.String(), Stuck: []TaskLine{}}
	var err error
	if part.Paused, err = unattended.Paused(ctx, s); err != nil {
		return part, err
	}
	if part.Counts, err = jobs.Tally(ctx, s); err != nil {
		return part, err
	}
	part.Open = part.Counts[jobs.StatusQueued] + part.Counts[jobs.StatusRunning] + part.Counts[jobs.StatusPaused]
	newest, err := jobs.List(ctx, s, jobs.Filter{Limit: cfg.JobsShown})
	if err != nil {
		return part, err
	}
	part.Newest = lines(newest)
	open, err := jobs.List(ctx, s, jobs.Filter{Open: true})
	if err != nil {
		return part, err
	}
	for _, t := range open {
		if t.Status == jobs.StatusPaused || t.Waiting {
			continue
		}
		began := t.Created
		switch {
		case t.RoundStarted != nil:
			began = *t.RoundStarted
		case t.Started != nil:
			began = *t.Started
		}
		if now.Sub(began) > cfg.StuckAfter {
			part.Stuck = append(part.Stuck, taskLine(t))
		}
	}
	failed, err := jobs.List(ctx, s, jobs.Filter{Status: jobs.StatusFailed, EndedSince: now.Add(-cfg.HealthWindow)})
	if err != nil {
		return part, err
	}
	part.FailedCount = len(failed)
	part.Failed = lines(failed[:min(len(failed), cfg.JobsShown)])
	return part, nil
}

// ScheduleLine is one schedule, as Operations lists it.
type ScheduleLine struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Says is its recurrence in words.
	Says string `json:"says"`
	// Enabled is false while the person has paused it, and Held says a
	// switch holds every schedule.
	Enabled bool `json:"enabled"`
	Held    bool `json:"held"`
	// Next is when it is due next, in the person's time zone.
	Next *time.Time `json:"next,omitempty"`
	// LastRun is its newest run.
	LastRun *RunLine `json:"last_run,omitempty"`
}

// RunLine is a schedule's newest run.
type RunLine struct {
	Started time.Time `json:"started"`
	// Status is starting, queued, running, paused, done, stopped, failed
	// or gone.
	Status     string `json:"status"`
	Waiting    bool   `json:"waiting,omitempty"`
	Late       bool   `json:"late,omitempty"`
	LateReason string `json:"late_reason,omitempty"`
	Missed     int    `json:"missed"`
	// Error is why it failed or waits to start, passed through audit.Scrub.
	Error       string `json:"error,omitempty"`
	ChatID      string `json:"chat_id,omitempty"`
	SpentMicros int64  `json:"spent_micros"`
}

// SchedulesPart is the person's schedules.
type SchedulesPart struct {
	// Paused is the schedules' own switch.
	Paused bool `json:"paused"`
	// Zone is the person's time zone in words.
	Zone      string         `json:"zone"`
	Schedules []ScheduleLine `json:"schedules"`
	// Enabled counts the schedules that are not paused, and Failing those
	// whose newest run failed.
	Enabled int `json:"enabled"`
	Failing int `json:"failing"`
}

// readSchedules is the person's schedules as the Scheduled screen lists
// them, through list. A nil list is no schedules.
func readSchedules(ctx context.Context, s *tenant.Store, list func(context.Context, *tenant.Store) (schedules.SchedulesReply, error)) (SchedulesPart, error) {
	part := SchedulesPart{Schedules: []ScheduleLine{}}
	if list == nil {
		return part, nil
	}
	reply, err := list(ctx, s)
	if err != nil {
		return part, err
	}
	part.Paused, part.Zone = reply.Paused, reply.Zone.Label
	for _, sc := range reply.Schedules {
		l := ScheduleLine{ID: sc.ID, Name: sc.Name, Says: sc.Says, Enabled: sc.Enabled, Held: sc.Held, Next: sc.Next}
		if sc.Enabled {
			part.Enabled++
		}
		if r := sc.LastRun; r != nil {
			l.LastRun = &RunLine{Started: r.Started, Status: r.Status, Waiting: r.Waiting, Late: r.Late, LateReason: r.LateReason,
				Missed: r.Missed, Error: audit.Scrub(r.Error), ChatID: r.ChatID, SpentMicros: r.SpentMicros}
			if r.Status == schedules.RunFailed || r.Status == jobs.StatusFailed {
				part.Failing++
			}
		}
		part.Schedules = append(part.Schedules, l)
	}
	return part, nil
}
