package adminaccount

import (
	"context"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
)

// The admin audit log: every admin call and every admin sign-in, in the
// shape of a person's audit log (audit.Entry), kept in admin.db. It is added
// to and read, never changed: the table's triggers refuse a change, a delete
// and a replace. An admin call that reads or changes a person is also in
// that person's own log, written by internal/adminview, with the same
// request id.

const logTimeLayout = "2006-01-02T15:04:05.000Z"

// Append adds one entry to the admin audit log and returns it as written.
// Every text field is scrubbed of anything shaped like a key or a token
// first, and of each value named in secrets, as a person's log is.
func (s *Store) Append(ctx context.Context, e audit.Entry, secrets ...string) (audit.Entry, error) {
	e.RequestID = audit.Scrub(e.RequestID, secrets...)
	e.Actor = audit.Scrub(e.Actor, secrets...)
	e.Action = audit.Scrub(e.Action, secrets...)
	e.Sent = audit.Scrub(e.Sent, secrets...)
	e.Received = audit.Scrub(e.Received, secrets...)
	if err := audit.Validate(e); err != nil {
		return audit.Entry{}, err
	}
	e.At = time.Now().UTC().Truncate(time.Millisecond)
	res, err := s.db.ExecContext(ctx, `INSERT INTO admin_audit_log(at, kind, request_id, actor, action, outcome, status, sent, received, duration_ms)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At.Format(logTimeLayout), string(e.Kind), e.RequestID, e.Actor, e.Action, string(e.Outcome), e.Status, e.Sent, e.Received, e.DurationMS)
	if err != nil {
		return audit.Entry{}, fmt.Errorf("admin audit: %w", err)
	}
	if e.ID, err = res.LastInsertId(); err != nil {
		return audit.Entry{}, fmt.Errorf("admin audit: %w", err)
	}
	return e, nil
}

// Log returns the admin audit log's entries, oldest first, narrowed by f's
// request id, after and limit.
func (s *Store) Log(ctx context.Context, f audit.Filter) ([]audit.Entry, error) {
	q := "SELECT id, at, kind, request_id, actor, action, outcome, status, sent, received, duration_ms FROM admin_audit_log WHERE id > ?"
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
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("admin audit: %w", err)
	}
	defer rows.Close()
	var out []audit.Entry
	for rows.Next() {
		var e audit.Entry
		var at, kind, outcome string
		if err := rows.Scan(&e.ID, &at, &kind, &e.RequestID, &e.Actor, &e.Action, &outcome, &e.Status, &e.Sent, &e.Received, &e.DurationMS); err != nil {
			return nil, fmt.Errorf("admin audit: %w", err)
		}
		if e.At, err = time.Parse(logTimeLayout, at); err != nil {
			return nil, fmt.Errorf("admin audit: entry %d: %w", e.ID, err)
		}
		e.Kind, e.Outcome = audit.Kind(kind), audit.Outcome(outcome)
		out = append(out, e)
	}
	return out, rows.Err()
}
