package adminview

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// The admin's changes to a person (P15-02): adding one, a setup code,
// disabling and enabling. Each is written in the person's own log before it
// is made and after, with the old and the new value (Touch.Key, Old, New),
// and in the admin's log by the door. None reads anything of theirs but
// the state it changes.

// ErrNotReady is a change the view was not given what it needs for:
// a view made with New, which has no hooks and no setup code length.
var ErrNotReady = errors.New("adminview: this view cannot make changes")

// ErrName is a name for a new person that cannot be used.
var ErrName = errors.New("adminview: a person's name is 1 to 100 characters")

// SetupCode is a one-time code a new person, or one who needs a new
// password, enters at /setup. It is shown once, to the admin who made it,
// and only its hash is kept.
type SetupCode struct {
	Code    string    `json:"code"`
	Expires time.Time `json:"expires"`
}

// issue makes a setup code for a person, ending any earlier one.
func (v *View) issue(acc adminaccount.Account, id string) (SetupCode, error) {
	db, err := adminaccount.Open(v.in.Dir())
	if err != nil {
		return SetupCode{}, err
	}
	defer db.Close()
	code, expires, err := db.IssueSetupCode(id, acc.ID, v.o.SetupCodeFor, v.now())
	return SetupCode{Code: code, Expires: expires}, err
}

// AddPerson makes a person with the name given, sealed as every new person
// is (P15-06), and a setup code for them. The admin never sets their
// password: the person does, with the code, at /setup.
func (v *View) AddPerson(ctx context.Context, a Asker, name string) (Person, SetupCode, error) {
	acc, err := v.check(a)
	if err != nil {
		return Person{}, SetupCode{}, err
	}
	if v.o.SetupCodeFor <= 0 {
		return Person{}, SetupCode{}, ErrNotReady
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		return Person{}, SetupCode{}, ErrName
	}
	t, err := v.in.Create(name)
	if err != nil {
		return Person{}, SetupCode{}, err
	}
	var code SetupCode
	err = v.with(ctx, a, t.ID, Touch{By: acc.Name, Key: "person", New: "added, with a setup code"}, func(*tenant.Store) (err error) {
		code, err = v.issue(acc, t.ID)
		return err
	})
	if err != nil {
		return Person{}, SetupCode{}, err
	}
	p := Person{ID: t.ID, Name: t.Name, Added: t.Created}
	p.Enabled, p.SetupUntil = true, code.Expires
	return p, code, nil
}

// NewSetupCode gives a person a new setup code and ends any earlier one.
// Used, it sets their password and ends every session they had, so it is
// also how a forgotten password is replaced; the person sees in Settings,
// Admin access, that one was made.
func (v *View) NewSetupCode(ctx context.Context, a Asker, id string) (SetupCode, error) {
	acc, err := v.check(a)
	if err != nil {
		return SetupCode{}, err
	}
	if v.o.SetupCodeFor <= 0 {
		return SetupCode{}, ErrNotReady
	}
	if !v.known(id) {
		return SetupCode{}, fmt.Errorf("%w: %s", ErrNoPerson, id)
	}
	var code SetupCode
	err = v.with(ctx, a, id, Touch{By: acc.Name, Key: "setup_code", New: "made"}, func(s *tenant.Store) (err error) {
		if _, off, err := s.Disabled(ctx); err != nil {
			return err
		} else if off {
			return ErrDisabled
		}
		code, err = v.issue(acc, id)
		return err
	})
	return code, err
}

// ErrDisabled is a setup code asked for a person who is disabled: enable
// them first.
var ErrDisabled = errors.New("adminview: the person is disabled; enable them first")

// SetEnabled disables or enables a person and returns them as People shows
// them. Disabling marks them in their own database and ends every session
// they have, in one transaction, then wakes the task runner so their work
// pauses now; the switch that pauses every agent reads the mark, so their
// schedules are held and their tasks, research and sub-agents pause, and
// their share links answer as revoked. Enabling moves their schedules' due
// times past now first, then clears the mark and wakes the runner. Their
// own pause switch is left as they set it. Asking for the state they are
// already in changes nothing.
func (v *View) SetEnabled(ctx context.Context, a Asker, id string, enabled bool) (Person, error) {
	acc, err := v.check(a)
	if err != nil {
		return Person{}, err
	}
	if v.o.Pause == nil || v.o.Rearm == nil || v.o.Resume == nil {
		return Person{}, ErrNotReady
	}
	if !v.known(id) {
		return Person{}, fmt.Errorf("%w: %s", ErrNoPerson, id)
	}
	reg, err := settings.Registry()
	if err != nil {
		return Person{}, err
	}
	now := v.now()
	waiting, err := v.setupWaiting(now)
	if err != nil {
		return Person{}, err
	}
	var p Person
	for _, t := range v.in.Tenants() {
		if t.ID == id {
			p = Person{ID: t.ID, Name: t.Name, Added: t.Created}
			p.SetupUntil = waiting[t.ID]
		}
	}
	// The change and the person as they are after it, in one pair of
	// entries in their log.
	touch := Touch{By: acc.Name, Key: "enabled", Old: strconv.FormatBool(!enabled), New: strconv.FormatBool(enabled), Read: numbersRead}
	err = v.with(ctx, a, id, touch, func(s *tenant.Store) error {
		if err := v.change(ctx, s, enabled, now); err != nil {
			return err
		}
		var err error
		if p.LastSignIn, err = signin.LastSignIn(s); err != nil {
			return err
		}
		p.Numbers, err = readNumbers(ctx, s, reg, now)
		return err
	})
	if err != nil {
		return Person{}, err
	}
	return p, nil
}

// change disables or enables the person whose store s is.
func (v *View) change(ctx context.Context, s *tenant.Store, enabled bool, now time.Time) error {
	_, off, err := s.Disabled(ctx)
	switch {
	case err != nil:
		return err
	case off != enabled:
		return nil // already so
	case !enabled:
		if err := signin.Disable(s, now); err != nil {
			return err
		}
		return v.o.Pause(ctx, s)
	default:
		if err := v.o.Rearm(ctx, s); err != nil {
			return err
		}
		if err := signin.Enable(s); err != nil {
			return err
		}
		return v.o.Resume(ctx, s)
	}
}
