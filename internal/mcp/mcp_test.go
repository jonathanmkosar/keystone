package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/mcp"
	"github.com/jonathanmkosar/keystone/internal/mcp/mcptest"
)

// The test binary is also the stdio server: a connector runs it with
// mcptest.StdioEnv set, and it serves the invented notes instead of
// running the tests.
func TestMain(m *testing.M) {
	mcptest.ServeIfAsked()
	os.Exit(m.Run())
}

const invented = "an invented token for a test, not a real one"

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func httpTransport(srv *httptest.Server, token string) mcp.HTTP {
	h := http.Header{}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	return mcp.HTTP{URL: srv.URL + "/mcp", Header: h}
}

func stdio(mode string, extra ...string) mcp.Stdio {
	exe, _ := os.Executable()
	env := append([]string{mcptest.StdioEnv + "=" + mode, "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}, extra...)
	return mcp.Stdio{Command: exe, Args: []string{"-test.run=^$"}, Env: env}
}

func names(tools []mcp.Tool) string {
	var out []string
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return strings.Join(out, " ")
}

func textOf(r mcp.CallResult) string {
	var out []string
	for _, c := range r.Content {
		out = append(out, c.Text)
	}
	return strings.Join(out, "\n")
}

// exercise runs the calls every transport must answer the same way.
func exercise(t *testing.T, c *mcp.Client) {
	t.Helper()
	if s := c.Server(); s.Name != "Invented notes" || s.Protocol != mcp.ProtocolVersion {
		t.Errorf("the server said it is %+v", s)
	}
	tools, err := c.ListTools(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(tools); got != "search add listTags whoami fail" {
		t.Errorf("the tools listed are %q", got)
	}
	for _, tool := range tools {
		if want := tool.Name != "add"; tool.ReadOnly() != want {
			t.Errorf("%s says it only reads: %v", tool.Name, tool.ReadOnly())
		}
	}
	res, err := c.CallTool(ctx(t), "search", json.RawMessage(`{"query":"harbour"}`))
	if err != nil || res.IsError || !strings.Contains(textOf(res), `Two notes match "harbour"`) {
		t.Errorf("search gave %+v, %v", res, err)
	}
	if res, err := c.CallTool(ctx(t), "add", json.RawMessage(`{"title":"Tide","text":"Low at noon."}`)); err != nil || textOf(res) != `Added the note "Tide".` {
		t.Errorf("add gave %+v, %v", res, err)
	}
	if res, err := c.CallTool(ctx(t), "fail", nil); err != nil || !res.IsError {
		t.Errorf("a tool that fails gave %+v, %v, want a result marked as an error", res, err)
	}
	var rpc *mcp.RPCError
	if _, err := c.CallTool(ctx(t), "no_such_tool", nil); !errors.As(err, &rpc) || rpc.Code != -32602 {
		t.Errorf("an unknown tool gave %v", err)
	}
}

// Over HTTP, answered as JSON, with tools on several pages.
func TestHTTP(t *testing.T) {
	notes := mcptest.Notes()
	notes.PageSize = 2
	srv := httptest.NewServer(notes)
	defer srv.Close()
	c, err := mcp.Connect(ctx(t), httpTransport(srv, ""), mcp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, c)
	if notes.Sessions() != 1 {
		t.Errorf("%d sessions are open", notes.Sessions())
	}
	c.Close()
	if notes.Sessions() != 0 {
		t.Error("closing did not end the session")
	}
	if _, err := c.ListTools(ctx(t)); !errors.Is(err, mcp.ErrClosed) {
		t.Errorf("a closed client listed tools: %v", err)
	}
}

// Over HTTP, answered as event streams, with a server that sends a
// notification and a ping of its own before each response.
func TestHTTPEventStream(t *testing.T) {
	notes := mcptest.Notes()
	notes.SSE, notes.Chatty = true, true
	srv := httptest.NewServer(notes)
	defer srv.Close()
	c, err := mcp.Connect(ctx(t), httpTransport(srv, ""), mcp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exercise(t, c)
}

// A token is sent as a bearer token. A server that refuses it says so in an
// error that holds neither the token nor what the server wrote back.
func TestHTTPToken(t *testing.T) {
	notes := mcptest.Notes()
	notes.Token = invented
	srv := httptest.NewServer(notes)
	defer srv.Close()
	c, err := mcp.Connect(ctx(t), httpTransport(srv, invented), mcp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.CallTool(ctx(t), "whoami", nil)
	if err != nil || !strings.Contains(textOf(res), "Bearer "+invented) {
		t.Errorf("whoami gave %+v, %v", res, err)
	}
	c.Close()
	_, err = mcp.Connect(ctx(t), httpTransport(srv, "a wrong token"), mcp.Options{})
	if !errors.Is(err, mcp.ErrRefused) || strings.Contains(err.Error(), "wrong token") || strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("a refused token gave %v", err)
	}
}

// The address a person gave may hold a token. A transport error does not
// repeat it.
func TestHTTPErrorsLeaveOutTheAddress(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	_, err := mcp.Connect(ctx(t), mcp.HTTP{URL: addr + "/mcp?key=" + strings.ReplaceAll(invented, " ", "-")}, mcp.Options{})
	if err == nil || strings.Contains(err.Error(), "key=") || strings.Contains(err.Error(), addr) {
		t.Errorf("an unreachable server gave %v", err)
	}
}

// No redirect is followed.
func TestHTTPRedirectIsNotFollowed(t *testing.T) {
	notes := httptest.NewServer(mcptest.Notes())
	defer notes.Close()
	srv := httptest.NewServer(http.RedirectHandler(notes.URL+"/mcp", http.StatusTemporaryRedirect))
	defer srv.Close()
	if _, err := mcp.Connect(ctx(t), mcp.HTTP{URL: srv.URL}, mcp.Options{}); !errors.Is(err, mcp.ErrRedirect) {
		t.Errorf("a redirect gave %v", err)
	}
}

// A server that speaks a version this client does not is refused, and so
// is 2024-11-05 over HTTP, whose transport was another.
func TestVersions(t *testing.T) {
	for version, ok := range map[string]bool{"2025-06-18": true, "2025-03-26": true, "2024-11-05": false, "1999-01-01": false} {
		notes := mcptest.Notes()
		notes.Version = version
		srv := httptest.NewServer(notes)
		c, err := mcp.Connect(ctx(t), httpTransport(srv, ""), mcp.Options{})
		if (err == nil) != ok {
			t.Errorf("a server of version %s: %v", version, err)
		}
		if c != nil {
			c.Close()
		}
		srv.Close()
	}
	c, err := mcp.Connect(ctx(t), stdio("notes"), mcp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

// A server that forgot the session answers 404, which says a new one is
// needed.
func TestHTTPSessionGone(t *testing.T) {
	notes := mcptest.Notes()
	srv := httptest.NewServer(notes)
	defer srv.Close()
	c, err := mcp.Connect(ctx(t), httpTransport(srv, ""), mcp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	req, _ := http.NewRequest(http.MethodDelete, srv.URL, nil)
	req.Header.Set("Mcp-Session-Id", "session-1")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	if _, err := c.ListTools(ctx(t)); !errors.Is(err, mcp.ErrSessionGone) {
		t.Errorf("a forgotten session gave %v", err)
	}
}

// A message larger than the client reads is refused.
func TestHTTPTooLarge(t *testing.T) {
	notes := mcptest.Notes()
	notes.Tools = append(notes.Tools, mcptest.Tool{
		Def: mcp.Tool{Name: "big", Description: "Returns a lot.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		Run: func(mcptest.Call) (mcp.CallResult, *mcp.RPCError) {
			return mcp.CallResult{Content: []mcp.Content{{Type: "text", Text: strings.Repeat("tide ", 100000)}}}, nil
		},
	})
	for _, sse := range []bool{false, true} {
		notes.SSE = sse
		srv := httptest.NewServer(notes)
		c, err := mcp.Connect(ctx(t), httpTransport(srv, ""), mcp.Options{MaxMessageBytes: 64 << 10})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.CallTool(ctx(t), "big", nil); err == nil || !strings.Contains(err.Error(), "larger than") {
			t.Errorf("a message too large, as an event stream %v, gave %v", sse, err)
		}
		c.Close()
		srv.Close()
	}
}

// Over stdio, with the test binary as the server's command. A line that is
// not a message is let go, a chatty server's own messages are answered, and
// the token reaches the command in its environment.
func TestStdio(t *testing.T) {
	for _, mode := range []string{"notes", "chatty"} {
		c, err := mcp.Connect(ctx(t), stdio(mode, mcptest.StdioEnv+"_BANNER=1", mcptest.TokenEnv+"="+invented), mcp.Options{})
		if err != nil {
			t.Fatal(err)
		}
		exercise(t, c)
		res, err := c.CallTool(ctx(t), "whoami", nil)
		if err != nil || !strings.Contains(textOf(res), invented) {
			t.Errorf("whoami over stdio gave %+v, %v", res, err)
		}
		done := make(chan struct{})
		go func() { c.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("closing the stdio server did not end")
		}
		if _, err := c.CallTool(ctx(t), "search", json.RawMessage(`{"query":"x"}`)); !errors.Is(err, mcp.ErrClosed) {
			t.Errorf("a call after close gave %v", err)
		}
	}
}

// A command that breaks on starting is reported with what it wrote to its
// standard error, passed through the caller's redaction.
func TestStdioCrash(t *testing.T) {
	s := stdio("crash", mcptest.TokenEnv+"="+invented)
	s.Redact = func(text string) string { return strings.ReplaceAll(text, invented, "(the token, removed)") }
	_, err := mcp.Connect(ctx(t), s, mcp.Options{})
	if err == nil || !errors.Is(err, mcp.ErrClosed) || !strings.Contains(err.Error(), "breaking") || strings.Contains(err.Error(), invented) || !strings.Contains(err.Error(), "(the token, removed)") {
		t.Errorf("a command that broke gave %v", err)
	}
}

// A command that is not there is said to be so.
func TestStdioNoCommand(t *testing.T) {
	_, err := mcp.Connect(ctx(t), mcp.Stdio{Command: "keystone-no-such-command-for-a-test"}, mcp.Options{})
	if err == nil || !strings.Contains(err.Error(), "could not be started") {
		t.Errorf("a missing command gave %v", err)
	}
}

// A stopped call returns at once and the session goes on.
func TestStdioCancelledCall(t *testing.T) {
	c, err := mcp.Connect(ctx(t), stdio("notes"), mcp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.CallTool(stopped, "search", json.RawMessage(`{"query":"x"}`)); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled call gave %v", err)
	}
	if _, err := c.CallTool(ctx(t), "search", json.RawMessage(`{"query":"x"}`)); err != nil {
		t.Errorf("the session did not go on after a cancelled call: %v", err)
	}
}
