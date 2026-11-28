package adminapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/adminview"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/signin"
)

const signInAction = "POST " + api.AdminPrefix + "session"

// signedIn reports whether a call came through the admin door with an
// admin's session. The door refuses every other call first; this is here
// so a handler never runs on the strength of the door alone.
func signedIn(w http.ResponseWriter, r *http.Request, c api.Call) bool {
	if c.Admin == nil || c.AdminSession.AccountID == "" || c.Store != nil {
		api.Error(w, r, http.StatusUnauthorized, "no_session", "sign in first")
		return false
	}
	return true
}

func sessionReply(acc adminaccount.Account, s adminaccount.Session, o adminaccount.Options) SessionReply {
	return SessionReply{
		Account: Account{ID: acc.ID, Name: acc.Name},
		Session: SessionInfo{ID: s.ID, Method: s.Method, Created: s.Created, Expires: s.Expires, IdleUntil: s.LastUsed.Add(o.IdleFor)},
	}
}

// b64 writes bytes as base64url, as WebAuthn's JSON forms do.
func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// unb64 reads base64url, with or without padding. Empty is an error.
func unb64(s string) ([]byte, bool) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	return b, err == nil && len(b) > 0
}

// assertion reads a passkey's answer from its JSON form.
func assertion(p PasskeyCredential) (signin.PasskeyAssertion, bool) {
	var a signin.PasskeyAssertion
	for _, f := range []struct {
		in  string
		out *[]byte
	}{
		{p.RawID, &a.CredentialID},
		{p.Response.ClientDataJSON, &a.ClientDataJSON},
		{p.Response.AuthenticatorData, &a.AuthenticatorData},
		{p.Response.Signature, &a.Signature},
		{p.Response.UserHandle, &a.UserHandle},
	} {
		b, ok := unb64(f.in)
		if !ok {
			return signin.PasskeyAssertion{}, false
		}
		*f.out = b
	}
	return a, true
}

// signIn is the admin's door. It is not behind the admin door, because the
// call has no session yet, so it writes its own entries in the admin audit
// log: the actor is the account when it is known, and admin when it is not.
// A sign-in that cannot be recorded does not stand.
func (h handlers) signIn(w http.ResponseWriter, r *http.Request) {
	id := audit.RequestID(r.Context())
	var body SignInBody
	if !api.ReadJSON(w, r, &body) {
		return
	}
	db, err := h.o.Open()
	if err != nil {
		h.o.Log.Printf("%s the admin database could not be opened: %v", id, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "sign-in failed")
		return
	}
	defer db.Close()
	start := time.Now()
	actor := adminaccount.ActorUnknown
	record := func(status int) error {
		outcome := audit.OutcomeOK
		if status >= 500 {
			outcome = audit.OutcomeError
		} else if status >= 400 {
			outcome = audit.OutcomeRefused
		}
		_, err := db.Append(context.WithoutCancel(r.Context()), audit.Entry{
			Kind: audit.KindAPI, RequestID: id, Actor: actor, Action: signInAction,
			Outcome: outcome, Status: status, DurationMS: time.Since(start).Milliseconds(),
		}, body.Password)
		return err
	}
	var (
		token string
		sess  adminaccount.Session
		acc   adminaccount.Account
	)
	switch {
	case body.Password != "" && body.Passkey != nil:
		record(http.StatusBadRequest)
		api.Error(w, r, http.StatusBadRequest, "bad_request", "send a password or a passkey's answer, not both")
		return
	case body.Passkey != nil:
		a, ok := assertion(*body.Passkey)
		if !ok {
			record(http.StatusBadRequest)
			api.Error(w, r, http.StatusBadRequest, "bad_request", "the passkey's answer is not base64url in the shape PublicKeyCredential.toJSON() gives")
			return
		}
		token, sess, acc, err = db.PasskeySignIn(a, h.o.Accounts)
	default:
		token, sess, acc, err = db.SignIn(body.Account, body.Password, h.o.Accounts)
	}
	if acc.ID != "" {
		actor = adminaccount.ActorPrefix + acc.ID
	}
	switch {
	case errors.Is(err, adminaccount.ErrWait):
		record(http.StatusTooManyRequests)
		w.Header().Set("Retry-After", strconv.Itoa(int(h.o.Accounts.SignIn.FailureWait.Seconds())+1))
		api.Error(w, r, http.StatusTooManyRequests, "wait", "too many wrong passwords in a row: wait, then try again")
	case errors.Is(err, adminaccount.ErrPasskeysOff):
		record(http.StatusBadRequest)
		api.Error(w, r, http.StatusBadRequest, "passkeys_off", "passkeys are switched off")
	case errors.Is(err, adminaccount.ErrRefused):
		record(http.StatusUnauthorized)
		// The reason is for the log, never for the answer.
		h.o.Log.Printf("%s admin sign-in refused: %v", id, err)
		api.Error(w, r, http.StatusUnauthorized, "refused", "sign-in refused")
	case err != nil:
		record(http.StatusInternalServerError)
		h.o.Log.Printf("%s admin sign-in failed: %v", id, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "sign-in failed")
	default:
		if err := record(http.StatusOK); err != nil {
			db.SignOut(token)
			h.o.Log.Printf("%s an admin sign-in could not be recorded: %v", id, err)
			api.Error(w, r, http.StatusInternalServerError, "not_recorded", "the call could not be recorded, so it was not run")
			return
		}
		api.SetAdminCookie(w, r, token, sess.Expires, h.o.SecureCookie)
		api.WriteJSON(w, http.StatusOK, sessionReply(acc, sess, h.o.Accounts))
	}
}

// promptReply writes a passkey prompt in its JSON form.
func promptReply(p signin.PasskeyPrompt) PasskeyPromptReply {
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
	return reply
}

// signInPrompt hands out a passkey prompt for signing in. It names no
// account: the browser offers the admin's own passkey.
func (h handlers) signInPrompt(w http.ResponseWriter, r *http.Request) {
	id := audit.RequestID(r.Context())
	db, err := h.o.Open()
	if err != nil {
		h.o.Log.Printf("%s the admin database could not be opened: %v", id, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the passkey prompt could not be made")
		return
	}
	defer db.Close()
	p, err := db.BeginPasskeySignIn(h.o.Accounts.SignIn)
	switch {
	case errors.Is(err, adminaccount.ErrPasskeysOff):
		api.Error(w, r, http.StatusBadRequest, "passkeys_off", "passkeys are switched off")
	case err != nil:
		h.o.Log.Printf("%s an admin sign-in prompt failed: %v", id, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the passkey prompt could not be made")
	default:
		api.WriteJSON(w, http.StatusOK, promptReply(p))
	}
}

// whoAmI answers which admin account is signed in.
func (h handlers) whoAmI(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	acc, err := c.Admin.Account(c.AdminSession.AccountID)
	if err != nil {
		h.o.Log.Printf("%s the admin account could not be read: %v", c.RequestID, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the account could not be read")
		return
	}
	api.WriteJSON(w, http.StatusOK, sessionReply(acc, c.AdminSession, h.o.Accounts))
}

// signOut ends the admin's session and clears the cookie.
func (h handlers) signOut(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	token := api.AdminToken(r)
	if !adminaccount.IsToken(token) {
		token = api.Token(r)
	}
	if err := c.Admin.SignOut(token); err != nil && !errors.Is(err, adminaccount.ErrNoSession) {
		h.o.Log.Printf("%s admin sign-out failed: %v", c.RequestID, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "sign-out failed")
		return
	}
	api.ClearAdminCookie(w, r, h.o.SecureCookie)
	api.WriteJSON(w, http.StatusOK, SignedOutReply{SignedOut: true})
}

// people lists every person on the install, through the view.
func (h handlers) people(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	list, err := h.o.View.People(r.Context(), adminview.Asker{AdminID: c.AdminSession.AccountID, RequestID: c.RequestID, Action: r.Method + " " + r.URL.Path})
	switch {
	case errors.Is(err, adminview.ErrNotAdmin):
		api.Error(w, r, http.StatusUnauthorized, "no_session", "sign in first")
	case err != nil:
		h.o.Log.Printf("%s the admin's list of people failed: %v", c.RequestID, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the list of people could not be read")
	default:
		api.WriteJSON(w, http.StatusOK, PeopleReply{People: list})
	}
}

// passkeys lists the admin's own passkeys.
func (h handlers) passkeys(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	list, err := c.Admin.Passkeys(c.AdminSession.AccountID)
	if err != nil {
		h.o.Log.Printf("%s the admin's passkeys could not be read: %v", c.RequestID, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the passkeys could not be read")
		return
	}
	reply := PasskeysReply{Passkeys: []Passkey{}}
	for _, pk := range list {
		reply.Passkeys = append(reply.Passkeys, Passkey{ID: pk.ID, Name: pk.Name, Created: pk.Created, LastUsed: pk.LastUsed})
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

// addPrompt hands out a prompt to add a passkey to the admin's account.
func (h handlers) addPrompt(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	p, err := c.Admin.BeginAddPasskey(c.AdminSession.AccountID, h.o.Accounts.SignIn)
	switch {
	case errors.Is(err, adminaccount.ErrPasskeysOff):
		api.Error(w, r, http.StatusBadRequest, "passkeys_off", "passkeys are switched off")
		return
	case err != nil:
		h.o.Log.Printf("%s an admin passkey prompt failed: %v", c.RequestID, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the passkey prompt could not be made")
		return
	}
	acc, err := c.Admin.Account(c.AdminSession.AccountID)
	if err != nil {
		h.o.Log.Printf("%s the admin account could not be read: %v", c.RequestID, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the passkey prompt could not be made")
		return
	}
	reply := promptReply(p)
	reply.User = &PasskeyUser{ID: b64(p.UserID), Name: acc.Name, DisplayName: acc.Name}
	reply.Algorithms = signin.PasskeyAlgorithms()
	api.WriteJSON(w, http.StatusOK, reply)
}

// addPasskey keeps the passkey the browser made.
func (h handlers) addPasskey(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	var body AddPasskeyBody
	if !api.ReadJSON(w, r, &body) {
		return
	}
	var cr signin.PasskeyCreation
	for _, f := range []struct {
		in  string
		out *[]byte
	}{{body.RawID, &cr.CredentialID}, {body.ClientDataJSON, &cr.ClientDataJSON}, {body.AuthenticatorData, &cr.AuthenticatorData}, {body.PublicKey, &cr.PublicKey}} {
		b, ok := unb64(f.in)
		if !ok {
			api.Error(w, r, http.StatusBadRequest, "bad_request", "the new passkey is not base64url in the shape the browser gives")
			return
		}
		*f.out = b
	}
	pk, err := c.Admin.AddPasskey(c.AdminSession.AccountID, body.Name, cr, h.o.Accounts.SignIn)
	switch {
	case errors.Is(err, adminaccount.ErrPasskeysOff):
		api.Error(w, r, http.StatusBadRequest, "passkeys_off", "passkeys are switched off")
	case errors.Is(err, adminaccount.ErrRefused):
		h.o.Log.Printf("%s an admin passkey was refused: %v", c.RequestID, err)
		api.Error(w, r, http.StatusBadRequest, "refused", "the passkey could not be added")
	case errors.Is(err, adminaccount.ErrPasskeyName):
		api.Error(w, r, http.StatusBadRequest, "bad_request", err.Error())
	case err != nil:
		h.o.Log.Printf("%s an admin passkey could not be added: %v", c.RequestID, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the passkey could not be added")
	default:
		api.WriteJSON(w, http.StatusOK, Passkey{ID: pk.ID, Name: pk.Name, Created: pk.Created})
	}
}

// removePasskey forgets one of the admin's passkeys.
func (h handlers) removePasskey(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	err := c.Admin.RemovePasskey(c.AdminSession.AccountID, r.PathValue("id"))
	switch {
	case errors.Is(err, adminaccount.ErrNoPasskey):
		api.Error(w, r, http.StatusNotFound, "not_found", "there is no such passkey")
	case err != nil:
		h.o.Log.Printf("%s an admin passkey could not be removed: %v", c.RequestID, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", "the passkey could not be removed")
	default:
		api.WriteJSON(w, http.StatusOK, RemovedReply{Removed: true})
	}
}
