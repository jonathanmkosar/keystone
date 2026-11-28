package connectors

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

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/mcp/mcptest"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/settings"
)

// The test binary is also the stdio test server: a stdio connector runs it
// with mcptest.StdioEnv set.
func TestMain(m *testing.M) {
	mcptest.ServeIfAsked()
	os.Exit(m.Run())
}

const invented = "an invented connector token for a test, not a real one"

func options(t *testing.T) Options {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	o, err := ReadOptions(func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	o.IdleTimeout = time.Minute
	return o
}

// store returns a tenant's store in a folder of the test's own, and a
// second tenant's.
func stores(t *testing.T) (*tenant.Store, *tenant.Store) {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	var out []*tenant.Store
	for _, name := range []string{"Test Person One", "Test Person Two"} {
		made, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		out = append(out, s)
	}
	return out[0], out[1]
}

func bg() context.Context { return context.Background() }

// notesHTTP serves the test server on this machine and adds it to s as a
// local connector named notes, with its token stored.
func notesHTTP(t *testing.T, s *tenant.Store, server *mcptest.Server) *httptest.Server {
	t.Helper()
	server.Token = invented
	srv := httptest.NewServer(server)
	t.Cleanup(srv.Close)
	if err := s.Secrets().Set("connector.notes", invented); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(bg(), s, Connector{Name: "notes", Label: "Notes", Transport: TransportHTTP, URL: srv.URL + "/mcp", Local: true, Secret: "connector.notes", Enabled: true, Outside: true}, ByPerson, 20); err != nil {
		t.Fatal(err)
	}
	return srv
}

func offered(defs []seam.Tool) string {
	var out []string
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return strings.Join(out, " ")
}

func TestToolNames(t *testing.T) {
	got := ToolNames("notes", []string{"search", "listTags", "get-weather", "HTTPGet", "a.b.c", "___", "search_", strings.Repeat("x", 80), "Search"})
	want := []string{"notes_search", "notes_list_tags", "notes_get_weather", "notes_httpget", "notes_a_b_c", "notes_tool"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("name %d is %q, want %q", i, got[i], w)
		}
	}
	seen := map[string]bool{}
	for _, n := range got {
		if seen[n] || len(n) > 64 || !strings.HasPrefix(n, "notes_") {
			t.Errorf("%q is not a name of its own that a tool may have: %v", n, got)
		}
		seen[n] = true
	}
	if got[6] == "notes_search" || got[8] == "notes_search" || len(got[7]) > 64 {
		t.Errorf("names that collide or run long were not made their own: %v", got)
	}
	again := ToolNames("notes", []string{"search", "listTags", "get-weather", "HTTPGet", "a.b.c", "___", "search_", strings.Repeat("x", 80), "Search"})
	for i := range got {
		if got[i] != again[i] {
			t.Errorf("the names are not the same each time: %v and %v", got, again)
		}
	}
	reg := tools.New()
	for _, n := range got {
		if err := reg.Add(tools.Tool{Name: n, Description: "x", InputSchema: json.RawMessage(`{"type":"object"}`), Run: func(context.Context, tools.Input) (string, error) { return "", nil }}); err != nil {
			t.Errorf("the registry refuses %q: %v", n, err)
		}
	}
}

func TestCheck(t *testing.T) {
	good := []Connector{
		{Name: "notes", Transport: TransportHTTP, URL: "https://mcp.example/mcp"},
		{Name: "home_notes", Transport: TransportHTTP, URL: "http://127.0.0.1:7999/mcp", Local: true},
		{Name: "nas", Transport: TransportHTTP, URL: "http://nas.local:8080/mcp", Local: true},
		{Name: "mini", Transport: TransportHTTP, URL: "https://100.64.1.2/mcp", Local: true},
		{Name: "files", Transport: TransportStdio, Command: "npx", Args: []string{"-y", "some-server", "C:\\Notes"}, Env: map[string]string{"NOTES_DIR": "C:\\Notes"}, Secret: "connector.files", TokenEnv: "FILES_TOKEN"},
	}
	for _, c := range good {
		if err := Check(normal(c)); err != nil {
			t.Errorf("%s is refused: %v", c.Name, err)
		}
	}
	secret := "pasted-" + strings.ReplaceAll(invented, " ", "-")
	badOnes := map[string]Connector{
		"a plain http public address":         {Name: "notes", Transport: TransportHTTP, URL: "http://mcp.example/mcp"},
		"this machine, not marked local":      {Name: "notes", Transport: TransportHTTP, URL: "https://127.0.0.1/mcp"},
		"localhost, not marked local":         {Name: "notes", Transport: TransportHTTP, URL: "https://localhost/mcp"},
		"a home name, not marked local":       {Name: "notes", Transport: TransportHTTP, URL: "https://nas.local/mcp"},
		"a home address, not marked local":    {Name: "notes", Transport: TransportHTTP, URL: "https://192.168.1.20/mcp"},
		"a public address marked local":       {Name: "notes", Transport: TransportHTTP, URL: "http://93.184.216.34/mcp", Local: true},
		"a user in the address":               {Name: "notes", Transport: TransportHTTP, URL: "https://user:" + secret + "@mcp.example/mcp"},
		"no address":                          {Name: "notes", Transport: TransportHTTP},
		"a file address":                      {Name: "notes", Transport: TransportHTTP, URL: "file:///etc/passwd"},
		"an http connector with a command":    {Name: "notes", Transport: TransportHTTP, URL: "https://mcp.example/mcp", Command: "calc"},
		"a stdio connector with no command":   {Name: "notes", Transport: TransportStdio},
		"a stdio connector with an address":   {Name: "notes", Transport: TransportStdio, Command: "x", URL: "https://mcp.example"},
		"a token variable and no token":       {Name: "notes", Transport: TransportStdio, Command: "x", TokenEnv: "T"},
		"a variable named as the token's":     {Name: "notes", Transport: TransportStdio, Command: "x", Secret: "s", TokenEnv: "T", Env: map[string]string{"t": "y"}},
		"a variable with a line break":        {Name: "notes", Transport: TransportStdio, Command: "x", Env: map[string]string{"A": "y\nB=z"}},
		"a name with capitals":                {Name: "Notes", Transport: TransportHTTP, URL: "https://mcp.example/mcp"},
		"a name ending in an underscore":      {Name: "notes_", Transport: TransportHTTP, URL: "https://mcp.example/mcp"},
		"a name too long":                     {Name: strings.Repeat("n", 25), Transport: TransportHTTP, URL: "https://mcp.example/mcp"},
		"a secret's value as its secret name": {Name: "notes", Transport: TransportHTTP, URL: "https://mcp.example/mcp", Secret: secret + " x"},
		"no transport":                        {Name: "notes"},
	}
	for what, c := range badOnes {
		err := Check(normal(c))
		if !errors.Is(err, ErrBad) {
			t.Errorf("%s is not refused: %v", what, err)
			continue
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "93.184") {
			t.Errorf("the refusal of %s repeats what it was sent: %v", what, err)
		}
	}
}

// A connector is added, listed, changed and removed. Its switches and the
// policies of its tools go with it.
func TestTheRecord(t *testing.T) {
	s, other := stores(t)
	srv := notesHTTP(t, s, mcptest.Notes())
	o := options(t)
	k, err := o.Check(bg(), s, "notes")
	if err != nil || k.Problem != "" || len(k.Tools) != 5 || k.Tools[1].Name != "notes_add" || k.Tools[1].ReadOnly || !k.Tools[0].ReadOnly {
		t.Fatalf("the check gave %+v, %v", k, err)
	}
	if _, err := Add(bg(), s, Connector{Name: "notes", Transport: TransportHTTP, URL: "https://mcp.example/mcp"}, ByPerson, 20); !errors.Is(err, ErrExists) {
		t.Errorf("a second connector of the same name: %v", err)
	}
	if _, err := Add(bg(), s, Connector{Name: "more", Transport: TransportHTTP, URL: "https://mcp.example/mcp"}, ByPerson, 1); !errors.Is(err, ErrFull) {
		t.Errorf("a connector over the limit: %v", err)
	}
	if list, err := List(bg(), other); err != nil || len(list) != 0 {
		t.Errorf("another person's record holds %v, %v", list, err)
	}
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetSwitch(bg(), s, c.ID, "notes", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec("INSERT INTO tool_policies(tool, policy, updated_at) VALUES('notes_add', 'always', 'x')"); err != nil {
		t.Fatal(err)
	}
	// A new address may be another server: what it listed, and the
	// policies for its tools, go.
	k.URL = srv.URL + "/other"
	if k2, err := Replace(bg(), s, k.Connector); err != nil || len(k2.Tools) != 0 || !k2.Listed.IsZero() {
		t.Errorf("a moved connector kept its tools: %+v, %v", k2, err)
	}
	var n int
	s.DB().QueryRow("SELECT count(*) FROM tool_policies").Scan(&n)
	if n != 0 {
		t.Error("a moved connector kept the policies of its tools")
	}
	k.URL = srv.URL + "/mcp"
	Replace(bg(), s, k.Connector)
	o.Check(bg(), s, "notes")
	s.DB().Exec("INSERT INTO tool_policies(tool, policy, updated_at) VALUES('notes_add', 'always', 'x')")
	if err := Remove(bg(), s, "notes"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"connectors", "connector_tools", "chat_connectors", "tool_policies"} {
		s.DB().QueryRow("SELECT count(*) FROM " + table).Scan(&n)
		if n != 0 {
			t.Errorf("removing the connector left %d rows in %s", n, table)
		}
	}
	if _, kept, _ := s.Secrets().Get("connector.notes"); !kept {
		t.Error("removing the connector removed its token from the vault")
	}
}

// What a call is offered: each tool by name with one line and an empty
// schema, and load_tools; the full definitions once loaded in the branch.
// A tool runs, its result is outside content with the token taken out, and
// a tool that fails is an error.
func TestOfferAndCallOverHTTP(t *testing.T) {
	s, other := stores(t)
	notes := mcptest.Notes()
	notesHTTP(t, s, notes)
	p := NewPool(options(t), t.Logf)
	defer p.Close()
	c, _ := chat.Create(s)

	offer, run := p.ForCall(bg(), s, c.ID, nil, nil)
	if got := offered(offer); got != "load_tools notes_add notes_fail notes_list_tags notes_search notes_whoami" {
		t.Fatalf("the call was offered %q", got)
	}
	for _, d := range offer {
		if d.Name == LoadName {
			continue
		}
		if string(d.InputSchema) != string(stubSchema) || !strings.Contains(d.Description, "load_tools first") || strings.Contains(d.Description, "\n") {
			t.Errorf("%s was offered whole before it was loaded: %+v", d.Name, d)
		}
	}
	// The connector's results are outside content, so its words are offered
	// only through load_tools (P13-01, decision 0760).
	if strings.Contains(offer[4].Description, "Searches the notes") || !strings.Contains(offer[4].Description, "Notes connector") {
		t.Errorf("the search was offered with %q, want its connector's name and none of the server's words", offer[4].Description)
	}

	// load_tools gives the definitions, as outside content.
	lt := run[LoadName]
	if !lt.ReadOnly || lt.Inside {
		t.Errorf("load_tools is %+v", lt)
	}
	text, err := lt.Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{"names":["notes_search","notes_nope"]}`)})
	if err != nil || !tools.IsOutside(resultBlock(text)) || !strings.Contains(text, `"query"`) || !strings.Contains(text, "notes_nope") || !strings.Contains(text, "it only reads") {
		t.Errorf("load_tools gave %q, %v", text, err)
	}
	if _, err := lt.Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{"names":["notes_nope"]}`)}); err == nil {
		t.Error("load_tools of no tool of the connectors did not fail")
	}

	// Once loaded in the branch, the search is offered whole, and load_tools
	// is still offered for the rest.
	branch := []seam.Message{
		{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockToolCall, ID: "c1", Name: LoadName, Input: json.RawMessage(`{"names":["notes_search"]}`)}}},
		{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockToolResult, ID: "c1", Content: []seam.Block{{Type: seam.BlockText, Text: text}}}}},
	}
	offer, run = p.ForCall(bg(), s, c.ID, branch, nil)
	for _, d := range offer {
		if d.Name == "notes_search" && !strings.Contains(string(d.InputSchema), "query") {
			t.Errorf("the loaded search was offered as %+v", d)
		}
		if d.Name == "notes_add" && string(d.InputSchema) != string(stubSchema) {
			t.Errorf("a tool that was not loaded was offered whole: %+v", d)
		}
	}

	// A call runs, through the pool, and is outside content.
	res, err := run["notes_search"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{"query":"harbour"}`)})
	if err != nil || !tools.IsOutside(resultBlock(res)) || !strings.Contains(res, `Two notes match "harbour"`) {
		t.Errorf("the search gave %q, %v", res, err)
	}
	if run["notes_add"].ReadOnly || !run["notes_search"].ReadOnly || run["notes_search"].Inside {
		t.Error("the tools' flags are not the server's")
	}
	if d := tools.Summary(run["notes_add"], json.RawMessage(`{"title":"Tide"}`)); !strings.Contains(d, "Notes connector's add") {
		t.Errorf("the approval card would say %q", d)
	}
	// The token is sent, and taken out of what comes back.
	res, err = run["notes_whoami"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{}`)})
	if err != nil || strings.Contains(res, invented) || !strings.Contains(res, removed) {
		t.Errorf("whoami gave %q, %v", res, err)
	}
	_, err = run["notes_fail"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "closed for the night") || strings.Contains(err.Error(), invented) {
		t.Errorf("a tool that fails gave %v", err)
	}
	// Another person's store cannot run this person's connector.
	if _, err := run["notes_search"].Run(bg(), tools.Input{Store: other, Args: json.RawMessage(`{"query":"x"}`)}); err == nil {
		t.Error("another person ran this person's connector")
	}
	if notes.Count("search") != 1 || notes.Sessions() != 1 {
		t.Errorf("the server had %d searches over %d sessions", notes.Count("search"), notes.Sessions())
	}
	if got := offeredInfo(Known(bg(), s)); got != "notes_search notes_add notes_list_tags notes_whoami notes_fail load_tools" {
		t.Errorf("the tools known for policies are %q", got)
	}
}

func offeredInfo(list []tools.Info) string {
	var out []string
	for _, i := range list {
		out = append(out, i.Name)
	}
	return strings.Join(out, " ")
}

func resultBlock(text string) seam.Block {
	return seam.Block{Type: seam.BlockToolResult, Content: []seam.Block{{Type: seam.BlockText, Text: text}}}
}

// A connector that is off, or switched off for the chat, offers nothing;
// one marked as not outside content gives results with no mark.
func TestDisabledAndSwitchedOff(t *testing.T) {
	s, _ := stores(t)
	notesHTTP(t, s, mcptest.Notes())
	p := NewPool(options(t), t.Logf)
	defer p.Close()
	one, _ := chat.Create(s)
	two, _ := chat.Create(s)
	if offer, _ := p.ForCall(bg(), s, one.ID, nil, nil); len(offer) == 0 {
		t.Fatal("an enabled connector offered nothing")
	}
	SetSwitch(bg(), s, one.ID, "notes", false)
	if offer, run := p.ForCall(bg(), s, one.ID, nil, nil); len(offer) != 0 || len(run) != 0 {
		t.Errorf("a connector switched off for the chat offered %q", offered(offer))
	}
	if offer, _ := p.ForCall(bg(), s, two.ID, nil, nil); len(offer) == 0 {
		t.Error("switching a connector off in one chat switched it off in another")
	}
	SetEnabled(bg(), s, "notes", false)
	SetSwitch(bg(), s, two.ID, "notes", true)
	if offer, run := p.ForCall(bg(), s, two.ID, nil, nil); len(offer) != 0 || len(run) != 0 {
		t.Errorf("a disabled connector offered %q", offered(offer))
	}
	SetEnabled(bg(), s, "notes", true)
	k, _ := Get(bg(), s, "notes")
	k.Outside = false
	Replace(bg(), s, k.Connector)
	_, run := p.ForCall(bg(), s, two.ID, nil, nil)
	res, err := run["notes_search"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{"query":"x"}`)})
	if err != nil || tools.IsOutside(resultBlock(res)) || !run["notes_search"].Inside || !run[LoadName].Inside {
		t.Errorf("a connector marked as inside gave %q, %v", res, err)
	}
	// A name the call already offers keeps it.
	offer, run := p.ForCall(bg(), s, two.ID, nil, func(name string) bool { return name == "notes_search" })
	if strings.Contains(offered(offer), "notes_search") || run["notes_search"].Run != nil {
		t.Errorf("a connector's tool took a name already offered: %q", offered(offer))
	}
}

// Over stdio, the command is the test binary. One session serves several
// calls, the token reaches the command and is taken out of its answers,
// and closing the pool stops it.
func TestStdio(t *testing.T) {
	s, _ := stores(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s.Secrets().Set("connector.files", invented)
	if _, err := Add(bg(), s, Connector{Name: "files", Transport: TransportStdio, Command: exe, Args: []string{"-test.run=^$"},
		Env: map[string]string{mcptest.StdioEnv: "notes"}, Secret: "connector.files", TokenEnv: mcptest.TokenEnv, Enabled: true, Outside: true}, ByCommandLine, 20); err != nil {
		t.Fatal(err)
	}
	p := NewPool(options(t), t.Logf)
	c, _ := chat.Create(s)
	offer, run := p.ForCall(bg(), s, c.ID, nil, nil)
	if !strings.Contains(offered(offer), "files_search") {
		t.Fatalf("the stdio connector offered %q", offered(offer))
	}
	for i := 0; i < 3; i++ {
		if res, err := run["files_search"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{"query":"tide"}`)}); err != nil || !strings.Contains(res, "Two notes match") {
			t.Fatalf("the search over stdio gave %q, %v", res, err)
		}
	}
	res, err := run["files_whoami"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{}`)})
	if err != nil || strings.Contains(res, invented) || !strings.Contains(res, removed) {
		t.Errorf("whoami over stdio gave %q, %v", res, err)
	}
	p.mu.Lock()
	open := len(p.sessions)
	p.mu.Unlock()
	if open != 1 {
		t.Errorf("%d sessions are open for one connector", open)
	}
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("closing the pool did not stop the command")
	}
	if _, err := run["files_search"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{"query":"tide"}`)}); err == nil {
		t.Error("a call after the pool closed ran")
	}
}

// A command that breaks on starting is reported, with the token taken out.
func TestStdioThatBreaks(t *testing.T) {
	s, _ := stores(t)
	exe, _ := os.Executable()
	s.Secrets().Set("connector.broken", invented)
	Add(bg(), s, Connector{Name: "broken", Transport: TransportStdio, Command: exe, Args: []string{"-test.run=^$"},
		Env: map[string]string{mcptest.StdioEnv: "crash"}, Secret: "connector.broken", TokenEnv: mcptest.TokenEnv, Enabled: true, Outside: true}, ByCommandLine, 20)
	k, err := options(t).Check(bg(), s, "broken")
	if err != nil || k.Problem == "" || strings.Contains(k.Problem, invented) || !strings.Contains(k.Problem, removed) {
		t.Errorf("a command that broke was kept as %+v, %v", k, err)
	}
}

// A connector with no token stored says so, and reaches nothing.
func TestMissingToken(t *testing.T) {
	s, _ := stores(t)
	notes := mcptest.Notes()
	notesHTTP(t, s, notes)
	s.Secrets().Delete("connector.notes")
	k, err := options(t).Check(bg(), s, "notes")
	if err != nil || !strings.Contains(k.Problem, "connector.notes") || !strings.Contains(k.Problem, "missing") || notes.Sessions() != 0 {
		t.Errorf("a missing token gave %+v, %v", k, err)
	}
}

// A connector that is not marked local connects only to public addresses,
// as it connects: a name that resolves to this machine is refused. One
// marked local connects only to addresses that are not public.
func TestAddressesAreCheckedAsTheyConnect(t *testing.T) {
	o := options(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("reached")) }))
	defer srv.Close()
	if resp, err := o.client(false).Get(srv.URL); err == nil {
		resp.Body.Close()
		t.Error("a connector that is not local reached this machine")
	}
	resp, err := o.client(true).Get(srv.URL)
	if err != nil {
		t.Fatalf("a local connector did not reach this machine: %v", err)
	}
	resp.Body.Close()
	if resp, err := o.client(true).Get("http://93.184.216.34/"); err == nil {
		resp.Body.Close()
		t.Error("a local connector reached a public address")
	} else if !strings.Contains(err.Error(), "not a public address") {
		t.Errorf("a local connector's refusal of a public address was %v", err)
	}
	// Through the pool too: a stored entry is checked again before it is
	// reached, and a redirect is not followed.
	s, _ := stores(t)
	redirect := httptest.NewServer(http.RedirectHandler("http://169.254.169.254/", http.StatusFound))
	defer redirect.Close()
	Add(bg(), s, Connector{Name: "jumpy", Transport: TransportHTTP, URL: redirect.URL, Local: true, Enabled: true, Outside: true}, ByPerson, 20)
	if k, _ := o.Check(bg(), s, "jumpy"); !strings.Contains(k.Problem, "redirect") {
		t.Errorf("a redirect gave %q", k.Problem)
	}
	s.DB().Exec("UPDATE connectors SET local = 0 WHERE name = 'jumpy'")
	if k, _ := o.Check(bg(), s, "jumpy"); !strings.Contains(k.Problem, "your own server") {
		t.Errorf("a stored entry that is no longer allowed gave %q", k.Problem)
	}
}

// A session is reused, and a new one is opened when the token changes or
// the server forgets the session.
func TestSessions(t *testing.T) {
	s, _ := stores(t)
	notes := mcptest.Notes()
	srv := notesHTTP(t, s, notes)
	p := NewPool(options(t), t.Logf)
	defer p.Close()
	c, _ := chat.Create(s)
	_, run := p.ForCall(bg(), s, c.ID, nil, nil)
	search := func() error {
		_, err := run["notes_search"].Run(bg(), tools.Input{Store: s, Args: json.RawMessage(`{"query":"x"}`)})
		return err
	}
	search()
	search()
	if notes.Sessions() != 1 {
		t.Errorf("two calls opened %d sessions", notes.Sessions())
	}
	// The server forgets the session: the call is made again in a new one.
	req, _ := http.NewRequest(http.MethodDelete, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+invented)
	req.Header.Set("Mcp-Session-Id", "session-1")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	if err := search(); err != nil || notes.Count("search") != 3 {
		t.Errorf("a call after the server forgot the session gave %v, with %d searches", err, notes.Count("search"))
	}
	// A new token is a new session.
	notes.Token = "another invented token"
	s.Secrets().Set("connector.notes", "another invented token")
	if err := search(); err != nil {
		t.Errorf("a call after the token changed gave %v", err)
	}
}
