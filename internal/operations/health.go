package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"sort"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/figures"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// The states of a model's health over the window: internal/figures' own.
const (
	HealthGood    = figures.HealthGood
	HealthErrors  = figures.HealthErrors
	HealthFailing = figures.HealthFailing
	HealthRefused = figures.HealthRefused
	HealthIdle    = figures.HealthIdle
)

// CallError is a model call that failed, as a person may read it: its kind
// and its words, passed through audit.Scrub, so never a key.
type CallError struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
}

// ModelHealth is how one model's calls went over the window, from the
// usage records, with its last error from the audit log.
type ModelHealth struct {
	// Name is its registry name, and Label what the picker shows.
	Name  string `json:"name"`
	Label string `json:"label"`
	// Class is local, hosted or trusted, and Where its adapter and the
	// host it calls, never a key or a path.
	Class string `json:"class"`
	Where string `json:"where,omitempty"`
	// InRegistry is false for a model that took calls in the window and
	// has left the person's registry since.
	InRegistry bool `json:"in_registry"`
	// Problem says why its registry entry is refused today.
	Problem string `json:"problem,omitempty"`
	// The calls in the window by how they ended.
	Calls    int `json:"calls"`
	OK       int `json:"ok"`
	Errors   int `json:"errors"`
	Refusals int `json:"refusals"`
	Stopped  int `json:"stopped"`
	// ErrorPercent is the share of the calls that were sent that failed.
	ErrorPercent int `json:"error_percent"`
	// MedianMS is the median time of the calls that were sent, in
	// milliseconds, from the start of the call to its end.
	MedianMS   int64 `json:"median_ms"`
	CostMicros int64 `json:"cost_micros"`
	// State is good, errors, failing, refused or idle.
	State string `json:"state"`
	// LastError is its newest failed call in the window.
	LastError *CallError `json:"last_error,omitempty"`
}

// ModelsPart is model health over a window.
type ModelsPart struct {
	Since  time.Time     `json:"since"`
	Window string        `json:"window"`
	Models []ModelHealth `json:"models"`
}

// windowRecords returns the usage records at or after since, oldest first:
// figures.WindowRecords.
func windowRecords(ctx context.Context, s *tenant.Store, since, now time.Time) ([]usage.Record, error) {
	return figures.WindowRecords(ctx, s, since, now)
}

// readHealth is every model in the person's registry, and any other that
// took a call in the window, with its calls over the window.
func readHealth(ctx context.Context, s *tenant.Store, cfg Settings, now time.Time) (ModelsPart, error) {
	since := now.Add(-cfg.HealthWindow)
	part := ModelsPart{Since: since.UTC(), Window: cfg.HealthWindow.String(), Models: []ModelHealth{}}
	entries, err := models.List(ctx, s)
	if err != nil {
		return part, err
	}
	recs, err := windowRecords(ctx, s, since, now)
	if err != nil {
		return part, err
	}
	byName := map[string]*ModelHealth{}
	var order []string
	for _, e := range entries {
		m := &ModelHealth{Name: e.Model.Name, Label: e.Model.Label, Class: string(e.Model.Class), Where: where(e.Model), InRegistry: true, Problem: e.Problem}
		if m.Label == "" {
			m.Label = m.Name
		}
		byName[m.Name] = m
		order = append(order, m.Name)
	}
	times := map[string][]int64{}
	lastErr := map[string]usage.Record{}
	for _, r := range recs {
		m, ok := byName[r.Model]
		if !ok {
			m = &ModelHealth{Name: r.Model, Label: r.Model, Class: string(r.Class)}
			byName[r.Model] = m
			order = append(order, r.Model)
		}
		m.Calls++
		m.CostMicros += r.CostMicros
		switch r.Outcome {
		case usage.OutcomeOK:
			m.OK++
		case usage.OutcomeError:
			m.Errors++
			lastErr[r.Model] = r
		case usage.OutcomeRefused:
			m.Refusals++
		case usage.OutcomeStopped:
			m.Stopped++
		}
		if r.Outcome != usage.OutcomeRefused {
			times[r.Model] = append(times[r.Model], r.DurationMS)
		}
	}
	for _, name := range order {
		m := byName[name]
		m.MedianMS = median(times[name])
		m.State, m.ErrorPercent = figures.HealthState(m.Calls, m.Refusals, m.Errors, cfg.ModelErrorWatch)
		if r, ok := lastErr[name]; ok {
			if m.LastError, err = callError(ctx, s.DB(), r); err != nil {
				return part, err
			}
		}
		part.Models = append(part.Models, *m)
	}
	return part, nil
}

// callError reads a failed call's kind and words from the audit entry that
// ended it, under the record's request id, and scrubs them. A record with
// no such entry says only that the call failed.
func callError(ctx context.Context, db *sql.DB, r usage.Record) (*CallError, error) {
	out := &CallError{At: r.At, Kind: "other", Message: "The call failed. The audit log holds no words for it."}
	if r.RequestID == "" {
		return out, nil
	}
	var received string
	err := db.QueryRowContext(ctx, `SELECT received FROM audit_log
		WHERE request_id = ? AND kind = 'model' AND action = ? AND outcome = 'error' ORDER BY id DESC LIMIT 1`, r.RequestID, r.Model).Scan(&received)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var said struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(received), &said) == nil {
		if said.Kind != "" {
			out.Kind = audit.Scrub(said.Kind)
		}
		if said.Message != "" {
			out.Message = audit.Scrub(said.Message)
		}
	}
	return out, nil
}

// where is a model's adapter and the host it calls: no scheme, path, query
// or user, so nothing that could carry a key.
func where(m models.Model) string {
	if m.Endpoint == "" {
		return m.Adapter
	}
	u, err := url.Parse(m.Endpoint)
	if err != nil || u.Hostname() == "" {
		return m.Adapter
	}
	return m.Adapter + " · " + u.Hostname()
}

// median is the middle of some times, or the mean of the two in the middle.
func median(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	v = slices.Clone(v)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	if len(v)%2 == 1 {
		return v[len(v)/2]
	}
	return (v[len(v)/2-1] + v[len(v)/2]) / 2
}
