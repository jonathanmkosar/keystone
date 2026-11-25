package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Event is one server-sent event.
type Event struct {
	// ID lets a client that lost its connection ask for what it missed: the
	// browser sends the last id it saw in the Last-Event-ID header when it
	// connects again. An event with no id does not move that mark.
	ID string
	// Name is the kind of event. A client listens for events by name.
	Name string
	// Data is sent as JSON on one line.
	Data any
}

// Stream is an open response that sends server-sent events. Send and Close
// may be called from more than one goroutine.
type Stream struct {
	mu     sync.Mutex
	w      http.ResponseWriter
	rc     *http.ResponseController
	closed bool
	stop   chan struct{}
	done   chan struct{}
}

// OpenStream turns the response into a stream of server-sent events and
// sends the headers at once, so the client knows the stream is open before
// the first event. While the stream is open it sends a comment line every
// so often, the heartbeat, so a proxy does not close a quiet connection.
//
// The handler must call Close before it returns:
//
//	s, err := api.OpenStream(w, r)
//	if err != nil {
//		return
//	}
//	defer s.Close()
//	s.Send(api.Event{ID: "1", Name: "text", Data: chunk})
//
// A Send that fails means the client has gone. So does r.Context().Done().
func OpenStream(w http.ResponseWriter, r *http.Request) (*Stream, error) {
	rc := http.NewResponseController(w)
	head := w.Header()
	head.Set("Content-Type", "text/event-stream")
	head.Set("Cache-Control", "no-store")
	head.Set("X-Accel-Buffering", "no") // tells a proxy in front not to hold events back
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return nil, fmt.Errorf("api: this connection cannot stream: %w", err)
	}
	s := &Stream{w: w, rc: rc, stop: make(chan struct{}), done: make(chan struct{})}
	every := 15 * time.Second
	if h := heldOf(r.Context()); h != nil && h.heartbeat > 0 {
		every = h.heartbeat
	}
	go func() {
		defer close(s.done)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-r.Context().Done():
				return
			case <-tick.C:
				if s.write(": beat\n\n") != nil {
					return
				}
			}
		}
	}()
	return s, nil
}

// Send writes one event and pushes it to the client.
func (s *Stream) Send(e Event) error {
	if strings.ContainsAny(e.ID, "\r\n") || strings.ContainsAny(e.Name, "\r\n") {
		return errors.New("api: an event's id and name are one line each")
	}
	data, err := json.Marshal(e.Data)
	if err != nil {
		return fmt.Errorf("api: an event's data must be JSON: %w", err)
	}
	var b strings.Builder
	if e.ID != "" {
		b.WriteString("id: " + e.ID + "\n")
	}
	if e.Name != "" {
		b.WriteString("event: " + e.Name + "\n")
	}
	b.WriteString("data: ")
	b.Write(data) // JSON text holds no line end
	b.WriteString("\n\n")
	return s.write(b.String())
}

func (s *Stream) write(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("api: the stream is closed")
	}
	if _, err := s.w.Write([]byte(text)); err != nil {
		return err
	}
	return s.rc.Flush()
}

// Close ends the stream's heartbeat. Nothing is written after it returns,
// so the handler may return.
func (s *Stream) Close() {
	s.mu.Lock()
	already := s.closed
	s.closed = true
	s.mu.Unlock()
	if !already {
		close(s.stop)
	}
	<-s.done
}

// LastEventID returns the id of the last event a client saw before it lost
// its connection, or "" when it is connecting for the first time.
func LastEventID(r *http.Request) string {
	return r.Header.Get("Last-Event-ID")
}
