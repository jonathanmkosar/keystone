package app

import (
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/wikijobs"
)

// newWiki makes the wiki upkeep jobs' runner (P8-06). Each job is a
// schedule of its own, so it is made after the scheduler; its goroutine
// runs under the turn runner, which writes its notes.
func (c Config) newWiki(log *api.Log) *wikijobs.Runner {
	return wikijobs.New(wikijobs.Options{Install: c.Install, Log: log, Config: c.WikiJobs, Schedules: c.schedules})
}
