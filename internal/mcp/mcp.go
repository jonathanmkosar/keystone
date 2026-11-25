// Package mcp is a client for the Model Context Protocol. It speaks
// JSON-RPC 2.0 to one MCP server, over a local command's standard input and
// output (Stdio) or over the streamable HTTP transport (HTTP), and does what
// Keystone needs of a server: initialize a session, list its tools, and
// call one.
//
// It implements protocol version 2025-06-18. A server that answers with
// 2025-03-26 is accepted too, and over stdio so is 2024-11-05: the three
// methods used here have the same shape in each. Resources, prompts,
// sampling, roots and elicitation are not offered, so a server does not ask
// for them; a request it sends anyway is answered as a method not found,
// apart from ping.
//
// The package uses the standard library only and reads no setting: what a
// caller may vary comes in Options and in the transport. It never runs a
// command or reaches an address of its own choosing: both come from the
// caller, who checks them first (internal/connectors).
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProtocolVersion is the version of the protocol this client asks for.
const ProtocolVersion = "2025-06-18"

// versions are the protocol versions a server may answer with, newest
// first. 2024-11-05 is taken over stdio only: its HTTP transport is the
// older one with a separate event stream, which this package does not speak.
var versions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// Tool is one tool a server offers.
type Tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations *Annotations    `json:"annotations,omitempty"`
}

// Annotations are what a server says about a tool's behaviour. They are
// hints from the server, not promises.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// ReadOnly reports whether the server says the tool only reads. A tool that
// says nothing does not only read: the protocol's default for readOnlyHint
// is false.
func (t Tool) ReadOnly() bool {
	return t.Annotations != nil && t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint
}

// Content is one part of a tool's result: text, an image, audio, a link to
// a resource, or a resource held in the result.
type Content struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	Data     string    `json:"data,omitempty"`
	MimeType string    `json:"mimeType,omitempty"`
	URI      string    `json:"uri,omitempty"`
	Name     string    `json:"name,omitempty"`
	Title    string    `json:"title,omitempty"`
	Resource *Resource `json:"resource,omitempty"`
}

// Resource is a resource held in a result: its text, or its bytes in base64.
type Resource struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// CallResult is what a tool call came back with. IsError says the tool
// itself failed; its content says why, for the model to read.
type CallResult struct {
	Content           []Content       `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}

// Server is what a server said about itself when the session began.
type Server struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Protocol is the protocol version the session speaks.
	Protocol string `json:"-"`
	// Instructions is what the server says about using it. Outside content.
	Instructions string `json:"-"`
}

// Options are what a session runs with.
type Options struct {
	// ClientName and ClientVersion are how the client names itself to the
	// server. Empty means "keystone" and "1".
	ClientName    string
	ClientVersion string
	// MaxMessageBytes is the most bytes one message from the server may
	// hold. Zero means 4 MB.
	MaxMessageBytes int64
	// MaxPages is the most pages of tools ListTools reads. Zero means 20.
	MaxPages int
}

func (o Options) withDefaults() Options {
	if o.ClientName == "" {
		o.ClientName = "keystone"
	}
	if o.ClientVersion == "" {
		o.ClientVersion = "1"
	}
	if o.MaxMessageBytes <= 0 {
		o.MaxMessageBytes = 4 << 20
	}
	if o.MaxPages <= 0 {
		o.MaxPages = 20
	}
	return o
}

// Transport is a way to reach a server: Stdio or HTTP.
type Transport interface {
	open(o Options, in inbound) (wire, error)
	kind() string
}

// wire carries messages to one server and back.
type wire interface {
	// roundTrip sends a request and returns the server's response to it.
	roundTrip(ctx context.Context, id string, payload []byte) (message, error)
	// notify sends a notification, which has no response.
	notify(ctx context.Context, payload []byte) error
	// negotiated is told the protocol version the session speaks.
	negotiated(version string)
	// close ends the session and frees what it holds.
	close() error
}

// inbound answers a message the server sent that is not a response: a
// request, which gets the reply it returns, or a notification, which gets
// none (nil).
type inbound func(m message) *message

// Errors a caller may test for.
var (
	// ErrClosed is a call on a session that was closed, or whose server went
	// away. A new session is needed.
	ErrClosed = errors.New("the connection to the server is closed")
	// ErrSessionGone is a server that no longer knows the session: it
	// answered 404 to a request that named it. A new session is needed.
	ErrSessionGone = errors.New("the server no longer knows this session")
	// ErrRefused is a server that refused the request's credentials.
	ErrRefused = errors.New("the server refused the token it was sent")
)

// RPCError is an error the server answered a request with.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("the server answered with error %d: %s", e.Code, clip(e.Message, 300))
}

// Client is one session with one server. Its methods may be called from
// several goroutines at once.
type Client struct {
	w       wire
	o       Options
	ids     atomic.Int64
	server  Server
	changed atomic.Bool
	once    sync.Once
	closed  atomic.Bool
}

// Connect starts a session: it reaches the server, asks to initialize with
// ProtocolVersion, checks the version the server answers with, and tells it
// the session is ready. ctx bounds the start only.
func Connect(ctx context.Context, t Transport, o Options) (*Client, error) {
	if t == nil {
		return nil, errors.New("mcp: no transport was given")
	}
	c := &Client{o: o.withDefaults()}
	w, err := t.open(c.o, c.inbound)
	if err != nil {
		return nil, err
	}
	c.w = w
	ok := false
	defer func() {
		if !ok {
			w.close()
		}
	}()
	var init struct {
		ProtocolVersion string          `json:"protocolVersion"`
		Capabilities    json.RawMessage `json:"capabilities"`
		ServerInfo      Server          `json:"serverInfo"`
		Instructions    string          `json:"instructions"`
	}
	raw, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": c.o.ClientName, "version": c.o.ClientVersion},
	})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &init); err != nil {
		return nil, errors.New("the server's answer to initialize is not the shape the protocol gives it")
	}
	if !speaks(init.ProtocolVersion, t.kind()) {
		return nil, fmt.Errorf("the server speaks MCP version %q, and Keystone speaks %s", clip(init.ProtocolVersion, 40), strings.Join(versions[:2], " and "))
	}
	var caps struct {
		Tools *json.RawMessage `json:"tools"`
	}
	if json.Unmarshal(init.Capabilities, &caps) != nil || caps.Tools == nil {
		return nil, errors.New("the server offers no tools")
	}
	c.server = init.ServerInfo
	c.server.Protocol, c.server.Instructions = init.ProtocolVersion, init.Instructions
	w.negotiated(init.ProtocolVersion)
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return nil, err
	}
	ok = true
	return c, nil
}

func speaks(version, kind string) bool {
	for _, v := range versions {
		if v == version {
			return v != "2024-11-05" || kind == "stdio"
		}
	}
	return false
}

// Server returns what the server said about itself.
func (c *Client) Server() Server { return c.server }

// ToolsChanged reports whether the server has said its list of tools
// changed since the last ListTools.
func (c *Client) ToolsChanged() bool { return c.changed.Load() }

// ListTools returns every tool the server offers, following its pages up to
// Options.MaxPages.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	c.changed.Store(false)
	var all []Tool
	cursor := ""
	for page := 0; page < c.o.MaxPages; page++ {
		var params any
		if cursor != "" {
			params = map[string]string{"cursor": cursor}
		}
		raw, err := c.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var got struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			return nil, errors.New("the server's list of tools is not the shape the protocol gives it")
		}
		all = append(all, got.Tools...)
		if got.NextCursor == "" {
			return all, nil
		}
		cursor = got.NextCursor
	}
	return all, nil
}

// CallTool calls one tool with its arguments, a JSON object. A tool that
// failed comes back as a result with IsError set, not as an error: an
// error is a call the server could not take, or a server that could not be
// reached. When ctx ends first, the server is told the request is
// cancelled.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	raw, err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return CallResult{}, err
	}
	var res CallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return CallResult{}, errors.New("the server's result is not the shape the protocol gives a tool's result")
	}
	return res, nil
}

// Close ends the session. A stdio server's command is told to stop and is
// stopped if it does not. Calls after Close fail with ErrClosed.
func (c *Client) Close() error {
	var err error
	c.once.Do(func() {
		c.closed.Store(true)
		err = c.w.close()
	})
	return err
}

// request sends one request and returns its result.
func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err // nothing is sent for a call already stopped
	}
	n := c.ids.Add(1)
	id := fmt.Sprint(n)
	m := message{JSONRPC: "2.0", ID: json.RawMessage(id), Method: method}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		m.Params = p
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	resp, err := c.w.roundTrip(ctx, id, payload)
	if err != nil {
		if ctx.Err() != nil && method != "initialize" {
			c.cancelled(id)
		}
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	if len(resp.Result) == 0 || string(resp.Result) == "null" {
		return nil, fmt.Errorf("the server's answer to %s has no result", method)
	}
	return resp.Result, nil
}

// cancelled tells the server, as well as it can, that a request it may be
// working on is no longer wanted.
func (c *Client) cancelled(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.notify(ctx, "notifications/cancelled", map[string]any{"requestId": json.RawMessage(id), "reason": "The person stopped the turn, or the call ran out of time."})
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	m := message{JSONRPC: "2.0", Method: method}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return err
		}
		m.Params = p
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return c.w.notify(ctx, payload)
}

// inbound answers what the server sends that is not a response. A ping is
// answered; any other request is a method this client does not offer. A
// notification that the tools changed is noted; others are let go.
func (c *Client) inbound(m message) *message {
	switch {
	case m.Method == "":
		return nil
	case len(m.ID) == 0:
		if m.Method == "notifications/tools/list_changed" {
			c.changed.Store(true)
		}
		return nil
	case m.Method == "ping":
		return &message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage(`{}`)}
	}
	return &message{JSONRPC: "2.0", ID: m.ID, Error: &RPCError{Code: -32601, Message: "Method not found: this client offers no " + clip(m.Method, 60)}}
}

// clip keeps the first most characters of a text from a server, and says
// when it left some out.
func clip(s string, most int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= most {
		return s
	}
	return string(r[:most]) + "…"
}
