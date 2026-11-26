package embedding

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// The embedding reserve (decision 0412), as the memory pass has one (0280).
// It is a part of the monthly cap, not an addition to it: what embeddings
// cost on models that are not local counts against both. An embedding call
// is stopped when this month's embedding spend and the most the call could
// cost are together more than the reserve, so making vectors never eats the
// cap, and the monthly cap still stops it as it stops any call. A local
// model costs nothing and is never asked about.

// NewReserve turns the reserve in US dollars, as
// knowledge.embedding_reserve_usd holds it, into millionths of a dollar.
// Zero stops every embedding call that costs anything.
func NewReserve(usd float64) (int64, error) {
	switch {
	case math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0:
		return 0, errors.New("embedding: the reserve must be zero or more US dollars")
	case usd > 1e9:
		return 0, errors.New("embedding: the reserve is too large to count in millionths of a dollar")
	}
	return int64(math.Round(usd * 1e6)), nil
}

// Allow asks the reserve about one embedding call to a model that is not
// local, at one moment. reserve is in millionths of a dollar, and next is
// the most the call could cost. The answer is usage's: stopped when the
// month's embedding spend and next pass the reserve.
func Allow(ctx context.Context, s *tenant.Store, reserve int64, at time.Time, next int64) (usage.Status, error) {
	if reserve < 0 || next < 0 {
		return usage.Status{}, errors.New("embedding: an amount cannot be negative")
	}
	month := usage.MonthOf(at)
	spent, err := usage.SpentOn(ctx, s, month, Kind)
	if err != nil {
		return usage.Status{}, err
	}
	st := usage.Judge(spent, next, usage.Cap{LimitMicros: reserve, WarnPercent: 100})
	st.Month = month
	return st, nil
}
