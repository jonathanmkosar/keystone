package turn

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/connectors"
	"github.com/jonathanmkosar/keystone/internal/mcp/mcptest"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/settings"
)

// The connectors' tests (P7-04): the item's proof, on the real routes with
// the fake provider and the test MCP server over HTTP and over stdio.

// The test binary is also the stdio test server: a stdio connector runs it
// with mcptest.StdioEnv set, and it serves the invented notes instead.
func TestMain(m *testing.M) {
	mcptest.ServeIfAsked()
	os.Exit(m.Run())
}

const connectorToken = "an invented connector token for a turn test, not a real one"

const connectorScripts = `{"test_data":true,"scripts":[
	{"name":"use the notes","when":{"user_text":"Use the notes."},"steps":[
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_load","name":"load_tools","input":{"names":["notes_search","notes_add"]}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_search","name":"notes_search","input":{"query":"harbour"}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_add","name":"notes_add","input":{"title":"Tide","text":"Low at noon."}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"Found two notes and added one."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
	{"name":"call the other","when":{"user_text":"Call the other."},"steps":[
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_other","name":"other_search","input":{"query":"harbour"}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"That did not work."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
	{"name":"anything else","when":{},"steps":[{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"A plain answer."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]}
]}`

// serveConnectors starts a server as serveTools does, with no tools of its
// own and the person's connectors through a pool.
func (g *rig) serveConnectors(provider seam.Provider) *served {
	g.t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 50 * time.Millisecond
	reg, err := settings.Registry()
	if err != nil {
		g.t.Fatal(err)
	}
	o, err := connectors.ReadOptions(func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	})
	if err != nil {
		g.t.Fatal(err)
	}
	set, err := adapters.Fake(g.fx.Scripts, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	runner := NewRunner(Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Adapters: set, Tools: tools.New(), Connectors: connectors.NewPool(o, g.t.Logf)})
	runner.Recover()
	routes := append(Routes(runner), ApprovalRoutes(runner)...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, routes)
	if err != nil {
		g.t.Fatal(err)
	}
	sv := &served{g: g, runner: runner, server: httptest.NewServer(h), tokens: map[string]string{}}
	g.t.Cleanup(sv.stop)
	g.with(g.test.ID, func(s *tenant.Store) {
		token, _, err := signin.SignIn(s, g.fx.Password, g.signIn)
		if err != nil {
			g.t.Fatal(err)
		}
		sv.tokens[g.test.ID] = token
	})
	return sv
}

// addConnectors gives the test tenant the notes connector over transport,
// with its token, and a second connector, other, that is switched off. It
// returns the HTTP servers, so their calls can be counted.
func (g *rig) addConnectors(transport string) (notes, other *mcptest.Server) {
	g.t.Helper()
	notes, other = mcptest.Notes(), mcptest.Notes()
	notes.Token = connectorToken
	ns, os2 := httptest.NewServer(notes), httptest.NewServer(other)
	g.t.Cleanup(ns.Close)
	g.t.Cleanup(os2.Close)
	exe, err := os.Executable()
	if err != nil {
		g.t.Fatal(err)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		if err := s.Secrets().Set("connector.notes", connectorToken); err != nil {
			g.t.Fatal(err)
		}
		c := connectors.Connector{Name: "notes", Label: "Notes", Transport: connectors.TransportHTTP, URL: ns.URL + "/mcp", Local: true, Secret: "connector.notes", Enabled: true, Outside: true}
		if transport == connectors.TransportStdio {
			c = connectors.Connector{Name: "notes", Label: "Notes", Transport: connectors.TransportStdio, Command: exe, Args: []string{"-test.run=^$"},
				Env: map[string]string{mcptest.StdioEnv: "notes"}, Secret: "connector.notes", TokenEnv: mcptest.TokenEnv, Enabled: true, Outside: true}
		}
		if _, err := connectors.Add(context.Background(), s, c, connectors.ByPerson, 20); err != nil {
			g.t.Fatal(err)
		}
		if _, err := connectors.Add(context.Background(), s, connectors.Connector{Name: "other", Transport: connectors.TransportHTTP, URL: os2.URL + "/mcp", Local: true, Enabled: false, Outside: true}, connectors.ByPerson, 20); err != nil {
			g.t.Fatal(err)
		}
	})
	return notes, other
}

// offeredTools reads what each model call of a request was offered, from
// the audit log's record of what was sent.
func (g *rig) offeredTools(requestID string) []map[string]seam.Tool {
	g.t.Helper()
	var out []map[string]seam.Tool
	g.with(g.test.ID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{Kind: audit.KindModel, RequestID: requestID})
		if err != nil {
			g.t.Fatal(err)
		}
		for _, e := range entries {
			if e.Outcome != audit.OutcomeStarted {
				continue
			}
			var req seam.Request
			if err := json.Unmarshal([]byte(e.Sent), &req); err != nil {
				g.t.Fatal(err)
			}
			call := map[string]seam.Tool{}
			for _, tool := range req.Tools {
				call[tool.Name] = tool
			}
			out = append(out, call)
		}
	})
	return out
}

// The item's proof. The test server's tools are listed, offered by name,
// loaded on demand through load_tools, and called; a disabled connector's
// tools are absent from every call; the connector's tool that changes
// something asks for approval, and runs once approved.
func TestConnectorToolsAreOfferedByNameLoadedAndCalled(t *testing.T) {
	for _, transport := range []string{connectors.TransportHTTP, connectors.TransportStdio} {
		t.Run(transport, func(t *testing.T) {
			g := newRig(t)
			notes, other := g.addConnectors(transport)
			sv := g.serveConnectors(scripted(t, connectorScripts))
			id := sv.newChat(g.test.ID)
			var asked []Approval
			events, _, requestID := sv.sendAnswering(g.test.ID, id, `{"text":"Use the notes."}`, "approve", func(a Approval) { asked = append(asked, a) })
			if end := last(t, events); end.Status != StatusDone {
				t.Fatalf("the turn ended as %+v", end)
			}
			got := results(t, events)
			if r := got["call_load"]; r.IsError || !strings.Contains(said(r), `"query"`) || !strings.Contains(said(r), `"title"`) {
				t.Errorf("load_tools gave %+v", r)
			}
			search := got["call_search"]
			if search.IsError || !strings.Contains(said(search), "Two notes match") || !tools.IsOutside(seam.Block{Type: seam.BlockToolResult, Content: search.Content}) {
				t.Errorf("the search gave %+v", search)
			}
			if r := got["call_add"]; r.IsError || !strings.Contains(said(r), `Added the note "Tide"`) {
				t.Errorf("the add gave %+v", r)
			}
			if len(asked) != 1 || asked[0].Tool != "notes_add" || !strings.Contains(asked[0].Summary, "Notes connector's add") {
				t.Errorf("the calls that asked were %+v, want the add alone", asked)
			}
			if transport == connectors.TransportHTTP && (notes.Count("search") != 1 || notes.Count("add") != 1) {
				t.Errorf("the server took %d searches and %d adds", notes.Count("search"), notes.Count("add"))
			}
			if len(other.Calls()) != 0 {
				t.Error("the disabled connector's server was called")
			}

			// What each call was offered: by name first, whole once loaded,
			// and never the disabled connector's tools.
			calls := g.offeredTools(requestID)
			if len(calls) != 4 {
				t.Fatalf("%d model calls, want 4", len(calls))
			}
			first := calls[0]
			if s, ok := first["notes_search"]; !ok || strings.Contains(string(s.InputSchema), "query") || !strings.Contains(s.Description, "load_tools first") {
				t.Errorf("the first call was offered the search as %+v", s)
			}
			if _, ok := first[connectors.LoadName]; !ok {
				t.Error("the first call was not offered load_tools")
			}
			for i, call := range calls {
				for name := range call {
					if strings.HasPrefix(name, "other_") {
						t.Errorf("call %d was offered the disabled connector's %s", i+1, name)
					}
				}
				if i == 0 {
					continue
				}
				if s := call["notes_search"]; !strings.Contains(string(s.InputSchema), "query") {
					t.Errorf("call %d was offered the loaded search as %+v", i+1, s)
				}
				if s := call["notes_list_tags"]; strings.Contains(string(s.InputSchema), "required") || !strings.Contains(s.Description, "load_tools first") {
					t.Errorf("call %d was offered a tool that was not loaded as %+v", i+1, s)
				}
			}

			// Each call is in the audit log, before and after.
			var outcomes []string
			for _, e := range g.toolEntries(requestID) {
				outcomes = append(outcomes, e.Action+":"+string(e.Outcome))
			}
			if got, want := strings.Join(outcomes, " "), "load_tools:started load_tools:ok notes_search:started notes_search:ok notes_add:started notes_add:ok"; got != want {
				t.Errorf("the tool calls are recorded as\n\t%s\nwant\n\t%s", got, want)
			}
			for _, e := range g.toolEntries(requestID) {
				if strings.Contains(e.Sent+e.Received, connectorToken) {
					t.Error("the connector's token is in the audit log")
				}
			}
		})
	}
}

// A call the model makes to a disabled connector's tool is answered as a
// tool that does not exist, and its server is not reached.
func TestADisabledConnectorsToolDoesNotRun(t *testing.T) {
	g := newRig(t)
	_, other := g.addConnectors(connectors.TransportHTTP)
	sv := g.serveConnectors(scripted(t, connectorScripts))
	id := sv.newChat(g.test.ID)
	events, _, _ := sv.send(g.test.ID, id, `{"text":"Call the other."}`)
	if r := results(t, events)["call_other"]; !r.IsError || !strings.Contains(said(r), "no tool named") {
		t.Errorf("the call to the disabled connector's tool gave %+v", r)
	}
	if len(other.Calls()) != 0 {
		t.Error("the disabled connector's server was called")
	}
}

// The approvals screen lists a connector's tools, with the server's flags,
// and a policy can be set for one.
func TestConnectorToolsHavePolicies(t *testing.T) {
	g := newRig(t)
	g.addConnectors(connectors.TransportHTTP)
	g.with(g.test.ID, func(s *tenant.Store) {
		o, _ := connectors.ReadOptions(func(key string) (string, bool) {
			reg, _ := settings.Registry()
			st, ok := settings.Lookup(reg, key)
			return st.Default, ok
		})
		if k, err := o.Check(context.Background(), s, "notes"); err != nil || len(k.Tools) == 0 {
			t.Fatalf("the check gave %+v, %v", k, err)
		}
	})
	sv := g.serveConnectors(scripted(t, connectorScripts))
	var list ToolsReply
	sv.call(g.test.ID, "GET", "tools", "", 200, &list)
	byName := map[string]ToolPolicy{}
	for _, tp := range list.Tools {
		byName[tp.Name] = tp
	}
	if add := byName["notes_add"]; add.ReadOnly || add.Inside || add.Default != "ask" {
		t.Errorf("the add is listed as %+v", add)
	}
	if search := byName["notes_search"]; !search.ReadOnly || search.Default != "always" {
		t.Errorf("the search is listed as %+v", search)
	}
	sv.setPolicyFor(g.test.ID, "notes_add", "always")
}
