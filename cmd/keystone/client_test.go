package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// An invented password, used in a temporary folder only.
const testPassword = "test person one's phrase"

// testServer makes a data folder with one person in it and serves it as
// keystone serve does. It returns the server's address, the person's id
// and the server's log.
func testServer(t *testing.T) (string, string, *bytes.Buffer) {
	t.Helper()
	data := t.TempDir()
	in, err := tenant.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	o, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	if err := signin.SetPassword(s, testPassword, o); err != nil {
		t.Fatal(err)
	}
	s.Close()

	log := &bytes.Buffer{}
	api, err := openAPI(data, "file", log)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler(api))
	t.Cleanup(srv.Close)
	return srv.URL, made.ID, log
}

func TestServeServesTheAPIBesideThePage(t *testing.T) {
	url, _, _ := testServer(t)
	for path, want := range map[string]int{
		"/":                     http.StatusOK,
		"/api/v1/signin/people": http.StatusOK,
		"/api/v1/session":       http.StatusUnauthorized,
		"/api/v1/nothing":       http.StatusUnauthorized,
		"/api":                  http.StatusUnauthorized,
	} {
		resp, err := http.Get(url + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s: status %d, want %d", path, resp.StatusCode, want)
		}
		if strings.HasPrefix(path, "/api") && resp.Header.Get("X-Request-Id") == "" {
			t.Errorf("GET %s came with no request id", path)
		}
	}
}

func TestTheClientSignsInCallsAndSignsOut(t *testing.T) {
	url, id, log := testServer(t)
	file := filepath.Join(t.TempDir(), "profile", "session.json")
	t.Setenv(sessionFileEnv, file)

	var printed strings.Builder
	// client runs one command and returns what it printed and its exit code.
	client := func(stdin string, args ...string) (string, int) {
		var out, errw strings.Builder
		code := runClient(args[0], args[1:], strings.NewReader(stdin), &out, &errw)
		printed.WriteString(out.String() + errw.String())
		return out.String() + errw.String(), code
	}

	if got, code := client("", "people", "--server", url); code != 0 || !strings.Contains(got, id) || !strings.Contains(got, "Test Person One") {
		t.Fatalf("people: exit %d\n%s", code, got)
	}
	if got, code := client("", "whoami", "--server", url); code != 1 || !strings.Contains(got, "sign in first") {
		t.Errorf("whoami before signing in: exit %d\n%s", code, got)
	}
	if got, code := client("not the phrase at all\n", "signin", "--server", url, id); code != 1 || !strings.Contains(got, "sign-in refused") {
		t.Errorf("signin with a wrong password: exit %d\n%s", code, got)
	}
	if _, err := os.Stat(file); err == nil {
		t.Errorf("a refused sign-in left a session file")
	}
	if got, code := client("", "signin", "--server", url, id); code != 1 {
		t.Errorf("signin with no password: exit %d\n%s", code, got)
	}
	if got, code := client(testPassword+"\r\n", "signin", "--server", url, id); code != 0 || !strings.Contains(got, "Test Person One") {
		t.Fatalf("signin: exit %d\n%s", code, got)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var saved savedSession
	if err := json.Unmarshal(raw, &saved); err != nil || saved.Server != url || !strings.HasPrefix(saved.Token, "kss_"+id+"_") {
		t.Fatalf("the session file does not hold the session (%v)", err)
	}

	// The server is remembered from here on.
	if got, code := client("", "whoami"); code != 0 || !strings.Contains(got, id) || !strings.Contains(got, "password") {
		t.Errorf("whoami: exit %d\n%s", code, got)
	}
	if got, code := client("", "api", "get", "/api/v1/audit?limit=2"); code != 0 || !strings.Contains(got, `"entries": [`) {
		t.Errorf("api GET audit: exit %d\n%s", code, got)
	}
	if got, code := client("", "api", "GET", "stream/test?count=2"); code != 0 || !strings.Contains(got, "id: 2\nevent: tick\ndata: {\"n\":2}\n") || !strings.Contains(got, "event: done\n") {
		t.Errorf("api GET stream/test: exit %d\n%s", code, got)
	}
	if got, code := client("", "api", "GET", "/api/v1/nothing"); code != 1 || !strings.Contains(got, "no such route") || !strings.Contains(got, "404 no_route, request ") {
		t.Errorf("api GET of no route: exit %d\n%s", code, got)
	}
	if _, code := client("", "api", "GET"); code != 2 {
		t.Errorf("api with no path: exit %d, want 2", code)
	}

	// The token is not sent to a server it did not come from.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Cookies()) != 0 || r.Header.Get("Cookie") != "" {
			t.Errorf("the session went to another server")
		}
		w.Write([]byte(`{}`))
	}))
	defer other.Close()
	client("", "api", "--server", other.URL, "GET", "/api/v1/session")

	if got, code := client("", "signout"); code != 0 || !strings.Contains(got, "Signed out of "+url) {
		t.Errorf("signout: exit %d\n%s", code, got)
	}
	if _, err := os.Stat(file); err == nil {
		t.Errorf("signout left the session file")
	}
	if got, code := client("", "signout"); code != 0 || !strings.Contains(got, "Not signed in") {
		t.Errorf("signout twice: exit %d\n%s", code, got)
	}
	// The session is ended on the server, not only forgotten here.
	req, _ := http.NewRequest("GET", url+"/api/v1/session", nil)
	req.AddCookie(&http.Cookie{Name: "keystone_session", Value: saved.Token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the signed-out session still answers: %d", resp.StatusCode)
	}

	// Nothing the client printed, and nothing the server logged, holds the
	// token or the password.
	for where, text := range map[string]string{"the client's output": printed.String(), "the server's log": log.String()} {
		if strings.Contains(text, saved.Token) || strings.Contains(text, "kss_") || strings.Contains(text, testPassword) {
			t.Errorf("%s holds the token or the password:\n%s", where, text)
		}
	}
	if !strings.Contains(log.String(), "DELETE /api/v1/session 200") {
		t.Errorf("the server's log does not show the calls:\n%s", log.String())
	}
}

func TestTheClientSaysWhenNoServerAnswers(t *testing.T) {
	t.Setenv(sessionFileEnv, filepath.Join(t.TempDir(), "session.json"))
	var out, errw strings.Builder
	if code := runClient("people", []string{"--server", "http://127.0.0.1:9"}, strings.NewReader(""), &out, &errw); code != 1 || !strings.Contains(errw.String(), "keystone serve") {
		t.Errorf("exit %d: %s", code, errw.String())
	}
}

func TestOpenAPIPrintsTheDocument(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"openapi"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	var doc struct {
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal([]byte(out.String()), &doc); err != nil || doc.Paths["/api/v1/session"] == nil {
		t.Errorf("openapi did not print the document (%v)", err)
	}
	file := filepath.Join(t.TempDir(), "openapi.json")
	if code := run([]string{"openapi", "--out", file}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if raw, err := os.ReadFile(file); err != nil || !strings.HasPrefix(out.String(), string(raw)) {
		t.Errorf("openapi --out wrote something else (%v)", err)
	}
}
