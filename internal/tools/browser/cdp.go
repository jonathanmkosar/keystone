package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// The DevTools protocol over the browser's pipe: each message is one JSON
// object followed by a NUL byte, both ways. A call carries an id and gets
// a reply with the same id; anything else the browser sends is an event,
// handed to the handler in order on a goroutine of its own, so a handler
// may make calls.

// maxMessage is the largest message read from the browser. A screenshot of
// the largest window the settings allow is well under it.
const maxMessage = 64 << 20

type message struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *protocolError  `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

type protocolError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *protocolError) Error() string {
	return fmt.Sprintf("the browser answered %d: %s", e.Code, e.Message)
}

type conn struct {
	w      io.Writer
	wmu    sync.Mutex
	next   atomic.Int64
	pmu    sync.Mutex
	wait   map[int64]chan message
	events chan message
	done   chan struct{}
	err    error
}

// errClosed says the browser has stopped or its pipe was closed.
var errClosed = errors.New("the browser has stopped")

// newConn reads r and writes w, and gives every event to handle.
func newConn(r io.Reader, w io.Writer, handle func(method, session string, params json.RawMessage)) *conn {
	c := &conn{w: w, wait: map[int64]chan message{}, events: make(chan message, 1024), done: make(chan struct{})}
	go c.read(r)
	go func() {
		for m := range c.events {
			handle(m.Method, m.SessionID, m.Params)
		}
	}()
	return c
}

func (c *conn) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	defer func() {
		c.pmu.Lock()
		if c.err == nil {
			c.err = errClosed
		}
		for id, ch := range c.wait {
			close(ch)
			delete(c.wait, id)
		}
		c.pmu.Unlock()
		close(c.events)
		close(c.done)
	}()
	var buf []byte
	for {
		chunk, err := br.ReadSlice(0)
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			if len(buf) > maxMessage {
				c.fail(errors.New("the browser sent a message larger than Keystone reads"))
				return
			}
			continue
		}
		if err != nil {
			return
		}
		raw := buf[:len(buf)-1]
		buf = nil
		var m message
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if m.ID != 0 {
			c.pmu.Lock()
			ch := c.wait[m.ID]
			delete(c.wait, m.ID)
			c.pmu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		if m.Method != "" {
			select {
			case c.events <- m:
			default:
				// A browser that floods events faster than they are
				// handled loses the oldest ones' order, never a reply.
				go func(m message) {
					defer func() { recover() }()
					c.events <- m
				}(m)
			}
		}
	}
}

func (c *conn) fail(err error) {
	c.pmu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.pmu.Unlock()
}

// call sends one method and waits for its reply, which is put in out when
// out is not nil. session is empty for the browser itself.
func (c *conn) call(ctx context.Context, session, method string, params, out any) error {
	id := c.next.Add(1)
	m := message{ID: id, Method: method, SessionID: session}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		m.Params = raw
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ch := make(chan message, 1)
	c.pmu.Lock()
	if c.err != nil {
		err := c.err
		c.pmu.Unlock()
		return err
	}
	c.wait[id] = ch
	c.pmu.Unlock()
	c.wmu.Lock()
	_, err = c.w.Write(append(raw, 0))
	c.wmu.Unlock()
	if err != nil {
		c.pmu.Lock()
		delete(c.wait, id)
		c.pmu.Unlock()
		return errClosed
	}
	select {
	case reply, ok := <-ch:
		if !ok {
			return errClosed
		}
		if reply.Error != nil {
			return reply.Error
		}
		if out != nil && len(reply.Result) > 0 {
			return json.Unmarshal(reply.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.pmu.Lock()
		delete(c.wait, id)
		c.pmu.Unlock()
		return ctx.Err()
	}
}
