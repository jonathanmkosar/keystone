package jobs

import (
	"context"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

// Gate is a background task's budget, as the turn runner's gate
// (turn.Options.Gate). The turn runner asks it before each call to a model
// that is not local, after the monthly cap. A turn sent under a task's
// request id is that task's, and the call is not sent when the task's
// spend so far and the most the call could cost are together more than
// its budget. Any other turn is let through.
//
// Since P8-05 it is unattended.Gate, which does the same for every kind of
// unattended run in the run registry: a task, a scheduled run and, once
// P8-02 registers them, a sub-agent. The answer is the seam's spend_cap
// error, so a route moves on to the next model, and unattended.Moved
// decides whether the run may go on with it.
func Gate(ctx context.Context, s *tenant.Store, turnID string, m models.Model, mostMicros int64) *seam.Error {
	return unattended.Gate(ctx, s, turnID, m, mostMicros)
}

// Spent is what a task has spent on models that are not local, from the
// usage records of its request id: every model call it made, the ones that
// failed and the summaries of its chat among them.
func Spent(ctx context.Context, s *tenant.Store, requestID string) (int64, error) {
	return unattended.Spent(ctx, s, requestID)
}
