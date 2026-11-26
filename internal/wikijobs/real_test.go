package wikijobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The item's proof against a real Waymarks: the binary started on this
// machine with a throwaway data folder, on a free port from 7955 to 7959
// (never the owner's instances), with invented principals whose tokens
// live in memory for the test alone. It skips, saying why, on a machine
// with no Waymarks binary, so verify stays green there.
//
// The binary is found from KEYSTONE_WAYMARKS_BIN, then as waymarks on the
// path, then as Waymarks/waymarks/bin/waymarks beside a folder above this
// one, where the owner keeps it.
func TestAFactToldInChatBecomesACitedPageOnARealWaymarks(t *testing.T) {
	bin := waymarksBinary()
	if bin == "" {
		t.Skip("no Waymarks binary on this machine: set KEYSTONE_WAYMARKS_BIN to its path to run the proof against a real Waymarks")
	}
	port := freePort(7955, 7959)
	if port == 0 {
		t.Skip("no port from 7955 to 7959 is free on this machine, so no Waymarks test instance was started")
	}
	data := filepath.Join(t.TempDir(), "waymarks")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var out, errOut bytes.Buffer
	initCmd := exec.CommandContext(ctx, bin, "init", "--data", data, "--owner", "keeper")
	initCmd.Stdout, initCmd.Stderr = &out, &errOut
	if err := initCmd.Run(); err != nil {
		t.Fatalf("waymarks init: %v: %s", err, errOut.String())
	}
	owner := strings.TrimSpace(out.String())
	address := "http://127.0.0.1:" + strconv.Itoa(port)
	serve := exec.CommandContext(ctx, bin, "serve", "--data", data, "--listen", "127.0.0.1:"+strconv.Itoa(port))
	var serveOut bytes.Buffer
	serve.Stdout, serve.Stderr = io.Discard, &serveOut
	if err := serve.Start(); err != nil {
		t.Fatalf("waymarks serve: %v", err)
	}
	t.Cleanup(func() {
		_ = serve.Process.Kill()
		_ = serve.Wait()
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		// Any answer means it is up: the health route may want a token.
		if resp, err := http.Get(address + "/v1/health"); err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Waymarks test instance did not answer: %s", serveOut.String())
		}
		time.Sleep(100 * time.Millisecond)
	}

	call := func(method, path, token string, body any, into any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			rd = bytes.NewReader(raw)
		}
		req, _ := http.NewRequestWithContext(ctx, method, address+path, rd)
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		var env struct {
			Data  json.RawMessage `json:"data"`
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		raw, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(raw, &env); err != nil || env.Error != nil || resp.StatusCode >= 300 {
			msg := string(raw)
			if env.Error != nil {
				msg = env.Error.Code + ": " + env.Error.Message
			}
			t.Fatalf("%s %s answered %d: %s", method, path, resp.StatusCode, strings.ReplaceAll(msg, token, "(a token)"))
		}
		if into != nil {
			if err := json.Unmarshal(env.Data, into); err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
		}
	}

	// The space the memory pass writes to, and one principal for each role,
	// each with the agent kit's grant.
	call("PUT", "/v1/spaces/memory", owner, map[string]string{"label": "normal", "title": "Memory"}, nil)
	tokens := map[string]string{"keeper": owner}
	for name, actions := range map[string][]string{
		"keystone-reader":  {"read", "search"},
		"keystone-intake":  {"add_sources"},
		"keystone-compile": {"read", "search", "write_pages", "delete_pages"},
	} {
		call("POST", "/v1/principals", owner, map[string]any{"name": name, "kind": "agent", "tool": "Keystone (test)", "model": "an invented local model",
			"grant": []map[string]any{{"space": "*", "actions": actions}}}, nil)
		var issued struct {
			Token string `json:"token"`
		}
		call("POST", "/v1/principals/"+name+"/tokens", owner, map[string]string{"label": "the wiki jobs test"}, &issued)
		if issued.Token == "" {
			t.Fatalf("no token was issued for %s", name)
		}
		tokens[name] = issued.Token
	}

	wm := underTest{address: address, tokens: tokens}
	wm.audit = func() []fakeEntry {
		var list []fakeEntry
		call("GET", "/v1/audit?limit=1000", owner, nil, &list)
		return list
	}
	wm.sources = func() []fakeSource {
		var list []fakeSource
		call("GET", "/v1/sources", owner, nil, &list)
		return list
	}
	wm.pages = func() []fakePage {
		var list []fakePage
		call("GET", "/v1/pages", owner, nil, &list)
		for i, p := range list {
			call("GET", fmt.Sprintf("/v1/pages/%d", p.ID), owner, nil, &list[i])
		}
		return list
	}
	before := len(wm.pages())
	proveCompileFrom(t, wm, before)
}

// waymarksBinary finds the Waymarks binary, or returns "".
func waymarksBinary() string {
	name := "waymarks"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if p := os.Getenv("KEYSTONE_WAYMARKS_BIN"); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
		return ""
	}
	if p, err := exec.LookPath("waymarks"); err == nil {
		return p
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(dir, "Waymarks", "waymarks", "bin", name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
		up := filepath.Dir(dir)
		if up == dir {
			return ""
		}
		dir = up
	}
}

// freePort returns the first port from low to high nothing listens on.
func freePort(low, high int) int {
	for p := low; p <= high; p++ {
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err == nil {
			l.Close()
			return p
		}
	}
	return 0
}
