package audit

import (
	"context"
	"fmt"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// AdminActorPrefix starts the actor of every entry an admin account's call
// leaves in a person's log: admin:<the account's id> (P15-01).
const AdminActorPrefix = "admin:"

// ListByAdmin returns the entries admin accounts' calls left in this
// tenant's own log, oldest first, after the entry afterID, at most limit
// (zero means all). It is how a person sees, in their Settings, which admin
// read or changed what of theirs, and when (decision 0654).
func ListByAdmin(ctx context.Context, s *tenant.Store, afterID int64, limit int) ([]Entry, error) {
	q := `SELECT id, at, kind, request_id, actor, action, outcome, status, sent, received,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			thinking_tokens, tokens_estimated, cost_micros, duration_ms
		FROM audit_log WHERE id > ? AND substr(actor, 1, ?) = ? ORDER BY id`
	args := []any{afterID, len(AdminActorPrefix), AdminActorPrefix}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
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
