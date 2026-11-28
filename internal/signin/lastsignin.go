package signin

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// LastSignIn returns when the tenant last opened a session, by a password
// or a passkey, or the zero time when it never has. Before P15-01 it was
// not kept; for a tenant that has not signed in since, it is the newest
// session still open (decision 0646). It is a number the admin's list of
// people shows, read through internal/adminview.
func LastSignIn(s *tenant.Store) (time.Time, error) {
	var at string
	err := s.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = 'last_sign_in'").Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.DB().QueryRow("SELECT COALESCE(MAX(created_at), '') FROM signin_sessions").Scan(&at)
	}
	if err != nil {
		return time.Time{}, err
	}
	if at == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(stampLayout, at)
	if err != nil {
		return time.Time{}, fmt.Errorf("tenant %s: the last sign-in %q: %w", s.TenantID(), at, err)
	}
	return t, nil
}
