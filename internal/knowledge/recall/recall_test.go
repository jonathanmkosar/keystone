package recall

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Every name and every line of content in these tests is invented.

var bg = context.Background()

// defaults reads the options the way a caller does, from the registry's
// defaults, with the built-in store's threshold. It also proves the
// settings are there.
func defaults(t *testing.T) (Options, LookupOptions) {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	text := func(key string) string {
		st, ok := settings.Lookup(reg, key)
		if !ok {
			t.Fatalf("%s is not in the settings registry", key)
		}
		return st.Default
	}
	whole := func(key string) int {
		n, err := strconv.Atoi(text(key))
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return n
	}
	min, err := strconv.ParseFloat(text(SettingMinScore(builtin.Name)), 64)
	if err != nil {
		t.Fatal(err)
	}
	o := Options{
		MinScore: min, SearchResults: whole(SettingSearchResults), MaxItems: whole(SettingMaxItems),
		ItemMaxChars: whole(SettingItemMaxChars), QueryMaxChars: whole(SettingQueryMaxChars),
		IgnoreWords: strings.Fields(text(SettingIgnoreWords)),
	}
	if err := CheckOptions(o); err != nil {
		t.Fatalf("the registry's defaults are refused: %v", err)
	}
	lo := LookupOptions{MaxResults: whole(SettingLookupResults), MaxChars: whole(SettingLookupMaxChars)}
	if err := CheckLookupOptions(lo); err != nil {
		t.Fatalf("the registry's defaults are refused: %v", err)
	}
	for _, key := range []string{SettingOn, SettingSearchFirstText} {
		text(key)
	}
	if d, err := time.ParseDuration(text(SettingTimeout)); err != nil || d <= 0 {
		t.Fatalf("%s is %q", SettingTimeout, text(SettingTimeout))
	}
	return o, lo
}

// Every adapter a person can choose, but none, has a threshold of its own.
func TestEveryAdapterHasAThreshold(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := settings.Lookup(reg, knowledge.SettingAdapter)
	if !ok {
		t.Fatal("the adapter setting is missing")
	}
	for _, name := range st.Options {
		if name == knowledge.None {
			continue
		}
		th, ok := settings.Lookup(reg, SettingMinScore(name))
		if !ok || th.Type != "float" {
			t.Errorf("the adapter %s has no relevance threshold: want a float setting %s", name, SettingMinScore(name))
		}
	}
}

// world is a built-in store with three invented spaces: two normal ones
// and health, which is sensitive.
type world struct {
	ts                         *tenant.Store
	ks                         knowledge.Store
	beans, roof, sleep, letter string // ids
}

func newWorld(t *testing.T) world {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ten, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	ts, err := in.Store(ten.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ts.Close() })
	w := world{ts: ts}
	page := func(space, path, title, kind, body string) string {
		p, err := builtin.WritePage(bg, ts, builtin.NewPage{Space: space, Path: path, Title: title, Kind: kind, Body: body})
		if err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	w.beans = page("garden", "plants/runner-beans", "Runner beans", "plant",
		"Runner beans go in the north bed after the last frost. They climb the hazel poles and want water every evening in July.")
	page("garden", "plants/tomatoes", "Tomatoes", "plant", "Tomatoes stay in the greenhouse until June. Pinch out the side shoots.")
	w.roof = page("home", "projects/shed-roof", "Shed roof", "project",
		"The shed roof needs new felt before winter. Quoted at two rolls and a tin of adhesive.")
	page("home", "accounts/water", "Water account", "account", "The water account is paid each quarter by the house.")
	w.sleep = page("health", "notes/sleep", "Sleep notes", "note",
		"Melatonin helped with the insomnia in March. The clinic suggested no screens after nine.")
	src, err := builtin.AddText(bg, ts, builtin.NewText{Space: "health", Name: "clinic-letter.txt", Origin: "a letter", Dated: "2026-03-14",
		Text: "The clinic letter confirms the insomnia review and a follow-up appointment in May."})
	if err != nil {
		t.Fatal(err)
	}
	w.letter = src.ID
	if err := builtin.SetLabel(bg, ts, "health", knowledge.LabelSensitive); err != nil {
		t.Fatal(err)
	}
	if w.ks, err = knowledge.Open(bg, builtin.Name, ts); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.ks.Close() })
	return w
}

func texts(items []Item) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.Label + "\n" + it.Text + "\n")
	}
	return b.String()
}

// A message about something in the store brings it, with a line that says
// where it came from. A message about nothing in the store brings nothing.
func TestAMessageBringsWhatTheStoreHolds(t *testing.T) {
	w := newWorld(t)
	o, _ := defaults(t)

	f, err := Find(bg, w.ks, "When do the runner beans go in?", o)
	if err != nil {
		t.Fatal(err)
	}
	for _, may := range []bool{false, true} {
		out := f.For(may)
		if len(out.Items) == 0 || !strings.Contains(out.Items[0].Text, "north bed after the last frost") {
			t.Fatalf("may read sensitive %v: the beans page was not recalled first: %+v", may, out.Record)
		}
		first := out.Items[0]
		if !strings.Contains(first.Text, `Page "Runner beans" in the space "garden", page id `+w.beans) || !strings.Contains(first.Label, "garden: Runner beans (page "+w.beans+")") {
			t.Errorf("the item does not say where it came from: %q / %q", first.Label, first.Text)
		}
		if out.Sensitive || out.Record.Sensitive || out.Record.Kept != len(out.Items) || out.Record.Found != out.Record.Kept+out.Record.Dropped {
			t.Errorf("the record does not add up: %+v", out.Record)
		}
		if strings.Contains(texts(out.Items), "Shed roof") {
			t.Errorf("a page the message is not about was recalled: %+v", out.Record)
		}
	}

	if q := Query("When do the Runner beans go in, and WHEN do the beans come out?", o); q != "runner beans go come" {
		t.Errorf("the message is searched as %q", q)
	}
	for _, message := range []string{
		"What is the capital of a country far away?",
		"Is it the one that we had when they were there?",
		"   ",
	} {
		f, err := Find(bg, w.ks, message, o)
		if err != nil {
			t.Fatal(err)
		}
		if out := f.For(true); len(out.Items) != 0 || out.Sensitive {
			t.Errorf("%q recalled %d items: %+v", message, len(out.Items), out.Record)
		}
	}
}

// Fixed rule 3. What a sensitive space holds is given to a model that may
// read it, and the call is then marked sensitive. A model that may not is
// given none of it, and the record says how many hits were held back.
func TestSensitiveHitsAreDroppedForAModelThatMayNotReadThem(t *testing.T) {
	w := newWorld(t)
	o, _ := defaults(t)
	f, err := Find(bg, w.ks, "What helped with the insomnia, and what did the clinic letter say?", o)
	if err != nil {
		t.Fatal(err)
	}

	open := f.For(false)
	if got := texts(open.Items); got != "" {
		t.Errorf("a model that may not read sensitive content was given:\n%s", got)
	}
	if open.Sensitive || open.Record.Withheld != 2 || open.Record.Kept != 0 {
		t.Errorf("the open record: %+v", open.Record)
	}
	for _, h := range open.Record.Hits {
		if h.Sensitive && (h.Kept || h.Reason != ReasonSensitive) {
			t.Errorf("a sensitive hit is recorded as %+v", h)
		}
	}

	whole := f.For(true)
	got := texts(whole.Items)
	if !strings.Contains(got, "Melatonin helped") || !strings.Contains(got, "follow-up appointment in May") {
		t.Errorf("a model that may read sensitive content was not given the page and the source:\n%s", got)
	}
	if !whole.Sensitive || !whole.Record.Sensitive || whole.Record.Withheld != 0 {
		t.Errorf("a call that holds sensitive items is not marked: %+v", whole.Record)
	}
	if !strings.Contains(got, `Source "clinic-letter.txt" in the space "health", source id `+w.letter+", dated 2026-03-14:") {
		t.Errorf("the source does not say where it came from:\n%s", got)
	}

	// A space made sensitive is held back at the next search.
	if err := builtin.SetLabel(bg, w.ts, "garden", knowledge.LabelSensitive); err != nil {
		t.Fatal(err)
	}
	f, err = Find(bg, w.ks, "When do the runner beans go in?", o)
	if err != nil {
		t.Fatal(err)
	}
	if out := f.For(false); len(out.Items) != 0 || out.Record.Withheld == 0 {
		t.Errorf("a space made sensitive was still recalled: %+v", out.Record)
	}
}

// stub is a store that answers from fixed values, for what the built-in
// store cannot be made to do: a blank label, a label that changes between
// the search and the read, a page that cannot be read, a store that fails.
type stub struct {
	knowledge.Store // nil: only the calls below are made
	hits            []knowledge.Hit
	pages           map[string]knowledge.Page
	sources         map[string]knowledge.Source
	searchErr       error
	searches        int
	last            knowledge.Query
}

func (s *stub) Search(ctx context.Context, q knowledge.Query) ([]knowledge.Hit, error) {
	s.searches++
	s.last = q
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.hits, s.searchErr
}

func (s *stub) ReadPage(ctx context.Context, id string) (knowledge.Page, error) {
	if p, ok := s.pages[id]; ok {
		return p, nil
	}
	return knowledge.Page{}, knowledge.ErrNotFound
}

func (s *stub) ReadSource(ctx context.Context, id string) (knowledge.Source, error) {
	if src, ok := s.sources[id]; ok {
		return src, nil
	}
	return knowledge.Source{}, knowledge.ErrNotFound
}

// A missing label reads as sensitive, wherever it is missing: on the hit,
// or on the page that is read. A page that became sensitive after the
// search found it is sensitive.
func TestAMissingLabelReadsAsSensitive(t *testing.T) {
	o, _ := defaults(t)
	o.MinScore = 0
	s := &stub{
		hits: []knowledge.Hit{
			{Type: knowledge.HitPage, ID: "a", Space: "one", Label: "", Title: "No label on the hit", Score: 5},
			{Type: knowledge.HitPage, ID: "b", Space: "two", Label: knowledge.LabelNormal, Title: "No label on the page", Score: 4},
			{Type: knowledge.HitPage, ID: "c", Space: "three", Label: knowledge.LabelNormal, Title: "Made sensitive since", Score: 3},
			{Type: knowledge.HitSource, ID: "d", Space: "four", Label: "Normal", Title: "A label nobody knows", Score: 2},
			{Type: knowledge.HitPage, ID: "e", Space: "five", Label: knowledge.LabelNormal, Title: "Plain", Score: 1},
		},
		pages: map[string]knowledge.Page{
			"a": {ID: "a", Space: "one", Label: knowledge.LabelNormal, Title: "No label on the hit", Body: "first secret"},
			"b": {ID: "b", Space: "two", Title: "No label on the page", Body: "second secret"},
			"c": {ID: "c", Space: "three", Label: knowledge.LabelSensitive, Title: "Made sensitive since", Body: "third secret"},
			"e": {ID: "e", Space: "five", Label: knowledge.LabelNormal, Title: "Plain", Body: "nothing to hide"},
		},
		sources: map[string]knowledge.Source{"d": {ID: "d", Space: "four", Label: "Normal", Name: "A label nobody knows", Text: "fourth secret"}},
	}
	f, err := Find(bg, s, "anything", o)
	if err != nil {
		t.Fatal(err)
	}
	open := f.For(false)
	if got := texts(open.Items); strings.Contains(got, "secret") || !strings.Contains(got, "nothing to hide") || len(open.Items) != 1 {
		t.Errorf("a model that may not read sensitive content was given:\n%s", got)
	}
	if open.Record.Withheld != 4 || open.Sensitive {
		t.Errorf("the record: %+v", open.Record)
	}
	if whole := f.For(true); len(whole.Items) != 5 || !whole.Sensitive {
		t.Errorf("a model that may read sensitive content was given %d of 5 items", len(whole.Items))
	}
}

// The threshold, the limit on items, the size of an item and the size of
// the query each hold, and the record says why a hit was left out.
func TestTheLimitsHold(t *testing.T) {
	long := strings.Repeat("word ", 2000)
	s := &stub{pages: map[string]knowledge.Page{}}
	add := func(id string, label knowledge.Label, score float64) {
		s.hits = append(s.hits, knowledge.Hit{Type: knowledge.HitPage, ID: id, Space: "s", Label: label, Title: "Page " + id, Score: score})
		s.pages[id] = knowledge.Page{ID: id, Space: "s", Label: label, Title: "Page " + id, Body: id + " " + long}
	}
	add("s1", knowledge.LabelSensitive, 9)
	add("s2", knowledge.LabelSensitive, 8)
	add("n1", knowledge.LabelNormal, 7)
	add("s3", knowledge.LabelSensitive, 6)
	add("n2", knowledge.LabelNormal, 5)
	add("n3", knowledge.LabelNormal, 4)
	add("low", knowledge.LabelNormal, 0.5)
	s.hits = append(s.hits, knowledge.Hit{Type: knowledge.HitPage, ID: "gone", Space: "s", Label: knowledge.LabelNormal, Title: "Gone", Score: 3})
	delete(s.pages, "gone")

	o := Options{MinScore: 1, SearchResults: 7, MaxItems: 2, ItemMaxChars: 300, QueryMaxChars: 12}
	f, err := Find(bg, s, "a message that is longer than twelve characters", o)
	if err != nil {
		t.Fatal(err)
	}
	if s.last.Text != "message that" || s.last.Limit != 7 || len(s.last.Vector) != 0 {
		t.Errorf("the store was searched with %+v", s.last)
	}
	kept := func(out Outcome) string {
		var ids []string
		for _, h := range out.Record.Hits {
			if h.Kept {
				ids = append(ids, h.ID)
			}
		}
		return strings.Join(ids, " ")
	}
	reasons := func(out Outcome) map[string]string {
		m := map[string]string{}
		for _, h := range out.Record.Hits {
			m[h.ID] = h.Reason
		}
		return m
	}
	// Each kind of model is given its own best two: the sensitive hits do
	// not use up the room of a model that may not read them.
	whole, open := f.For(true), f.For(false)
	if kept(whole) != "s1 s2" || kept(open) != "n1 n2" {
		t.Errorf("kept %q for a model that may read sensitive content and %q for one that may not", kept(whole), kept(open))
	}
	if r := reasons(open); r["s1"] != ReasonSensitive || r["s3"] != ReasonSensitive || r["n3"] != ReasonOverLimit || r["low"] != ReasonBelowThreshold || r["gone"] != ReasonOverLimit {
		t.Errorf("the reasons for a model that may not read sensitive content: %v", r)
	}
	if r := reasons(whole); r["n1"] != ReasonOverLimit || r["low"] != ReasonBelowThreshold {
		t.Errorf("the reasons for a model that may read sensitive content: %v", r)
	}
	for _, it := range append(whole.Items, open.Items...) {
		if n := len([]rune(it.Text)); n > 300+400 || !strings.Contains(it.Text, "[Cut here.") {
			t.Errorf("an item of %d characters was not cut to its size", n)
		}
	}

	// A page that cannot be read is passed over, and the next one is kept.
	o.MaxItems = 10
	f, err = Find(bg, s, "anything", o)
	if err != nil {
		t.Fatal(err)
	}
	if r := reasons(f.For(true)); r["gone"] != ReasonUnreadable || len(f.For(true).Items) != 6 {
		t.Errorf("a page that cannot be read: %v", r)
	}

	// With no room for items, nothing is searched.
	before := s.searches
	o.MaxItems = 0
	if f, err = Find(bg, s, "anything", o); err != nil || len(f.For(true).Items) != 0 || s.searches != before {
		t.Errorf("a limit of no items searched the store (%v)", err)
	}
	for _, bad := range []Options{
		{MinScore: -1, SearchResults: 1, MaxItems: 1, ItemMaxChars: 100, QueryMaxChars: 1},
		{SearchResults: 0, MaxItems: 1, ItemMaxChars: 100, QueryMaxChars: 1},
		{SearchResults: 1, MaxItems: -1, ItemMaxChars: 100, QueryMaxChars: 1},
		{SearchResults: 1, MaxItems: 1, ItemMaxChars: 1, QueryMaxChars: 1},
		{SearchResults: 1, MaxItems: 1, ItemMaxChars: 100, QueryMaxChars: 0},
	} {
		if _, err := Find(bg, s, "anything", bad); !errors.Is(err, ErrOptions) {
			t.Errorf("options %+v were not refused: %v", bad, err)
		}
	}
}

// No store, the none adapter, a store that is down and a context that is
// done each give nothing. Only the last two are errors, for the log.
func TestWithNoStoreAndAStoreThatIsDown(t *testing.T) {
	o, _ := defaults(t)
	f, err := Find(bg, nil, "When do the runner beans go in?", o)
	if err != nil || len(f.For(true).Items) != 0 {
		t.Errorf("a nil store: %v", err)
	}
	none, err := knowledge.Open(bg, knowledge.None, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f, err = Find(bg, none, "When do the runner beans go in?", o); err != nil || len(f.For(true).Items) != 0 || f.For(true).Record.Found != 0 {
		t.Errorf("the none adapter: %v", err)
	}
	down := &stub{searchErr: knowledge.ErrUnavailable}
	if f, err = Find(bg, down, "When do the runner beans go in?", o); !errors.Is(err, knowledge.ErrUnavailable) || len(f.For(true).Items) != 0 {
		t.Errorf("a store that is down: %v", err)
	}
	done, cancel := context.WithCancel(bg)
	cancel()
	w := newWorld(t)
	if f, err = Find(done, w.ks, "When do the runner beans go in?", o); !errors.Is(err, context.Canceled) || len(f.For(true).Items) != 0 {
		t.Errorf("a context that is done: %v", err)
	}
}

func lookup(t *testing.T, list []Lookup, name string) Lookup {
	t.Helper()
	for _, l := range list {
		if l.Name == name {
			return l
		}
	}
	t.Fatalf("there is no lookup named %s", name)
	return Lookup{}
}

// The three lookups are plain values a registry can wrap: a name, a
// description, a schema that is JSON, a read-only mark and a function.
func TestTheLookupsAreReadyToRegister(t *testing.T) {
	_, lo := defaults(t)
	list, err := Lookups(false, lo)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range list {
		names = append(names, l.Name)
		var schema struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(l.Schema, &schema); err != nil || schema.Type != "object" || len(schema.Properties) == 0 || len(schema.Required) != 1 {
			t.Errorf("%s: its schema does not read as an object with properties: %v", l.Name, err)
		}
		if !l.ReadOnly || l.Run == nil || len(l.Description) < 40 {
			t.Errorf("%s is not ready to register: %+v", l.Name, l)
		}
	}
	if got := strings.Join(names, " "); got != LookupSearch+" "+LookupReadPage+" "+LookupReadSource {
		t.Errorf("the lookups are %s", got)
	}
	if _, err := Lookups(true, LookupOptions{MaxResults: 0, MaxChars: 1000}); !errors.Is(err, ErrOptions) {
		t.Errorf("options with no results were not refused: %v", err)
	}
}

// A lookup's results obey the same filter as recall, for the model in use.
func TestLookupsObeyTheSensitiveFilter(t *testing.T) {
	w := newWorld(t)
	_, lo := defaults(t)
	run := func(may bool, name, input string) Result {
		t.Helper()
		list, err := Lookups(may, lo)
		if err != nil {
			t.Fatal(err)
		}
		res, err := lookup(t, list, name).Run(bg, w.ts, w.ks, json.RawMessage(input))
		if err != nil {
			t.Fatalf("%s %s: %v", name, input, err)
		}
		return res
	}
	// What the sensitive space holds, and its name. None of these words is
	// in a query below, since a search says what was searched for.
	secrets := []string{"helped", "insomnia", "Sleep notes", "clinic", "health", "appointment"}
	holdsNone := func(what string, res Result) {
		t.Helper()
		for _, s := range secrets {
			if strings.Contains(strings.ToLower(res.Text), strings.ToLower(s)) {
				t.Errorf("%s gave a model that may not read sensitive content %q:\n%s", what, s, res.Text)
			}
		}
		if res.Sensitive || res.Withheld == 0 {
			t.Errorf("%s: sensitive %v, withheld %d", what, res.Sensitive, res.Withheld)
		}
	}

	// Search.
	res := run(false, LookupSearch, `{"query":"melatonin sleep letter"}`)
	holdsNone("a search", res)
	if !strings.Contains(res.Text, "left out: this model may not read") {
		t.Errorf("the search does not say results were left out:\n%s", res.Text)
	}
	res = run(true, LookupSearch, `{"query":"melatonin sleep letter"}`)
	if !res.Sensitive || res.Withheld != 0 || !strings.Contains(res.Text, "page "+w.sleep) || !strings.Contains(res.Text, "source "+w.letter) {
		t.Errorf("a model that may read sensitive content searched and got:\n%s", res.Text)
	}
	res = run(false, LookupSearch, `{"query":"runner beans poles","limit":1}`)
	if res.Sensitive || res.Withheld != 0 || !strings.Contains(res.Text, "page "+w.beans) || !strings.Contains(res.Text, "1 result for") {
		t.Errorf("a search of a normal space:\n%s", res.Text)
	}
	if res = run(false, LookupSearch, `{"query":"felt","space":"garden"}`); !strings.Contains(res.Text, "Nothing in the person's knowledge store matches") {
		t.Errorf("a search of one space found something from another:\n%s", res.Text)
	}

	// Read a page and a source.
	holdsNone("a page read", run(false, LookupReadPage, `{"id":"`+w.sleep+`"}`))
	holdsNone("a source read", run(false, LookupReadSource, `{"id":"`+w.letter+`"}`))
	if res = run(true, LookupReadPage, `{"id":"`+w.sleep+`"}`); !res.Sensitive || !strings.Contains(res.Text, "Melatonin helped") {
		t.Errorf("a model that may read sensitive content read the page and got:\n%s", res.Text)
	}
	if res = run(true, LookupReadSource, `{"id":"`+w.letter+`"}`); !res.Sensitive || !strings.Contains(res.Text, "follow-up appointment") || !strings.Contains(res.Text, "It came from: a letter.") {
		t.Errorf("a model that may read sensitive content read the source and got:\n%s", res.Text)
	}
	if res = run(false, LookupReadPage, `{"id":"`+w.roof+`"}`); res.Sensitive || !strings.Contains(res.Text, "new felt before winter") || !strings.Contains(res.Text, `Page "Shed roof"`) {
		t.Errorf("a page of a normal space:\n%s", res.Text)
	}
	if res = run(false, LookupReadPage, `{"id":"no-such-page"}`); !strings.Contains(res.Text, "There is no page with the id") {
		t.Errorf("a page that is not there:\n%s", res.Text)
	}
}

// A long page is read in parts, and input that does not read is said so.
func TestLookupsReadOnAndRefuseBadInput(t *testing.T) {
	w := newWorld(t)
	long, err := builtin.WritePage(bg, w.ts, builtin.NewPage{Space: "home", Path: "notes/long", Title: "A long page", Body: strings.Repeat("0123456789", 100)})
	if err != nil {
		t.Fatal(err)
	}
	list, err := Lookups(false, LookupOptions{MaxResults: 5, MaxChars: 400})
	if err != nil {
		t.Fatal(err)
	}
	read := lookup(t, list, LookupReadPage)
	first, err := read.Run(bg, w.ts, w.ks, json.RawMessage(`{"id":"`+long.ID+`"}`))
	if err != nil || !strings.Contains(first.Text, "[Cut at character 400 of 1000. Call again with from=400 to read on.]") {
		t.Fatalf("the first part: %v\n%s", err, first.Text)
	}
	last, err := read.Run(bg, w.ts, w.ks, json.RawMessage(`{"id":"`+long.ID+`","from":800}`))
	if err != nil || strings.Contains(last.Text, "[Cut") || !strings.HasSuffix(last.Text, strings.Repeat("0123456789", 20)) {
		t.Fatalf("the last part: %v\n%s", err, last.Text)
	}
	if past, err := read.Run(bg, w.ts, w.ks, json.RawMessage(`{"id":"`+long.ID+`","from":5000}`)); err != nil || !strings.Contains(past.Text, "There is nothing from character 5000 on") {
		t.Errorf("past the end: %v\n%s", err, past.Text)
	}

	for name, inputs := range map[string][]string{
		LookupSearch:     {`{}`, `{"query":"  "}`, `{"query":"beans","colour":"red"}`, `not json`, ``},
		LookupReadPage:   {`{}`, `{"id":""}`, `{"id":"x","from":-1}`, `[1]`},
		LookupReadSource: {`{"id":7}`, `{"page":"x"}`},
	} {
		for _, input := range inputs {
			if _, err := lookup(t, list, name).Run(bg, w.ts, w.ks, json.RawMessage(input)); !errors.Is(err, ErrInput) {
				t.Errorf("%s with the input %q: %v, want ErrInput", name, input, err)
			}
		}
	}
	if _, err := read.Run(bg, w.ts, nil, json.RawMessage(`{"id":"x"}`)); !errors.Is(err, ErrNoStore) {
		t.Errorf("a lookup with no store: %v", err)
	}
	down := &stub{searchErr: knowledge.ErrUnavailable}
	if _, err := lookup(t, list, LookupSearch).Run(bg, w.ts, down, json.RawMessage(`{"query":"beans"}`)); !errors.Is(err, knowledge.ErrUnavailable) {
		t.Errorf("a search of a store that is down: %v", err)
	}
}

// The record of a turn's recall is kept and read back, and a chat that was
// sent sensitive content stays marked.
func TestTheRecordIsKeptAndAChatIsHeld(t *testing.T) {
	w := newWorld(t)
	o, _ := defaults(t)
	turnIn := func(chatID, id string) {
		t.Helper()
		_, err := w.ts.DB().Exec(`INSERT INTO turns (id, chat_id, status, request_id, actor, input, created_at) VALUES (?, ?, 'done', 'r', 'a', '{}', '2026-11-25T00:00:00.000Z')`, id, chatID)
		if err != nil {
			t.Fatal(err)
		}
	}
	one, err := chat.Create(w.ts)
	if err != nil {
		t.Fatal(err)
	}
	two, err := chat.Create(w.ts)
	if err != nil {
		t.Fatal(err)
	}
	turnIn(one.ID, "t1")
	turnIn(one.ID, "t2")
	turnIn(two.ID, "t3")

	held := func(chatID string) bool {
		t.Helper()
		h, err := Held(bg, w.ts, chatID)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	f, err := Find(bg, w.ks, "What helped with the insomnia?", o)
	if err != nil {
		t.Fatal(err)
	}
	open := f.For(false).Record
	open.Adapter = builtin.Name
	if err := Save(bg, w.ts, "t1", one.ID, open); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Saved(bg, w.ts, "t1")
	if err != nil || !ok || got.Adapter != builtin.Name || got.Withheld != open.Withheld || len(got.Hits) != len(open.Hits) || got.Query != open.Query {
		t.Fatalf("the record read back as %+v (%v, %v)", got, ok, err)
	}
	if held(one.ID) {
		t.Error("a chat whose model was given nothing sensitive is held")
	}
	if err := Save(bg, w.ts, "t2", one.ID, f.For(true).Record); err != nil {
		t.Fatal(err)
	}
	if !held(one.ID) || held(two.ID) {
		t.Errorf("held: chat one %v, chat two %v", held(one.ID), held(two.ID))
	}
	// A later save of the same turn does not take the mark back.
	if err := Save(bg, w.ts, "t2", one.ID, open); err != nil || !held(one.ID) {
		t.Errorf("the mark was taken back (%v)", err)
	}
	// A lookup marks a chat with no record of its own.
	if err := Hold(bg, w.ts, "t3", two.ID); err != nil || !held(two.ID) {
		t.Errorf("a lookup did not hold the chat (%v)", err)
	}
	if _, ok, err := Saved(bg, w.ts, "t3"); ok || err != nil {
		t.Errorf("a turn a lookup marked has a record: %v %v", ok, err)
	}
	if _, ok, err := Saved(bg, w.ts, "no-such-turn"); ok || err != nil {
		t.Errorf("a turn with no recall has a record: %v %v", ok, err)
	}
	// The rows go with the chat.
	if err := chat.Delete(w.ts, one.ID); err != nil {
		t.Fatal(err)
	}
	if held(one.ID) {
		t.Error("a deleted chat is still held")
	}
}
