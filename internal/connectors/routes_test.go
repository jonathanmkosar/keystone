package connectors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/mcp/mcptest"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Invented, for throwaway folders only.
const (
	phraseOne = "connectors person one's phrase"
	phraseTwo = "connectors person two's phrase"
)

type routeRig struct {
	t        *testing.T
	h        http.Handler
	install  *tenant.Install
	one, two string
	tokens   map[string]string
	said     []string
}

func newRouteRig(t *testing.T) *routeRig {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	o, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	g := &routeRig{t: t, install: in, tokens: map[string]string{}}
	for i, person := range []struct{ name, phrase string }{{"Test Person One", phraseOne}, {"Test Person Two", phraseTwo}} {
		made, err := in.Create(person.name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		signin.SetPassword(s, person.phrase, o)
		token, _, err := signin.SignIn(s, person.phrase, o)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
		g.tokens[made.ID] = token
		if i == 0 {
			g.one = made.ID
		} else {
			g.two = made.ID
		}
	}
	if g.h, err = api.Handler(api.Options{Install: in, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, Routes(options(t))); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *routeRig) with(id string, f func(s *tenant.Store)) {
	g.t.Helper()
	s, err := g.install.Store(id)
	if err != nil {
		g.t.Fatal(err)
	}
	defer s.Close()
	f(s)
}

func (g *routeRig) call(who, method, path string, body, out any) *httptest.ResponseRecorder {
	g.t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, api.Prefix+path, nil)
	} else {
		raw, _ := json.Marshal(body)
		r = httptest.NewRequest(method, api.Prefix+path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
	}
	r.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.tokens[who]})
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r)
	g.said = append(g.said, rec.Body.String())
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			g.t.Fatalf("%s %s: %v\n%s", method, path, err, rec.Body)
		}
	}
	return rec
}

func (g *routeRig) want(rec *httptest.ResponseRecorder, status int, code string) {
	g.t.Helper()
	var e api.ErrorBody
	json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != status || (code != "" && e.Code != code) {
		g.t.Errorf("answered %d %q, want %d %q: %s", rec.Code, e.Code, status, code, rec.Body)
	}
}

func TestRoutes(t *testing.T) {
	g := newRouteRig(t)
	notes := mcptest.Notes()
	notes.Token = invented
	srv := httptest.NewServer(notes)
	defer srv.Close()
	g.with(g.one, func(s *tenant.Store) { s.Secrets().Set("connector.notes", invented) })

	// A remote connector is added and reached at once.
	var row Row
	g.want(g.call(g.one, "POST", "connectors", Body{Name: "notes", Label: "Notes", URL: srv.URL + "/mcp", Local: true, Secret: "connector.notes"}, &row), http.StatusCreated, "")
	if row.Transport != TransportHTTP || !row.Enabled || !row.Outside || !row.TokenPresent || len(row.Tools) != 5 || row.Problem != "" || row.AddedBy != ByPerson {
		t.Errorf("the connector added is %+v", row)
	}
	// A command is never added or set through the API.
	exe, _ := os.Executable()
	for _, b := range []Body{
		{Name: "files", Transport: TransportStdio, Command: exe},
		{Name: "files", URL: "https://mcp.example/mcp", Command: exe},
		{Name: "files", URL: "https://mcp.example/mcp", Env: map[string]string{"NODE_OPTIONS": "--require x"}},
		{Name: "files", URL: "https://mcp.example/mcp", TokenEnv: "NODE_OPTIONS"},
	} {
		g.want(g.call(g.one, "POST", "connectors", b, nil), http.StatusForbidden, "command_line_only")
	}
	g.want(g.call(g.one, "POST", "connectors", Body{Name: "notes", URL: "https://mcp.example/mcp"}, nil), http.StatusConflict, "exists")
	g.want(g.call(g.one, "POST", "connectors", Body{Name: "web", URL: "http://192.168.1.9/mcp"}, nil), http.StatusBadRequest, "bad_connector")
	g.want(g.call(g.one, "POST", "connectors", map[string]any{"name": "x", "surprise": true}, nil), http.StatusBadRequest, "bad_request")

	// The list holds the token's name, never the token.
	var list ListReply
	g.want(g.call(g.one, "GET", "connectors", nil, &list), http.StatusOK, "")
	if len(list.Connectors) != 1 || list.Max != 20 || list.Connectors[0].Secret != "connector.notes" {
		t.Errorf("the list is %+v", list)
	}
	var theirs ListReply
	g.call(g.two, "GET", "connectors", nil, &theirs)
	if len(theirs.Connectors) != 0 {
		t.Error("another person sees this person's connectors")
	}
	g.want(g.call(g.two, "DELETE", "connectors/notes", nil, nil), http.StatusNotFound, "not_found")

	// A change: the label and the switch. The name stays in the address.
	off := false
	g.want(g.call(g.one, "PUT", "connectors/notes", Body{Label: "My notes", URL: srv.URL + "/mcp", Local: true, Secret: "connector.notes", Enabled: &off}, &row), http.StatusOK, "")
	if row.Label != "My notes" || row.Enabled || len(row.Tools) != 5 {
		t.Errorf("the changed connector is %+v", row)
	}
	g.want(g.call(g.one, "PUT", "connectors/notes", Body{Name: "other", URL: srv.URL}, nil), http.StatusBadRequest, "bad_connector")
	g.want(g.call(g.one, "PUT", "connectors/notes", Body{URL: srv.URL, Local: true, Command: exe}, nil), http.StatusForbidden, "command_line_only")
	g.want(g.call(g.one, "PUT", "connectors/nothing", Body{URL: srv.URL}, nil), http.StatusNotFound, "not_found")

	// A connector added from the command line: only what cannot run a
	// program may change through the API.
	g.with(g.one, func(s *tenant.Store) {
		if _, err := Add(bg(), s, Connector{Name: "files", Transport: TransportStdio, Command: exe, Args: []string{"-test.run=^$"}, Env: map[string]string{mcptest.StdioEnv: "notes"}, Enabled: true, Outside: true}, ByCommandLine, 20); err != nil {
			t.Fatal(err)
		}
	})
	for _, b := range []Body{{Command: "calc"}, {Env: map[string]string{"X": "y"}}, {TokenEnv: "NODE_OPTIONS"}, {Transport: TransportHTTP, URL: "https://mcp.example/mcp"}, {Args: []string{"--evil"}}} {
		g.want(g.call(g.one, "PUT", "connectors/files", b, nil), http.StatusForbidden, "command_line_only")
	}
	g.want(g.call(g.one, "PUT", "connectors/files", Body{Label: "Files", Check: &off}, &row), http.StatusOK, "")
	if row.Label != "Files" || row.Command != exe || len(row.Args) != 1 || len(row.EnvNames) != 1 || row.EnvNames[0] != mcptest.StdioEnv || row.AddedBy != ByCommandLine {
		t.Errorf("the stdio connector after a change is %+v", row)
	}
	g.want(g.call(g.one, "POST", "connectors/files/check", nil, &row), http.StatusOK, "")
	if len(row.Tools) != 5 || row.Problem != "" {
		t.Errorf("the stdio connector's check gave %+v", row)
	}

	// The chat's menu.
	var c chat.Chat
	g.with(g.one, func(s *tenant.Store) { c, _ = chat.Create(s) })
	var menu ChatReply
	g.want(g.call(g.one, "GET", "chats/"+c.ID+"/connectors", nil, &menu), http.StatusOK, "")
	if len(menu.Connectors) != 2 || menu.Connectors[0].Name != "files" || !menu.Connectors[0].On || menu.Connectors[1].On || menu.Connectors[1].Enabled {
		t.Errorf("the menu is %+v", menu)
	}
	g.want(g.call(g.one, "PUT", "chats/"+c.ID+"/connectors/files", SwitchBody{On: false}, &menu), http.StatusOK, "")
	if menu.Connectors[0].On || !menu.Connectors[0].Switched {
		t.Errorf("after switching files off the menu is %+v", menu)
	}
	g.want(g.call(g.two, "GET", "chats/"+c.ID+"/connectors", nil, nil), http.StatusNotFound, "not_found")
	g.want(g.call(g.one, "PUT", "chats/"+c.ID+"/connectors/nothing", SwitchBody{On: true}, nil), http.StatusNotFound, "not_found")
	g.want(g.call(g.one, "GET", "chats/no-such-chat/connectors", nil, nil), http.StatusNotFound, "not_found")

	// Removed, it is gone.
	g.want(g.call(g.one, "DELETE", "connectors/notes", nil, nil), http.StatusOK, "")
	g.want(g.call(g.one, "POST", "connectors/notes/check", nil, nil), http.StatusNotFound, "not_found")

	for _, s := range g.said {
		if strings.Contains(s, invented) {
			t.Fatalf("an answer holds the token: %s", s)
		}
	}
}
