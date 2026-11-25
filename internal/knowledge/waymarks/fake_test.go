package waymarks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/jonathanmkosar/keystone/internal/knowledge/knowledgetest"
)

// fake is a small Waymarks, served on this machine for the tests. It answers
// the calls the adapter makes the way Waymarks' own API does: the same
// addresses, the same JSON, the same grant checks, the same mark on an
// answer that holds something from a sensitive space, and the same errors.
// It holds everything in memory. All content in it is invented.
type fake struct {
	srv *httptest.Server

	mu      sync.Mutex
	spaces  map[string]string // name to label
	pages   []*fakePage
	sources []*fakeSource
	vectors map[int64]fakeVector
	grants  map[string]*fakeGrant // by token
	model   string                // the one embedding model the instance takes
	size    int
	calls   []fakeCall

	// mode makes every call misbehave in one way. "" is a working instance.
	mode string
	// hold, when set, makes every call wait until it is closed.
	hold chan struct{}
	// elsewhere is where the "redirect" mode sends a caller.
	elsewhere string
}

type fakePage struct {
	id      int64
	space   string
	path    string
	title   string
	kind    string
	body    string
	rev     int
	sources []int64
	deleted bool
}

type fakeSource struct {
	id                                int64
	space, name, origin, dated, text  string
	mediaType, fileName, fileContents string
}

type fakeVector struct {
	rev int
	v   []float32
}

// fakeGrant is what one token may do: its actions in every normal space,
// and in the sensitive spaces it names. A grant for every space does not
// reach a sensitive one until the space is named.
type fakeGrant struct {
	role    string
	actions map[string]bool
	named   map[string]bool
}

// fakeCall is one call the fake received.
type fakeCall struct {
	role   string // the role of the token it came under, or "" for none it knows
	method string
	path   string
}

// The tokens of the three roles. Each is an invented text, not a real token.
const (
	readerToken   = "an-invented-reader-token"
	intakeToken   = "an-invented-intake-token"
	embedderToken = "an-invented-embedder-token"
)

const (
	actRead       = "read"
	actSearch     = "search"
	actAddSources = "add_sources"
	actWritePages = "write_pages"
)

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{
		spaces:  map[string]string{"wiki": "normal"},
		vectors: map[int64]fakeVector{},
		model:   knowledgetest.VectorModel,
		size:    knowledgetest.VectorSize,
		grants: map[string]*fakeGrant{
			readerToken:   {role: "reader", actions: map[string]bool{actRead: true, actSearch: true}, named: map[string]bool{}},
			intakeToken:   {role: "intake", actions: map[string]bool{actAddSources: true}, named: map[string]bool{}},
			embedderToken: {role: "embedder", actions: map[string]bool{actRead: true, actWritePages: true}, named: map[string]bool{}},
		},
	}
	// A fresh Waymarks holds a page of its own rules.
	f.pages = append(f.pages, &fakePage{id: 1, space: "wiki", path: "schema", title: "Schema", kind: "schema",
		body: "How this wiki is organized, and what an agent does here.", rev: 1})

	mux := http.NewServeMux()
	for pattern, h := range map[string]func(*fakeGrant, *http.Request) (any, bool, *fakeError){
		"GET /v1/spaces":            f.spacesList,
		"GET /v1/catalog":           f.catalogGet,
		"GET /v1/pages/{id}":        f.pagesGet,
		"GET /v1/sources/{id}/text": f.sourcesText,
		"GET /v1/search":            f.search,
		"POST /v1/search/hybrid":    f.searchHybrid,
		"POST /v1/sources":          f.sourcesAdd,
		"GET /v1/vectors/needed":    f.vectorsNeeded,
		"PUT /v1/pages/{id}/vector": f.vectorsPut,
	} {
		mux.HandleFunc(pattern, f.wrap(h))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.record("", r)
		writeError(w, &fakeError{404, "not_found", "no such call"})
	})
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		mode, hold, elsewhere := f.mode, f.hold, f.elsewhere
		f.mu.Unlock()
		if hold != nil {
			f.record("", r)
			select {
			case <-hold:
			case <-r.Context().Done():
			}
			return
		}
		switch mode {
		case "":
			mux.ServeHTTP(w, r)
			return
		case "a page that is not JSON":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html><body>It works!</body></html>")
		case "a fault that is not JSON":
			http.Error(w, "bad gateway", http.StatusBadGateway)
		case "a redirect":
			http.Redirect(w, r, elsewhere+r.URL.Path, http.StatusFound)
		case "JSON with no data":
			io.WriteString(w, `{"ok": true}`)
		case "a refused token":
			writeError(w, &fakeError{401, "unauthenticated", "no valid token"})
		case "a fault":
			writeError(w, &fakeError{500, "fault", "the core's own fault"})
		case "a fault that echoes the token":
			writeError(w, &fakeError{500, "fault", "failed for " + r.Header.Get("Authorization")})
		case "an answer too large":
			io.WriteString(w, `{"data": "`+strings.Repeat("a", 4096)+`"}`)
		default:
			panic("the fake has no mode " + mode)
		}
		f.record("", r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// set changes how the fake behaves, under its lock.
func (f *fake) set(change func(f *fake)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *fake) record(role string, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{role: role, method: r.Method, path: r.URL.Path})
}

// received returns the calls so far and forgets them.
func (f *fake) received() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.calls
	f.calls = nil
	return out
}

type fakeError struct {
	status  int
	code    string
	message string
}

func bad(format string, args ...any) *fakeError {
	return &fakeError{400, "invalid", fmt.Sprintf(format, args...)}
}

func forbidden(format string, args ...any) *fakeError {
	return &fakeError{403, "forbidden", fmt.Sprintf(format, args...)}
}

func notFound(format string, args ...any) *fakeError {
	return &fakeError{404, "not_found", fmt.Sprintf(format, args...)}
}

func writeError(w http.ResponseWriter, e *fakeError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": e.code, "message": e.message}})
}

// wrap is what happens to every call: who is asking, then the handler, then
// the answer as {"data": ...}, marked when it holds something sensitive.
func (f *fake) wrap(h func(*fakeGrant, *http.Request) (any, bool, *fakeError)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		g := f.grants[token]
		f.mu.Unlock()
		if !ok || g == nil {
			f.record("", r)
			writeError(w, &fakeError{401, "unauthenticated", "no valid token"})
			return
		}
		f.record(g.role, r)
		f.mu.Lock()
		data, sensitive, ferr := h(g, r)
		f.mu.Unlock()
		if ferr != nil {
			writeError(w, ferr)
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

// ---- the grant. Every function below is called with the lock held.

func (f *fake) sensitive(space string) bool { return f.spaces[space] == "sensitive" }

func (f *fake) allows(g *fakeGrant, action, space string) bool {
	return g.actions[action] && (!f.sensitive(space) || g.named[space])
}

// allowed returns the spaces that exist where the grant has every action
// named, sorted.
func (f *fake) allowed(g *fakeGrant, actions ...string) []string {
	var out []string
	for name := range f.spaces {
		ok := true
		for _, a := range actions {
			ok = ok && f.allows(g, a, name)
		}
		if ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func pathID(r *http.Request) (int64, *fakeError) {
	n, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || n < 1 {
		return 0, bad("id must be a positive whole number")
	}
	return n, nil
}

// body reads a JSON body. An unknown field is refused, not ignored.
func body(r *http.Request, v any) *fakeError {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return bad("the request body is not the JSON this call expects: %v", err)
	}
	return nil
}

// ---- the calls

func (f *fake) spacesList(g *fakeGrant, r *http.Request) (any, bool, *fakeError) {
	out := []map[string]string{}
	for _, name := range f.allowed(g, actRead) {
		out = append(out, map[string]string{"name": name, "label": f.spaces[name], "created_at": "2026-01-01T00:00:00Z", "created_by": "owner"})
	}
	return out, false, nil
}

func (f *fake) catalogGet(g *fakeGrant, r *http.Request) (any, bool, *fakeError) {
	out := []map[string]any{}
	sensitive := false
	for _, space := range f.allowed(g, actRead) {
		sensitive = sensitive || f.sensitive(space)
		var in []*fakePage
		for _, p := range f.pages {
			if p.space == space && !p.deleted {
				in = append(in, p)
			}
		}
		sort.Slice(in, func(i, j int) bool { return in[i].path < in[j].path })
		for _, p := range in {
			row := map[string]any{"id": p.id, "space": p.space, "path": p.path, "title": p.title, "rev": p.rev,
				"updated_at": "2026-01-01T00:00:00Z", "updated_by": "owner", "links_out": 0, "links_in": 0, "sources": len(p.sources)}
			if p.kind != "" {
				row["properties"] = map[string]string{"kind": p.kind}
			}
			out = append(out, row)
		}
	}
	return out, sensitive, nil
}

func (f *fake) page(id int64) *fakePage {
	for _, p := range f.pages {
		if p.id == id {
			return p
		}
	}
	return nil
}

func (f *fake) pagesGet(g *fakeGrant, r *http.Request) (any, bool, *fakeError) {
	id, ferr := pathID(r)
	if ferr != nil {
		return nil, false, ferr
	}
	p := f.page(id)
	if p == nil {
		return nil, false, notFound("page %d", id)
	}
	// Whether a page exists is not hidden: no grant for its space is 403.
	if !f.allows(g, actRead, p.space) {
		return nil, false, forbidden("the grant does not allow read in %s", p.space)
	}
	row := map[string]any{"id": p.id, "space": p.space, "path": p.path, "title": p.title, "body": p.body, "rev": p.rev, "deleted": p.deleted}
	if p.kind != "" {
		row["properties"] = map[string]string{"kind": p.kind}
	}
	if len(p.sources) > 0 {
		row["sources"] = p.sources
	}
	return row, f.sensitive(p.space), nil
}

func (f *fake) source(id int64) *fakeSource {
	for _, s := range f.sources {
		if s.id == id {
			return s
		}
	}
	return nil
}

func sourceRecord(s *fakeSource) map[string]any {
	return map[string]any{"id": s.id, "hash": "0000", "size": len(s.fileContents), "name": s.name, "media_type": s.mediaType,
		"origin": s.origin, "dated": s.dated, "space": s.space, "added_at": "2026-01-01T00:00:00Z", "added_by": "owner", "erased": false}
}

func (f *fake) sourcesText(g *fakeGrant, r *http.Request) (any, bool, *fakeError) {
	id, ferr := pathID(r)
	if ferr != nil {
		return nil, false, ferr
	}
	s := f.source(id)
	if s == nil {
		return nil, false, notFound("source %d", id)
	}
	if !f.allows(g, actRead, s.space) {
		return nil, false, forbidden("the grant does not allow read in %s", s.space)
	}
	return map[string]any{"source": sourceRecord(s), "text": s.text}, f.sensitive(s.space), nil
}

// searchable returns the spaces a search may look in: the one named, or
// every space where the grant can both search and read.
func (f *fake) searchable(g *fakeGrant, space string) (map[string]bool, *fakeError) {
	if !g.actions[actSearch] {
		return nil, forbidden("the grant does not allow search anywhere")
	}
	out := map[string]bool{}
	if space != "" {
		if !f.allows(g, actSearch, space) || !f.allows(g, actRead, space) {
			return nil, forbidden("the grant does not allow search and read in %s", space)
		}
		out[space] = true
		return out, nil
	}
	for _, name := range f.allowed(g, actSearch, actRead) {
		out[name] = true
	}
	return out, nil
}

func wordsOf(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out[w] = true
	}
	return out
}

type fakeHit struct {
	kind  string
	id    int64
	score float64
}

// find ranks the pages and sources in the spaces given by the words they
// share with the query, and by how near a page's vector is to the query's.
func (f *fake) find(spaces map[string]bool, text string, vector []float32, limit int) (any, bool) {
	scores := map[[2]int64]float64{} // {0 for a page or 1 for a source, id}
	query := wordsOf(text)
	shared := func(texts ...string) float64 {
		have := wordsOf(strings.Join(texts, " "))
		n := 0.0
		for w := range query {
			if have[w] {
				n++
			}
		}
		return n
	}
	for _, p := range f.pages {
		if p.deleted || !spaces[p.space] {
			continue
		}
		if n := shared(p.title, p.body); n > 0 {
			scores[[2]int64{0, p.id}] += n
		}
		if v, ok := f.vectors[p.id]; ok && len(vector) == len(v.v) {
			if c := cosine(vector, v.v); c > 0 {
				scores[[2]int64{0, p.id}] += c
			}
		}
	}
	for _, s := range f.sources {
		if !spaces[s.space] {
			continue
		}
		if n := shared(s.name, s.text); n > 0 {
			scores[[2]int64{1, s.id}] += n
		}
	}
	var hits []fakeHit
	for key, score := range scores {
		kind := "page"
		if key[0] == 1 {
			kind = "source"
		}
		hits = append(hits, fakeHit{kind, key[1], score})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].kind != hits[j].kind {
			return hits[i].kind < hits[j].kind
		}
		return hits[i].id < hits[j].id
	})
	if limit == 0 {
		limit = 20
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := []map[string]any{}
	sensitive := false
	for _, h := range hits {
		row := map[string]any{"kind": h.kind, "id": h.id, "score": h.score}
		if h.kind == "page" {
			p := f.page(h.id)
			row["space"], row["path"], row["title"], row["snippet"] = p.space, p.path, p.title, p.body
		} else {
			s := f.source(h.id)
			row["space"], row["name"], row["snippet"] = s.space, s.name, s.text
		}
		sensitive = sensitive || f.sensitive(row["space"].(string))
		out = append(out, row)
	}
	return out, sensitive
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

func (f *fake) search(g *fakeGrant, r *http.Request) (any, bool, *fakeError) {
	q := r.URL.Query()
	spaces, ferr := f.searchable(g, q.Get("space"))
	if ferr != nil {
		return nil, false, ferr
	}
	if strings.TrimSpace(q.Get("q")) == "" {
		return nil, false, bad("q is required: the words to search for")
	}
	limit := 0
	if text := q.Get("limit"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 || n > 100 {
			return nil, false, bad("limit must be from 1 to 100")
		}
		limit = n
	}
	out, sensitive := f.find(spaces, q.Get("q"), nil, limit)
	return out, sensitive, nil
}

// checkVector refuses a vector the instance is not set up for.
func (f *fake) checkVector(model string, v []float32) *fakeError {
	if f.model == "" || f.size < 1 {
		return bad("this instance takes no vectors")
	}
	if model != f.model {
		return bad("this instance takes vectors from the model %q only, not %q", f.model, model)
	}
	if len(v) != f.size {
		return bad("a vector here has %d values, this one has %d", f.size, len(v))
	}
	zero := true
	for _, x := range v {
		zero = zero && x == 0
	}
	if zero {
		return bad("a vector cannot be all zeros")
	}
	return nil
}

func (f *fake) searchHybrid(g *fakeGrant, r *http.Request) (any, bool, *fakeError) {
	if !g.actions[actSearch] {
		return nil, false, forbidden("the grant does not allow search anywhere")
	}
	var in struct {
		Q      string    `json:"q"`
		Vector []float32 `json:"vector"`
		Model  string    `json:"model"`
		Space  string    `json:"space"`
		Kind   string    `json:"kind"`
		Limit  int       `json:"limit"`
	}
	if ferr := body(r, &in); ferr != nil {
		return nil, false, ferr
	}
	spaces, ferr := f.searchable(g, in.Space)
	if ferr != nil {
		return nil, false, ferr
	}
	if strings.TrimSpace(in.Q) == "" && len(in.Vector) == 0 {
		return nil, false, bad("q or vector is required")
	}
	if in.Limit < 0 || in.Limit > 100 {
		return nil, false, bad("limit must be from 1 to 100")
	}
	if len(in.Vector) > 0 {
		if ferr := f.checkVector(in.Model, in.Vector); ferr != nil {
			return nil, false, ferr
		}
	}
	out, sensitive := f.find(spaces, in.Q, in.Vector, in.Limit)
	return out, sensitive, nil
}

func (f *fake) sourcesAdd(g *fakeGrant, r *http.Request) (any, bool, *fakeError) {
	if !g.actions[actAddSources] {
		return nil, false, forbidden("the grant does not allow add_sources anywhere")
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, false, bad("send a source as multipart/form-data, with the fields first and the file last")
	}
	s := &fakeSource{}
	for {
		part, err := mr.NextPart()
		if err != nil {
			return nil, false, bad("the source has no file part")
		}
		raw, err := io.ReadAll(part)
		if err != nil {
			return nil, false, bad("the source cannot be read")
		}
		value := string(raw)
		if part.FormName() == "file" {
			s.fileName, s.fileContents = part.FileName(), value
			if s.name == "" {
				s.name = part.FileName()
			}
			break
		}
		switch part.FormName() {
		case "space":
			s.space = value
		case "name":
			s.name = value
		case "media_type":
			s.mediaType = value
		case "origin":
			s.origin = value
		case "dated":
			s.dated = value
		case "text":
			s.text = value
		default:
			return nil, false, bad("unknown field %q", part.FormName())
		}
	}
	if !f.allows(g, actAddSources, s.space) {
		return nil, false, forbidden("the grant does not allow add_sources in %s", s.space)
	}
	if s.space == "" {
		return nil, false, bad("a source needs a space, sent before the file")
	}
	if _, ok := f.spaces[s.space]; !ok {
		f.spaces[s.space] = "normal"
	}
	s.id = int64(len(f.sources) + 1)
	f.sources = append(f.sources, s)
	return sourceRecord(s), f.sensitive(s.space), nil
}

func (f *fake) vectorsNeeded(g *fakeGrant, r *http.Request) (any, bool, *fakeError) {
	if !g.actions[actWritePages] {
		return nil, false, forbidden("the grant does not allow write_pages anywhere")
	}
	limit := 100
	if text := r.URL.Query().Get("limit"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 || n > 1000 {
			return nil, false, bad("limit must be from 1 to 1000")
		}
		if n > 0 {
			limit = n
		}
	}
	if f.model == "" || f.size < 1 {
		return nil, false, bad("this instance takes no vectors")
	}
	out := []map[string]any{}
	sensitive := false
	for _, space := range f.allowed(g, actWritePages, actRead) {
		var in []*fakePage
		for _, p := range f.pages {
			if p.space == space && !p.deleted {
				in = append(in, p)
			}
		}
		sort.Slice(in, func(i, j int) bool { return in[i].path < in[j].path })
		for _, p := range in {
			v, ok := f.vectors[p.id]
			if ok && v.rev >= p.rev {
				continue
			}
			if len(out) == limit {
				break
			}
			reason := "none"
			if ok {
				reason = "stale"
			}
			out = append(out, map[string]any{"page_id": p.id, "space": p.space, "path": p.path, "title": p.title, "rev": p.rev, "reason": reason})
			sensitive = sensitive || f.sensitive(space)
		}
	}
	return out, sensitive, nil
}

func (f *fake) vectorsPut(g *fakeGrant, r *http.Request) (any, bool, *fakeError) {
	if !g.actions[actWritePages] {
		return nil, false, forbidden("the grant does not allow write_pages anywhere")
	}
	id, ferr := pathID(r)
	if ferr != nil {
		return nil, false, ferr
	}
	p := f.page(id)
	if p == nil || p.deleted {
		return nil, false, notFound("page %d", id)
	}
	if !f.allows(g, actWritePages, p.space) {
		return nil, false, forbidden("the grant does not allow write_pages in %s", p.space)
	}
	var in struct {
		Rev    int       `json:"rev"`
		Model  string    `json:"model"`
		Vector []float32 `json:"vector"`
	}
	if ferr := body(r, &in); ferr != nil {
		return nil, false, ferr
	}
	if in.Rev < 1 {
		return nil, false, bad("rev is required: the page revision the vector was made from")
	}
	if ferr := f.checkVector(in.Model, in.Vector); ferr != nil {
		return nil, false, ferr
	}
	if in.Rev != p.rev {
		return nil, false, &fakeError{409, "stale", fmt.Sprintf("page %d is at revision %d, the vector was made from %d", id, p.rev, in.Rev)}
	}
	f.vectors[id] = fakeVector{rev: in.Rev, v: in.Vector}
	return map[string]any{"page_id": id, "rev": in.Rev, "model": in.Model, "size": len(in.Vector)}, f.sensitive(p.space), nil
}

// ---- putting content in, as the instance's owner would

// seed puts the suite's content into the fake. A space marked sensitive is
// named in every token's grant, as an owner does who wants Keystone to go
// on reading it.
func (f *fake) seed(c knowledgetest.Content) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, space := range c.Sensitive {
		f.spaces[space] = "sensitive"
		for _, g := range f.grants {
			g.named[space] = true
		}
	}
	named := map[string]int64{}
	for _, src := range c.Sources {
		f.ensureSpace(src.Space)
		s := &fakeSource{id: int64(len(f.sources) + 1), space: src.Space, name: src.Name, origin: src.Origin, dated: src.Dated, text: src.Text}
		f.sources = append(f.sources, s)
		named[src.Name] = s.id
	}
	for _, sp := range c.Pages {
		f.ensureSpace(sp.Space)
		var cites []int64
		for _, name := range sp.Cites {
			cites = append(cites, named[name])
		}
		var p *fakePage
		for _, have := range f.pages {
			if have.space == sp.Space && have.path == sp.Path && !have.deleted {
				p = have
			}
		}
		if p == nil {
			p = &fakePage{id: int64(len(f.pages) + 1), space: sp.Space, path: sp.Path}
			f.pages = append(f.pages, p)
		}
		p.title, p.kind, p.body, p.sources = sp.Title, sp.Kind, sp.Body, cites
		p.rev++
		if sp.Vector != nil {
			f.vectors[p.id] = fakeVector{rev: p.rev, v: sp.Vector}
		}
	}
}

func (f *fake) ensureSpace(name string) {
	if _, ok := f.spaces[name]; !ok {
		f.spaces[name] = "normal"
	}
}
