package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DisabledKey is the row of tenant_meta that marks a person the admin has
// disabled (P15-02), holding when. internal/signin writes and clears it.
const DisabledKey = "disabled_at"

// disabledLayout is the stored form of the time.
const disabledLayout = "2006-01-02T15:04:05Z"

// Disabled reports whether the admin has disabled this person, and since
// when. Every place that decides whether a person may act reads it: the
// sign-in, the switch that pauses every agent and schedule, and a share
// link. It is kept in the person's own database, so the mark and the end
// of their sessions are one transaction.
func (s *Store) Disabled(ctx context.Context) (time.Time, bool, error) {
	var at string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM tenant_meta WHERE key = ?", DisabledKey).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, true, err
	}
	t, err := time.Parse(disabledLayout, at)
	if err != nil {
		return time.Time{}, true, fmt.Errorf("tenant %s: the time it was disabled does not read: %w", s.id, err)
	}
	return t, true, nil
}

// DisabledStamp is a time as the mark keeps it.
func DisabledStamp(t time.Time) string { return t.UTC().Format(disabledLayout) }
