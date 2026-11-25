// Package mcptest is a Model Context Protocol server for tests and for
// verify: a few invented note-keeping tools, served over stdio or over the
// streamable HTTP transport, on this machine only. Keystone's server never
// includes it.
//
// Notes is the server the tests use. Over HTTP it is an http.Handler; over
// stdio a test binary serves it by calling ServeIfAsked first thing in
// TestMain, and a connector runs that same binary with StdioEnv set.
package mcptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/mcp"
)

// StdioEnv is the variable that makes a test binary serve Notes over its
// standard input and output, through ServeIfAsked. Its value is "notes".
const StdioEnv = "KEYSTONE_MCPTEST_STDIO"

// TokenEnv is the variable the stdio server reads a token from, which a
// connector passes its token in.
const TokenEnv = "NOTES_TOKEN"

// Call is one tool call the server took.
type Call struct {
	Tool string
	Args json.RawMessage
	// Auth is what the call came with: the Authorization header over HTTP,
	// and the token in TokenEnv over stdio.
	Auth string
}

// Tool is one tool the server offers, and what it does.
type Tool struct {
	Def mcp.Tool
	Run func(c Call) (mcp.CallResult, *mcp.RPCError)
}

// Server is one MCP server.
type Server struct {
	Name string
	// Version is the protocol version it answers initialize with. Empty
	// means mcp.ProtocolVersion.
	Version string
	Tools   []Tool
	// Token is the bearer token an HTTP request must carry. Empty means
	// none is asked for.
	Token string
	// SSE answers each HTTP request as an event stream, not as JSON.
	SSE bool
	// Chatty sends a notification and a ping of its own before each
	// response, as a server may.
	Chatty bool
	// PageSize is how many tools one page of tools/list holds. Zero means
	// all of them on one page.
	PageSize int

	mu       sync.Mutex
	calls    []Call
	sessions map[string]bool
	next     int
	pings    int
}

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

func text(s string) mcp.CallResult {
	return mcp.CallResult{Content: []mcp.Content{{Type: "text", Text: s}}}
}

// Notes returns the test server: invented notes, with tools that read
// (search, listTags, whoami, fail) and one that changes something (add).
func Notes() *Server {
	return &Server{
		Name: "Invented notes",
		Tools: []Tool{
			{
				Def: mcp.Tool{Name: "search", Title: "Search notes", Description: "Searches the notes for words. Returns each note that holds them, with its title.\nUse it before adding a note, to see whether one exists.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"The words to look for."}},"required":["query"]}`),
					Annotations: &mcp.Annotations{ReadOnlyHint: yes()}},
				Run: func(c Call) (mcp.CallResult, *mcp.RPCError) {
					var in struct {
						Query string `json:"query"`
					}
					if json.Unmarshal(c.Args, &in) != nil || strings.TrimSpace(in.Query) == "" {
						return mcp.CallResult{}, &mcp.RPCError{Code: -32602, Message: "search needs a query"}
					}
					return text(fmt.Sprintf("Two notes match %q:\n1. Harbour walk: the tide is lowest at noon.\n2. Ferry times: the first boat leaves at seven.", in.Query)), nil
				},
			},
			{
				Def: mcp.Tool{Name: "add", Title: "Add a note", Description: "Adds a note with a title and a text.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"text":{"type":"string"}},"required":["title","text"]}`),
					Annotations: &mcp.Annotations{ReadOnlyHint: no(), DestructiveHint: no()}},
				Run: func(c Call) (mcp.CallResult, *mcp.RPCError) {
					var in struct {
						Title string `json:"title"`
					}
					json.Unmarshal(c.Args, &in)
					return text(fmt.Sprintf("Added the note %q.", in.Title)), nil
				},
			},
			{
				Def: mcp.Tool{Name: "listTags", Description: "Lists the tags the notes use.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
					Annotations: &mcp.Annotations{ReadOnlyHint: yes()}},
				Run: func(Call) (mcp.CallResult, *mcp.RPCError) { return text("harbour, ferry, tide"), nil },
			},
			{
				Def: mcp.Tool{Name: "whoami", Description: "Says what credentials the call came with. A tool for checks.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
					Annotations: &mcp.Annotations{ReadOnlyHint: yes()}},
				Run: func(c Call) (mcp.CallResult, *mcp.RPCError) {
					if c.Auth == "" {
						return text("The call came with no credentials."), nil
					}
					return mcp.CallResult{Content: []mcp.Content{{Type: "text", Text: "The call came with: " + c.Auth}}, StructuredContent: json.RawMessage(fmt.Sprintf(`{"auth":%q}`, c.Auth))}, nil
				},
			},
			{
				Def: mcp.Tool{Name: "fail", Description: "Always fails, as a tool that breaks does.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
					Annotations: &mcp.Annotations{ReadOnlyHint: yes()}},
				Run: func(c Call) (mcp.CallResult, *mcp.RPCError) {
					return mcp.CallResult{IsError: true, Content: []mcp.Content{{Type: "text", Text: "The note store is closed for the night. " + c.Auth}}}, nil
				},
			},
		},
	}
}

// Calls returns every tool call the server took, oldest first.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Count returns how many times one tool was called.
func (s *Server) Count(tool string) int {
	n := 0
	for _, c := range s.Calls() {
		if c.Tool == tool {
			n++
		}
	}
	return n
}

// Sessions returns how many HTTP sessions are open.
func (s *Server) Sessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcp.RPCError   `json:"error,omitempty"`
}

func (s *Server) version() string {
	if s.Version != "" {
		return s.Version
	}
	return mcp.ProtocolVersion
}

// Handle answers one message: a response for a request, nil for a
// notification or a response.
func (s *Server) Handle(raw []byte, auth string) []byte {
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		out, _ := json.Marshal(message{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcp.RPCError{Code: -32700, Message: "Parse error"}})
		return out
	}
	if m.Method == "" || len(m.ID) == 0 {
		return nil
	}
	reply := message{JSONRPC: "2.0", ID: m.ID}
	switch m.Method {
	case "initialize":
		reply.Result = map[string]any{
			"protocolVersion": s.version(),
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]string{"name": s.Name, "version": "1"},
			"instructions":    "Invented notes for tests.",
		}
	case "ping":
		reply.Result = map[string]any{}
	case "tools/list":
		var p struct {
			Cursor string `json:"cursor"`
		}
		json.Unmarshal(m.Params, &p)
		start := 0
		fmt.Sscanf(p.Cursor, "from-%d", &start)
		defs := []mcp.Tool{}
		for _, t := range s.Tools {
			defs = append(defs, t.Def)
		}
		end := len(defs)
		if s.PageSize > 0 && start+s.PageSize < end {
			end = start + s.PageSize
		}
		if start > len(defs) {
			start = len(defs)
		}
		res := map[string]any{"tools": defs[start:end]}
		if end < len(defs) {
			res["nextCursor"] = fmt.Sprintf("from-%d", end)
		}
		reply.Result = res
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(m.Params, &p)
		var tool *Tool
		for i := range s.Tools {
			if s.Tools[i].Def.Name == p.Name {
				tool = &s.Tools[i]
			}
		}
		if tool == nil {
			reply.Error = &mcp.RPCError{Code: -32602, Message: "Unknown tool: " + p.Name}
			break
		}
		c := Call{Tool: p.Name, Args: p.Arguments, Auth: auth}
		s.mu.Lock()
		s.calls = append(s.calls, c)
		s.mu.Unlock()
		res, rpcErr := tool.Run(c)
		if rpcErr != nil {
			reply.Error = rpcErr
		} else {
			reply.Result = res
		}
	default:
		reply.Error = &mcp.RPCError{Code: -32601, Message: "Method not found"}
	}
	out, _ := json.Marshal(reply)
	return out
}

// chatter is what a chatty server sends before a response: a log
// notification and a ping of its own.
func (s *Server) chatter() [][]byte {
	s.mu.Lock()
	s.pings++
	n := s.pings
	s.mu.Unlock()
	note, _ := json.Marshal(message{JSONRPC: "2.0", Method: "notifications/message", Params: json.RawMessage(`{"level":"info","data":"working"}`)})
	ping, _ := json.Marshal(message{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprintf(`"server-ping-%d"`, n)), Method: "ping"})
	return [][]byte{note, ping}
}

// ServeHTTP serves the streamable HTTP transport at any path.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if s.Token != "" && auth != "Bearer "+s.Token {
		// The body repeats what it was sent, as a careless server might. A
		// client must not read it into an error.
		http.Error(w, "unauthorized: "+auth, http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		s.mu.Lock()
		delete(s.sessions, r.Header.Get("Mcp-Session-Id"))
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	case http.MethodPost:
	default:
		http.Error(w, "not here", http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "unreadable", http.StatusBadRequest)
		return
	}
	var m message
	if json.Unmarshal(raw, &m) != nil {
		http.Error(w, "not JSON", http.StatusBadRequest)
		return
	}
	if m.Method == "initialize" {
		s.mu.Lock()
		if s.sessions == nil {
			s.sessions = map[string]bool{}
		}
		s.next++
		id := fmt.Sprintf("session-%d", s.next)
		s.sessions[id] = true
		s.mu.Unlock()
		w.Header().Set("Mcp-Session-Id", id)
	} else {
		s.mu.Lock()
		known := s.sessions[r.Header.Get("Mcp-Session-Id")]
		s.mu.Unlock()
		if !known {
			http.Error(w, "no such session", http.StatusNotFound)
			return
		}
		if r.Header.Get("MCP-Protocol-Version") != s.version() {
			http.Error(w, "the protocol version header is missing or wrong", http.StatusBadRequest)
			return
		}
	}
	out := s.Handle(raw, auth)
	if out == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !s.SSE {
		w.Header().Set("Content-Type", "application/json")
		w.Write(out)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	if s.Chatty {
		for _, c := range s.chatter() {
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", c)
		}
	}
	fmt.Fprintf(w, ": a comment line\nevent: message\nid: 1\ndata: %s\n\n", out)
}

// ServeStdio serves the stdio transport until in ends.
func (s *Server) ServeStdio(in io.Reader, out io.Writer, tokenEnv string) error {
	auth := ""
	if tokenEnv != "" {
		auth = os.Getenv(tokenEnv)
	}
	r := bufio.NewReader(in)
	bw := bufio.NewWriter(out)
	fmt.Fprintln(os.Stderr, "invented notes server: ready")
	for {
		line, err := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			if reply := s.Handle(line, auth); reply != nil {
				if s.Chatty {
					for _, c := range s.chatter() {
						bw.Write(append(c, '\n'))
					}
				}
				bw.Write(append(reply, '\n'))
				bw.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// ServeIfAsked serves Notes over standard input and output and exits, when
// StdioEnv is "notes" or "chatty". A test binary calls it first thing in
// TestMain, so a connector can run that binary as its command. "crash"
// writes the token to standard error and stops at once, as a server that
// breaks on starting may.
func ServeIfAsked() {
	switch os.Getenv(StdioEnv) {
	case "notes":
	case "chatty":
	case "crash":
		fmt.Fprintln(os.Stderr, "starting with the token "+os.Getenv(TokenEnv)+", and breaking")
		os.Exit(3)
	default:
		return
	}
	s := Notes()
	s.Chatty = os.Getenv(StdioEnv) == "chatty"
	if os.Getenv(StdioEnv+"_BANNER") != "" {
		fmt.Fprintln(os.Stdout, "a line that is not a message")
	}
	if err := s.ServeStdio(os.Stdin, os.Stdout, TokenEnv); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
