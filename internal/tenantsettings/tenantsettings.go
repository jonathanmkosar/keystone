// Package tenantsettings holds one tenant's own values for the settings in
// the registry, and answers what value is in force for that tenant.
//
// The registry in the settings package says what can be set, its default and
// who may change it. This package stores what one tenant has changed, in the
// tenant's own database. A setting with no stored value has its default.
//
// The four fixed rules are not settings. Nothing here stores a value for
// one, and the table refuses a row that names one.
package tenantsettings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Role says who is asking to change a setting.
type Role string

const (
	// Person is the tenant themself, through their own session.
	Person Role = "person"
	// Admin is the install's admin.
	Admin Role = "admin"
)

var (
	// ErrUnknown means the key names no setting in the registry.
	ErrUnknown = errors.New("no such setting")
	// ErrNotAllowed means the setting exists and this role may not change
	// it for a tenant, or the key names a fixed rule.
	ErrNotAllowed = errors.New("not allowed")
	// ErrBadValue means the value does not read as the setting's type, or
	// is not one of its options.
	ErrBadValue = errors.New("not a value for this setting")
)

// Value is one setting as it stands for a tenant.
type Value struct {
	Setting settings.Setting `json:"setting"`
	// Value is the value in force: the tenant's own, or the default.
	Value string `json:"value"`
	// Overridden says Value is the tenant's own. SetBy and UpdatedAt say
	// who stored it and when; they are empty for a default.
	Overridden bool      `json:"overridden"`
	SetBy      Role      `json:"set_by,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitzero"`
}

const timeLayout = "2006-01-02T15:04:05Z"

// MayChange reports whether a role may change a setting for one tenant,
// following decision 0001: the person changes a tenant setting, the admin
// changes a tenant-admin setting, and an install setting has one value for
// everyone and cannot be set for a tenant at all.
func MayChange(st settings.Setting, by Role) error {
	if by != Person && by != Admin {
		return fmt.Errorf("%w: %q is not person or admin", ErrNotAllowed, by)
	}
	if settings.IsFixedRule(st.Key) {
		return fixed(st.Key)
	}
	switch st.Scope {
	case settings.ScopeTenant:
		if by != Person {
			return fmt.Errorf("%w: %s is the person's own to change", ErrNotAllowed, st.Key)
		}
	case settings.ScopeTenantAdmin:
		if by != Admin {
			return fmt.Errorf("%w: %s is set by the admin", ErrNotAllowed, st.Key)
		}
	default:
		return fmt.Errorf("%w: %s has one value for the whole install and cannot be set for one tenant", ErrNotAllowed, st.Key)
	}
	return nil
}

func fixed(key string) error {
	return fmt.Errorf("%w: %s is a fixed rule, not a setting, and nothing can switch it off", ErrNotAllowed, key)
}

// find looks a key up in the registry. A fixed rule is refused before the
// registry is read, so the answer does not depend on what the registry holds.
func find(reg []settings.Setting, key string) (settings.Setting, error) {
	if settings.IsFixedRule(key) {
		return settings.Setting{}, fixed(key)
	}
	st, ok := settings.Lookup(reg, key)
	if !ok {
		return settings.Setting{}, fmt.Errorf("%w: %s", ErrUnknown, key)
	}
	return st, nil
}

// Set stores a tenant's own value for a setting. The role must be allowed
// to change it (MayChange) and the value must pass settings.CheckValue.
func Set(ctx context.Context, s *tenant.Store, reg []settings.Setting, by Role, key, value string) error {
	st, err := find(reg, key)
	if err != nil {
		return err
	}
	if err := MayChange(st, by); err != nil {
		return err
	}
	if err := settings.CheckValue(st, value); err != nil {
		return fmt.Errorf("%w: %w", ErrBadValue, err)
	}
	_, err = s.DB().ExecContext(ctx, `INSERT INTO tenant_settings(key, value, set_by, updated_at) VALUES(?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, set_by = excluded.set_by, updated_at = excluded.updated_at`,
		key, value, string(by), time.Now().UTC().Format(timeLayout))
	return err
}

// Clear removes a tenant's own value, so the setting goes back to its
// default. The role must be allowed to change the setting. Clearing a
// setting that has no stored value does nothing.
func Clear(ctx context.Context, s *tenant.Store, reg []settings.Setting, by Role, key string) error {
	st, err := find(reg, key)
	if err != nil {
		return err
	}
	if err := MayChange(st, by); err != nil {
		return err
	}
	_, err = s.DB().ExecContext(ctx, "DELETE FROM tenant_settings WHERE key = ?", key)
	return err
}

// Get returns the value in force for a tenant: its own stored value, or the
// registry's default.
func Get(ctx context.Context, s *tenant.Store, reg []settings.Setting, key string) (string, error) {
	st, err := find(reg, key)
	if err != nil {
		return "", err
	}
	var r row
	err = s.DB().QueryRowContext(ctx, "SELECT value, set_by, updated_at FROM tenant_settings WHERE key = ?", key).
		Scan(&r.value, &r.setBy, &r.updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return settings.DefaultFor(st), nil // the install's own default, when it has one (P15-03)
	}
	if err != nil {
		return "", err
	}
	return inForce(st, &r).Value, nil
}

// All returns every setting in the registry as it stands for a tenant,
// sorted by key.
func All(ctx context.Context, s *tenant.Store, reg []settings.Setting) ([]Value, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT key, value, set_by, updated_at FROM tenant_settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stored := map[string]*row{}
	for rows.Next() {
		var key string
		var r row
		if err := rows.Scan(&key, &r.value, &r.setBy, &r.updatedAt); err != nil {
			return nil, err
		}
		stored[key] = &r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Value, 0, len(reg))
	for _, st := range reg {
		out = append(out, inForce(st, stored[st.Key]))
	}
	return out, nil
}

type row struct {
	value, setBy, updatedAt string
}

// inForce works out a setting's value from its stored row, which may be nil.
// A stored value counts only while whoever stored it could store it today
// and it still passes CheckValue. So when a setting's scope, type or options
// change in the registry, a value that no longer fits falls back to the
// default without anyone having to find and remove it.
//
// With no value of the tenant's own, the default is the install's, when
// the admin set one (P15-03, settings.DefaultFor), so a registry read
// before the change still answers what is in force now.
func inForce(st settings.Setting, r *row) Value {
	st.Default = settings.DefaultFor(st)
	v := Value{Setting: st, Value: st.Default}
	if r == nil || MayChange(st, Role(r.setBy)) != nil || settings.CheckValue(st, r.value) != nil {
		return v
	}
	v.Value, v.Overridden, v.SetBy = r.value, true, Role(r.setBy)
	v.UpdatedAt, _ = time.Parse(timeLayout, r.updatedAt)
	return v
}
