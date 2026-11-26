package isolationdrill

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
)

// callTimeout is how long one call may take before the drill stops reading
// it. A turn's stream ends long before; a stream that does not end, such as
// a turn that waits for an approval, is cut off here.
const callTimeout = 20 * time.Second

// call is one request to the real server's handler, in memory.
type call struct {
	method, path string
	// token is the session cookie's value; empty sends no cookie.
	token string
	// body is sent as JSON unless raw is set.
	body string
	raw  []byte
	// extra changes the request before it is sent.
	extra   func(*http.Request)
	timeout time.Duration
}

// do sends one call and returns what came back.
func (g *rig) do(c call) *httptest.ResponseRecorder {
	timeout := c.timeout
	if timeout == 0 {
		timeout = callTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var r *http.Request
	switch {
	case c.raw != nil:
		r = httptest.NewRequest(c.method, "http://keystone.test"+c.path, strings.NewReader(string(c.raw)))
		r.Header.Set("Content-Type", "application/octet-stream")
	case c.body != "":
		r = httptest.NewRequest(c.method, "http://keystone.test"+c.path, strings.NewReader(c.body))
		r.Header.Set("Content-Type", "application/json")
	default:
		r = httptest.NewRequest(c.method, "http://keystone.test"+c.path, nil)
	}
	if c.token != "" {
		r.AddCookie(&http.Cookie{Name: api.CookieName, Value: c.token})
	}
	if c.extra != nil {
		c.extra(r)
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r.WithContext(ctx))
	return rec
}

// asPage sets the Origin a page of Keystone's own sends with a write, which
// the Google routes read for the address Google returns to.
func asPage(r *http.Request) { r.Header.Set("Origin", "http://"+r.Host) }

// jsonOf marshals v, which cannot fail for the drill's own values.
func jsonOf(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// event is one server-sent event as it was written.
type event struct {
	name string
	data string
}

// events reads a stream's events.
func events(stream string) []event {
	var out []event
	for _, chunk := range strings.Split(strings.ReplaceAll(stream, "\r\n", "\n"), "\n\n") {
		var e event
		for _, line := range strings.Split(chunk, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				e.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				e.data = strings.TrimPrefix(line, "data: ")
			}
		}
		if e.name != "" {
			out = append(out, e)
		}
	}
	return out
}

// waitFor polls ok until it says done, an error comes, or the time is up.
func waitFor(what string, within time.Duration, ok func() (bool, error)) error {
	deadline := time.Now().Add(within)
	for {
		done, err := ok()
		if err != nil || done {
			return err
		}
		if time.Now().After(deadline) {
			return &waitError{what}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type waitError struct{ what string }

func (e *waitError) Error() string { return "the drill waited too long for " + e.what }
