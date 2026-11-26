package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A route that takes a larger body, as an upload does (P9-02), gets its own
// limit; every other route keeps the handler's.
func TestARouteMayTakeALargerBody(t *testing.T) {
	mux := http.NewServeMux()
	nothing := func(http.ResponseWriter, *http.Request) {}
	mux.HandleFunc("POST /api/v1/chats/{id}/uploads", nothing)
	mux.HandleFunc("POST /api/v1/chats/{id}/turns", nothing)
	mux.HandleFunc("/", nothing)
	limits := map[string]int64{"POST /api/v1/chats/{id}/uploads": 5000}
	for path, want := range map[string]int64{
		"/api/v1/chats/abc/uploads": 5000,
		"/api/v1/chats/abc/turns":   100,
		"/api/v1/no/such/route":     100,
	} {
		if got := bodyLimit(mux, limits, httptest.NewRequest("POST", path, nil), 100); got != want {
			t.Errorf("POST %s may take %d bytes, want %d", path, got, want)
		}
	}
	if got := bodyLimit(mux, limits, httptest.NewRequest("GET", "/api/v1/chats/abc/uploads", nil), 100); got != 100 {
		t.Errorf("a GET of the upload route may take %d bytes, want the handler's 100", got)
	}
	if got := bodyLimit(mux, nil, httptest.NewRequest("POST", "/api/v1/chats/abc/uploads", nil), 100); got != 100 {
		t.Errorf("with no route of its own, a body may take %d bytes", got)
	}
}
