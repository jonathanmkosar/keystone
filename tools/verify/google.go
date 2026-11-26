package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/connectors"
	"github.com/jonathanmkosar/keystone/internal/google"
	"github.com/jonathanmkosar/keystone/internal/google/googletest"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/approval"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/settings"
)

// The Gmail and Calendar connectors' parts of verify (P12-02).
//
//   - googleRecorded is a step of "calls recorded": a stand-in Google is
//     served on this machine, the admin gives the test tenant the invented
//     OAuth client, and the test tenant connects Google through the API
//     (start, the stand-in's consent, finish). Its recorded turns search
//     mail, draft a reply and send it with gmail_send set to always; add a
//     personal event with calendar_create_event set to always; and invite a
//     guest. The draft must ask after the mail it read, the send must ask
//     because it reaches other people, set to always or not, the personal
//     event must run without asking, and the invitation must ask; nothing
//     may reach the stand-in while it waits; every call is in the audit
//     log; and no token is in any answer, event or audit entry.
//   - googleRule is part of the standing check "approval": a call that
//     reaches other people asks under every policy but never, which
//     refuses it, and no standing approval covers it.
//   - keysInGoogle is a surface of "no keys in output": a stand-in Google
//     that repeats the refresh token in its results, its errors and its
//     token answers.
func init() {
	componentDocs["internal/google"] = "docs/architecture/google.md"
	componentDocs["internal/google/googletest"] = "docs/architecture/google.md"
	networkAllowed["internal/google"] = "calls Google's OAuth, Gmail and Calendar endpoints at the addresses in settings, through the client the connectors give it; its tests call the stand-in on this machine only"
	networkAllowed["internal/google/googletest"] = "a stand-in Google served on this machine, for tests and verify; never in the server Keystone ships"
	networkAllowed["tools/verify"] += "; the Google step serves a stand-in Google on this machine and calls nothing else"
	keySurfaces = append(keySurfaces, struct {
		name string
		run  func(root, dir, value string) ([]keyOutput, error)
	}{"Google", keysInGoogle})
	// Google's shapes of a key: an OAuth client secret, an access token, a
	// refresh token and an API key.
	secretShapes = append(secretShapes,
		regexp.MustCompile(`GOCSPX-[A-Za-z0-9_-]{20,}`),
		regexp.MustCompile(`ya29\.[A-Za-z0-9_-]{20,}`),
		regexp.MustCompile(`1//0[A-Za-z0-9_-]{20,}`),
		regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`),
	)
}

// The messages of fixtures/test-tenant/turns/google.json.
const (
	googleMailMessage   = "Find the lunch email, draft a reply, and send it."
	googleSwimMessage   = "Put a swim on my calendar on Thursday morning."
	googleInviteMessage = "Invite the keeper to tea on Saturday."
)

// openGoogleRig is openToolRig with the connectors' Google at the stand-in.
func openGoogleRig(endpoints google.Endpoints) (rig *toolRig, err error) {
	dir, err := os.MkdirTemp("", "keystone-google-")
	if err != nil {
		return nil, err
	}
	var undo []func()
	done := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		os.RemoveAll(dir)
	}
	defer func() {
		if err != nil {
			done()
		}
	}()
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	other, err := in.Create("Ordinary Person")
	if err != nil {
		return nil, err
	}
	fx, err := testtenant.Shipped()
	if err != nil {
		return nil, err
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		return nil, err
	}
	loaded, err := testtenant.Load(in, fx, o)
	if err != nil {
		return nil, err
	}
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	c.Connectors.Google.Endpoints, c.Connectors.Google.OnThisMachine = endpoints, true
	h, stop, err := app.Open(c)
	if err != nil {
		return nil, err
	}
	w.h, undo = h, append(undo, stop)
	rig = &toolRig{w: w}
	if rig.test, err = in.Store(loaded.Tenant.ID); err != nil {
		return nil, err
	}
	undo = append(undo, func() { rig.test.Close() })
	if rig.other, err = in.Store(other.ID); err != nil {
		return nil, err
	}
	undo = append(undo, func() { rig.other.Close() })
	token, rec := w.signIn(loaded.Tenant.ID, fx.Password)
	if token == "" {
		return nil, fmt.Errorf("the test tenant cannot sign in through the API: status %d", rec.Code)
	}
	rig.token, rig.close = token, done
	return rig, nil
}

// asPage sets the Origin a page of Keystone's own sends with a write.
func asPage(r *http.Request) { r.Header.Set("Origin", "http://"+r.Host) }

// approveEach approves every call of the test tenant's that waits, until
// stop is called, and says what each asked and how much the stand-in had
// sent or changed when it was answered.
func approveEach(w *walker, token string, s *tenant.Store, stand *googletest.Server) (stop func() []askedGoogle) {
	var mu sync.Mutex
	var seen []askedGoogle
	quit, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		answered := map[string]bool{}
		for {
			select {
			case <-quit:
				return
			case <-time.After(20 * time.Millisecond):
			}
			rows, err := s.DB().Query("SELECT id, turn_id, chat_id, tool, reason, summary FROM tool_approvals WHERE status = 'pending'")
			if err != nil {
				continue
			}
			var pending []turn.Approval
			for rows.Next() {
				var a turn.Approval
				if rows.Scan(&a.ID, &a.TurnID, &a.ChatID, &a.Tool, &a.Reason, &a.Summary) == nil && !answered[a.ID] {
					pending = append(pending, a)
				}
			}
			rows.Close()
			for _, a := range pending {
				answered[a.ID] = true
				got := askedGoogle{a: a, sent: len(stand.Sent()), drafts: len(stand.Drafts()), changes: len(stand.Changes())}
				rec := w.request("POST", api.Prefix+"chats/"+a.ChatID+"/turns/"+a.TurnID+"/approvals/"+a.ID+"/approve", token, "", nil)
				got.status = rec.Code
				mu.Lock()
				seen = append(seen, got)
				mu.Unlock()
			}
		}
	}()
	return func() []askedGoogle {
		close(quit)
		<-finished
		mu.Lock()
		defer mu.Unlock()
		return seen
	}
}

type askedGoogle struct {
	a                     turn.Approval
	sent, drafts, changes int
	status                int
}

// googleRecorded runs the test tenant's recorded Gmail and Calendar turns
// against the stand-in.
func googleRecorded() (problems []string, err error) {
	stand := googletest.New()
	srv := httptest.NewServer(stand)
	defer srv.Close()
	r, err := openGoogleRig(stand.Endpoints(srv.URL))
	if err != nil {
		return nil, err
	}
	defer r.close()

	// The admin's part: the OAuth client's id as a setting, its secret in
	// the person's vault.
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	if err := tenantsettings.Set(context.Background(), r.test, reg, tenantsettings.Admin, connectors.KeyGoogleClientID, googletest.ClientID); err != nil {
		return nil, err
	}
	if err := r.test.Secrets().Set(connectors.GoogleClientSecret, googletest.ClientSecret); err != nil {
		return nil, err
	}

	// The person's part: Connect Google, through the API.
	var said []string
	rec := r.w.request("POST", api.Prefix+"google/start", r.token, `{}`, asPage)
	said = append(said, rec.Body.String())
	var start connectors.GoogleStartReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &start) != nil {
		return nil, fmt.Errorf("the test tenant cannot start a Google sign-in: status %d", rec.Code)
	}
	if u, _ := url.Parse(start.URL); u == nil || u.Query().Get("code_challenge_method") != "S256" || !strings.HasSuffix(start.Redirect, connectors.GoogleRedirectPath) {
		problems = append(problems, "the Google sign-in does not use PKCE or does not return to the connectors page: "+start.URL)
	}
	back, err := stand.Authorize(start.URL)
	if err != nil {
		return nil, fmt.Errorf("the stand-in Google refused the sign-in address: %w", err)
	}
	bu, _ := url.Parse(back)
	body, _ := json.Marshal(connectors.GoogleFinishBody{State: bu.Query().Get("state"), Code: bu.Query().Get("code")})
	rec = r.w.request("POST", api.Prefix+"google/finish", r.token, string(body), asPage)
	said = append(said, rec.Body.String())
	var st connectors.GoogleStatus
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &st) != nil || !st.Connected || len(st.Services) != 2 || !st.Services[0].Added || !st.Services[1].Added {
		return nil, fmt.Errorf("the test tenant cannot finish a Google sign-in: status %d, %s", rec.Code, rec.Body.String())
	}
	refresh, _, _ := r.test.Secrets().Get(connectors.GoogleSecret)
	for _, p := range []struct{ tool, policy string }{{"gmail_send", "always"}, {"calendar_create_event", "always"}} {
		if rec := r.w.request("PUT", api.Prefix+"tools/"+p.tool+"/policy", r.token, `{"policy":"`+p.policy+`"}`, nil); rec.Code != http.StatusOK {
			return nil, fmt.Errorf("%s has no policy route: status %d", p.tool, rec.Code)
		}
	}

	var turns []sent
	for _, message := range []string{googleMailMessage, googleSwimMessage, googleInviteMessage} {
		stop := approveEach(r.w, r.token, r.test, stand)
		got, err := r.send(message)
		asked := stop()
		if err != nil {
			return nil, err
		}
		turns = append(turns, got)
		if got.end.Status != turn.StatusDone {
			problems = append(problems, fmt.Sprintf("the recorded turn %q ended as %s", message, got.end.Status))
		}
		for _, c := range got.calls {
			if res, ok := got.results[c.ID]; !ok || res.IsError {
				problems = append(problems, fmt.Sprintf("the call to %s gave an error or nothing: %q", c.Name, resultText(res)))
			}
		}
		var askedTools []string
		for _, a := range asked {
			askedTools = append(askedTools, a.a.Tool+":"+a.a.Reason)
			if a.status != http.StatusOK {
				problems = append(problems, fmt.Sprintf("approving %s answered %d", a.a.Tool, a.status))
			}
		}
		switch message {
		case googleMailMessage:
			if got := strings.Join(askedTools, " "); got != "gmail_draft:outside gmail_send:others" {
				problems = append(problems, fmt.Sprintf("searching, drafting and sending asked %q, want the draft after outside content and the send, set to always, because it reaches others", got))
			}
			if len(asked) == 2 && (asked[0].drafts != 0 || asked[1].sent != 0) {
				problems = append(problems, "a draft or an email reached the stand-in Google before the person approved it")
			}
			if len(asked) == 2 && (!strings.Contains(asked[1].a.Summary, "person.two@example.org") || !strings.Contains(asked[1].a.Summary, "Re: Lunch on Friday?") || !strings.Contains(asked[1].a.Summary, "noon by the harbour")) {
				problems = append(problems, "the send's card does not show its recipient, subject and words: "+asked[1].a.Summary)
			}
			if s := stand.Sent(); len(s) != 1 || len(stand.Drafts()) != 1 || s[0].InReplyTo != "<lunch-1@example.org>" {
				problems = append(problems, fmt.Sprintf("the stand-in Google holds %d drafts and sent %d emails, want 1 and 1 in the lunch thread", len(stand.Drafts()), len(s)))
			}
		case googleSwimMessage:
			if len(asked) != 0 {
				problems = append(problems, fmt.Sprintf("a personal event with calendar_create_event set to always asked %q", strings.Join(askedTools, " ")))
			}
		case googleInviteMessage:
			if got := strings.Join(askedTools, " "); got != "calendar_create_event:others" {
				problems = append(problems, fmt.Sprintf("an event with a guest, set to always, asked %q, want it to ask because it reaches others", got))
			}
			if len(asked) == 1 && asked[0].changes != 1 {
				problems = append(problems, "the invitation reached the stand-in Google before the person approved it")
			}
		}
	}
	if ch := stand.Changes(); len(ch) != 2 || ch[0].SendUpdates != "none" || ch[1].SendUpdates != "all" {
		problems = append(problems, fmt.Sprintf("the stand-in's calendar changed as %+v, want a personal event that told nobody and an invitation", ch))
	}

	// The audit log: each call before and after; no token anywhere.
	var all []string
	for _, got := range turns {
		entries, err := r.entriesOf(audit.KindTool, got.requestID)
		if err != nil {
			return nil, err
		}
		if len(entries) != 2*len(got.calls) {
			problems = append(problems, fmt.Sprintf("%d Google calls left %d audit entries, want two each", len(got.calls), len(entries)))
		}
		for _, e := range got.events {
			all = append(all, e.data)
		}
	}
	entries, err := audit.List(context.Background(), r.test, audit.Filter{})
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		all = append(all, e.Sent, e.Received, e.Action)
	}
	all = append(all, said...)
	for _, text := range all {
		for _, held := range []string{refresh, googletest.ClientSecret, "invented-access-"} {
			if held != "" && strings.Contains(text, held) {
				problems = append(problems, "a Google token or the client's secret is in an answer, an event or the audit log")
				return problems, nil
			}
		}
	}
	return problems, nil
}

// googleRule is the rule's part of "approval": a call that reaches other
// people asks under every policy but never, which refuses it, with outside
// content or without, and only a person's answer covers it.
func googleRule() []string {
	var problems []string
	input := []byte(`{"to":["someone@example.com"]}`)
	for _, outside := range []bool{false, true} {
		for _, p := range []approval.Policy{approval.Always, approval.Ask, "", "sometimes"} {
			d := approval.Decide(approval.Call{Tool: "gmail_send", Input: input, Policy: p, Outside: outside, Others: true})
			if d.Verdict != approval.VerdictAsk || d.Approval.Granted() {
				problems = append(problems, fmt.Sprintf("approval.Decide does not ask for a call that reaches other people under the policy %q", p))
			}
		}
		if d := approval.Decide(approval.Call{Tool: "gmail_send", Input: input, Policy: approval.Never, Outside: outside, Others: true}); d.Verdict != approval.VerdictRefuse {
			problems = append(problems, "approval.Decide does not refuse a call set to never that reaches other people")
		}
	}
	standing := approval.Decide(approval.Call{Tool: "gmail_send", Input: input, Policy: approval.Always}).Approval
	if approval.CheckReach(true, standing) == nil {
		problems = append(problems, "a standing approval lets a call that reaches other people run")
	}
	return problems
}

// keysInGoogle stores value as a person's Google refresh token, with a
// stand-in Google that takes it and repeats it in every answer: results,
// errors and token answers. It returns the tools' results and errors, the
// problem a check keeps, the connectors as kept, the audit log and the data
// folder.
func keysInGoogle(_, dir, value string) ([]keyOutput, error) {
	data := filepath.Join(dir, "data")
	in, err := tenant.OpenWithKeeper(data, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		return nil, err
	}
	s, err := in.Store(made.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	stand := googletest.New()
	stand.AddRefresh(value, google.ScopesFor(google.Services()))
	stand.Echo = value
	srv := httptest.NewServer(stand)
	defer srv.Close()
	c.Connectors.Google.Endpoints, c.Connectors.Google.OnThisMachine = stand.Endpoints(srv.URL), true
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Admin, connectors.KeyGoogleClientID, googletest.ClientID); err != nil {
		return nil, err
	}
	if err := s.Secrets().Set(connectors.GoogleClientSecret, googletest.ClientSecret); err != nil {
		return nil, err
	}
	if err := s.Secrets().Set(connectors.GoogleSecret, value); err != nil {
		return nil, err
	}
	var out []keyOutput
	for _, svc := range google.Services() {
		if _, err := connectors.Add(ctx, s, connectors.Connector{Name: string(svc), Transport: connectors.TransportGoogle, Secret: connectors.GoogleSecret, Enabled: true, Outside: true}, connectors.ByPerson, 0); err != nil {
			return nil, err
		}
		k, err := c.Connectors.Check(ctx, s, string(svc))
		out = append(out, keyOutput{"the check of " + string(svc), []byte(fmt.Sprintf("%+v %v", k, err))})
	}
	pool := connectors.NewPool(c.Connectors, nil)
	defer pool.Close()
	made2, err := chat.Create(s)
	if err != nil {
		return out, err
	}
	_, run := pool.ForCall(ctx, s, made2.ID, nil, nil)
	reg2 := tools.New()
	calls := []struct{ name, input string }{
		{"gmail_search", `{"query":"tide"}`},
		{"gmail_read", `{"id":"m1a2b3"}`},
		{"gmail_read", `{"id":"nosuchmessage"}`},
		{"calendar_get_event", `{"event_id":"e2committee"}`},
		{"calendar_get_event", `{"event_id":"nosuchevent"}`},
	}
	for _, call := range calls {
		if _, ok := reg2.Get(call.name); !ok {
			t, ok := run[call.name]
			if !ok {
				return out, fmt.Errorf("the Google connectors did not offer %s", call.name)
			}
			reg2.Add(t)
		}
	}
	for i, call := range calls {
		res := reg2.Run(ctx, tools.Call{Store: s, RequestID: fmt.Sprintf("req-google-%d", i), Actor: "person", ID: fmt.Sprintf("call_%d", i), Name: call.name, Input: json.RawMessage(call.input)})
		out = append(out, keyOutput{"the result of " + call.name, []byte(fmt.Sprintf("%+v", res))})
	}
	// The check is worth nothing unless the token really was used and
	// repeated.
	used := false
	for _, call := range stand.Calls() {
		used = used || call.Path == "/token"
	}
	if !used {
		return out, fmt.Errorf("the stand-in Google was never asked for an access token with the stored refresh token")
	}
	kept, err := connectors.List(ctx, s)
	if err != nil {
		return out, err
	}
	out = append(out, keyOutput{"the connectors as kept", []byte(fmt.Sprintf("%+v", kept))})
	entries, err := audit.List(ctx, s, audit.Filter{})
	if err != nil {
		return out, err
	}
	out = append(out, keyOutput{"the audit log", []byte(fmt.Sprintf("%+v", entries))})
	files, err := folderFiles(data)
	return append(out, files...), err
}
