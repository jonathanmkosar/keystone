package turn

import (
	"context"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// What unattended spend (P8-05, internal/unattended) uses of the runner:
// one hook, asked when a turn's route moves past a model. Nothing here
// changes how a turn runs when the option is not set, and the option
// answers nothing for a turn that is no unattended run's.
// docs/architecture/unattended-spend.md is the whole of it.

// Moved is told when a model of a turn's route did not answer and the next
// is about to be tried: turnID is the turn the call is for, failed the
// model that did not answer and why, and next the model the route goes on
// to. A non-nil answer ends the call with that error, as the call's own
// failure: the failed model's call is recorded with it, and next is never
// called.
type Moved func(ctx context.Context, s *tenant.Store, turnID string, failed routing.Attempt, next models.Model) *seam.Error

// optionMoved is a route's Moved hook for the turn t: Options.Moved first,
// which may end the call, then inner, which records the failed call and
// the start of the next.
func (r *Runner) optionMoved(s *tenant.Store, t row, inner func(context.Context, routing.Attempt, models.Model, seam.Request) error) func(context.Context, routing.Attempt, models.Model, seam.Request) error {
	if r.o.Moved == nil {
		return inner
	}
	return func(ctx context.Context, failed routing.Attempt, next models.Model, req seam.Request) error {
		if e := r.o.Moved(ctx, s, t.ID, failed, next); e != nil {
			return e
		}
		return inner(ctx, failed, next, req)
	}
}
