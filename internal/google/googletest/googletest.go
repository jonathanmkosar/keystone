// Package googletest is a stand-in Google for tests and verify (P12-02): the
// OAuth token and revoke endpoints, and enough of the Gmail and Google
// Calendar APIs for Keystone's tools, served on this machine with httptest.
// Every name, address, message and event in it is invented. It is never in
// the server Keystone ships.
//
// The person's consent is Authorize: it reads the address Keystone sent the
// browser to, checks it as Google would, and returns the address Google
// would send the browser back to, with a code.
package googletest

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/google"
)

// The invented account and OAuth client.
const (
	Email        = "person.one@example.com"
	ClientID     = "invented-client-id.example"
	ClientSecret = "an invented client secret, not a real one"
)

// Call is one request the stand-in took.
type Call struct {
	Method string
	Path   string
	Query  url.Values
	// Auth is the Authorization header it came with.
	Auth string
}

// Sent is an email the stand-in was asked to send.
type Sent struct {
	To, Cc, Bcc []string
	Subject     string
	Body        string
	ThreadID    string
	InReplyTo   string
	DraftID     string
}

// Draft is a draft in the stand-in's mailbox.
type Draft struct {
	ID       string
	ThreadID string
	Mail     Sent
}

// Change is a change made to the stand-in's calendar.
type Change struct {
	Method      string // POST, PATCH or DELETE
	EventID     string
	SendUpdates string
}

type message struct {
	id, thread, from, to, subject, date, messageID string
	labels                                         []string
	plain, html                                    string
	attachment                                     string
}

type event struct {
	ID          string            `json:"id"`
	Status      string            `json:"status"`
	ETag        string            `json:"etag"`
	Summary     string            `json:"summary"`
	Description string            `json:"description,omitempty"`
	Location    string            `json:"location,omitempty"`
	Start       map[string]string `json:"start"`
	End         map[string]string `json:"end"`
	Organizer   map[string]any    `json:"organizer"`
	Attendees   []map[string]any  `json:"attendees,omitempty"`
}

type code struct {
	challenge, redirect string
	scopes              []string
}

// Server is the stand-in. Make one with New.
type Server struct {
	// Grant, when set, is every scope a sign-in is granted, whatever it
	// asked for: a person who unticked one on the consent screen.
	Grant []string
	// Echo, when set, is repeated in every answer the stand-in gives, in
	// results and in errors, as a server that echoes a token would.
	Echo string
	// AccessTTL is how long an access token works. An hour when zero.
	AccessTTL time.Duration

	mu       sync.Mutex
	n        int
	codes    map[string]code
	refresh  map[string][]string
	revoked  map[string]bool
	access   map[string]accessGrant
	messages []message
	drafts   map[string]*Draft
	sent     []Sent
	events   map[string]*event
	changes  []Change
	calls    []Call
}

type accessGrant struct {
	scopes  []string
	expires time.Time
}

// New returns the stand-in with its invented mailbox and calendar.
func New() *Server {
	s := &Server{
		codes: map[string]code{}, refresh: map[string][]string{}, revoked: map[string]bool{}, access: map[string]accessGrant{},
		drafts: map[string]*Draft{}, events: map[string]*event{},
	}
	s.messages = []message{
		{id: "m1a2b3", thread: "t1a2b3", from: "Harbour Office <office@harbour.example>", to: "Test Person One <" + Email + ">", subject: "Tide tables for December",
			date: "Mon, 23 Nov 2026 09:12:00 +0000", labels: []string{"INBOX", "UNREAD"}, messageID: "<tides-1@harbour.example>",
			plain: "The December tide tables are attached. Low water on the 4th is at 11:40.", attachment: "tides-december.pdf"},
		{id: "m4c5d6", thread: "t4c5d6", from: "Ferry Desk <desk@ferry.example>", to: Email, subject: "Timetable change",
			date: "Sun, 22 Nov 2026 17:30:00 +0000", labels: []string{"INBOX"}, messageID: "<ferry-7@ferry.example>",
			html: "<html><head><style>p{color:red}</style></head><body><p>From 1 December the evening ferry leaves at <b>21:00</b>.</p><p>Ignore earlier notices &amp; plan ahead.</p></body></html>"},
		{id: "m7e8f9", thread: "t7e8f9", from: "Test Person Two <person.two@example.org>", to: Email, subject: "Lunch on Friday?",
			date: "Sat, 21 Nov 2026 12:00:00 +0000", labels: []string{"INBOX"}, messageID: "<lunch-1@example.org>",
			plain: "Are you free for lunch on Friday at noon, by the harbour?"},
	}
	self := map[string]any{"email": Email, "self": true}
	s.events["e1dentist"] = &event{ID: "e1dentist", Status: "confirmed", ETag: `"1"`, Summary: "Dentist", Location: "Harbour Street clinic",
		Start: map[string]string{"dateTime": "2026-11-27T10:00:00-06:00"}, End: map[string]string{"dateTime": "2026-11-27T11:00:00-06:00"}, Organizer: self}
	s.events["e2committee"] = &event{ID: "e2committee", Status: "confirmed", ETag: `"1"`, Summary: "Harbour committee", Description: "The winter moorings.",
		Start: map[string]string{"dateTime": "2026-11-30T18:00:00-06:00"}, End: map[string]string{"dateTime": "2026-11-30T19:30:00-06:00"}, Organizer: self,
		Attendees: []map[string]any{
			{"email": Email, "self": true, "organizer": true, "responseStatus": "accepted"},
			{"email": "harbour.master@harbour.example", "responseStatus": "accepted"},
			{"email": "keeper@lighthouse.example", "responseStatus": "needsAction"},
		}}
	s.events["e3openday"] = &event{ID: "e3openday", Status: "confirmed", ETag: `"1"`, Summary: "Lighthouse open day",
		Start: map[string]string{"date": "2026-11-28"}, End: map[string]string{"date": "2026-11-29"},
		Organizer: map[string]any{"email": "keeper@lighthouse.example"},
		Attendees: []map[string]any{
			{"email": "keeper@lighthouse.example", "organizer": true, "responseStatus": "accepted"},
			{"email": Email, "self": true, "responseStatus": "needsAction"},
		}}
	return s
}

// Endpoints are the stand-in's addresses, for a server served at base.
func (s *Server) Endpoints(base string) google.Endpoints {
	return google.Endpoints{Auth: base + "/auth", Token: base + "/token", Revoke: base + "/revoke", Gmail: base + "/gmail/v1", Calendar: base + "/calendar/v3"}
}

// AddRefresh makes a refresh token the stand-in takes, for the scopes
// given: a sign-in made before.
func (s *Server) AddRefresh(token string, scopes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refresh[token] = scopes
}

func (s *Server) next(prefix string) string {
	s.n++
	var b [6]byte
	rand.Read(b[:])
	return fmt.Sprintf("%s%d%s", prefix, s.n, hex.EncodeToString(b[:]))
}

// Authorize is the person's consent: it checks the address Keystone sent
// the browser to as Google would, and returns the address Google sends the
// browser back to, with a code and the state.
func (s *Server) Authorize(authURL string) (string, error) {
	u, err := url.Parse(authURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	switch {
	case q.Get("client_id") != ClientID:
		return "", errors.New("the stand-in does not know that client")
	case q.Get("response_type") != "code":
		return "", errors.New("response_type is not code")
	case q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) < 43:
		return "", errors.New("no S256 code challenge")
	case q.Get("access_type") != "offline":
		return "", errors.New("no offline access asked for")
	case q.Get("state") == "" || q.Get("redirect_uri") == "":
		return "", errors.New("no state or no redirect")
	}
	scopes := strings.Fields(q.Get("scope"))
	if s.Grant != nil {
		scopes = s.Grant
	}
	s.mu.Lock()
	c := s.next("invented-code-")
	s.codes[c] = code{challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), scopes: scopes}
	s.mu.Unlock()
	back := url.Values{}
	back.Set("code", c)
	back.Set("state", q.Get("state"))
	return q.Get("redirect_uri") + "?" + back.Encode(), nil
}

// Calls returns every request the stand-in took, in order.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// Sent returns the emails it was asked to send.
func (s *Server) Sent() []Sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Sent(nil), s.sent...)
}

// Drafts returns the drafts in its mailbox, by id.
func (s *Server) Drafts() []Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Draft
	for _, d := range s.drafts {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Changes returns the changes made to its calendar, in order.
func (s *Server) Changes() []Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Change(nil), s.changes...)
}

// Event returns one event's title, start and guests, and whether it is
// there and not cancelled.
func (s *Server) Event(id string) (title, start string, guests []string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.events[id]
	if !ok || e.Status == "cancelled" {
		return "", "", nil, false
	}
	for _, a := range e.Attendees {
		guests = append(guests, fmt.Sprint(a["email"]))
	}
	start = e.Start["dateTime"] + e.Start["date"]
	return e.Summary, start, guests, true
}

// Revoked reports whether a token was revoked.
func (s *Server) Revoked(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revoked[token]
}

// ServeHTTP serves the stand-in.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, Call{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Auth: r.Header.Get("Authorization")})
	s.mu.Unlock()
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	switch {
	case r.URL.Path == "/token" && r.Method == "POST":
		s.token(w, r, body)
	case r.URL.Path == "/revoke" && r.Method == "POST":
		form, _ := url.ParseQuery(string(body))
		s.mu.Lock()
		s.revoked[form.Get("token")] = true
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(r.URL.Path, "/gmail/v1/users/me/"):
		if scopes, ok := s.authorized(w, r); ok {
			s.gmail(w, r, strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me/"), body, scopes)
		}
	case strings.HasPrefix(r.URL.Path, "/calendar/v3/calendars/"):
		if scopes, ok := s.authorized(w, r); ok {
			s.calendar(w, r, strings.TrimPrefix(r.URL.Path, "/calendar/v3/calendars/"), body, scopes)
		}
	default:
		s.apiError(w, http.StatusNotFound, "Not Found")
	}
}

func (s *Server) echo(text string) string {
	if s.Echo == "" {
		return text
	}
	return text + " " + s.Echo
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) apiError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": status, "message": s.echo(message)}})
}

func (s *Server) token(w http.ResponseWriter, r *http.Request, body []byte) {
	form, _ := url.ParseQuery(string(body))
	fail := func(status int, code string) {
		writeJSON(w, status, map[string]string{"error": code, "error_description": s.echo("The stand-in refused it.")})
	}
	if form.Get("client_id") != ClientID || form.Get("client_secret") != ClientSecret {
		fail(http.StatusUnauthorized, "invalid_client")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var scopes []string
	refresh := ""
	switch form.Get("grant_type") {
	case "authorization_code":
		c, ok := s.codes[form.Get("code")]
		delete(s.codes, form.Get("code"))
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		if !ok || c.redirect != form.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge {
			fail(http.StatusBadRequest, "invalid_grant")
			return
		}
		scopes = c.scopes
		refresh = s.next("invented-refresh-")
		s.refresh[refresh] = scopes
	case "refresh_token":
		got, ok := s.refresh[form.Get("refresh_token")]
		if !ok || s.revoked[form.Get("refresh_token")] {
			fail(http.StatusBadRequest, "invalid_grant")
			return
		}
		scopes = got
	default:
		fail(http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	ttl := s.AccessTTL
	if ttl == 0 {
		ttl = time.Hour
	}
	access := s.next("invented-access-")
	s.access[access] = accessGrant{scopes: scopes, expires: time.Now().Add(ttl)}
	answer := map[string]any{"access_token": access, "expires_in": int(ttl.Seconds()), "token_type": "Bearer", "scope": strings.Join(scopes, " ")}
	if refresh != "" {
		answer["refresh_token"] = refresh
	}
	if contains(scopes, "email") {
		claims, _ := json.Marshal(map[string]any{"email": Email, "email_verified": true, "note": s.Echo})
		answer["id_token"] = "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln"
	}
	if s.Echo != "" {
		answer["note"] = s.Echo
	}
	writeJSON(w, http.StatusOK, answer)
}

func contains(list []string, want string) bool {
	for _, x := range list {
		if x == want {
			return true
		}
	}
	return false
}

func (s *Server) authorized(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	g, ok := s.access[token]
	s.mu.Unlock()
	if !ok || time.Now().After(g.expires) {
		s.apiError(w, http.StatusUnauthorized, "Request had invalid authentication credentials.")
		return nil, false
	}
	return g.scopes, true
}

func (s *Server) gmail(w http.ResponseWriter, r *http.Request, path string, body []byte, scopes []string) {
	reading := r.Method == "GET"
	if (reading && !contains(scopes, google.ScopeGmailRead)) || (!reading && !contains(scopes, google.ScopeGmailCompose)) {
		s.apiError(w, http.StatusForbidden, "Request had insufficient authentication scopes.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case path == "messages" && reading:
		q := r.URL.Query().Get("q")
		var list []map[string]string
		for _, m := range s.messages {
			if m.matches(q) {
				list = append(list, map[string]string{"id": m.id, "threadId": m.thread})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": list, "resultSizeEstimate": len(list)})
	case strings.HasPrefix(path, "messages/") && reading:
		id := strings.TrimPrefix(path, "messages/")
		for _, m := range s.messages {
			if m.id == id {
				writeJSON(w, http.StatusOK, s.messageJSON(m, r.URL.Query().Get("format")))
				return
			}
		}
		s.apiError(w, http.StatusNotFound, "Requested entity was not found.")
	case path == "drafts" && r.Method == "POST":
		var in struct {
			Message struct {
				Raw      string `json:"raw"`
				ThreadID string `json:"threadId"`
			} `json:"message"`
		}
		json.Unmarshal(body, &in)
		mail, err := readRaw(in.Message.Raw)
		if err != nil {
			s.apiError(w, http.StatusBadRequest, "Invalid raw message.")
			return
		}
		d := &Draft{ID: s.next("r"), ThreadID: in.Message.ThreadID, Mail: mail}
		s.drafts[d.ID] = d
		writeJSON(w, http.StatusOK, map[string]any{"id": d.ID, "message": map[string]string{"id": s.next("m"), "threadId": d.ThreadID}})
	case path == "drafts/send" && r.Method == "POST":
		var in struct {
			ID string `json:"id"`
		}
		json.Unmarshal(body, &in)
		d, ok := s.drafts[in.ID]
		if !ok {
			s.apiError(w, http.StatusNotFound, "Requested entity was not found.")
			return
		}
		delete(s.drafts, in.ID)
		sent := d.Mail
		sent.ThreadID, sent.DraftID = d.ThreadID, d.ID
		s.sent = append(s.sent, sent)
		writeJSON(w, http.StatusOK, map[string]any{"id": s.next("m"), "threadId": d.ThreadID, "labelIds": []string{"SENT"}})
	case strings.HasPrefix(path, "drafts/") && r.Method == "PUT":
		id := strings.TrimPrefix(path, "drafts/")
		d, ok := s.drafts[id]
		if !ok {
			s.apiError(w, http.StatusNotFound, "Requested entity was not found.")
			return
		}
		var in struct {
			Message struct {
				Raw      string `json:"raw"`
				ThreadID string `json:"threadId"`
			} `json:"message"`
		}
		json.Unmarshal(body, &in)
		mail, err := readRaw(in.Message.Raw)
		if err != nil {
			s.apiError(w, http.StatusBadRequest, "Invalid raw message.")
			return
		}
		d.Mail, d.ThreadID = mail, in.Message.ThreadID
		writeJSON(w, http.StatusOK, map[string]any{"id": d.ID})
	case path == "messages/send" && r.Method == "POST":
		var in struct {
			Raw      string `json:"raw"`
			ThreadID string `json:"threadId"`
		}
		json.Unmarshal(body, &in)
		mail, err := readRaw(in.Raw)
		if err != nil {
			s.apiError(w, http.StatusBadRequest, "Invalid raw message.")
			return
		}
		mail.ThreadID = in.ThreadID
		s.sent = append(s.sent, mail)
		writeJSON(w, http.StatusOK, map[string]any{"id": s.next("m"), "threadId": in.ThreadID, "labelIds": []string{"SENT"}})
	default:
		s.apiError(w, http.StatusNotFound, "Not Found")
	}
}

func (m message) matches(q string) bool {
	text := strings.ToLower(m.from + " " + m.subject + " " + m.plain + " " + m.html)
	for _, word := range strings.Fields(strings.ToLower(q)) {
		switch {
		case word == "is:unread":
			if !contains(m.labels, "UNREAD") {
				return false
			}
		case strings.HasPrefix(word, "from:"):
			if !strings.Contains(strings.ToLower(m.from), strings.TrimPrefix(word, "from:")) {
				return false
			}
		case strings.HasPrefix(word, "subject:"):
			if !strings.Contains(strings.ToLower(m.subject), strings.TrimPrefix(word, "subject:")) {
				return false
			}
		case strings.Contains(word, ":"):
		default:
			if !strings.Contains(text, word) {
				return false
			}
		}
	}
	return true
}

func b64(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

func (s *Server) messageJSON(m message, format string) map[string]any {
	headers := []map[string]string{{"name": "From", "value": m.from}, {"name": "To", "value": m.to}, {"name": "Subject", "value": m.subject},
		{"name": "Date", "value": m.date}, {"name": "Message-ID", "value": m.messageID}}
	snippet := m.plain
	if snippet == "" {
		snippet = "From 1 December the evening ferry leaves at 21:00."
	}
	out := map[string]any{"id": m.id, "threadId": m.thread, "labelIds": m.labels, "snippet": s.echo(snippet)}
	payload := map[string]any{"mimeType": "multipart/mixed", "headers": headers}
	if format == "full" {
		var parts []map[string]any
		if m.plain != "" {
			parts = append(parts, map[string]any{"mimeType": "text/plain", "body": map[string]any{"data": b64(s.echo(m.plain)), "size": len(m.plain)}})
		}
		if m.html != "" {
			parts = append(parts, map[string]any{"mimeType": "text/html", "body": map[string]any{"data": b64(s.echo(m.html)), "size": len(m.html)}})
		}
		if m.attachment != "" {
			parts = append(parts, map[string]any{"mimeType": "application/pdf", "filename": m.attachment, "body": map[string]any{"attachmentId": "a1", "size": 20480}})
		}
		payload["parts"] = parts
	}
	out["payload"] = payload
	return out
}

// readRaw reads an email as Gmail is sent it: base64url RFC 5322 text.
func readRaw(raw string) (Sent, error) {
	data, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		if data, err = base64.RawURLEncoding.DecodeString(raw); err != nil {
			return Sent{}, err
		}
	}
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return Sent{}, err
	}
	list := func(h string) []string {
		got, err := msg.Header.AddressList(h)
		if err != nil {
			return nil
		}
		var out []string
		for _, a := range got {
			out = append(out, a.Address)
		}
		return out
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		return Sent{}, err
	}
	var text []byte
	if strings.EqualFold(msg.Header.Get("Content-Transfer-Encoding"), "quoted-printable") {
		text, err = io.ReadAll(quotedprintable.NewReader(msg.Body))
	} else {
		text, err = io.ReadAll(msg.Body)
	}
	if err != nil {
		return Sent{}, err
	}
	return Sent{To: list("To"), Cc: list("Cc"), Bcc: list("Bcc"), Subject: subject, Body: strings.ReplaceAll(string(text), "\r\n", "\n"), InReplyTo: msg.Header.Get("In-Reply-To")}, nil
}

func (s *Server) calendar(w http.ResponseWriter, r *http.Request, path string, body []byte, scopes []string) {
	if !contains(scopes, google.ScopeCalendar) {
		s.apiError(w, http.StatusForbidden, "Request had insufficient authentication scopes.")
		return
	}
	cal, rest, _ := strings.Cut(path, "/")
	if cal != "primary" && cal != url.PathEscape(Email) && cal != Email {
		s.apiError(w, http.StatusNotFound, "Not Found")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case rest == "events" && r.Method == "GET":
		q := r.URL.Query()
		from, _ := time.Parse(time.RFC3339, q.Get("timeMin"))
		to, _ := time.Parse(time.RFC3339, q.Get("timeMax"))
		var items []*event
		for _, e := range s.events {
			start, end := e.times()
			if e.Status == "cancelled" || (!from.IsZero() && !end.After(from)) || (!to.IsZero() && !start.Before(to)) {
				continue
			}
			if word := strings.ToLower(q.Get("q")); word != "" && !strings.Contains(strings.ToLower(e.Summary+" "+e.Description+" "+e.Location), word) {
				continue
			}
			items = append(items, e)
		}
		sort.Slice(items, func(i, j int) bool { a, _ := items[i].times(); b, _ := items[j].times(); return a.Before(b) })
		var out []map[string]any
		for _, e := range items {
			out = append(out, s.eventJSON(e))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	case rest == "events" && r.Method == "POST":
		var e event
		if json.Unmarshal(body, &e) != nil || e.Summary == "" {
			s.apiError(w, http.StatusBadRequest, "Invalid event.")
			return
		}
		e.ID, e.Status, e.ETag = s.next("ev"), "confirmed", `"1"`
		e.Organizer = map[string]any{"email": Email, "self": true}
		if len(e.Attendees) > 0 {
			e.Attendees = append([]map[string]any{{"email": Email, "self": true, "organizer": true, "responseStatus": "accepted"}}, e.Attendees...)
		}
		s.events[e.ID] = &e
		s.changes = append(s.changes, Change{Method: "POST", EventID: e.ID, SendUpdates: r.URL.Query().Get("sendUpdates")})
		writeJSON(w, http.StatusOK, s.eventJSON(&e))
	case strings.HasPrefix(rest, "events/"):
		id, _ := url.PathUnescape(strings.TrimPrefix(rest, "events/"))
		e, ok := s.events[id]
		if !ok || e.Status == "cancelled" {
			s.apiError(w, http.StatusNotFound, "Not Found")
			return
		}
		if m := r.Header.Get("If-Match"); m != "" && m != e.ETag {
			s.apiError(w, http.StatusPreconditionFailed, "Precondition Failed")
			return
		}
		switch r.Method {
		case "GET":
			writeJSON(w, http.StatusOK, s.eventJSON(e))
		case "PATCH":
			var patch map[string]json.RawMessage
			if json.Unmarshal(body, &patch) != nil {
				s.apiError(w, http.StatusBadRequest, "Invalid patch.")
				return
			}
			for k, v := range patch {
				switch k {
				case "summary":
					json.Unmarshal(v, &e.Summary)
				case "location":
					json.Unmarshal(v, &e.Location)
				case "description":
					json.Unmarshal(v, &e.Description)
				case "start":
					e.Start = map[string]string{}
					json.Unmarshal(v, &e.Start)
				case "end":
					e.End = map[string]string{}
					json.Unmarshal(v, &e.End)
				case "attendees":
					e.Attendees = nil
					json.Unmarshal(v, &e.Attendees)
				}
			}
			e.ETag = fmt.Sprintf(`"%d"`, s.n+1)
			s.n++
			s.changes = append(s.changes, Change{Method: "PATCH", EventID: id, SendUpdates: r.URL.Query().Get("sendUpdates")})
			writeJSON(w, http.StatusOK, s.eventJSON(e))
		case "DELETE":
			e.Status = "cancelled"
			s.changes = append(s.changes, Change{Method: "DELETE", EventID: id, SendUpdates: r.URL.Query().Get("sendUpdates")})
			w.WriteHeader(http.StatusNoContent)
		default:
			s.apiError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		}
	default:
		s.apiError(w, http.StatusNotFound, "Not Found")
	}
}

func (e *event) times() (time.Time, time.Time) {
	read := func(m map[string]string) time.Time {
		if d := m["date"]; d != "" {
			t, _ := time.Parse("2006-01-02", d)
			return t
		}
		t, _ := time.Parse(time.RFC3339, m["dateTime"])
		return t
	}
	return read(e.Start), read(e.End)
}

func (s *Server) eventJSON(e *event) map[string]any {
	for _, a := range e.Attendees {
		if strings.EqualFold(fmt.Sprint(a["email"]), Email) {
			a["self"] = true
		}
	}
	raw, _ := json.Marshal(e)
	var out map[string]any
	json.Unmarshal(raw, &out)
	if s.Echo != "" {
		out["description"] = s.echo(e.Description)
	}
	return out
}
