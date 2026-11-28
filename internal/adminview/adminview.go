// Package adminview is the only code the admin routes use to read a person
// (P15-01, decision 0644). It returns numbers and settings, and nothing
// else: never a chat, a title, a message, a file, an artifact, memory, a
// trace, an error's words or an audit log entry.
//
// It holds the install, and the admin routes' package is given a *View and
// never the install, so an admin route has no other way to a tenant. The
// admin isolation check in verify holds its imports: it reaches none of the
// packages that keep a person's content, directly or through another, and
// it never lists an audit log.
//
// An admin is a separate account in data/admin/admin.db (decision 0650).
// Every read asks that the account is there now. Every read or change of a
// person is written in that person's own audit log before it is made, with
// the actor admin:<the account's id>, the admin call's request id and its
// route, the account's name and what is about to be read; and again when it
// ends. If the first entry cannot be written, nothing of theirs is read.
// The admin's own log has the call already: the API shell wrote it.
package adminview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// ActorPrefix starts the actor of the entries an admin's reads leave in a
// person's audit log. The admin account's id follows it.
const ActorPrefix = adminaccount.ActorPrefix

// ErrNotAdmin is a read asked for by something that is not an admin account
// now.
var ErrNotAdmin = errors.New("adminview: only an admin account may read a person")

var actionShape = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE) /api/v1/admin/`)

// View reads people for the admin routes.
type View struct {
	in *tenant.Install
	o  Options // P15-02: the switch's hooks, the setup code's length, the clock (numbers.go)
}

// New returns the view over an install. internal/app makes the one view and
// hands it to the admin routes.
func New(in *tenant.Install) *View { return &View{in: in} }

// Asker is the admin call a read is made for.
type Asker struct {
	// AdminID is the id of the admin account making the call.
	AdminID string
	// RequestID is the admin call's request id. The entries in the
	// person's log carry it, as the admin audit log does, so the two can
	// be read side by side.
	RequestID string
	// Action is the admin route's method and path, as the admin audit log
	// records it.
	Action string
}

// check refuses a read for anything but an admin account that is in the
// admin database now, on an admin route, with a request id. It returns the
// account.
func (v *View) check(a Asker) (adminaccount.Account, error) {
	switch {
	case v == nil || v.in == nil:
		return adminaccount.Account{}, errors.New("adminview: the view has no install")
	case !adminaccount.IsID(a.AdminID):
		return adminaccount.Account{}, fmt.Errorf("adminview: %q is not an admin account's id", a.AdminID)
	case strings.TrimSpace(a.RequestID) == "":
		return adminaccount.Account{}, errors.New("adminview: a read needs the admin call's request id")
	case !actionShape.MatchString(a.Action):
		return adminaccount.Account{}, fmt.Errorf("adminview: %q is not an admin route", a.Action)
	}
	db, err := adminaccount.Open(v.in.Dir())
	if err != nil {
		return adminaccount.Account{}, err
	}
	defer db.Close()
	acc, err := db.Account(a.AdminID)
	if errors.Is(err, adminaccount.ErrNoAccount) {
		return adminaccount.Account{}, ErrNotAdmin
	}
	return acc, err
}

// Touch is what one read or change of a person is, as their audit log
// records it in the entry's sent field.
type Touch struct {
	// By is the admin account's name when the read was made, so the person
	// can tell who it was.
	By string `json:"by"`
	// Read names the facts read, such as last_sign_in.
	Read []string `json:"read,omitempty"`
	// Key, Old and New record a change of a setting (P15-03).
	Key string `json:"key,omitempty"`
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// with runs f on a person's store, between an entry in their log written
// before and one written after. f must read only what touch says.
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

// Person is one person on the install, as the admin sees them: numbers and
// settings, never content: since P15-02 with their numbers.
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Added is when the person was added to the install.
	Added time.Time `json:"added"`
	// LastSignIn is when they last signed in, and is left out when that is
	// not known.
	LastSignIn time.Time `json:"last_sign_in,omitzero"`
	// Numbers are the rest of the row (P15-02, numbers.go).
	Numbers
}

// People returns every person the server serves, oldest first, with their
// last sign-in and their numbers (P15-02). Each person's log gets the read.
func (v *View) People(ctx context.Context, a Asker) ([]Person, error) {
	acc, err := v.check(a)
	if err != nil {
		return nil, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	now := v.now()
	waiting, err := v.setupWaiting(now)
	if err != nil {
		return nil, err
	}
	out := []Person{}
	for _, t := range v.in.Tenants() {
		p, err := v.readPerson(ctx, a, acc, t, reg, waiting, now)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
