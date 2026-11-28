package subagents

import (
	"context"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Auto for unattended work (P14-06). A sub-agent whose call names no model
// works in a chat on Auto when the person's ladder has a step: its turn
// names no model, so the router routes it, and a held parent's sub-agent,
// held by carry.Down before its turn runs, is given only steps that may
// read sensitive content (decision 0483). One that names a model keeps it.
// Off Auto, a sub-agent with no model has the parent chat's, as before.
// The parts of a research task follow the same rule through StartParts.
// docs/architecture/unattended-auto.md.

// onAuto puts a sub-agent's chat on Auto when named is "" and Auto is
// offered, and says whether it did. With no turn runner bound it does
// nothing.
func (r *Runner) onAuto(ctx context.Context, s *tenant.Store, chatID, named string) (bool, error) {
	t := r.bound()
	if t == nil {
		return false, nil
	}
	return t.StartOnAuto(ctx, s, chatID, named)
}
