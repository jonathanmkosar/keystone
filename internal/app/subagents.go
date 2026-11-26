package app

import (
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/subagents"
)

// newSubagents makes the sub-agents' runner (P8-02). It is made before the
// turn runner, so run_subagents can be added to the tools, and bound to the
// turn runner after: each call's supervisor runs under it.
func (c Config) newSubagents(log *api.Log) *subagents.Runner {
	return subagents.NewRunner(subagents.Options{Install: c.Install, Log: log, Config: c.Subagents, Turns: c.Turns})
}
