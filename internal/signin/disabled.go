package signin

import (
	"errors"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// ErrDisabled is a sign-in, or a session, for a person the admin has
// disabled (P15-02). The person is told to ask the admin.
var ErrDisabled = errors.New("this account is disabled: ask the admin to enable it")

// Disable marks the person disabled and ends every session they have, on
// every device, in one transaction. From then on no session opens for
// them (newSession) until Enable. Their data is kept as it is.
func Disable(s *tenant.Store, at time.Time) error {
	tx, err := s.DB().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO tenant_meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING`,
		tenant.DisabledKey, tenant.DisabledStamp(at)); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM signin_sessions"); err != nil {
		return err
	}
	return tx.Commit()
}

// Enable clears the mark Disable made, so the person may sign in again.
func Enable(s *tenant.Store) error {
	_, err := s.DB().Exec("DELETE FROM tenant_meta WHERE key = ?", tenant.DisabledKey)
	return err
}
