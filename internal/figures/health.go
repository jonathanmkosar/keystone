package figures

import (
	"context"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// The states of a model's health over the window.
const (
	// HealthGood is a model whose calls failed less than the watch share.
	HealthGood = "good"
	// HealthErrors is a model whose calls failed at or past the watch share.
	HealthErrors = "errors"
	// HealthFailing is a model every call of which that was sent failed.
	HealthFailing = "failing"
	// HealthRefused is a model whose every call Keystone did not send: at
	// the cap, or too long for its window.
	HealthRefused = "refused"
	// HealthIdle is a model that took no call in the window.
	HealthIdle = "idle"
)

// WindowRecords returns the usage records at or after since and not after
// now, oldest first, month by month through the month index.
func WindowRecords(ctx context.Context, s *tenant.Store, since, now time.Time) ([]usage.Record, error) {
	var out []usage.Record
	first, _ := time.Parse("2006-01", usage.MonthOf(since))
	for m := first; !m.After(now.UTC()); m = m.AddDate(0, 1, 0) {
		recs, err := usage.List(ctx, s, usage.Filter{Month: usage.MonthOf(m)})
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			if !r.At.Before(since) && !r.At.After(now) {
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// HealthState is a model's health from its calls over the window: how many
// there were, how many Keystone refused, how many of the rest failed, and
// the watch share in percent.
func HealthState(calls, refusals, errs, watch int) (state string, errorPercent int) {
	sent := calls - refusals
	if sent > 0 {
		errorPercent = errs * 100 / sent
	}
	switch {
	case calls == 0:
		return HealthIdle, errorPercent
	case sent == 0:
		return HealthRefused, errorPercent
	case errs == sent:
		return HealthFailing, errorPercent
	case errorPercent >= watch:
		return HealthErrors, errorPercent
	}
	return HealthGood, errorPercent
}

// ModelStates counts a person's models by their health over the window:
// each model in their registry, and each other one that took a call in the
// window, as Operations lists them.
func ModelStates(ctx context.Context, s *tenant.Store, cfg Settings, now time.Time) (map[string]int, error) {
	entries, err := models.List(ctx, s)
	if err != nil {
		return nil, err
	}
	recs, err := WindowRecords(ctx, s, now.Add(-cfg.HealthWindow), now)
	if err != nil {
		return nil, err
	}
	type tally struct{ calls, refusals, errs int }
	by := map[string]*tally{}
	for _, e := range entries {
		by[e.Model.Name] = &tally{}
	}
	for _, r := range recs {
		t, ok := by[r.Model]
		if !ok {
			t = &tally{}
			by[r.Model] = t
		}
		t.calls++
		switch r.Outcome {
		case usage.OutcomeError:
			t.errs++
		case usage.OutcomeRefused:
			t.refusals++
		}
	}
	out := map[string]int{}
	for _, t := range by {
		state, _ := HealthState(t.calls, t.refusals, t.errs, cfg.ModelErrorWatch)
		out[state]++
	}
	return out, nil
}
