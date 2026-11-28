package trickdrill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
)

// callTimeout is how long one call may take before the drill stops
// reading it. A turn that waits for approvals is answered by the drill
// long before.
const callTimeout = 60 * time.Second

// call is one request to the real server's handler, in memory.
type call struct {
	method, path string
	// body is sent as JSON.
	body string
	// extra changes the request before it is sent.
	extra func(*http.Request)
}

// do sends one call, as the person when they are signed in, and returns
// what came back.
func (g *rig) do(c call) *httptest.ResponseRecorder {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var r *http.Request
	if c.body != "" {
		r = httptest.NewRequest(c.method, "http://keystone.test"+c.path, strings.NewReader(c.body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(c.method, "http://keystone.test"+c.path, nil)
	}
	if g.token != "" {
		r.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.token})
	}
	if c.extra != nil {
		c.extra(r)
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r.WithContext(ctx))
	return rec
}

// as makes one JSON call and reads the answer into out.
func (g *rig) as(method, path, body string, want int, out any) error {
	rec := g.do(call{method: method, path: api.Prefix + path, body: body})
	if rec.Code != want {
		return fmt.Errorf("%s %s answered %d, want %d: %s", method, path, rec.Code, want, strip(rec.Body.String()))
	}
	if out != nil {
		return json.Unmarshal(rec.Body.Bytes(), out)
	}
	return nil
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
			return fmt.Errorf("the drill waited too long for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
