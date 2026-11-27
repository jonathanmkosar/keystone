// Package adminview is the only code the admin routes use to read another
// person (P15-01, decision 0644). It returns numbers and settings, and
// nothing else: never a chat, a title, a message, a file, memory, a trace
// or an error's words.
//
// It holds the install, and the admin routes' package is given a *View and
// never the install, so an admin route has no other way to another tenant.
// The admin isolation check in verify holds its imports: it reaches none of
// the packages that keep a person's content, directly or through another.
//
// Every read of another person is written in that person's own audit log
// before it is made, with the actor admin:<the admin's tenant id>, the admin
// call's request id and its route, and what is about to be read; and again
// when it ends. If the first entry cannot be written, nothing of theirs is
// read. The admin's own facts are read from their own store, whose log
// already holds the call.
package adminview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// ActorPrefix starts the actor of the entries an admin's reads leave in
// another person's audit log. The admin's tenant id follows it.
const ActorPrefix = "admin:"

// ErrNotAdmin is a read asked for by someone who does not carry the admin
// mark now.
var ErrNotAdmin = errors.New("adminview: only an admin may read another person")

var (
	idShape     = regexp.MustCompile(`^[a-f0-9]{16}$`)
	actionShape = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE) /api/v1/admin/`)
)

// View reads other people for the admin routes.
type View struct {
	in *tenant.Install
}

// New returns the view over an install. internal/app makes the one view and
// hands it to the admin routes.
func New(in *tenant.Install) *View { return &View{in: in} }

// Asker is the admin call a read is made for.
type Asker struct {
	// AdminID is the tenant id of the admin making the call.
	AdminID string
	// RequestID is the admin call's request id. The entries in the other
	// person's log carry it, so the two logs can be read side by side.
	RequestID string
	// Action is the admin route's method and path, as the admin's own log
	// records it.
	Action string
}

func (v *View) check(a Asker) error {
	switch {
	case v == nil || v.in == nil:
		return errors.New("adminview: the view has no install")
	case !idShape.MatchString(a.AdminID):
		return fmt.Errorf("adminview: %q is not a tenant id", a.AdminID)
	case strings.TrimSpace(a.RequestID) == "":
		return errors.New("adminview: a read needs the admin call's request id")
	case !actionShape.MatchString(a.Action):
		return fmt.Errorf("adminview: %q is not an admin route", a.Action)
	}
	marked, err := v.in.IsAdmin(a.AdminID)
	if err != nil {
		return err
	}
	if !marked {
		return ErrNotAdmin
	}
	return nil
}

// Touch is what one read or change of another person is, as their audit
// log records it in the entry's sent field.
type Touch struct {
	// Read names the facts read, such as last_sign_in.
	Read []string `json:"read,omitempty"`
	// Key, Old and New record a change of a setting (P15-03).
	Key string `json:"key,omitempty"`
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// with runs f on another person's store, between an entry in their log
// written before and one written after. f must read only what touch says.
func (v *View) with(ctx context.Context, a Asker, id string, touch Touch, f func(*tenant.Store) error) error {
	s, err := v.in.Store(id)
	if err != nil {
		return err
	}
	defer s.Close()
	sent, err := json.Marshal(touch)
	if err != nil {
		return err
	}
	e := audit.Entry{Kind: audit.KindAPI, RequestID: a.RequestID, Actor: ActorPrefix + a.AdminID, Action: a.Action,
		Outcome: audit.OutcomeStarted, Sent: string(sent)}
	if _, err := audit.Append(ctx, s, e); err != nil {
		return fmt.Errorf("adminview: tenant %s: the read could not be recorded, so it was not made: %w", id, err)
	}
	start := time.Now()
	ferr := f(s)
	e.Outcome, e.DurationMS = audit.OutcomeOK, time.Since(start).Milliseconds()
	if ferr != nil {
		e.Outcome = audit.OutcomeError
	}
	if _, err := audit.Append(context.WithoutCancel(ctx), s, e); err != nil && ferr == nil {
		return fmt.Errorf("adminview: tenant %s: the end of the read could not be recorded: %w", id, err)
	}
	return ferr
}

// own runs f on the admin's own store, with no second entry.
func (v *View) own(id string, f func(*tenant.Store) error) error {
	s, err := v.in.Store(id)
	if err != nil {
		return err
	}
	defer s.Close()
	return f(s)
}

// Person is one person on the install, as the admin sees them: numbers and
// settings, never content. P15-02 adds the numbers (spend, room, jobs,
// schedules, model health, watch levels) as more fields.
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Admin says the person carries the admin mark.
	Admin bool `json:"admin"`
	// Added is when the person was added to the install.
	Added time.Time `json:"added"`
	// LastSignIn is when they last signed in, and is left out when that is
	// not known.
	LastSignIn time.Time `json:"last_sign_in,omitzero"`
}

// People returns every person the server serves, oldest first, with their
// mark as keystone.json holds it now and their last sign-in. Each other
// person's log gets the read.
func (v *View) People(ctx context.Context, a Asker) ([]Person, error) {
	if err := v.check(a); err != nil {
		return nil, err
	}
	marks, err := v.in.Marks()
	if err != nil {
		return nil, err
	}
	out := []Person{}
	for _, t := range v.in.Tenants() {
		p := Person{ID: t.ID, Name: t.Name, Admin: marks[t.ID], Added: t.Created}
		read := func(s *tenant.Store) (err error) {
			p.LastSignIn, err = signin.LastSignIn(s)
			return err
		}
		if t.ID == a.AdminID {
			err = v.own(t.ID, read)
		} else {
			err = v.with(ctx, a, t.ID, Touch{Read: []string{"last_sign_in"}}, read)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
