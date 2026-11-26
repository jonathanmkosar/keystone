package unattended

import (
	"context"
	"fmt"
)

// SetBudget changes the budget of a run that is registered (P8-04). A
// research task is one sum shared between its own turns and its parts,
// each a run of its own: it lowers its own run's budget by what it gives
// its parts before they start, and raises it by what they did not spend
// once they have ended, so the runs together can never spend more than the
// task's budget. The gate reads the budget at every call, so the change
// holds from the next call on. A run that is not registered is refused.
func SetBudget(ctx context.Context, q Execer, requestID string, micros int64) error {
	if micros < 0 {
		return fmt.Errorf("%w: a budget cannot be negative", ErrInvalid)
	}
	res, err := q.ExecContext(ctx, "UPDATE unattended_runs SET budget_micros = ? WHERE request_id = ?", micros, requestID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: no run is registered under that request id", ErrInvalid)
	}
	return nil
}
