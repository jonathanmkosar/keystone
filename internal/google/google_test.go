package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/google"
	"github.com/jonathanmkosar/keystone/internal/google/googletest"
)

// The Google package's tests (P12-02), against the stand-in on this
// machine. Every name, address, message and event is invented.

var client = google.Client{ID: googletest.ClientID, Secret: googletest.ClientSecret}

func standIn(t *testing.T) (*googletest.Server, google.Endpoints, *httptest.Server) {
	t.Helper()
	g := googletest.New()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return g, g.Endpoints(srv.URL), srv
}

// signIn runs the whole sign-in against the stand-in and returns the grant.
func signIn(t *testing.T, g *googletest.Server, e google.Endpoints, srv *httptest.Server, services ...google.Service) google.Grant {
	t.Helper()
	verifier, err := google.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	state, _ := google.NewSecret()
	auth := google.AuthURL(e, client.ID, "https://keystone.example/settings/connectors/google", state, google.Challenge(verifier), google.ScopesFor(services))
	back, err := g.Authorize(auth)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(back)
	if u.Query().Get("state") != state {
		t.Fatalf("the state came back as %q", u.Query().Get("state"))
	}
	grant, err := google.Exchange(context.Background(), srv.Client(), e, client, u.Query().Get("code"), verifier, "https://keystone.example/settings/connectors/google", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func api(srv *httptest.Server, e google.Endpoints, access string) google.API {
	return google.API{
		HTTP: srv.Client(), Endpoints: e, MaxBytes: 1 << 20, ListMax: 25,
		Access: func(context.Context) (string, error) { return access, nil },
		Now:    func() time.Time { return time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC) },
	}
}

func run(t *testing.T, a google.API, s google.Service, name string, input string) (string, error) {
	t.Helper()
	d, ok := google.Find(s, name)
	if !ok {
		t.Fatalf("%s has no tool %s", s, name)
	}
	return d.Run(context.Background(), a, json.RawMessage(input))
}

func TestTheSignInUsesPKCEAndAsksForTheNarrowestScopes(t *testing.T) {
	g, e, srv := standIn(t)
	verifier, _ := google.NewSecret()
	auth := google.AuthURL(e, client.ID, "https://keystone.example/back", "a-state", google.Challenge(verifier), google.ScopesFor(google.Services()))
	u, err := url.Parse(auth)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	want := "openid email " + google.ScopeGmailRead + " " + google.ScopeGmailCompose + " " + google.ScopeCalendar
	if q.Get("scope") != want || q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" || q.Get("redirect_uri") != "https://keystone.example/back" {
		t.Errorf("the sign-in address asks %v", q)
	}
	if strings.Contains(auth, verifier) || strings.Contains(auth, client.Secret) {
		t.Error("the sign-in address holds the verifier or the secret")
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Errorf("the verifier has %d characters", len(verifier))
	}

	grant := signIn(t, g, e, srv, google.Gmail, google.Calendar)
	if grant.Access == "" || grant.Refresh == "" || grant.Email != googletest.Email || !google.Gmail.Granted(grant.Scopes) || !google.Calendar.Granted(grant.Scopes) || !grant.Expires.After(time.Now()) {
		t.Errorf("the grant is %+v", grant)
	}
	again, err := google.Refresh(context.Background(), srv.Client(), e, client, grant.Refresh, 1<<20)
	if err != nil || again.Access == "" || again.Access == grant.Access {
		t.Errorf("the refresh gave %+v, %v", again, err)
	}
	if err := google.Revoke(context.Background(), srv.Client(), e, grant.Refresh); err != nil || !g.Revoked(grant.Refresh) {
		t.Errorf("the revoke gave %v", err)
	}
	if _, err := google.Refresh(context.Background(), srv.Client(), e, client, grant.Refresh, 1<<20); !errors.Is(err, google.ErrSignInGone) {
		t.Errorf("a revoked sign-in refreshed: %v", err)
	}
}

func TestASignInThatDoesNotFitIsRefusedWithoutRepeatingIt(t *testing.T) {
	g, e, srv := standIn(t)
	g.Echo = "an echoed value"
	verifier, _ := google.NewSecret()
	back, err := g.Authorize(google.AuthURL(e, client.ID, "https://keystone.example/back", "s", google.Challenge(verifier), google.ScopesFor(google.Services())))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(back)
	code := u.Query().Get("code")
	other, _ := google.NewSecret()
	for what, try := range map[string]func() error{
		"another verifier": func() error {
			_, err := google.Exchange(context.Background(), srv.Client(), e, client, code, other, "https://keystone.example/back", 1<<20)
			return err
		},
		"another client secret": func() error {
			_, err := google.Exchange(context.Background(), srv.Client(), e, google.Client{ID: client.ID, Secret: "not the secret"}, code, verifier, "https://keystone.example/back", 1<<20)
			return err
		},
	} {
		err := try()
		if err == nil {
			t.Fatalf("%s was taken", what)
		}
		for _, held := range []string{code, verifier, other, client.Secret, "an echoed value"} {
			if strings.Contains(err.Error(), held) {
				t.Errorf("%s: the error repeats what was sent or what came back: %v", what, err)
			}
		}
	}
	// A code is used once, and the right verifier after a wrong one fails.
	if _, err := google.Exchange(context.Background(), srv.Client(), e, client, code, verifier, "https://keystone.example/back", 1<<20); err == nil {
		t.Error("a code was used twice")
	}
	if _, ok := google.ParseService("drive"); ok {
		t.Error("drive is a service")
	}
}

func TestGmailSearchesAndReads(t *testing.T) {
	g, e, srv := standIn(t)
	grant := signIn(t, g, e, srv, google.Gmail)
	a := api(srv, e, grant.Access)
	got, err := run(t, a, google.Gmail, "search", `{"query":"harbour"}`)
	if err != nil || !strings.Contains(got, "2 messages for “harbour”") || !strings.Contains(got, "Tide tables for December") || !strings.Contains(got, "Unread") || !strings.Contains(got, "id m1a2b3") {
		t.Errorf("the search gave %q, %v", got, err)
	}
	if got, err := run(t, a, google.Gmail, "search", `{"query":"no such words anywhere"}`); err != nil || !strings.HasPrefix(got, "No message matches") {
		t.Errorf("an empty search gave %q, %v", got, err)
	}
	got, err = run(t, a, google.Gmail, "read", `{"id":"m1a2b3"}`)
	if err != nil || !strings.Contains(got, "Low water on the 4th is at 11:40.") || !strings.Contains(got, "Attachments, not read: tides-december.pdf") || !strings.Contains(got, "From: Harbour Office") {
		t.Errorf("reading a plain message gave %q, %v", got, err)
	}
	got, err = run(t, a, google.Gmail, "read", `{"id":"m4c5d6"}`)
	if err != nil || !strings.Contains(got, "the evening ferry leaves at 21:00.") || strings.Contains(got, "<b>") || strings.Contains(got, "color:red") || !strings.Contains(got, "notices & plan") {
		t.Errorf("reading an HTML message gave %q, %v", got, err)
	}
	for _, bad := range []string{`{"id":"../../drafts"}`, `{"id":"m1a2b3","extra":1}`, `{"query":"a\nb"}`} {
		name := "read"
		if strings.Contains(bad, "query") {
			name = "search"
		}
		if _, err := run(t, a, google.Gmail, name, bad); err == nil {
			t.Errorf("%s took %s", name, bad)
		}
	}
	// A sign-in for the calendar alone cannot read mail.
	cal := signIn(t, g, e, srv, google.Calendar)
	if _, err := run(t, api(srv, e, cal.Access), google.Gmail, "search", `{}`); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("a calendar sign-in searched mail: %v", err)
	}
	if _, err := run(t, api(srv, e, "not a token"), google.Gmail, "search", `{}`); err == nil || !strings.Contains(err.Error(), "connect Google again") {
		t.Errorf("a bad token gave %v", err)
	}
}

func TestGmailDraftsAndSendsExactlyWhatTheCardShows(t *testing.T) {
	g, e, srv := standIn(t)
	grant := signIn(t, g, e, srv, google.Gmail)
	a := api(srv, e, grant.Access)
	draft := `{"to":["Test Person Two <person.two@example.org>"],"cc":["office@harbour.example"],"subject":"Re: Lunch on Friday?","body":"Yes, noon by the harbour works.\nSee you then.","reply_to":"m7e8f9"}`
	d, _ := google.Find(google.Gmail, "draft")
	card := d.Describe(json.RawMessage(draft))
	for _, want := range []string{"Save a draft to Test Person Two <person.two@example.org>.", "It is not sent.", "Copy to office@harbour.example.", "Subject: “Re: Lunch on Friday?”", "It says: “Yes, noon by the harbour works. See you then.”"} {
		if !strings.Contains(card, want) {
			t.Errorf("the draft's card %q lacks %q", card, want)
		}
	}
	if d.Reaches != nil {
		t.Error("a draft reaches other people")
	}
	got, err := run(t, a, google.Gmail, "draft", draft)
	if err != nil || !strings.Contains(got, "It has not been sent.") {
		t.Fatalf("the draft gave %q, %v", got, err)
	}
	drafts := g.Drafts()
	if len(drafts) != 1 || drafts[0].ThreadID != "t7e8f9" || drafts[0].Mail.Subject != "Re: Lunch on Friday?" || drafts[0].Mail.InReplyTo != "<lunch-1@example.org>" ||
		strings.Join(drafts[0].Mail.To, ",") != "person.two@example.org" || !strings.Contains(drafts[0].Mail.Body, "See you then.") || len(g.Sent()) != 0 {
		t.Fatalf("the stand-in has the drafts %+v and sent %+v", drafts, g.Sent())
	}

	send := `{"to":["person.two@example.org"],"subject":"Re: Lunch on Friday?","body":"Yes, noon by the harbour works. Café on the quay?","draft_id":"` + drafts[0].ID + `"}`
	s, _ := google.Find(google.Gmail, "send")
	if s.Reaches == nil || !s.Reaches(json.RawMessage(send)) || !s.Reaches(json.RawMessage(`not json`)) {
		t.Error("a send does not reach other people")
	}
	card = s.Describe(json.RawMessage(send))
	for _, want := range []string{"Send an email to person.two@example.org.", "Subject: “Re: Lunch on Friday?”", "It sends your draft " + drafts[0].ID, "Café on the quay?"} {
		if !strings.Contains(card, want) {
			t.Errorf("the send's card %q lacks %q", card, want)
		}
	}
	if got, err := run(t, a, google.Gmail, "send", send); err != nil || !strings.HasPrefix(got, "Sent the email to person.two@example.org") {
		t.Fatalf("the send gave %q, %v", got, err)
	}
	sent := g.Sent()
	if len(sent) != 1 || sent[0].DraftID != drafts[0].ID || sent[0].Body != "Yes, noon by the harbour works. Café on the quay?" || len(g.Drafts()) != 0 || len(sent[0].Cc) != 0 {
		t.Errorf("the stand-in sent %+v and kept the drafts %+v", sent, g.Drafts())
	}
	// A plain send, with a subject that needs encoding.
	if _, err := run(t, a, google.Gmail, "send", `{"to":["keeper@lighthouse.example"],"bcc":["person.two@example.org"],"subject":"Lampe à huile","body":"Le phare."}`); err != nil {
		t.Fatal(err)
	}
	if sent = g.Sent(); len(sent) != 2 || sent[1].Subject != "Lampe à huile" || strings.Join(sent[1].Bcc, ",") != "person.two@example.org" {
		t.Errorf("the second send reached the stand-in as %+v", sent)
	}

	many := make([]string, 51)
	for i := range many {
		many[i] = "someone@example.com"
	}
	manyJSON, _ := json.Marshal(map[string]any{"to": many, "subject": "Hello", "body": "Hi"})
	for what, input := range map[string]string{
		"a subject of two lines":    `{"to":["a@example.com"],"subject":"Hi\r\nBcc: other@example.com","body":"x"}`,
		"an address that is not":    `{"to":["not an address"],"subject":"Hi","body":"x"}`,
		"two addresses in one":      `{"to":["a@example.com, b@example.com"],"subject":"Hi","body":"x"}`,
		"no recipient":              `{"to":[],"subject":"Hi","body":"x"}`,
		"51 recipients":             string(manyJSON),
		"a field it does not know":  `{"to":["a@example.com"],"subject":"Hi","body":"x","from":"b@example.com"}`,
		"a draft id that is a path": `{"to":["a@example.com"],"subject":"Hi","body":"x","draft_id":"../x"}`,
	} {
		if _, err := run(t, a, google.Gmail, "send", input); err == nil {
			t.Errorf("a send with %s was taken", what)
		}
	}
	if len(g.Sent()) != 2 {
		t.Errorf("a refused send reached the stand-in: %d sent", len(g.Sent()))
	}
}

func TestCalendarListsAndReads(t *testing.T) {
	g, e, srv := standIn(t)
	grant := signIn(t, g, e, srv, google.Calendar)
	a := api(srv, e, grant.Access)
	got, err := run(t, a, google.Calendar, "list_events", `{}`)
	if err != nil || !strings.Contains(got, "3 events on your calendar") || !strings.Contains(got, "“Dentist” · Fri 27 Nov 2026, 10:00–11:00 (UTC−06:00) · id e1dentist") ||
		!strings.Contains(got, "Sat 28 Nov 2026, all day") || !strings.Contains(got, "A change reaches: harbour.master@harbour.example, keeper@lighthouse.example") {
		t.Errorf("the list gave %q, %v", got, err)
	}
	got, err = run(t, a, google.Calendar, "get_event", `{"event_id":"e3openday"}`)
	if err != nil || !strings.Contains(got, "Organizer: keeper@lighthouse.example") || !strings.Contains(got, "A change reaches: keeper@lighthouse.example") {
		t.Errorf("reading an event someone else organises gave %q, %v", got, err)
	}
	if got, err := run(t, a, google.Calendar, "list_events", `{"from":"2026-12-02","to":"2026-12-08"}`); err != nil || !strings.HasPrefix(got, "No events") {
		t.Errorf("an empty window gave %q, %v", got, err)
	}
}

func TestCalendarChangesAreCheckedAgainstTheEvent(t *testing.T) {
	g, e, srv := standIn(t)
	grant := signIn(t, g, e, srv, google.Calendar)
	a := api(srv, e, grant.Access)

	// A personal event: nobody else is told.
	create, _ := google.Find(google.Calendar, "create_event")
	alone := `{"title":"Swim","start":"2026-11-26T07:00","end":"2026-11-26T08:00","time_zone":"America/Chicago"}`
	if create.Reaches(json.RawMessage(alone)) {
		t.Error("an event with no guests reaches other people")
	}
	if card := create.Describe(json.RawMessage(alone)); card != "Add “Swim” to your calendar: Thu 26 Nov 2026, 07:00–08:00 (America/Chicago).\nNobody else is invited." {
		t.Errorf("the card is %q", card)
	}
	if got, err := run(t, a, google.Calendar, "create_event", alone); err != nil || !strings.HasPrefix(got, "Added “Swim”") || strings.Contains(got, "invitation") {
		t.Fatalf("the personal event gave %q, %v", got, err)
	}
	invite := `{"title":"Tea","start":"2026-11-28","end":"2026-11-28","guests":["person.two@example.org"]}`
	if !create.Reaches(json.RawMessage(invite)) || !strings.Contains(create.Describe(json.RawMessage(invite)), "Google sends an invitation to person.two@example.org.") {
		t.Error("an event with a guest does not say it invites them")
	}
	if got, err := run(t, a, google.Calendar, "create_event", invite); err != nil || !strings.Contains(got, "Sat 28 Nov 2026, all day") || !strings.Contains(got, "sent an invitation to person.two@example.org") {
		t.Fatalf("the event with a guest gave %q, %v", got, err)
	}
	changes := g.Changes()
	if len(changes) != 2 || changes[0].SendUpdates != "none" || changes[1].SendUpdates != "all" {
		t.Errorf("the stand-in's changes are %+v", changes)
	}

	// The dentist has no guests: a move tells nobody.
	update, _ := google.Find(google.Calendar, "update_event")
	move := `{"event_id":"e1dentist","title":"Dentist","start":"2026-11-27T11:00:00-06:00","end":"2026-11-27T12:00:00-06:00"}`
	if update.Reaches(json.RawMessage(move)) {
		t.Error("moving a personal event reaches other people")
	}
	if got, err := run(t, a, google.Calendar, "update_event", move); err != nil || !strings.Contains(got, "moved it to Fri 27 Nov 2026, 11:00–12:00") {
		t.Fatalf("the move gave %q, %v", got, err)
	}
	if _, start, _, _ := g.Event("e1dentist"); start != "2026-11-27T11:00:00-06:00" {
		t.Errorf("the dentist starts at %s", start)
	}

	// The committee has guests: a call that leaves them out, names them
	// wrongly or gets the title wrong changes nothing.
	before := len(g.Changes())
	for what, input := range map[string]string{
		"no guests":      `{"event_id":"e2committee","title":"Harbour committee","new_title":"Harbour board"}`,
		"one guest":      `{"event_id":"e2committee","title":"Harbour committee","guests":["harbour.master@harbour.example"],"new_title":"Harbour board"}`,
		"the title":      `{"event_id":"e2committee","title":"Dentist","guests":["harbour.master@harbour.example","keeper@lighthouse.example"],"new_title":"Harbour board"}`,
		"a guest to cut": `{"event_id":"e2committee","title":"Harbour committee","guests":["harbour.master@harbour.example","keeper@lighthouse.example"],"remove_guests":["stranger@example.com"]}`,
	} {
		_, err := run(t, a, google.Calendar, "update_event", input)
		if err == nil || !strings.HasPrefix(err.Error(), "nothing was changed") {
			t.Errorf("an update with the wrong %s gave %v", what, err)
		}
		if what == "no guests" && !strings.Contains(err.Error(), `["harbour.master@harbour.example","keeper@lighthouse.example"]`) {
			t.Errorf("the refusal does not give the guests to name: %v", err)
		}
	}
	if len(g.Changes()) != before {
		t.Fatal("a refused change reached the stand-in")
	}
	rename := `{"event_id":"e2committee","title":"harbour committee","guests":["Keeper@Lighthouse.example","harbour.master@harbour.example"],"new_title":"Harbour board","add_guests":["person.two@example.org"]}`
	if !update.Reaches(json.RawMessage(rename)) {
		t.Error("changing an event with guests does not reach them")
	}
	card := update.Describe(json.RawMessage(rename))
	if !strings.Contains(card, "- rename it “Harbour board”") || !strings.Contains(card, "- invite person.two@example.org") || !strings.Contains(card, "Google tells Keeper@Lighthouse.example, harbour.master@harbour.example, person.two@example.org.") {
		t.Errorf("the card is %q", card)
	}
	if got, err := run(t, a, google.Calendar, "update_event", rename); err != nil || !strings.Contains(got, "Google told harbour.master@harbour.example, keeper@lighthouse.example, person.two@example.org.") {
		t.Fatalf("the change to the committee gave %q, %v", got, err)
	}
	if title, _, guests, _ := g.Event("e2committee"); title != "Harbour board" || len(guests) != 4 {
		t.Errorf("the committee is now %q with %v", title, guests)
	}
	if c := g.Changes(); c[len(c)-1].SendUpdates != "all" {
		t.Errorf("the change to an event with guests was sent with %+v", c[len(c)-1])
	}

	// Deleting an event someone else organises reaches the organizer.
	del, _ := google.Find(google.Calendar, "delete_event")
	if _, err := run(t, a, google.Calendar, "delete_event", `{"event_id":"e3openday","title":"Lighthouse open day"}`); err == nil || !strings.Contains(err.Error(), "keeper@lighthouse.example") {
		t.Errorf("deleting the open day without its organizer gave %v", err)
	}
	gone := `{"event_id":"e3openday","title":"Lighthouse open day","guests":["keeper@lighthouse.example"]}`
	if !del.Reaches(json.RawMessage(gone)) || !strings.Contains(del.Describe(json.RawMessage(gone)), "Google tells keeper@lighthouse.example.") {
		t.Error("the delete's card does not name who is told")
	}
	if got, err := run(t, a, google.Calendar, "delete_event", gone); err != nil || !strings.Contains(got, "Deleted “Lighthouse open day”") {
		t.Fatalf("the delete gave %q, %v", got, err)
	}
	if _, _, _, ok := g.Event("e3openday"); ok {
		t.Error("the open day is still there")
	}
	for what, input := range map[string]string{
		"an end before the start":   `{"title":"x","start":"2026-11-26T08:00:00Z","end":"2026-11-26T07:00:00Z"}`,
		"a time with no zone":       `{"title":"x","start":"2026-11-26T08:00","end":"2026-11-26T09:00"}`,
		"a date and a time":         `{"title":"x","start":"2026-11-26","end":"2026-11-26T09:00:00Z"}`,
		"a title of two lines":      `{"title":"x\ny","start":"2026-11-26","end":"2026-11-27"}`,
		"a calendar that is a path": `{"title":"x","start":"2026-11-26","end":"2026-11-27","calendar_id":"../../users"}`,
	} {
		if _, err := run(t, a, google.Calendar, "create_event", input); err == nil {
			t.Errorf("an event with %s was added", what)
		}
	}
}
