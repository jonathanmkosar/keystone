package turn

import (
	"context"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// What sub-agents (P8-02, internal/subagents) use of the runner: one
// option, asked for the step limit of a chat's turns. A sub-agent works
// through one ordinary turn of a chat of its own, so it is built on turns
// and not beside them, as a background task is (jobs.go); its turn has
// agents.subagent_max_steps in place of chats.turn_max_steps. Nothing here
// changes how a turn runs when the option is not set.
// docs/architecture/sub-agents.md is the whole of it.

// maxSteps is the most model calls one turn of the chat makes:
// Options.MaxSteps when it answers 1 or more, and chats.turn_max_steps
// otherwise.
func (r *Runner) maxSteps(ctx context.Context, s *tenant.Store, chatID string) int {
	if r.o.MaxSteps != nil {
		if n := r.o.MaxSteps(ctx, s, chatID); n >= 1 {
			return n
		}
	}
	return r.o.Config.MaxSteps
}
