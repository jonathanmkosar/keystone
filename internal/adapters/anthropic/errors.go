package anthropic

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// classify sorts a failure into one of the seam's kinds, and says whether
// the same call may work if sent again. typ is the provider's own error
// type, which is trusted first. status is the HTTP status, or zero for an
// error that came inside a stream.
func classify(status int, typ, message string) (seam.ErrorKind, bool) {
	switch typ {
	case "authentication_error", "permission_error":
		return seam.ErrAuth, false
	case "rate_limit_error":
		return seam.ErrRateLimited, true
	case "overloaded_error":
		return seam.ErrOverloaded, true
	case "request_too_large":
		return seam.ErrTooLong, false
	case "invalid_request_error", "not_found_error":
		if saysTooLong(message) {
			return seam.ErrTooLong, false
		}
		return seam.ErrInvalid, false
	case "billing_error":
		return seam.ErrOther, false
	case "api_error", "timeout_error":
		return seam.ErrUnavailable, true
	}
	// No type, or one this adapter does not know: a gateway's own answer.
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return seam.ErrAuth, false
	case status == http.StatusTooManyRequests:
		return seam.ErrRateLimited, true
	case status == 529:
		return seam.ErrOverloaded, true
	case status == http.StatusRequestEntityTooLarge:
		return seam.ErrTooLong, false
	case status == http.StatusBadRequest, status == http.StatusNotFound, status == http.StatusUnprocessableEntity:
		if saysTooLong(message) {
			return seam.ErrTooLong, false
		}
		return seam.ErrInvalid, false
	case status == http.StatusRequestTimeout, status >= 500:
		return seam.ErrUnavailable, true
	case status == http.StatusConflict:
		return seam.ErrOther, true
	case status >= 300 && status < 400:
		// A redirect, which is not followed. The entry's address is wrong.
		return seam.ErrUnavailable, false
	}
	return seam.ErrOther, false
}

// saysTooLong reports whether a refusal is about the size of the request.
// The API has no error type of its own for it.
func saysTooLong(message string) bool {
	m := strings.ToLower(message)
	for _, phrase := range []string{"prompt is too long", "context window", "context length", "context limit", "too many tokens"} {
		if strings.Contains(m, phrase) {
			return true
		}
	}
	return false
}

// describe writes the message of a failure. It is not yet cleaned.
func describe(status int, e wireError, requestID string) string {
	s := "the endpoint ended the stream with an error"
	if status != 0 {
		s = "the endpoint answered " + strconv.Itoa(status)
	}
	if e.Type != "" {
		s += " (" + e.Type + ")"
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	if requestID != "" {
		s += " [request " + requestID + "]"
	}
	return s
}

// statusFault reads an answer that is not 200 and returns the fault, and
// the wait the provider asked for, if it asked.
func (p *Provider) statusFault(resp *http.Response) (*seam.Error, time.Duration) {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var body struct {
		Error wireError `json:"error"`
	}
	// A gateway may answer with something that is not the API's error
	// shape. Then the status alone decides.
	_ = json.Unmarshal(raw, &body)

	kind, retryable := classify(resp.StatusCode, body.Error.Type, body.Error.Message)
	// The provider can say outright whether to send the call again.
	switch strings.ToLower(resp.Header.Get("X-Should-Retry")) {
	case "true":
		retryable = true
	case "false":
		retryable = false
	}
	fault := &seam.Error{
		Kind:      kind,
		Retryable: retryable,
		Message:   p.clean(describe(resp.StatusCode, body.Error, resp.Header.Get("Request-Id"))),
	}
	return fault, retryAfter(resp.Header)
}

// retryAfter reads how long the provider asked Keystone to wait.
func retryAfter(h http.Header) time.Duration {
	if ms, err := strconv.ParseFloat(h.Get("Retry-After-Ms"), 64); err == nil && ms > 0 {
		return time.Duration(min(ms, 1e9) * float64(time.Millisecond))
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(min(secs, 1e6) * float64(time.Second))
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}
