package memory

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// The memory pass's spend reserve (decision 0280). It is a part of the
// monthly cap, not an addition to it: what the memory pass spends on
// models that are not local counts against both. A memory call is stopped
// when this month's memory spend and the most the call could cost are
// together more than the reserve, so the memory pass stops before it eats
// the cap, and a chat goes on. The monthly cap still stops a memory call,
// as it stops any. A local model costs nothing and is never asked about.

// NewReserve turns the reserve in US dollars, as knowledge.memory_reserve_usd
// holds it, into millionths of a dollar. Zero stops every memory call that
// costs anything.
func NewReserve(usd float64) (int64, error) {
	switch {
	case math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0:
		return 0, errors.New("memory: the reserve must be zero or more US dollars")
	case usd > 1e9:
		return 0, errors.New("memory: the reserve is too large to count in millionths of a dollar")
	}
	return int64(math.Round(usd * 1e6)), nil
}

// Allow asks the reserve about one memory call to a model that is not
// local, at one moment. reserve is in millionths of a dollar, and next is
// the most the call could cost. The answer is usage's: stopped when the
// month's memory spend and next pass the reserve.
func Allow(ctx context.Context, s *tenant.Store, reserve int64, at time.Time, next int64) (usage.Status, error) {
	if reserve < 0 || next < 0 {
		return usage.Status{}, errors.New("memory: an amount cannot be negative")
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
