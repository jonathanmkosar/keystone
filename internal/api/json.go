package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jonathanmkosar/keystone/internal/audit"
)

// ErrorBody is the JSON body of every answer that is not a success.
type ErrorBody struct {
	// Error says what went wrong, in words a person can read.
	Error string `json:"error"`
	// Code is a short fixed name for the failure, for a client to test.
	Code string `json:"code"`
	// RequestID names the call in the audit log and the server's log.
	RequestID string `json:"request_id"`
}

// WriteJSON answers with v as JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Error answers with an ErrorBody. The message is searched for keys and
// tokens first, because it is often made from another error's text.
func Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	WriteJSON(w, status, ErrorBody{
		Error:     audit.Scrub(message),
		Code:      code,
		RequestID: audit.RequestID(r.Context()),
	})
}

// ReadJSON reads the request's body into v. When the body is not the JSON
// the route takes, or is too large, it answers the call itself and returns
// false, and the handler has nothing more to do. A field v does not have
// is refused, so a misspelt field is not silently dropped.
func ReadJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil {
		if _, more := dec.Token(); more != io.EOF {
			err = errors.New("more than one JSON value")
		}
	}
	if err == nil {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		Error(w, r, http.StatusRequestEntityTooLarge, "too_large", "the request body is too large")
		return false
	}
	// The decoder's own message can quote the body, so it is not sent back.
	Error(w, r, http.StatusBadRequest, "bad_request", "the request body is not the JSON this route takes")
	return false
}
