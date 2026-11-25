package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Gate is a background task's budget, as the turn runner's gate
// (turn.Options.Gate). The turn runner asks it before each call to a model
// that is not local, after the monthly cap. A turn sent under a task's
// request id is that task's, and the call is not sent when the task's
// spend so far and the most the call could cost are together more than
// its budget. Any other turn is let through.
//
// The answer is the seam's spend_cap error, so a route falls back to a
// local model when the person's chain has one, and otherwise the round
// fails with the code spend_cap and this message, and the task ends with
// it.
func Gate(ctx context.Context, s *tenant.Store, turnID string, m models.Model, mostMicros int64) *seam.Error {
	var title, requestID string
	var budget int64
	err := s.DB().QueryRowContext(ctx, `SELECT j.title, j.request_id, j.budget_micros FROM jobs j
		JOIN turns t ON t.request_id = j.request_id AND t.chat_id = j.work_chat_id WHERE t.id = ?`, turnID).Scan(&title, &requestID, &budget)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return &seam.Error{Kind: seam.ErrSpendCap, Message: "The background task's budget could not be read, so the call was not sent."}
	}
	spent, err := Spent(ctx, s, requestID)
	if err != nil {
		return &seam.Error{Kind: seam.ErrSpendCap, Message: "What the background task has spent could not be read, so the call was not sent."}
	}
	if spent+mostMicros > budget {
		return &seam.Error{Kind: seam.ErrSpendCap, Message: fmt.Sprintf(
			"The call to %s was not sent. The background task “%s” has spent %s of its budget of %s, and this call could cost up to %s.",
			m.Name, title, usage.Dollars(spent), usage.Dollars(budget), usage.Dollars(mostMicros))}
	}
	return nil
}

// Spent is what a task has spent on models that are not local, from the
// usage records of its request id: every model call it made, the ones that
// failed and the summaries of its chat among them.
func Spent(ctx context.Context, s *tenant.Store, requestID string) (int64, error) {
	recs, err := usage.List(ctx, s, usage.Filter{RequestID: requestID})
	if err != nil {
		return 0, err
	}
	var n int64
	for _, r := range recs {
		if usage.Counts(r.Class) {
			n += r.CostMicros
		}
	}
	return n, nil
}
