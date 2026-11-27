package visuals

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Invented, for throwaway folders only.
const (
	phraseOne = "visuals person one's phrase"
	phraseTwo = "visuals person two's phrase"
	host      = "127.0.0.1:7980"
)

// A renderer as a build has it: it knows the channel.
const renderer = `/* an invented renderer */ const channel = "keystone-diagram"; if (1 < 2) { /* x */ }`

func runtime(mermaid string) artifacts.Runtime {
	return artifacts.FindRuntime(fstest.MapFS{"sandbox/mermaid.js": {Data: []byte(mermaid)}})
}

func directive(policy, name string) string {
	for _, part := range strings.Split(policy, ";") {
		part = strings.TrimSpace(part)
		if part == name || strings.HasPrefix(part, name+" ") {
			return part
		}
	}
	return ""
}

func TestThePageHoldsTheRendererAndNoDiagram(t *testing.T) {
	rt := runtime(renderer)
	for _, theme := range []string{"dark", "light"} {
		page, script := DiagramPage(theme, rt)
		p := string(page)
		if !strings.Contains(p, `<html lang="en" data-theme="`+theme+`">`) {
			t.Errorf("%s: the page does not carry its theme", theme)
		}
		if !strings.Contains(p, `<body data-ks-inline="1"><div id="ks-root"></div><script>`+script+`</script></body>`) {
			t.Errorf("%s: the page does not hold the renderer as its one script: %s", theme, p)
		}
		if strings.Count(p, "<script") != 1 {
			t.Errorf("%s: the page has %d script elements, want the renderer alone", theme, strings.Count(p, "<script"))
		}
		if script != rt.Mermaid || script == "" {
			t.Errorf("%s: the script is not the build's renderer", theme)
		}
		if strings.Contains(p, "ks-artifact") {
			t.Errorf("%s: the page holds an artifact; it is the same for every diagram and holds none", theme)
		}
		if !strings.Contains(p, "background:transparent") {
			t.Errorf("%s: the page is not drawn on the reply's own ground", theme)
		}
	}
	// Anything but dark is light.
	if page, _ := DiagramPage(`"><script>alert(1)</script>`, rt); !strings.Contains(string(page), `data-theme="light"`) || strings.Contains(string(page), "alert(1)") {
		t.Error("a theme that is neither dark nor light reached the page")
	}
}

func TestAPolicyThatRunsTheOneScriptAndFetchesNothing(t *testing.T) {
	_, script := DiagramPage("dark", runtime(renderer))
	p := Policy(host, script)
	sum := sha256.Sum256([]byte(script))
	if got, want := directive(p, "script-src"), "script-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'"; got != want {
		t.Errorf("script-src is %q, want the page's one script by its digest, %q", got, want)
	}
	if strings.Contains(directive(p, "script-src"), "unsafe") {
		t.Errorf("the policy %q lets a script run that is not the renderer", p)
	}
	for _, want := range []string{"default-src 'none'", "connect-src 'none'", "form-action 'none'", "base-uri 'none'", "frame-src 'none'", "worker-src 'none'"} {
		if directive(p, strings.Fields(want)[0]) != want {
			t.Errorf("the policy %q has no %q", p, want)
		}
	}
	if got := directive(p, "sandbox"); got != "sandbox allow-scripts" {
		t.Errorf("the sandbox directive is %q, want scripts and nothing else", got)
	}
	for _, out := range []string{"allow-same-origin", "allow-top-navigation", "allow-popups", "allow-forms", "allow-modals", "allow-downloads"} {
		if strings.Contains(p, out) {
			t.Errorf("the policy %q gives the frame %s", p, out)
		}
	}
	if got := directive(p, "frame-ancestors"); got != "frame-ancestors 'self' http://"+host+" https://"+host {
		t.Errorf("frame-ancestors is %q", got)
	}
	for _, d := range []string{"img-src", "font-src", "media-src", "style-src"} {
		if strings.Contains(directive(p, d), "http") || strings.Contains(directive(p, d), "*") {
			t.Errorf("%s reaches a network address: %q", d, directive(p, d))
		}
	}
	// A crafted Host does not reach the policy.
	if bad := Policy("evil.example; script-src *", script); strings.Contains(bad, "evil") || strings.Contains(bad, "script-src *") {
		t.Errorf("a crafted Host reached the policy: %q", bad)
	}
	// With no script, none runs.
	if got := directive(Policy(host, ""), "script-src"); got != "script-src 'none'" {
		t.Errorf("with no script the policy says %q", got)
	}
}

func TestABuildThatCannotDrawSaysSo(t *testing.T) {
	for name, rt := range map[string]artifacts.Runtime{
		"no renderer":                      artifacts.FindRuntime(fstest.MapFS{}),
		"a renderer from before the reply": runtime("/* an invented renderer that reads an artifact only */"),
	} {
		if CanDraw(rt) {
			t.Errorf("%s: it is said to draw a reply's diagram", name)
		}
		page, script := DiagramPage("dark", rt)
		if script != "" || strings.Contains(string(page), "<script") {
			t.Errorf("%s: the page holds a script", name)
		}
		if !strings.Contains(string(page), "built without the renderer") {
			t.Errorf("%s: the page does not say why nothing is drawn", name)
		}
	}
	if !CanDraw(runtime(renderer)) {
		t.Error("a renderer that knows the channel is said not to draw")
	}
}

type rig struct {
	t        *testing.T
	h        http.Handler
	one, two *tenant.Store
	tokens   map[string]string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	o, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	g := &rig{t: t, tokens: map[string]string{}}
	for i, name := range []string{"Test Person One", "Test Person Two"} {
		made, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		phrase := []string{phraseOne, phraseTwo}[i]
		if err := signin.SetPassword(s, phrase, o); err != nil {
			t.Fatal(err)
		}
		token, _, err := signin.SignIn(s, phrase, o)
		if err != nil {
			t.Fatal(err)
		}
		g.tokens[s.TenantID()] = token
		if i == 0 {
			g.one = s
		} else {
			g.two = s
		}
	}
	opts := Options{Artifacts: artifacts.Options{MaxBytes: 4 << 10, TitleMaxChars: 60, Sandbox: []string{"allow-scripts"},
		Runtime: fstest.MapFS{"sandbox/mermaid.js": {Data: []byte(renderer)}}}}
	if g.h, err = api.Handler(api.Options{Install: in, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, Routes(opts)); err != nil {
		t.Fatal(err)
	}
	return g
}

func (g *rig) call(who *tenant.Store, method, path, body string, extra func(*http.Request)) *httptest.ResponseRecorder {
	g.t.Helper()
	r := httptest.NewRequest(method, api.Prefix+path, bytes.NewReader([]byte(body)))
	r.Host = host
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if who != nil {
		r.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.tokens[who.TenantID()]})
	}
	if extra != nil {
		extra(r)
	}
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r)
	return rec
}

func TestTheFrameRoute(t *testing.T) {
	g := newRig(t)
	rec := g.call(g.one, "GET", "visuals/diagram?theme=dark", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the frame's page answered %d: %s", rec.Code, rec.Body)
	}
	page, script := DiagramPage("dark", runtime(renderer))
	if !bytes.Equal(rec.Body.Bytes(), page) {
		t.Error("the route does not serve the diagram page")
	}
	head := rec.Header()
	if got := head.Get("Content-Security-Policy"); got != Policy(host, script) {
		t.Errorf("the page's policy is %q", got)
	}
	if head.Get("X-Frame-Options") != "SAMEORIGIN" || head.Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Errorf("the page may be framed or read from elsewhere: %v", head)
	}
	if !strings.HasPrefix(head.Get("Content-Type"), "text/html") || head.Get("Cache-Control") != "private, no-cache" {
		t.Errorf("the page's type or cache is wrong: %v", head)
	}
	etag := head.Get("ETag")
	if etag == "" {
		t.Fatal("the page has no ETag, so megabytes of renderer are sent with every diagram")
	}
	again := g.call(g.one, "GET", "visuals/diagram?theme=dark", "", func(r *http.Request) { r.Header.Set("If-None-Match", etag) })
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Errorf("asked again with its ETag, the page answered %d with %d bytes", again.Code, again.Body.Len())
	}
	if again.Header().Get("Content-Security-Policy") == "" {
		t.Error("a 304 carries no policy")
	}
	light := g.call(g.one, "GET", "visuals/diagram?theme=light", "", nil)
	if light.Header().Get("ETag") == etag || !strings.Contains(light.Body.String(), `data-theme="light"`) {
		t.Error("the light page is not its own")
	}
	// Signed in or nothing: it is not a door.
	if rec := g.call(nil, "GET", "visuals/diagram", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("with no session the frame's page answered %d", rec.Code)
	}
}

func TestSavingADiagramMakesAMermaidArtifactOfItsChat(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()
	c, err := chat.Create(g.one)
	if err != nil {
		t.Fatal(err)
	}
	source := "flowchart LR\n  A[Arrive] --> B[Sign the sheet]\n"
	body, _ := json.Marshal(SaveBody{Title: "  How a hire goes ", Content: source})
	rec := g.call(g.one, "POST", "chats/"+c.ID+"/visuals/diagrams", string(body), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving answered %d: %s", rec.Code, rec.Body)
	}
	var made SaveReply
	if err := json.Unmarshal(rec.Body.Bytes(), &made); err != nil {
		t.Fatal(err)
	}
	if made.Artifact.Type != artifacts.KindMermaid || made.Artifact.Title != "How a hire goes" || made.Artifact.ChatID != c.ID || made.Version.Version != 1 || made.Artifact.Sensitive {
		t.Errorf("the saved artifact is %+v, version %+v", made.Artifact, made.Version)
	}
	_, content, err := artifacts.Content(ctx, g.one, made.Artifact.ID, 1)
	if err != nil || content != source {
		t.Errorf("the saved content is %q (%v)", content, err)
	}
	if made.Version.RequestID == "" {
		t.Error("the version does not name the request that saved it")
	}

	// No title: the default.
	body, _ = json.Marshal(SaveBody{Content: source})
	rec = g.call(g.one, "POST", "chats/"+c.ID+"/visuals/diagrams", string(body), nil)
	var plain SaveReply
	if json.Unmarshal(rec.Body.Bytes(), &plain); rec.Code != http.StatusCreated || plain.Artifact.Title != DefaultTitle {
		t.Errorf("with no title: %d, %+v", rec.Code, plain.Artifact)
	}

	// Refusals: nothing to keep, too large, a field the route does not take.
	for name, b := range map[string]string{
		"empty":     `{"title":"t","content":""}`,
		"too large": `{"title":"t","content":"` + strings.Repeat("x", 5<<10) + `"}`,
		"a type":    `{"title":"t","content":"graph LR","type":"html"}`,
	} {
		if rec := g.call(g.one, "POST", "chats/"+c.ID+"/visuals/diagrams", b, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: saving answered %d: %s", name, rec.Code, rec.Body)
		}
	}
	if list, err := artifacts.List(ctx, g.one, c.ID); err != nil || len(list) != 2 {
		t.Errorf("the chat has %d artifacts after two saves and three refusals (%v)", len(list), err)
	}

	// Fixed rule 1: another person cannot save into this chat, and nothing
	// is written to either store.
	body, _ = json.Marshal(SaveBody{Title: "Theirs", Content: source})
	if rec := g.call(g.two, "POST", "chats/"+c.ID+"/visuals/diagrams", string(body), nil); rec.Code != http.StatusNotFound {
		t.Errorf("another person saving into the chat answered %d", rec.Code)
	}
	if all, err := artifacts.ListAll(ctx, g.two); err != nil || len(all) != 0 {
		t.Errorf("the other person has %d artifacts (%v)", len(all), err)
	}
	if rec := g.call(nil, "POST", "chats/"+c.ID+"/visuals/diagrams", string(body), nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("with no session saving answered %d", rec.Code)
	}
}

func TestADiagramSavedFromAHeldChatIsSensitive(t *testing.T) {
	g := newRig(t)
	c, err := chat.Create(g.one)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.one.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES('turn-held', ?, 'done', 'req', 'person', '{}', '2026-11-26T10:00:00.000Z')", c.ID); err != nil {
		t.Fatal(err)
	}
	if err := recall.Hold(context.Background(), g.one, "turn-held", c.ID); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(SaveBody{Title: "Held", Content: "graph LR\n A-->B"})
	rec := g.call(g.one, "POST", "chats/"+c.ID+"/visuals/diagrams", string(body), nil)
	var made SaveReply
	if json.Unmarshal(rec.Body.Bytes(), &made); rec.Code != http.StatusCreated || !made.Artifact.Sensitive || !made.Version.Sensitive {
		t.Errorf("a diagram saved from a held chat: %d, %+v", rec.Code, made)
	}
}
