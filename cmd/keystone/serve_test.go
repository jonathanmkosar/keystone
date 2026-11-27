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

// Dictation (P10-01) needs the microphone on Keystone's own pages and on no
// other origin's, and the content security policy stays as it was.
func TestHandlerAllowsTheMicrophoneToItsOwnOriginOnly(t *testing.T) {
	for _, p := range []string{"/", "/chats/abc123", "/keystone.css", "/api/v1/nothing"} {
		rec := httptest.NewRecorder()
		handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if got := rec.Header().Get("Permissions-Policy"); got != "microphone=(self)" {
			t.Errorf("GET %s: Permissions-Policy %q, want microphone=(self)", p, got)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'self'" {
			t.Errorf("GET %s: Content-Security-Policy %q, want default-src 'self'", p, got)
		}
	}
}

// Location per message (P10-06) needs the browser's position on Keystone's
// own pages and on no other origin's, beside the microphone, and the other
// headers stay as they were. The two lines are one policy to a browser,
// which joins a header's lines with a comma (decision 0602).
func TestHandlerAllowsTheLocationToItsOwnOriginOnly(t *testing.T) {
	for _, p := range []string{"/", "/chats/abc123", "/keystone.css", "/api/v1/nothing"} {
		rec := httptest.NewRecorder()
		handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		h := rec.Header()
		if got := strings.Join(h.Values("Permissions-Policy"), ", "); got != "microphone=(self), geolocation=(self)" {
			t.Errorf("GET %s: Permissions-Policy reads %q, want microphone=(self), geolocation=(self)", p, got)
		}
		for name, want := range map[string]string{
			"Content-Security-Policy": "default-src 'self'",
			"X-Content-Type-Options":  "nosniff",
			"X-Frame-Options":         "DENY",
			"Referrer-Policy":         "no-referrer",
		} {
			if got := h.Values(name); len(got) != 1 || got[0] != want {
				t.Errorf("GET %s: %s is %q, want %q alone", p, name, got, want)
			}
		}
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

// A file whose name stays the same from build to build is asked for again
// each time and kept by its ETag; a hashed file under /assets is kept.
func TestHandlerTellsBrowsersWhatToKeep(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/keystone.css", nil))
	tag := rec.Header().Get("ETag")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" || tag == "" {
		t.Fatalf("GET /keystone.css: %d, Cache-Control %q, ETag %q", rec.Code, rec.Header().Get("Cache-Control"), tag)
	}
	again := httptest.NewRequest(http.MethodGet, "/keystone.css", nil)
	again.Header.Set("If-None-Match", tag)
	rec = httptest.NewRecorder()
	handler(nil).ServeHTTP(rec, again)
	if rec.Code != http.StatusNotModified {
		t.Errorf("GET /keystone.css with its ETag: %d, want 304", rec.Code)
	}
	page := httptest.NewRecorder()
	handler(nil).ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	body := page.Body.String()
	i := strings.Index(body, "\"/assets/")
	if i < 0 {
		t.Fatal("the page names no file under /assets")
	}
	asset := body[i+1:]
	asset = asset[:strings.IndexByte(asset, '"')]
	rec = httptest.NewRecorder()
	handler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, asset, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("GET %s: %d, Cache-Control %q", asset, rec.Code, rec.Header().Get("Cache-Control"))
	}
}
