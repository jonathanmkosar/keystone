package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing/fstest"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/visuals"
	"github.com/jonathanmkosar/keystone/settings"
)

// Inline visuals (P9-04): a chart and a diagram drawn in a reply. The
// package has a doc and serves routes; the chart format and the page's
// part are files of the web interface with the same doc.
func init() {
	componentDocs["internal/visuals"] = "docs/architecture/inline-visuals.md"
	componentDocs["web/app/src/chart.ts"] = "docs/architecture/inline-visuals.md"
	componentDocs["web/app/src/visuals.ts"] = "docs/architecture/inline-visuals.md"
	componentDocs["web/app/src/InlineVisuals.tsx"] = "docs/architecture/inline-visuals.md"
	networkAllowed["internal/visuals"] = "the inline visuals' two routes' handlers, served by the server; it opens no connection"
}

// The "inline visuals" check (P9-04), the item's proof as a standing
// check: a reply's chart is drawn by the app from checked numbers, its
// diagram in a frame of no origin, and nothing a model writes runs in the
// app's own page.
//
// From the source:
//
//  1. The main page's policy is still default-src 'self'.
//  2. The chart format and the block rules import nothing, and the screen's
//     part never sets markup from text, never evaluates text, and never
//     imports Mermaid.
//  3. The diagram's frame may run scripts and nothing else, and the frame's
//     renderer draws at Mermaid's strict level and hears only the page that
//     frames it.
//  4. Only the conversation draws visuals: no other screen, a shared page
//     above all, names the drawer.
//  5. No chart library is a dependency, and the settings are in the
//     registry.
//
// With Node on this computer, the tests of the chart format and of what
// the page builds run (decision 0501).
//
// On the real server, on a throwaway folder, with an invented person and a
// model that plays the test tenant's recorded turns:
//
//  6. The recorded turns answer with a chart block and with a diagram block.
//  7. The standing start of the call holds the standing text and then the
//     instructions on the two formats, the same in two calls of one chat;
//     with visuals.draw off they are not sent.
//  8. The diagram's page is served only to someone signed in, with a policy
//     that fetches and sends nothing, runs the page's one script by its
//     digest, gives the page no origin, and lets only Keystone frame it.
//  9. Saving a diagram makes a Mermaid artifact of the chat; another person
//     cannot save into it.
func checkInlineVisuals(root string) error {
	problems := visualsSourceProblems(root)
	problems = append(problems, visualsSettingsProblems()...)
	problems = append(problems, visualsTestProblems(root)...)
	served, err := visualsServedProblems()
	if err != nil {
		return err
	}
	problems = append(problems, served...)
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// visualsPure are the files that must import nothing: Node runs them as
// they are, and they can reach nothing but what they are given.
var visualsPure = []string{"chart.ts", "visuals.ts"}

// visualsNever are what the screen's part must never name: a way to turn
// text into markup or script in the app's own page.
var visualsNever = []string{"dangerouslySetInnerHTML", "innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "srcDoc", "srcdoc", "allow-same-origin", "allow-top-navigation", "allow-popups", "allow-forms", "allow-modals"}

// visualsChartLibraries are chart libraries by name. Charts are drawn by
// the app's own code (decision 0541); one of these needs a decision record
// and a change here.
var visualsChartLibraries = []string{"chart.js", "d3", "recharts", "echarts", "plotly.js", "vega", "vega-lite", "apexcharts", "highcharts", "victory", "@nivo/core", "@visx/visx", "uplot", "billboard.js", "c3"}

func visualsSourceProblems(root string) []string {
	var problems []string
	src := filepath.Join(root, "web", "app", "src")
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(name)))
		if err != nil {
			problems = append(problems, "web/app/src/"+name+", part of inline visuals, cannot be read: "+err.Error())
			return ""
		}
		return string(raw)
	}
	serve, err := os.ReadFile(filepath.Join(root, "cmd", "keystone", "serve.go"))
	if err != nil || !bytes.Contains(serve, []byte(`h.Set("Content-Security-Policy", "default-src 'self'")`)) {
		problems = append(problems, "the main page's content security policy is no longer default-src 'self' in cmd/keystone/serve.go")
	}
	imports := regexp.MustCompile(`(?m)^\s*import\s`)
	for _, name := range visualsPure {
		if imports.MatchString(read(name)) {
			problems = append(problems, "web/app/src/"+name+" imports something; the chart format and the block rules are given all they use")
		}
	}
	screen := read("InlineVisuals.tsx")
	for _, bad := range visualsNever {
		if strings.Contains(screen, bad) {
			problems = append(problems, "web/app/src/InlineVisuals.tsx names "+bad+": nothing a model writes may become markup or script in the app's own page, and the diagram's frame may only run its script")
		}
	}
	if regexp.MustCompile(`from "(mermaid|sucrase)`).MatchString(screen) {
		problems = append(problems, "web/app/src/InlineVisuals.tsx imports a sandbox renderer into the main page")
	}
	if !strings.Contains(screen, `const frameSandbox = "`+visuals.FramePermissions+`";`) || !strings.Contains(screen, "sandbox={frameSandbox}") {
		problems = append(problems, "the diagram's frame in InlineVisuals.tsx is not sandboxed with "+visuals.FramePermissions+" and nothing else")
	}
	if strings.Count(screen, "<iframe") != 1 {
		problems = append(problems, "InlineVisuals.tsx draws more or fewer than its one frame")
	}
	if !strings.Contains(screen, "e.source !== frame.current.contentWindow") || !strings.Contains(screen, "readFrameMessage(e.data)") {
		problems = append(problems, "InlineVisuals.tsx no longer hears only its own frame, through readFrameMessage")
	}
	rules := read("visuals.ts")
	if !strings.Contains(rules, `export const diagramChannel = "`+visuals.Channel+`";`) {
		problems = append(problems, "web/app/src/visuals.ts and internal/visuals name the frame's channel differently")
	}
	renderer := read("sandbox/mermaid.ts")
	if n := strings.Count(renderer, "mermaid.initialize("); n == 0 || n != strings.Count(renderer, `securityLevel: "strict"`) {
		problems = append(problems, "web/app/src/sandbox/mermaid.ts draws a diagram without Mermaid's strict security level")
	}
	if !strings.Contains(renderer, "if (e.source !== window.parent) return;") || !strings.Contains(renderer, "readDrawMessage(e.data)") {
		problems = append(problems, "web/app/src/sandbox/mermaid.ts no longer hears only the page that frames it, through readDrawMessage")
	}
	// Only the conversation draws visuals.
	filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".tsx")) {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		name := filepath.Base(p)
		if name != "InlineVisuals.tsx" && name != "Markdown.tsx" && bytes.Contains(raw, []byte("FenceDrawer")) {
			problems = append(problems, "web/app/src/"+name+" names FenceDrawer: only InlineVisuals.tsx may draw a fenced block as anything but code")
		}
		if name != "InlineVisuals.tsx" && name != "Conversation.tsx" && (bytes.Contains(raw, []byte("<InlineVisuals")) || bytes.Contains(raw, []byte(`from "./InlineVisuals"`))) {
			problems = append(problems, "web/app/src/"+name+" draws with InlineVisuals: only the conversation draws a reply's charts and diagrams; a shared page shows them as text")
		}
		return nil
	})
	if c := read("Conversation.tsx"); !strings.Contains(c, "<InlineVisuals streaming={") {
		problems = append(problems, "Conversation.tsx no longer draws a reply's visuals")
	}
	if m := read("Markdown.tsx"); strings.Contains(m, "FenceDrawer.Provider") {
		problems = append(problems, "Markdown.tsx gives a drawer itself, so every screen that shows Markdown would draw visuals")
	}
	// No chart library.
	raw, err := os.ReadFile(filepath.Join(root, "web", "app", "package.json"))
	if err != nil {
		return append(problems, "web/app/package.json cannot be read: "+err.Error())
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return append(problems, "web/app/package.json does not read: "+err.Error())
	}
	for _, lib := range visualsChartLibraries {
		_, dep := pkg.Dependencies[lib]
		_, dev := pkg.DevDependencies[lib]
		if dep || dev {
			problems = append(problems, "web/app/package.json depends on "+lib+": charts are drawn by the app's own code (decision 0541)")
		}
	}
	return problems
}

func visualsSettingsProblems() []string {
	reg, err := settings.Registry()
	if err != nil {
		return []string{err.Error()}
	}
	want := map[string]string{
		"visuals.draw": "bool", "visuals.instructions": "string", "visuals.max_block_bytes": "int",
		"visuals.chart_max_series": "int", "visuals.chart_max_points": "int", "visuals.chart_label_max_chars": "int",
		"visuals.chart_height": "int", "visuals.diagram_height": "int", "visuals.diagram_wait": "duration",
	}
	var problems []string
	for key, typ := range want {
		s, ok := settings.Lookup(reg, key)
		if !ok {
			problems = append(problems, key+" is not in the settings registry; the page draws no visual without it")
			continue
		}
		if s.Type != typ {
			problems = append(problems, fmt.Sprintf("%s is a %s, and the page reads it as a %s", key, s.Type, typ))
		}
	}
	return problems
}

// visualsTestProblems runs the chart format's and the page's tests with
// Node's own test runner. Without Node it runs nothing, as the dictation
// check does (decision 0501).
func visualsTestProblems(root string) []string {
	node, err := exec.LookPath("node")
	if err != nil {
		return nil
	}
	appDir := filepath.Join(root, "web", "app")
	ver, err := project.Run(appDir, node, "--version")
	if err != nil {
		return []string{"node --version failed: " + ver}
	}
	if !nodeReadsTypeScript(ver) {
		return []string{"node " + ver + " cannot run the inline visuals' tests, which need Node 22.6 or later; update Node"}
	}
	out, err := project.Run(appDir, node, "--experimental-strip-types", "--test", filepath.Join("test", "chart.test.ts"), filepath.Join("test", "visuals.test.ts"))
	if err != nil {
		return []string{"the inline visuals' tests failed (npm test in web/app):\n" + out}
	}
	if !strings.Contains(out, "fail 0") {
		return []string{"the inline visuals' tests did not report their result:\n" + out}
	}
	return nil
}

// Invented, for a throwaway folder only.
const (
	visualsPassword      = "an invented phrase for the visuals check"
	visualsOtherPassword = "another invented phrase for the visuals check"
	visualsChartAsk      = "Chart the kayak hires at the boathouse for the season."
	visualsDiagramAsk    = "Draw how a kayak hire goes, from arriving to handing the paddle back."
	// A renderer as a build has it: it knows the frame's channel.
	visualsRenderer = `/* an invented renderer */ const channel = "` + visuals.Channel + `";`
)

var visualsModel = models.Model{
	Name: "visuals-local", Label: "Visuals Local", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8084/v1",
	ProviderModel: "small-1", ContextWindow: 16384, OutputLimit: 2048, Class: models.ClassLocal,
}

func policyPart(policy, name string) string {
	for _, part := range strings.Split(policy, ";") {
		part = strings.TrimSpace(part)
		if part == name || strings.HasPrefix(part, name+" ") {
			return part
		}
	}
	return ""
}

func visualsServedProblems() ([]string, error) {
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	ctx := context.Background()

	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	rules, _ := settings.Lookup(reg, contextbuild.KeySystemText)
	how, _ := settings.Lookup(reg, turn.KeyVisualsInstructions)
	fx, err := testtenant.Shipped()
	if err != nil {
		return nil, err
	}
	p, err := fake.New(fx.Scripts, fake.Options{})
	if err != nil {
		return nil, err
	}
	ears := &styleEars{p: p}

	dir, err := os.MkdirTemp("", "keystone-visuals-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	o, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	person, err := in.Create("Visuals Check Person")
	if err != nil {
		return nil, err
	}
	other, err := in.Create("Visuals Check Other Person")
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
	set := func(key, value string) error {
		return with(person.ID, func(s *tenant.Store) error {
			return tenantsettings.Set(ctx, s, reg, tenantsettings.Person, key, value)
		})
	}
	err = with(person.ID, func(s *tenant.Store) error {
		if err := signin.SetPassword(s, visualsPassword, o); err != nil {
			return err
		}
		lim, err := adapters.Limits(reg)
		if err != nil {
			return err
		}
		if err := models.Add(ctx, s, lim, visualsModel); err != nil {
			return err
		}
		for key, value := range map[string]string{turn.KeyTitleAuto: "false", turn.KeyRecap: "false", memory.SettingOn: "false"} {
			if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, key, value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := with(other.ID, func(s *tenant.Store) error { return signin.SetPassword(s, visualsOtherPassword, o) }); err != nil {
		return nil, err
	}

	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	c.Adapters = adapters.Set{models.AdapterChatCompletions: ears.build}
	// The page is checked with a stand-in renderer, so the check does not
	// rest on which build of the web interface is compiled in.
	c.Artifacts.Runtime = fstest.MapFS{artifacts.RuntimeFiles[artifacts.KindMermaid]: {Data: []byte(visualsRenderer)}}
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	token, rec := w.signIn(person.ID, visualsPassword)
	if token == "" {
		return nil, fmt.Errorf("the invented person cannot sign in through the API: status %d", rec.Code)
	}
	otherToken, rec := w.signIn(other.ID, visualsOtherPassword)
	if otherToken == "" {
		return nil, fmt.Errorf("the other invented person cannot sign in through the API: status %d", rec.Code)
	}
	newChat := func() (string, error) {
		rec := w.request("POST", api.Prefix+"chats", token, "", nil)
		var made turn.ChatReply
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil {
			return "", fmt.Errorf("making a chat answered %d: %s", rec.Code, rec.Body.String())
		}
		return made.Chat.ID, nil
	}
	// send sends a message and returns the reply's text and the standing
	// start of the request the model was sent.
	send := func(chatID, text string) (string, []string, error) {
		body, _ := json.Marshal(turn.SendBody{Text: text})
		rec := w.request("POST", api.Prefix+"chats/"+chatID+"/turns", token, string(body), nil)
		stream := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(stream, "event: "+turn.EventEnd+"\n") {
			return "", nil, fmt.Errorf("a turn did not stream to its end: status %d", rec.Code)
		}
		var reply strings.Builder
		for _, e := range readStream(stream) {
			if e.name == turn.EventText {
				var d turn.DeltaEvent
				if json.Unmarshal([]byte(e.data), &d) == nil {
					reply.WriteString(d.Text)
				}
			}
		}
		req, ok := ears.last()
		if !ok || req.Cache == nil {
			return "", nil, errors.New("the model was sent nothing, or a call with no cache points")
		}
		var start []string
		for _, b := range req.System[:req.Cache.Start] {
			start = append(start, b.Text)
		}
		return reply.String(), start, nil
	}

	// 6 and 7. The recorded turns, and what the model is told.
	chatID, err := newChat()
	if err != nil {
		return nil, err
	}
	reply, first, err := send(chatID, visualsChartAsk)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(reply, "```chart\n{") || !strings.Contains(reply, "\n```") {
		say("the test tenant's recorded turn for %q holds no chart block", visualsChartAsk)
	}
	if len(first) == 0 || first[0] != rules.Default+"\n\n"+how.Default {
		say("the standing start does not begin with the standing text and then the instructions on charts and diagrams: %q", first)
	}
	reply, second, err := send(chatID, visualsDiagramAsk)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(reply, "```mermaid\n") {
		say("the test tenant's recorded turn for %q holds no diagram block", visualsDiagramAsk)
	}
	if strings.Join(first, "\x00") != strings.Join(second, "\x00") {
		say("the standing start changed between two calls of one chat, so a provider cannot keep it")
	}
	if err := set(turn.KeyVisualsDraw, "false"); err != nil {
		return nil, err
	}
	offChat, err := newChat()
	if err != nil {
		return nil, err
	}
	if _, off, err := send(offChat, visualsChartAsk); err != nil {
		return nil, err
	} else if len(off) == 0 || off[0] != rules.Default {
		say("with visuals.draw off the model is still told about charts and diagrams: %q", off)
	}
	if err := set(turn.KeyVisualsDraw, "true"); err != nil {
		return nil, err
	}

	// 8. The diagram's page.
	const visualsHost = "keystone.test:7900"
	page := w.request("GET", api.Prefix+"visuals/diagram?theme=dark", token, "", func(r *http.Request) { r.Host = visualsHost })
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `<body data-ks-inline="1">`) || !strings.Contains(page.Body.String(), visualsRenderer) {
		say("the diagram's page answered %d, or does not hold the renderer", page.Code)
	}
	if strings.Count(page.Body.String(), "<script") != 1 {
		say("the diagram's page holds %d scripts, want the renderer alone", strings.Count(page.Body.String(), "<script"))
	}
	policy := page.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "connect-src 'none'", "form-action 'none'", "base-uri 'none'", "frame-src 'none'", "worker-src 'none'", "sandbox " + visuals.FramePermissions} {
		if policyPart(policy, strings.Fields(want)[0]) != want {
			say("the diagram's page has the policy %q, which has no %q", policy, want)
		}
	}
	if script := policyPart(policy, "script-src"); !strings.HasPrefix(script, "script-src 'sha256-") || strings.Contains(script, "unsafe") || len(strings.Fields(script)) != 2 {
		say("the diagram's page lets more run than its one script: %q", script)
	}
	if !strings.HasPrefix(policyPart(policy, "frame-ancestors"), "frame-ancestors 'self'") || page.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		say("the diagram's page may be framed by another site: %q", policyPart(policy, "frame-ancestors"))
	}
	if policy != visuals.Policy(visualsHost, visualsRenderer) {
		say("the diagram's page is served with a policy that is not visuals.Policy: %q", policy)
	}
	if rec := w.request("GET", api.Prefix+"visuals/diagram", "", "", nil); rec.Code != http.StatusUnauthorized {
		say("the diagram's page is served to someone who has not signed in: %d", rec.Code)
	}

	// 9. Saving a diagram.
	body, _ := json.Marshal(visuals.SaveBody{Title: "How a hire goes", Content: "flowchart LR\n  A[Arrive] --> B[Sign the sheet]\n"})
	saved := w.request("POST", api.Prefix+"chats/"+chatID+"/visuals/diagrams", token, string(body), nil)
	var made visuals.SaveReply
	if saved.Code != http.StatusCreated || json.Unmarshal(saved.Body.Bytes(), &made) != nil || made.Artifact.Type != artifacts.KindMermaid || made.Artifact.ChatID != chatID || made.Version.Version != 1 {
		say("saving a diagram answered %d: %s", saved.Code, saved.Body.String())
	}
	if rec := w.request("POST", api.Prefix+"chats/"+chatID+"/visuals/diagrams", otherToken, string(body), nil); rec.Code != http.StatusNotFound {
		say("another person saving a diagram into the chat answered %d, want 404", rec.Code)
	}
	err = with(other.ID, func(s *tenant.Store) error {
		all, err := artifacts.ListAll(ctx, s)
		if err == nil && len(all) != 0 {
			say("the other person has %d artifacts after being refused", len(all))
		}
		return err
	})
	return problems, err
}
