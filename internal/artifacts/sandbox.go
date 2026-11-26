package artifacts

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html"
	"io/fs"
	"regexp"
	"strings"

	"github.com/jonathanmkosar/keystone/web"
)

// The sandbox: the page an artifact is shown in, served from a route of its
// own (GET /artifacts/{artifact}/versions/{version}/sandbox) and drawn in
// an iframe whose sandbox never allows same-origin. Its policy allows
// nothing to be fetched or sent: no connection, no script or style from any
// address, no form action, no frame, no worker, and images and fonts only
// from data: and blob: addresses. Everything the page needs is in it. Its
// sandbox directive makes it a page of no origin even when it is opened on
// its own, so its script can never read the person's session, cookies or
// API. The main page's policy is not touched (decision 0422).
//
// What each type is served as:
//
//   - html: the page as written, or the fragment in a page;
//   - svg: an image, from a data: address, so its own script never runs;
//   - mermaid and react: the source as JSON, and the renderer, a script
//     built into the web interface under sandbox/ (decision 0421), written
//     into the page, since a page of no origin should fetch nothing;
//   - markdown and code: the text, escaped. The panel draws these two in
//     the app itself, with its own Markdown reader and a code block.

// Runtime is the sandbox's renderers, as a page holds them: each script
// made safe to write inside a script element, and a digest of it, which
// names the build in the page's ETag. Empty when the build has none.
type Runtime struct {
	React, Mermaid             string
	ReactDigest, MermaidDigest string
}

// RuntimeFiles are the renderers' files in the built web interface.
var RuntimeFiles = map[Kind]string{KindReact: "sandbox/react.js", KindMermaid: "sandbox/mermaid.js"}

// FindRuntime reads the renderers from the built web interface. Nil means
// the files compiled into the binary.
func FindRuntime(fsys fs.FS) Runtime {
	if fsys == nil {
		fsys = web.Files()
	}
	var rt Runtime
	rt.React, rt.ReactDigest = script(fsys, RuntimeFiles[KindReact])
	rt.Mermaid, rt.MermaidDigest = script(fsys, RuntimeFiles[KindMermaid])
	return rt
}

func script(fsys fs.FS, name string) (text, sum string) {
	raw, err := fs.ReadFile(fsys, name)
	if err != nil || len(raw) == 0 {
		return "", ""
	}
	d := sha256.Sum256(raw)
	return Inline(string(raw)), hex.EncodeToString(d[:8])
}

// closing is any spelling of what would end a script element or start the
// HTML parser's escaped states inside one.
var closing = regexp.MustCompile(`(?i)<(/script|script|!--)`)

// Inline makes a script safe to write inside a script element: each "<"
// that would end the element, or start the parser's escaped states inside
// it, is written \x3C, which JavaScript reads as "<" in a string, a
// template and a regular expression alike.
func Inline(js string) string {
	return closing.ReplaceAllString(js, `\x3C$1`)
}

// hostShape is a Host header that may be written into a policy: a name or
// an address, and a port. Anything else is left out of it.
var hostShape = regexp.MustCompile(`^([a-zA-Z0-9.-]+|\[[0-9a-fA-F:.]+\])(:[0-9]{1,5})?$`)

// selfSources is Keystone's own address, for the frame-ancestors of a page
// of no origin: 'self', and the address the browser asked for, under both
// schemes, so it holds behind a tunnel that speaks plain HTTP to Keystone.
func selfSources(host string) string {
	s := "'self'"
	if hostShape.MatchString(host) {
		s += " http://" + host + " https://" + host
	}
	return s
}

// Policy is the sandbox page's content security policy for one type.
// sandbox is the frame's permissions, as Options.SandboxTokens gives them.
func Policy(k Kind, host, sandbox string) string {
	script := "'none'"
	switch k {
	case KindHTML, KindReact, KindMermaid:
		// The page's own scripts: an HTML page's, or the renderer and,
		// for React, the component it turns into script and runs.
		script = "'unsafe-inline'"
	}
	parts := []string{
		"default-src 'none'",
		"script-src " + script,
		"style-src 'unsafe-inline'",
		"img-src data: blob:",
		"font-src data:",
		"media-src data: blob:",
		"connect-src 'none'",
		"frame-src 'none'",
		"worker-src 'none'",
		"manifest-src 'none'",
		"form-action 'none'",
		"base-uri 'none'",
		"frame-ancestors " + selfSources(host),
		strings.TrimSpace("sandbox " + sandbox),
	}
	return strings.Join(parts, "; ")
}

// Theme is a sandbox page's theme: dark or light.
func Theme(s string) string {
	if s == "dark" {
		return "dark"
	}
	return "light"
}

const baseStyle = `:root{color-scheme:light}:root[data-theme="dark"]{color-scheme:dark}` +
	`html,body{margin:0;min-height:100%;background:Canvas;color:CanvasText}` +
	`body{font:15px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}` +
	`.ks-center{box-sizing:border-box;min-height:100vh;display:grid;place-items:center;padding:16px}` +
	`.ks-center>img,.ks-center>svg{max-width:100%;height:auto}` +
	`.ks-text{margin:0;padding:16px;white-space:pre-wrap;overflow-wrap:anywhere;font:13px/1.55 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}` +
	`.ks-note{margin:16px;padding:12px 14px;border:1px dashed;border-radius:8px;font-size:14px}`

func head(title, theme string) string {
	return `<!doctype html><html lang="en" data-theme="` + theme + `"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1"><meta name="referrer" content="no-referrer">` +
		`<title>` + html.EscapeString(title) + `</title><style>` + baseStyle + `</style></head>`
}

// isDocument reports whether HTML is a whole page rather than a fragment.
func isDocument(content string) bool {
	start := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(content, "ï»¿")))
	if len(start) > 512 {
		start = start[:512]
	}
	for strings.HasPrefix(start, "<!--") {
		end := strings.Index(start, "-->")
		if end < 0 {
			return false
		}
		start = strings.TrimSpace(start[end+3:])
	}
	return strings.HasPrefix(start, "<!doctype") || strings.HasPrefix(start, "<html")
}

// noScripts is what a page whose renderer needs script says when the
// artifact's frame may run none.
const noScripts = `<noscript><p class="ks-note">Scripts are switched off for artifacts on this Keystone (the artifacts.sandbox setting), so this one cannot be drawn. Its source is in the panel.</p></noscript>`

// SandboxPage is the page the sandbox route serves for one version of an
// artifact of type k, titled title, holding content.
func SandboxPage(k Kind, title, content, theme string, rt Runtime) []byte {
	theme = Theme(theme)
	var b strings.Builder
	switch k {
	case KindHTML:
		if isDocument(content) {
			return []byte(content)
		}
		b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">`)
		b.WriteString(`<meta name="referrer" content="no-referrer"><title>` + html.EscapeString(title) + `</title></head><body>`)
		b.WriteString(content)
		b.WriteString(`</body></html>`)
	case KindSVG:
		b.WriteString(head(title, theme))
		b.WriteString(`<body><div class="ks-center"><img alt="` + html.EscapeString(title) + `" src="data:image/svg+xml;base64,`)
		b.WriteString(base64.StdEncoding.EncodeToString([]byte(content)))
		b.WriteString(`"></div></body></html>`)
	case KindReact, KindMermaid:
		script := rt.React
		root := `<div id="ks-root"></div>`
		if k == KindMermaid {
			script = rt.Mermaid
			root = `<div id="ks-root" class="ks-center"></div>`
		}
		b.WriteString(head(title, theme))
		b.WriteString(`<body>`)
		if script == "" {
			b.WriteString(`<p class="ks-note">This Keystone was built without the renderer for ` + string(k) + ` artifacts, so this one cannot be drawn here. Its source:</p>`)
			b.WriteString(`<pre class="ks-text">` + html.EscapeString(content) + `</pre></body></html>`)
			break
		}
		// The source as JSON, which escapes <, > and &, so nothing in it can
		// end the script element.
		data, _ := json.Marshal(struct {
			Type    Kind   `json:"type"`
			Title   string `json:"title"`
			Content string `json:"content"`
			Theme   string `json:"theme"`
		}{k, title, content, theme})
		b.WriteString(root)
		b.WriteString(`<script type="application/json" id="ks-artifact">` + string(data) + `</script>`)
		b.WriteString(noScripts)
		b.WriteString(`<script>` + script + `</script></body></html>`)
	default: // markdown and code
		b.WriteString(head(title, theme))
		b.WriteString(`<body><pre class="ks-text">` + html.EscapeString(content) + `</pre></body></html>`)
	}
	return []byte(b.String())
}
