package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/share"
	"github.com/jonathanmkosar/keystone/internal/shareapi"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// Share links (P10-04): two packages with a doc, the rules (a guard of
// fixed rules 1 and 3, which reads no setting) and the routes.
func init() {
	componentDocs["internal/share"] = "docs/architecture/share.md"
	componentDocs["internal/shareapi"] = "docs/architecture/share.md"
	networkAllowed["internal/shareapi"] = "the share routes' handlers and the two share doors, served by the server; it opens no connection"
	fixedRuleGuards["rules.tenant_isolation"] = append(fixedRuleGuards["rules.tenant_isolation"], "internal/share")
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/share")
}

// Invented, for throwaway folders only.
const (
	shareWordsShared  = "When does the harbour ferry leave on Sundays?"
	shareWordsReply   = "The first ferry leaves at nine."
	shareWordsPrivate = "A private note about the lighthouse keys."
	shareWordsOther   = "Tenant two's words about the beehives."
	shareToolSaid     = "Search result: the ferry office opens at eight."
	shareThought      = "The person wants the Sunday times."
	sharePasswordOne  = "share check person one's phrase"
	sharePasswordTwo  = "share check person two's phrase"
)

var shareTokenShape = regexp.MustCompile(`ksh_[a-f0-9]{16}_[a-f0-9]{64}`)

// shareChat makes a chat titled title with these messages in a row.
func shareChat(s *tenant.Store, title string, msgs ...seam.Message) (string, error) {
	c, err := chat.Create(s)
	if err != nil {
		return "", err
	}
	lim, err := chat.DefaultLimits()
	if err != nil {
		return "", err
	}
	if _, err := chat.Rename(s, lim, c.ID, title); err != nil {
		return "", err
	}
	var parent int64
	for _, m := range msgs {
		got, err := chat.Append(s, c.ID, chat.NewMessage{ParentID: parent, Role: m.Role, Blocks: m.Blocks})
		if err != nil {
			return "", err
		}
		parent = got.ID
	}
	return c.ID, nil
}

func shareSaid(role seam.Role, text string) seam.Message {
	return seam.Message{Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}}
}

// shareMake makes a link through the API and returns its answer.
func shareMake(w *walker, session, chatID, body string) (shareapi.CreateReply, int) {
	rec := w.request("POST", api.Prefix+"chats/"+chatID+"/shares", session, body, nil)
	var made shareapi.CreateReply
	if rec.Code == http.StatusCreated {
		json.Unmarshal(rec.Body.Bytes(), &made)
	}
	return made, rec.Code
}

// shareProbes is the share door's part of the tenant isolation check: the
// door finds its tenant from the link's token alone. A link shows its own
// chat and no other of the same person, and nothing of another tenant;
// tenant two's session changes nothing a link shows and leaves nothing in
// tenant two's log; a token re-addressed to tenant two, or with tenant
// one's row copied into tenant two's database, finds nothing; a share
// token is no session and a session token is no share token; and tenant
// two can neither list nor revoke tenant one's link.
func shareProbes(in *tenant.Install, one, two *tenant.Store) (reached []string, err error) {
	o, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	if err := signin.SetPassword(one, sharePasswordOne, o); err != nil {
		return nil, err
	}
	if err := signin.SetPassword(two, sharePasswordTwo, o); err != nil {
		return nil, err
	}
	shared, err := shareChat(one, "Sunday ferry", shareSaid(seam.RoleUser, shareWordsShared), shareSaid(seam.RoleAssistant, shareWordsReply))
	if err != nil {
		return nil, err
	}
	if _, err := shareChat(one, "Lighthouse keys", shareSaid(seam.RoleUser, shareWordsPrivate)); err != nil {
		return nil, err
	}
	twos, err := shareChat(two, "Beehives", shareSaid(seam.RoleUser, shareWordsOther))
	if err != nil {
		return nil, err
	}
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	sessionOne, rec := w.signIn(one.TenantID(), sharePasswordOne)
	if sessionOne == "" {
		return nil, fmt.Errorf("tenant one cannot sign in for the share probes: status %d", rec.Code)
	}
	sessionTwo, rec := w.signIn(two.TenantID(), sharePasswordTwo)
	if sessionTwo == "" {
		return nil, fmt.Errorf("tenant two cannot sign in for the share probes: status %d", rec.Code)
	}
	made, status := shareMake(w, sessionOne, shared, "")
	if status != http.StatusCreated || made.Token == "" {
		return nil, fmt.Errorf("tenant one cannot make a share link: status %d", status)
	}
	if id, _ := share.TenantOf(made.Token); id != one.TenantID() {
		reached = append(reached, "a share link's token names a tenant other than its chat's")
	}
	count := func(s *tenant.Store) int {
		got, _ := logOf(s)
		return len(got)
	}
	haveTwo := count(two)
	for who, session := range map[string]string{"no session": "", "tenant two's session": sessionTwo} {
		rec := w.request("GET", api.Prefix+"shared/"+made.Token, session, "", func(r *http.Request) {
			r.Header.Set("X-Tenant", two.TenantID())
			r.URL.RawQuery = "tenant=" + two.TenantID()
		})
		text := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(text, shareWordsShared) {
			return nil, fmt.Errorf("tenant one's share link does not show its chat with %s: status %d", who, rec.Code)
		}
		for _, never := range []string{shareWordsPrivate, shareWordsOther, "Lighthouse keys", "Beehives"} {
			if strings.Contains(text, never) {
				reached = append(reached, fmt.Sprintf("tenant one's share link, opened with %s, shows %q, which is not of its chat", who, never))
			}
		}
	}
	if count(two) != haveTwo {
		reached = append(reached, "a share link opened with tenant two's session wrote to tenant two's audit log")
	}
	readdressed := strings.Replace(made.Token, one.TenantID(), two.TenantID(), 1)
	if rec := w.request("GET", api.Prefix+"shared/"+readdressed, "", "", nil); rec.Code != http.StatusNotFound {
		reached = append(reached, fmt.Sprintf("a share token re-addressed to tenant two answered %d", rec.Code))
	}
	// Tenant one's link's hash copied into tenant two's database, on a row
	// of tenant two's own that points at tenant two's chat.
	if _, err := two.DB().Exec(`INSERT INTO share_links(id, chat_id, token_hash, view, created_at) VALUES('ffffffffffffffff', ?, ?, 'live', '2026-11-26T00:00:00.000Z')`,
		twos, shareHash(one, made.Link.ID)); err != nil {
		return nil, err
	}
	for name, tok := range map[string]string{"tenant one's token": made.Token, "a re-addressed token": readdressed} {
		rec := w.request("GET", api.Prefix+"shared/"+tok, "", "", nil)
		if strings.Contains(rec.Body.String(), shareWordsOther) {
			reached = append(reached, fmt.Sprintf("with tenant one's link row copied into tenant two's database, %s showed tenant two's chat", name))
		}
	}
	if count(two) != haveTwo {
		reached = append(reached, "share tokens wrote to tenant two's audit log")
	}
	// A share token is no session, and a session token no share token.
	if rec := w.request("GET", api.Prefix+"chats", made.Token, "", nil); rec.Code != http.StatusUnauthorized {
		reached = append(reached, fmt.Sprintf("a share token used as a session answered %d", rec.Code))
	}
	if rec := w.request("GET", api.Prefix+"shared/"+sessionOne, "", "", nil); rec.Code != http.StatusNotFound {
		reached = append(reached, fmt.Sprintf("a session token used as a share token answered %d", rec.Code))
	}
	// Tenant two cannot list or turn off tenant one's link.
	if rec := w.request("GET", api.Prefix+"chats/"+shared+"/shares", sessionTwo, "", nil); rec.Code != http.StatusNotFound {
		reached = append(reached, fmt.Sprintf("tenant two listed tenant one's chat's links: status %d", rec.Code))
	}
	if rec := w.request("DELETE", api.Prefix+"shares/"+made.Link.ID, sessionTwo, "", nil); rec.Code != http.StatusNotFound {
		reached = append(reached, fmt.Sprintf("tenant two turned off a link: status %d", rec.Code))
	}
	if rec := w.request("GET", api.Prefix+"shared/"+made.Token, "", "", nil); rec.Code != http.StatusOK {
		reached = append(reached, fmt.Sprintf("tenant one's link stopped opening after tenant two's tries: status %d", rec.Code))
	}
	return reached, nil
}

// shareHash reads a link's stored hash.
func shareHash(s *tenant.Store, id string) []byte {
	var hash []byte
	s.DB().QueryRow("SELECT token_hash FROM share_links WHERE id = ?", id).Scan(&hash)
	return hash
}

// The "share" check (P10-04), the item's proof as a standing check, on the
// real server with a throwaway folder and two invented people:
//
//  1. A link shows only its chat: its words and replies, and not another
//     chat of the same person, nothing of the other person, no thinking and
//     no tool's input or result. The other person's session changes nothing.
//  2. Every view is in the chat's tenant's audit log, under the route and
//     the link's id; no audit entry, no line of the server's log and no
//     file in the data folder holds the token.
//  3. The doors set no cookie.
//  4. A revoked link is refused with 410 and shows nothing, and stays in
//     its person's list as revoked; an expired link is refused with 410.
//  5. A snapshot does not show a later message; a live link does.
//  6. A chat held for sensitive content is not shared (409), and a link
//     made before it was held is refused (403); an incognito chat is not
//     shared.
//
// Last, from the source: the shared page is drawn before the app asks who
// is signed in, reads the link with credentials omitted, sets no HTML from
// text, and the main page's policy is still default-src 'self'.
func checkShare(root string) error {
	problems, err := shareProblems()
	if err != nil {
		return err
	}
	problems = append(problems, shareSourceProblems(root)...)
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func shareProblems() ([]string, error) {
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "keystone-share-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	o, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	open := func(name, password string) (*tenant.Store, error) {
		t, err := in.Create(name)
		if err != nil {
			return nil, err
		}
		s, err := in.Store(t.ID)
		if err != nil {
			return nil, err
		}
		return s, signin.SetPassword(s, password, o)
	}
	one, err := open("Share Check Person", sharePasswordOne)
	if err != nil {
		return nil, err
	}
	defer one.Close()
	two, err := open("Share Check Other Person", sharePasswordTwo)
	if err != nil {
		return nil, err
	}
	defer two.Close()

	input, _ := json.Marshal(map[string]string{"query": "ferry office"})
	shared, err := shareChat(one, "Sunday ferry",
		shareSaid(seam.RoleUser, shareWordsShared),
		seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{
			{Type: seam.BlockThinking, Text: shareThought, Signature: "sig"},
			{Type: seam.BlockToolCall, ID: "call-1", Name: "web_search", Input: input},
		}},
		seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockToolResult, ID: "call-1", Content: []seam.Block{{Type: seam.BlockText, Text: shareToolSaid}}}}},
		shareSaid(seam.RoleAssistant, shareWordsReply))
	if err != nil {
		return nil, err
	}
	if _, err := shareChat(one, "Lighthouse keys", shareSaid(seam.RoleUser, shareWordsPrivate)); err != nil {
		return nil, err
	}
	if _, err := shareChat(two, "Beehives", shareSaid(seam.RoleUser, shareWordsOther)); err != nil {
		return nil, err
	}

	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	session, rec := w.signIn(one.TenantID(), sharePasswordOne)
	if session == "" {
		return nil, fmt.Errorf("the invented person cannot sign in through the API: status %d", rec.Code)
	}
	otherSession, rec := w.signIn(two.TenantID(), sharePasswordTwo)
	if otherSession == "" {
		return nil, fmt.Errorf("the other invented person cannot sign in through the API: status %d", rec.Code)
	}
	door := func(token, session string) *httptest.ResponseRecorder {
		return w.request("GET", api.Prefix+"shared/"+token, session, "", nil)
	}

	// 1. Only that chat.
	live, status := shareMake(w, session, shared, `{"view":"live"}`)
	if status != http.StatusCreated {
		return nil, fmt.Errorf("making a live link answered %d", status)
	}
	snap, status := shareMake(w, session, shared, `{"view":"snapshot"}`)
	if status != http.StatusCreated {
		return nil, fmt.Errorf("making a snapshot link answered %d", status)
	}
	var tokens []string
	tokens = append(tokens, live.Token, snap.Token)
	for who, s := range map[string]string{"no one signed in": "", "the other person signed in": otherSession} {
		rec := door(live.Token, s)
		text := rec.Body.String()
		if rec.Code != http.StatusOK {
			say("with %s the link answered %d", who, rec.Code)
			continue
		}
		for _, want := range []string{"Sunday ferry", shareWordsShared, shareWordsReply, `"web_search"`} {
			if !strings.Contains(text, want) {
				say("with %s the link does not show %q", who, want)
			}
		}
		for _, never := range []string{shareWordsPrivate, shareWordsOther, "Lighthouse keys", "Beehives", shareThought, shareToolSaid, "ferry office", "Share Check"} {
			if strings.Contains(text, never) {
				say("with %s the link shows %q, which it must not", who, never)
			}
		}
		// 3. No cookie.
		if c := rec.Header().Values("Set-Cookie"); len(c) > 0 {
			say("with %s the share door set a cookie: %v", who, c)
		}
	}
	if entries, err := audit.List(ctx, two, audit.Filter{}); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Actor, shareapi.ActorPrefix) || strings.Contains(e.Action, "/shared/") {
				say("a view of the person's link is in the other person's audit log: %+v", e)
			}
		}
	}

	// 4. Revoked and expired.
	rec = w.request("DELETE", api.Prefix+"shares/"+snap.Link.ID, session, "", nil)
	if rec.Code != http.StatusOK {
		say("revoking a link answered %d", rec.Code)
	}
	for i := 0; i < 2; i++ {
		rec := door(snap.Token, "")
		if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), `"revoked"`) || strings.Contains(rec.Body.String(), shareWordsShared) {
			say("a revoked link answered %d: %s", rec.Code, rec.Body)
		}
	}
	var listed shareapi.LinksReply
	if rec := w.request("GET", api.Prefix+"shares", session, "", nil); json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Links) != 2 {
		say("the person's list of links answered %d: %s", rec.Code, rec.Body)
	} else {
		for _, l := range listed.Links {
			if l.ID == snap.Link.ID && l.State != share.StateRevoked {
				say("the revoked link is listed as %s", l.State)
			}
		}
	}
	if err := tenantsettings.Set(ctx, one, reg, tenantsettings.Person, shareapi.KeyLifetime, "1ms"); err != nil {
		return nil, err
	}
	short, status := shareMake(w, session, shared, "")
	if status != http.StatusCreated {
		return nil, fmt.Errorf("making a short link answered %d", status)
	}
	tokens = append(tokens, short.Token)
	time.Sleep(10 * time.Millisecond)
	if rec := door(short.Token, ""); rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), `"expired"`) {
		say("an expired link answered %d: %s", rec.Code, rec.Body)
	}

	// 5. Snapshot and live after a later message.
	const later = "And on Mondays?"
	path, err := chat.Path(one, shared)
	if err != nil {
		return nil, err
	}
	if _, err := chat.Append(one, shared, chat.NewMessage{ParentID: path[len(path)-1].ID, Role: seam.RoleUser, Blocks: shareSaid(seam.RoleUser, later).Blocks}); err != nil {
		return nil, err
	}
	if rec := door(live.Token, ""); !strings.Contains(rec.Body.String(), later) {
		say("a live link does not show a later message")
	}
	if err := tenantsettings.Set(ctx, one, reg, tenantsettings.Person, shareapi.KeyLifetime, "720h"); err != nil {
		return nil, err
	}
	asOf, status := shareMake(w, session, shared, `{"view":"snapshot"}`)
	if status != http.StatusCreated {
		return nil, fmt.Errorf("making a second snapshot answered %d", status)
	}
	tokens = append(tokens, asOf.Token)
	const latest = "And on holidays?"
	path, _ = chat.Path(one, shared)
	if _, err := chat.Append(one, shared, chat.NewMessage{ParentID: path[len(path)-1].ID, Role: seam.RoleAssistant, Blocks: shareSaid(seam.RoleAssistant, latest).Blocks}); err != nil {
		return nil, err
	}
	if rec := door(asOf.Token, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), later) || strings.Contains(rec.Body.String(), latest) {
		say("a snapshot shows a message written after it was made, or not one before: %d %s", rec.Code, rec.Body)
	}

	// 6. Held and incognito.
	if _, err := one.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES('share-check-turn', ?, 'done', 'r', 'person', '{}', '2026-11-26T00:00:00.000Z')", shared); err != nil {
		return nil, err
	}
	if err := recall.Hold(ctx, one, "share-check-turn", shared); err != nil {
		return nil, err
	}
	if rec := door(live.Token, ""); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"sensitive"`) || strings.Contains(rec.Body.String(), shareWordsShared) {
		say("a link of a chat held since answered %d: %s", rec.Code, rec.Body)
	}
	if _, status := shareMake(w, session, shared, ""); status != http.StatusConflict {
		say("sharing a held chat answered %d", status)
	}
	hidden, err := shareChat(one, "Incognito", shareSaid(seam.RoleUser, shareWordsShared))
	if err != nil {
		return nil, err
	}
	if err := memory.SetIncognito(ctx, one, hidden, true); err != nil {
		return nil, err
	}
	if _, status := shareMake(w, session, hidden, ""); status != http.StatusConflict {
		say("sharing an incognito chat answered %d", status)
	}

	// 2. Every view recorded, and the token nowhere.
	entries, err := audit.List(ctx, one, audit.Filter{})
	if err != nil {
		return nil, err
	}
	views := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Actor, shareapi.ActorPrefix) {
			views++
			if e.Action != "GET "+api.Prefix+"shared/{token}" {
				say("a view of a link is recorded as %q", e.Action)
			}
		}
	}
	// Two views, two refusals of the revoked link, one of the expired, one
	// live after the message, one snapshot, one refusal of the held: eight.
	if views != 8 {
		say("the audit log holds %d calls through links, want 8", views)
	}
	var surfaces []keyOutput
	for _, e := range entries {
		raw, _ := json.Marshal(e)
		surfaces = append(surfaces, keyOutput{"the audit log", raw})
	}
	surfaces = append(surfaces, keyOutput{"the server's log", w.log.Bytes()})
	files, err := folderFiles(dir)
	if err != nil {
		return nil, err
	}
	surfaces = append(surfaces, files...)
	for _, s := range surfaces {
		for _, tok := range tokens {
			if bytes.Contains(s.data, []byte(tok)) || bytes.Contains(s.data, []byte(tok[len(tok)-64:])) {
				say("%s holds a share link's token", s.where)
			}
		}
		if shareTokenShape.Match(s.data) {
			say("%s holds something shaped like a share token", s.where)
		}
	}
	if !strings.Contains(w.log.String(), "share: link ") {
		say("the server's log does not name the links that were opened")
	}
	return problems, nil
}

// shareSourceProblems reads the web interface's source for the shared page.
func shareSourceProblems(root string) []string {
	var problems []string
	src := filepath.Join(root, "web", "app", "src")
	page, err := os.ReadFile(filepath.Join(src, "Shared.tsx"))
	if err != nil {
		return []string{"web/app/src/Shared.tsx, the shared page, cannot be read: " + err.Error()}
	}
	if !bytes.Contains(page, []byte(`credentials: "omit"`)) {
		problems = append(problems, "the shared page in Shared.tsx does not read the link with credentials omitted")
	}
	for _, bad := range []string{"dangerouslySetInnerHTML", "innerHTML", "<iframe", "from \"./api\""} {
		if bytes.Contains(page, []byte(bad)) {
			problems = append(problems, "the shared page in Shared.tsx uses "+bad)
		}
	}
	main, err := os.ReadFile(filepath.Join(src, "main.tsx"))
	if err != nil || !bytes.Contains(main, []byte("SharedPage")) {
		problems = append(problems, "main.tsx does not draw the shared page before the app asks who is signed in")
	}
	serve, err := os.ReadFile(filepath.Join(root, "cmd", "keystone", "serve.go"))
	if err != nil || !bytes.Contains(serve, []byte(`h.Set("Content-Security-Policy", "default-src 'self'")`)) {
		problems = append(problems, "the main page's content security policy is no longer default-src 'self' in cmd/keystone/serve.go")
	}
	return problems
}
