package knowledgetest

import (
	"context"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
)

// memory is a knowledge store held in memory. It exists only to prove the
// suite is sound: a correct copy must pass, and a copy with one fault must
// fail. It lives in a test file, so it is in no binary, it is not
// registered, and no tenant can choose it.
type memory struct {
	caps  knowledge.Capabilities
	fault string

	sensitive map[string]bool
	pages     []*memPage
	sources   []*memSource
	nextID    int
	changes   int
}

type memPage struct {
	id, space, path, title, kind, body string
	rev                                int
	cites                              []string
	vec                                []float32
	vecRev                             int
}

type memSource struct {
	id, space, name, origin, dated, text string
}

// The faults a memory store can be given. Each breaks one promise.
const (
	faultBlankLabel      = "leaves every label blank"
	faultAllNormal       = "labels every space normal"
	faultKeepsFirstLabel = "keeps a space's first label"
	faultIgnoresCancel   = "ignores a cancelled context"
	faultFindsAnything   = "returns an empty page for an id it does not hold"
	faultUnsorted        = "lists the catalog out of order"
	faultIgnoresSpace    = "searches every space when one is named"
	faultIgnoresLimit    = "returns more hits than the limit"
	faultWorstFirst      = "returns the worst hit first"
	faultClaimsAddSource = "claims AddSource and refuses it"
	faultDropsAdded      = "accepts a source and does not keep it"
	faultFrozenMarker    = "never changes its catalog marker"
	faultIgnoresVector   = "claims VectorSearch and ignores the vector"
	faultAcceptsStale    = "takes a vector for a revision that is gone"
	faultForgetsStale    = "does not list a page whose vector is stale"
)

func newMemory(caps knowledge.Capabilities, fault string) *memory {
	return &memory{caps: caps, fault: fault, sensitive: map[string]bool{}}
}

// memoryHarness is the harness for a memory store with these capabilities
// and this fault, or no fault when it is empty.
func memoryHarness(caps knowledge.Capabilities, fault string) Harness {
	return Harness{
		New:  func(*testing.T) knowledge.Store { return newMemory(caps, fault) },
		Seed: func(_ *testing.T, s knowledge.Store, c Content) { s.(*memory).seed(c) },
	}
}

func (m *memory) id(prefix string) string {
	m.nextID++
	return prefix + strconv.Itoa(m.nextID)
}

func (m *memory) holds(space string) bool {
	for _, pg := range m.pages {
		if pg.space == space {
			return true
		}
	}
	for _, src := range m.sources {
		if src.space == space {
			return true
		}
	}
	return false
}

func (m *memory) seed(c Content) {
	for _, space := range c.Sensitive {
		if m.fault == faultKeepsFirstLabel && m.holds(space) {
			continue
		}
		if !m.sensitive[space] {
			m.sensitive[space] = true
			m.changes++
		}
	}
	named := map[string]string{}
	for _, src := range c.Sources {
		id := m.id("s")
		m.sources = append(m.sources, &memSource{id: id, space: src.Space, name: src.Name, origin: src.Origin, dated: src.Dated, text: src.Text})
		named[src.Name] = id
	}
	for _, sp := range c.Pages {
		var pg *memPage
		for _, have := range m.pages {
			if have.space == sp.Space && have.path == sp.Path {
				pg = have
			}
		}
		if pg == nil {
			pg = &memPage{id: m.id("p"), space: sp.Space, path: sp.Path}
			m.pages = append(m.pages, pg)
		}
		pg.title, pg.kind, pg.body = sp.Title, sp.Kind, sp.Body
		pg.rev++
		pg.cites = nil
		for _, name := range sp.Cites {
			pg.cites = append(pg.cites, named[name])
		}
		if sp.Vector != nil && m.caps.VectorSearch {
			pg.vec, pg.vecRev = sp.Vector, pg.rev
		}
		m.changes++
	}
}

func (m *memory) label(space string) knowledge.Label {
	switch {
	case m.fault == faultBlankLabel:
		return ""
	case m.fault == faultAllNormal:
		return knowledge.LabelNormal
	case m.sensitive[space]:
		return knowledge.LabelSensitive
	}
	return knowledge.LabelNormal
}

func (m *memory) done(ctx context.Context) error {
	if m.fault == faultIgnoresCancel {
		return nil
	}
	return ctx.Err()
}

func (m *memory) page(id string) *memPage {
	for _, pg := range m.pages {
		if pg.id == id {
			return pg
		}
	}
	return nil
}

// sorted returns the pages by space and then path.
func (m *memory) sorted() []*memPage {
	out := slices.Clone(m.pages)
	sort.Slice(out, func(i, j int) bool {
		if out[i].space != out[j].space {
			return out[i].space < out[j].space
		}
		return out[i].path < out[j].path
	})
	return out
}

func (m *memory) Capabilities() knowledge.Capabilities { return m.caps }

func (m *memory) Catalog(ctx context.Context) ([]knowledge.Entry, error) {
	if err := m.done(ctx); err != nil {
		return nil, err
	}
	var out []knowledge.Entry
	for _, pg := range m.sorted() {
		out = append(out, knowledge.Entry{ID: pg.id, Space: pg.space, Label: m.label(pg.space), Path: pg.path, Title: pg.title, Kind: pg.kind})
	}
	if m.fault == faultUnsorted {
		slices.Reverse(out)
	}
	return out, nil
}

func (m *memory) Search(ctx context.Context, q knowledge.Query) ([]knowledge.Hit, error) {
	if len(q.Vector) > 0 && !m.caps.VectorSearch {
		return nil, knowledge.ErrNotSupported
	}
	if err := m.done(ctx); err != nil {
		return nil, err
	}
	vector := q.Vector
	if m.fault == faultIgnoresVector {
		vector = nil
	}
	in := func(space string) bool {
		return q.Space == "" || q.Space == space || m.fault == faultIgnoresSpace
	}

	// A hit's score is the number of times the words appear, plus how near
	// its vector is to the query's.
	at := map[string]int{}
	var hits []knowledge.Hit
	add := func(h knowledge.Hit, score float64) {
		key := h.Type + ":" + h.ID
		if i, ok := at[key]; ok {
			hits[i].Score += score
			return
		}
		h.Score = score
		at[key] = len(hits)
		hits = append(hits, h)
	}
	pageHit := func(pg *memPage) knowledge.Hit {
		return knowledge.Hit{Type: knowledge.HitPage, ID: pg.id, Space: pg.space, Label: m.label(pg.space), Path: pg.path, Title: pg.title, Snippet: pg.body}
	}
	if words := tokens(q.Text); len(words) > 0 {
		for _, pg := range m.pages {
			if n := count(words, pg.title+" "+pg.body); n > 0 && in(pg.space) {
				add(pageHit(pg), float64(n))
			}
		}
		for _, src := range m.sources {
			if n := count(words, src.name+" "+src.text); n > 0 && in(src.space) {
				add(knowledge.Hit{Type: knowledge.HitSource, ID: src.id, Space: src.space, Label: m.label(src.space), Title: src.name, Snippet: src.text}, float64(n))
			}
		}
	}
	if len(vector) > 0 {
		for _, pg := range m.pages {
			if pg.vec != nil && in(pg.space) {
				add(pageHit(pg), 1+cosine(vector, pg.vec))
			}
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.Type+a.ID < b.Type+b.ID
	})
	if m.fault == faultWorstFirst {
		slices.Reverse(hits)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if len(hits) > limit && m.fault != faultIgnoresLimit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (m *memory) ReadPage(ctx context.Context, id string) (knowledge.Page, error) {
	if err := m.done(ctx); err != nil {
		return knowledge.Page{}, err
	}
	pg := m.page(id)
	if pg == nil {
		if m.fault == faultFindsAnything {
			return knowledge.Page{}, nil
		}
		return knowledge.Page{}, knowledge.ErrNotFound
	}
	return knowledge.Page{
		ID: pg.id, Space: pg.space, Label: m.label(pg.space), Path: pg.path, Title: pg.title, Kind: pg.kind,
		Body: pg.body, Revision: strconv.Itoa(pg.rev), Sources: slices.Clone(pg.cites),
	}, nil
}

func (m *memory) source(src *memSource) knowledge.Source {
	return knowledge.Source{ID: src.id, Space: src.space, Label: m.label(src.space), Name: src.name, Origin: src.origin, Dated: src.dated, Text: src.text}
}

func (m *memory) ReadSource(ctx context.Context, id string) (knowledge.Source, error) {
	if err := m.done(ctx); err != nil {
		return knowledge.Source{}, err
	}
	for _, src := range m.sources {
		if src.id == id {
			return m.source(src), nil
		}
	}
	return knowledge.Source{}, knowledge.ErrNotFound
}

func (m *memory) AddSource(ctx context.Context, n knowledge.NewSource) (knowledge.Source, error) {
	if !m.caps.AddSource || m.fault == faultClaimsAddSource {
		return knowledge.Source{}, knowledge.ErrNotSupported
	}
	if err := m.done(ctx); err != nil {
		return knowledge.Source{}, err
	}
	if err := knowledge.CheckNewSource(n); err != nil {
		return knowledge.Source{}, err
	}
	var text strings.Builder
	for _, f := range n.Facts {
		text.WriteString("- " + f.Text + "\n")
	}
	src := &memSource{id: m.id("s"), space: n.Space, name: n.Name, origin: n.Origin, dated: n.Dated, text: text.String()}
	if m.fault != faultDropsAdded {
		m.sources = append(m.sources, src)
	}
	return m.source(src), nil
}

func (m *memory) CatalogMarker(ctx context.Context) (string, error) {
	if !m.caps.CatalogMarker {
		return "", knowledge.ErrNotSupported
	}
	if err := m.done(ctx); err != nil {
		return "", err
	}
	if m.fault == faultFrozenMarker {
		return "change 0", nil
	}
	return "change " + strconv.Itoa(m.changes), nil
}

func (m *memory) VectorsNeeded(ctx context.Context, limit int) ([]knowledge.PageNeed, error) {
	if !m.caps.PageVectors {
		return nil, knowledge.ErrNotSupported
	}
	if err := m.done(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	var out []knowledge.PageNeed
	for _, pg := range m.sorted() {
		stale := pg.vec != nil && pg.vecRev != pg.rev
		if pg.vec != nil && (!stale || m.fault == faultForgetsStale) {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, knowledge.PageNeed{PageID: pg.id, Space: pg.space, Label: m.label(pg.space), Title: pg.title, Stale: stale})
	}
	return out, nil
}

func (m *memory) PutVector(ctx context.Context, v knowledge.PageVector) error {
	if !m.caps.PageVectors {
		return knowledge.ErrNotSupported
	}
	if err := m.done(ctx); err != nil {
		return err
	}
	pg := m.page(v.PageID)
	if pg == nil {
		return knowledge.ErrNotFound
	}
	if len(v.Vector) != VectorSize || v.Model != VectorModel {
		return knowledge.ErrInvalid
	}
	if v.Revision != strconv.Itoa(pg.rev) && m.fault != faultAcceptsStale {
		return knowledge.ErrStale
	}
	pg.vec, pg.vecRev = slices.Clone(v.Vector), pg.rev
	return nil
}

func (m *memory) Close() error { return nil }

// tokens cuts text into lower-case words.
func tokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// count returns how many times the words appear in text, or zero when any
// one of them is missing.
func count(words []string, text string) int {
	have := map[string]int{}
	for _, w := range tokens(text) {
		have[w]++
	}
	total := 0
	for _, w := range words {
		if have[w] == 0 {
			return 0
		}
		total += have[w]
	}
	return total
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
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
