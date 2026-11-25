package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerServesTheEmbeddedPage(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET /: content type %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "<title>Keystone</title>") {
		t.Errorf("GET / is not the Keystone page:\n%s", rec.Body.String())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("the page came without its security headers: %v", rec.Header())
	}
}

func TestHandlerServesThePageFiles(t *testing.T) {
	for path, want := range map[string]string{"/keystone.css": "text/css", "/mark.svg": "image/svg+xml"} {
		rec := httptest.NewRecorder()
		handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), want) {
			t.Errorf("GET %s: status %d, content type %q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

// A missing file is 404, and so is anything under /api when no API is
// mounted. A path with no file extension is a route of the interface and
// gets the page (decision 0071).
func TestHandlerRefusesWhatItDoesNotServe(t *testing.T) {
	for _, p := range []string{"/no-such-file.js", "/assets/no-such-file.css", "/api/v1/nothing", "/api"} {
		rec := httptest.NewRecorder()
		handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", p, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /: status %d, want 405", rec.Code)
	}
}

func TestHandlerServesThePageForAnInterfaceRoute(t *testing.T) {
	for _, p := range []string{"/chats", "/chats/abc123", "/settings/"} {
		rec := httptest.NewRecorder()
		handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") || !strings.Contains(rec.Body.String(), "<html") {
			t.Errorf("GET %s: status %d, content type %q, want the page", p, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

func TestServeListensAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, "127.0.0.1:0", handler(nil), pw) }()

	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(line, "http://")
	if i < 0 {
		t.Fatalf("serve did not say where it listens: %q", line)
	}
	resp, err := http.Get(strings.TrimSpace(line[i:]) + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d over a real connection", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve stopped with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop when asked")
	}
}

func TestServeRefusesABadAddress(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"serve", "--address", "not an address", "--data", t.TempDir()}, &out, &errw); code != 1 {
		t.Errorf("want exit 1, got %d (%s)", code, errw.String())
	}
}
