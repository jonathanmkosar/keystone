package connectors

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/google"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The routes behind Connect Google on Settings → Connectors (P12-02). Each
// acts for the signed-in person on their own tenant. No route returns a
// token, a code, a verifier or the client's secret, and none is a door: the
// browser comes back from Google to a page, which posts the code here with
// the session it already has.

// GoogleStatus answers the Google routes: whether Google is set up for the
// person, whether they are connected and as whom, and each service.
type GoogleStatus struct {
	// SetUp says the OAuth client's id and secret are there. Missing says,
	// in words, what is not, for the admin to add.
	SetUp   bool     `json:"set_up"`
	Missing []string `json:"missing"`
	// Connected says a sign-in is stored. Email is the account, and Scopes
	// what Google granted.
	Connected   bool      `json:"connected"`
	Email       string    `json:"email,omitempty"`
	Scopes      []string  `json:"scopes"`
	ConnectedAt time.Time `json:"connected_at,omitzero"`
	// Services are Gmail and Calendar, in that order.
	Services []GoogleService `json:"services"`
	// Redirect is the admin's fixed return address, when one is set; else
	// the page's own origin with RedirectPath is used. Either is what the
	// OAuth client must have registered.
	Redirect     string `json:"redirect,omitempty"`
	RedirectPath string `json:"redirect_path"`
}

// GoogleService is one service in a GoogleStatus.
type GoogleService struct {
	Service string `json:"service"`
	Label   string `json:"label"`
	// Granted says Google granted the service's scopes.
	Granted bool `json:"granted"`
	// Added says its connector is there, and Enabled whether it is on.
	Added   bool   `json:"added"`
	Enabled bool   `json:"enabled"`
	Tools   int    `json:"tools"`
	Problem string `json:"problem,omitempty"`
}

// GoogleStartBody starts a sign-in. Services are gmail, calendar or both;
// none means both.
type GoogleStartBody struct {
	Services []string `json:"services"`
}

// GoogleStartReply is where to send the browser.
type GoogleStartReply struct {
	// URL is Google's sign-in page for this sign-in.
	URL string `json:"url"`
	// Redirect is the address Google sends the browser back to.
	Redirect string `json:"redirect"`
}

// GoogleFinishBody is what Google sent the browser back with: the state,
// and a code, or an error such as access_denied.
type GoogleFinishBody struct {
	State string `json:"state"`
	Code  string `json:"code"`
	Error string `json:"error"`
}

func googleRoutes(o Options) []api.Route {
	const root = api.Prefix + "google"
	h := googleHandlers{o: o}
	return []api.Route{
		{Method: "GET", Path: root, Handle: h.status, Reply: GoogleStatus{},
			Summary: "The person's Google sign-in for the Gmail and Calendar connectors: whether Google is set up on this install and what is missing, the account connected, and each service. Never a token."},
		{Method: "POST", Path: root + "/start", Handle: h.start, Body: GoogleStartBody{}, Reply: GoogleStartReply{},
			Summary: "Start a Google sign-in with PKCE for Gmail, Calendar or both, and answer with Google's sign-in address. Google sends the browser back to the page at /settings/connectors/google, or to agents.google_redirect_url."},
		{Method: "POST", Path: root + "/finish", Handle: h.finish, Body: GoogleFinishBody{}, Reply: GoogleStatus{},
			Summary: "Finish a Google sign-in with the state and code Google sent the browser back with: the refresh token is kept in the person's vault, and the Gmail and Calendar connectors are added for what was granted."},
		{Method: "DELETE", Path: root, Handle: h.disconnect, Reply: GoogleStatus{},
			Summary: "Disconnect Google: the sign-in is revoked at Google, taken out of the vault, and the Gmail and Calendar connectors and their tools' policies are removed."},
	}
}

type googleHandlers struct{ o Options }

func (h googleHandlers) googleStatus(ctx context.Context, s *tenant.Store) (GoogleStatus, error) {
	_, missing := googleClient(ctx, s)
	st := GoogleStatus{SetUp: len(missing) == 0, Missing: missing, Scopes: []string{}, RedirectPath: GoogleRedirectPath, Redirect: googleRedirectSetting(ctx, s)}
	if st.Missing == nil {
		st.Missing = []string{}
	}
	var scopes, connected string
	err := s.DB().QueryRowContext(ctx, "SELECT email, scopes, connected_at FROM google_accounts WHERE id = 1").Scan(&st.Email, &scopes, &connected)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return st, err
	}
	refresh, ok, verr := s.Secrets().Get(GoogleSecret)
	st.Connected = err == nil && verr == nil && ok && refresh != ""
	if err == nil {
		st.Scopes = strings.Fields(scopes)
		st.ConnectedAt, _ = time.Parse(timeLayout, connected)
	}
	for _, svc := range google.Services() {
		gs := GoogleService{Service: string(svc), Label: svc.Label(), Granted: st.Connected && svc.Granted(st.Scopes)}
		if k, err := Get(ctx, s, string(svc)); err == nil && k.Transport == TransportGoogle {
			gs.Added, gs.Enabled, gs.Tools, gs.Problem = true, k.Enabled, len(k.Tools), k.Problem
		} else if err == nil {
			gs.Problem = "a connector of another kind is named " + string(svc) + ": remove it before you connect Google"
		}
		st.Services = append(st.Services, gs)
	}
	return st, nil
}

func (h googleHandlers) status(w http.ResponseWriter, r *http.Request, c api.Call) {
	st, err := h.googleStatus(r.Context(), c.Store)
	if err != nil {
		failed(w, r)
		return
	}
	api.WriteJSON(w, http.StatusOK, st)
}

// redirectFor is the address Google sends the browser back to: the admin's
// setting when there is one, else the origin of the page that asked, which
// the browser states in the Origin of a same-origin POST, with the page's
// path. The API refuses a write from another site before this runs.
func redirectFor(r *http.Request, setting string) (string, bool) {
	if setting != "" {
		u, err := url.Parse(setting)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
			return "", false
		}
		return setting, true
	}
	origin := r.Header.Get("Origin")
	u, err := url.Parse(origin)
	if origin == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return "", false
	}
	return u.Scheme + "://" + u.Host + GoogleRedirectPath, true
}

func (h googleHandlers) start(w http.ResponseWriter, r *http.Request, c api.Call) {
	var b GoogleStartBody
	if !api.ReadJSON(w, r, &b) {
		return
	}
	ctx := r.Context()
	var services []string
	for _, name := range b.Services {
		svc, ok := google.ParseService(name)
		if !ok {
			api.Error(w, r, http.StatusBadRequest, "bad_request", "a service is gmail or calendar")
			return
		}
		services = append(services, string(svc))
	}
	if len(services) == 0 {
		services = []string{string(google.Gmail), string(google.Calendar)}
	}
	client, missing := googleClient(ctx, c.Store)
	if len(missing) > 0 {
		api.Error(w, r, http.StatusConflict, "not_set_up", "Google is not set up on this install: the admin adds "+strings.Join(missing, ", and "))
		return
	}
	redirect, ok := redirectFor(r, googleRedirectSetting(ctx, c.Store))
	if !ok {
		api.Error(w, r, http.StatusBadRequest, "no_redirect", "Keystone cannot tell the address Google should send you back to: the admin sets "+KeyGoogleRedirect)
		return
	}
	state, err := google.NewSecret()
	if err != nil {
		failed(w, r)
		return
	}
	verifier, err := google.NewSecret()
	if err != nil {
		failed(w, r)
		return
	}
	db := c.Store.DB()
	cutoff := time.Now().UTC().Add(-h.o.Google.Window).Format(timeLayout)
	if _, err := db.ExecContext(ctx, "DELETE FROM google_pending WHERE created_at < ?", cutoff); err != nil {
		failed(w, r)
		return
	}
	// At most a few sign-ins wait at once; the oldest goes.
	if _, err := db.ExecContext(ctx, "DELETE FROM google_pending WHERE state NOT IN (SELECT state FROM google_pending ORDER BY created_at DESC LIMIT 4)"); err != nil {
		failed(w, r)
		return
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO google_pending(state, verifier, redirect, services, created_at) VALUES(?, ?, ?, ?, ?)",
		state, verifier, redirect, strings.Join(services, " "), stamp()); err != nil {
		failed(w, r)
		return
	}
	var asked []google.Service
	for _, name := range services {
		svc, _ := google.ParseService(name)
		asked = append(asked, svc)
	}
	api.WriteJSON(w, http.StatusOK, GoogleStartReply{
		URL:      google.AuthURL(h.o.Google.Endpoints, client.ID, redirect, state, google.Challenge(verifier), google.ScopesFor(asked)),
		Redirect: redirect,
	})
}

// refusals are the words for the errors Google sends the browser back
// with.
var refusals = map[string]string{
	"access_denied": "You did not allow Keystone to use your Google account, so nothing was connected.",
}

func (h googleHandlers) finish(w http.ResponseWriter, r *http.Request, c api.Call) {
	var b GoogleFinishBody
	if !api.ReadJSON(w, r, &b) {
		return
	}
	ctx := r.Context()
	s := c.Store
	// The state is used once: it is taken out before anything else.
	var verifier, redirect, services, created string
	err := s.DB().QueryRowContext(ctx, "SELECT verifier, redirect, services, created_at FROM google_pending WHERE state = ?", b.State).Scan(&verifier, &redirect, &services, &created)
	if err == nil {
		var res sql.Result
		if res, err = s.DB().ExecContext(ctx, "DELETE FROM google_pending WHERE state = ?", b.State); err == nil {
			if n, _ := res.RowsAffected(); n != 1 {
				err = sql.ErrNoRows
			}
		}
	}
	at, perr := time.Parse(timeLayout, created)
	if err != nil || b.State == "" || perr != nil || time.Since(at) > h.o.Google.Window {
		api.Error(w, r, http.StatusBadRequest, "bad_state", "This sign-in was not started here, was used already, or took too long: press Connect Google again.")
		return
	}
	if b.Error != "" {
		msg, ok := refusals[b.Error]
		if !ok {
			msg = "Google did not sign you in, so nothing was connected."
		}
		api.Error(w, r, http.StatusBadRequest, "refused", msg)
		return
	}
	if b.Code == "" || len(b.Code) > 2048 {
		api.Error(w, r, http.StatusBadRequest, "bad_request", "Google sent no code back")
		return
	}
	client, missing := googleClient(ctx, s)
	if len(missing) > 0 {
		api.Error(w, r, http.StatusConflict, "not_set_up", "Google is not set up on this install: the admin adds "+strings.Join(missing, ", and "))
		return
	}
	tctx, cancel := context.WithTimeout(ctx, h.o.Google.Timeout)
	grant, err := google.Exchange(tctx, h.o.googleHTTP(), h.o.Google.Endpoints, client, b.Code, verifier, redirect, h.o.MaxBytes)
	cancel()
	if err != nil {
		g := &googleReach{client: client, refresh: b.Code}
		g.note(verifier)
		api.Error(w, r, http.StatusBadGateway, "google_refused", g.redact(err.Error()))
		return
	}
	refresh := grant.Refresh
	if refresh == "" {
		if kept, ok, err := s.Secrets().Get(GoogleSecret); err == nil && ok {
			refresh = kept // Google sends a refresh token only now and then
		}
	}
	if refresh == "" {
		api.Error(w, r, http.StatusBadGateway, "google_refused", "Google did not give Keystone a lasting sign-in: press Connect Google again")
		return
	}
	if err := s.Secrets().Set(GoogleSecret, refresh); err != nil {
		failed(w, r)
		return
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO google_accounts(id, email, scopes, connected_at) VALUES(1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET email = excluded.email, scopes = excluded.scopes, connected_at = excluded.connected_at`,
		grant.Email, strings.Join(grant.Scopes, " "), stamp()); err != nil {
		failed(w, r)
		return
	}
	// The access token that came with the sign-in serves the first calls.
	key, fingerprint := googleKey(s, GoogleSecret, client, refresh)
	h.o.Google.tokens.put(key, fingerprint, grant.Access, grant.Expires)
	for _, name := range strings.Fields(services) {
		svc, ok := google.ParseService(name)
		if !ok || !svc.Granted(grant.Scopes) {
			continue // the status says it was not granted
		}
		if err := h.ensure(ctx, s, svc); err != nil && !errors.Is(err, ErrExists) {
			failed(w, r)
			return
		}
	}
	st, err := h.googleStatus(ctx, s)
	if err != nil {
		failed(w, r)
		return
	}
	api.WriteJSON(w, http.StatusOK, st)
}

// ensure adds a service's connector, or switches it on again, and keeps its
// tools. A connector of another kind under the name is left alone and
// ErrExists is returned; the status names it.
func (h googleHandlers) ensure(ctx context.Context, s *tenant.Store, svc google.Service) error {
	c := Connector{Name: string(svc), Label: svc.Label(), Transport: TransportGoogle, Secret: GoogleSecret, Enabled: true, Outside: true}
	k, err := Get(ctx, s, c.Name)
	switch {
	case errors.Is(err, ErrNotFound):
		// Built in, so it is not counted against agents.connectors_max.
		if k, err = Add(ctx, s, c, ByPerson, 0); err != nil {
			return err
		}
	case err != nil:
		return err
	case k.Transport != TransportGoogle:
		return ErrExists
	default:
		c.Label, c.Outside = k.Label, k.Outside // the person's own choices stay
		if k, err = Replace(ctx, s, c); err != nil {
			return err
		}
	}
	return saveGoogleTools(ctx, s, k.Connector, h.o.MaxTools)
}

func (h googleHandlers) disconnect(w http.ResponseWriter, r *http.Request, c api.Call) {
	ctx := r.Context()
	s := c.Store
	if refresh, ok, err := s.Secrets().Get(GoogleSecret); err == nil && ok && refresh != "" {
		// Revoked at Google first, as far as it can be: a sign-in Google
		// still honours is not left behind by a disconnect that failed.
		tctx, cancel := context.WithTimeout(ctx, h.o.Google.Timeout)
		_ = google.Revoke(tctx, h.o.googleHTTP(), h.o.Google.Endpoints, refresh)
		cancel()
		h.o.Google.tokens.drop(s.TenantID() + "/" + GoogleSecret)
	}
	for _, svc := range google.Services() {
		if k, err := Get(ctx, s, string(svc)); err == nil && k.Transport == TransportGoogle {
			if err := Remove(ctx, s, k.Name); err != nil {
				failed(w, r)
				return
			}
		}
	}
	if err := s.Secrets().Delete(GoogleSecret); err != nil {
		failed(w, r)
		return
	}
	if _, err := s.DB().ExecContext(ctx, "DELETE FROM google_accounts"); err != nil {
		failed(w, r)
		return
	}
	if _, err := s.DB().ExecContext(ctx, "DELETE FROM google_pending"); err != nil {
		failed(w, r)
		return
	}
	st, err := h.googleStatus(ctx, s)
	if err != nil {
		failed(w, r)
		return
	}
	api.WriteJSON(w, http.StatusOK, st)
}
