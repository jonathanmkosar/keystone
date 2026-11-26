package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/settings"
	"github.com/jonathanmkosar/keystone/web"
)

// Artifacts (P9-03): the documents a model makes in a chat. The package
// has a doc, and serves routes.
func init() {
	componentDocs["internal/artifacts"] = "docs/architecture/artifacts.md"
	networkAllowed["internal/artifacts"] = "the artifact routes' handlers, served by the server; it opens no connection"
}

// The "artifacts" check (P9-03), the item's proof as a standing check:
// each artifact type is served in the sandbox under a policy of its own
// that fetches nothing and gives the page no origin, versions can be
// compared and restored, and the frame never has same-origin.
//
// It runs the real server on a throwaway folder, with an invented person
// and a model the check answers for. One message:
//
//  1. The model makes one artifact of each type and then changes the HTML
//     one, through the tools, in one turn that ends as done and asks for
//     no approval; every call is in the audit log before and after, under
//     the turn's request id.
//  2. For each type, the sandbox route serves the page the type is served
//     as, with the sandbox's own policy (default-src 'none', no connection,
//     no form action, framed by Keystone only, and the sandbox directive
//     without same-origin, top navigation or popups) and SAMEORIGIN.
//  3. The two versions of the HTML compare line by line; version 1 is
//     restored as version 3, with version 1's content, and version 2 stays.
//  4. Download names the file by its title and type, as an attachment.
//  5. Another person gets 404 from every artifact route.
//  6. An artifact of a chat held for sensitive content says so.
//  7. Deleting the chat removes its artifacts folder and its artifacts.
//
// Last, from the source: the frame's sandbox in the web interface never
// names same-origin, top navigation or popups, the renderers are imported
// only by the sandbox's own entries, and the main page's policy is still
// default-src 'self'.
func checkArtifacts(root string) error {
	problems, err := artifactProblems()
	if err != nil {
		return err
	}
	problems = append(problems, artifactSourceProblems(root)...)
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

var artifactModel = models.Model{
	Name: "artifact-example", Label: "Artifact Example", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8083/v1",
	ProviderModel: "artifact-1", ContextWindow: 32768, OutputLimit: 2048, Class: models.ClassLocal,
	Capabilities: []models.Capability{models.CapTools},
}

// Invented, for a throwaway folder only.
const (
	artifactPassword      = "an invented phrase for the artifacts check"
	artifactOtherPassword = "another invented phrase for the artifacts check"
	artifactPageOne       = "<!doctype html><html><body><h1>Ferry times</h1>\n<p>The first ferry leaves at nine.</p>\n</body></html>"
	artifactPageTwo       = "<!doctype html><html><body><h1>Ferry times</h1>\n<p>The first ferry leaves at ten.</p>\n</body></html>"
)

// artifactDrafts are the six the model makes, one of each type.
var artifactDrafts = []map[string]string{
	{"type": "html", "title": "Ferry times", "content": artifactPageOne},
	{"type": "react", "title": "Tide counter", "content": "import { useState } from \"react\";\nexport default function App() { const [n, setN] = useState(0); return <button onClick={() => setN(n + 1)}>{n}</button>; }"},
	{"type": "svg", "title": "Buoy", "content": `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40"><circle cx="20" cy="20" r="12" fill="orange"/></svg>`},
	{"type": "mermaid", "title": "Ferry route", "content": "graph LR; North-->South"},
	{"type": "markdown", "title": "Harbour notes", "content": "# Harbour notes\n\nThe tide turns at <b>noon</b>."},
	{"type": "code", "language": "python", "title": "Tide table", "content": "print('high water at noon')\n"},
}

// artifactEar is the model the check answers for: it makes the six, then
// changes the HTML one, then says it is done.
type artifactEar struct{}

func (artifactEar) build(models.Model, string) (seam.Provider, error) { return artifactEar{}, nil }

var artifactIDIn = regexp.MustCompile(`\(artifact ([0-9a-f]{16}), version (\d+)`)

func (artifactEar) Stream(_ context.Context, req seam.Request, emit func(seam.Event) error) error {
	reply := seam.Response{Model: req.Model, StopReason: seam.StopEnd, Usage: seam.Usage{InputTokens: 100, OutputTokens: 10},
		Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: "The artifacts are in the panel."}}}}
	results := ""
	if n := len(req.Messages); n > 0 && len(req.Tools) > 0 {
		for _, b := range req.Messages[n-1].Blocks {
			for _, c := range b.Content {
				results += c.Text + "\n"
			}
		}
		switch {
		case results == "":
			var calls []seam.Block
			for i, d := range artifactDrafts {
				input, _ := json.Marshal(d)
				calls = append(calls, seam.Block{Type: seam.BlockToolCall, ID: fmt.Sprintf("make-%d", i), Name: artifacts.CreateName, Input: input})
			}
			reply.StopReason, reply.Message.Blocks = seam.StopToolCall, calls
		case strings.Contains(results, "Made the artifact"):
			if m := artifactIDIn.FindStringSubmatch(results); m != nil {
				input, _ := json.Marshal(map[string]string{"artifact_id": m[1], "old_text": "at nine", "new_text": "at ten"})
				reply.StopReason = seam.StopToolCall
				reply.Message.Blocks = []seam.Block{{Type: seam.BlockToolCall, ID: "change-html", Name: artifacts.UpdateName, Input: input}}
			}
		}
	}
	events, err := seam.Events(reply)
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := emit(e); err != nil {
			return err
		}
	}
	return nil
}

func artifactProblems() ([]string, error) {
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "keystone-artifacts-")
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
	person, err := in.Create("Artifacts Check Person")
	if err != nil {
		return nil, err
	}
	other, err := in.Create("Artifacts Check Other Person")
	if err != nil {
		return nil, err
	}
	with := func(id string, f func(s *tenant.Store) error) error {
		s, err := in.Store(id)
		if err != nil {
			return err
		}
		defer s.Close()
		return f(s)
	}
	err = with(person.ID, func(s *tenant.Store) error {
		if err := signin.SetPassword(s, artifactPassword, o); err != nil {
			return err
		}
		lim, err := adapters.Limits(reg)
		if err != nil {
			return err
		}
		if err := models.Add(ctx, s, lim, artifactModel); err != nil {
			return err
		}
		for key, value := range map[string]string{turn.KeyTitleAuto: "false", turn.KeyFallbackModels: ""} {
			if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, key, value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := with(other.ID, func(s *tenant.Store) error { return signin.SetPassword(s, artifactOtherPassword, o) }); err != nil {
		return nil, err
	}

	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	c.Adapters = adapters.Set{models.AdapterChatCompletions: artifactEar{}.build}
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	token, rec := w.signIn(person.ID, artifactPassword)
	if token == "" {
		return nil, fmt.Errorf("the invented person cannot sign in through the API: status %d", rec.Code)
	}
	otherToken, rec := w.signIn(other.ID, artifactOtherPassword)
	if otherToken == "" {
		return nil, fmt.Errorf("the other invented person cannot sign in through the API: status %d", rec.Code)
	}
	host := func(r *http.Request) { r.Host = "127.0.0.1:7980" }
	get := func(tok, path string) *httptest.ResponseRecorder {
		return w.request("GET", api.Prefix+path, tok, "", host)
	}

	rec = w.request("POST", api.Prefix+"chats", token, "", nil)
	var made turn.ChatReply
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil {
		return nil, fmt.Errorf("the invented person cannot make a chat: status %d", rec.Code)
	}
	chatID := made.Chat.ID

	// 1. One turn makes the six and changes one.
	body, _ := json.Marshal(turn.SendBody{Text: "Make one artifact of each kind, then fix the ferry time.", Model: artifactModel.Name})
	rec = w.request("POST", api.Prefix+"chats/"+chatID+"/turns", token, string(body), nil)
	stream := rec.Body.String()
	requestID := rec.Header().Get("X-Request-Id")
	if rec.Code != http.StatusOK || !strings.Contains(stream, `"status":"done"`) {
		return nil, fmt.Errorf("the turn that makes artifacts did not end as done: status %d: %s", rec.Code, stream)
	}
	if strings.Contains(stream, "event: approval") {
		say("making an artifact asked for an approval; it changes nothing outside Keystone and runs without asking (decision 0420)")
	}
	if n := strings.Count(stream, "event: tool_result"); n != len(artifactDrafts)+1 {
		say("the turn streamed %d tool results, want %d", n, len(artifactDrafts)+1)
	}
	err = with(person.ID, func(s *tenant.Store) error {
		entries, err := audit.List(ctx, s, audit.Filter{RequestID: requestID, Kind: audit.KindTool})
		if err != nil {
			return err
		}
		count := map[string]int{}
		for _, e := range entries {
			count[e.Action+" "+string(e.Outcome)]++
		}
		if count[artifacts.CreateName+" started"] != len(artifactDrafts) || count[artifacts.CreateName+" ok"] != len(artifactDrafts) ||
			count[artifacts.UpdateName+" started"] != 1 || count[artifacts.UpdateName+" ok"] != 1 {
			say("the audit log holds the artifact tools' calls as %v, want each before and after", count)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var listed artifacts.ListReply
	if rec := get(token, "chats/"+chatID+"/artifacts"); json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Artifacts) != len(artifactDrafts) {
		return append(problems, fmt.Sprintf("the chat lists %d artifacts after the turn, want %d: %s", len(listed.Artifacts), len(artifactDrafts), rec.Body)), nil
	}
	byType := map[artifacts.Kind]artifacts.Artifact{}
	for _, a := range listed.Artifacts {
		byType[a.Type] = a
	}
	html := byType[artifacts.KindHTML]
	if html.Latest != 2 {
		say("the HTML artifact is at version %d after its change, want 2", html.Latest)
	}

	// 2. Each type in the sandbox.
	var one artifacts.OneReply
	if rec := get(token, "artifacts/"+html.ID); json.Unmarshal(rec.Body.Bytes(), &one) != nil {
		say("one artifact answered %d", rec.Code)
	}
	for _, bad := range []string{"allow-same-origin", "allow-top-navigation", "allow-popups"} {
		if strings.Contains(one.Sandbox, bad) {
			say("the frame is given %s", bad)
		}
	}
	rt := artifacts.FindRuntime(web.Files())
	for _, d := range artifactDrafts {
		kind := artifacts.Kind(d["type"])
		a := byType[kind]
		rec := get(token, "artifacts/"+a.ID+"/versions/1/sandbox?theme=dark")
		page, policy := rec.Body.String(), rec.Header().Get("Content-Security-Policy")
		if rec.Code != http.StatusOK {
			say("the %s artifact's sandbox answered %d", kind, rec.Code)
			continue
		}
		for _, want := range []string{"default-src 'none'", "connect-src 'none'", "form-action 'none'", "frame-ancestors 'self'", "; sandbox"} {
			if !strings.Contains(policy, want) {
				say("the %s artifact's sandbox policy %q has no %s", kind, policy, want)
			}
		}
		for _, bad := range []string{"allow-same-origin", "allow-top-navigation", "allow-popups", "default-src 'self'", "*"} {
			if strings.Contains(policy, bad) {
				say("the %s artifact's sandbox policy %q holds %s", kind, policy, bad)
			}
		}
		if rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
			say("the %s artifact's sandbox may be framed as %q", kind, rec.Header().Get("X-Frame-Options"))
		}
		switch kind {
		case artifacts.KindHTML:
			if page != artifactPageOne {
				say("the HTML artifact's sandbox serves %q, want the page as written", page)
			}
		case artifacts.KindSVG:
			if !strings.Contains(page, "data:image/svg+xml;base64,"+base64.StdEncoding.EncodeToString([]byte(d["content"]))) || strings.Contains(page, "<svg") {
				say("the SVG artifact is not served as an image: %q", page)
			}
		case artifacts.KindReact, artifacts.KindMermaid:
			script := rt.React
			if kind == artifacts.KindMermaid {
				script = rt.Mermaid
			}
			source, _ := json.Marshal(d["content"])
			// The renderers are built into web/dist/sandbox by npm run build;
			// a build of the Go code alone, before that, has none and says so.
			switch {
			case script == "" && !strings.Contains(page, "built without the renderer"):
				say("this build has no %s renderer, and the sandbox does not say so", kind)
			case script == "":
			case !strings.Contains(page, string(source)):
				say("the %s artifact's sandbox does not give its renderer the source", kind)
			case !strings.Contains(page, `<script>`+script+`</script>`):
				say("the %s artifact's sandbox does not hold its renderer", kind)
			case !strings.Contains(policy, "script-src 'unsafe-inline';"):
				say("the %s artifact's sandbox may load script from an address: %q", kind, policy)
			}
		default:
			if !strings.Contains(page, `<pre class="ks-text">`) || strings.Contains(page, "<b>noon") || strings.Contains(page, "<script") {
				say("the %s artifact's sandbox does not serve escaped text: %q", kind, page)
			}
		}
	}

	// 3. Compare and restore.
	var cmp artifacts.CompareReply
	if rec := get(token, "artifacts/"+html.ID+"/compare?from=1&to=2"); json.Unmarshal(rec.Body.Bytes(), &cmp) != nil || cmp.Added != 1 || cmp.Removed != 1 {
		say("versions 1 and 2 of the HTML compare as %+v, want one line out and one in", cmp)
	} else {
		for _, l := range cmp.Lines {
			if (l.Op == artifacts.OpRemove && !strings.Contains(l.Text, "at nine")) || (l.Op == artifacts.OpAdd && !strings.Contains(l.Text, "at ten")) {
				say("the comparison reads %+v", l)
			}
		}
	}
	rec = w.request("POST", api.Prefix+"artifacts/"+html.ID+"/versions/1/restore", token, "", nil)
	var restored artifacts.VersionReply
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &restored) != nil || restored.Version.Version != 3 || restored.Content != artifactPageOne || restored.Version.RestoredFrom != 1 {
		say("restoring version 1 answered %d: %s", rec.Code, rec.Body)
	}
	restoreID := rec.Header().Get("X-Request-Id")
	var v2 artifacts.VersionReply
	if rec := get(token, "artifacts/"+html.ID+"/versions/2"); json.Unmarshal(rec.Body.Bytes(), &v2) != nil || v2.Content != artifactPageTwo {
		say("version 2 reads %q after the restore", v2.Content)
	}
	err = with(person.ID, func(s *tenant.Store) error {
		entries, err := audit.List(ctx, s, audit.Filter{RequestID: restoreID})
		if err != nil {
			return err
		}
		if len(entries) < 2 {
			say("the restore is not in the audit log before and after")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 4. Download.
	rec = get(token, "artifacts/"+byType[artifacts.KindCode].ID+"/versions/1/download")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Disposition"), `attachment; filename="Tide table.py"`) || rec.Body.String() != artifactDrafts[5]["content"] {
		say("the code artifact downloads as %d %q", rec.Code, rec.Header().Get("Content-Disposition"))
	}

	// 5. Another person.
	paths := []string{"chats/" + chatID + "/artifacts", "artifacts/" + html.ID, "artifacts/" + html.ID + "/versions/1", "artifacts/" + html.ID + "/compare?from=1",
		"artifacts/" + html.ID + "/versions/1/download", "artifacts/" + html.ID + "/versions/1/sandbox"}
	for _, p := range paths {
		if rec := get(otherToken, p); rec.Code != http.StatusNotFound {
			say("another person's GET %s answered %d", p, rec.Code)
		}
	}
	if rec := w.request("POST", api.Prefix+"artifacts/"+html.ID+"/versions/1/restore", otherToken, "", nil); rec.Code != http.StatusNotFound {
		say("another person's restore answered %d", rec.Code)
	}

	// 6. A held chat.
	err = with(person.ID, func(s *tenant.Store) error {
		var turnID string
		if err := s.DB().QueryRow("SELECT id FROM turns WHERE chat_id = ? LIMIT 1", chatID).Scan(&turnID); err != nil {
			return err
		}
		_, err := s.DB().Exec("INSERT INTO turn_recall(turn_id, chat_id, sensitive) VALUES(?, ?, 1) ON CONFLICT(turn_id) DO UPDATE SET sensitive = 1", turnID, chatID)
		return err
	})
	if err != nil {
		return nil, err
	}
	one = artifacts.OneReply{}
	if rec := get(token, "artifacts/"+html.ID); json.Unmarshal(rec.Body.Bytes(), &one) != nil || !one.Artifact.Sensitive || one.Notice == "" {
		say("an artifact of a held chat reads %+v, want it marked sensitive with a notice", one)
	}

	// 7. Deleting the chat.
	if rec := w.request("DELETE", api.Prefix+"chats/"+chatID, token, "", nil); rec.Code != http.StatusOK {
		say("deleting the chat answered %d", rec.Code)
	}
	if rec := get(token, "artifacts/"+html.ID); rec.Code != http.StatusNotFound {
		say("an artifact of a deleted chat answered %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "tenants", person.ID, "artifacts", chatID)); !errors.Is(err, fs.ErrNotExist) {
		say("the deleted chat's artifacts folder is still there (%v)", err)
	}
	return problems, nil
}

// artifactSourceProblems reads the web interface's source: the frame's
// sandbox, the renderers' imports, and the main page's policy.
func artifactSourceProblems(root string) []string {
	var problems []string
	src := filepath.Join(root, "web", "app", "src")
	frame, err := os.ReadFile(filepath.Join(src, "ArtifactFrame.tsx"))
	if err != nil {
		return []string{"web/app/src/ArtifactFrame.tsx, the artifact's frame, cannot be read: " + err.Error()}
	}
	if !bytes.Contains(frame, []byte("sandbox={")) {
		problems = append(problems, "the artifact's frame in ArtifactFrame.tsx has no sandbox attribute")
	}
	allowed := regexp.MustCompile(`"(allow-[a-z-]+)"`).FindAllSubmatch(frame, -1)
	if len(allowed) == 0 {
		problems = append(problems, "ArtifactFrame.tsx names no permission it lets the frame have")
	}
	for _, m := range allowed {
		ok := false
		for _, p := range artifacts.SandboxAllowed {
			ok = ok || p == string(m[1])
		}
		if !ok {
			problems = append(problems, "ArtifactFrame.tsx lets the frame have "+string(m[1]))
		}
	}
	filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".tsx")) {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		for _, bad := range []string{"allow-same-origin", "allow-top-navigation", "allow-popups"} {
			if strings.HasPrefix(filepath.Base(p), "Artifact") && bytes.Contains(raw, []byte(bad)) {
				problems = append(problems, rel+" names "+bad)
			}
		}
		if !strings.Contains(rel, "/src/sandbox/") && regexp.MustCompile(`from "(mermaid|sucrase)`).Match(raw) {
			problems = append(problems, rel+" imports a sandbox renderer into the main page")
		}
		return nil
	})
	serve, err := os.ReadFile(filepath.Join(root, "cmd", "keystone", "serve.go"))
	if err != nil || !bytes.Contains(serve, []byte(`h.Set("Content-Security-Policy", "default-src 'self'")`)) {
		problems = append(problems, "the main page's content security policy is no longer default-src 'self' in cmd/keystone/serve.go")
	}
	return problems
}
