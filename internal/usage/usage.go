// Package usage keeps one record for each model call a tenant made, in the
// tenant's own database, and holds the monthly spend cap.
//
// A record says when the call was made, what kind of task it was for,
// which model took it and that model's class, how it ended, its tokens,
// what it cost in millionths of a US dollar, and how long it took. Records
// are added and read, and never changed or removed.
//
// The cap is on what a tenant spends in a calendar month on models that
// are not local. Allow compares what was spent, and the most the next call
// could cost, with the cap, and answers ok, warning or stopped.
//
// The package reads no setting. The cap and the threshold are arguments,
// and a caller reads them from settings. docs/architecture/usage.md
// describes it.
package usage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Outcome says how a call ended.
type Outcome string

const (
	// OutcomeOK is a call that gave a whole reply.
	OutcomeOK Outcome = "ok"
	// OutcomeStopped is a call the person stopped part of the way.
	OutcomeStopped Outcome = "stopped"
	// OutcomeRefused is a call Keystone did not send: over the cap, or too
	// long for the model's window.
	OutcomeRefused Outcome = "refused"
	// OutcomeError is a call that failed.
	OutcomeError Outcome = "error"
)

// Record is one model call.
type Record struct {
	// ID is set by Add. At is the time of the call's end: zero means now.
	ID int64     `json:"id"`
	At time.Time `json:"at"`
	// RequestID is the id of the API call that set the model call off, the
	// same one its audit entries carry.
	RequestID string `json:"request_id,omitempty"`
	// Kind is the kind of task, such as chat or title.
	Kind string `json:"kind"`
	// Model is the registry name of the model that took the call, and
	// Class its class when it did.
	Model   string       `json:"model"`
	Class   models.Class `json:"class"`
	Outcome Outcome      `json:"outcome"`

	// The token counts are the seam's: the first four do not overlap, and
	// thinking is part of the output.
	InputTokens      int  `json:"input_tokens"`
	OutputTokens     int  `json:"output_tokens"`
	CacheReadTokens  int  `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int  `json:"cache_write_tokens,omitempty"`
	ThinkingTokens   int  `json:"thinking_tokens,omitempty"`
	Estimated        bool `json:"estimated,omitempty"`

	// CostMicros is what the call cost, in millionths of a US dollar.
	CostMicros int64 `json:"cost_micros"`
	// DurationMS is how long the call took, in milliseconds.
	DurationMS int64 `json:"duration_ms"`
}

// timeLayout sorts as text in the order of time, and its first seven
// characters are the month.
const timeLayout = "2006-01-02T15:04:05.000Z"

var monthShape = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

// MonthOf returns the calendar month a time falls in, in UTC, as 2026-11.
// A month begins and the cap starts again at midnight UTC on the first.
func MonthOf(t time.Time) string { return t.UTC().Format("2006-01") }

// Validate checks a record before it is written.
func Validate(r Record) error {
	switch r.Outcome {
	case OutcomeOK, OutcomeStopped, OutcomeRefused, OutcomeError:
	default:
		return fmt.Errorf("usage: outcome %q is not ok, stopped, refused or error", r.Outcome)
	}
	switch r.Class {
	case models.ClassLocal, models.ClassHosted, models.ClassTrusted:
	default:
		return fmt.Errorf("usage: class %q is not local, hosted or trusted", r.Class)
	}
	switch {
	case strings.TrimSpace(r.Kind) == "":
		return errors.New("usage: a record needs the kind of task")
	case strings.TrimSpace(r.Model) == "":
		return errors.New("usage: a record needs a model")
	case r.InputTokens < 0 || r.OutputTokens < 0 || r.CacheReadTokens < 0 || r.CacheWriteTokens < 0 || r.ThinkingTokens < 0:
		return errors.New("usage: a token count cannot be negative")
	case r.CostMicros < 0:
		return errors.New("usage: a cost cannot be negative")
	case r.DurationMS < 0:
		return errors.New("usage: a duration cannot be negative")
	}
	return nil
}

// now is the clock, so a test can set it.
var now = time.Now

// Add writes one record and returns it as written, with its id and time.
func Add(ctx context.Context, s *tenant.Store, r Record) (Record, error) {
	if err := Validate(r); err != nil {
		return Record{}, err
	}
	if r.At.IsZero() {
		r.At = now()
	}
	r.At = r.At.UTC().Truncate(time.Millisecond)
	res, err := s.DB().ExecContext(ctx, `INSERT INTO usage_records(
			at, month, request_id, kind, model, class, outcome,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			thinking_tokens, tokens_estimated, cost_micros, duration_ms)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.At.Format(timeLayout), MonthOf(r.At), r.RequestID, r.Kind, r.Model, string(r.Class), string(r.Outcome),
		r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens,
		r.ThinkingTokens, r.Estimated, r.CostMicros, r.DurationMS)
	if err != nil {
		return Record{}, fmt.Errorf("usage: %w", err)
	}
	if r.ID, err = res.LastInsertId(); err != nil {
		return Record{}, fmt.Errorf("usage: %w", err)
	}
	return r, nil
}

// Filter narrows List. The zero Filter lists every record.
type Filter struct {
	// Month is a calendar month as 2026-11. Empty means every month.
	Month string
	// RequestID is the id of one API call. Empty means every call.
	RequestID string
	// Limit is the most records to return, the newest ones. Zero means all.
	Limit int
}

const listColumns = `SELECT id, at, request_id, kind, model, class, outcome, input_tokens, output_tokens,
		cache_read_tokens, cache_write_tokens, thinking_tokens, tokens_estimated, cost_micros, duration_ms
		FROM usage_records`

// listWhere is the conditions of a filter, for List's query: a month, a
// request id, both or neither.
func listWhere(f Filter) string {
	switch {
	case f.Month != "" && f.RequestID != "":
		return " WHERE month = ? AND request_id = ?"
	case f.Month != "":
		return " WHERE month = ?"
	case f.RequestID != "":
		return " WHERE request_id = ?"
	}
	return ""
}

// List returns a tenant's records, oldest first.
func List(ctx context.Context, s *tenant.Store, f Filter) ([]Record, error) {
	if f.Month != "" && !monthShape.MatchString(f.Month) {
		return nil, fmt.Errorf("usage: %q is not a month such as 2026-11", f.Month)
	}
	// Only the conditions that are set are in the query, so the database
	// finds a month's or a request's records by their index. One query for
	// both, with "? = '' OR ...", reads every record a person has (P13-03).
	q := listColumns + listWhere(f) + " ORDER BY id DESC"
	var args []any
	if f.Month != "" {
		args = append(args, f.Month)
	}
	if f.RequestID != "" {
		args = append(args, f.RequestID)
	}
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("usage: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		var at, class, outcome string
		if err := rows.Scan(&r.ID, &at, &r.RequestID, &r.Kind, &r.Model, &class, &outcome, &r.InputTokens, &r.OutputTokens,
			&r.CacheReadTokens, &r.CacheWriteTokens, &r.ThinkingTokens, &r.Estimated, &r.CostMicros, &r.DurationMS); err != nil {
			return nil, fmt.Errorf("usage: %w", err)
		}
		if r.At, err = time.Parse(timeLayout, at); err != nil {
			return nil, fmt.Errorf("usage: record %d has a time that cannot be read", r.ID)
		}
		r.Class, r.Outcome = models.Class(class), Outcome(outcome)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usage: %w", err)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Total is the sum of some records.
type Total struct {
	Calls            int   `json:"calls"`
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	CostMicros       int64 `json:"cost_micros"`
}

// ModelTotal is one model's part of a month.
type ModelTotal struct {
	Model string       `json:"model"`
	Class models.Class `json:"class"`
	Total
}

// Summary is one tenant's calendar month.
type Summary struct {
	Month string `json:"month"`
	Total
	// CappedMicros is the part of CostMicros that counts against the cap:
	// what was spent on models that are not local.
	CappedMicros int64 `json:"capped_micros"`
	// Models is the month by model and class, in order of name. A model
	// whose class was changed during the month has a line for each.
	Models []ModelTotal `json:"models"`
}

// Month sums a tenant's records for one calendar month, given as 2026-11.
func Month(ctx context.Context, s *tenant.Store, month string) (Summary, error) {
	if !monthShape.MatchString(month) {
		return Summary{}, fmt.Errorf("usage: %q is not a month such as 2026-11", month)
	}
	rows, err := s.DB().QueryContext(ctx, `SELECT model, class, count(*), sum(input_tokens), sum(output_tokens),
			sum(cache_read_tokens), sum(cache_write_tokens), sum(cost_micros)
		FROM usage_records WHERE month = ? GROUP BY model, class ORDER BY model, class`, month)
	if err != nil {
		return Summary{}, fmt.Errorf("usage: %w", err)
	}
	defer rows.Close()
	out := Summary{Month: month, Models: []ModelTotal{}}
	for rows.Next() {
		var m ModelTotal
		var class string
		if err := rows.Scan(&m.Model, &class, &m.Calls, &m.InputTokens, &m.OutputTokens, &m.CacheReadTokens, &m.CacheWriteTokens, &m.CostMicros); err != nil {
			return Summary{}, fmt.Errorf("usage: %w", err)
		}
		m.Class = models.Class(class)
		out.Models = append(out.Models, m)
		out.Calls += m.Calls
		out.InputTokens += m.InputTokens
		out.OutputTokens += m.OutputTokens
		out.CacheReadTokens += m.CacheReadTokens
		out.CacheWriteTokens += m.CacheWriteTokens
		out.CostMicros += m.CostMicros
		if Counts(m.Class) {
			out.CappedMicros += m.CostMicros
		}
	}
	if err := rows.Err(); err != nil {
		return Summary{}, fmt.Errorf("usage: %w", err)
	}
	return out, nil
}

// Counts reports whether a call to a model of this class counts against
// the monthly cap. A local model does not: the cap is on what is paid to
// someone else.
func Counts(class models.Class) bool { return class != models.ClassLocal }

// Spent is what a tenant has spent in one month on models that are not
// local, in millionths of a US dollar.
func Spent(ctx context.Context, s *tenant.Store, month string) (int64, error) {
	if !monthShape.MatchString(month) {
		return 0, fmt.Errorf("usage: %q is not a month such as 2026-11", month)
	}
	var n int64
	err := s.DB().QueryRowContext(ctx, "SELECT coalesce(sum(cost_micros), 0) FROM usage_records WHERE month = ? AND class <> 'local'", month).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("usage: %w", err)
	}
	return n, nil
}

// Cap is a tenant's monthly spend cap. A caller reads it from settings.
type Cap struct {
	// LimitMicros is the most a tenant may spend in a calendar month on
	// models that are not local, in millionths of a US dollar. Zero stops
	// every call that costs anything.
	LimitMicros int64
	// WarnPercent is the share of the limit at which the answer becomes a
	// warning, from 0 to 100.
	WarnPercent int
}

// NewCap makes a cap from a limit in US dollars and a warning threshold in
// percent, which is how the settings hold them.
func NewCap(usd float64, warnPercent int) (Cap, error) {
	switch {
	case math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0:
		return Cap{}, errors.New("usage: the monthly cap must be zero or more US dollars")
	case usd > 1e9:
		return Cap{}, errors.New("usage: the monthly cap is too large to count in millionths of a dollar")
	case warnPercent < 0 || warnPercent > 100:
		return Cap{}, errors.New("usage: the warning threshold is a percentage from 0 to 100")
	}
	return Cap{LimitMicros: int64(math.Round(usd * 1e6)), WarnPercent: warnPercent}, nil
}

// State is the cap's answer for one call.
type State string

const (
	// StateOK: the call may be made.
	StateOK State = "ok"
	// StateWarning: the call may be made, and the month's spend is at or
	// past the warning threshold.
	StateWarning State = "warning"
	// StateStopped: the call would take the month past the cap, and must
	// not be made.
	StateStopped State = "stopped"
)

// Status is where a tenant's month stands against the cap, for one call.
type Status struct {
	State State  `json:"state"`
	Month string `json:"month"`
	// SpentMicros is what the month's calls to models that are not local
	// have cost so far.
	SpentMicros int64 `json:"spent_micros"`
	// NextMicros is the most the call being asked about could cost.
	NextMicros   int64 `json:"next_micros"`
	LimitMicros  int64 `json:"limit_micros"`
	WarnAtMicros int64 `json:"warn_at_micros"`
}

// Judge is the cap's rule, with no store: a call is stopped when what was
// spent and the most the call could cost are together more than the limit,
// and warned about when together they reach the warning threshold.
func Judge(spent, next int64, c Cap) Status {
	st := Status{State: StateOK, SpentMicros: spent, NextMicros: next, LimitMicros: c.LimitMicros}
	st.WarnAtMicros = c.LimitMicros / 100 * int64(c.WarnPercent)
	st.WarnAtMicros += c.LimitMicros % 100 * int64(c.WarnPercent) / 100
	switch {
	case spent+next > c.LimitMicros:
		st.State = StateStopped
	case spent+next >= st.WarnAtMicros:
		st.State = StateWarning
	}
	return st
}

// Allow asks the cap about one call to a model that is not local, at one
// moment. next is the most the call could cost. A caller does not ask
// about a local model: Counts says which calls the cap is for.
func Allow(ctx context.Context, s *tenant.Store, c Cap, at time.Time, next int64) (Status, error) {
	if next < 0 {
		return Status{}, errors.New("usage: a cost cannot be negative")
	}
	month := MonthOf(at)
	spent, err := Spent(ctx, s, month)
	if err != nil {
		return Status{}, err
	}
	st := Judge(spent, next, c)
	st.Month = month
	return st, nil
}

// Dollars writes an amount in millionths of a US dollar as dollars, to the
// cent, for a message a person reads.
func Dollars(micros int64) string {
	return fmt.Sprintf("$%.2f", float64(micros)/1e6)
}
