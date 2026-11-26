package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Invented, for throwaway folders only.
const (
	phraseOne = "artifacts person one's phrase"
	phraseTwo = "artifacts person two's phrase"
)

type routeRig struct {
	t        *testing.T
	h        http.Handler
	one, two *tenant.Store
	tokens   map[string]string
}

func newRouteRig(t *testing.T) *routeRig {
	t.Helper()
	in, a, b := stores(t)
	o, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	g := &routeRig{t: t, one: a, two: b, tokens: map[string]string{}}
	for s, phrase := range map[*tenant.Store]string{a: phraseOne, b: phraseTwo} {
		if err := signin.SetPassword(s, phrase, o); err != nil {
			t.Fatal(err)
		}
		token, _, err := signin.SignIn(s, phrase, o)
		if err != nil {
			t.Fatal(err)
		}
		g.tokens[s.TenantID()] = token
	}
	opts := testOptions()
	opts.Runtime = testRuntime
	if g.h, err = api.Handler(api.Options{Install: in, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, Routes(opts)); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *routeRig) call(who *tenant.Store, method, path string) *httptest.ResponseRecorder {
	g.t.Helper()
	r := httptest.NewRequest(method, api.Prefix+path, bytes.NewReader(nil))
	r.Host = "127.0.0.1:7980"
	r.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.tokens[who.TenantID()]})
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("the answer %d does not read: %v: %s", rec.Code, err, rec.Body)
	}
	return v
}

func TestTheRoutesListReadCompareRestoreAndDownload(t *testing.T) {
	g := newRouteRig(t)
	ctx := context.Background()
	chatID := newChat(t, g.one)
	a := mustCreate(t, g.one, chatID, Draft{Type: KindHTML, Title: "Harbour: tides", Content: pageOne})
	if _, _, err := Update(ctx, g.one, testOptions(), chatID, a.ID, Change{Content: text(pageTwo)}, "r"); err != nil {
		t.Fatal(err)
	}

	listed := decode[ListReply](t, g.call(g.one, "GET", "chats/"+chatID+"/artifacts"))
	if len(listed.Artifacts) != 1 || listed.Artifacts[0].ID != a.ID || listed.Artifacts[0].Latest != 2 {
		t.Fatalf("the chat's list is %+v", listed)
	}
	if all := decode[ListReply](t, g.call(g.one, "GET", "artifacts")); len(all.Artifacts) != 1 {
		t.Errorf("every artifact lists %+v", all)
	}
	one := decode[OneReply](t, g.call(g.one, "GET", "artifacts/"+a.ID))
	if len(one.Versions) != 2 || one.Sandbox != "allow-scripts allow-forms allow-modals" || one.Notice != "" || one.Artifact.Link != "/artifacts/"+a.ID {
		t.Errorf("one artifact reads %+v", one)
	}
	v1 := decode[VersionReply](t, g.call(g.one, "GET", "artifacts/"+a.ID+"/versions/1"))
	if v1.Content != pageOne || v1.Version.Version != 1 {
		t.Errorf("version 1 reads %+v", v1)
	}

	// Compare.
	cmp := decode[CompareReply](t, g.call(g.one, "GET", "artifacts/"+a.ID+"/compare?from=1&to=2"))
	if cmp.Added != 1 || cmp.Removed != 1 || cmp.From != 1 || cmp.To != 2 {
		t.Errorf("the comparison is %+v", cmp)
	}
	if latest := decode[CompareReply](t, g.call(g.one, "GET", "artifacts/"+a.ID+"/compare?from=1")); latest.To != 2 {
		t.Errorf("a comparison with no to compares with %d, want the newest", latest.To)
	}
	if rec := g.call(g.one, "GET", "artifacts/"+a.ID+"/compare?from=x"); rec.Code != http.StatusBadRequest {
		t.Errorf("a comparison from no version answered %d", rec.Code)
	}

	// Restore.
	rec := g.call(g.one, "POST", "artifacts/"+a.ID+"/versions/1/restore")
	restored := decode[VersionReply](t, rec)
	if rec.Code != http.StatusCreated || restored.Version.Version != 3 || restored.Version.RestoredFrom != 1 || restored.Content != pageOne || restored.Version.RequestID == "" {
		t.Errorf("restoring version 1 answered %d: %+v", rec.Code, restored)
	}
	if rec := g.call(g.one, "POST", "artifacts/"+a.ID+"/versions/3/restore"); rec.Code != http.StatusBadRequest {
		t.Errorf("restoring the newest version answered %d", rec.Code)
	}

	// Download, with its name and type, never drawn.
	rec = g.call(g.one, "GET", "artifacts/"+a.ID+"/versions/2/download")
	_, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition"))
	if rec.Code != http.StatusOK || rec.Body.String() != pageTwo || err != nil || params["filename"] != "Harbour- tides (version 2).html" {
		t.Errorf("the download answered %d, %q, disposition %q", rec.Code, rec.Body, rec.Header().Get("Content-Disposition"))
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("the download's type is %q", got)
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") || rec.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Errorf("the download could be drawn: %v", rec.Header())
	}
	rec = g.call(g.one, "GET", "artifacts/"+a.ID+"/versions/3/download")
	if _, params, _ := mime.ParseMediaType(rec.Header().Get("Content-Disposition")); params["filename"] != "Harbour- tides.html" {
		t.Errorf("the newest version downloads as %q", params["filename"])
	}
}

// The item's sandbox proof at the route: the page carries the sandbox's
// own policy, not the main page's, and only Keystone may frame it.
func TestTheSandboxRouteCarriesItsOwnPolicy(t *testing.T) {
	g := newRouteRig(t)
	chatID := newChat(t, g.one)
	for _, d := range []Draft{
		{Type: KindHTML, Title: "Page", Content: pageOne},
		{Type: KindReact, Title: "Counter", Content: "export default function App() { return <p>1</p> }"},
		{Type: KindSVG, Title: "Buoy", Content: `<svg xmlns="http://www.w3.org/2000/svg"/>`},
		{Type: KindMermaid, Title: "Route", Content: "graph TD; A-->B"},
		{Type: KindMarkdown, Title: "Notes", Content: "# Notes"},
		{Type: KindCode, Language: "go", Title: "Main", Content: "package main"},
	} {
		a := mustCreate(t, g.one, chatID, d)
		rec := g.call(g.one, "GET", "artifacts/"+a.ID+"/versions/1/sandbox?theme=dark")
		h := rec.Header()
		if rec.Code != http.StatusOK || h.Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("%s: the sandbox answered %d %q", d.Type, rec.Code, h.Get("Content-Type"))
			continue
		}
		if got, want := h.Get("Content-Security-Policy"), Policy(d.Type, "127.0.0.1:7980", "allow-scripts allow-forms allow-modals"); got != want {
			t.Errorf("%s: the sandbox's policy is %q, want %q", d.Type, got, want)
		}
		if h.Get("X-Frame-Options") != "SAMEORIGIN" || h.Get("Cache-Control") != "private, no-cache" || h.Get("ETag") == "" {
			t.Errorf("%s: the sandbox's headers are %v", d.Type, h)
		}
		if !bytes.Equal(rec.Body.Bytes(), SandboxPage(d.Type, d.Title, d.Content, "dark", FindRuntime(testRuntime))) {
			t.Errorf("%s: the sandbox serves %q", d.Type, rec.Body)
		}
		// Asked again with its tag, the page is not sent again; in the other
		// theme it is.
		r := httptest.NewRequest("GET", api.Prefix+"artifacts/"+a.ID+"/versions/1/sandbox?theme=dark", nil)
		r.Host = "127.0.0.1:7980"
		r.Header.Set("If-None-Match", h.Get("ETag"))
		r.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.tokens[g.one.TenantID()]})
		again := httptest.NewRecorder()
		g.h.ServeHTTP(again, r)
		if again.Code != http.StatusNotModified || again.Body.Len() != 0 || again.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: asked again with its tag, the sandbox answered %d with %d bytes", d.Type, again.Code, again.Body.Len())
		}
		if light := g.call(g.one, "GET", "artifacts/"+a.ID+"/versions/1/sandbox?theme=light"); light.Header().Get("ETag") == h.Get("ETag") {
			t.Errorf("%s: the light page has the dark page's tag", d.Type)
		}
	}
}

func TestAnotherPersonGetsNothingFromAnyRoute(t *testing.T) {
	g := newRouteRig(t)
	chatID := newChat(t, g.one)
	a := mustCreate(t, g.one, chatID, Draft{Type: KindHTML, Title: "Page", Content: pageOne})
	for _, c := range []struct{ method, path string }{
		{"GET", "chats/" + chatID + "/artifacts"},
		{"GET", "artifacts/" + a.ID},
		{"GET", "artifacts/" + a.ID + "/versions/1"},
		{"GET", "artifacts/" + a.ID + "/compare?from=1"},
		{"POST", "artifacts/" + a.ID + "/versions/1/restore"},
		{"GET", "artifacts/" + a.ID + "/versions/1/download"},
		{"GET", "artifacts/" + a.ID + "/versions/1/sandbox"},
	} {
		if rec := g.call(g.two, c.method, c.path); rec.Code != http.StatusNotFound {
			t.Errorf("another person's %s %s answered %d: %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	if all := decode[ListReply](t, g.call(g.two, "GET", "artifacts")); len(all.Artifacts) != 0 {
		t.Errorf("another person lists %+v", all)
	}
	for _, path := range []string{"artifacts/..%2f..%2fkeystone.db", "artifacts/" + a.ID + "/versions/0", "artifacts/" + a.ID + "/versions/-1/sandbox", "artifacts/" + a.ID + "/versions/v1"} {
		if rec := g.call(g.one, "GET", path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s answered %d", path, rec.Code)
		}
	}
	// With no session, nothing at all.
	r := httptest.NewRequest("GET", api.Prefix+"artifacts/"+a.ID+"/versions/1/sandbox", nil)
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("the sandbox with no session answered %d", rec.Code)
	}
}

func TestASensitiveArtifactSaysSo(t *testing.T) {
	g := newRouteRig(t)
	chatID := newChat(t, g.one)
	a := mustCreate(t, g.one, chatID, Draft{Type: KindMarkdown, Title: "Notes", Content: "x"})
	if _, err := g.one.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES('turn-held', ?, 'done', 'req', 'person', '{}', '2026-11-25T10:00:00.000Z')", chatID); err != nil {
		t.Fatal(err)
	}
	if _, err := g.one.DB().Exec("INSERT INTO turn_recall(turn_id, chat_id, sensitive) VALUES('turn-held', ?, 1)", chatID); err != nil {
		t.Fatal(err)
	}
	one := decode[OneReply](t, g.call(g.one, "GET", "artifacts/"+a.ID))
	if !one.Artifact.Sensitive || one.Notice != SensitiveNotice {
		t.Errorf("an artifact of a held chat reads %+v", one)
	}
}
