// Package visuals serves what a reply's inline visuals need from the
// server (P9-04): the page a diagram is drawn in, and a route that keeps a
// reply's diagram as an artifact of its chat.
//
// A chart in a reply is drawn by the web interface's own code from checked
// numbers, and needs nothing from here. A diagram is Mermaid source, and
// Mermaid never runs in the app's own page: it runs in a sandboxed frame
// of the artifact sandbox's kind (internal/artifacts, decision 0422), a
// page of no origin under a policy that fetches and sends nothing. This
// package serves that page. It is the same page for every diagram: it
// holds the renderer and no diagram, and the app hands it a block's source
// with postMessage. So it holds nothing of any person, and its script is
// Keystone's own and nothing else, which lets its policy name that one
// script by its digest where an artifact's page allows any inline script
// (decision 0541).
//
// The package reads no setting. It imports net/http for its two routes and
// opens no connection.
package visuals

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
)

// Channel is the name every message between the page and a diagram's
// frame carries. The renderer built for the frame holds it, which is how a
// build whose renderer cannot draw a reply's diagram is told from one that
// can. web/app/src/visuals.ts has the same name.
const Channel = "keystone-diagram"

// FramePermissions is all a diagram's frame may do: run the page's one
// script. It is not a setting: the frame runs Keystone's renderer and
// nothing a model wrote, and needs nothing else. Never same-origin.
const FramePermissions = "allow-scripts"

// DefaultTitle is the title a saved diagram gets when its source names
// none.
const DefaultTitle = "Diagram"

// Options are what the routes run with.
type Options struct {
	// Artifacts are the install's limits on artifacts, which a saved
	// diagram is kept under, and where the sandbox's renderers are found.
	Artifacts artifacts.Options
}

// SaveBody is a diagram of a reply to keep as an artifact.
type SaveBody struct {
	// Title names the artifact. Empty means DefaultTitle.
	Title string `json:"title"`
	// Content is the diagram's Mermaid source, as the reply holds it.
	Content string `json:"content"`
}

// SaveReply answers a save: the artifact that was made, as version 1.
type SaveReply struct {
	Artifact artifacts.Artifact `json:"artifact"`
	Version  artifacts.Version  `json:"version"`
}

// Routes returns the inline visuals' routes.
func Routes(o Options) []api.Route {
	h := &handlers{o: o, rt: artifacts.FindRuntime(o.Artifacts.Runtime)}
	return []api.Route{
		{Method: "GET", Path: api.Prefix + "visuals/diagram", Handle: h.frame,
			Query:   []api.Param{{Name: "theme", Description: "dark or light: the theme of the page around the diagram."}},
			Summary: "The page a reply's diagram is drawn in, inside a sandboxed frame: the same for every diagram, holding the renderer and no diagram, served with a policy of its own that fetches and sends nothing, runs that one script and gives the page no origin."},
		{Method: "POST", Path: api.Prefix + "chats/{id}/visuals/diagrams", Handle: h.save, Body: SaveBody{}, Reply: SaveReply{},
			Summary: "Keep a diagram of a reply as a Mermaid artifact of its chat, as version 1."},
	}
}

type handlers struct {
	o  Options
	rt artifacts.Runtime

	mu    sync.Mutex
	pages map[string]built
}

// built is the frame's page in one theme, with what names it.
type built struct {
	page   []byte
	script string
}

// CanDraw reports whether a build's Mermaid renderer can draw a reply's
// diagram: it is there, and it knows the messages.
func CanDraw(rt artifacts.Runtime) bool {
	return rt.Mermaid != "" && strings.Contains(rt.Mermaid, Channel)
}

const frameStyle = `:root{color-scheme:light}:root[data-theme="dark"]{color-scheme:dark}` +
	`html,body{margin:0;height:100%;background:transparent;color:CanvasText}` +
	`body{font:14px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}` +
	`#ks-root{box-sizing:border-box;height:100vh;display:grid;place-items:center;padding:12px;overflow:hidden}` +
	// The drawing keeps its own width, or the frame's when that is less,
	// and is fitted whole into the frame's height by its viewBox.
	`#ks-root>svg{display:block;height:auto;max-height:100%}` +
	`.ks-note{margin:16px;padding:12px 14px;border:1px dashed;border-radius:8px}`

// DiagramPage is the page a reply's diagram is drawn in, in one theme, and
// the script it holds, exactly as written between its script tags. A build
// whose renderer cannot draw one gets a page that says so and no script.
func DiagramPage(theme string, rt artifacts.Runtime) (page []byte, script string) {
	theme = artifacts.Theme(theme)
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en" data-theme="` + theme + `"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1"><meta name="referrer" content="no-referrer">`)
	b.WriteString(`<title>Diagram</title><style>` + frameStyle + `</style></head>`)
	if !CanDraw(rt) {
		b.WriteString(`<body><p class="ks-note">This Keystone was built without the renderer for diagrams in a reply, so this one cannot be drawn here. Its source is shown in the reply.</p></body></html>`)
		return []byte(b.String()), ""
	}
	// data-ks-inline tells the renderer to wait for a diagram from the page
	// that frames it, in place of reading an artifact out of this one.
	b.WriteString(`<body data-ks-inline="1"><div id="ks-root"></div><script>`)
	b.WriteString(rt.Mermaid)
	b.WriteString(`</script></body></html>`)
	return []byte(b.String()), rt.Mermaid
}

// Policy is the content security policy of the diagram's page: the
// artifact sandbox's policy for a Mermaid artifact, with the frame's own
// permissions, and with script allowed only as the one script the page
// holds, named by its digest. With no script, none is allowed.
func Policy(host, script string) string {
	source := "'none'"
	if script != "" {
		sum := sha256.Sum256([]byte(script))
		source = "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	parts := strings.Split(artifacts.Policy(artifacts.KindMermaid, host, FramePermissions), "; ")
	for i, p := range parts {
		if strings.HasPrefix(p, "script-src ") {
			parts[i] = "script-src " + source
		}
	}
	return strings.Join(parts, "; ")
}

func (h *handlers) built(theme string) built {
	h.mu.Lock()
	defer h.mu.Unlock()
	if b, ok := h.pages[theme]; ok {
		return b
	}
	page, script := DiagramPage(theme, h.rt)
	if h.pages == nil {
		h.pages = map[string]built{}
	}
	h.pages[theme] = built{page: page, script: script}
	return h.pages[theme]
}

func (h *handlers) frame(w http.ResponseWriter, r *http.Request, _ api.Call) {
	theme := artifacts.Theme(r.URL.Query().Get("theme"))
	b := h.built(theme)
	policy := Policy(r.Host, b.script)
	head := w.Header()
	head.Set("Content-Security-Policy", policy)
	head.Set("X-Frame-Options", "SAMEORIGIN")
	head.Set("Cross-Origin-Resource-Policy", "same-origin")
	// The page holds the renderer, megabytes of it, and is the same for
	// every diagram: the browser keeps it and asks whether it changed.
	sum := sha256.Sum256([]byte(strings.Join([]string{"diagram", theme, h.rt.MermaidDigest, policy}, "\n")))
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	head.Set("ETag", etag)
	head.Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	head.Set("Content-Type", "text/html; charset=utf-8")
	head.Set("Content-Length", strconv.Itoa(len(b.page)))
	w.WriteHeader(http.StatusOK)
	w.Write(b.page)
}

func (h *handlers) save(w http.ResponseWriter, r *http.Request, c api.Call) {
	var body SaveBody
	if !api.ReadJSON(w, r, &body) {
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = DefaultTitle
	}
	// The chat is one of this person's: the store is theirs alone. A chat
	// held for sensitive content marks the artifact sensitive, as any
	// artifact of it is.
	a, v, err := artifacts.Create(r.Context(), c.Store, h.o.Artifacts, r.PathValue("id"), artifacts.Draft{
		Type: artifacts.KindMermaid, Title: title, Content: body.Content,
	}, c.RequestID)
	switch {
	case errors.Is(err, artifacts.ErrNotFound):
		api.Error(w, r, http.StatusNotFound, "not_found", "there is no such chat")
	case errors.Is(err, artifacts.ErrRefused):
		api.Error(w, r, http.StatusBadRequest, "refused", strings.TrimPrefix(err.Error(), artifacts.ErrRefused.Error()+": "))
	case err != nil:
		api.Error(w, r, http.StatusInternalServerError, "failed", "the call failed")
	default:
		api.WriteJSON(w, http.StatusCreated, SaveReply{Artifact: a, Version: v})
	}
}
