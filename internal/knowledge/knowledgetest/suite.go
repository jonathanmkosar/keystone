// Package knowledgetest holds the contract every knowledge store adapter
// must pass. An adapter's own tests call Run with a way to make a fresh
// store and a way to put content into it.
//
// The suite checks what the Store interface promises. A part behind a
// capability flag is checked when the flag is set, and when the flag is not
// set the suite checks the call answers "not supported".
//
// All content here is invented.
package knowledgetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
)

// The vectors the suite pushes and searches with are of this model and this
// size. A store that checks either must be set up to take them.
const (
	VectorModel = "knowledgetest-embed"
	VectorSize  = 4
)

// Harness is what an adapter's test gives the suite.
type Harness struct {
	// New returns a fresh store. It holds nothing but what the store puts
	// there by itself. The suite closes it.
	New func(t *testing.T) knowledge.Store

	// Seed puts content into a store New returned, by whatever means the
	// adapter has outside the interface. It adds to what is there. A page
	// whose space and path are already there is replaced by the new one. A
	// space named in Content.Sensitive is marked sensitive, whether or not
	// it already holds anything. A page's Vector is stored, as a vector of
	// VectorModel, when the store has VectorSearch, and ignored when not.
	//
	// Only an adapter that can hold nothing, such as none, leaves Seed nil.
	// The suite then requires the store to claim no capability.
	Seed func(t *testing.T, s knowledge.Store, c Content)
}

// Content is what Seed puts into a store.
type Content struct {
	// Sensitive names the spaces to mark sensitive. Every other space is
	// normal.
	Sensitive []string
	Sources   []SeedSource
	Pages     []SeedPage
}

// SeedPage is one page to put in.
type SeedPage struct {
	Space string
	Path  string
	Title string
	Kind  string
	Body  string
	// Cites names the sources the page cites, by SeedSource.Name. They are
	// in the same Content.
	Cites []string
	// Vector is the page's vector, or nil for a page with none.
	Vector []float32
}

// SeedSource is one source to put in.
type SeedSource struct {
	Space  string
	Name   string
	Origin string
	Dated  string
	Text   string
}

// Run checks a store against the contract. Each check is a subtest.
func Run(t *testing.T, h Harness) {
	t.Helper()
	if h.New == nil {
		t.Fatal("knowledgetest: the harness needs New")
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			problems, skipped := runCheck(t, h, c)
			for _, p := range problems {
				t.Error(p)
			}
			if skipped != "" && len(problems) == 0 {
				t.Skip(skipped)
			}
		})
	}
}

type check struct {
	name string
	// seeded checks need content, and are skipped for a store with no Seed.
	seeded bool
	run    func(p *probe)
}

var checks = []check{
	{"capabilities", false, checkCapabilities},
	{"fresh store", false, checkFresh},
	{"unsupported calls", false, checkUnsupported},
	{"cancelled context", false, checkCancelled},
	{"catalog", true, checkCatalog},
	{"read page and source", true, checkRead},
	{"search", true, checkSearch},
	{"space made sensitive later", true, checkRelabel},
	{"catalog marker", true, checkMarker},
	{"add source", true, checkAddSource},
	{"vector search", true, checkVectorSearch},
	{"page vectors", true, checkPageVectors},
	{"remove source", true, checkRemoveSource},
}

// runCheck runs one check and returns what it found wrong. The suite's own
// tests call it to see that a faulty store is caught.
func runCheck(t *testing.T, h Harness, c check) (problems []string, skipped string) {
	t.Helper()
	if c.seeded && h.Seed == nil {
		return nil, "this store cannot be given content"
	}
	p := &probe{t: t, h: h}
	c.run(p)
	return p.problems, p.skipped
}

// probe is one check at work: the harness, and the problems found so far.
type probe struct {
	t        *testing.T
	h        Harness
	problems []string
	skipped  string
}

func (p *probe) errorf(format string, args ...any) {
	p.problems = append(p.problems, fmt.Sprintf(format, args...))
}

// fresh returns a new store, closed when the test ends.
func (p *probe) fresh() knowledge.Store {
	p.t.Helper()
	s := p.h.New(p.t)
	if s == nil {
		p.t.Fatal("knowledgetest: the harness's New returned no store")
	}
	p.t.Cleanup(func() {
		if err := s.Close(); err != nil {
			p.t.Errorf("Close: %v", err)
		}
	})
	return s
}

// seeded returns a new store holding the content.
func (p *probe) seeded(c Content) knowledge.Store {
	p.t.Helper()
	s := p.fresh()
	p.h.Seed(p.t, s, c)
	return s
}

// ---- the content the suite works with

const (
	spaceHome   = "home"
	spaceHealth = "health" // sensitive
	spaceNotes  = "notes"  // not seeded: AddSource makes it

	pathGarden = "garden/beds"
	pathBread  = "kitchen/bread"
	pathSleep  = "sleep"

	nameSeedOrder = "seed-order.txt"
	nameClinic    = "clinic-letter.txt"

	noSuchID   = "no-such-id"
	noSuchWord = "zeppelinarium"
)

var (
	vecGarden = []float32{1, 0, 0, 0}
	vecBread  = []float32{0, 1, 0, 0}
	vecSleep  = []float32{0, 0, 1, 0}

	nearGarden = []float32{0.9, 0.1, 0, 0}
	nearBread  = []float32{0.1, 0.9, 0, 0}
	nearSleep  = []float32{0, 0.1, 0.9, 0}
)

// base is two normal pages, one sensitive page, and a source for each space.
// The word "spring" is in three of them, "tomatoes" in one page only,
// "marigold" in one source only, and "insomnia" only in the sensitive space.
func base(vectors bool) Content {
	c := Content{
		Sensitive: []string{spaceHealth},
		Sources: []SeedSource{
			{Space: spaceHome, Name: nameSeedOrder, Origin: "an invented seed catalogue", Dated: "2026-02-14",
				Text: "Order for the spring: tomato seed, basil seed and marigold seed."},
			{Space: spaceHealth, Name: nameClinic, Origin: "an invented clinic", Dated: "2026-03-02",
				Text: "The follow-up about insomnia moves to the autumn."},
		},
		Pages: []SeedPage{
			{Space: spaceHome, Path: pathGarden, Title: "Raised beds", Kind: "project",
				Body: "The raised beds get tomatoes and basil this spring.", Cites: []string{nameSeedOrder}},
			{Space: spaceHome, Path: pathBread, Title: "Bread routine", Kind: "note",
				Body: "Feed the starter each evening with rye flour. Bake on the first day of spring."},
			{Space: spaceHealth, Path: pathSleep, Title: "Sleep notes", Kind: "log",
				Body: "Magnesium before bed eased the insomnia.", Cites: []string{nameClinic}},
		},
	}
	if vectors {
		c.Pages[0].Vector = vecGarden
		c.Pages[1].Vector = vecBread
		c.Pages[2].Vector = vecSleep
	}
	return c
}

// baseSensitive says which of the base spaces are sensitive.
var baseSensitive = map[string]bool{spaceHome: false, spaceHealth: true, spaceNotes: false}

func chatFacts(space string) knowledge.NewSource {
	return knowledge.NewSource{
		Space:  space,
		Name:   "chat-facts",
		Origin: "an invented chat",
		Dated:  "2026-11-24",
		Facts: []knowledge.Fact{
			{Text: "The greenhouse door needs a new hinge."},
			{Text: "The compost bin is emptied in October."},
		},
	}
}

// ---- helpers

// label checks a label is one of the two an adapter may give, and the right
// one of the two.
func (p *probe) label(what string, got knowledge.Label, sensitive bool) {
	want := knowledge.LabelNormal
	if sensitive {
		want = knowledge.LabelSensitive
	}
	if got != want {
		p.errorf("%s: label is %q, want %q", what, got, want)
	}
}

func entryAt(entries []knowledge.Entry, space, path string) (knowledge.Entry, bool) {
	for _, e := range entries {
		if e.Space == space && e.Path == path {
			return e, true
		}
	}
	return knowledge.Entry{}, false
}

// catalog reads the catalog and checks what holds for every catalog: ids,
// spaces, labels and the order.
func (p *probe) catalog(s knowledge.Store) []knowledge.Entry {
	entries, err := s.Catalog(context.Background())
	if err != nil {
		p.errorf("Catalog: %v", err)
		return nil
	}
	seen := map[string]bool{}
	for i, e := range entries {
		what := fmt.Sprintf("catalog entry %s/%s", e.Space, e.Path)
		if e.ID == "" || e.Space == "" || e.Title == "" {
			p.errorf("%s: an entry needs an id, a space and a title: %+v", what, e)
		}
		if seen[e.ID] {
			p.errorf("%s: id %q is in the catalog twice", what, e.ID)
		}
		seen[e.ID] = true
		if e.Label != knowledge.LabelNormal && e.Label != knowledge.LabelSensitive {
			p.errorf("%s: label is %q, want normal or sensitive", what, e.Label)
		}
		if i > 0 {
			prev := entries[i-1]
			if prev.Space > e.Space || (prev.Space == e.Space && prev.Path > e.Path) {
				p.errorf("the catalog is not ordered by space and path: %s/%s comes before %s/%s", prev.Space, prev.Path, e.Space, e.Path)
			}
		}
	}
	return entries
}

// search runs a query and checks what holds for every answer: the type, the
// id, the space and the label of each hit, best first, and no more than the
// limit. The labels of the spaces the suite knows are checked against
// sensitive.
func (p *probe) search(s knowledge.Store, q knowledge.Query, sensitive map[string]bool) []knowledge.Hit {
	what := fmt.Sprintf("Search(%q, vector %v, space %q, limit %d)", q.Text, len(q.Vector) > 0, q.Space, q.Limit)
	hits, err := s.Search(context.Background(), q)
	if err != nil {
		p.errorf("%s: %v", what, err)
		return nil
	}
	if q.Limit > 0 && len(hits) > q.Limit {
		p.errorf("%s: %d hits, more than the limit", what, len(hits))
	}
	for i, h := range hits {
		if h.Type != knowledge.HitPage && h.Type != knowledge.HitSource {
			p.errorf("%s: hit %d has type %q, want page or source", what, i, h.Type)
		}
		if h.ID == "" || h.Space == "" {
			p.errorf("%s: hit %d needs an id and a space: %+v", what, i, h)
		}
		if h.Label != knowledge.LabelNormal && h.Label != knowledge.LabelSensitive {
			p.errorf("%s: hit %d has label %q, want normal or sensitive", what, i, h.Label)
		}
		if want, known := sensitive[h.Space]; known {
			p.label(fmt.Sprintf("%s: hit %d in space %s", what, i, h.Space), h.Label, want)
		}
		if q.Space != "" && h.Space != q.Space {
			p.errorf("%s: hit %d is in space %q", what, i, h.Space)
		}
		if i > 0 && h.Score > hits[i-1].Score {
			p.errorf("%s: hit %d scores %v, above the hit before it at %v: best must come first", what, i, h.Score, hits[i-1].Score)
		}
	}
	return hits
}

func hitFor(hits []knowledge.Hit, typ, id string) (knowledge.Hit, bool) {
	for _, h := range hits {
		if h.Type == typ && h.ID == id {
			return h, true
		}
	}
	return knowledge.Hit{}, false
}

// ids returns the ids of the three base pages, read from the catalog.
func (p *probe) ids(s knowledge.Store) (garden, bread, sleep string, ok bool) {
	entries := p.catalog(s)
	g, ok1 := entryAt(entries, spaceHome, pathGarden)
	b, ok2 := entryAt(entries, spaceHome, pathBread)
	sl, ok3 := entryAt(entries, spaceHealth, pathSleep)
	if !ok1 || !ok2 || !ok3 {
		p.errorf("the catalog does not list the three seeded pages")
		return "", "", "", false
	}
	return g.ID, b.ID, sl.ID, true
}

// ---- checks every store gets

func checkCapabilities(p *probe) {
	s := p.fresh()
	a, b := s.Capabilities(), s.Capabilities()
	if a != b {
		p.errorf("Capabilities changed between two calls: %+v then %+v", a, b)
	}
	if p.h.Seed == nil && a != (knowledge.Capabilities{}) {
		p.errorf("the harness has no Seed and the store claims %+v: only a store that holds nothing may leave Seed out, and it has no capability", a)
	}
}

func checkFresh(p *probe) {
	ctx := context.Background()
	s := p.fresh()
	entries := p.catalog(s)
	if p.h.Seed == nil && len(entries) != 0 {
		p.errorf("a store that cannot be given content lists %d pages", len(entries))
	}
	for _, q := range []knowledge.Query{
		{},
		{Space: spaceHome},
		{Text: noSuchWord},
		{Text: noSuchWord, Space: spaceHome},
		{Text: noSuchWord, Limit: 1},
	} {
		hits, err := s.Search(ctx, q)
		if err != nil || len(hits) != 0 {
			p.errorf("Search(%+v) on a fresh store: %d hits, error %v, want no hits and no error", q, len(hits), err)
		}
	}
	for _, id := range []string{"", noSuchID} {
		if _, err := s.ReadPage(ctx, id); !errors.Is(err, knowledge.ErrNotFound) {
			p.errorf("ReadPage(%q): error %v, want ErrNotFound", id, err)
		}
		if _, err := s.ReadSource(ctx, id); !errors.Is(err, knowledge.ErrNotFound) {
			p.errorf("ReadSource(%q): error %v, want ErrNotFound", id, err)
		}
	}
}

func checkUnsupported(p *probe) {
	ctx := context.Background()
	s := p.fresh()
	caps := s.Capabilities()
	want := func(call string, err error) {
		if !errors.Is(err, knowledge.ErrNotSupported) {
			p.errorf("%s without its capability: error %v, want ErrNotSupported", call, err)
		}
	}
	if !caps.AddSource {
		_, err := s.AddSource(ctx, chatFacts(spaceNotes))
		want("AddSource", err)
	}
	if !caps.CatalogMarker {
		_, err := s.CatalogMarker(ctx)
		want("CatalogMarker", err)
	}
	if !caps.VectorSearch {
		_, err := s.Search(ctx, knowledge.Query{Vector: nearGarden, Model: VectorModel})
		want("Search with a vector", err)
		_, err = s.Search(ctx, knowledge.Query{Text: "tomatoes", Vector: nearGarden, Model: VectorModel})
		want("Search with words and a vector", err)
	}
	if !caps.PageVectors {
		_, err := s.VectorsNeeded(ctx, VectorModel, 0)
		want("VectorsNeeded", err)
		want("PutVector", s.PutVector(ctx, knowledge.PageVector{PageID: noSuchID, Revision: "1", Model: VectorModel, Vector: vecGarden}))
	}
	if !caps.RemoveSource {
		want("RemoveSource", s.RemoveSource(ctx, noSuchID))
	}
}

func checkCancelled(p *probe) {
	var s knowledge.Store
	pageID, sourceID, revision := noSuchID, noSuchID, "1"
	if p.h.Seed != nil {
		s = p.seeded(base(true))
		if garden, _, _, ok := p.ids(s); ok {
			pageID = garden
			if page, err := s.ReadPage(context.Background(), garden); err == nil {
				revision = page.Revision
				if len(page.Sources) > 0 {
					sourceID = page.Sources[0]
				}
			}
		}
	} else {
		s = p.fresh()
	}
	caps := s.Capabilities()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := []struct {
		name      string
		supported bool
		err       func() error
	}{
		{"Catalog", true, func() error { _, err := s.Catalog(ctx); return err }},
		{"Search", true, func() error { _, err := s.Search(ctx, knowledge.Query{Text: "spring"}); return err }},
		{"ReadPage", true, func() error { _, err := s.ReadPage(ctx, pageID); return err }},
		{"ReadSource", true, func() error { _, err := s.ReadSource(ctx, sourceID); return err }},
		{"AddSource", caps.AddSource, func() error { _, err := s.AddSource(ctx, chatFacts(spaceNotes)); return err }},
		{"CatalogMarker", caps.CatalogMarker, func() error { _, err := s.CatalogMarker(ctx); return err }},
		{"Search with a vector", caps.VectorSearch, func() error {
			_, err := s.Search(ctx, knowledge.Query{Vector: nearGarden, Model: VectorModel})
			return err
		}},
		{"VectorsNeeded", caps.PageVectors, func() error { _, err := s.VectorsNeeded(ctx, VectorModel, 0); return err }},
		{"PutVector", caps.PageVectors, func() error {
			return s.PutVector(ctx, knowledge.PageVector{PageID: pageID, Revision: revision, Model: VectorModel, Vector: vecGarden})
		}},
		{"RemoveSource", caps.RemoveSource, func() error { return s.RemoveSource(ctx, sourceID) }},
	}
	for _, c := range calls {
		err := c.err()
		switch {
		case err == nil:
			p.errorf("%s with a cancelled context returned no error", c.name)
		case c.supported && !errors.Is(err, context.Canceled):
			p.errorf("%s with a cancelled context: error %v, want the context's error", c.name, err)
		}
	}
}

// ---- checks for a store that can be given content

func checkCatalog(p *probe) {
	s := p.fresh()
	before := p.catalog(s)
	c := base(false)
	p.h.Seed(p.t, s, c)
	entries := p.catalog(s)
	if len(entries) != len(before)+len(c.Pages) {
		p.errorf("the catalog lists %d pages after %d were added to %d", len(entries), len(c.Pages), len(before))
	}
	for _, page := range c.Pages {
		what := "catalog entry " + page.Space + "/" + page.Path
		e, ok := entryAt(entries, page.Space, page.Path)
		if !ok {
			p.errorf("%s is missing", what)
			continue
		}
		if e.Title != page.Title || e.Kind != page.Kind {
			p.errorf("%s: title %q and kind %q, want %q and %q", what, e.Title, e.Kind, page.Title, page.Kind)
		}
		p.label(what, e.Label, baseSensitive[page.Space])
	}
}

func checkRead(p *probe) {
	ctx := context.Background()
	c := base(false)
	s := p.seeded(c)
	entries := p.catalog(s)
	sources := map[string]SeedSource{}
	for _, src := range c.Sources {
		sources[src.Name] = src
	}
	for _, want := range c.Pages {
		what := "page " + want.Space + "/" + want.Path
		e, ok := entryAt(entries, want.Space, want.Path)
		if !ok {
			p.errorf("%s is not in the catalog", what)
			continue
		}
		page, err := s.ReadPage(ctx, e.ID)
		if err != nil {
			p.errorf("ReadPage of %s: %v", what, err)
			continue
		}
		if page.ID != e.ID || page.Space != e.Space || page.Path != e.Path || page.Title != e.Title || page.Kind != e.Kind || page.Label != e.Label {
			p.errorf("%s reads as %+v, which does not match its catalog entry %+v", what, page, e)
		}
		if strings.TrimSpace(page.Body) != want.Body {
			p.errorf("%s: body is %q, want %q", what, page.Body, want.Body)
		}
		if page.Revision == "" {
			p.errorf("%s has no revision", what)
		}
		p.label(what, page.Label, baseSensitive[want.Space])
		if len(page.Sources) != len(want.Cites) {
			p.errorf("%s cites %d sources, want %d", what, len(page.Sources), len(want.Cites))
			continue
		}
		for i, id := range page.Sources {
			wantSrc := sources[want.Cites[i]]
			got, err := s.ReadSource(ctx, id)
			if err != nil {
				p.errorf("ReadSource of %s, cited by %s: %v", wantSrc.Name, what, err)
				continue
			}
			swhat := "source " + wantSrc.Name
			if got.ID != id || got.Space != wantSrc.Space || got.Name != wantSrc.Name || got.Origin != wantSrc.Origin || got.Dated != wantSrc.Dated {
				p.errorf("%s reads as %+v, want %+v", swhat, got, wantSrc)
			}
			if strings.TrimSpace(got.Text) != wantSrc.Text {
				p.errorf("%s: text is %q, want %q", swhat, got.Text, wantSrc.Text)
			}
			p.label(swhat, got.Label, baseSensitive[wantSrc.Space])
		}
	}
}

func checkSearch(p *probe) {
	ctx := context.Background()
	s := p.seeded(base(false))
	garden, bread, sleep, ok := p.ids(s)
	if !ok {
		return
	}

	// One page, by a word only it holds.
	hits := p.search(s, knowledge.Query{Text: "tomatoes"}, baseSensitive)
	if h, ok := hitFor(hits, knowledge.HitPage, garden); !ok {
		p.errorf(`a search for "tomatoes" does not find the page that holds the word`)
	} else if h.Space != spaceHome || h.Path != pathGarden || h.Title != "Raised beds" {
		p.errorf("the hit for the garden page is %+v", h)
	}
	if _, ok := hitFor(hits, knowledge.HitPage, bread); ok {
		p.errorf(`a search for "tomatoes" finds a page without the word`)
	}

	// One source, by a word only it holds.
	hits = p.search(s, knowledge.Query{Text: "marigold"}, baseSensitive)
	found := false
	for _, h := range hits {
		if h.Type != knowledge.HitSource {
			continue
		}
		src, err := s.ReadSource(ctx, h.ID)
		if err != nil {
			p.errorf("ReadSource of a source hit: %v", err)
			continue
		}
		if src.Name == nameSeedOrder {
			found = true
			if h.Title != nameSeedOrder || h.Space != spaceHome {
				p.errorf("the hit for the seed order is %+v", h)
			}
		}
	}
	if !found {
		p.errorf(`a search for "marigold" does not find the source that holds the word`)
	}

	// The sensitive space: the page and its source, each marked.
	hits = p.search(s, knowledge.Query{Text: "insomnia"}, baseSensitive)
	if _, ok := hitFor(hits, knowledge.HitPage, sleep); !ok {
		p.errorf(`a search for "insomnia" does not find the sensitive page`)
	}
	sources := 0
	for _, h := range hits {
		if h.Type == knowledge.HitSource {
			sources++
		}
		if !h.Label.Sensitive() {
			p.errorf(`a hit for "insomnia", a word only the sensitive space holds, is not marked sensitive: %+v`, h)
		}
	}
	if sources == 0 {
		p.errorf(`a search for "insomnia" does not find the sensitive source`)
	}

	// A word in three things, and the limit.
	hits = p.search(s, knowledge.Query{Text: "spring"}, baseSensitive)
	if len(hits) < 3 {
		p.errorf(`a search for "spring" finds %d of the three things that hold the word`, len(hits))
	}
	if hits = p.search(s, knowledge.Query{Text: "spring", Limit: 1}, baseSensitive); len(hits) != 1 {
		p.errorf(`a search for "spring" with limit 1 returns %d hits`, len(hits))
	}

	// One space only.
	hits = p.search(s, knowledge.Query{Text: "spring", Space: spaceHome}, baseSensitive)
	if _, ok := hitFor(hits, knowledge.HitPage, garden); !ok {
		p.errorf(`a search for "spring" in the home space does not find the garden page`)
	}
	if hits = p.search(s, knowledge.Query{Text: "insomnia", Space: spaceHome}, baseSensitive); len(hits) != 0 {
		p.errorf(`a search of the home space finds %d hits for a word only the health space holds`, len(hits))
	}

	// Nothing to find.
	for _, q := range []knowledge.Query{
		{},
		{Space: spaceHome},
		{Text: noSuchWord},
		{Text: "spring", Space: "no-such-space"},
	} {
		if hits := p.search(s, q, baseSensitive); len(hits) != 0 {
			p.errorf("Search(%+v): %d hits, want none", q, len(hits))
		}
	}
}

// checkRelabel marks a space sensitive after the store has been read, and
// checks the next read says so. An adapter that remembers labels too long
// would send sensitive content to a model that may not have it.
func checkRelabel(p *probe) {
	ctx := context.Background()
	s := p.seeded(base(false))
	garden, _, _, ok := p.ids(s)
	if !ok {
		return
	}
	p.search(s, knowledge.Query{Text: "tomatoes"}, baseSensitive)

	p.h.Seed(p.t, s, Content{Sensitive: []string{spaceHome}})
	now := map[string]bool{spaceHome: true, spaceHealth: true}

	for _, e := range p.catalog(s) {
		if e.Space == spaceHome {
			p.label("catalog entry "+e.Space+"/"+e.Path+", after its space was made sensitive", e.Label, true)
		}
	}
	page, err := s.ReadPage(ctx, garden)
	if err != nil {
		p.errorf("ReadPage: %v", err)
		return
	}
	p.label("a page, after its space was made sensitive", page.Label, true)
	for _, id := range page.Sources {
		src, err := s.ReadSource(ctx, id)
		if err != nil {
			p.errorf("ReadSource: %v", err)
			continue
		}
		p.label("a source, after its space was made sensitive", src.Label, true)
	}
	hits := p.search(s, knowledge.Query{Text: "tomatoes"}, now)
	if _, ok := hitFor(hits, knowledge.HitPage, garden); !ok {
		p.errorf("the garden page is no longer found after its space was made sensitive")
	}
}

func checkMarker(p *probe) {
	ctx := context.Background()
	s := p.seeded(base(false))
	if !s.Capabilities().CatalogMarker {
		p.skipped = "the store has no CatalogMarker"
		return
	}
	marker := func(when string) string {
		m, err := s.CatalogMarker(ctx)
		if err != nil {
			p.errorf("CatalogMarker %s: %v", when, err)
		}
		if m == "" {
			p.errorf("CatalogMarker %s is empty", when)
		}
		return m
	}
	first := marker("after seeding")
	p.catalog(s)
	p.search(s, knowledge.Query{Text: "spring"}, baseSensitive)
	if again := marker("after reads only"); again != first {
		p.errorf("the marker changed from %q to %q when nothing but reads happened", first, again)
	}

	p.h.Seed(p.t, s, Content{Pages: []SeedPage{
		{Space: spaceHome, Path: "shed/tools", Title: "Tool shed", Kind: "note", Body: "The rake hangs by the door."},
	}})
	added := marker("after a page was added")
	if added == first {
		p.errorf("the marker is still %q after a page was added", first)
	}

	p.h.Seed(p.t, s, Content{Sensitive: []string{spaceHome}})
	if relabelled := marker("after a space was made sensitive"); relabelled == added {
		p.errorf("the marker is still %q after a space was made sensitive", added)
	}
}

func checkAddSource(p *probe) {
	ctx := context.Background()
	s := p.fresh()
	if !s.Capabilities().AddSource {
		p.skipped = "the store has no AddSource"
		return
	}

	// What is refused.
	noSpace, noName, noFacts, blankFact := chatFacts(""), chatFacts(spaceNotes), chatFacts(spaceNotes), chatFacts(spaceNotes)
	noName.Name = ""
	noFacts.Facts = nil
	blankFact.Facts = append([]knowledge.Fact{{Text: "  "}}, blankFact.Facts...)
	for name, n := range map[string]knowledge.NewSource{
		"no space": noSpace, "no name": noName, "no facts": noFacts, "a blank fact": blankFact,
	} {
		if _, err := s.AddSource(ctx, n); !errors.Is(err, knowledge.ErrInvalid) {
			p.errorf("AddSource of a source with %s: error %v, want ErrInvalid", name, err)
		}
	}

	// A source in a space the store does not hold yet.
	pagesBefore := len(p.catalog(s))
	n := chatFacts(spaceNotes)
	added, err := s.AddSource(ctx, n)
	if err != nil {
		p.errorf("AddSource: %v", err)
		return
	}
	if added.ID == "" || added.Space != n.Space || added.Name != n.Name || added.Origin != n.Origin || added.Dated != n.Dated {
		p.errorf("AddSource returned %+v for %+v", added, n)
	}
	if added.Label != knowledge.LabelNormal {
		p.errorf("a source added to a new space has label %q: a new space is a normal one", added.Label)
	}
	for _, f := range n.Facts {
		if !strings.Contains(added.Text, f.Text) {
			p.errorf("the added source's text does not hold the fact %q: %q", f.Text, added.Text)
		}
	}
	read, err := s.ReadSource(ctx, added.ID)
	if err != nil {
		p.errorf("ReadSource of the added source: %v", err)
	} else if read != added {
		p.errorf("the added source reads back as %+v, and AddSource returned %+v", read, added)
	}
	for _, q := range []knowledge.Query{{Text: "hinge"}, {Text: "hinge", Space: spaceNotes}} {
		hits := p.search(s, q, baseSensitive)
		if h, ok := hitFor(hits, knowledge.HitSource, added.ID); !ok {
			p.errorf("Search(%+v) does not find the added source", q)
		} else if h.Title != n.Name || h.Space != n.Space {
			p.errorf("the hit for the added source is %+v", h)
		}
	}
	if pagesAfter := len(p.catalog(s)); pagesAfter != pagesBefore {
		p.errorf("the catalog went from %d pages to %d when a source was added: a source is not a page", pagesBefore, pagesAfter)
	}
	second, err := s.AddSource(ctx, n)
	if err != nil {
		p.errorf("AddSource a second time: %v", err)
	} else if second.ID == added.ID {
		p.errorf("two added sources share the id %q", added.ID)
	}

	// A source in a sensitive space.
	p.h.Seed(p.t, s, base(false))
	sensitive, err := s.AddSource(ctx, chatFacts(spaceHealth))
	if err != nil {
		p.errorf("AddSource in a sensitive space: %v", err)
		return
	}
	p.label("a source added to a sensitive space", sensitive.Label, true)
	if read, err := s.ReadSource(ctx, sensitive.ID); err != nil {
		p.errorf("ReadSource of the source added to a sensitive space: %v", err)
	} else {
		p.label("a source added to a sensitive space, read back", read.Label, true)
	}
	hits := p.search(s, knowledge.Query{Text: "hinge"}, baseSensitive)
	if h, ok := hitFor(hits, knowledge.HitSource, sensitive.ID); !ok {
		p.errorf("a search does not find the source added to a sensitive space")
	} else {
		p.label("the hit for a source added to a sensitive space", h.Label, true)
	}
}

func checkVectorSearch(p *probe) {
	s := p.seeded(base(true))
	if !s.Capabilities().VectorSearch {
		p.skipped = "the store has no VectorSearch"
		return
	}
	garden, bread, sleep, ok := p.ids(s)
	if !ok {
		return
	}
	first := func(q knowledge.Query, id, name string) []knowledge.Hit {
		hits := p.search(s, q, baseSensitive)
		if len(hits) == 0 || hits[0].Type != knowledge.HitPage || hits[0].ID != id {
			p.errorf("a search with a vector nearest the %s page does not put it first: %+v", name, hits)
		}
		return hits
	}
	first(knowledge.Query{Vector: nearGarden, Model: VectorModel}, garden, "garden")
	first(knowledge.Query{Vector: nearBread, Model: VectorModel}, bread, "bread")
	first(knowledge.Query{Vector: nearSleep, Model: VectorModel, Space: spaceHealth}, sleep, "sleep")
	if hits := first(knowledge.Query{Vector: nearGarden, Model: VectorModel, Limit: 1}, garden, "garden"); len(hits) != 1 {
		p.errorf("a search with a vector and limit 1 returns %d hits", len(hits))
	}

	// Words and a vector together: what either finds is found.
	hits := p.search(s, knowledge.Query{Text: "tomatoes", Vector: nearBread, Model: VectorModel}, baseSensitive)
	if _, ok := hitFor(hits, knowledge.HitPage, garden); !ok {
		p.errorf("a search with words and a vector does not find the page the words match")
	}
	if _, ok := hitFor(hits, knowledge.HitPage, bread); !ok {
		p.errorf("a search with words and a vector does not find the page the vector is nearest")
	}
}

func checkPageVectors(p *probe) {
	ctx := context.Background()
	c := base(false)
	s := p.seeded(c)
	caps := s.Capabilities()
	if !caps.PageVectors {
		p.skipped = "the store has no PageVectors"
		return
	}
	garden, bread, sleep, ok := p.ids(s)
	if !ok {
		return
	}
	needs := func(when string) map[string]knowledge.PageNeed {
		list, err := s.VectorsNeeded(ctx, VectorModel, 0)
		if err != nil {
			p.errorf("VectorsNeeded %s: %v", when, err)
		}
		out := map[string]knowledge.PageNeed{}
		for _, n := range list {
			out[n.PageID] = n
		}
		return out
	}

	// Every page needs one at first.
	need := needs("after seeding")
	for id, want := range map[string]SeedPage{garden: c.Pages[0], bread: c.Pages[1], sleep: c.Pages[2]} {
		n, ok := need[id]
		what := "the need for page " + want.Space + "/" + want.Path
		if !ok {
			p.errorf("%s is missing: the page has no vector", what)
			continue
		}
		if n.Space != want.Space || n.Title != want.Title || n.Stale {
			p.errorf("%s is %+v", what, n)
		}
		p.label(what, n.Label, baseSensitive[want.Space])
	}
	if list, err := s.VectorsNeeded(ctx, VectorModel, 1); err != nil || len(list) != 1 {
		p.errorf("VectorsNeeded with limit 1: %d needs, error %v", len(list), err)
	}

	// What is refused.
	page, err := s.ReadPage(ctx, garden)
	if err != nil {
		p.errorf("ReadPage: %v", err)
		return
	}
	good := knowledge.PageVector{PageID: garden, Revision: page.Revision, Model: VectorModel, Vector: vecGarden}
	wrongRevision, noPage, noVector := good, good, good
	wrongRevision.Revision = "not-" + page.Revision
	noPage.PageID = noSuchID
	noVector.Vector = nil
	if err := s.PutVector(ctx, wrongRevision); !errors.Is(err, knowledge.ErrStale) {
		p.errorf("PutVector for a revision that is not the page's: error %v, want ErrStale", err)
	}
	if err := s.PutVector(ctx, noPage); !errors.Is(err, knowledge.ErrNotFound) {
		p.errorf("PutVector for a page that is not there: error %v, want ErrNotFound", err)
	}
	if err := s.PutVector(ctx, noVector); !errors.Is(err, knowledge.ErrInvalid) {
		p.errorf("PutVector with no vector: error %v, want ErrInvalid", err)
	}
	if _, still := needs("after three refused vectors")[garden]; !still {
		p.errorf("a refused vector was stored: the page no longer needs one")
	}

	// A vector that is taken.
	if err := s.PutVector(ctx, good); err != nil {
		p.errorf("PutVector: %v", err)
		return
	}
	need = needs("after one vector was stored")
	if _, still := need[garden]; still {
		p.errorf("the page still needs a vector after PutVector stored one")
	}
	if _, still := need[bread]; !still {
		p.errorf("a page with no vector is missing from VectorsNeeded after another page got one")
	}
	if caps.VectorSearch {
		hits := p.search(s, knowledge.Query{Vector: nearGarden, Model: VectorModel}, baseSensitive)
		if len(hits) == 0 || hits[0].Type != knowledge.HitPage || hits[0].ID != garden {
			p.errorf("a search by vector does not find the page whose vector was just stored: %+v", hits)
		}
	}

	// The page changes, so its vector is stale.
	changed := c.Pages[0]
	changed.Body = "The raised beds get beans this year."
	changed.Cites = nil
	p.h.Seed(p.t, s, Content{Pages: []SeedPage{changed}})
	if n, ok := needs("after the page changed")[garden]; !ok {
		p.errorf("a page that changed after its vector was stored is missing from VectorsNeeded")
	} else if !n.Stale {
		p.errorf("a page that changed after its vector was stored is listed as having no vector: %+v", n)
	}
}

// checkRemoveSource (P6-07): a removed source can no longer be read or
// found, a page that cited it no longer does and moves on a revision, and
// nothing else goes with it.
func checkRemoveSource(p *probe) {
	ctx := context.Background()
	s := p.seeded(base(false))
	if !s.Capabilities().RemoveSource {
		p.skipped = "the store has no RemoveSource"
		return
	}
	garden, _, sleep, ok := p.ids(s)
	if !ok {
		return
	}
	before, err := s.ReadPage(ctx, garden)
	if err != nil || len(before.Sources) != 1 {
		p.errorf("the raised beds page reads as %+v, error %v: it should cite the seed order", before, err)
		return
	}
	order := before.Sources[0]
	sleepPage, err := s.ReadPage(ctx, sleep)
	if err != nil || len(sleepPage.Sources) != 1 {
		p.errorf("the sleep page reads as %+v, error %v: it should cite the clinic letter", sleepPage, err)
		return
	}
	clinic := sleepPage.Sources[0]

	if err := s.RemoveSource(ctx, noSuchID); !errors.Is(err, knowledge.ErrNotFound) {
		p.errorf("RemoveSource of an id the store does not hold: error %v, want ErrNotFound", err)
	}
	if err := s.RemoveSource(ctx, order); err != nil {
		p.errorf("RemoveSource of the seed order: %v", err)
		return
	}
	if _, err := s.ReadSource(ctx, order); !errors.Is(err, knowledge.ErrNotFound) {
		p.errorf("ReadSource of a removed source: error %v, want ErrNotFound", err)
	}
	for _, q := range []knowledge.Query{{Text: "marigold"}, {Text: "marigold", Space: spaceHome}} {
		if _, found := hitFor(p.search(s, q, baseSensitive), knowledge.HitSource, order); found {
			p.errorf("Search(%+v) still finds the removed source", q)
		}
	}
	after, err := s.ReadPage(ctx, garden)
	switch {
	case err != nil:
		p.errorf("the page that cited the removed source cannot be read: %v; the page stays", err)
	case len(after.Sources) != 0:
		p.errorf("the page that cited the removed source still cites %v", after.Sources)
	case after.Revision == before.Revision:
		p.errorf("the page that cited the removed source keeps its revision %q: what it cites changed", after.Revision)
	case strings.TrimSpace(after.Body) != strings.TrimSpace(before.Body):
		p.errorf("the page that cited the removed source changed its body to %q", after.Body)
	}
	if _, found := hitFor(p.search(s, knowledge.Query{Text: "tomatoes"}, baseSensitive), knowledge.HitPage, garden); !found {
		p.errorf("the page that cited the removed source is no longer found")
	}
	if got, err := s.ReadSource(ctx, clinic); err != nil || got.ID != clinic {
		p.errorf("the source that was not removed reads as %+v, error %v", got, err)
	}
	if err := s.RemoveSource(ctx, order); !errors.Is(err, knowledge.ErrNotFound) {
		p.errorf("RemoveSource of a source removed already: error %v, want ErrNotFound", err)
	}

	// A source in a sensitive space goes the same way.
	if err := s.RemoveSource(ctx, clinic); err != nil {
		p.errorf("RemoveSource of the clinic letter, in a sensitive space: %v", err)
	} else if _, found := hitFor(p.search(s, knowledge.Query{Text: "autumn"}, baseSensitive), knowledge.HitSource, clinic); found {
		p.errorf("a search still finds the removed source of the sensitive space")
	}

	// And so does a source added through the interface.
	if !s.Capabilities().AddSource {
		return
	}
	added, err := s.AddSource(ctx, chatFacts(spaceNotes))
	if err != nil {
		p.errorf("AddSource: %v", err)
		return
	}
	if err := s.RemoveSource(ctx, added.ID); err != nil {
		p.errorf("RemoveSource of an added source: %v", err)
	} else if _, found := hitFor(p.search(s, knowledge.Query{Text: "hinge"}, baseSensitive), knowledge.HitSource, added.ID); found {
		p.errorf("a search still finds the added source after it was removed")
	}
}
