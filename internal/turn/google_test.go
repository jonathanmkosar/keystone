package turn

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/connectors"
	"github.com/jonathanmkosar/keystone/internal/google"
	"github.com/jonathanmkosar/keystone/internal/google/googletest"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/settings"
)

// The Gmail and Calendar connectors' proof (P12-02), on the real routes
// with the fake provider and the stand-in Google on this machine. Every
// name, address, message and event is invented.

const googleRefresh = "invented-refresh-for-a-turn-test"

const googleScripts = `{"test_data":true,"scripts":[
	{"name":"draft and send","when":{"user_text":"Draft the lunch reply, then send it."},"steps":[
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_draft","name":"gmail_draft","input":{"to":["person.two@example.org"],"subject":"Re: Lunch on Friday?","body":"Yes, noon by the harbour works."}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_send","name":"gmail_send","input":{"to":["person.two@example.org"],"subject":"Re: Lunch on Friday?","body":"Yes, noon by the harbour works.","reply_to":"m7e8f9"}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"Drafted and sent."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
	{"name":"send","when":{"user_text":"Send the keeper a note about the lamp."},"steps":[
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_send","name":"gmail_send","input":{"to":["keeper@lighthouse.example"],"subject":"The lamp","body":"The lamp was checked tonight."}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"Done, or not: the step says."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
	{"name":"swim","when":{"user_text":"Put a swim on my calendar."},"steps":[
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_swim","name":"calendar_create_event","input":{"title":"Swim","start":"2026-11-26T07:00:00-06:00","end":"2026-11-26T08:00:00-06:00"}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"On your calendar."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
	{"name":"tea","when":{"user_text":"Invite the keeper to tea."},"steps":[
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_tea","name":"calendar_create_event","input":{"title":"Tea","start":"2026-11-28T16:00:00-06:00","end":"2026-11-28T17:00:00-06:00","guests":["keeper@lighthouse.example"]}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"Invited."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
	{"name":"rename","when":{"user_text":"Rename the committee."},"steps":[
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"tool_call","id":"call_rename","name":"calendar_update_event","input":{"event_id":"e2committee","title":"Harbour committee","new_title":"Harbour board"}}]},"stop_reason":"tool_call","usage":{"input_tokens":10,"output_tokens":5}}},
		{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"It has guests, so I will ask."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]},
	{"name":"anything else","when":{},"steps":[{"reply":{"message":{"role":"assistant","blocks":[{"type":"text","text":"A plain answer."}]},"stop_reason":"end","usage":{"input_tokens":10,"output_tokens":5}}}]}
]}`

// serveGoogle starts a server with the person's connectors through a pool
// whose Google is the stand-in, and gives the test tenant a Google sign-in
// with both connectors.
func (g *rig) serveGoogle(stand *googletest.Server, provider seam.Provider) *served {
	g.t.Helper()
	srv := httptest.NewServer(stand)
	g.t.Cleanup(srv.Close)
	cfg, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 50 * time.Millisecond
	reg, err := settings.Registry()
	if err != nil {
		g.t.Fatal(err)
	}
	o, err := connectors.ReadOptions(func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	})
	if err != nil {
		g.t.Fatal(err)
	}
	o.Google.Endpoints, o.Google.OnThisMachine = stand.Endpoints(srv.URL), true
	stand.AddRefresh(googleRefresh, google.ScopesFor(google.Services()))
	g.with(g.test.ID, func(s *tenant.Store) {
		if err := tenantsettings.Set(context.Background(), s, reg, tenantsettings.Admin, connectors.KeyGoogleClientID, googletest.ClientID); err != nil {
			g.t.Fatal(err)
		}
		for name, value := range map[string]string{connectors.GoogleClientSecret: googletest.ClientSecret, connectors.GoogleSecret: googleRefresh} {
			if err := s.Secrets().Set(name, value); err != nil {
				g.t.Fatal(err)
			}
		}
		for _, svc := range google.Services() {
			if _, err := connectors.Add(context.Background(), s, connectors.Connector{Name: string(svc), Label: svc.Label(), Transport: connectors.TransportGoogle, Secret: connectors.GoogleSecret, Enabled: true, Outside: true}, connectors.ByPerson, 0); err != nil {
				g.t.Fatal(err)
			}
			if k, err := o.Check(context.Background(), s, string(svc)); err != nil || k.Problem != "" {
				g.t.Fatalf("the %s connector checks as %+v, %v", svc, k, err)
			}
		}
	})
	set, err := adapters.Fake(g.fx.Scripts, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	runner := NewRunner(Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Adapters: set, Tools: tools.New(), Connectors: connectors.NewPool(o, g.t.Logf)})
	runner.Recover()
	routes := append(Routes(runner), ApprovalRoutes(runner)...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, routes)
	if err != nil {
		g.t.Fatal(err)
	}
	sv := &served{g: g, runner: runner, server: httptest.NewServer(h), tokens: map[string]string{}}
	g.t.Cleanup(sv.stop)
	g.with(g.test.ID, func(s *tenant.Store) {
		token, _, err := signin.SignIn(s, g.fx.Password, g.signIn)
		if err != nil {
			g.t.Fatal(err)
		}
		sv.tokens[g.test.ID] = token
	})
	return sv
}

// noGoogleTokens fails when a token or the client's secret is in a turn's
// events or anywhere in the test tenant's audit log.
func (g *rig) noGoogleTokens(events []event) {
	g.t.Helper()
	var all []string
	for _, e := range events {
		all = append(all, e.data)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{})
		if err != nil {
			g.t.Fatal(err)
		}
		for _, e := range entries {
			all = append(all, e.Actor, e.Action, e.Sent, e.Received)
		}
	})
	for _, text := range all {
		for _, held := range []string{googleRefresh, googletest.ClientSecret, "invented-access-"} {
			if strings.Contains(text, held) {
				g.t.Errorf("a token or the client's secret is in what a turn kept: %.200s", text)
			}
		}
	}
}

// The item's proof, first part: drafting and sending an email asks, and
// runs once approved. The card shows the recipients, the subject and the
// body; the draft asks by the person's policy and the send because it
// reaches other people.
func TestDraftingAndSendingAnEmailAsksAndRunsOnceApproved(t *testing.T) {
	g := newRig(t)
	stand := googletest.New()
	sv := g.serveGoogle(stand, scripted(t, googleScripts))
	id := sv.newChat(g.test.ID)
	var asked []Approval
	events, _, requestID := sv.sendAnswering(g.test.ID, id, `{"text":"Draft the lunch reply, then send it."}`, "approve", func(a Approval) {
		asked = append(asked, a)
		if len(stand.Sent()) != 0 {
			t.Errorf("an email was sent before the person answered %s", a.Tool)
		}
	})
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	if len(asked) != 2 || asked[0].Tool != "gmail_draft" || asked[0].Reason != "policy" || asked[1].Tool != "gmail_send" || asked[1].Reason != "others" {
		t.Fatalf("the calls that asked were %+v", asked)
	}
	for _, want := range []string{"Send an email to person.two@example.org.", "Subject: “Re: Lunch on Friday?”", "In reply to message m7e8f9.", "It says: “Yes, noon by the harbour works.”"} {
		if !strings.Contains(asked[1].Summary, want) {
			t.Errorf("the send's card %q lacks %q", asked[1].Summary, want)
		}
	}
	if !strings.Contains(asked[1].Why, "reaches other people") || !strings.Contains(asked[0].Summary, "Save a draft to person.two@example.org. It is not sent.") {
		t.Errorf("the cards say %q and %q", asked[0].Summary, asked[1].Why)
	}
	got := results(t, events)
	if r := got["call_draft"]; r.IsError || !strings.Contains(said(r), "It has not been sent.") || !tools.IsOutside(seam.Block{Type: seam.BlockToolResult, Content: r.Content}) {
		t.Errorf("the draft gave %+v", r)
	}
	if r := got["call_send"]; r.IsError || !strings.Contains(said(r), "Sent the email to person.two@example.org") {
		t.Errorf("the send gave %+v", r)
	}
	sent := stand.Sent()
	if len(stand.Drafts()) != 1 || len(sent) != 1 || sent[0].Subject != "Re: Lunch on Friday?" || sent[0].ThreadID != "t7e8f9" || sent[0].InReplyTo != "<lunch-1@example.org>" {
		t.Errorf("the stand-in has the drafts %+v and sent %+v", stand.Drafts(), sent)
	}
	var outcomes []string
	for _, e := range g.toolEntries(requestID) {
		outcomes = append(outcomes, e.Action+":"+string(e.Outcome))
	}
	if got, want := strings.Join(outcomes, " "), "gmail_draft:started gmail_draft:ok gmail_send:started gmail_send:ok"; got != want {
		t.Errorf("the calls are recorded as %s, want %s", got, want)
	}
	g.noGoogleTokens(events)
}

// A send set to always still asks, and declined it is not sent.
func TestASendSetToAlwaysStillAsks(t *testing.T) {
	g := newRig(t)
	stand := googletest.New()
	sv := g.serveGoogle(stand, scripted(t, googleScripts))
	sv.setPolicyFor(g.test.ID, "gmail_send", "always")
	var asked []Approval
	events, _, _ := sv.sendAnswering(g.test.ID, sv.newChat(g.test.ID), `{"text":"Send the keeper a note about the lamp."}`, "decline", func(a Approval) { asked = append(asked, a) })
	if len(asked) != 1 || asked[0].Tool != "gmail_send" || asked[0].Reason != "others" || !strings.Contains(asked[0].Summary, "keeper@lighthouse.example") {
		t.Fatalf("the send set to always asked %+v", asked)
	}
	if r := results(t, events)["call_send"]; !r.IsError || !strings.Contains(said(r), "declined") || len(stand.Sent()) != 0 {
		t.Errorf("declined, the send gave %+v and the stand-in sent %d", r, len(stand.Sent()))
	}
	events, _, _ = sv.sendAnswering(g.test.ID, sv.newChat(g.test.ID), `{"text":"Send the keeper a note about the lamp."}`, "approve", nil)
	if r := results(t, events)["call_send"]; r.IsError || len(stand.Sent()) != 1 {
		t.Errorf("approved, the send gave %+v and the stand-in sent %d", r, len(stand.Sent()))
	}
	g.noGoogleTokens(events)
}

// A personal calendar change follows the person's policy: set to always it
// runs without asking, and left at its default it asks. A change that
// invites a guest always asks, set to always or not.
func TestAPersonalCalendarChangeFollowsThePolicyAndAGuestChangeAlwaysAsks(t *testing.T) {
	g := newRig(t)
	stand := googletest.New()
	sv := g.serveGoogle(stand, scripted(t, googleScripts))

	var asked []Approval
	note := func(a Approval) { asked = append(asked, a) }
	events, _, _ := sv.sendAnswering(g.test.ID, sv.newChat(g.test.ID), `{"text":"Put a swim on my calendar."}`, "approve", note)
	if len(asked) != 1 || asked[0].Tool != "calendar_create_event" || asked[0].Reason != "policy" || !strings.Contains(asked[0].Summary, "Nobody else is invited.") {
		t.Fatalf("at its default the personal change asked %+v", asked)
	}
	if r := results(t, events)["call_swim"]; r.IsError {
		t.Errorf("approved, the swim gave %+v", r)
	}

	sv.setPolicyFor(g.test.ID, "calendar_create_event", "always")
	asked = nil
	events, _, _ = sv.sendAnswering(g.test.ID, sv.newChat(g.test.ID), `{"text":"Put a swim on my calendar."}`, "", note)
	if len(asked) != 0 {
		t.Errorf("set to always, the personal change asked %+v", asked)
	}
	if r := results(t, events)["call_swim"]; r.IsError || !strings.Contains(said(r), "Added “Swim”") {
		t.Errorf("set to always, the swim gave %+v", r)
	}

	events, _, _ = sv.sendAnswering(g.test.ID, sv.newChat(g.test.ID), `{"text":"Invite the keeper to tea."}`, "approve", note)
	if len(asked) != 1 || asked[0].Reason != "others" || !strings.Contains(asked[0].Summary, "Google sends an invitation to keeper@lighthouse.example.") {
		t.Fatalf("set to always, the change with a guest asked %+v", asked)
	}
	if r := results(t, events)["call_tea"]; r.IsError || !strings.Contains(said(r), "sent an invitation to keeper@lighthouse.example") {
		t.Errorf("approved, the tea gave %+v", r)
	}
	changes := stand.Changes()
	if len(changes) != 3 || changes[0].SendUpdates != "none" || changes[1].SendUpdates != "none" || changes[2].SendUpdates != "all" {
		t.Errorf("the stand-in's calendar changed as %+v", changes)
	}

	// An update set to always that leaves out the event's guests runs, finds
	// them, and changes nothing: the card would have shown nobody.
	sv.setPolicyFor(g.test.ID, "calendar_update_event", "always")
	asked = nil
	events, _, _ = sv.sendAnswering(g.test.ID, sv.newChat(g.test.ID), `{"text":"Rename the committee."}`, "", note)
	if r := results(t, events)["call_rename"]; !r.IsError || !strings.Contains(said(r), "nothing was changed") || !strings.Contains(said(r), "keeper@lighthouse.example") {
		t.Errorf("the update that left out the guests gave %+v", r)
	}
	if title, _, _, _ := stand.Event("e2committee"); title != "Harbour committee" || len(stand.Changes()) != 3 {
		t.Errorf("the committee became %q after %d changes", title, len(stand.Changes()))
	}
	g.noGoogleTokens(events)

	// The approvals screen marks the tools whose calls can reach others.
	var list ToolsReply
	sv.call(g.test.ID, "GET", "tools", "", 200, &list)
	marks := map[string]bool{}
	for _, tp := range list.Tools {
		marks[tp.Name] = tp.ReachesOthers
	}
	if !marks["gmail_send"] || !marks["calendar_create_event"] || marks["gmail_draft"] || marks["gmail_search"] {
		t.Errorf("the approvals screen marks %v", marks)
	}
}
