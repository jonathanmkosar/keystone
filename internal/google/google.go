// Package google is Keystone's side of Google (P12-02): the OAuth 2.0
// client that signs a person in for Gmail and Google Calendar, and the tools
// those two connectors offer, as plain data and functions.
//
// It holds no token and reads no setting. The connectors package reads the
// person's refresh token from their vault, gets access tokens through
// Refresh, and gives each tool's Run an API whose Access function hands the
// current access token over; the addresses, the HTTP client and the limits
// come in the same way. What a tool returns is text for a model, and what a
// Google endpoint says in an error is reduced to a short reason, so nothing
// here repeats a token. docs/architecture/google.md is the whole of it.
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Service is one of the two Google services Keystone connects to. Its name
// is also the name of its connector.
type Service string

const (
	Gmail    Service = "gmail"
	Calendar Service = "calendar"
)

// The scopes, the narrowest that do each service's work.
const (
	// ScopeGmailRead reads messages and their metadata: search and read.
	ScopeGmailRead = "https://www.googleapis.com/auth/gmail.readonly"
	// ScopeGmailCompose makes drafts and sends mail, and reads nothing else.
	ScopeGmailCompose = "https://www.googleapis.com/auth/gmail.compose"
	// ScopeCalendar sees and changes the events of calendars the person
	// owns, and no calendar shared with them.
	ScopeCalendar = "https://www.googleapis.com/auth/calendar.events.owned"
)

// identityScopes name the account that signed in, so Settings can say which
// one is connected. They give no access to mail or events.
var identityScopes = []string{"openid", "email"}

// Services returns both services, in the order the screens list them.
func Services() []Service { return []Service{Gmail, Calendar} }

// ParseService reads a service's name.
func ParseService(s string) (Service, bool) {
	switch Service(strings.TrimSpace(s)) {
	case Gmail:
		return Gmail, true
	case Calendar:
		return Calendar, true
	}
	return "", false
}

// Label is what the screens call a service.
func (s Service) Label() string {
	if s == Calendar {
		return "Google Calendar"
	}
	return "Gmail"
}

// Scopes are the scopes a service needs.
func (s Service) Scopes() []string {
	if s == Calendar {
		return []string{ScopeCalendar}
	}
	return []string{ScopeGmailRead, ScopeGmailCompose}
}

// ScopesFor returns the scopes a sign-in asks for: the account's identity
// and each service's, once each.
func ScopesFor(services []Service) []string {
	out := append([]string{}, identityScopes...)
	seen := map[string]bool{}
	for _, s := range out {
		seen[s] = true
	}
	for _, s := range services {
		for _, sc := range s.Scopes() {
			if !seen[sc] {
				seen[sc] = true
				out = append(out, sc)
			}
		}
	}
	return out
}

// Granted reports whether every scope a service needs is among those
// Google granted. A person may untick one on Google's consent screen.
func (s Service) Granted(scopes []string) bool {
	have := map[string]bool{}
	for _, sc := range scopes {
		have[sc] = true
	}
	for _, sc := range s.Scopes() {
		if !have[sc] {
			return false
		}
	}
	return true
}

// Endpoints are Google's addresses: settings in settings/agents.json, so a
// test can point them at a stand-in on this machine.
type Endpoints struct {
	Auth     string // agents.google_auth_endpoint
	Token    string // agents.google_token_endpoint
	Revoke   string // agents.google_revoke_endpoint
	Gmail    string // agents.google_gmail_endpoint, ending before /users
	Calendar string // agents.google_calendar_endpoint, ending before /calendars
}

// API is what one tool call reaches Google with.
type API struct {
	// HTTP sends the requests. The connectors package gives one that dials
	// public addresses only, uses no proxy and follows no redirect.
	HTTP      *http.Client
	Endpoints Endpoints
	// MaxBytes is the most bytes of one answer that are read.
	MaxBytes int64
	// ListMax is the most messages or events one search or list returns.
	ListMax int
	// Access returns the access token for this call. It is asked once a
	// call and the token is sent only as a bearer token to Google.
	Access func(ctx context.Context) (string, error)
	// Now is the time; nil means time.Now. Tests set it.
	Now func() time.Time
}

func (a API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Error is a refusal from a Google endpoint, in words a model or a person
// reads. It never holds what was sent or a token.
type Error struct {
	Status int
	Reason string
}

func (e *Error) Error() string {
	switch {
	case e.Status == http.StatusUnauthorized:
		return "Google did not accept the sign-in: connect Google again on Settings → Connectors"
	case e.Status == http.StatusForbidden && e.Reason != "":
		return "Google refused the call: " + e.Reason
	case e.Status == http.StatusForbidden:
		return "Google refused the call: the sign-in may not allow it. Connect Google again and allow Gmail and Calendar"
	case e.Status == http.StatusNotFound:
		return "Google has no such message, draft or event"
	case e.Status == http.StatusPreconditionFailed:
		return "the event changed since it was read: read it again before you change it"
	case e.Status == http.StatusTooManyRequests:
		return "Google says too many calls were made: try again later"
	case e.Reason != "":
		return fmt.Sprintf("Google answered %d: %s", e.Status, e.Reason)
	}
	return fmt.Sprintf("Google answered %d", e.Status)
}

// reasonOf reads the message of a Google error answer, cut short. A body
// that is not Google's error shape gives none.
func reasonOf(raw []byte) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	return clip(oneLine(body.Error.Message), 300)
}

// call sends one request to a Google API with the access token and reads
// the answer into out, unless out is nil. header adds headers.
func (a API) call(ctx context.Context, method, address string, body any, header map[string]string, out any) error {
	token, err := a.Access(ctx)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return errors.New("Keystone could not write the request to Google")
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		return errors.New("a Google address in the settings is not an address Keystone can call")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Keystone could not reach Google: try again later")
	}
	defer resp.Body.Close()
	raw, err := readMost(resp.Body, a.MaxBytes)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Error{Status: resp.StatusCode, Reason: reasonOf(raw)}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.New("Google's answer could not be read")
	}
	return nil
}

const userAgent = "Keystone (personal assistant)"

// readMost reads an answer of at most max bytes, and refuses a larger one.
func readMost(r io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		max = 4 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, errors.New("Google's answer could not be read")
	}
	if int64(len(raw)) > max {
		return nil, fmt.Errorf("Google's answer was larger than the %d bytes Keystone reads", max)
	}
	return raw, nil
}

// Def is one tool of a service, as plain data and functions. The connectors
// package names it (<service>_<name>), wraps its Run, and offers it.
type Def struct {
	// Name is the tool's name within its service, such as search.
	Name        string
	Title       string
	Description string
	Schema      json.RawMessage
	// ReadOnly says it only reads.
	ReadOnly bool
	// Reaches says one call reaches other people: it sends mail, or
	// invites or tells an event's guests. Nil means no call does. It answers
	// true for input it cannot read.
	Reaches func(input json.RawMessage) bool
	// Describe says exactly what one call will send or change, for the
	// approval card.
	Describe func(input json.RawMessage) string
	// Run makes the call.
	Run func(ctx context.Context, a API, input json.RawMessage) (string, error)
}

// Defs returns a service's tools.
func Defs(s Service) []Def {
	if s == Calendar {
		return calendarDefs()
	}
	return gmailDefs()
}

// Find returns one tool of a service by its name within the service.
func Find(s Service, name string) (Def, bool) {
	for _, d := range Defs(s) {
		if d.Name == name {
			return d, true
		}
	}
	return Def{}, false
}

// always is Reaches for a tool every call of which reaches other people.
func always(json.RawMessage) bool { return true }

// decode reads a tool's input into v, refusing fields it does not know, so
// a misspelt field is an error the model reads and not a silent default.
func decode(input json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("the input does not fit the tool's arguments: %s", clip(err.Error(), 200))
	}
	return nil
}

// oneLine folds every run of white space into one space.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// clip keeps the first most characters of a text, with an ellipsis when it
// left some out.
func clip(s string, most int) string {
	if utf8.RuneCountInString(s) <= most {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:most-1])) + "…"
}

// quote puts a text in curly quotes, folded to one line and cut short.
func quote(s string, most int) string { return "“" + clip(oneLine(s), most) + "”" }

// plainLine reports whether a text is one line with no control characters.
func plainLine(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
