// Package installsettings keeps the install's own setting values (P15-03):
// a value for each install setting the admin changed, and an install-wide
// default for each person's own setting, which a person who has not chosen
// starts with and follows.
//
// The values are kept in keystone.json, under "settings" and
// "person_defaults" (internal/tenant, installvalues.go), and are handed to
// the registry's one overlay (settings.UseInstalled) at start and after each
// change. A setting the registry marks restart is read by the server once,
// at start, so a change to it is kept and takes effect at the next start;
// any other change takes effect at once.
//
// What may be set here: an install setting, and a person's own (tenant)
// setting as a default. A tenant-admin setting is set for one person
// (internal/adminview), a fixed rule is not a setting, and data.dir names
// the folder keystone.json is in, so it cannot be kept inside it.
package installsettings

import (
	"errors"
	"fmt"
	"maps"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

var (
	// ErrUnknown is a key that names no setting.
	ErrUnknown = errors.New("no such setting")
	// ErrNotAllowed is a setting that has no install value: a tenant-admin
	// setting, a fixed rule, or data.dir.
	ErrNotAllowed = errors.New("not allowed")
	// ErrBadValue is a value that does not read as the setting's type or is
	// not one of its options.
	ErrBadValue = errors.New("not a value for this setting")
)

// notKept are the install settings that cannot be kept in keystone.json:
// data.dir names the folder the file is in, so the server needs it before it
// can read the file.
var notKept = map[string]string{
	"data.dir": "it names the data folder keystone.json is in; give keystone serve --data instead",
}

// mu keeps one change at a time, from the file to the overlay.
var mu sync.Mutex

// fieldOf says where a setting's install value is kept, or why it has none.
func fieldOf(st settings.Setting) (string, error) {
	if settings.IsFixedRule(st.Key) {
		return "", fmt.Errorf("%w: %s is a fixed rule, not a setting, and nothing can switch it off", ErrNotAllowed, st.Key)
	}
	if why, ok := notKept[st.Key]; ok {
		return "", fmt.Errorf("%w: %s cannot be set here: %s", ErrNotAllowed, st.Key, why)
	}
	switch st.Scope {
	case settings.ScopeInstall:
		return tenant.SettingsField, nil
	case settings.ScopeTenant:
		return tenant.PersonDefaultsField, nil
	default:
		return "", fmt.Errorf("%w: %s is set for each person by the admin, not for the whole install", ErrNotAllowed, st.Key)
	}
}

// find looks a key up, refusing a fixed rule before the registry is read.
func find(key string) (settings.Setting, error) {
	if settings.IsFixedRule(key) {
		return settings.Setting{}, fmt.Errorf("%w: %s is a fixed rule, not a setting, and nothing can switch it off", ErrNotAllowed, key)
	}
	reg, err := settings.Registry()
	if err != nil {
		return settings.Setting{}, err
	}
	st, ok := settings.Lookup(reg, key)
	if !ok {
		return settings.Setting{}, fmt.Errorf("%w: %s", ErrUnknown, key)
	}
	return st, nil
}

// stored reads both objects from keystone.json.
func stored(in *tenant.Install) (install, defaults map[string]string, err error) {
	if install, err = in.InstallValues(tenant.SettingsField); err != nil {
		return nil, nil, err
	}
	if defaults, err = in.InstallValues(tenant.PersonDefaultsField); err != nil {
		return nil, nil, err
	}
	return install, defaults, nil
}

// Load reads the install's values from keystone.json and hands every one to
// the registry, restart settings included. The server calls it once, at
// start, before it reads its settings.
func Load(in *tenant.Install) error {
	mu.Lock()
	defer mu.Unlock()
	install, defaults, err := stored(in)
	if err != nil {
		return err
	}
	all := maps.Clone(defaults)
	maps.Copy(all, install)
	settings.UseInstalled(all)
	return nil
}

// apply hands the values in keystone.json to the registry after a change: a
// restart setting keeps the value it started with. The caller holds mu.
func apply(in *tenant.Install) error {
	install, defaults, err := stored(in)
	if err != nil {
		return err
	}
	reg, err := settings.Registry()
	if err != nil {
		return err
	}
	now := settings.Installed()
	next := maps.Clone(defaults)
	maps.Copy(next, install)
	for _, st := range reg {
		if !st.Restart {
			continue
		}
		if v, ok := now[st.Key]; ok {
			next[st.Key] = v
		} else {
			delete(next, st.Key)
		}
	}
	settings.UseInstalled(next)
	return nil
}

// Value is one setting as the install has it.
type Value struct {
	// Setting is the registry's entry, with its shipped default.
	Setting settings.Setting
	// Shipped is the default as the registry ships it.
	Shipped string
	// Stored is the install's own value in keystone.json, and IsStored says
	// there is one. A value that no longer fits the setting is left out.
	Stored   string
	IsStored bool
	// Set is what the install has chosen: Stored when there is one, and
	// Shipped when not.
	Set string
	// InForce is what Keystone runs with now. It differs from Set only for a
	// restart setting changed since the server started, and Pending says so.
	InForce string
	Pending bool
}

func valueOf(st settings.Setting, field map[string]string) Value {
	v := Value{Setting: st, Shipped: settings.Shipped(st)}
	v.Setting.Default = v.Shipped
	if s, ok := field[st.Key]; ok && settings.CheckValue(st, s) == nil {
		v.Stored, v.IsStored = s, true
	}
	v.Set = v.Shipped
	if v.IsStored {
		v.Set = v.Stored
	}
	v.InForce = settings.DefaultFor(st)
	v.Pending = st.Restart && v.InForce != v.Set
	return v
}

// List returns every setting the install may set, sorted by key: each
// install setting but data.dir, and each person's own setting as the
// default a person starts from.
func List(in *tenant.Install) ([]Value, error) {
	install, defaults, err := stored(in)
	if err != nil {
		return nil, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	var out []Value
	for _, st := range reg {
		field, err := fieldOf(st)
		if err != nil {
			continue
		}
		if field == tenant.SettingsField {
			out = append(out, valueOf(st, install))
		} else {
			out = append(out, valueOf(st, defaults))
		}
	}
	return out, nil
}

// Get returns one setting as the install has it.
func Get(in *tenant.Install, key string) (Value, error) {
	st, err := find(key)
	if err != nil {
		return Value{}, err
	}
	field, err := fieldOf(st)
	if err != nil {
		return Value{}, err
	}
	values, err := in.InstallValues(field)
	if err != nil {
		return Value{}, err
	}
	return valueOf(st, values), nil
}

// Change is what one change did: the setting, what the install had chosen
// before and after (a stored value, or the shipped default), and whether it
// waits for a restart.
type Change struct {
	Key   string         `json:"key"`
	Scope settings.Scope `json:"scope"`
	Old   string         `json:"old"`
	New   string         `json:"new"`
	// Restart says the new value takes effect at the next start.
	Restart bool `json:"restart,omitempty"`
}

// Check reports whether a value may be set for a key, without setting it.
func Check(key, value string) error {
	st, err := find(key)
	if err != nil {
		return err
	}
	if _, err := fieldOf(st); err != nil {
		return err
	}
	if err := settings.CheckValue(st, value); err != nil {
		return fmt.Errorf("%w: %w", ErrBadValue, err)
	}
	return nil
}

// Set keeps the install's value for a setting in keystone.json and, unless
// the setting is marked restart, puts it in force at once.
func Set(in *tenant.Install, key, value string) (Change, error) {
	if err := Check(key, value); err != nil {
		return Change{}, err
	}
	return change(in, key, func(m map[string]string) { m[key] = value })
}

// Clear removes the install's value for a setting, so it goes back to its
// shipped default. Clearing a setting with no value changes nothing.
func Clear(in *tenant.Install, key string) (Change, error) {
	st, err := find(key)
	if err != nil {
		return Change{}, err
	}
	if _, err := fieldOf(st); err != nil {
		return Change{}, err
	}
	return change(in, key, func(m map[string]string) { delete(m, key) })
}

func change(in *tenant.Install, key string, edit func(map[string]string)) (Change, error) {
	mu.Lock()
	defer mu.Unlock()
	st, err := find(key)
	if err != nil {
		return Change{}, err
	}
	field, err := fieldOf(st)
	if err != nil {
		return Change{}, err
	}
	c := Change{Key: key, Scope: st.Scope, Restart: st.Restart}
	err = in.ChangeInstallValues(field, func(m map[string]string) error {
		c.Old = valueOf(st, m).Set
		edit(m)
		c.New = valueOf(st, m).Set
		return nil
	})
	if err != nil {
		return Change{}, err
	}
	return c, apply(in)
}
