package artifacts

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// A runtime as a build has it: the two renderers.
var testRuntime = fstest.MapFS{
	"sandbox/react.js":   {Data: []byte("/* an invented react renderer */")},
	"sandbox/mermaid.js": {Data: []byte("/* an invented mermaid renderer */")},
}

// directive returns one directive of a policy, or "" when it has none.
func directive(policy, name string) string {
	for _, part := range strings.Split(policy, ";") {
		part = strings.TrimSpace(part)
		if part == name || strings.HasPrefix(part, name+" ") {
			return part
		}
	}
	return ""
}

// The sandbox's policy, for every type: nothing is fetched or sent, the
// page has no origin, and only Keystone may frame it.
func TestEveryTypesPolicyFetchesNothingAndGivesNoOrigin(t *testing.T) {
	for _, k := range Kinds {
		p := Policy(k, "127.0.0.1:7980", testOptions().SandboxTokens())
		for _, want := range []string{"default-src 'none'", "connect-src 'none'", "form-action 'none'", "base-uri 'none'", "frame-src 'none'", "worker-src 'none'"} {
			if directive(p, strings.Fields(want)[0]) != want {
				t.Errorf("%s: the policy %q has no %q", k, p, want)
			}
		}
		if got := directive(p, "sandbox"); got != "sandbox allow-scripts allow-forms allow-modals" {
			t.Errorf("%s: the sandbox directive is %q", k, got)
		}
		if strings.Contains(p, "allow-same-origin") || strings.Contains(p, "allow-top-navigation") || strings.Contains(p, "allow-popups") {
			t.Errorf("%s: the policy %q lets the page out", k, p)
		}
		if got := directive(p, "frame-ancestors"); got != "frame-ancestors 'self' http://127.0.0.1:7980 https://127.0.0.1:7980" {
			t.Errorf("%s: frame-ancestors is %q", k, got)
		}
		for _, d := range []string{"img-src", "font-src", "media-src"} {
			if strings.Contains(directive(p, d), "http") || strings.Contains(directive(p, d), "*") {
				t.Errorf("%s: %s reaches a network address: %q", k, d, directive(p, d))
			}
		}
		// Script only from the page itself: nothing is fetched, not even
		// from Keystone.
		script := directive(p, "script-src")
		switch k {
		case KindHTML, KindReact, KindMermaid:
			if script != "script-src 'unsafe-inline'" {
				t.Errorf("%s: %q", k, script)
			}
		default:
			if script != "script-src 'none'" {
				t.Errorf("%s: %q, want no script", k, script)
			}
		}
	}
	// A Host header that could write into the policy is left out of it.
	p := Policy(KindReact, "evil.example; script-src *", "allow-scripts")
	if strings.Contains(p, "evil") || strings.Contains(p, "*") {
		t.Errorf("a crafted Host reached the policy: %q", p)
	}
	if got := directive(Policy(KindSVG, "x", ""), "sandbox"); got != "sandbox" {
		t.Errorf("with no permissions the directive is %q, want sandbox alone", got)
	}
}

func TestHTMLIsServedAsWritten(t *testing.T) {
	rt := FindRuntime(testRuntime)
	page := string(SandboxPage(KindHTML, "Harbour", pageOne, "dark", rt))
	if page != pageOne {
		t.Errorf("a whole page was changed: %q", page)
	}
	commented := "<!-- tides -->\n<!DOCTYPE html><html><body>x</body></html>"
	if got := string(SandboxPage(KindHTML, "Harbour", commented, "dark", rt)); got != commented {
		t.Errorf("a whole page after a comment was changed: %q", got)
	}
	fragment := `<h1>Tides</h1><script>document.title = "x"</script>`
	got := string(SandboxPage(KindHTML, "Harbour <tides>", fragment, "light", rt))
	if !strings.HasPrefix(got, "<!doctype html>") || !strings.Contains(got, "<body>"+fragment+"</body>") || !strings.Contains(got, "<title>Harbour &lt;tides&gt;</title>") {
		t.Errorf("a fragment is served as %q", got)
	}
}

func TestSVGIsServedAsAnImage(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>alert(1)</script><circle r="4"/></svg>`
	page := string(SandboxPage(KindSVG, "Buoy", svg, "dark", FindRuntime(testRuntime)))
	m := regexp.MustCompile(`<img alt="Buoy" src="data:image/svg\+xml;base64,([A-Za-z0-9+/=]+)">`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the SVG is not an image from a data: address: %q", page)
	}
	if raw, err := base64.StdEncoding.DecodeString(m[1]); err != nil || string(raw) != svg {
		t.Errorf("the image holds %q (%v)", raw, err)
	}
	if strings.Contains(page, "<svg") || strings.Contains(page, "<script") {
		t.Errorf("the SVG's own markup is in the page: %q", page)
	}
	if !strings.Contains(page, `data-theme="dark"`) {
		t.Error("the page does not wear the theme it was asked for")
	}
}

// artifactJSON reads the source a renderer is given.
func artifactJSON(t *testing.T, page string) map[string]string {
	t.Helper()
	m := regexp.MustCompile(`<script type="application/json" id="ks-artifact">(.*?)</script>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("the page has no source for its renderer: %q", page)
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(m[1]), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestReactAndMermaidAreGivenTheirSourceAndRenderer(t *testing.T) {
	rt := FindRuntime(testRuntime)
	if rt.React != "/* an invented react renderer */" || rt.Mermaid != "/* an invented mermaid renderer */" || len(rt.ReactDigest) != 16 || rt.ReactDigest == rt.MermaidDigest {
		t.Fatalf("the renderers read %+v", rt)
	}
	// A source that tries to end its script element.
	source := "export default function App() { return <p>{\"</script><script>alert(1)</script>\"}</p> }"
	for k, script := range map[Kind]string{KindReact: rt.React, KindMermaid: rt.Mermaid} {
		page := string(SandboxPage(k, "Ferry", source, "dark", rt))
		if strings.Count(page, "</script>") != 2 {
			t.Errorf("%s: the source ended its script element: %q", k, page)
		}
		got := artifactJSON(t, page)
		if got["content"] != source || got["type"] != string(k) || got["theme"] != "dark" || got["title"] != "Ferry" {
			t.Errorf("%s: the renderer is given %+v", k, got)
		}
		if !strings.Contains(page, `<script>`+script+`</script></body></html>`) || strings.Contains(page, "src=") {
			t.Errorf("%s: the page does not hold its renderer %s, or loads something: %q", k, script, page)
		}
		if !strings.Contains(page, `<div id="ks-root"`) || !strings.Contains(page, "<noscript>") {
			t.Errorf("%s: the page has no root or no word for a frame without scripts", k)
		}
	}
	// A build without the renderers says so and shows the source, escaped.
	page := string(SandboxPage(KindMermaid, "Ferry", "graph TD; A-->B<i>", "light", FindRuntime(fstest.MapFS{})))
	if !strings.Contains(page, "built without the renderer for mermaid") || !strings.Contains(page, "A--&gt;B&lt;i&gt;") || strings.Contains(page, "<script") {
		t.Errorf("a build without the renderer serves %q", page)
	}
}

// A renderer written into the page cannot end its script element, or start
// the parser's escaped states, whatever its text holds.
func TestARendererIsWrittenSafelyIntoThePage(t *testing.T) {
	js := "a=`<!-->`; b='</script><script>'; c=/<!--x/; d='</SCRIPT >'; e='<Script>'"
	got := Inline(js)
	if regexp.MustCompile(`(?i)</script|<script|<!--`).MatchString(got) {
		t.Errorf("the renderer still holds a closing tag or a comment's start: %q", got)
	}
	if want := "a=`\\x3C!-->`; b='\\x3C/script>\\x3Cscript>'; c=/\\x3C!--x/; d='\\x3C/SCRIPT >'; e='\\x3CScript>'"; got != want {
		t.Errorf("Inline gives %q, want %q", got, want)
	}
	rt := FindRuntime(fstest.MapFS{"sandbox/react.js": {Data: []byte(js)}})
	page := string(SandboxPage(KindReact, "x", "export default () => null", "light", rt))
	if strings.Count(strings.ToLower(page), "</script") != 2 {
		t.Errorf("the page's script elements do not end where they should: %q", page)
	}
}

func TestMarkdownAndCodeAreServedAsEscapedText(t *testing.T) {
	for _, k := range []Kind{KindMarkdown, KindCode} {
		page := string(SandboxPage(k, "Notes", "# Tides\n<script>alert(1)</script>", "light", FindRuntime(testRuntime)))
		if !strings.Contains(page, `<pre class="ks-text"># Tides`+"\n"+`&lt;script&gt;alert(1)&lt;/script&gt;</pre>`) || strings.Contains(page, "<script") {
			t.Errorf("%s: served as %q", k, page)
		}
	}
}
