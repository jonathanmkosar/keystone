// Package audit is a tenant's audit log: a list of entries in the tenant's
// own database that is added to and read, and never changed.
//
// The package can append an entry and list entries. It has no function that
// changes or removes one, and the table's triggers refuse a change, a delete
// and a replace from any code. Every entry is searched for keys before it is
// written, and what is found is removed.
//
// This package enforces the fixed rule that every model call and every tool
// call is recorded. It reads no setting, so no setting can switch it off;
// the "fixed rules" check in verify fails if it ever imports one.
package audit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Kind says what sort of call an entry records.
type Kind string

const (
	// KindAPI is a call to Keystone's own HTTP API.
	KindAPI Kind = "api"
	// KindModel is a call to a model through the seam.
	KindModel Kind = "model"
	// KindTool is a tool run for the agent.
	KindTool Kind = "tool"
)

// Outcome says how a call went.
type Outcome string

const (
	// OutcomeStarted marks an entry written before the call ran. An entry
	// with the same request id and action follows when the call ends. A
	// started entry with nothing after it is a call that never finished.
	OutcomeStarted Outcome = "started"
	// OutcomeOK is a call that did what was asked.
	OutcomeOK Outcome = "ok"
	// OutcomeRefused is a call Keystone or the other side turned down.
	OutcomeRefused Outcome = "refused"
	// OutcomeError is a call that failed.
	OutcomeError Outcome = "error"
)

// Usage is the token count of a model call. It has the fields of the seam's
// Usage, with the same meaning, so one converts to the other:
//
//	audit.Usage(u) // u is a seam.Usage
//
// It is declared here, and not borrowed from the seam, so that this package
// imports nothing that might one day read a setting.
type Usage struct {
	// InputTokens is the input that was neither read from nor written to
	// the provider's cache. The first four counts do not overlap.
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	// ThinkingTokens is the part of OutputTokens spent on thinking.
	ThinkingTokens int `json:"thinking_tokens,omitempty"`
	// Estimated says Keystone counted the tokens because the provider did
	// not report them.
	Estimated bool `json:"estimated,omitempty"`
}

// Entry is one row of the log.
type Entry struct {
	// ID and At are set by Append. What a caller puts in them is ignored,
	// so an entry cannot be placed in the past.
	ID int64     `json:"id"`
	At time.Time `json:"at"`

	Kind Kind `json:"kind"`
	// RequestID ties together everything one API call set off: the call
	// itself and each model call and tool call made for it.
	RequestID string `json:"request_id"`
	// Actor is who made the call: a short name such as person, admin, or
	// the job or schedule that ran it.
	Actor string `json:"actor"`
	// Action is what was called: the method and path of an API call, the
	// registry name of a model, or the name of a tool.
	Action  string  `json:"action"`
	Outcome Outcome `json:"outcome"`
	// Status is the HTTP status of an API call, and zero otherwise.
	Status int `json:"status,omitempty"`

	// Sent is what went out and Received is what came back. For a model
	// call they are the seam's Request and Response as JSON; for a tool
	// call, its input and its result. An API call leaves both empty.
	Sent     string `json:"sent,omitempty"`
	Received string `json:"received,omitempty"`

	// Usage is the token count of a model call.
	Usage Usage `json:"usage"`
	// CostMicros is what the call cost, in millionths of a US dollar.
	CostMicros int64 `json:"cost_micros"`
	// DurationMS is how long the call took, in milliseconds.
	DurationMS int64 `json:"duration_ms"`
}

// timeLayout sorts as text in the order of time.
const timeLayout = "2006-01-02T15:04:05.000Z"

// Validate checks an entry before it is written.
func Validate(e Entry) error {
	switch e.Kind {
	case KindAPI, KindModel, KindTool:
	default:
		return fmt.Errorf("audit: kind %q is not api, model or tool", e.Kind)
	}
	switch e.Outcome {
	case OutcomeStarted, OutcomeOK, OutcomeRefused, OutcomeError:
	default:
		return fmt.Errorf("audit: outcome %q is not started, ok, refused or error", e.Outcome)
	}
	switch {
	case strings.TrimSpace(e.RequestID) == "":
		return fmt.Errorf("audit: an entry needs a request id")
	case strings.TrimSpace(e.Actor) == "":
		return fmt.Errorf("audit: an entry needs an actor")
	case strings.TrimSpace(e.Action) == "":
		return fmt.Errorf("audit: an entry needs an action")
	case e.Status != 0 && (e.Status < 100 || e.Status > 599):
		return fmt.Errorf("audit: %d is not an HTTP status", e.Status)
	case e.CostMicros < 0:
		return fmt.Errorf("audit: a cost cannot be negative")
	case e.DurationMS < 0:
		return fmt.Errorf("audit: a duration cannot be negative")
	}
	u := e.Usage
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.CacheReadTokens < 0 || u.CacheWriteTokens < 0 || u.ThinkingTokens < 0 {
		return fmt.Errorf("audit: a token count cannot be negative")
	}
	if u.ThinkingTokens > u.OutputTokens {
		return fmt.Errorf("audit: thinking tokens (%d) are part of output tokens (%d) and cannot be more", u.ThinkingTokens, u.OutputTokens)
	}
	return nil
}

// Append adds one entry to the tenant's log and returns it as written, with
// its id and its time.
//
// Every text field is passed through Scrub first. A caller that holds a key
// or a token while it makes the call names it in secrets, and that exact
// value is removed wherever it appears, whatever its shape.
func Append(ctx context.Context, s *tenant.Store, e Entry, secrets ...string) (Entry, error) {
	e.RequestID = Scrub(e.RequestID, secrets...)
	e.Actor = Scrub(e.Actor, secrets...)
	e.Action = Scrub(e.Action, secrets...)
	e.Sent = Scrub(e.Sent, secrets...)
	e.Received = Scrub(e.Received, secrets...)
	if err := Validate(e); err != nil {
		return Entry{}, err
	}
	e.At = time.Now().UTC().Truncate(time.Millisecond)
	res, err := s.DB().ExecContext(ctx, `INSERT INTO audit_log(
			at, kind, request_id, actor, action, outcome, status, sent, received,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			thinking_tokens, tokens_estimated, cost_micros, duration_ms)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At.Format(timeLayout), string(e.Kind), e.RequestID, e.Actor, e.Action, string(e.Outcome), e.Status, e.Sent, e.Received,
		e.Usage.InputTokens, e.Usage.OutputTokens, e.Usage.CacheReadTokens, e.Usage.CacheWriteTokens,
		e.Usage.ThinkingTokens, e.Usage.Estimated, e.CostMicros, e.DurationMS)
	if err != nil {
		return Entry{}, fmt.Errorf("audit: %w", err)
	}
	if e.ID, err = res.LastInsertId(); err != nil {
		return Entry{}, fmt.Errorf("audit: %w", err)
	}
	return e, nil
}

// Filter narrows List. The zero Filter lists the whole log.
type Filter struct {
	// Kind keeps one kind of call.
	Kind Kind
	// RequestID keeps the entries of one API call.
	RequestID string
	// AfterID keeps the entries written after this one, for reading the
	// log a page at a time.
	AfterID int64
	// Limit is the most entries to return. Zero means all of them.
	Limit int
}

// List returns the tenant's entries, oldest first.
func List(ctx context.Context, s *tenant.Store, f Filter) ([]Entry, error) {
	q := `SELECT id, at, kind, request_id, actor, action, outcome, status, sent, received,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			thinking_tokens, tokens_estimated, cost_micros, duration_ms
		FROM audit_log WHERE id > ?`
	args := []any{f.AfterID}
	if f.Kind != "" {
		q += " AND kind = ?"
		args = append(args, string(f.Kind))
	}
	if f.RequestID != "" {
		q += " AND request_id = ?"
		args = append(args, f.RequestID)
	}
	q += " ORDER BY id"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	return out, nil
}

func scan(rows *sql.Rows) (Entry, error) {
	var (
		e                 Entry
		at, kind, outcome string
	)
	u := &e.Usage
	if err := rows.Scan(&e.ID, &at, &kind, &e.RequestID, &e.Actor, &e.Action, &outcome, &e.Status, &e.Sent, &e.Received,
		&u.InputTokens, &u.OutputTokens, &u.CacheReadTokens, &u.CacheWriteTokens,
		&u.ThinkingTokens, &u.Estimated, &e.CostMicros, &e.DurationMS); err != nil {
		return Entry{}, fmt.Errorf("audit: %w", err)
	}
	t, err := time.Parse(timeLayout, at)
	if err != nil {
		return Entry{}, fmt.Errorf("audit: entry %d: %w", e.ID, err)
	}
	e.At, e.Kind, e.Outcome = t, Kind(kind), Outcome(outcome)
	return e, nil
}
