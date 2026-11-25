package app

import (
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/schedules"
)

// newSchedules makes the scheduler of scheduled tasks (P8-03). Each run is
// a background task, so it is made after the task runner, and its
// goroutine runs under the turn runner: Handler starts it after the tasks
// a stopped server left have gone on, and closing the turn runner stops it.
func (c Config) newSchedules(log *api.Log) *schedules.Scheduler {
	return schedules.New(schedules.Options{Install: c.Install, Log: log, Config: c.Schedules, Jobs: c.jobs, Limits: c.Turns.Limits, Now: c.Clock})
}
