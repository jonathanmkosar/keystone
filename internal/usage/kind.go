package usage

import (
	"context"
	"fmt"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// SpentOn is what a tenant has spent in one month on models that are not
// local for one kind of task, such as memory, in millionths of a US
// dollar. A reserve kept for one kind of task, such as the memory pass's
// (P6-06), is measured against it.
func SpentOn(ctx context.Context, s *tenant.Store, month, kind string) (int64, error) {
	if !monthShape.MatchString(month) {
		return 0, fmt.Errorf("usage: %q is not a month such as 2026-11", month)
	}
	var n int64
	err := s.DB().QueryRowContext(ctx,
		"SELECT coalesce(sum(cost_micros), 0) FROM usage_records WHERE month = ? AND kind = ? AND class <> 'local'", month, kind).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("usage: %w", err)
	}
	return n, nil
}
