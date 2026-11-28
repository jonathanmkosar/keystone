package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/audit"
)

// The admin door (P15-01, decisions 0650 and 0651). An admin is a separate
// account with its own database, data/admin/admin.db, and its own session
// token, ksa_ and 32 random bytes as hex. The shell tells the two kinds of
// token apart by their form, before any database is read:
//
//   - A request whose token has the admin form goes to the admin side. Its
//     session is checked in the admin database, and the call is written in
//     the admin audit log before it runs and when it ends. An admin route
//     then runs, with the admin's session and no tenant store; a person's
//     route is refused, 403 admin_account.
//   - Every other request stays on the person's side, as before. A person's
//     call to an admin path is recorded in their own audit log and refused,
//     403 not_admin.
//
// A person's token is never read on the admin side, and an admin's never
// opens a tenant.

const (
	// AdminPrefix is where every admin route lives. A route under it is
	// marked Admin, and only such a route.
	AdminPrefix = Prefix + "admin/"
	// AdminCookieName is the cookie that carries an admin session's token.
	// It is sent with admin routes alone, so a browser signed in as a
	// person and as an admin keeps the two apart.
	AdminCookieName = "keystone_admin"
	adminCookiePath = AdminPrefix
)

// IsAdminPath reports whether a request path is an admin path.
func IsAdminPath(path string) bool {
	return path == strings.TrimSuffix(AdminPrefix, "/") || strings.HasPrefix(path, AdminPrefix)
}

// AdminToken returns the token in the admin cookie, or "" when there is
// none.
func AdminToken(r *http.Request) string {
	c, err := r.Cookie(AdminCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// adminToken picks the admin token a request carries, by its form alone.
// On an admin path the admin cookie is read first. Elsewhere the person's
// cookie is read first, and the admin cookie only when the person's is
// empty, so a person's session in the same browser always wins on a
// person's route.
func adminToken(r *http.Request) (string, bool) {
	person, admin := Token(r), AdminToken(r)
	if IsAdminPath(r.URL.Path) {
		switch {
		case adminaccount.IsToken(admin):
			return admin, true
		case adminaccount.IsToken(person):
			return person, true
		}
		return "", false
	}
	switch {
	case adminaccount.IsToken(person):
		return person, true
	case person == "" && adminaccount.IsToken(admin):
		return admin, true
	}
	return "", false
}

// checkAdminOptions refuses a list of routes with admin routes and no
// admin session limits.
func checkAdminOptions(o Options, routes []Route) error {
	for _, rt := range routes {
		if rt.Admin && (o.Admin.SessionFor <= 0 || o.Admin.IdleFor <= 0) {
			return errors.New("api: there are admin routes, so admin.session_for and admin.idle_for must be longer than nothing")
		}
	}
	return nil
}

// personDoor is the person's side of the admin door: inside audit.Wrap, so
// a call it refuses is recorded in the person's own log like any other.
func personDoor(inside http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsAdminPath(r.URL.Path) {
			Error(w, r, http.StatusForbidden, "not_admin", "this is an admin account's route, and a person's session cannot open it")
			return
		}
		inside.ServeHTTP(w, r)
	})
}

// adminDoor is the admin side: it checks the admin session, records the
// call in the admin audit log before it runs and when it ends, refuses a
// person's route, and serves an admin route.
func adminDoor(o Options, inside http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := audit.RequestID(r.Context())
		h := heldOf(r.Context())
		token, ok := adminToken(r)
		if h == nil || !ok {
			Error(w, r, http.StatusUnauthorized, "no_session", "sign in first")
			return
		}
		store, err := adminaccount.Open(o.Install.Dir())
		if err != nil {
			o.Log.Printf("%s the admin database could not be opened: %v", id, err)
			Error(w, r, http.StatusInternalServerError, "failed", "the call failed")
			return
		}
		h.admin = store // the shell closes it
		sess, err := store.Check(token, o.Admin)
		switch {
		case errors.Is(err, adminaccount.ErrNoSession):
			Error(w, r, http.StatusUnauthorized, "no_session", "sign in first")
			return
		case err != nil:
			o.Log.Printf("%s an admin session could not be checked: %v", id, err)
			Error(w, r, http.StatusInternalServerError, "failed", "the call failed")
			return
		}
		h.adminSession = sess
		e := audit.Entry{Kind: audit.KindAPI, RequestID: id, Actor: adminaccount.ActorPrefix + sess.AccountID,
			Action: r.Method + " " + r.URL.Path, Outcome: audit.OutcomeStarted}
		if _, err := store.Append(r.Context(), e); err != nil {
			o.Log.Printf("%s an admin call was refused because it could not be recorded: %v", id, err)
			Error(w, r, http.StatusInternalServerError, "not_recorded", "the call could not be recorded, so it was not run")
			return
		}
		start := time.Now()
		rec := &statusWriter{ResponseWriter: w}
		finish := func(status int, received string) {
			if status == 0 {
				status = http.StatusOK
			}
			e.Status, e.Received, e.DurationMS = status, received, time.Since(start).Milliseconds()
			switch {
			case status >= 500:
				e.Outcome = audit.OutcomeError
			case status >= 400:
				e.Outcome = audit.OutcomeRefused
			default:
				e.Outcome = audit.OutcomeOK
			}
			if _, err := store.Append(context.WithoutCancel(r.Context()), e); err != nil {
				o.Log.Printf("%s the end of an admin call could not be recorded: %v", id, err)
			}
		}
		defer func() {
			if p := recover(); p != nil {
				finish(http.StatusInternalServerError, "the handler panicked")
				panic(p)
			}
		}()
		if IsAdminPath(r.URL.Path) {
			inside.ServeHTTP(rec, r)
		} else {
			Error(rec, r, http.StatusForbidden, "admin_account", "an admin account has no person's routes: sign in as a person to use them")
		}
		finish(rec.status, "")
	})
}

// serveAdminRoute runs an admin route's handler with the admin's session.
func serveAdminRoute(w http.ResponseWriter, r *http.Request, h *held, handle func(http.ResponseWriter, *http.Request, Call)) {
	if h == nil || h.admin == nil || h.adminSession.AccountID == "" {
		// Not reachable: a person's side refuses an admin path first.
		Error(w, r, http.StatusUnauthorized, "no_session", "sign in first")
		return
	}
	handle(w, r, Call{Admin: h.admin, AdminSession: h.adminSession, RequestID: audit.RequestID(r.Context())})
}

// closeAdmin closes the admin database an admin call opened, and returns
// what the server's log line says of the admin: their account's id.
func closeAdmin(h *held, log *Log, id string) string {
	if h.admin == nil {
		return ""
	}
	if err := h.admin.Close(); err != nil {
		log.Printf("%s closing the admin database: %v", id, err)
	}
	if h.adminSession.AccountID == "" {
		return " admin=-"
	}
	return " admin=" + h.adminSession.AccountID
}

// SetAdminCookie gives the browser an admin session's token, in a cookie
// the page's scripts cannot read, sent only with Keystone's own admin
// routes.
func SetAdminCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminCookieName,
		Value:    token,
		Path:     adminCookiePath,
		Expires:  expires,
		HttpOnly: true,
		Secure:   secure || r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearAdminCookie tells the browser to forget the admin cookie.
func ClearAdminCookie(w http.ResponseWriter, r *http.Request, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     AdminCookieName,
		Value:    "",
		Path:     adminCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure || r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}
