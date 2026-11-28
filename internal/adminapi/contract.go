// Package adminapi holds the admin account's routes (P15-01): signing in at
// /admin/sign-in, the session, the admin's own passkeys, and the admin's
// reads of the install, each behind the admin door the API shell keeps
// (decision 0651).
//
// An admin is a separate account with its own database (decision 0650).
// Its routes are about the account itself, or read people through an
// adminview.View, which returns numbers and settings and writes each read
// in that person's audit log (decision 0644). This package is given the view
// and never the install, and it does not import internal/tenant, so the
// view is its only way to a person. It has no chat, file or model route.
// The admin isolation check in verify holds all of it.
package adminapi

import (
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/adminview"
	"github.com/jonathanmkosar/keystone/internal/api"
)

// Options is what the admin routes run with.
type Options struct {
	// View is the one way to a person.
	View *adminview.View
	// Open opens the admin database, for the sign-in doors, which run
	// before there is a session. An admin route is handed the database by
	// the shell instead.
	Open func() (*adminaccount.Store, error)
	// Accounts holds the sign-in rules and the session limits: the signin.
	// settings, admin.session_for and admin.idle_for.
	Accounts adminaccount.Options
	// SecureCookie marks the admin cookie for HTTPS only, as the person's.
	SecureCookie bool
	// Log is the server's own log.
	Log *api.Log
}

// The shapes the admin routes take and return. They are the contract the
// admin screens build against.

// SignInBody is what POST /api/v1/admin/session takes: the account, by its
// id or its name, and its password; or the answer to a passkey prompt from
// POST /api/v1/admin/session/passkey. A password or a passkey, never both.
type SignInBody struct {
	Account  string             `json:"account,omitempty"`
	Password string             `json:"password,omitempty"`
	Passkey  *PasskeyCredential `json:"passkey,omitempty"`
}

// PasskeyCredential is a browser's answer to a passkey prompt, in the shape
// PublicKeyCredential.toJSON() gives it: every byte field is base64url.
type PasskeyCredential struct {
	ID       string           `json:"id"`
	RawID    string           `json:"rawId"`
	Type     string           `json:"type"`
	Response PasskeyAssertion `json:"response"`
}

// PasskeyAssertion is the response part of a PasskeyCredential.
type PasskeyAssertion struct {
	ClientDataJSON    string `json:"clientDataJSON"`
	AuthenticatorData string `json:"authenticatorData"`
	Signature         string `json:"signature"`
	UserHandle        string `json:"userHandle"`
}

// Account is the signed-in admin account.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SessionInfo describes an admin session. It never holds the token.
type SessionInfo struct {
	ID      string    `json:"id"`
	Method  string    `json:"method"`
	Created time.Time `json:"created"`
	// Expires is when the session ends, whatever happens: admin.session_for
	// after it began.
	Expires time.Time `json:"expires"`
	// IdleUntil is when the session ends if it is not used before then:
	// admin.idle_for after its last call.
	IdleUntil time.Time `json:"idle_until"`
}

// SessionReply answers the admin session routes.
type SessionReply struct {
	Account Account     `json:"account"`
	Session SessionInfo `json:"session"`
}

// SignedOutReply answers DELETE /api/v1/admin/session.
type SignedOutReply struct {
	SignedOut bool `json:"signed_out"`
}

// PasskeyPromptReply is a passkey prompt. For signing in it is in the shape
// PublicKeyCredential.parseRequestOptionsFromJSON() reads, and for adding a
// passkey the user and the passkeys to leave out are filled in too, for
// parseCreationOptionsFromJSON(). Every byte field is base64url.
type PasskeyPromptReply struct {
	Challenge        string              `json:"challenge"`
	RPID             string              `json:"rpId"`
	AllowCredentials []AllowedCredential `json:"allowCredentials"`
	UserVerification string              `json:"userVerification"`
	// Timeout is how long the prompt can be answered, in milliseconds.
	Timeout int64 `json:"timeout"`
	// User and Algorithms are set on a prompt to add a passkey.
	User       *PasskeyUser `json:"user,omitempty"`
	Algorithms []int        `json:"algorithms,omitempty"`
}

// PasskeyUser is the account a new passkey is made for.
type PasskeyUser struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// AllowedCredential is one passkey: one to answer with, or, on a prompt to
// add one, one the account has already.
type AllowedCredential struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// AddPasskeyBody is what POST /api/v1/admin/passkeys takes: a name for the
// passkey and the browser's new credential. Every byte field is base64url.
type AddPasskeyBody struct {
	Name           string `json:"name"`
	RawID          string `json:"rawId"`
	ClientDataJSON string `json:"clientDataJSON"`
	// AuthenticatorData is response.getAuthenticatorData().
	AuthenticatorData string `json:"authenticatorData"`
	// PublicKey is response.getPublicKey(), the key as SubjectPublicKeyInfo.
	PublicKey string `json:"publicKey"`
}

// Passkey is one of the admin's passkeys. None of it is secret.
type Passkey struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used,omitzero"`
}

// PasskeysReply answers GET /api/v1/admin/passkeys.
type PasskeysReply struct {
	Passkeys []Passkey `json:"passkeys"`
}

// RemovedReply answers DELETE /api/v1/admin/passkeys/{id}.
type RemovedReply struct {
	Removed bool `json:"removed"`
}

// PeopleReply answers GET /api/v1/admin/people.
type PeopleReply struct {
	People []adminview.Person `json:"people"`
}

// Routes returns the admin routes. Every one is under api.AdminPrefix and
// marked Admin; the two sign-in routes are doors.
func Routes(o Options) []api.Route {
	h := handlers{o}
	return []api.Route{
		{
			Method: "POST", Path: api.AdminPrefix + "session", Admin: true, Door: true, Open: h.signIn,
			Summary: "Sign in to an admin account, with its id or name and its password, or a passkey's answer. The session token comes back in the keystone_admin cookie, sent with admin routes alone, and never in the body. A wrong one is 401 refused and counts towards the wait, as a person's does.",
			Body:    SignInBody{}, Reply: SessionReply{},
		},
		{
			Method: "POST", Path: api.AdminPrefix + "session/passkey", Admin: true, Door: true, Open: h.signInPrompt,
			Summary: "A passkey prompt for signing in to an admin account, for navigator.credentials.get. 400 passkeys_off while passkeys are switched off.",
			Reply:   PasskeyPromptReply{},
		},
		{
			Method: "GET", Path: api.AdminPrefix + "session", Admin: true, Handle: h.whoAmI,
			Summary: "Which admin account is signed in, and until when.",
			Reply:   SessionReply{},
		},
		{
			Method: "DELETE", Path: api.AdminPrefix + "session", Admin: true, Handle: h.signOut,
			Summary: "Sign the admin account out: end this session and forget its cookie.",
			Reply:   SignedOutReply{},
		},
		{
			Method: "GET", Path: api.AdminPrefix + "people", Admin: true, Handle: h.people,
			Summary: "Every person on the install: id, name, when added and last sign-in. Never a chat, a title, a file or an error's words. Each person's own audit log records the read.",
			Reply:   PeopleReply{},
		},
		{
			Method: "GET", Path: api.AdminPrefix + "passkeys", Admin: true, Handle: h.passkeys,
			Summary: "The signed-in admin account's own passkeys.",
			Reply:   PasskeysReply{},
		},
		{
			Method: "POST", Path: api.AdminPrefix + "passkeys/prompt", Admin: true, Handle: h.addPrompt,
			Summary: "A passkey prompt for adding a passkey to the signed-in admin account, for navigator.credentials.create. 400 passkeys_off while passkeys are switched off.",
			Reply:   PasskeyPromptReply{},
		},
		{
			Method: "POST", Path: api.AdminPrefix + "passkeys", Admin: true, Handle: h.addPasskey,
			Summary: "Add the passkey a browser made in answer to the prompt.",
			Body:    AddPasskeyBody{}, Reply: Passkey{},
		},
		{
			Method: "DELETE", Path: api.AdminPrefix + "passkeys/{id}", Admin: true, Handle: h.removePasskey,
			Summary: "Remove one of the signed-in admin account's passkeys.",
			Reply:   RemovedReply{},
		},
	}
}

// handlers serves the routes.
type handlers struct {
	o Options
}
