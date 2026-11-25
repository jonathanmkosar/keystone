package builtin

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/knowledgetest"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// Every name and every line of content in these tests is invented.

var bg = context.Background()

// newInstall makes an empty install in a folder of the test's own.
func newInstall(t *testing.T) *tenant.Install {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// newTenant adds a tenant and returns its open handle.
func newTenant(t *testing.T, in *tenant.Install, name string) *tenant.Store {
	t.Helper()
	ten, err := in.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := in.Store(ten.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ts.Close() })
	return ts
}

// openStore opens the built-in store the way the rest of Keystone does: by
// name, through the registry.
func openStore(t *testing.T, ts *tenant.Store) knowledge.Store {
	t.Helper()
	s, err := knowledge.Open(bg, Name, ts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func fresh(t *testing.T) (knowledge.Store, *tenant.Store) {
	t.Helper()
	ts := newTenant(t, newInstall(t), "Test Person One")
	return openStore(t, ts), ts
}

// seed puts the suite's content into a store with the package's own writing
// functions.
func seed(t *testing.T, s knowledge.Store, c knowledgetest.Content) {
	t.Helper()
	ts := s.(*store).t
	for _, space := range c.Sensitive {
		if err := SetLabel(bg, ts, space, knowledge.LabelSensitive); err != nil {
			t.Fatal(err)
		}
	}
	named := map[string]string{}
	for _, src := range c.Sources {
		added, err := AddText(bg, ts, NewText{Space: src.Space, Name: src.Name, Origin: src.Origin, Dated: src.Dated, Text: src.Text})
		if err != nil {
			t.Fatal(err)
		}
		named[src.Name] = added.ID
	}
	for _, sp := range c.Pages {
		p := NewPage{Space: sp.Space, Path: sp.Path, Title: sp.Title, Kind: sp.Kind, Body: sp.Body}
		for _, name := range sp.Cites {
			p.Sources = append(p.Sources, named[name])
		}
		page, err := WritePage(bg, ts, p)
		if err != nil {
			t.Fatal(err)
		}
		if sp.Vector != nil {
			err := s.PutVector(bg, knowledge.PageVector{PageID: page.ID, Revision: page.Revision, Model: knowledgetest.VectorModel, Vector: sp.Vector})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

// The item's proof: the built-in store passes the contract every knowledge
// store must, with every capability on.
func TestContract(t *testing.T) {
	knowledgetest.Run(t, knowledgetest.Harness{
		New:  func(t *testing.T) knowledge.Store { s, _ := fresh(t); return s },
		Seed: seed,
	})
}

func TestItClaimsEveryCapability(t *testing.T) {
	s, _ := fresh(t)
	want := knowledge.Capabilities{AddSource: true, CatalogMarker: true, VectorSearch: true, PageVectors: true}
	if got := s.Capabilities(); got != want {
		t.Errorf("Capabilities() = %+v, want %+v", got, want)
	}
}

// Every value the knowledge.adapter setting offers is an adapter this
// package's build can open for a tenant, and the built-in store is one.
func TestEveryOptionOfTheSettingOpens(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := settings.Lookup(reg, knowledge.SettingAdapter)
	if !ok {
		t.Fatalf("the registry has no %s setting", knowledge.SettingAdapter)
	}
	if !slices.Contains(st.Options, Name) {
		t.Errorf("%s does not offer %q: %v", knowledge.SettingAdapter, Name, st.Options)
	}
	ts := newTenant(t, newInstall(t), "Test Person One")
	for _, name := range st.Options {
		s, err := knowledge.Open(bg, name, ts)
		if err != nil {
			t.Errorf("the option %q does not open: %v", name, err)
			continue
		}
		if err := s.Close(); err != nil {
			t.Errorf("Close of %q: %v", name, err)
		}
	}
	for _, name := range knowledge.Adapters() {
		if !slices.Contains(st.Options, name) {
			t.Errorf("the adapter %q is registered and the setting does not offer it", name)
		}
	}
}

func TestItNeedsATenant(t *testing.T) {
	if s, err := knowledge.Open(bg, Name, nil); err == nil {
		t.Errorf("the built-in store opened with no tenant: %v", s)
	}
	for name, err := range map[string]error{
		"WritePage":  func() error { _, err := WritePage(bg, nil, NewPage{Space: "home", Path: "a", Title: "A"}); return err }(),
		"AddText":    func() error { _, err := AddText(bg, nil, NewText{Space: "home", Name: "a", Text: "a"}); return err }(),
		"SetLabel":   SetLabel(bg, nil, "home", knowledge.LabelSensitive),
		"RemovePage": RemovePage(bg, nil, "p1"),
	} {
		if err == nil {
			t.Errorf("%s worked with no tenant", name)
		}
	}
}

// The label is the guard of fixed rule 3. A space whose row in the spaces
// table is gone, or holds something that is not a label, must read as
// sensitive on every call that returns content.
func TestASpaceWithNoLabelReadsAsSensitive(t *testing.T) {
	for name, damage := range map[string]string{
		"no row":              "DELETE FROM knowledge_spaces",
		"no row and no check": "DROP TABLE knowledge_spaces; CREATE TABLE knowledge_spaces (name TEXT PRIMARY KEY, label TEXT); INSERT INTO knowledge_spaces VALUES('home', 'Normal'), ('other', 'normal')",
	} {
		t.Run(name, func(t *testing.T) {
			s, ts := fresh(t)
			src, err := AddText(bg, ts, NewText{Space: "home", Name: "seed-list.txt", Text: "Parsnip seed for the far bed."})
			if err != nil {
				t.Fatal(err)
			}
			page, err := WritePage(bg, ts, NewPage{Space: "home", Path: "garden/far-bed", Title: "Far bed", Body: "Parsnips go in the far bed.", Sources: []string{src.ID}})
			if err != nil {
				t.Fatal(err)
			}
			if page.Label != knowledge.LabelNormal || src.Label != knowledge.LabelNormal {
				t.Fatalf("a new space is normal: page %q, source %q", page.Label, src.Label)
			}
			if err := s.PutVector(bg, knowledge.PageVector{PageID: page.ID, Revision: page.Revision, Model: "test-embed", Vector: []float32{1, 0}}); err != nil {
				t.Fatal(err)
			}
			if _, err := WritePage(bg, ts, NewPage{Space: "home", Path: "garden/near-bed", Title: "Near bed", Body: "Leeks go in the near bed."}); err != nil {
				t.Fatal(err)
			}

			if _, err := ts.DB().Exec(damage); err != nil {
				t.Fatal(err)
			}

			sensitive := func(what string, l knowledge.Label) {
				t.Helper()
				if l != knowledge.LabelSensitive {
					t.Errorf("%s has label %q, want sensitive", what, l)
				}
			}
			entries, err := s.Catalog(bg)
			if err != nil || len(entries) != 2 {
				t.Fatalf("Catalog: %d entries, error %v", len(entries), err)
			}
			for _, e := range entries {
				sensitive("catalog entry "+e.Path, e.Label)
			}
			if p, err := s.ReadPage(bg, page.ID); err != nil {
				t.Error(err)
			} else {
				sensitive("the page", p.Label)
			}
			if got, err := s.ReadSource(bg, src.ID); err != nil {
				t.Error(err)
			} else {
				sensitive("the source", got.Label)
			}
			for _, q := range []knowledge.Query{
				{Text: "parsnips"},
				{Vector: []float32{1, 0}, Model: "test-embed"},
				{Text: "bed", Vector: []float32{1, 0}, Model: "test-embed"},
			} {
				hits, err := s.Search(bg, q)
				if err != nil || len(hits) == 0 {
					t.Errorf("Search(%+v): %d hits, error %v", q, len(hits), err)
				}
				for _, h := range hits {
					sensitive("a "+h.Type+" hit", h.Label)
				}
			}
			needs, err := s.VectorsNeeded(bg, 0)
			if err != nil || len(needs) != 1 {
				t.Fatalf("VectorsNeeded: %d, error %v", len(needs), err)
			}
			sensitive("a page needing a vector", needs[0].Label)
		})
	}
}

// Two tenants each have a built-in store, and neither sees the other's.
func TestTwoTenantsDoNotShare(t *testing.T) {
	in := newInstall(t)
	oneT, twoT := newTenant(t, in, "Test Person One"), newTenant(t, in, "Test Person Two")
	one, two := openStore(t, oneT), openStore(t, twoT)

	page, err := WritePage(bg, oneT, NewPage{Space: "home", Path: "bees", Title: "Beehive", Body: "The hive is inspected on Sundays."})
	if err != nil {
		t.Fatal(err)
	}
	src, err := one.AddSource(bg, knowledge.NewSource{Space: "home", Name: "chat-facts", Facts: []knowledge.Fact{{Text: "The hive has a new queen."}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := one.PutVector(bg, knowledge.PageVector{PageID: page.ID, Revision: page.Revision, Model: "test-embed", Vector: []float32{1, 0}}); err != nil {
		t.Fatal(err)
	}

	if entries, err := two.Catalog(bg); err != nil || len(entries) != 0 {
		t.Errorf("tenant two's catalog lists %d of tenant one's pages, error %v", len(entries), err)
	}
	if _, err := two.ReadPage(bg, page.ID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("tenant two read tenant one's page by its id: %v", err)
	}
	if _, err := two.ReadSource(bg, src.ID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("tenant two read tenant one's source by its id: %v", err)
	}
	for _, q := range []knowledge.Query{{Text: "hive"}, {Vector: []float32{1, 0}, Model: "test-embed"}} {
		if hits, err := two.Search(bg, q); err != nil || len(hits) != 0 {
			t.Errorf("tenant two's Search(%+v) found %d of tenant one's things, error %v", q, len(hits), err)
		}
	}
	if needs, err := two.VectorsNeeded(bg, 0); err != nil || len(needs) != 0 {
		t.Errorf("tenant two is asked for vectors for %d of tenant one's pages, error %v", len(needs), err)
	}
	if err := two.PutVector(bg, knowledge.PageVector{PageID: page.ID, Revision: page.Revision, Model: "test-embed", Vector: []float32{0, 1}}); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("tenant two stored a vector for tenant one's page: %v", err)
	}
	if hits, err := one.Search(bg, knowledge.Query{Text: "hive"}); err != nil || len(hits) != 2 {
		t.Errorf("tenant one finds %d of its own two things, error %v", len(hits), err)
	}
}

// What is written stays: a second handle on the same tenant reads it, and
// the store holds no content of its own between calls.
func TestASecondHandleReadsTheSame(t *testing.T) {
	a, ts := fresh(t)
	b := openStore(t, ts)
	if _, err := WritePage(bg, ts, NewPage{Space: "home", Path: "pond", Title: "Pond", Body: "The pond is topped up from the rain barrel."}); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]knowledge.Store{"a closed handle's twin": b, "a new handle": openStore(t, ts)} {
		if hits, err := s.Search(bg, knowledge.Query{Text: "barrel"}); err != nil || len(hits) != 1 {
			t.Errorf("%s finds %d hits, error %v", name, len(hits), err)
		}
	}
	// Closing a store does not close the tenant's handle, which is not the
	// store's to close.
	if err := ts.DB().Ping(); err != nil {
		t.Errorf("closing the store closed the tenant's database: %v", err)
	}
}

func TestWritePage(t *testing.T) {
	s, ts := fresh(t)
	marker := func() string {
		t.Helper()
		m, err := s.CatalogMarker(bg)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	src, err := AddText(bg, ts, NewText{Space: "home", Name: "note.txt", Text: "The gate latch sticks in the wet."})
	if err != nil {
		t.Fatal(err)
	}
	before := marker()
	first, err := WritePage(bg, ts, NewPage{Space: "home", Path: "gate", Title: "Gate", Kind: "note", Body: "The latch sticks.", Sources: []string{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision == "" || first.ID == "" || !slices.Equal(first.Sources, []string{src.ID}) {
		t.Errorf("the page as written: %+v", first)
	}
	added := marker()
	if added == before {
		t.Error("the marker did not change when a page was added")
	}

	// The same page again changes nothing, so a vector stays good.
	if err := s.PutVector(bg, knowledge.PageVector{PageID: first.ID, Revision: first.Revision, Model: "test-embed", Vector: []float32{1, 0}}); err != nil {
		t.Fatal(err)
	}
	same, err := WritePage(bg, ts, NewPage{Space: "home", Path: "gate", Title: "Gate", Kind: "note", Body: "The latch sticks.", Sources: []string{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if same.ID != first.ID || same.Revision != first.Revision {
		t.Errorf("writing the same page again gave id %s revision %s, want %s and %s", same.ID, same.Revision, first.ID, first.Revision)
	}
	if needs, err := s.VectorsNeeded(bg, 0); err != nil || len(needs) != 0 {
		t.Errorf("an unchanged page needs a vector again: %+v, error %v", needs, err)
	}
	if marker() != added {
		t.Error("the marker changed when the same page was written again")
	}

	// A new body is a new revision under the same id. The catalog is the
	// same, so the marker is too.
	changed, err := WritePage(bg, ts, NewPage{Space: "home", Path: "gate", Title: "Gate", Kind: "note", Body: "The latch was oiled."})
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID != first.ID || changed.Revision == first.Revision || len(changed.Sources) != 0 {
		t.Errorf("the changed page: %+v", changed)
	}
	if marker() != added {
		t.Error("the marker changed when only a page's body did")
	}
	if hits, _ := s.Search(bg, knowledge.Query{Text: "sticks", Space: "home"}); len(hits) != 1 || hits[0].Type != knowledge.HitSource {
		t.Errorf("the old body is still found, or the source is not: %+v", hits)
	}
	if hits, _ := s.Search(bg, knowledge.Query{Text: "oiled"}); len(hits) != 1 || hits[0].ID != first.ID {
		t.Errorf("the new body is not found: %+v", hits)
	}

	// A new title changes the catalog.
	if _, err := WritePage(bg, ts, NewPage{Space: "home", Path: "gate", Title: "Garden gate", Kind: "note", Body: "The latch was oiled."}); err != nil {
		t.Fatal(err)
	}
	renamed := marker()
	if renamed == added {
		t.Error("the marker did not change when a page's title did")
	}

	// Removing the page takes it out of the catalog, the search and the
	// list of pages needing a vector. Its source stays.
	if err := RemovePage(bg, ts, first.ID); err != nil {
		t.Fatal(err)
	}
	if marker() == renamed {
		t.Error("the marker did not change when a page was removed")
	}
	if _, err := s.ReadPage(bg, first.ID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("a removed page still reads: %v", err)
	}
	if entries, _ := s.Catalog(bg); len(entries) != 0 {
		t.Errorf("a removed page is still in the catalog: %+v", entries)
	}
	if hits, _ := s.Search(bg, knowledge.Query{Text: "oiled latch", Vector: []float32{1, 0}, Model: "test-embed"}); len(hits) != 1 || hits[0].ID != src.ID {
		t.Errorf("after the page was removed a search finds %+v, want its source alone", hits)
	}
	if needs, _ := s.VectorsNeeded(bg, 0); len(needs) != 0 {
		t.Errorf("a removed page needs a vector: %+v", needs)
	}
	if err := RemovePage(bg, ts, first.ID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("removing a page twice: %v, want ErrNotFound", err)
	}
	if _, err := s.ReadSource(bg, src.ID); err != nil {
		t.Errorf("the source went with the page that cited it: %v", err)
	}
}

func TestWhatIsRefused(t *testing.T) {
	s, ts := fresh(t)
	good := NewPage{Space: "home", Path: "shed/tools", Title: "Tools", Body: "The spade is by the door."}
	pages := map[string]func(p *NewPage){
		"no space":                  func(p *NewPage) { p.Space = "" },
		"a space with a slash":      func(p *NewPage) { p.Space = "home/other" },
		"a space of two dots":       func(p *NewPage) { p.Space = ".." },
		"a space with a space end":  func(p *NewPage) { p.Space = "home " },
		"no path":                   func(p *NewPage) { p.Path = "" },
		"a path that climbs":        func(p *NewPage) { p.Path = "../other/tools" },
		"a path from the root":      func(p *NewPage) { p.Path = "/shed/tools" },
		"a path with an empty part": func(p *NewPage) { p.Path = "shed//tools" },
		"a path with a backslash":   func(p *NewPage) { p.Path = `shed\tools` },
		"no title":                  func(p *NewPage) { p.Title = " " },
		"a citation that is no id":  func(p *NewPage) { p.Sources = []string{"p1"} },
		"a citation of no source":   func(p *NewPage) { p.Sources = []string{"s999"} },
	}
	for name, spoil := range pages {
		p := good
		spoil(&p)
		if _, err := WritePage(bg, ts, p); !errors.Is(err, knowledge.ErrInvalid) {
			t.Errorf("WritePage with %s: error %v, want ErrInvalid", name, err)
		}
	}
	if entries, _ := s.Catalog(bg); len(entries) != 0 {
		t.Errorf("a refused page was written: %+v", entries)
	}
	for name, n := range map[string]NewText{
		"no space": {Name: "a.txt", Text: "words"},
		"no name":  {Space: "home", Text: "words"},
		"no text":  {Space: "home", Name: "a.txt", Text: " \n"},
	} {
		if _, err := AddText(bg, ts, n); !errors.Is(err, knowledge.ErrInvalid) {
			t.Errorf("AddText with %s: error %v, want ErrInvalid", name, err)
		}
	}
	for _, label := range []knowledge.Label{"", "Normal", "private"} {
		if err := SetLabel(bg, ts, "home", label); !errors.Is(err, knowledge.ErrInvalid) {
			t.Errorf("SetLabel(%q): error %v, want ErrInvalid", label, err)
		}
	}
	if err := SetLabel(bg, ts, "", knowledge.LabelSensitive); !errors.Is(err, knowledge.ErrInvalid) {
		t.Errorf("SetLabel with no space: error %v, want ErrInvalid", err)
	}

	page, err := WritePage(bg, ts, good)
	if err != nil {
		t.Fatal(err)
	}
	nan := float32(math.NaN())
	for name, v := range map[string]knowledge.PageVector{
		"no model":   {PageID: page.ID, Revision: page.Revision, Vector: []float32{1, 0}},
		"a NaN":      {PageID: page.ID, Revision: page.Revision, Model: "test-embed", Vector: []float32{1, nan}},
		"all zeros":  {PageID: page.ID, Revision: page.Revision, Model: "test-embed", Vector: []float32{0, 0}},
		"no numbers": {PageID: page.ID, Revision: page.Revision, Model: "test-embed"},
	} {
		if err := s.PutVector(bg, v); !errors.Is(err, knowledge.ErrInvalid) {
			t.Errorf("PutVector with %s: error %v, want ErrInvalid", name, err)
		}
	}
	if _, err := s.Search(bg, knowledge.Query{Vector: []float32{1, 0}}); !errors.Is(err, knowledge.ErrInvalid) {
		t.Errorf("Search with a vector and no model: error %v, want ErrInvalid", err)
	}
	for _, id := range []string{"1", "p", "p0", "p01", "p-1", "s1", "p1 ", "P1"} {
		if id == page.ID {
			continue
		}
		if _, err := s.ReadPage(bg, id); !errors.Is(err, knowledge.ErrNotFound) {
			t.Errorf("ReadPage(%q): error %v, want ErrNotFound", id, err)
		}
	}
}

// A search is by any of the words, stemmed, and nothing in the text is read
// as search syntax.
func TestSearchByWords(t *testing.T) {
	s, ts := fresh(t)
	for path, body := range map[string]string{
		"orchard": "The apple trees were pruned in February. The apple store is dry.",
		"hens":    "The hens lay fewer eggs in winter.",
		"roof":    "A slate slipped on the north side.",
	} {
		if _, err := WritePage(bg, ts, NewPage{Space: "home", Path: path, Title: "About the " + path, Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	paths := func(q string) []string {
		t.Helper()
		hits, err := s.Search(bg, knowledge.Query{Text: q})
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		var out []string
		for _, h := range hits {
			out = append(out, h.Path)
		}
		return out
	}
	// A whole question finds the page that holds some of its words.
	if got := paths("When did we prune the apples?"); len(got) == 0 || got[0] != "orchard" {
		t.Errorf("a question about pruning apples finds %v, want the orchard page first", got)
	}
	// A word in its other form.
	if got := paths("egg"); !slices.Equal(got, []string{"hens"}) {
		t.Errorf(`"egg" finds %v, want the hens page`, got)
	}
	// A word in a title.
	if got := paths("roof"); !slices.Equal(got, []string{"roof"}) {
		t.Errorf(`"roof" finds %v, want the roof page`, got)
	}
	// Search syntax is plain text here.
	for _, q := range []string{`"`, `apple"`, `NOT hens`, `title:roof`, `hens AND`, `(`, `*`, `apple NEAR(trees`, `^apple`, `a-b`, `-`, `OR`} {
		if _, err := s.Search(bg, knowledge.Query{Text: q}); err != nil {
			t.Errorf("Search(%q): %v", q, err)
		}
	}
	if got := paths(`"?!`); len(got) != 0 {
		t.Errorf("a search with no word in it finds %v", got)
	}
	// The snippet is cut to the setting's length.
	long := strings.Repeat("filler ", 200) + "heron " + strings.Repeat("filler ", 200)
	if _, err := WritePage(bg, ts, NewPage{Space: "home", Path: "long", Title: "Long", Body: long}); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(bg, knowledge.Query{Text: "heron"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("%d hits, error %v", len(hits), err)
	}
	if n := len(strings.Fields(hits[0].Snippet)); n > 30 || !strings.Contains(hits[0].Snippet, "heron") {
		t.Errorf("the snippet has %d words and should show the match: %q", n, hits[0].Snippet)
	}
}

// A vector is compared only with vectors of its own model and size, and a
// vector kept from an older revision is still searched until a new one
// arrives.
func TestSearchByVector(t *testing.T) {
	s, ts := fresh(t)
	write := func(path, body, model string, vec []float32) knowledge.Page {
		t.Helper()
		p, err := WritePage(bg, ts, NewPage{Space: "home", Path: path, Title: path, Body: body})
		if err != nil {
			t.Fatal(err)
		}
		if vec != nil {
			if err := s.PutVector(bg, knowledge.PageVector{PageID: p.ID, Revision: p.Revision, Model: model, Vector: vec}); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	a := write("a", "Alpha.", "test-embed", []float32{1, 0})
	b := write("b", "Beta.", "test-embed", []float32{0.6, 0.8})
	write("c", "Gamma.", "other-embed", []float32{1, 0})
	write("d", "Delta.", "test-embed", []float32{1, 0, 0})
	write("e", "Epsilon.", "test-embed", []float32{-1, 0})
	write("f", "Zeta.", "", nil)

	ids := func(q knowledge.Query) []string {
		t.Helper()
		hits, err := s.Search(bg, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range hits {
			out = append(out, h.ID)
		}
		return out
	}
	q := knowledge.Query{Vector: []float32{1, 0}, Model: "test-embed"}
	if got := ids(q); !slices.Equal(got, []string{a.ID, b.ID}) {
		t.Errorf("the vector finds %v, want %v: its own model and size, nearest first, and nothing that points away", got, []string{a.ID, b.ID})
	}
	// The page changes. Its old vector is stale and still found.
	if _, err := WritePage(bg, ts, NewPage{Space: "home", Path: "a", Title: "a", Body: "Alpha, changed."}); err != nil {
		t.Fatal(err)
	}
	if got := ids(q); !slices.Equal(got, []string{a.ID, b.ID}) {
		t.Errorf("after the page changed the vector finds %v", got)
	}
	needs, err := s.VectorsNeeded(bg, 0)
	if err != nil {
		t.Fatal(err)
	}
	stale := map[string]bool{}
	for _, n := range needs {
		stale[n.PageID] = n.Stale
	}
	if len(needs) != 2 || !stale[a.ID] {
		t.Errorf("the pages needing a vector: %+v, want the changed page as stale and the page with none", needs)
	}
	// A limit of one, with words and a vector, returns the best of both.
	if got := ids(knowledge.Query{Text: "beta", Vector: []float32{0.6, 0.8}, Model: "test-embed", Limit: 1}); !slices.Equal(got, []string{b.ID}) {
		t.Errorf("words and a vector for the same page, limit 1: %v", got)
	}
}

// The limits come from settings, and a person's own value is used.
func TestLimitsComeFromSettings(t *testing.T) {
	ts := newTenant(t, newInstall(t), "Test Person One")
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	if err := tenantsettings.Set(bg, ts, reg, tenantsettings.Person, SettingSearchResults, "2"); err != nil {
		t.Fatal(err)
	}
	s := openStore(t, ts)
	for _, path := range []string{"a", "b", "c"} {
		if _, err := WritePage(bg, ts, NewPage{Space: "home", Path: path, Title: path, Body: "The kettle is descaled monthly."}); err != nil {
			t.Fatal(err)
		}
	}
	if hits, err := s.Search(bg, knowledge.Query{Text: "kettle"}); err != nil || len(hits) != 2 {
		t.Errorf("with the limit set to 2 a search returns %d hits, error %v", len(hits), err)
	}
	if hits, err := s.Search(bg, knowledge.Query{Text: "kettle", Limit: 3}); err != nil || len(hits) != 3 {
		t.Errorf("a search that names its own limit of 3 returns %d hits, error %v", len(hits), err)
	}
	// A value out of range stops the store from opening.
	if err := tenantsettings.Set(bg, ts, reg, tenantsettings.Person, SettingSnippetWords, "0"); err != nil {
		t.Fatal(err)
	}
	if s, err := knowledge.Open(bg, Name, ts); err == nil {
		t.Errorf("the store opened with a snippet of 0 words: %v", s)
	}
}
