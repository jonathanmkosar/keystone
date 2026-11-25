package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/connectors"
	"github.com/jonathanmkosar/keystone/internal/mcp"
	"github.com/jonathanmkosar/keystone/internal/mcp/mcptest"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// The connectors' parts of verify (P7-04).
//
//   - connectorsRecorded is a step of "calls recorded": a test MCP server is
//     served on this machine, the test tenant adds it as a connector through
//     the API with a token, and a second connector switched off, and its
//     recorded turn loads two of the server's tools and calls them. The
//     tools must be listed, offered by name, loaded, called and recorded;
//     the switched-off connector's tools must be in no call; and the tool
//     that changes something, set to always, must still ask, because the
//     chat read the connector's outside content first.
//   - keysInConnectors is a surface of "no keys in output": servers that
//     repeat the token in results, in errors, in a refusal's body and on a
//     command's standard error.
//
// verify's own binary is also the stdio test server: a connector runs it
// with mcptest.StdioEnv set.
func init() {
	mcptest.ServeIfAsked()
}

// The message of fixtures/test-tenant/turns/connectors.json.
const connectorsMessage = "Find my notes about the harbour, and add one about the tide."

// connectorsRecorded runs the test tenant's recorded connector turn.
func connectorsRecorded() (problems []string, err error) {
	const token = "an invented connector token for verify, not a real one"
	var added atomic.Int32
	notes, other := mcptest.Notes(), mcptest.Notes()
	notes.Token = token
	for i := range notes.Tools {
		if notes.Tools[i].Def.Name == "add" {
			run := notes.Tools[i].Run
			notes.Tools[i].Run = func(c mcptest.Call) (mcp.CallResult, *mcp.RPCError) { added.Add(1); return run(c) }
		}
	}
	stand, off := httptest.NewServer(notes), httptest.NewServer(other)
	defer stand.Close()
	defer off.Close()
	r, err := openToolRig(nil)
	if err != nil {
		return nil, err
	}
	defer r.close()

	// The token through the secrets route, and both connectors through
	// theirs.
	if rec := r.w.request("PUT", api.Prefix+"secrets/connector.notes", r.token, fmt.Sprintf(`{"value":%q}`, token), nil); rec.Code != http.StatusOK {
		return nil, fmt.Errorf("the test tenant cannot store a connector's token: status %d", rec.Code)
	}
	body, _ := json.Marshal(connectors.Body{Name: "notes", Label: "Invented notes", URL: stand.URL + "/mcp", Local: true, Secret: "connector.notes"})
	rec := r.w.request("POST", api.Prefix+"connectors", r.token, string(body), nil)
	var row connectors.Row
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &row) != nil {
		return nil, fmt.Errorf("the test tenant cannot add a connector through the API: status %d", rec.Code)
	}
	if len(row.Tools) != 5 || row.Problem != "" || !row.TokenPresent {
		problems = append(problems, fmt.Sprintf("the connector added lists %d tools, with the problem %q, want the test server's 5", len(row.Tools), row.Problem))
	}
	no := false
	body, _ = json.Marshal(connectors.Body{Name: "other", URL: off.URL + "/mcp", Local: true, Enabled: &no})
	if rec := r.w.request("POST", api.Prefix+"connectors", r.token, string(body), nil); rec.Code != http.StatusCreated {
		return nil, fmt.Errorf("the test tenant cannot add a switched-off connector: status %d", rec.Code)
	}
	if rec := r.w.request("PUT", api.Prefix+"tools/notes_add/policy", r.token, `{"policy":"always"}`, nil); rec.Code != http.StatusOK {
		return nil, fmt.Errorf("the connector's tool has no policy route: status %d", rec.Code)
	}

	answered := answerWhenAsked(r.w, r.token, r.test, "approve", &added)
	got, err := r.send(connectorsMessage)
	if err != nil {
		return nil, err
	}
	asked, ranWhileWaiting, status := answered()
	switch {
	case asked.Tool != "notes_add" || asked.Reason != "outside":
		problems = append(problems, fmt.Sprintf("the connector's tool that changes something, set to always, did not ask after the chat read the connector's outside content: asked %q for %q", asked.Tool, asked.Reason))
	case ranWhileWaiting != 0 || status != http.StatusOK:
		problems = append(problems, fmt.Sprintf("the connector's tool ran %d times while it waited, and the approval answered %d", ranWhileWaiting, status))
	}
	if got.end.Status != turn.StatusDone {
		problems = append(problems, fmt.Sprintf("the recorded connector turn ended as %s", got.end.Status))
	}
	var called []string
	for _, c := range got.calls {
		called = append(called, c.Name)
		if res, ok := got.results[c.ID]; !ok || res.IsError {
			problems = append(problems, fmt.Sprintf("the call to %s gave an error or nothing: %q", c.Name, resultText(res)))
		}
	}
	if strings.Join(called, " ") != "load_tools notes_search notes_add" {
		problems = append(problems, fmt.Sprintf("the recorded connector turn called %q", strings.Join(called, " ")))
	}
	for _, c := range got.calls {
		res := got.results[c.ID]
		if c.Name == "notes_search" && !tools.IsOutside(seam.Block{Type: seam.BlockToolResult, Content: res.Content}) {
			problems = append(problems, "the connector's result is not marked as outside content")
		}
	}
	if notes.Count("search") != 1 || added.Load() != 1 || len(other.Calls()) != 0 {
		problems = append(problems, fmt.Sprintf("the test server took %d searches and %d adds, and the switched-off one %d calls; want 1, 1 and 0", notes.Count("search"), added.Load(), len(other.Calls())))
	}

	// What each model call was offered.
	models, err := r.entriesOf(audit.KindModel, got.requestID)
	if err != nil {
		return nil, err
	}
	var offers []map[string]string
	for _, e := range models {
		if e.Outcome != audit.OutcomeStarted {
			continue
		}
		var req seam.Request
		if json.Unmarshal([]byte(e.Sent), &req) != nil {
			return nil, fmt.Errorf("a model call's record does not read")
		}
		call := map[string]string{}
		for _, t := range req.Tools {
			call[t.Name] = string(t.InputSchema)
		}
		offers = append(offers, call)
	}
	if len(offers) != 4 {
		return append(problems, fmt.Sprintf("the recorded connector turn made %d model calls, want 4", len(offers))), nil
	}
	if s, ok := offers[0]["notes_search"]; !ok || strings.Contains(s, "query") {
		problems = append(problems, "the first call was not offered the connector's search by name alone")
	}
	if _, ok := offers[0][connectors.LoadName]; !ok {
		problems = append(problems, "the first call was not offered load_tools")
	}
	if !strings.Contains(offers[1]["notes_search"], "query") || !strings.Contains(offers[1]["notes_add"], "title") {
		problems = append(problems, "the call after load_tools was not offered the loaded tools whole")
	}
	for i, call := range offers {
		for name := range call {
			if strings.HasPrefix(name, "other_") {
				problems = append(problems, fmt.Sprintf("call %d was offered the switched-off connector's %s", i+1, name))
			}
		}
	}

	// The audit log: each call before and after, and no token.
	entries, err := r.entriesOf(audit.KindTool, got.requestID)
	if err != nil {
		return nil, err
	}
	if len(entries) != 2*len(got.calls) {
		problems = append(problems, fmt.Sprintf("%d connector calls left %d audit entries, want two each", len(got.calls), len(entries)))
	}
	for _, e := range entries {
		if strings.Contains(e.Sent+e.Received, token) {
			problems = append(problems, "the connector's token is in the audit log")
		}
	}
	return problems, nil
}

// keysInConnectors stores value as the token of connectors whose servers
// repeat it: in a result, in a failed tool's error, in the body of a
// refusal, and on a command's standard error as it breaks. It returns the
// tools' results and errors, what the connectors keep, the audit log and
// the data folder.
func keysInConnectors(_, dir, value string) ([]keyOutput, error) {
	data := filepath.Join(dir, "data")
	in, err := tenant.OpenWithKeeper(data, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		return nil, err
	}
	s, err := in.Store(made.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	echo := mcptest.Notes()
	echo.Token = value
	refusing := mcptest.Notes()
	refusing.Token = "the token this server wants, which is not the stored one"
	es, rs := httptest.NewServer(echo), httptest.NewServer(refusing)
	defer es.Close()
	defer rs.Close()
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	for _, add := range []connectors.Connector{
		{Name: "echo", Transport: connectors.TransportHTTP, URL: es.URL + "/mcp", Local: true, Secret: "connector.echo"},
		{Name: "refusing", Transport: connectors.TransportHTTP, URL: rs.URL + "/mcp", Local: true, Secret: "connector.echo"},
		{Name: "breaking", Transport: connectors.TransportStdio, Command: exe, Env: map[string]string{mcptest.StdioEnv: "crash"}, Secret: "connector.echo", TokenEnv: mcptest.TokenEnv},
		{Name: "files", Transport: connectors.TransportStdio, Command: exe, Env: map[string]string{mcptest.StdioEnv: "notes"}, Secret: "connector.echo", TokenEnv: mcptest.TokenEnv},
	} {
		add.Enabled, add.Outside = true, true
		if _, err := connectors.Add(ctx, s, add, connectors.ByCommandLine, 20); err != nil {
			return nil, err
		}
	}
	if err := s.Secrets().Set("connector.echo", value); err != nil {
		return nil, err
	}
	pool := connectors.NewPool(c.Connectors, nil)
	defer pool.Close()
	var out []keyOutput
	for _, name := range []string{"echo", "refusing", "breaking", "files"} {
		k, err := c.Connectors.Check(ctx, s, name)
		out = append(out, keyOutput{"the check of " + name, []byte(fmt.Sprintf("%+v %v", k, err))})
	}
	made2, err := chat.Create(s)
	if err != nil {
		return out, err
	}
	_, run := pool.ForCall(ctx, s, made2.ID, nil, nil)
	reg := tools.New()
	for _, name := range []string{"echo_whoami", "echo_fail", "files_whoami", "files_fail", connectors.LoadName} {
		t, ok := run[name]
		if !ok {
			return out, fmt.Errorf("the connectors did not offer %s", name)
		}
		reg.Add(t)
	}
	for _, name := range []string{"echo_whoami", "echo_fail", "files_whoami", "files_fail", connectors.LoadName} {
		res := reg.Run(ctx, tools.Call{Store: s, RequestID: "req-" + name, Actor: "person", ID: "call_" + name, Name: name, Input: json.RawMessage(`{"names":["echo_whoami"]}`)})
		out = append(out, keyOutput{"the result of " + name, []byte(fmt.Sprintf("%+v", res))})
	}
	// The check is worth nothing unless the token really was sent.
	if len(echo.Calls()) == 0 || !strings.Contains(echo.Calls()[0].Auth, value) {
		return out, fmt.Errorf("the echoing server was not sent the stored token")
	}
	kept, err := connectors.List(ctx, s)
	if err != nil {
		return out, err
	}
	out = append(out, keyOutput{"the connectors as kept", []byte(fmt.Sprintf("%+v", kept))})
	entries, err := audit.List(ctx, s, audit.Filter{})
	if err != nil {
		return out, err
	}
	out = append(out, keyOutput{"the audit log", []byte(fmt.Sprintf("%+v", entries))})
	files, err := folderFiles(data)
	return append(out, files...), err
}
