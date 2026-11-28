package figures

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Total is what some model calls add up to: calls, tokens and cost, the
// part of the cost that counts against the monthly cap, and how many of
// the calls failed or were refused.
type Total struct {
	usage.Total
	// CappedMicros is what was spent on models that are not local.
	CappedMicros int64 `json:"capped_micros"`
	Errors       int   `json:"errors"`
	Refusals     int   `json:"refusals"`
}

func (t *Total) add(r usage.Record) {
	t.Calls++
	t.InputTokens += int64(r.InputTokens)
	t.OutputTokens += int64(r.OutputTokens)
	t.CacheReadTokens += int64(r.CacheReadTokens)
	t.CacheWriteTokens += int64(r.CacheWriteTokens)
	t.CostMicros += r.CostMicros
	if usage.Counts(r.Class) {
		t.CappedMicros += r.CostMicros
	}
	switch r.Outcome {
	case usage.OutcomeError:
		t.Errors++
	case usage.OutcomeRefused:
		t.Refusals++
	}
}

// Merge adds another total to this one.
func (t *Total) Merge(o Total) {
	t.Calls += o.Calls
	t.InputTokens += o.InputTokens
	t.OutputTokens += o.OutputTokens
	t.CacheReadTokens += o.CacheReadTokens
	t.CacheWriteTokens += o.CacheWriteTokens
	t.CostMicros += o.CostMicros
	t.CappedMicros += o.CappedMicros
	t.Errors += o.Errors
	t.Refusals += o.Refusals
}

// Day is one day of calls, in UTC.
type Day struct {
	// Day is the date, as 2026-11-28.
	Day string `json:"day"`
	Total
}

// ModelUse is one model's part of a period: its registry name and its
// class when the calls were made. A model whose class changed has a line
// for each, as in the usage report.
type ModelUse struct {
	Model string       `json:"model"`
	Class models.Class `json:"class"`
	Total
}

// Use is a period's model calls, summed three ways that each add up to the
// total.
type Use struct {
	Total  Total      `json:"total"`
	Days   []Day      `json:"days"`
	Models []ModelUse `json:"models"`
}

// Merge adds another period's use to this one, day by day and model by
// model: the install's use is the sum of each person's.
func (u *Use) Merge(o Use) {
	u.Total.Merge(o.Total)
	days := map[string]int{}
	for i, d := range u.Days {
		days[d.Day] = i
	}
	for _, d := range o.Days {
		if i, ok := days[d.Day]; ok {
			u.Days[i].Merge(d.Total)
		} else {
			days[d.Day] = len(u.Days)
			u.Days = append(u.Days, d)
		}
	}
	type key struct {
		model string
		class models.Class
	}
	ms := map[key]int{}
	for i, m := range u.Models {
		ms[key{m.Model, m.Class}] = i
	}
	for _, m := range o.Models {
		if i, ok := ms[key{m.Model, m.Class}]; ok {
			u.Models[i].Merge(m.Total)
		} else {
			ms[key{m.Model, m.Class}] = len(u.Models)
			u.Models = append(u.Models, m)
		}
	}
	u.sort()
}

func (u *Use) sort() {
	slices.SortFunc(u.Days, func(a, b Day) int { return cmp.Compare(a.Day, b.Day) })
	slices.SortFunc(u.Models, func(a, b ModelUse) int {
		return cmp.Or(cmp.Compare(b.CostMicros, a.CostMicros), cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Model, b.Model), cmp.Compare(a.Class, b.Class))
	})
}

// Usage sums a person's model calls made at or after from and before to:
// the total, by day in UTC (oldest first) and by model and class
// (costliest first). It reads the usage records, which hold no words.
func Usage(ctx context.Context, s *tenant.Store, from, to time.Time) (Use, error) {
	out := Use{Days: []Day{}, Models: []ModelUse{}}
	if !to.After(from) {
		return out, nil
	}
	days := map[string]*Day{}
	type key struct {
		model string
		class models.Class
	}
	byModel := map[key]*ModelUse{}
	start := time.Date(from.UTC().Year(), from.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	for m := start; m.Before(to); m = m.AddDate(0, 1, 0) {
		recs, err := usage.List(ctx, s, usage.Filter{Month: usage.MonthOf(m)})
		if err != nil {
			return Use{}, err
		}
		for _, r := range recs {
			if r.At.Before(from) || !r.At.Before(to) {
				continue
			}
			out.Total.add(r)
			day := r.At.UTC().Format("2006-01-02")
			d, ok := days[day]
			if !ok {
				d = &Day{Day: day}
				days[day] = d
			}
			d.add(r)
			k := key{r.Model, r.Class}
			mu, ok := byModel[k]
			if !ok {
				mu = &ModelUse{Model: r.Model, Class: r.Class}
				byModel[k] = mu
			}
			mu.add(r)
		}
	}
	for _, d := range days {
		out.Days = append(out.Days, *d)
	}
	for _, m := range byModel {
		out.Models = append(out.Models, *m)
	}
	out.sort()
	return out, nil
}

// The kinds of failure a period counts. None carries its words.
const (
	// FailModelError is a model call that failed.
	FailModelError = "model_error"
	// FailModelRefused is a model call Keystone did not send: at the cap,
	// a run's budget, or too long for the model's window.
	FailModelRefused = "model_refused"
	// FailTask is a background task or scheduled run that failed.
	FailTask = "task_failed"
	// FailScheduleStart is a schedule's run that could not start.
	FailScheduleStart = "schedule_start_failed"
)

// Failures counts a person's failures at or after from and before to, by
// kind. The model calls' are the period's records'; the tasks' and the
// schedules' are counted from their rows, by when they ended or were made.
func Failures(ctx context.Context, s *tenant.Store, use Use, from, to time.Time) (map[string]int, error) {
	out := map[string]int{FailModelError: use.Total.Errors, FailModelRefused: use.Total.Refusals}
	var tasks, starts int
	if err := s.DB().QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE status = 'failed' AND ended_at <> '' AND ended_at >= ? AND ended_at < ?`,
		stamp(from), stamp(to)).Scan(&tasks); err != nil {
		return nil, err
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT count(*) FROM schedule_runs WHERE state = 'failed' AND created_at >= ? AND created_at < ?`,
		stamp(from), stamp(to)).Scan(&starts); err != nil {
		return nil, err
	}
	out[FailTask], out[FailScheduleStart] = tasks, starts
	return out, nil
}

// StoragePoint is where a person's database, files and artifacts stood at
// one snapshot of their limits.
type StoragePoint struct {
	At        time.Time `json:"at"`
	Database  int64     `json:"database"`
	Files     int64     `json:"files"`
	Artifacts int64     `json:"artifacts"`
}

// Storage is a person's storage over time, from the snapshots Operations'
// sampler keeps (operations.snapshot_every), at or after from and before
// to, oldest first.
func Storage(ctx context.Context, s *tenant.Store, from, to time.Time) ([]StoragePoint, error) {
	rows, err := s.DB().QueryContext(ctx, `SELECT at, key, used FROM operations_snapshots
		WHERE key IN ('database', 'files', 'artifacts') AND at >= ? AND at < ? ORDER BY at`, stamp(from), stamp(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StoragePoint{}
	for rows.Next() {
		var at, key string
		var used int64
		if err := rows.Scan(&at, &key, &used); err != nil {
			return nil, err
		}
		t, err := time.Parse(timeLayout, at)
		if err != nil {
			return nil, err
		}
		if len(out) == 0 || !out[len(out)-1].At.Equal(t) {
			out = append(out, StoragePoint{At: t})
		}
		p := &out[len(out)-1]
		switch key {
		case LimitDatabase:
			p.Database = used
		case LimitFiles:
			p.Files = used
		case LimitArtifacts:
			p.Artifacts = used
		}
	}
	return out, rows.Err()
}
