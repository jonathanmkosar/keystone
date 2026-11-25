package app

import (
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/jobs"
)

// newJobs makes the runner of background tasks (P8-01). Its tasks' rounds
// are turns, so it is bound to the turn runner once that is made, its
// goroutines run under the turn runner, and closing the turn runner stops
// them and waits for them.
func (c Config) newJobs(log *api.Log) *jobs.Runner {
	return jobs.NewRunner(jobs.Options{Install: c.Install, Log: log, Config: c.Jobs, Turns: c.Turns})
}
