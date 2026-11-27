package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Shell is what the shell's own routes need. The caller reads the values
// from the settings.
type Shell struct {
	Install *tenant.Install
	// SignIn holds the sign-in settings.
	SignIn signin.Options
	// ListPeople says whether the door lists the people on the install, so
	// the sign-in screen can ask who is signing in.
	ListPeople bool
	// SecureCookie marks the session cookie for HTTPS only.
	SecureCookie bool
	// PageLimit is the most rows a list returns in one call.
	PageLimit int
	// Version is Keystone's version, for the OpenAPI document.
	Version string
	// Routes returns every route that is served, for the OpenAPI document.
	Routes func() []Route
	// Log is the server's own log. A sign-in that is refused has no session
	// and so no tenant's audit log to go in for certain; it is noted here.
	Log *Log
}

// The shapes the shell's routes take and return.

// Person is someone who can sign in to this install.
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PeopleReply answers GET /api/v1/signin/people.
type PeopleReply struct {
	// Listed is false when the install does not say who is on it. People
	// is then empty, and the person signing in must know their id.
	Listed bool     `json:"listed"`
	People []Person `json:"people"`
}

// SignInBody is what POST /api/v1/session takes.
type SignInBody struct {
	// Tenant is the id of the person signing in.
	Tenant   string `json:"tenant"`
	Password string `json:"password"`
}

// SessionInfo describes a session. It never holds the session's token.
type SessionInfo struct {
	ID      string    `json:"id"`
	Method  string    `json:"method"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
	// AdminUntil is when this session's admin step-up ends, and is left
	// out when it has none (P15-01).
	AdminUntil time.Time `json:"admin_until,omitzero"`
}

// SessionReply answers the session routes.
type SessionReply struct {
	Person  Person      `json:"person"`
	Session SessionInfo `json:"session"`
	// Admin says the person carries the admin mark, so the interface can
	// show the admin area to them alone (P15-01). The admin routes check
	// the mark themselves.
	Admin bool `json:"admin"`
}

// SignedOutReply answers DELETE /api/v1/session.
type SignedOutReply struct {
	SignedOut bool `json:"signed_out"`
}

// AuditReply answers GET /api/v1/audit.
type AuditReply struct {
	Entries []audit.Entry `json:"entries"`
	// NextAfter is the value of after that reads the next page. It is zero
	// when this page was the last.
	NextAfter int64 `json:"next_after"`
}

// Tick is the data of the test stream's events.
type Tick struct {
	N int `json:"n"`
}

const signInAction = "POST " + Prefix + "session"

// ShellRoutes returns the routes the shell brings itself: the door, the
// session, the person's own audit log, the OpenAPI document and a stream
// for testing a client's event reader.
func ShellRoutes(s Shell) []Route {
	logf := s.Log.Printf
	name := func(id string) string {
		for _, t := range s.Install.Tenants() {
			if t.ID == id {
				return t.Name
			}
		}
		return ""
	}
	info := func(sess signin.Session) SessionInfo {
		return SessionInfo{ID: sess.ID, Method: sess.Method, Created: sess.Created, Expires: sess.Expires, AdminUntil: sess.AdminUntil}
	}
	// marked reads the admin mark from keystone.json now. A registry that
	// cannot be read says no.
	marked := func(id string) bool {
		ok, err := s.Install.IsAdmin(id)
		if err != nil {
			logf("the admin mark of tenant %s could not be read: %v", id, err)
		}
		return ok && err == nil
	}

	people := func(w http.ResponseWriter, r *http.Request) {
		reply := PeopleReply{Listed: s.ListPeople, People: []Person{}}
		if s.ListPeople {
			for _, t := range s.Install.Tenants() {
				reply.People = append(reply.People, Person{ID: t.ID, Name: t.Name})
			}
		}
		WriteJSON(w, http.StatusOK, reply)
	}

	// signIn is the door. It is not inside audit.Wrap, because the call has
	// no session yet, so it writes its own entry in the audit log of the
	// tenant the sign-in was for. An unknown tenant has no log, and goes in
	// the server's log alone.
	signIn := func(w http.ResponseWriter, r *http.Request) {
		id := audit.RequestID(r.Context())
		var body SignInBody
		if !ReadJSON(w, r, &body) {
			return
		}
		start := time.Now()
		store, err := s.Install.Store(body.Tenant)
		if err != nil {
			// The same answer as a wrong password, so the door does not say
			// which ids exist.
			logf("%s sign-in refused: there is no such tenant", id)
			Error(w, r, http.StatusUnauthorized, "refused", "sign-in refused")
			return
		}
		defer store.Close()
		record := func(status int) error {
			outcome := audit.OutcomeOK
			if status >= 500 {
				outcome = audit.OutcomeError
			} else if status >= 400 {
				outcome = audit.OutcomeRefused
			}
			_, err := audit.Append(context.WithoutCancel(r.Context()), store, audit.Entry{
				Kind: audit.KindAPI, RequestID: id, Actor: ActorPerson, Action: signInAction,
				Outcome: outcome, Status: status, DurationMS: time.Since(start).Milliseconds(),
			}, body.Password)
			return err
		}
		token, sess, err := signin.SignIn(store, body.Password, s.SignIn)
		switch {
		case errors.Is(err, signin.ErrWait):
			record(http.StatusTooManyRequests)
			logf("%s sign-in refused for tenant %s: waiting after wrong passwords", id, store.TenantID())
			w.Header().Set("Retry-After", strconv.Itoa(int(s.SignIn.FailureWait.Seconds())+1))
			Error(w, r, http.StatusTooManyRequests, "wait", "too many wrong passwords in a row: wait, then try again")
		case errors.Is(err, signin.ErrRefused):
			record(http.StatusUnauthorized)
			logf("%s sign-in refused for tenant %s", id, store.TenantID())
			Error(w, r, http.StatusUnauthorized, "refused", "sign-in refused")
		case err != nil:
			record(http.StatusInternalServerError)
			logf("%s sign-in failed for tenant %s: %v", id, store.TenantID(), err)
			Error(w, r, http.StatusInternalServerError, "failed", "sign-in failed")
		default:
			// A sign-in that cannot be recorded does not stand.
			if err := record(http.StatusOK); err != nil {
				signin.SignOut(store, token)
				logf("%s sign-in for tenant %s could not be recorded: %v", id, store.TenantID(), err)
				Error(w, r, http.StatusInternalServerError, "not_recorded", "the call could not be recorded, so it was not run")
				return
			}
			SetSessionCookie(w, r, token, sess.Expires, s.SecureCookie)
			WriteJSON(w, http.StatusOK, SessionReply{Person: Person{ID: sess.TenantID, Name: name(sess.TenantID)}, Session: info(sess), Admin: marked(sess.TenantID)})
		}
	}

	whoAmI := func(w http.ResponseWriter, r *http.Request, c Call) {
		WriteJSON(w, http.StatusOK, SessionReply{Person: Person{ID: c.Session.TenantID, Name: name(c.Session.TenantID)}, Session: info(c.Session), Admin: marked(c.Session.TenantID)})
	}

	signOut := func(w http.ResponseWriter, r *http.Request, c Call) {
		if err := signin.SignOut(c.Store, Token(r)); err != nil && !errors.Is(err, signin.ErrNoSession) {
			logf("%s sign-out failed: %v", c.RequestID, err)
			Error(w, r, http.StatusInternalServerError, "failed", "sign-out failed")
			return
		}
		ClearSessionCookie(w, r, s.SecureCookie)
		WriteJSON(w, http.StatusOK, SignedOutReply{SignedOut: true})
	}

	auditLog := func(w http.ResponseWriter, r *http.Request, c Call) {
		q := r.URL.Query()
		f := audit.Filter{Kind: audit.Kind(q.Get("kind")), RequestID: q.Get("request_id"), Limit: s.PageLimit}
		if v := q.Get("after"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				Error(w, r, http.StatusBadRequest, "bad_request", "after is a whole number, zero or more")
				return
			}
			f.AfterID = n
		}
		if v := q.Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				Error(w, r, http.StatusBadRequest, "bad_request", "limit is a whole number, one or more")
				return
			}
			f.Limit = min(n, s.PageLimit)
		}
		switch f.Kind {
		case "", audit.KindAPI, audit.KindModel, audit.KindTool:
		default:
			Error(w, r, http.StatusBadRequest, "bad_request", "kind is api, model or tool")
			return
		}
		entries, err := audit.List(r.Context(), c.Store, f)
		if err != nil {
			logf("%s reading the audit log failed: %v", c.RequestID, err)
			Error(w, r, http.StatusInternalServerError, "failed", "the audit log could not be read")
			return
		}
		reply := AuditReply{Entries: entries}
		if reply.Entries == nil {
			reply.Entries = []audit.Entry{}
		}
		if len(entries) == f.Limit {
			reply.NextAfter = entries[len(entries)-1].ID
		}
		WriteJSON(w, http.StatusOK, reply)
	}

	document := func(w http.ResponseWriter, r *http.Request, c Call) {
		if s.Routes == nil {
			Error(w, r, http.StatusNotFound, "no_route", "this server has no OpenAPI document")
			return
		}
		raw, err := Document(s.Routes(), s.Version)
		if err != nil {
			logf("%s the OpenAPI document could not be built: %v", c.RequestID, err)
			Error(w, r, http.StatusInternalServerError, "failed", "the OpenAPI document could not be built")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}

	// testStream counts. It is here so a client's event reader, its
	// reconnecting and its handling of Last-Event-ID can be built and
	// checked before any real stream exists.
	testStream := func(w http.ResponseWriter, r *http.Request, c Call) {
		count := 3
		if v := r.URL.Query().Get("count"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 100 {
				Error(w, r, http.StatusBadRequest, "bad_request", "count is a whole number from 1 to 100")
				return
			}
			count = n
		}
		from := 0
		if v := LastEventID(r); v != "" {
			from, _ = strconv.Atoi(v) // an id this stream did not send starts it again
		}
		stream, err := OpenStream(w, r)
		if err != nil {
			logf("%s %v", c.RequestID, err)
			return
		}
		defer stream.Close()
		for n := max(from, 0) + 1; n <= count; n++ {
			if stream.Send(Event{ID: strconv.Itoa(n), Name: "tick", Data: Tick{N: n}}) != nil {
				return
			}
		}
		stream.Send(Event{Name: "done", Data: Tick{N: count}})
	}

	return []Route{
		{
			Method: "GET", Path: Prefix + "signin/people", Door: true, Open: people,
			Summary: "The people who can sign in to this install, for the sign-in screen. Empty when the signin.list_people setting is off.",
			Reply:   PeopleReply{},
		},
		{
			Method: "POST", Path: Prefix + "session", Door: true, Open: signIn,
			Summary: "Sign in with a password. The session token comes back in a cookie the page's scripts cannot read, and never in the body.",
			Body:    SignInBody{}, Reply: SessionReply{},
		},
		{
			Method: "GET", Path: Prefix + "session", Handle: whoAmI,
			Summary: "Who is signed in, and until when; whether they carry the admin mark, and until when their admin step-up lasts.",
			Reply:   SessionReply{},
		},
		{
			Method: "DELETE", Path: Prefix + "session", Handle: signOut,
			Summary: "Sign out: end this session and forget its cookie.",
			Reply:   SignedOutReply{},
		},
		{
			Method: "GET", Path: Prefix + "audit", Handle: auditLog,
			Summary: "The signed-in person's audit log, oldest first, a page at a time.",
			Query: []Param{
				{"after", "Return the entries written after the entry with this id."},
				{"limit", "The most entries to return. Never more than the server.page_limit setting."},
				{"kind", "Keep one kind of call: api, model or tool."},
				{"request_id", "Keep the entries of one API call."},
			},
			Reply: AuditReply{},
		},
		{
			Method: "GET", Path: Prefix + "openapi.json", Handle: document,
			Summary: "This document.",
		},
		{
			Method: "GET", Path: Prefix + "stream/test", Handle: testStream,
			Summary: "A stream that counts, for building and checking a client's event reader. It honours Last-Event-ID.",
			Query:   []Param{{"count", "How many tick events to send, 1 to 100. Three unless given."}},
			Events: []EventDoc{
				{Name: "tick", Description: "One count. Its id is the count.", Data: Tick{}},
				{Name: "done", Description: "The last event. It has no id.", Data: Tick{}},
			},
		},
	}
}
