package app

import (
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/research"
)

// newResearch makes the runner of research tasks (P8-04). Its steps are
// turns and its parts are sub-agents, so it is made after the sub-agents'
// runner, bound to the turn runner once that is made, and its goroutines
// run under the turn runner, which stops them and waits for them.
func (c Config) newResearch(log *api.Log) *research.Runner {
	return research.NewRunner(research.Options{Install: c.Install, Log: log, Config: c.Research, Turns: c.Turns,
		Subagents: c.subagents, Artifacts: c.Artifacts, PartChars: c.Subagents.ResultChars})
}
