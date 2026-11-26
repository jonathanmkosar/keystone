package shareapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/share"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/uploads"
	"github.com/jonathanmkosar/keystone/settings"
)

var bg = context.Background()

// Invented, for throwaway folders only.
const (
	passwordOne  = "share test person one's phrase"
	passwordTwo  = "share test person two's phrase"
	sharedWords  = "When does the harbour ferry leave on Sundays?"
	sharedReply  = "The first ferry leaves at nine."
	privateWords = "A private note about the lighthouse keys."
	otherWords   = "Tenant two's words about the beehives."
)

// A picture's first bytes: enough for its type to be known as PNG.
var picture = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 40)...)

type rig struct {
	t        *testing.T
	in       *tenant.Install
	h        http.Handler
	log      *bytes.Buffer
	one, two *tenant.Store
	reg      []settings.Setting
}

func newRig(t *testing.T) *rig {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, in: in, log: &bytes.Buffer{}, reg: reg}
	open := func(name, password string) *tenant.Store {
		made, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		if err := signin.SetPassword(s, password, o); err != nil {
			t.Fatal(err)
		}
		return s
	}
	r.one, r.two = open("Test Person One", passwordOne), open("Test Person Two", passwordTwo)
	log := api.NewLog(r.log)
	routes := append(api.ShellRoutes(api.Shell{Install: in, SignIn: o, PageLimit: 100, Log: log}), Routes(Options{Install: in, Log: log})...)
	if r.h, err = api.Handler(api.Options{Install: in, Log: log, MaxBodyBytes: 1 << 20, Heartbeat: time.Second}, routes); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) do(method, path, session, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if session != "" {
		req.AddCookie(&http.Cookie{Name: api.CookieName, Value: session})
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

func (r *rig) signIn(s *tenant.Store, password string) string {
	r.t.Helper()
	body, _ := json.Marshal(api.SignInBody{Tenant: s.TenantID(), Password: password})
	rec := r.do("POST", api.Prefix+"session", "", string(body))
	for _, c := range rec.Result().Cookies() {
		if c.Name == api.CookieName {
			return c.Value
		}
	}
	r.t.Fatalf("signing in answered %d", rec.Code)
	return ""
}

// chatWith makes a chat with these messages in a row.
func (r *rig) chatWith(s *tenant.Store, title string, msgs ...seam.Message) string {
	r.t.Helper()
	c, err := chat.Create(s)
	if err != nil {
		r.t.Fatal(err)
	}
	lim, err := chat.DefaultLimits()
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := chat.Rename(s, lim, c.ID, title); err != nil {
		r.t.Fatal(err)
	}
	var parent int64
	for _, m := range msgs {
		got, err := chat.Append(s, c.ID, chat.NewMessage{ParentID: parent, Role: m.Role, Blocks: m.Blocks})
		if err != nil {
			r.t.Fatal(err)
		}
		parent = got.ID
	}
	return c.ID
}

func said(text string) seam.Message {
	return seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}}
}

func answered(text string) seam.Message {
	return seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}}
}

func (r *rig) share(session, chatID, body string) CreateReply {
	r.t.Helper()
	rec := r.do("POST", api.Prefix+"chats/"+chatID+"/shares", session, body)
	var made CreateReply
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil {
		r.t.Fatalf("making a link answered %d: %s", rec.Code, rec.Body)
	}
	return made
}

func (r *rig) entries(s *tenant.Store) []audit.Entry {
	r.t.Helper()
	got, err := audit.List(bg, s, audit.Filter{})
	if err != nil {
		r.t.Fatal(err)
	}
	return got
}

func (r *rig) set(s *tenant.Store, key, value string) {
	r.t.Helper()
	if err := tenantsettings.Set(bg, s, r.reg, tenantsettings.Person, key, value); err != nil {
		r.t.Fatal(err)
	}
}

func hold(t *testing.T, s *tenant.Store, chatID string) {
	t.Helper()
	turnID := "turn-" + chatID
	if _, err := s.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES(?, ?, 'done', 'r', 'person', '{}', '2026-11-26T00:00:00.000Z')", turnID, chatID); err != nil {
		t.Fatal(err)
	}
	if err := recall.Hold(bg, s, turnID, chatID); err != nil {
		t.Fatal(err)
	}
}

func TestALinkShowsOnlyItsChatToAnyoneAndSetsNoCookie(t *testing.T) {
	r := newRig(t)
	shared := r.chatWith(r.one, "Sunday ferry", said(sharedWords), answered(sharedReply))
	r.chatWith(r.one, "Lighthouse keys", said(privateWords), answered("Noted."))
	r.chatWith(r.two, "Beehives", said(otherWords), answered("Noted."))
	sessionOne := r.signIn(r.one, passwordOne)
	sessionTwo := r.signIn(r.two, passwordTwo)

	made := r.share(sessionOne, shared, "")
	if !strings.HasPrefix(made.Path, PagePrefix+"ksh_"+r.one.TenantID()+"_") || made.Token == "" || made.Link.View != share.ViewLive || made.Link.Expires == nil {
		t.Fatalf("the new link is %+v", made)
	}
	if made.Link.Expires.Sub(made.Link.Created) != 720*time.Hour {
		t.Errorf("a new link opens for %s, want the shipped 720h", made.Link.Expires.Sub(made.Link.Created))
	}
	before := len(r.entries(r.two))

	// With no session, and with tenant two's: the same view, of tenant
	// one's one chat, and no cookie set.
	for who, session := range map[string]string{"no one signed in": "", "tenant two signed in": sessionTwo} {
		rec := r.do("GET", api.Prefix+"shared/"+made.Token, session, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("with %s the link answered %d: %s", who, rec.Code, rec.Body)
		}
		if c := rec.Header().Values("Set-Cookie"); len(c) > 0 {
			t.Errorf("with %s the link set a cookie: %v", who, c)
		}
		text := rec.Body.String()
		for _, want := range []string{"Sunday ferry", sharedWords, sharedReply} {
			if !strings.Contains(text, want) {
				t.Errorf("with %s the view has no %q", who, want)
			}
		}
		for _, never := range []string{privateWords, otherWords, "Lighthouse keys", "Beehives", "Test Person", shared + `"`} {
			if strings.Contains(text, never) {
				t.Errorf("with %s the view holds %q: %s", who, never, text)
			}
		}
	}
	if after := len(r.entries(r.two)); after != before {
		t.Errorf("a view made with tenant two's session wrote %d entries in tenant two's log", after-before)
	}

	// Each view is in tenant one's log, under the route and the link, and
	// no log holds the token.
	views := 0
	for _, e := range r.entries(r.one) {
		if e.Action == viewRoute {
			views++
			if e.Actor != ActorPrefix+made.Link.ID || e.Outcome != audit.OutcomeOK || e.Status != http.StatusOK {
				t.Errorf("a view is recorded as %+v", e)
			}
		}
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), made.Token) || strings.Contains(string(raw), made.Token[len(made.Token)-64:]) {
			t.Errorf("an audit entry holds the token: %s", raw)
		}
	}
	if views != 2 {
		t.Errorf("the log holds %d views, want 2", views)
	}
	if strings.Contains(r.log.String(), made.Token[len(made.Token)-64:]) {
		t.Errorf("the server's log holds the token:\n%s", r.log)
	}
	var listed LinksReply
	if rec := r.do("GET", api.Prefix+"shares", sessionOne, ""); json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Links) != 1 || listed.Links[0].Views != 2 || listed.Links[0].LastViewed == nil {
		t.Errorf("the person's list is %+v", listed)
	}
	if strings.Contains(fmt.Sprint(listed), made.Token) {
		t.Error("the list holds the token")
	}
}

func TestAnotherPersonCannotListOrRevokeALink(t *testing.T) {
	r := newRig(t)
	shared := r.chatWith(r.one, "Sunday ferry", said(sharedWords), answered(sharedReply))
	sessionOne, sessionTwo := r.signIn(r.one, passwordOne), r.signIn(r.two, passwordTwo)
	made := r.share(sessionOne, shared, "")
	for _, c := range []struct{ method, path string }{
		{"GET", "chats/" + shared + "/shares"},
		{"POST", "chats/" + shared + "/shares"},
		{"DELETE", "shares/" + made.Link.ID},
	} {
		if rec := r.do(c.method, api.Prefix+c.path, sessionTwo, ""); rec.Code != http.StatusNotFound {
			t.Errorf("tenant two's %s %s answered %d", c.method, c.path, rec.Code)
		}
	}
	var listed LinksReply
	if rec := r.do("GET", api.Prefix+"shares", sessionTwo, ""); json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Links) != 0 {
		t.Errorf("tenant two's list is %s", rec.Body)
	}
	if rec := r.do("GET", api.Prefix+"shared/"+made.Token, "", ""); rec.Code != http.StatusOK {
		t.Errorf("the link stopped opening after tenant two's tries: %d", rec.Code)
	}
}

func TestRefusalsAndTheirStatuses(t *testing.T) {
	r := newRig(t)
	session := r.signIn(r.one, passwordOne)
	shared := r.chatWith(r.one, "Sunday ferry", said(sharedWords), answered(sharedReply))

	// A token that finds nothing: 404, and in no tenant's log.
	made := r.share(session, shared, `{"view":"snapshot"}`)
	if made.Link.View != share.ViewSnapshot {
		t.Errorf("a snapshot was asked for and %s made", made.Link.View)
	}
	before := len(r.entries(r.one))
	for _, tok := range []string{
		strings.Replace(made.Token, r.one.TenantID(), r.two.TenantID(), 1),
		made.Token[:len(made.Token)-1] + "x",
		"ksh_nothing",
	} {
		if rec := r.do("GET", api.Prefix+"shared/"+tok, "", ""); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"not_found"`) {
			t.Errorf("a forged token answered %d: %s", rec.Code, rec.Body)
		}
	}
	if after := len(r.entries(r.one)); after != before {
		t.Errorf("forged tokens wrote %d entries in tenant one's log", after-before)
	}

	// Revoked: 410, recorded as refused.
	rec := r.do("DELETE", api.Prefix+"shares/"+made.Link.ID, session, "")
	var revoked LinkReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &revoked) != nil || revoked.Link.State != share.StateRevoked {
		t.Fatalf("revoking answered %d: %s", rec.Code, rec.Body)
	}
	rec = r.do("GET", api.Prefix+"shared/"+made.Token, "", "")
	if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), `"revoked"`) || strings.Contains(rec.Body.String(), sharedWords) {
		t.Errorf("a revoked link answered %d: %s", rec.Code, rec.Body)
	}
	last := r.entries(r.one)
	if e := last[len(last)-1]; e.Action != viewRoute || e.Outcome != audit.OutcomeRefused || e.Status != http.StatusGone {
		t.Errorf("the refused view is recorded as %+v", e)
	}

	// Held since: 403 for the link, and no new link.
	live := r.share(session, shared, `{"view":"live"}`)
	hold(t, r.one, shared)
	rec = r.do("GET", api.Prefix+"shared/"+live.Token, "", "")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"sensitive"`) || strings.Contains(rec.Body.String(), sharedWords) {
		t.Errorf("a link of a chat held since answered %d: %s", rec.Code, rec.Body)
	}
	if rec := r.do("POST", api.Prefix+"chats/"+shared+"/shares", session, ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"sensitive"`) {
		t.Errorf("sharing a held chat answered %d: %s", rec.Code, rec.Body)
	}

	// Incognito and empty: no link.
	hidden := r.chatWith(r.one, "Incognito", said(sharedWords))
	if err := memory.SetIncognito(bg, r.one, hidden, true); err != nil {
		t.Fatal(err)
	}
	if rec := r.do("POST", api.Prefix+"chats/"+hidden+"/shares", session, ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"incognito"`) {
		t.Errorf("sharing an incognito chat answered %d: %s", rec.Code, rec.Body)
	}
	empty := r.chatWith(r.one, "Empty")
	if rec := r.do("POST", api.Prefix+"chats/"+empty+"/shares", session, ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"empty"`) {
		t.Errorf("sharing an empty chat answered %d: %s", rec.Code, rec.Body)
	}
	if rec := r.do("POST", api.Prefix+"chats/"+empty+"/shares", session, `{"view":"all of it"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a view that is no view answered %d: %s", rec.Code, rec.Body)
	}

	// Expired: 410.
	other := r.chatWith(r.one, "Tides", said("When is high water?"), answered("At noon."))
	r.set(r.one, KeyLifetime, "1ms")
	short := r.share(session, other, "")
	time.Sleep(5 * time.Millisecond)
	if rec := r.do("GET", api.Prefix+"shared/"+short.Token, "", ""); rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), `"expired"`) {
		t.Errorf("an expired link answered %d: %s", rec.Code, rec.Body)
	}
	r.set(r.one, KeyLifetime, "0s")
	if forever := r.share(session, other, ""); forever.Link.Expires != nil {
		t.Errorf("a link made with a lifetime of 0s expires at %v", forever.Link.Expires)
	}
}

func TestImagesAreServedOnlyWhenTheViewShowsThem(t *testing.T) {
	r := newRig(t)
	session := r.signIn(r.one, passwordOne)
	reg := r.reg
	get := func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	}
	uo, err := uploads.ReadOptions(get)
	if err != nil {
		t.Fatal(err)
	}
	c, err := chat.Create(r.one)
	if err != nil {
		t.Fatal(err)
	}
	shown, _, err := uploads.Add(bg, r.one, uo, c.ID, "harbour.png", picture)
	if err != nil {
		t.Fatal(err)
	}
	unsent, _, err := uploads.Add(bg, r.one, uo, c.ID, "unsent.png", append(append([]byte(nil), picture...), 1))
	if err != nil {
		t.Fatal(err)
	}
	first, err := chat.Append(r.one, c.ID, chat.NewMessage{Role: seam.RoleUser, Blocks: []seam.Block{
		{Type: seam.BlockImage, MediaType: shown.MediaType, Ref: shown.ID}, {Type: seam.BlockText, Text: sharedWords},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Append(r.one, c.ID, chat.NewMessage{ParentID: first.ID, Role: seam.RoleAssistant, Blocks: answered(sharedReply).Blocks}); err != nil {
		t.Fatal(err)
	}
	made := r.share(session, c.ID, "")
	var v share.View
	if rec := r.do("GET", api.Prefix+"shared/"+made.Token, "", ""); json.Unmarshal(rec.Body.Bytes(), &v) != nil || len(v.Entries) != 2 || v.Entries[0].Parts[0].Image != shown.ID {
		t.Fatalf("the view is %s", rec.Body)
	}
	rec := r.do("GET", api.Prefix+"shared/"+made.Token+"/images/"+shown.ID, "", "")
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), picture) || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("the image answered %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if p := rec.Header().Get("Content-Security-Policy"); p != "default-src 'none'; sandbox" {
		t.Errorf("the image is served under %q", p)
	}
	if len(rec.Header().Values("Set-Cookie")) > 0 {
		t.Error("the image set a cookie")
	}
	for name, id := range map[string]string{"an upload no message shows": unsent.ID, "an id of nothing": strings.Repeat("d", 64), "not an id": "x"} {
		if rec := r.do("GET", api.Prefix+"shared/"+made.Token+"/images/"+id, "", ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d", name, rec.Code)
		}
	}
	r.set(r.one, KeyImages, "false")
	if rec := r.do("GET", api.Prefix+"shared/"+made.Token+"/images/"+shown.ID, "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("with images off, the image answered %d", rec.Code)
	}
	v = share.View{}
	if rec := r.do("GET", api.Prefix+"shared/"+made.Token, "", ""); json.Unmarshal(rec.Body.Bytes(), &v) != nil || v.Entries[0].Parts[0].Image != "" {
		t.Errorf("with images off, the view is %s", rec.Body)
	}
}

func TestAnArtifactIsShownAsItsSource(t *testing.T) {
	r := newRig(t)
	session := r.signIn(r.one, passwordOne)
	get := func(key string) (string, bool) {
		s, ok := settings.Lookup(r.reg, key)
		return s.Default, ok
	}
	ao, err := artifacts.ReadOptions(get)
	if err != nil {
		t.Fatal(err)
	}
	c, err := chat.Create(r.one)
	if err != nil {
		t.Fatal(err)
	}
	const page = "<!doctype html><p>The ferry leaves at nine.</p><script>alert(1)</script>"
	a, v, err := artifacts.Create(bg, r.one, ao, c.ID, artifacts.Draft{Type: artifacts.KindHTML, Title: "Ferry times", Content: page}, "r")
	if err != nil {
		t.Fatal(err)
	}
	result := fmt.Sprintf("Made the artifact “Ferry times” (artifact %s, version %d, type html).", a.ID, v.Version)
	var parent int64
	for _, m := range []seam.Message{
		said(sharedWords),
		{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockToolCall, ID: "c1", Name: artifacts.CreateName, Input: json.RawMessage(`{"type":"html"}`)}}},
		{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockToolResult, ID: "c1", Content: []seam.Block{{Type: seam.BlockText, Text: result}}}}},
		answered(sharedReply),
	} {
		got, err := chat.Append(r.one, c.ID, chat.NewMessage{ParentID: parent, Role: m.Role, Blocks: m.Blocks})
		if err != nil {
			t.Fatal(err)
		}
		parent = got.ID
	}
	made := r.share(session, c.ID, "")
	var view share.View
	rec := r.do("GET", api.Prefix+"shared/"+made.Token, "", "")
	if json.Unmarshal(rec.Body.Bytes(), &view) != nil || len(view.Entries) != 2 {
		t.Fatalf("the view is %s", rec.Body)
	}
	p := view.Entries[1].Parts[0]
	if p.Kind != share.PartArtifact || p.Artifact == nil || p.Artifact.Content != page || p.Artifact.Title != "Ferry times" || p.Artifact.Type != "html" {
		t.Errorf("the artifact's part is %+v", p)
	}
	if strings.Contains(rec.Body.String(), "Made the artifact") {
		t.Error("the view holds the tool's result")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("the view is served as %q", ct)
	}
	r.set(r.one, KeyArtifacts, "false")
	view = share.View{}
	if rec := r.do("GET", api.Prefix+"shared/"+made.Token, "", ""); json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.Entries[1].Parts[0].Kind != share.PartTool || strings.Contains(rec.Body.String(), "ferry leaves at nine.</p>") {
		t.Errorf("with artifacts off, the view is %s", rec.Body)
	}
}

func TestTooManyLinks(t *testing.T) {
	r := newRig(t)
	session := r.signIn(r.one, passwordOne)
	shared := r.chatWith(r.one, "Sunday ferry", said(sharedWords), answered(sharedReply))
	// The admin sets it for a person; the person may not.
	if err := tenantsettings.Set(bg, r.one, r.reg, tenantsettings.Person, KeyMaxLinks, "100"); err == nil {
		t.Error("a person raised their own most open links")
	}
	if err := tenantsettings.Set(bg, r.one, r.reg, tenantsettings.Admin, KeyMaxLinks, "1"); err != nil {
		t.Fatal(err)
	}
	first := r.share(session, shared, "")
	if rec := r.do("POST", api.Prefix+"chats/"+shared+"/shares", session, ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"too_many"`) {
		t.Errorf("a second link past a most of one answered %d: %s", rec.Code, rec.Body)
	}
	if rec := r.do("DELETE", api.Prefix+"shares/"+first.Link.ID, session, ""); rec.Code != http.StatusOK {
		t.Fatalf("revoking answered %d", rec.Code)
	}
	r.share(session, shared, "") // a link turned off does not count
	if err := tenantsettings.Set(bg, r.one, r.reg, tenantsettings.Admin, KeyMaxLinks, "0"); err != nil {
		t.Fatal(err)
	}
	if rec := r.do("POST", api.Prefix+"chats/"+shared+"/shares", session, ""); rec.Code != http.StatusConflict {
		t.Errorf("with a most of nothing, a link answered %d", rec.Code)
	}
}
