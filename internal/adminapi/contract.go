// Package adminapi holds the admin routes (P15-01): the step-up, and the
// admin's reads of the install, each behind the admin door the API shell
// keeps (decision 0642).
//
// It is given an adminview.View and never the install, and it does not
// import internal/tenant, so the only way it has to another person is the
// view, which returns numbers and settings and writes each read in that
// person's audit log (decision 0644). The admin isolation check in verify
// holds both.
package adminapi

import (
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminview"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/signin"
)

// Options is what the admin routes run with.
type Options struct {
	// View is the one way to another person.
	View *adminview.View
	// SignIn holds the sign-in settings, which the step-up checks the
	// password and passkeys under.
	SignIn signin.Options
	// StepUpFor is how long a step-up keeps the admin area open: the
	// admin.step_up_for setting.
	StepUpFor time.Duration
	// Log is the server's own log.
	Log *api.Log
}

// The shapes the admin routes take and return. They are the contract the
// admin screens (P15-02 to P15-04) build against.

// StepUpBody is what POST /api/v1/admin/step-up takes: the password, or the
// answer to a passkey prompt from POST /api/v1/admin/step-up/passkey. One of
// the two, never both.
type StepUpBody struct {
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

// StepUpReply answers a step-up that was granted.
type StepUpReply struct {
	// AdminUntil is when the admin area closes again for this session.
	AdminUntil time.Time `json:"admin_until"`
}

// PasskeyPromptReply answers POST /api/v1/admin/step-up/passkey, in the
// shape PublicKeyCredential.parseRequestOptionsFromJSON() reads: every byte
// field is base64url.
type PasskeyPromptReply struct {
	Challenge        string              `json:"challenge"`
	RPID             string              `json:"rpId"`
	AllowCredentials []AllowedCredential `json:"allowCredentials"`
	UserVerification string              `json:"userVerification"`
	// Timeout is how long the prompt can be answered, in milliseconds.
	Timeout int64 `json:"timeout"`
}

// AllowedCredential is one passkey the person may answer with.
type AllowedCredential struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// EndStepUpReply answers DELETE /api/v1/admin/step-up.
type EndStepUpReply struct {
	Ended bool `json:"ended"`
}

// PeopleReply answers GET /api/v1/admin/people.
type PeopleReply struct {
	People []adminview.Person `json:"people"`
}

// Routes returns the admin routes. Every one is under api.AdminPrefix and
// marked Admin; the two that grant the step-up are marked StepUp.
func Routes(o Options) []api.Route {
	h := handlers{o}
	return []api.Route{
		{
			Method: "POST", Path: api.AdminPrefix + "step-up", Admin: true, StepUp: true, Handle: h.stepUp,
			Summary: "Open the admin area on this session: the password, or a passkey's answer, again. It stays open for the admin.step_up_for setting. A wrong one is 403 refused and counts towards the sign-in's wait.",
			Body:    StepUpBody{}, Reply: StepUpReply{},
		},
		{
			Method: "POST", Path: api.AdminPrefix + "step-up/passkey", Admin: true, StepUp: true, Handle: h.passkeyPrompt,
			Summary: "A passkey prompt for a step-up, for navigator.credentials.get. 400 passkeys_off while passkeys are switched off.",
			Reply:   PasskeyPromptReply{},
		},
		{
			Method: "DELETE", Path: api.AdminPrefix + "step-up", Admin: true, Handle: h.endStepUp,
			Summary: "Close the admin area on this session before its time.",
			Reply:   EndStepUpReply{},
		},
		{
			Method: "GET", Path: api.AdminPrefix + "people", Admin: true, Handle: h.people,
			Summary: "Every person on the install: id, name, admin mark, when added and last sign-in. Never a chat, a title or an error's words. Each other person's audit log records the read.",
			Reply:   PeopleReply{},
		},
	}
}

// handlers serves the routes.
type handlers struct {
	o Options
}
