package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HTTP reaches a server over the streamable HTTP transport: each message
// the client sends is a POST to one address, and the server answers a
// request with one JSON message or with an event stream that ends with the
// response. A session id the server gives is sent back with every request
// after it.
//
// The server's own event stream (a GET to the same address) is not opened,
// so a notification the server sends outside a request is not heard, and an
// answer whose stream breaks before its response is not resumed.
type HTTP struct {
	// URL is the server's MCP endpoint.
	URL string
	// Header is sent with every request. The token goes here, as
	// Authorization.
	Header http.Header
	// Client sends the requests. Nil means a client that uses no proxy and
	// follows no redirect.
	Client *http.Client
}

func (HTTP) kind() string { return "http" }

// ErrRedirect is a server that answered with a redirect. None is followed:
// a redirect could point the connection anywhere.
var ErrRedirect = errors.New("the server answered with a redirect, and Keystone does not follow one")

// NoRedirects is a CheckRedirect that follows none.
func NoRedirects(*http.Request, []*http.Request) error { return ErrRedirect }

// DefaultClient is the client HTTP uses when it is given none.
func DefaultClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
			TLSHandshakeTimeout: 15 * time.Second,
			ForceAttemptHTTP2:   true,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     90 * time.Second,
		},
		CheckRedirect: NoRedirects,
	}
}

type httpWire struct {
	url    string
	header http.Header
	client *http.Client
	in     inbound
	max    int64

	mu      sync.Mutex
	session string
	version string
}

func (h HTTP) open(o Options, in inbound) (wire, error) {
	u, err := url.Parse(h.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the server's address is not an http or https address")
	}
	client := h.Client
	if client == nil {
		client = DefaultClient()
	}
	return &httpWire{url: h.URL, header: h.Header.Clone(), client: client, in: in, max: o.MaxMessageBytes}, nil
}

func (w *httpWire) negotiated(version string) {
	w.mu.Lock()
	w.version = version
	w.mu.Unlock()
}

// post sends one message.
func (w *httpWire) post(ctx context.Context, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("the request to the server could not be made")
	}
	for k, v := range w.header {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	w.mu.Lock()
	session, version := w.session, w.version
	w.mu.Unlock()
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	if version != "" {
		req.Header.Set("MCP-Protocol-Version", version)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, plain(err)
	}
	return resp, nil
}

// plain is a transport error without the address in it, which a person may
// have written a token into.
func plain(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if errors.Is(ue.Err, ErrRedirect) {
			return ErrRedirect
		}
		return fmt.Errorf("the server could not be reached: %w", ue.Err)
	}
	return fmt.Errorf("the server could not be reached: %w", err)
}

// refusal turns a status that is not an answer into an error. The body is
// never read into it: it is the server's, and may repeat what it was sent.
func (w *httpWire) refusal(resp *http.Response) error {
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusNotFound && w.hasSession():
		return ErrSessionGone
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (HTTP status %d)", ErrRefused, resp.StatusCode)
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return ErrRedirect
	}
	return fmt.Errorf("the server answered with HTTP status %d", resp.StatusCode)
}

func (w *httpWire) hasSession() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.session != ""
}

func (w *httpWire) roundTrip(ctx context.Context, id string, payload []byte) (message, error) {
	resp, err := w.post(ctx, payload)
	if err != nil {
		return message{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return message{}, w.refusal(resp)
	}
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" && !w.hasSession() {
		if !visible(s) {
			return message{}, errors.New("the server gave a session id that is not visible ASCII")
		}
		w.mu.Lock()
		w.session = s
		w.mu.Unlock()
	}
	kind, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch kind {
	case "application/json":
		raw, err := io.ReadAll(io.LimitReader(resp.Body, w.max+1))
		if err != nil {
			return message{}, plain(err)
		}
		if int64(len(raw)) > w.max {
			return message{}, errTooLarge{w.max}
		}
		msgs, err := decode(raw)
		if err != nil {
			return message{}, err
		}
		for _, m := range msgs {
			if m.isResponse() && idKey(m.ID) == id {
				return m, nil
			}
			w.answer(m)
		}
		return message{}, errors.New("the server's answer holds no response to the request")
	case "text/event-stream":
		return w.stream(ctx, resp.Body, id)
	}
	return message{}, errors.New("the server answered with a type that is neither JSON nor an event stream")
}

// stream reads an event stream until the response to the request comes.
// Each event's data is one message. What else the server sends meanwhile,
// a request or a notification, is answered or noted as it comes.
func (w *httpWire) stream(ctx context.Context, body io.Reader, id string) (message, error) {
	r := bufio.NewReaderSize(body, 64<<10)
	var data []byte
	for {
		line, err := readLine(r, w.max)
		if err != nil {
			if ctx.Err() != nil {
				return message{}, ctx.Err()
			}
			if _, big := err.(errTooLarge); big {
				return message{}, err
			}
			return message{}, errors.New("the server ended its answer before it gave the response")
		}
		switch {
		case len(line) == 0:
			if len(data) == 0 {
				continue
			}
			msgs, err := decode(data)
			data = data[:0]
			if err != nil {
				continue
			}
			for _, m := range msgs {
				if m.isResponse() && idKey(m.ID) == id {
					return m, nil
				}
				w.answer(m)
			}
		case bytes.HasPrefix(line, []byte("data:")):
			part := bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" "))
			if int64(len(data)+len(part)) > w.max {
				return message{}, errTooLarge{w.max}
			}
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, part...)
		}
		// event:, id:, retry: and comments are not needed here.
	}
}

// answer hands a message that is not the awaited response to the client,
// and sends its reply, if it has one, in a request of its own.
func (w *httpWire) answer(m message) {
	if m.isResponse() {
		return
	}
	reply := w.in(m)
	if reply == nil {
		return
	}
	raw, err := json.Marshal(*reply)
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		w.notify(ctx, raw)
	}()
}

// notify sends a notification or a reply. The server accepts it with 202.
func (w *httpWire) notify(ctx context.Context, payload []byte) error {
	resp, err := w.post(ctx, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil
	}
	return w.refusal(resp)
}

// close tells the server the session is over, when it gave one, and lets
// the client's idle connections go.
func (w *httpWire) close() error {
	w.mu.Lock()
	session, version := w.session, w.version
	w.mu.Unlock()
	if session != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if req, err := http.NewRequestWithContext(ctx, http.MethodDelete, w.url, nil); err == nil {
			for k, v := range w.header {
				req.Header[k] = v
			}
			req.Header.Set("Mcp-Session-Id", session)
			if version != "" {
				req.Header.Set("MCP-Protocol-Version", version)
			}
			if resp, err := w.client.Do(req); err == nil {
				io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
				resp.Body.Close()
			}
		}
	}
	w.client.CloseIdleConnections()
	return nil
}

// visible reports whether a session id is visible ASCII, as the protocol
// requires of one.
func visible(s string) bool {
	if len(s) > 1024 {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return r < 0x21 || r > 0x7e })
}
