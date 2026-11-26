package wikijobs

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeWaymarks is a Waymarks on this machine, for the tests: the calls the
// Waymarks adapter and the wiki jobs make, written from Waymarks' own
// source (internal/api/routes.go and internal/mcp/tools.go in Waymarks):
// the same addresses, the {"data": …} answers, one principal for each
// token with its grant, the refusal of an unknown field, and an audit log
// that names the principal of every call. Every name and token in it is
// invented.
type fakeWaymarks struct {
	t      *testing.T
	server *httptest.Server

	mu         sync.Mutex
	principals map[string]fakePrincipal // by token
	spaces     map[string]string        // name to label
	sources    []fakeSource
	pages      map[int64]*fakePage
	batches    map[int64]*fakeBatch
	audit      []fakeEntry
	nextPage   int64
	nextBatch  int64
	nextChange int64
}

type fakePrincipal struct {
	name    string
	actions map[string]bool
}

type fakeSource struct {
	ID     int64  `json:"id"`
	Space  string `json:"space"`
	Name   string `json:"name"`
	Origin string `json:"origin"`
	Dated  string `json:"dated"`
	text   string
}

type fakePage struct {
	ID         int64             `json:"id"`
	Space      string            `json:"space"`
	Path       string            `json:"path"`
	Title      string            `json:"title"`
	Body       string            `json:"body"`
	Rev        int               `json:"rev"`
	Properties map[string]string `json:"properties"`
	Sources    []int64           `json:"sources"`
}

type fakeWrite struct {
	PageID     int64             `json:"page_id"`
	BaseRev    int               `json:"base_rev"`
	Space      string            `json:"space"`
	Path       string            `json:"path"`
	Title      string            `json:"title"`
	Body       string            `json:"body"`
	Delete     bool              `json:"delete"`
	Properties map[string]string `json:"properties,omitempty"`
	Sources    []int64           `json:"sources,omitempty"`
}

type fakeBatch struct {
	actor, note, state string
	writes             map[int64]fakeWrite // by page id
	order              []int64
}

type fakeEntry struct {
	Actor   string `json:"actor"`
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
	Target  string `json:"target"`
}

// The invented principals' grants, as the agent kit gives them.
var fakeGrants = map[string][]string{
	"keeper":           {"read", "search", "write_pages", "delete_pages", "add_sources", "raise_issues", "close_issues", "administer"},
	"keystone-reader":  {"read", "search"},
	"keystone-intake":  {"add_sources"},
	"keystone-compile": {"read", "search", "write_pages", "delete_pages"},
	"keystone-lint":    {"read", "search", "write_pages", "delete_pages", "raise_issues"},
}

func newFakeWaymarks(t *testing.T) *fakeWaymarks {
	f := &fakeWaymarks{t: t, principals: map[string]fakePrincipal{}, spaces: map[string]string{"wiki": "normal", "memory": "normal"},
		pages: map[int64]*fakePage{}, batches: map[int64]*fakeBatch{}}
	f.pages[1] = &fakePage{ID: 1, Space: "wiki", Path: "schema", Title: "How this wiki works", Body: "One thing, one page. Cite every fact.", Rev: 1, Properties: map[string]string{}}
	f.nextPage = 1
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

// token gives a principal of fakeGrants a token, and returns it.
func (f *fakeWaymarks) token(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok := "invented-token-of-" + name
	acts := map[string]bool{}
	for _, a := range fakeGrants[name] {
		acts[a] = true
	}
	f.principals[tok] = fakePrincipal{name: name, actions: acts}
	return tok
}

func (f *fakeWaymarks) entries() []fakeEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeEntry(nil), f.audit...)
}

func (f *fakeWaymarks) pageList() []fakePage {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakePage
	for _, p := range f.pages {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (f *fakeWaymarks) sourceList() []fakeSource {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeSource(nil), f.sources...)
}

func answerJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v, "sensitive": false})
}

func refuse(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

// decode reads a JSON body, refusing a field it does not know.
func decode(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	raw, _ := io.ReadAll(r.Body)
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (f *fakeWaymarks) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.principals[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !ok {
		refuse(w, http.StatusUnauthorized, "unauthorized", "the token is not known")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "v1" {
		refuse(w, http.StatusNotFound, "not_found", "no such route")
		return
	}
	parts = parts[1:]
	action, target := "", ""
	need := func(act string) bool {
		if !p.actions[act] {
			f.audit = append(f.audit, fakeEntry{Actor: p.name, Action: action, Outcome: "refused", Target: target})
			refuse(w, http.StatusForbidden, "forbidden", "the grant does not allow "+act)
			return false
		}
		return true
	}
	ok200 := func(status int, v any) {
		f.audit = append(f.audit, fakeEntry{Actor: p.name, Action: action, Outcome: "ok", Target: target})
		answerJSON(w, status, v)
	}
	id := func(i int) int64 {
		if len(parts) <= i {
			return 0
		}
		n, _ := strconv.ParseInt(parts[i], 10, 64)
		return n
	}
	switch {
	case r.Method == "GET" && parts[0] == "whoami":
		action = "whoami"
		acts := []string{}
		for a := range p.actions {
			acts = append(acts, a)
		}
		sort.Strings(acts)
		ok200(200, map[string]any{"name": p.name, "kind": "agent", "grant": []map[string]any{{"space": "*", "actions": acts}}})
	case r.Method == "GET" && parts[0] == "schema":
		action = "schema"
		if need("read") {
			ok200(200, f.pages[1].Body)
		}
	case r.Method == "GET" && parts[0] == "spaces":
		action = "spaces.list"
		var out []map[string]string
		for name, label := range f.spaces {
			out = append(out, map[string]string{"name": name, "label": label})
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["name"] < out[j]["name"] })
		ok200(200, out)
	case r.Method == "GET" && parts[0] == "catalog":
		action = "catalog"
		if !need("read") {
			return
		}
		var rows []map[string]any
		var md strings.Builder
		for _, pg := range f.sortedPages() {
			rows = append(rows, map[string]any{"id": pg.ID, "space": pg.Space, "path": pg.Path, "title": pg.Title, "properties": pg.Properties, "sources": len(pg.Sources)})
			fmt.Fprintf(&md, "- [[page:%d]] %s/%s, %s, %d sources\n", pg.ID, pg.Space, pg.Path, pg.Title, len(pg.Sources))
		}
		if r.URL.Query().Get("format") == "md" {
			ok200(200, md.String())
			return
		}
		ok200(200, rows)
	case r.Method == "GET" && parts[0] == "pages" && len(parts) == 1:
		action = "pages.list"
		if need("read") {
			ok200(200, f.sortedPages())
		}
	case r.Method == "GET" && parts[0] == "pages" && len(parts) == 2:
		action, target = "pages.get", "page:"+parts[1]
		if !need("read") {
			return
		}
		pg, ok := f.pages[id(1)]
		if !ok {
			refuse(w, http.StatusNotFound, "not_found", "no such page")
			return
		}
		ok200(200, pg)
	case r.Method == "GET" && parts[0] == "pages" && len(parts) == 3 && parts[2] == "links":
		action = "pages.links"
		if !need("read") {
			return
		}
		pg, ok := f.pages[id(1)]
		if !ok {
			refuse(w, http.StatusNotFound, "not_found", "no such page")
			return
		}
		ok200(200, map[string]any{"links_to": []int64{}, "linked_from": []int64{}, "sources": pg.Sources})
	case r.Method == "GET" && parts[0] == "sources" && len(parts) == 1:
		action = "sources.list"
		if !need("read") {
			return
		}
		out := []fakeSource{}
		for _, s := range f.sources {
			if sp := r.URL.Query().Get("space"); sp == "" || sp == s.Space {
				out = append(out, s)
			}
		}
		ok200(200, out)
	case r.Method == "GET" && parts[0] == "sources" && len(parts) >= 2:
		action, target = "sources.get", "source:"+parts[1]
		if len(parts) == 3 && parts[2] == "text" {
			action = "sources.text"
		}
		if !need("read") {
			return
		}
		n := id(1)
		if n < 1 || int(n) > len(f.sources) {
			refuse(w, http.StatusNotFound, "not_found", "no such source")
			return
		}
		s := f.sources[n-1]
		if action == "sources.text" {
			ok200(200, map[string]any{"source": s, "text": s.text})
			return
		}
		ok200(200, s)
	case r.Method == "POST" && parts[0] == "sources":
		action = "sources.add"
		if !need("add_sources") {
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			refuse(w, http.StatusBadRequest, "invalid", "not a form")
			return
		}
		space := r.FormValue("space")
		if _, ok := f.spaces[space]; !ok {
			refuse(w, http.StatusBadRequest, "invalid", "no such space")
			return
		}
		s := fakeSource{ID: int64(len(f.sources) + 1), Space: space, Name: r.FormValue("name"), Origin: r.FormValue("origin"), Dated: r.FormValue("dated"), text: r.FormValue("text")}
		f.sources = append(f.sources, s)
		target = fmt.Sprintf("source:%d", s.ID)
		ok200(201, s)
	case r.Method == "GET" && parts[0] == "search":
		action = "search"
		if !need("search") {
			return
		}
		q := strings.Fields(strings.ToLower(r.URL.Query().Get("q")))
		hits := []map[string]any{}
		match := func(text string) bool {
			text = strings.ToLower(text)
			for _, w := range q {
				if !strings.Contains(text, w) {
					return false
				}
			}
			return len(q) > 0
		}
		for _, pg := range f.sortedPages() {
			if match(pg.Title + " " + pg.Body) {
				hits = append(hits, map[string]any{"kind": "page", "id": pg.ID, "space": pg.Space, "path": pg.Path, "title": pg.Title, "snippet": pg.Body, "score": 1})
			}
		}
		for _, s := range f.sources {
			if match(s.text) {
				hits = append(hits, map[string]any{"kind": "source", "id": s.ID, "space": s.Space, "name": s.Name, "snippet": s.text, "score": 0.5})
			}
		}
		ok200(200, hits)
	case r.Method == "GET" && (parts[0] == "issues" || parts[0] == "lint"):
		action = parts[0]
		if need("read") {
			ok200(200, []any{})
		}
	case r.Method == "POST" && parts[0] == "batches" && len(parts) == 1:
		action = "batches.open"
		if !p.actions["write_pages"] && !p.actions["delete_pages"] {
			need("write_pages")
			return
		}
		var in struct {
			Note string `json:"note"`
		}
		if err := decode(r, &in); err != nil {
			refuse(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		f.nextBatch++
		f.batches[f.nextBatch] = &fakeBatch{actor: p.name, note: in.Note, state: "open", writes: map[int64]fakeWrite{}}
		target = fmt.Sprintf("batch:%d", f.nextBatch)
		ok200(201, map[string]any{"id": f.nextBatch, "actor": p.name, "note": in.Note, "state": "open"})
	case r.Method == "POST" && parts[0] == "batches" && len(parts) == 3:
		b, ok := f.batches[id(1)]
		target = "batch:" + parts[1]
		if !ok || b.actor != p.name || b.state != "open" {
			refuse(w, http.StatusForbidden, "forbidden", "not your open batch")
			return
		}
		switch parts[2] {
		case "writes":
			action = "batches.write"
			var wr fakeWrite
			if err := decode(r, &wr); err != nil {
				refuse(w, http.StatusBadRequest, "invalid", err.Error())
				return
			}
			if wr.Delete && !need("delete_pages") || !wr.Delete && !need("write_pages") {
				return
			}
			pageID := wr.PageID
			if pageID == 0 {
				for pid, held := range b.writes {
					if held.BaseRev == 0 && held.Space == wr.Space && held.Path == wr.Path && !held.Delete && f.pages[pid] == nil {
						pageID = pid
					}
				}
				if pageID == 0 {
					for _, pg := range f.pages {
						if pg.Space == wr.Space && pg.Path == wr.Path {
							refuse(w, http.StatusConflict, "path_taken", "another page is at that path")
							return
						}
					}
					f.nextPage++
					pageID = f.nextPage
					b.order = append(b.order, pageID)
				}
			} else {
				cur, ok := f.pages[pageID]
				if !ok {
					refuse(w, http.StatusNotFound, "not_found", "no such page")
					return
				}
				if cur.Rev != wr.BaseRev {
					refuse(w, http.StatusConflict, "stale", "the page changed")
					return
				}
				if _, held := b.writes[pageID]; !held {
					b.order = append(b.order, pageID)
				}
			}
			b.writes[pageID] = wr
			ok200(200, map[string]any{"batch_id": id(1), "page_id": pageID, "new_rev": wr.BaseRev + 1})
		case "close":
			action = "batches.close"
			for _, pid := range b.order {
				wr := b.writes[pid]
				if wr.Delete {
					delete(f.pages, pid)
					continue
				}
				pg := f.pages[pid]
				if pg == nil {
					pg = &fakePage{ID: pid}
					f.pages[pid] = pg
				}
				pg.Space, pg.Path, pg.Title, pg.Body, pg.Rev, pg.Properties, pg.Sources = wr.Space, wr.Path, wr.Title, wr.Body, pg.Rev+1, wr.Properties, wr.Sources
			}
			b.state = "closed"
			f.nextChange++
			ok200(200, map[string]any{"id": f.nextChange, "kind": "batch", "actor": p.name, "note": b.note, "batch_id": id(1)})
		case "drop":
			action = "batches.drop"
			b.state = "dropped"
			ok200(200, map[string]any{"id": id(1), "state": "dropped"})
		default:
			refuse(w, http.StatusNotFound, "not_found", "no such route")
		}
	case r.Method == "GET" && parts[0] == "audit":
		action = "audit.list"
		if need("administer") {
			ok200(200, f.audit)
		}
	default:
		refuse(w, http.StatusNotFound, "not_found", "no such route")
	}
}

func (f *fakeWaymarks) sortedPages() []fakePage {
	var out []fakePage
	for _, p := range f.pages {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
