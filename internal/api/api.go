// Package api is the shell of Keystone's HTTP API: what a route is, the one
// handler that serves a list of routes, the session cookie, request ids,
// the shape of an error, server-sent events, and the OpenAPI document.
//
// A route is a row of data. Handler turns a list of them into one handler
// in which every route, apart from the few door routes a person needs
// before a session exists, sits inside one audit.Wrap. The session cookie
// is read and signin.Resolve is called in one place, and a route's handler
// is handed the one store that session resolved to. It is given no other
// way to a tenant.
//
// This package reads no setting. Its callers pass the values in.
package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

const (
	// Prefix is where every route lives.
	Prefix = "/api/v1/"
	// CookieName is the cookie that carries the session token.
	CookieName = "keystone_session"
	// cookiePath keeps the cookie off requests for the page's own files.
	cookiePath = "/api/"
	// ActorPerson is the actor of a call made with a person's session.
	ActorPerson = "person"
)

// Call is one signed-in call. It is everything a route's handler knows
// about who is calling.
type Call struct {
	// Store is the store of the tenant the session belongs to. The shell
	// opened it and closes it when the handler returns.
	Store *tenant.Store
	// Session is the session the call came with.
	Session signin.Session
	// RequestID names the call in the audit log and in the server's log.
	// Model calls and tool calls made for it take it from the request's
	// context with audit.RequestID.
	RequestID string
}

// Param is one query parameter, for the OpenAPI document.
type Param struct {
	Name        string
	Description string
}

// EventDoc describes one kind of event a stream route sends, for the
// OpenAPI document. Data is a value of the type sent as the event's data.
type EventDoc struct {
	Name        string
	Description string
	Data        any
}

// Route is one route of the API.
type Route struct {
	// Method is GET, POST, PUT, PATCH or DELETE.
	Method string
	// Path starts with Prefix. A part in braces, such as {id}, is read in
	// the handler with r.PathValue("id").
	Path string
	// Summary says in one line what the route does.
	Summary string

	// Handle serves the route for a signed-in call. Every route has one,
	// except a door route.
	Handle func(w http.ResponseWriter, r *http.Request, c Call)

	// Door marks a route that is served before anyone has signed in, and
	// Open serves it. A door route gets no store. The list of door routes
	// is short and is repeated in tools/verify, so adding one fails the
	// tenant isolation check until it is added there too.
	Door bool
	Open func(w http.ResponseWriter, r *http.Request)

	// MaxBodyBytes is the most this route's body may hold, for a route that
	// takes more than Options.MaxBodyBytes, as an upload does (P9-02). Zero,
	// or anything less, means the handler's limit.
	MaxBodyBytes int64

	// The rest describes the route in the OpenAPI document.

	// Query lists the query parameters the route reads.
	Query []Param
	// Body is a value of the type the route reads as its JSON body, or nil.
	Body any
	// Reply is a value of the type the route answers with as JSON, or nil.
	Reply any
	// Events lists what a stream route sends. A route with events answers
	// with server-sent events and not with JSON.
	Events []EventDoc
}

// Options are the values the handler runs with. The caller reads them from
// the settings.
type Options struct {
	// Install is the data folder whose tenants the API serves.
	Install *tenant.Install
	// Log is the server's own log, one line for each call. Nil means none.
	Log *Log
	// MaxBodyBytes is the most a request body may hold.
	MaxBodyBytes int64
	// Heartbeat is how often an open stream sends a line to show it is
	// still there.
	Heartbeat time.Duration
}

var (
	methods   = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
	pathShape = regexp.MustCompile(`^/api/v1/[a-z0-9_.-]+(/([a-z0-9_.-]+|\{[a-z_]+\}))*$`)
)

// Check reports what is wrong with a list of routes: a route outside the
// prefix, a method that is not one, the same route twice, or a route whose
// handler does not match its side of the door.
func Check(routes []Route) error {
	seen := map[string]bool{}
	for _, rt := range routes {
		name := rt.Method + " " + rt.Path
		switch {
		case !methods[rt.Method]:
			return fmt.Errorf("api: %s: %q is not a method a route may have", name, rt.Method)
		case !pathShape.MatchString(rt.Path):
			return fmt.Errorf("api: %s: a path starts with %s and is made of lower-case parts and {names}", name, Prefix)
		case seen[name]:
			return fmt.Errorf("api: %s is listed twice", name)
		case rt.Summary == "":
			return fmt.Errorf("api: %s has no summary", name)
		case rt.Door && (rt.Open == nil || rt.Handle != nil):
			return fmt.Errorf("api: %s is a door route, so it has Open and no Handle", name)
		case !rt.Door && (rt.Handle == nil || rt.Open != nil):
			return fmt.Errorf("api: %s needs a session, so it has Handle and no Open", name)
		}
		seen[name] = true
	}
	return nil
}

// held is what the shell keeps for one request: the store its session
// resolved to, so the handler can be given it and the shell can close it.
type held struct {
	store     *tenant.Store
	session   signin.Session
	heartbeat time.Duration
}

type heldKey struct{}

func heldOf(ctx context.Context) *held {
	h, _ := ctx.Value(heldKey{}).(*held)
	return h
}

// Handler returns the handler for everything under /api/.
//
// For each request it makes a request id, limits the body, refuses a write
// that a page on another site set off, and writes one line in the server's
// log. A door route is then served as it is. Everything else, a path that
// is no route included, goes through audit.Wrap: the session cookie is read,
// signin.Resolve turns it into the one tenant's store, and the call is
// written in that tenant's audit log before the route's handler runs.
func Handler(o Options, routes []Route) (http.Handler, error) {
	if o.Install == nil {
		return nil, errors.New("api: no data folder was given")
	}
	if o.MaxBodyBytes < 1 {
		return nil, errors.New("api: the largest request body must be 1 byte or more")
	}
	if o.Heartbeat <= 0 {
		return nil, errors.New("api: the stream heartbeat must be longer than nothing")
	}
	if err := Check(routes); err != nil {
		return nil, err
	}

	door, inside := http.NewServeMux(), http.NewServeMux()
	bodyLimits := map[string]int64{} // P9-02: the routes that take a larger body, by pattern
	for _, rt := range routes {
		pattern := rt.Method + " " + rt.Path
		if rt.MaxBodyBytes > o.MaxBodyBytes {
			bodyLimits[pattern] = rt.MaxBodyBytes
		}
		if rt.Door {
			door.HandleFunc(pattern, rt.Open)
			continue
		}
		handle := rt.Handle
		inside.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			h := heldOf(r.Context())
			if h == nil || h.store == nil {
				// Not reachable: audit.Wrap has refused the call by now.
				Error(w, r, http.StatusUnauthorized, "no_session", "sign in first")
				return
			}
			handle(w, r, Call{Store: h.store, Session: h.session, RequestID: audit.RequestID(r.Context())})
		})
	}
	inside.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		Error(w, r, http.StatusNotFound, "no_route", "there is no such route")
	})

	// who is the one place a request is tied to a tenant.
	who := func(r *http.Request) (audit.Caller, error) {
		h := heldOf(r.Context())
		if h == nil {
			return audit.Caller{}, errors.New("api: the request did not come through the shell")
		}
		store, session, err := signin.Resolve(o.Install, Token(r))
		if err != nil {
			return audit.Caller{}, err
		}
		h.store, h.session = store, session
		return audit.Caller{Store: store, Actor: ActorPerson}, nil
	}
	recorded := audit.Wrap(who, inside)
	crossOrigin := http.NewCrossOriginProtection()
	logTo := o.Log

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := audit.NewRequestID()
		h := &held{heartbeat: o.Heartbeat}
		ctx := context.WithValue(audit.WithRequestID(r.Context(), id), heldKey{}, h)
		r = r.WithContext(ctx)

		head := w.Header()
		head.Set("X-Request-Id", id)
		head.Set("X-Content-Type-Options", "nosniff")
		head.Set("Cache-Control", "no-store")

		rec := &statusWriter{ResponseWriter: w}
		start := time.Now()
		defer func() {
			if p := recover(); p != nil {
				if rec.status == 0 {
					Error(rec, r, http.StatusInternalServerError, "failed", "the call failed")
				}
				rec.status = http.StatusInternalServerError
				logTo.Printf("%s the handler panicked: %v", id, p)
			}
			who := "-"
			if h.store != nil {
				who = h.store.TenantID()
				if err := h.store.Close(); err != nil {
					logTo.Printf("%s closing the store of tenant %s: %v", id, who, err)
				}
			}
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			// The path and never the query string, a header or the body.
			logTo.Printf("%s %s %s %d %dms tenant=%s", id, r.Method, r.URL.Path, status, time.Since(start).Milliseconds(), who)
		}()

		if r.Body != nil {
			r.Body = http.MaxBytesReader(rec, r.Body, bodyLimit(inside, bodyLimits, r, o.MaxBodyBytes))
		}
		if err := crossOrigin.Check(r); err != nil {
			Error(rec, r, http.StatusForbidden, "cross_origin", "a page on another site may not make this call")
			return
		}
		if _, pattern := door.Handler(r); pattern != "" {
			door.ServeHTTP(rec, r)
			return
		}
		recorded.ServeHTTP(rec, r)
	}), nil
}

// bodyLimit is the most a request's body may hold: the limit of the route
// it is for, when that route takes a larger body (P9-02), and otherwise
// most.
func bodyLimit(inside *http.ServeMux, limits map[string]int64, r *http.Request, most int64) int64 {
	if len(limits) == 0 {
		return most
	}
	if _, pattern := inside.Handler(r); limits[pattern] > most {
		return limits[pattern]
	}
	return most
}

// Token returns the session token a request carries, or "" when it has
// none. The token travels in the session cookie and nowhere else.
func Token(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// SetSessionCookie gives the browser a session token in a cookie the page's
// scripts cannot read, that is sent only with requests from Keystone's own
// pages, and only to the API. secure adds the flag that keeps it off plain
// HTTP; it is always set when the request itself came over HTTPS.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     cookiePath,
		Expires:  expires,
		HttpOnly: true,
		Secure:   secure || r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie tells the browser to forget the session cookie.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     cookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure || r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

// statusWriter notes the status a handler sends, for the server's log. It
// passes Flush through, so a stream reaches the client as it is written.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 && code >= 200 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		if s.status == 0 {
			s.status = http.StatusOK
		}
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Log is the server's own log. It writes whole lines, one at a time, each
// with the time in front, and with anything shaped like a key or a token
// taken out first. A nil *Log writes nothing.
type Log struct {
	mu sync.Mutex
	w  io.Writer
}

// NewLog returns a log that writes to w.
func NewLog(w io.Writer) *Log { return &Log{w: w} }

// Printf writes one line.
func (l *Log) Printf(format string, args ...any) {
	if l == nil || l.w == nil {
		return
	}
	text := audit.Scrub(strings.ReplaceAll(fmt.Sprintf(format, args...), "\n", " "))
	at := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.w, at, text)
}
