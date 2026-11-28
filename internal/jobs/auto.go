package jobs

import (
	"context"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Auto for unattended work (P14-06). A task's work chat, and a scheduled
// run's own chat, start on Auto when the call that started the work named
// no model and the person's ladder has a step: the task's rounds then name
// no model, so the router routes each afresh (turn.Runner.StartOnAuto).
// A task cannot name a model, so it always starts on Auto when Auto is
// offered; a schedule that names one keeps it. Off Auto, the work names
// the model it did before Auto. docs/architecture/unattended-auto.md.

// onAuto puts a chat this runner just made, or a run's own chat, on Auto
// when named is "" and Auto is offered, and says whether it did. With no
// turn runner bound it does nothing.
func (r *Runner) onAuto(ctx context.Context, s *tenant.Store, chatID, named string) (bool, error) {
	t := r.bound()
	if t == nil {
		return false, nil
	}
	return t.StartOnAuto(ctx, s, chatID, named)
}
