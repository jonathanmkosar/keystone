package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Caller is who an API call is from: the tenant's store, and a short name
// for the actor, such as person or admin.
type Caller struct {
	Store *tenant.Store
	Actor string
}

type requestIDKey struct{}

// NewRequestID returns a fresh request id: sixteen random hex characters.
func NewRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("audit: no random bytes for a request id: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// WithRequestID returns ctx carrying a request id. Wrap keeps an id that is
// already there, so the API may choose its own before Wrap runs.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request id ctx carries, or "" when it has none.
// Code that makes a model call or a tool call for an API call puts this id
// on its own entries, which ties them to the call that set them off.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Wrap returns a handler that records every call to next in the caller's
// audit log. The API puts it around all of its routes at once.
//
// who says whose call a request is, from its session. A request who cannot
// place belongs to no tenant: it is refused with 401 and next does not run.
//
// For a request who can place, Wrap writes an entry with the outcome
// "started" before next runs. If that entry cannot be written, the call is
// refused with 500 and next does not run, so no call is served without an
// entry. When next returns, or panics, Wrap writes a second entry with the
// status, the outcome and how long the call took.
//
// An entry holds the method and the path. It holds no header, no query
// string and no body, because that is where passwords, keys and session
// tokens travel.
func Wrap(who func(*http.Request) (Caller, error), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := RequestID(r.Context())
		if id == "" {
			id = NewRequestID()
			r = r.WithContext(WithRequestID(r.Context(), id))
		}
		w.Header().Set("X-Request-Id", id)

		c, err := who(r)
		if err != nil || c.Store == nil {
			refuse(w, http.StatusUnauthorized, "sign in first", id)
			return
		}
		e := Entry{
			Kind:      KindAPI,
			RequestID: id,
			Actor:     c.Actor,
			Action:    r.Method + " " + r.URL.Path,
			Outcome:   OutcomeStarted,
		}
		if _, err := Append(r.Context(), c.Store, e); err != nil {
			log.Printf("audit: request %s was refused because it could not be recorded: %v", id, err)
			refuse(w, http.StatusInternalServerError, "the call could not be recorded, so it was not run", id)
			return
		}

		start := time.Now()
		rec := &recorder{ResponseWriter: w}
		finish := func(status int, received string) {
			if status == 0 {
				status = http.StatusOK // what net/http sends when a handler writes nothing
			}
			e.Status = status
			e.Outcome = outcomeOf(status)
			e.Received = received
			e.DurationMS = time.Since(start).Milliseconds()
			// The client may have gone, which cancels the request's
			// context. The end of the call is recorded all the same.
			if _, err := Append(context.WithoutCancel(r.Context()), c.Store, e); err != nil {
				log.Printf("audit: request %s: the end of the call could not be recorded: %v", id, err)
			}
		}
		defer func() {
			if p := recover(); p != nil {
				// Whatever the handler had sent, the call failed.
				finish(http.StatusInternalServerError, "the handler panicked")
				panic(p)
			}
		}()
		next.ServeHTTP(rec, r)
		finish(rec.status, "")
	})
}

// outcomeOf sorts an HTTP status into an outcome.
func outcomeOf(status int) Outcome {
	switch {
	case status >= 500:
		return OutcomeError
	case status >= 400:
		return OutcomeRefused
	}
	return OutcomeOK
}

// refuse answers a call that was not run. The shape of an API error belongs
// to the API shell (P2-05), which may change this to match its own.
func refuse(w http.ResponseWriter, status int, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message, "request_id": requestID})
}

// recorder notes the status a handler sends. It passes Flush through, so a
// stream of server-sent events still reaches the client as it is written.
type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 && code >= 200 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		if r.status == 0 {
			r.status = http.StatusOK
		}
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
