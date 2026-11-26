package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// fakeWaymarks is a small Waymarks served on this machine, for the recall
// and sensitive content checks (P6-05). It answers the five read calls the
// Waymarks adapter makes for recall, the lookups and the map: the spaces,
// the catalog, a page, a source's text, and a search by words. Its answers
// have Waymarks' shape, its mark on an answer that holds something from a
// sensitive space, and its refusal of a token it does not know. Its one
// token reaches every space, sensitive ones too, so the adapter is given
// sensitive content and has to label it.
//
// The adapter's own tests have a fuller fake, in a test file this package
// cannot import. Everything this one holds is invented.
type fakeWaymarks struct {
	srv   *httptest.Server
	token string

	mu      sync.Mutex
	spaces  map[string]string // name to label
	pages   []wmPage
	sources []wmSource
	// vectors are the page vectors pushed to it, by page id, for the
	// embeddings check (P6-08, waymarksvectors.go).
	vectors map[int64]wmVector
}

type wmPage struct {
	id                             int64
	space, path, title, kind, body string
}

type wmSource struct {
	id                               int64
	space, name, origin, dated, text string
}

// waymarksToken is invented. It guards a fake that lives for one check.
const waymarksToken = "an-invented-reader-token-for-verify"

func newFakeWaymarks() *fakeWaymarks {
	f := &fakeWaymarks{token: waymarksToken, spaces: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/spaces", f.wrap(f.spaceList))
	mux.HandleFunc("GET /v1/catalog", f.wrap(f.catalog))
	mux.HandleFunc("GET /v1/pages/{id}", f.wrap(f.page))
	mux.HandleFunc("GET /v1/sources/{id}/text", f.wrap(f.sourceText))
	mux.HandleFunc("GET /v1/search", f.wrap(f.search))
	// Page vectors, and search by one (P6-08, waymarksvectors.go).
	mux.HandleFunc("GET /v1/vectors/needed", f.wrap(f.vectorsNeeded))
	mux.HandleFunc("PUT /v1/pages/{id}/vector", f.wrap(f.vectorPut))
	mux.HandleFunc("POST /v1/search/hybrid", f.wrap(f.hybrid))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		wmError(w, http.StatusNotFound, "not_found", "no such call")
	})
	f.srv = httptest.NewServer(mux)
	return f
}

func (f *fakeWaymarks) close() { f.srv.Close() }

// addSpace, addPage and addSource fill the fake. Ids count from 1, pages
// and sources apart, as Waymarks numbers them.
func (f *fakeWaymarks) addSpace(name, label string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spaces[name] = label
}

func (f *fakeWaymarks) addPage(space, path, title, kind, body string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := int64(len(f.pages) + 1)
	f.pages = append(f.pages, wmPage{id, space, path, title, kind, body})
	return id
}

func (f *fakeWaymarks) addSource(space, name, origin, dated, text string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := int64(len(f.sources) + 1)
	f.sources = append(f.sources, wmSource{id, space, name, origin, dated, text})
	return id
}

func wmError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// wrap checks the token, runs the call under the lock, and answers with
// {"data": ...}, marked when it holds something from a sensitive space.
func (f *fakeWaymarks) wrap(h func(*http.Request) (any, bool, int)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			wmError(w, http.StatusUnauthorized, "unauthenticated", "no valid token")
			return
		}
		f.mu.Lock()
		data, sensitive, status := h(r)
		f.mu.Unlock()
		if status != 0 {
			wmError(w, status, "not_found", "no such page or source")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		out := map[string]any{"data": data}
		if sensitive {
			out["sensitive"] = true
			w.Header().Set("Waymarks-Sensitive", "true")
		}
		json.NewEncoder(w).Encode(out)
	}
}

func (f *fakeWaymarks) sensitive(space string) bool { return f.spaces[space] != "normal" }

func (f *fakeWaymarks) spaceList(*http.Request) (any, bool, int) {
	var names []string
	for name := range f.spaces {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []map[string]string{}
	for _, name := range names {
		out = append(out, map[string]string{"name": name, "label": f.spaces[name], "created_at": "2026-01-01T00:00:00Z", "created_by": "owner"})
	}
	return out, false, 0
}

func (f *fakeWaymarks) catalog(*http.Request) (any, bool, int) {
	out := []map[string]any{}
	sensitive := false
	pages := append([]wmPage(nil), f.pages...)
	sort.Slice(pages, func(i, j int) bool {
		if pages[i].space != pages[j].space {
			return pages[i].space < pages[j].space
		}
		return pages[i].path < pages[j].path
	})
	for _, p := range pages {
		sensitive = sensitive || f.sensitive(p.space)
		out = append(out, map[string]any{"id": p.id, "space": p.space, "path": p.path, "title": p.title, "rev": 1,
			"updated_at": "2026-01-01T00:00:00Z", "updated_by": "owner", "properties": map[string]string{"kind": p.kind}})
	}
	return out, sensitive, 0
}

func pathID(r *http.Request) int64 {
	n, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return n
}

func (f *fakeWaymarks) page(r *http.Request) (any, bool, int) {
	id := pathID(r)
	for _, p := range f.pages {
		if p.id == id {
			return map[string]any{"id": p.id, "space": p.space, "path": p.path, "title": p.title, "body": p.body, "rev": 1,
				"deleted": false, "properties": map[string]string{"kind": p.kind}}, f.sensitive(p.space), 0
		}
	}
	return nil, false, http.StatusNotFound
}

func (f *fakeWaymarks) sourceText(r *http.Request) (any, bool, int) {
	id := pathID(r)
	for _, s := range f.sources {
		if s.id == id {
			record := map[string]any{"id": s.id, "hash": "0000", "size": len(s.text), "name": s.name, "media_type": "text/plain",
				"origin": s.origin, "dated": s.dated, "space": s.space, "added_at": "2026-01-01T00:00:00Z", "added_by": "owner", "erased": false}
			return map[string]any{"source": record, "text": s.text}, f.sensitive(s.space), 0
		}
	}
	return nil, false, http.StatusNotFound
}

func wmWords(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out[w] = true
	}
	return out
}

// search ranks pages and sources by how many words of the query they hold.
func (f *fakeWaymarks) search(r *http.Request) (any, bool, int) {
	q := r.URL.Query()
	query := wmWords(q.Get("q"))
	space := q.Get("space")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	score := func(texts ...string) float64 {
		have := wmWords(strings.Join(texts, " "))
		n := 0.0
		for w := range query {
			if have[w] {
				n++
			}
		}
		return n
	}
	type hit struct {
		row   map[string]any
		score float64
		key   string
	}
	var hits []hit
	for _, p := range f.pages {
		if n := score(p.title, p.body); n > 0 && (space == "" || space == p.space) {
			hits = append(hits, hit{map[string]any{"kind": "page", "id": p.id, "space": p.space, "path": p.path, "title": p.title, "snippet": p.body, "score": n}, n, "p" + p.path})
		}
	}
	for _, s := range f.sources {
		if n := score(s.name, s.text); n > 0 && (space == "" || space == s.space) {
			hits = append(hits, hit{map[string]any{"kind": "source", "id": s.id, "space": s.space, "name": s.name, "snippet": s.text, "score": n}, n, "s" + s.name})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].key < hits[j].key
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := []map[string]any{}
	sensitive := false
	for _, h := range hits {
		sensitive = sensitive || f.sensitive(h.row["space"].(string))
		out = append(out, h.row)
	}
	return out, sensitive, 0
}
