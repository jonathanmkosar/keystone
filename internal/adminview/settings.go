package adminview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/installsettings"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// Settings from the browser (P15-03): a person's tenant-admin settings,
// which the admin sets for that person, and the install's own values, which
// are no one person's. A person's own (tenant) settings are theirs: the
// admin can neither read nor change them here, and sets only the default a
// person starts from, as an install value.
//
// Every read of a person's settings is in their log as a read; every change
// is in their log before it is made, with the key and the new value, and
// after, with the old value too (Touch). Each change is also in the admin
// audit log as a pair of its own, beside the door's pair, with the person,
// the key, and the old and new value. A change that is refused (a person's
// own setting, a fixed rule, a value that does not fit) is refused before
// anything of the person's is read, and is in the admin log through the
// door alone. An install change is in the admin log, with its old and new
// value: it is no person's, so it is in no person's log.

// ErrNoPerson is an id that names no person on the install.
var ErrNoPerson = errors.New("adminview: there is no such person")

// Setting is one setting as the admin sees it: what the registry says of
// it, and its value.
type Setting struct {
	Key         string   `json:"key"`
	Title       string   `json:"title,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Lane        string   `json:"lane"`
	Type        string   `json:"type"`
	Options     []string `json:"options"`
	Scope       string   `json:"scope"`
	Description string   `json:"description"`
	// Default is what the setting has when nothing is set: for a person's
	// setting, the registry's default; for an install value, the shipped
	// default.
	Default string `json:"default"`
	// Value is what is set now: the person's value, or the install's.
	Value string `json:"value"`
	// Overridden says Value is set, for this person or for the install, and
	// is not the default.
	Overridden bool `json:"overridden"`
	// Updated is when a person's value was last set. Install values have
	// none.
	Updated time.Time `json:"updated,omitzero"`
	// Restart says the setting takes effect only after Keystone restarts.
	Restart bool `json:"restart,omitempty"`
	// InForce is what Keystone runs with now, for an install value. It
	// differs from Value for a restart setting changed since the start,
	// and Pending says so.
	InForce string `json:"in_force,omitempty"`
	Pending bool   `json:"pending,omitempty"`
}

func settingOf(st settings.Setting) Setting {
	options := st.Options
	if options == nil {
		options = []string{}
	}
	return Setting{Key: st.Key, Title: st.Title, Summary: st.Summary, Lane: st.Lane, Type: st.Type, Options: options,
		Scope: string(st.Scope), Description: st.Description, Default: settings.Shipped(st), Restart: st.Restart}
}

func personSetting(v tenantsettings.Value) Setting {
	s := settingOf(v.Setting)
	s.Default = v.Setting.Default
	s.Value, s.Overridden, s.Updated = v.Value, v.Overridden, v.UpdatedAt
	return s
}

func installSetting(v installsettings.Value) Setting {
	s := settingOf(v.Setting)
	s.Default = v.Shipped
	s.Value, s.Overridden = v.Set, v.IsStored
	if v.Setting.Scope == settings.ScopeInstall {
		s.InForce, s.Pending = v.InForce, v.Pending
	}
	return s
}

// Change is one setting the admin changed: for whom (a person's id, or none
// for the install), the key, and what was set before and after.
type Change struct {
	By     string `json:"by"`
	Person string `json:"person,omitempty"`
	Key    string `json:"key"`
	Old    string `json:"old"`
	New    string `json:"new"`
	// Restart says the new value takes effect at the next start.
	Restart bool `json:"restart,omitempty"`
}

// logged is a change as a log entry's sent field holds it: before a
// person's change is made its old value is not read yet, and is left out;
// after, it is there even when it is empty.
type logged struct {
	By      string  `json:"by"`
	Person  string  `json:"person,omitempty"`
	Key     string  `json:"key"`
	Old     *string `json:"old,omitempty"`
	New     string  `json:"new"`
	Restart bool    `json:"restart,omitempty"`
}

func (c Change) logged(oldKnown bool) logged {
	l := logged{By: c.By, Person: c.Person, Key: c.Key, New: c.New, Restart: c.Restart}
	if oldKnown {
		old := c.Old
		l.Old = &old
	}
	return l
}

// person checks that an id names a person on the install.
func (v *View) person(id string) error {
	if slices.ContainsFunc(v.in.Tenants(), func(t tenant.Tenant) bool { return t.ID == id }) {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrNoPerson, id)
}

// logChange writes a change in the admin audit log before it is made and
// when it ends, with the admin call's request id and route. If the first
// entry cannot be written, the change is not made. do fills in c's old and
// new value.
func (v *View) logChange(ctx context.Context, a Asker, c *Change, oldKnown bool, do func() error) error {
	db, err := adminaccount.Open(v.in.Dir())
	if err != nil {
		return err
	}
	defer db.Close()
	entry := func(outcome audit.Outcome, took time.Duration) audit.Entry {
		sent, _ := json.Marshal(c.logged(oldKnown || outcome != audit.OutcomeStarted))
		return audit.Entry{Kind: audit.KindAPI, RequestID: a.RequestID, Actor: ActorPrefix + a.AdminID, Action: a.Action,
			Outcome: outcome, Sent: string(sent), DurationMS: took.Milliseconds()}
	}
	if _, err := db.Append(ctx, entry(audit.OutcomeStarted, 0)); err != nil {
		return fmt.Errorf("adminview: the change could not be recorded in the admin log, so it was not made: %w", err)
	}
	start := time.Now()
	derr := do()
	outcome := audit.OutcomeOK
	if derr != nil {
		outcome = audit.OutcomeError
	}
	if _, err := db.Append(context.WithoutCancel(ctx), entry(outcome, time.Since(start))); err != nil && derr == nil {
		return fmt.Errorf("adminview: the end of the change could not be recorded in the admin log: %w", err)
	}
	return derr
}

// PersonSettings returns a person's tenant-admin settings, sorted by key.
// Their log gets the read.
func (v *View) PersonSettings(ctx context.Context, a Asker, id string) ([]Setting, error) {
	acc, err := v.check(a)
	if err != nil {
		return nil, err
	}
	if err := v.person(id); err != nil {
		return nil, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	var out []Setting
	err = v.with(ctx, a, id, Touch{By: acc.Name, Read: []string{"settings"}}, func(s *tenant.Store) error {
		all, err := tenantsettings.All(ctx, s, reg)
		if err != nil {
			return err
		}
		out = []Setting{}
		for _, val := range all {
			if val.Setting.Scope == settings.ScopeTenantAdmin && !settings.IsFixedRule(val.Setting.Key) {
				out = append(out, personSetting(val))
			}
		}
		return nil
	})
	return out, err
}

// ChangePersonSetting sets one of a person's tenant-admin settings, or with
// reset takes their value away so the setting has its default again,
// through tenantsettings with the role admin. A setting that is the
// person's own, a fixed rule, an unknown key or a value that does not fit is
// refused (tenantsettings.ErrNotAllowed, ErrUnknown, ErrBadValue) before
// anything of theirs is read. The change is in both logs.
func (v *View) ChangePersonSetting(ctx context.Context, a Asker, id, key, value string, reset bool) (Setting, Change, error) {
	acc, err := v.check(a)
	if err != nil {
		return Setting{}, Change{}, err
	}
	if err := v.person(id); err != nil {
		return Setting{}, Change{}, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return Setting{}, Change{}, err
	}
	st, err := mayChange(reg, key, value, reset)
	if err != nil {
		return Setting{}, Change{}, err
	}
	c := Change{By: acc.Name, Person: id, Key: key, New: value}
	if reset {
		c.New = settings.DefaultFor(st)
	}
	var after Setting
	err = v.logChange(ctx, a, &c, false, func() error {
		s, err := v.in.Store(id)
		if err != nil {
			return err
		}
		defer s.Close()
		started, err := json.Marshal(c.logged(false))
		if err != nil {
			return err
		}
		e := audit.Entry{Kind: audit.KindAPI, RequestID: a.RequestID, Actor: ActorPrefix + a.AdminID, Action: a.Action,
			Outcome: audit.OutcomeStarted, Sent: string(started)}
		if _, err := audit.Append(ctx, s, e); err != nil {
			return fmt.Errorf("adminview: tenant %s: the change could not be recorded, so it was not made: %w", id, err)
		}
		start := time.Now()
		ferr := func() error {
			old, err := tenantsettings.Get(ctx, s, reg, key)
			if err != nil {
				return err
			}
			c.Old = old
			if reset {
				err = tenantsettings.Clear(ctx, s, reg, tenantsettings.Admin, key)
			} else {
				err = tenantsettings.Set(ctx, s, reg, tenantsettings.Admin, key, value)
			}
			if err != nil {
				return err
			}
			all, err := tenantsettings.All(ctx, s, reg)
			if err != nil {
				return err
			}
			for _, val := range all {
				if val.Setting.Key == key {
					after = personSetting(val)
					c.New = val.Value
				}
			}
			return nil
		}()
		e.Outcome, e.DurationMS = audit.OutcomeOK, time.Since(start).Milliseconds()
		if ferr != nil {
			e.Outcome = audit.OutcomeError
		}
		ended, err := json.Marshal(c.logged(true))
		if err != nil {
			return err
		}
		e.Sent = string(ended)
		if _, err := audit.Append(context.WithoutCancel(ctx), s, e); err != nil && ferr == nil {
			return fmt.Errorf("adminview: tenant %s: the end of the change could not be recorded: %w", id, err)
		}
		return ferr
	})
	if err != nil {
		return Setting{}, Change{}, err
	}
	return after, c, nil
}

// mayChange checks a change of a person's setting before anything of theirs
// is read: the key is a setting, the admin may change it for one person,
// and the value fits.
func mayChange(reg []settings.Setting, key, value string, reset bool) (settings.Setting, error) {
	if settings.IsFixedRule(key) {
		return settings.Setting{}, fmt.Errorf("%w: %s is a fixed rule, not a setting, and nothing can switch it off", tenantsettings.ErrNotAllowed, key)
	}
	st, ok := settings.Lookup(reg, key)
	if !ok {
		return settings.Setting{}, fmt.Errorf("%w: %s", tenantsettings.ErrUnknown, key)
	}
	if err := tenantsettings.MayChange(st, tenantsettings.Admin); err != nil {
		return settings.Setting{}, err
	}
	if !reset {
		if err := settings.CheckValue(st, value); err != nil {
			return settings.Setting{}, fmt.Errorf("%w: %w", tenantsettings.ErrBadValue, err)
		}
	}
	return st, nil
}

// InstallSettings returns every setting the install may set: each install
// setting, and each person's own setting as the default a person starts
// from. It reads no person.
func (v *View) InstallSettings(a Asker) ([]Setting, error) {
	if _, err := v.check(a); err != nil {
		return nil, err
	}
	all, err := installsettings.List(v.in)
	if err != nil {
		return nil, err
	}
	out := make([]Setting, 0, len(all))
	for _, val := range all {
		out = append(out, installSetting(val))
	}
	return out, nil
}

// ChangeInstallSetting sets the install's value for a setting, or with
// reset takes it away, through installsettings: an install value, or the
// default for a person's own setting. A tenant-admin setting, a fixed rule,
// data.dir, an unknown key or a value that does not fit is refused
// (installsettings.ErrNotAllowed, ErrUnknown, ErrBadValue). The change is in
// the admin log with its old and new value.
func (v *View) ChangeInstallSetting(ctx context.Context, a Asker, key, value string, reset bool) (Setting, Change, error) {
	acc, err := v.check(a)
	if err != nil {
		return Setting{}, Change{}, err
	}
	if !reset {
		if err := installsettings.Check(key, value); err != nil {
			return Setting{}, Change{}, err
		}
	}
	before, err := installsettings.Get(v.in, key)
	if err != nil {
		return Setting{}, Change{}, err
	}
	c := Change{By: acc.Name, Key: key, Old: before.Set, New: value, Restart: before.Setting.Restart}
	if reset {
		c.New = before.Shipped
	}
	err = v.logChange(ctx, a, &c, true, func() error {
		var done installsettings.Change
		var err error
		if reset {
			done, err = installsettings.Clear(v.in, key)
		} else {
			done, err = installsettings.Set(v.in, key, value)
		}
		if err == nil {
			c.Old, c.New = done.Old, done.New
		}
		return err
	})
	if err != nil {
		return Setting{}, Change{}, err
	}
	after, err := installsettings.Get(v.in, key)
	if err != nil {
		return Setting{}, Change{}, err
	}
	return installSetting(after), c, nil
}
