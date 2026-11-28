// Package setupapi is the door a new person comes through (P15-02): POST
// /api/v1/setup takes the one-time setup code an admin gave them and the
// password they choose, sets it, and signs them in. The page /setup asks
// for both. The admin never sees or sets the password.
//
// A code is good once, until admin.setup_code_for has passed since it was
// made, and for the one person it was made for; codes are kept as hashes
// in the admin database (internal/adminaccount). A code for a disabled
// person is refused and left unused. docs/architecture/admin.md describes
// it.
package setupapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Path is the door's route.
const Path = api.Prefix + "setup"

const action = "POST " + Path

// Options is what the door runs with.
type Options struct {
	Install *tenant.Install
	// SignIn holds the password rules and the session's length: the
	// signin. settings.
	SignIn       signin.Options
	SecureCookie bool
	Log          *api.Log
	// Now is the clock. Nil is the machine's.
	Now func() time.Time
}

// Body is what the door takes: the code, with or without its dashes, and
// the password the person chooses.
type Body struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

// Routes returns the door.
func Routes(o Options) []api.Route {
	return []api.Route{{
		Method: "POST", Path: Path, Door: true, Open: o.setup,
		Summary: "Set up a person with the one-time code an admin gave them: sets the password they choose and signs them in, with the session in the cookie. 401 refused for a code that does not work (used, replaced, expired, or typed wrong), 403 disabled for a person the admin has disabled, 400 bad_password for a password the rules refuse.",
		Body:    Body{}, Reply: api.SessionReply{},
	}}
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Options) setup(w http.ResponseWriter, r *http.Request) {
	id := audit.RequestID(r.Context())
	var body Body
	if !api.ReadJSON(w, r, &body) {
		return
	}
	refused := func(why string) {
		o.Log.Printf("%s setup refused: %s", id, why)
		api.Error(w, r, http.StatusUnauthorized, "refused", "That setup code does not work. It may have been used, replaced or expired, or typed wrong: ask the admin for a new one.")
	}
	code := adminaccount.NormalizeCode(body.Code)
	if code == "" || o.Install == nil {
		refused("no code")
		return
	}
	db, err := adminaccount.Open(o.Install.Dir())
	if err != nil {
		o.Log.Printf("%s setup: the admin database could not be opened: %v", id, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "setup failed")
		return
	}
	defer db.Close()
	now := o.now()
	tenantID, err := db.SetupCodeFor(code, now)
	if errors.Is(err, adminaccount.ErrNoCode) {
		refused("the code does not work")
		return
	}
	if err != nil {
		o.Log.Printf("%s setup: reading the code failed: %v", id, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "setup failed")
		return
	}
	store, err := o.Install.Store(tenantID)
	if err != nil {
		refused("the code's person is not on the install")
		return
	}
	defer store.Close()
	start := time.Now()
	// The call is the person's: it goes in their own log, as a sign-in
	// does, with the password and the code kept out of it.
	record := func(status int) error {
		outcome := audit.OutcomeOK
		if status >= 500 {
			outcome = audit.OutcomeError
		} else if status >= 400 {
			outcome = audit.OutcomeRefused
		}
		_, err := audit.Append(context.WithoutCancel(r.Context()), store, audit.Entry{
			Kind: audit.KindAPI, RequestID: id, Actor: api.ActorPerson, Action: action,
			Outcome: outcome, Status: status, DurationMS: time.Since(start).Milliseconds(),
		}, body.Password, body.Code, code)
		return err
	}
	if _, off, err := store.Disabled(r.Context()); err != nil || off {
		if err != nil {
			record(http.StatusInternalServerError)
			o.Log.Printf("%s setup for tenant %s failed: %v", id, tenantID, err)
			api.Error(w, r, http.StatusInternalServerError, "failed", "setup failed")
			return
		}
		record(http.StatusForbidden)
		o.Log.Printf("%s setup refused for tenant %s: disabled", id, tenantID)
		api.Error(w, r, http.StatusForbidden, "disabled", "This account is disabled. Ask the admin to enable it.")
		return
	}
	if err := signin.CheckPasswordRules(body.Password, o.SignIn); err != nil {
		record(http.StatusBadRequest)
		api.Error(w, r, http.StatusBadRequest, "bad_password", err.Error())
		return
	}
	if err := db.UseSetupCode(code, tenantID, now); err != nil {
		if errors.Is(err, adminaccount.ErrNoCode) {
			record(http.StatusUnauthorized)
			refused("the code was used at the same moment")
			return
		}
		record(http.StatusInternalServerError)
		o.Log.Printf("%s setup for tenant %s failed: %v", id, tenantID, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "setup failed")
		return
	}
	token, sess, err := setPassword(store, body.Password, o.SignIn)
	if err != nil {
		record(http.StatusInternalServerError)
		o.Log.Printf("%s setup for tenant %s failed after the code was used: %v", id, tenantID, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "setup failed: ask the admin for a new code")
		return
	}
	// A setup that cannot be recorded does not stand: the session ends.
	if err := record(http.StatusOK); err != nil {
		signin.SignOut(store, token)
		o.Log.Printf("%s setup for tenant %s could not be recorded: %v", id, tenantID, err)
		api.Error(w, r, http.StatusInternalServerError, "not_recorded", "the call could not be recorded, so it was not run")
		return
	}
	o.Log.Printf("%s setup for tenant %s: the password is set", id, tenantID)
	api.SetSessionCookie(w, r, token, sess.Expires, o.SecureCookie)
	name := ""
	for _, t := range o.Install.Tenants() {
		if t.ID == tenantID {
			name = t.Name
		}
	}
	api.WriteJSON(w, http.StatusOK, api.SessionReply{
		Person:  api.Person{ID: tenantID, Name: name},
		Session: api.SessionInfo{ID: sess.ID, Method: sess.Method, Created: sess.Created, Expires: sess.Expires},
	})
}

// setPassword sets the person's password, which ends every session they
// had, and opens one with it.
func setPassword(s *tenant.Store, password string, o signin.Options) (string, signin.Session, error) {
	if err := signin.SetPassword(s, password, o); err != nil {
		return "", signin.Session{}, err
	}
	return signin.SignIn(s, password, o)
}
