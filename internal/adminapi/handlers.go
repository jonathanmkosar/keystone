package adminapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminview"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/signin"
)

// refuseNotAdmin answers a call that did not come through the admin door.
// It is not reachable: the door refuses such a call first. It is here so a
// handler never runs on the strength of the door alone.
func refuseNotAdmin(w http.ResponseWriter, r *http.Request) {
	api.Error(w, r, http.StatusForbidden, "not_admin", "this is the admin area, and only an admin may open it")
}

// asker is the admin call a read through the view is made for.
func asker(r *http.Request, c api.Call) adminview.Asker {
	return adminview.Asker{AdminID: c.Store.TenantID(), RequestID: c.RequestID, Action: r.Method + " " + r.URL.Path}
}

// stepUp opens the admin area on the caller's session, with the password or
// a passkey's answer (decision 0643).
func (h handlers) stepUp(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !c.Admin {
		refuseNotAdmin(w, r)
		return
	}
	var body StepUpBody
	if !api.ReadJSON(w, r, &body) {
		return
	}
	var (
		until time.Time
		err   error
	)
	switch {
	case body.Password != "" && body.Passkey != nil:
		api.Error(w, r, http.StatusBadRequest, "bad_request", "send the password or a passkey's answer, not both")
		return
	case body.Password != "":
		until, err = signin.StepUp(c.Store, c.Session.ID, body.Password, h.o.SignIn, h.o.StepUpFor)
	case body.Passkey != nil:
		a, ok := assertion(*body.Passkey)
		if !ok {
			api.Error(w, r, http.StatusBadRequest, "bad_request", "the passkey's answer is not base64url in the shape PublicKeyCredential.toJSON() gives")
			return
		}
		until, err = signin.PasskeyStepUp(c.Store, c.Session.ID, a, h.o.SignIn, h.o.StepUpFor)
	default:
		api.Error(w, r, http.StatusBadRequest, "bad_request", "send the password or a passkey's answer")
		return
	}
	switch {
	case errors.Is(err, signin.ErrWait):
		w.Header().Set("Retry-After", strconv.Itoa(int(h.o.SignIn.FailureWait.Seconds())+1))
		api.Error(w, r, http.StatusTooManyRequests, "wait", "too many wrong passwords in a row: wait, then try again")
	case errors.Is(err, signin.ErrPasskeysOff):
		api.Error(w, r, http.StatusBadRequest, "passkeys_off", "passkeys are switched off")
	case errors.Is(err, signin.ErrRefused):
		// The reason is for the log, never for the answer.
		h.o.Log.Printf("%s step-up refused for tenant %s: %v", c.RequestID, c.Store.TenantID(), err)
		api.Error(w, r, http.StatusForbidden, "refused", "that was not your password or your passkey")
	case errors.Is(err, signin.ErrNoSession):
		api.Error(w, r, http.StatusUnauthorized, "no_session", "sign in first")
	case err != nil:
		h.o.Log.Printf("%s step-up failed for tenant %s: %v", c.RequestID, c.Store.TenantID(), err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the step-up failed")
	default:
		api.WriteJSON(w, http.StatusOK, StepUpReply{AdminUntil: until})
	}
}

// assertion reads a passkey's answer from its JSON form.
func assertion(p PasskeyCredential) (signin.PasskeyAssertion, bool) {
	var a signin.PasskeyAssertion
	fields := []struct {
		in  string
		out *[]byte
	}{
		{p.RawID, &a.CredentialID},
		{p.Response.ClientDataJSON, &a.ClientDataJSON},
		{p.Response.AuthenticatorData, &a.AuthenticatorData},
		{p.Response.Signature, &a.Signature},
		{p.Response.UserHandle, &a.UserHandle},
	}
	for _, f := range fields {
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(f.in, "="))
		if err != nil || len(b) == 0 {
			return signin.PasskeyAssertion{}, false
		}
		*f.out = b
	}
	return a, true
}

// b64 writes bytes as base64url, as WebAuthn's JSON forms do.
func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// passkeyPrompt hands out a passkey prompt for a step-up.
func (h handlers) passkeyPrompt(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !c.Admin {
		refuseNotAdmin(w, r)
		return
	}
	p, err := signin.BeginPasskeyStepUp(c.Store, h.o.SignIn)
	switch {
	case errors.Is(err, signin.ErrPasskeysOff):
		api.Error(w, r, http.StatusBadRequest, "passkeys_off", "passkeys are switched off")
		return
	case err != nil:
		h.o.Log.Printf("%s a step-up prompt for tenant %s failed: %v", c.RequestID, c.Store.TenantID(), err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the passkey prompt could not be made")
		return
	}
	reply := PasskeyPromptReply{
		Challenge:        b64(p.Challenge),
		RPID:             p.RPID,
		AllowCredentials: []AllowedCredential{},
		UserVerification: "required",
		Timeout:          time.Until(p.Expires).Milliseconds(),
	}
	for _, id := range p.Credentials {
		reply.AllowCredentials = append(reply.AllowCredentials, AllowedCredential{Type: "public-key", ID: b64(id)})
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

// endStepUp closes the admin area on the caller's session.
func (h handlers) endStepUp(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !c.Admin {
		refuseNotAdmin(w, r)
		return
	}
	if err := signin.EndStepUp(c.Store, c.Session.ID); err != nil {
		h.o.Log.Printf("%s ending the step-up of tenant %s failed: %v", c.RequestID, c.Store.TenantID(), err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the admin area could not be closed")
		return
	}
	api.WriteJSON(w, http.StatusOK, EndStepUpReply{Ended: true})
}

// people lists every person on the install, through the view.
func (h handlers) people(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !c.Admin {
		refuseNotAdmin(w, r)
		return
	}
	list, err := h.o.View.People(r.Context(), asker(r, c))
	switch {
	case errors.Is(err, adminview.ErrNotAdmin):
		refuseNotAdmin(w, r)
	case err != nil:
		h.o.Log.Printf("%s the admin's list of people failed: %v", c.RequestID, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the list of people could not be read")
	default:
		api.WriteJSON(w, http.StatusOK, PeopleReply{People: list})
	}
}
