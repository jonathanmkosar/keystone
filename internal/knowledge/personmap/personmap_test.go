package personmap

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Every name and every line of content in these tests is invented.

var bg = context.Background()

// defaults reads the limits the way a caller does, from the registry's
// defaults. It also proves the five settings are there.
func defaults(t *testing.T) Limits {
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
	cpt, err := strconv.ParseFloat(text(SettingCharsPerToken), 64)
	if err != nil {
		t.Fatal(err)
	}
	l := Limits{
		ProfileMaxChars: whole(SettingProfileMaxChars),
		MapMaxTokens:    whole(SettingMapMaxTokens),
		CharsPerToken:   cpt,
		TitlesPerKind:   whole(SettingMapTitlesPerKind),
		TitleMaxChars:   whole(SettingMapTitleMaxChars),
	}
	if err := CheckLimits(l); err != nil {
		t.Fatalf("the registry's defaults are refused: %v", err)
	}
	return l
}

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

func fresh(t *testing.T) *tenant.Store {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return newTenant(t, in, "Test Person One")
}

// counted wraps a store and counts the catalogs read from it, so a test can
// tell a kept map from one built again.
type counted struct {
	knowledge.Store
	catalogs int
}

func (c *counted) Catalog(ctx context.Context) ([]knowledge.Entry, error) {
	c.catalogs++
	return c.Store.Catalog(ctx)
}

func openBuiltin(t *testing.T, ts *tenant.Store) *counted {
	t.Helper()
	s, err := knowledge.Open(bg, builtin.Name, ts)
	if err != nil {
		t.Fatal(err)
	}
	return &counted{Store: s}
}

// plain is a store with no optional capability, like one whose catalog can
// only be read and compared.
type plain struct {
	knowledge.Store // nil: only the calls below are made
	entries         []knowledge.Entry
	err             error
	marker          bool
	catalogs        int
}

func (p *plain) Capabilities() knowledge.Capabilities {
	return knowledge.Capabilities{CatalogMarker: p.marker}
}

func (p *plain) Catalog(ctx context.Context) ([]knowledge.Entry, error) {
	p.catalogs++
	return p.entries, p.err
}

func (p *plain) CatalogMarker(ctx context.Context) (string, error) { return "", p.err }

type page struct{ space, path, title, kind string }

// The test store: four spaces, one of them sensitive.
var testPages = []page{
	{"home", "accounts/electric", "Electricity account", "account"},
	{"home", "accounts/water", "Water account", "account"},
	{"home", "projects/shed-roof", "Shed roof", "project"},
	{"home", "notes/bins", "Bin days", ""},
	{"garden", "beds/north", "North bed", "place"},
	{"garden", "plants/tomatoes", "Tomatoes", "plant"},
	{"garden", "plants/beans", "Runner beans", "plant"},
	{"work", "people/test-person-two", "Test Person Two", "person"},
	{"work", "projects/ledger-move", "Ledger move", "project"},
	{"health", "visits/zqcheckup", "Zqcheckup in spring", "zqvisit"},
	{"health", "plans/zqstretch", "Zqstretch plan", "zqroutine"},
}

func seed(t *testing.T, ts *tenant.Store) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, p := range testPages {
		w, err := builtin.WritePage(bg, ts, builtin.NewPage{Space: p.space, Path: p.path, Title: p.title, Kind: p.kind, Body: "Invented."})
		if err != nil {
			t.Fatal(err)
		}
		ids[p.space+"/"+p.path] = w.ID
	}
	if err := builtin.SetLabel(bg, ts, "health", knowledge.LabelSensitive); err != nil {
		t.Fatal(err)
	}
	return ids
}

func mustLoad(t *testing.T, ts *tenant.Store, ks knowledge.Store, l Limits) Map {
	t.Helper()
	m, err := Load(bg, ts, ks, l)
	if err != nil {
		t.Fatal(err)
	}
	if m.StoreErr != nil {
		t.Fatal(m.StoreErr)
	}
	return m
}

// The item's proof: the map lists every space and kind of page in the test
// store and stays under its token limit.
func TestTheMapListsEverySpaceAndKindUnderItsLimit(t *testing.T) {
	ts := fresh(t)
	seed(t, ts)
	l := defaults(t)
	m := mustLoad(t, ts, openBuiltin(t, ts), l)

	whole := m.Text(true)
	for _, p := range testPages {
		kind := p.kind
		if kind == "" {
			kind = NoKind
		}
		line := lineOf(whole, p.space)
		if line == "" {
			t.Fatalf("the map has no line for the space %s:\n%s", p.space, whole)
		}
		if !strings.Contains(line, kind+" ") {
			t.Errorf("the line for %s does not name the kind %s: %s", p.space, kind, line)
		}
		if !strings.Contains(line, p.title) {
			t.Errorf("the line for %s does not show the title %q: %s", p.space, p.title, line)
		}
	}
	if !strings.Contains(lineOf(whole, "health"), "(sensitive)") {
		t.Errorf("the whole map does not mark the sensitive space:\n%s", whole)
	}
	for _, text := range []string{whole, m.Text(false)} {
		if n := EstimateTokens(text, l.CharsPerToken); n > l.MapMaxTokens || n == 0 {
			t.Errorf("the map is %d tokens and the limit is %d", n, l.MapMaxTokens)
		}
	}
	if m.Spaces != 4 || m.Pages != len(testPages) || m.Withheld != 1 {
		t.Errorf("counts: %d spaces, %d pages, %d withheld", m.Spaces, m.Pages, m.Withheld)
	}
	if got := lineOf(whole, "home"); got != "- home, 4 pages: account 2 (Electricity account; Water account); other 1 (Bin days); project 1 (Shed roof)" {
		t.Errorf("the line for home reads: %s", got)
	}
}

func lineOf(text, space string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "- "+space+",") || strings.HasPrefix(line, "- "+space+" (sensitive),") {
			return line
		}
	}
	return ""
}

// Fixed rule 3: nothing from a sensitive space is in the text for a model
// that may not read sensitive content.
func TestTheOpenMapHoldsNothingFromASensitiveSpace(t *testing.T) {
	ts := fresh(t)
	seed(t, ts)
	l := defaults(t)
	ks := openBuiltin(t, ts)
	m := mustLoad(t, ts, ks, l)

	open := m.Text(false)
	for _, word := range []string{"health", "zq", "Zq", "visits", "plans"} {
		if strings.Contains(open, word) {
			t.Errorf("the open map holds %q, from the sensitive space:\n%s", word, open)
		}
	}
	if !strings.HasSuffix(open, "- 1 sensitive space is not shown to this model") {
		t.Errorf("the open map does not say a space was held back:\n%s", open)
	}
	for _, space := range []string{"home", "garden", "work"} {
		if lineOf(open, space) == "" {
			t.Errorf("the open map lost the normal space %s", space)
		}
	}
	if m.WithheldFor(false) != 1 || m.WithheldFor(true) != 0 {
		t.Errorf("withheld: %d for a model that may not, %d for one that may", m.WithheldFor(false), m.WithheldFor(true))
	}

	// The same through the function the context builder calls.
	for _, may := range []bool{false, true} {
		p, err := ForContext(bg, ts, ks, may, l)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(p.Map, "health") != may {
			t.Errorf("ForContext with %v: the sensitive space is shown: %v", may, strings.Contains(p.Map, "health"))
		}
		if p.MapTokens != EstimateTokens(p.Map, l.CharsPerToken) || p.MapTokens > l.MapMaxTokens {
			t.Errorf("ForContext with %v: %d tokens", may, p.MapTokens)
		}
	}

	// A space made sensitive later leaves the open map at the next call,
	// and a space made normal comes back.
	if err := builtin.SetLabel(bg, ts, "work", knowledge.LabelSensitive); err != nil {
		t.Fatal(err)
	}
	m = mustLoad(t, ts, ks, l)
	if open := m.Text(false); strings.Contains(open, "work") || strings.Contains(open, "Ledger") || !strings.Contains(open, "2 sensitive spaces are not shown") {
		t.Errorf("after work was made sensitive the open map reads:\n%s", open)
	}
	if err := builtin.SetLabel(bg, ts, "work", knowledge.LabelNormal); err != nil {
		t.Fatal(err)
	}
	if open := mustLoad(t, ts, ks, l).Text(false); lineOf(open, "work") == "" {
		t.Errorf("after work was made normal again the open map reads:\n%s", open)
	}
}

func TestAMissingOrUnknownLabelReadsAsSensitive(t *testing.T) {
	l := defaults(t)
	entries := []knowledge.Entry{
		{ID: "1", Space: "open", Label: knowledge.LabelNormal, Path: "a", Title: "Plain page", Kind: "note"},
		{ID: "2", Space: "blank", Path: "a", Title: "Zqblank page", Kind: "zqkind"},
		{ID: "3", Space: "odd", Label: "private", Path: "a", Title: "Zqodd page", Kind: "zqkind"},
		// One entry without a label makes its whole space sensitive.
		{ID: "4", Space: "mixed", Label: knowledge.LabelNormal, Path: "a", Title: "Zqmixed one", Kind: "zqkind"},
		{ID: "5", Space: "mixed", Path: "b", Title: "Zqmixed two", Kind: "zqkind"},
	}
	m := Build(entries, l)
	if m.Withheld != 3 {
		t.Errorf("%d spaces withheld, want 3", m.Withheld)
	}
	open := m.Text(false)
	for _, word := range []string{"blank", "odd", "mixed", "zq", "Zq"} {
		if strings.Contains(open, word) {
			t.Errorf("the open map holds %q:\n%s", word, open)
		}
	}
	if lineOf(open, "open") == "" {
		t.Errorf("the open map lost the normal space:\n%s", open)
	}
	if got := (Map{}).Text(false) + (Map{}).Text(true); got != "" {
		t.Errorf("an empty map has text: %q", got)
	}
}

// big is a catalog too large for any small limit: 30 spaces, every third
// one sensitive, each with 8 kinds of 4 pages.
func big() []knowledge.Entry {
	var out []knowledge.Entry
	for s := 0; s < 30; s++ {
		space, label, mark := fmt.Sprintf("space%02d", s), knowledge.LabelNormal, "plain"
		if s%3 == 0 {
			space, label, mark = fmt.Sprintf("zqspace%02d", s), knowledge.LabelSensitive, "zq"
		}
		for k := 0; k < 8; k++ {
			for p := 0; p <= s%4; p++ {
				out = append(out, knowledge.Entry{
					ID: fmt.Sprint(len(out)), Space: space, Label: label,
					Path:  fmt.Sprintf("%skind%d/page%d", mark, k, p),
					Title: fmt.Sprintf("%s title %d of kind %d in a store with long titles", mark, p, k),
					Kind:  fmt.Sprintf("%skind%d", mark, k),
				})
			}
		}
	}
	return out
}

func TestTheMapStaysUnderAnyLimitAndSaysWhatItLeftOut(t *testing.T) {
	entries := big()
	l := defaults(t)
	sawKinds, sawSpaces := false, false
	for _, limit := range []int{1, 5, 12, 25, 60, 150, 400, 600, 1500, 4000, MaxMapTokens} {
		for _, cpt := range []float64{1, 3, 4.5} {
			l.MapMaxTokens, l.CharsPerToken = limit, cpt
			m := Build(entries, l)
			for _, may := range []bool{false, true} {
				text := m.Text(may)
				if n := EstimateTokens(text, cpt); n > limit {
					t.Errorf("limit %d at %v characters a token: the map is %d tokens", limit, cpt, n)
				}
				if !may && strings.Contains(text, "zq") {
					t.Errorf("limit %d: the open map holds a sensitive name:\n%s", limit, text)
				}
				sawKinds = sawKinds || strings.Contains(text, "more kinds")
				sawSpaces = sawSpaces || strings.Contains(text, "more spaces")
			}
		}
	}
	if !sawKinds || !sawSpaces {
		t.Errorf("no limit made the map say it left out kinds (%v) or spaces (%v)", sawKinds, sawSpaces)
	}

	// With room, nothing is left out.
	l.MapMaxTokens, l.CharsPerToken = MaxMapTokens, 3
	whole := Build(entries, l).Text(true)
	if strings.Contains(whole, "more ") || strings.Count(whole, "\n") != 29 {
		t.Errorf("with room the map still leaves something out:\n%s", whole)
	}

	// The same catalog in another order gives the same texts.
	back := slices.Clone(entries)
	slices.Reverse(back)
	if again := Build(back, l); again.Text(true) != whole || again.Text(false) != Build(entries, l).Text(false) {
		t.Error("the map depends on the order the catalog came in")
	}
}

func TestATitleCannotBreakTheMap(t *testing.T) {
	l := defaults(t)
	l.TitleMaxChars = 12
	m := Build([]knowledge.Entry{
		{ID: "1", Space: "home", Label: knowledge.LabelNormal, Path: "a", Title: "Line one\n- zzz, 9 pages\tand a very long tail", Kind: "note\nkind"},
		{ID: "2", Space: "home", Label: knowledge.LabelNormal, Path: "b/untitled", Title: "  ", Kind: "note kind"},
	}, l)
	if got, want := m.Text(true), "- home, 2 pages: note kind 2 (Line one -…; b/untitled)"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestTheMapIsKeptUntilTheMarkerChanges(t *testing.T) {
	ts := fresh(t)
	ids := seed(t, ts)
	l := defaults(t)
	ks := openBuiltin(t, ts)

	clock := time.Date(2026, 11, 25, 6, 0, 0, 0, time.UTC)
	now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	t.Cleanup(func() { now = time.Now })

	first := mustLoad(t, ts, ks, l)
	if ks.catalogs != 1 || first.Built.IsZero() {
		t.Fatalf("the first load read %d catalogs, built %v", ks.catalogs, first.Built)
	}
	again := mustLoad(t, ts, ks, l)
	if ks.catalogs != 1 || again.Text(true) != first.Text(true) || again.Text(false) != first.Text(false) || !again.Built.Equal(first.Built) || again.Withheld != first.Withheld {
		t.Fatalf("with the marker unchanged the map was built again: %d catalogs", ks.catalogs)
	}

	// A page added.
	if _, err := builtin.WritePage(bg, ts, builtin.NewPage{Space: "garden", Path: "tools/spade", Title: "Border spade", Kind: "tool", Body: "Invented."}); err != nil {
		t.Fatal(err)
	}
	added := mustLoad(t, ts, ks, l)
	if ks.catalogs != 2 || !strings.Contains(added.Text(true), "tool 1 (Border spade)") || !added.Built.After(first.Built) {
		t.Fatalf("after a page was added: %d catalogs\n%s", ks.catalogs, added.Text(true))
	}
	// A page removed.
	if err := builtin.RemovePage(bg, ts, ids["garden/beds/north"]); err != nil {
		t.Fatal(err)
	}
	if got := mustLoad(t, ts, ks, l).Text(true); ks.catalogs != 3 || strings.Contains(got, "North bed") {
		t.Fatalf("after a page was removed: %d catalogs\n%s", ks.catalogs, got)
	}
	// Other limits.
	l.TitlesPerKind = 0
	if got := mustLoad(t, ts, ks, l).Text(true); ks.catalogs != 4 || strings.Contains(got, "Tomatoes") || !strings.Contains(got, "plant 2") {
		t.Fatalf("after the limits changed: %d catalogs\n%s", ks.catalogs, got)
	}
	// A new handle on the same tenant finds the kept map.
	other := openBuiltin(t, ts)
	if mustLoad(t, ts, other, l); other.catalogs != 0 {
		t.Errorf("a new handle built the map again")
	}
}

func TestAStoreWithNoMarkerIsComparedByItsCatalog(t *testing.T) {
	ts := fresh(t)
	l := defaults(t)
	ks := &plain{entries: []knowledge.Entry{
		{ID: "1", Space: "home", Label: knowledge.LabelNormal, Path: "a", Title: "First page", Kind: "note"},
	}}
	clock := time.Date(2026, 11, 25, 6, 0, 0, 0, time.UTC)
	now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	t.Cleanup(func() { now = time.Now })

	first := mustLoad(t, ts, ks, l)
	again := mustLoad(t, ts, ks, l)
	if !again.Built.Equal(first.Built) || again.Text(true) != "- home, 1 page: note 1 (First page)" {
		t.Fatalf("an unchanged catalog built the map again: %q", again.Text(true))
	}
	ks.entries = append(ks.entries, knowledge.Entry{ID: "2", Space: "home", Label: knowledge.LabelNormal, Path: "b", Title: "Second page", Kind: "note"})
	changed := mustLoad(t, ts, ks, l)
	if !changed.Built.After(first.Built) || !strings.Contains(changed.Text(true), "Second page") {
		t.Fatalf("a changed catalog did not build the map again: %q", changed.Text(true))
	}
	// A label alone changing is a change.
	ks.entries[0].Label = knowledge.LabelSensitive
	if got := mustLoad(t, ts, ks, l).Text(false); got != "- 1 sensitive space is not shown to this model" {
		t.Fatalf("after the label changed the open map reads %q", got)
	}
}

func TestNoStoreGivesAnEmptyMap(t *testing.T) {
	ts := fresh(t)
	l := defaults(t)
	if _, err := SetProfile(bg, ts, "Prefers short answers.", l.ProfileMaxChars); err != nil {
		t.Fatal(err)
	}
	none, err := knowledge.Open(bg, knowledge.None, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, ks := range map[string]knowledge.Store{"none": none, "nil": nil} {
		for _, may := range []bool{false, true} {
			p, err := ForContext(bg, ts, ks, may, l)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if p.Map != "" || p.MapTokens != 0 || p.WithheldSpaces != 0 || p.StoreErr != nil {
				t.Errorf("%s: %+v", name, p)
			}
			if p.Profile != "Prefers short answers." || p.ProfileTokens != 8 {
				t.Errorf("%s: the profile is %q, %d tokens", name, p.Profile, p.ProfileTokens)
			}
		}
	}
	// An empty built-in store too.
	if m := mustLoad(t, ts, openBuiltin(t, ts), l); m.Text(true) != "" || m.Text(false) != "" {
		t.Errorf("an empty store has a map: %q", m.Text(true))
	}
	// And a map switched off by its limit.
	seed(t, ts)
	l.MapMaxTokens = 0
	if m := mustLoad(t, ts, openBuiltin(t, ts), l); m.Text(true) != "" {
		t.Errorf("a limit of 0 gave a map: %q", m.Text(true))
	}
}

func TestAStoreThatIsDownGivesAnEmptyMapAndNoError(t *testing.T) {
	ts := fresh(t)
	seed(t, ts)
	l := defaults(t)
	// A map is kept from when the store was up.
	if mustLoad(t, ts, openBuiltin(t, ts), l).Text(true) == "" {
		t.Fatal("no map to begin with")
	}
	down := fmt.Errorf("%w: nothing is listening", knowledge.ErrUnavailable)
	for _, marker := range []bool{false, true} {
		p, err := ForContext(bg, ts, &plain{err: down, marker: marker}, true, l)
		if err != nil {
			t.Fatalf("marker %v: %v", marker, err)
		}
		if p.Map != "" || p.MapTokens != 0 || !errors.Is(p.StoreErr, knowledge.ErrUnavailable) {
			t.Errorf("marker %v: %+v", marker, p)
		}
	}
	// The kept map is still there for when the store is back.
	back := openBuiltin(t, ts)
	if m := mustLoad(t, ts, back, l); m.Text(true) == "" || back.catalogs != 0 {
		t.Errorf("the kept map was lost while the store was down")
	}

	// A context that is done is an error, and so are bad limits.
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := ForContext(ctx, ts, back, true, l); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled call returned %v", err)
	}
	for _, bad := range []func(*Limits){
		func(l *Limits) { l.ProfileMaxChars = 0 },
		func(l *Limits) { l.MapMaxTokens = -1 },
		func(l *Limits) { l.MapMaxTokens = MaxMapTokens + 1 },
		func(l *Limits) { l.CharsPerToken = 0 },
		func(l *Limits) { l.TitlesPerKind = -1 },
		func(l *Limits) { l.TitleMaxChars = MinTitleChars - 1 },
	} {
		wrong := l
		bad(&wrong)
		if _, err := ForContext(bg, ts, back, true, wrong); !errors.Is(err, ErrLimits) {
			t.Errorf("limits %+v returned %v", wrong, err)
		}
	}
	if _, err := Load(bg, nil, back, l); err == nil {
		t.Error("a map was loaded for no tenant")
	}
}

func TestTheProfile(t *testing.T) {
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	one, two := newTenant(t, in, "Test Person One"), newTenant(t, in, "Test Person Two")
	l := defaults(t)

	if p, err := ReadProfile(bg, one); err != nil || p.Text != "" || !p.Updated.IsZero() {
		t.Fatalf("a new tenant's profile: %+v %v", p, err)
	}
	set, err := SetProfile(bg, one, "  Lives in an invented town.\r\nPrefers metric units.\n\n", l.ProfileMaxChars)
	if err != nil {
		t.Fatal(err)
	}
	want := "Lives in an invented town.\nPrefers metric units."
	if set.Text != want || set.Updated.IsZero() {
		t.Fatalf("set: %+v", set)
	}
	if got, err := ReadProfile(bg, one); err != nil || got.Text != want || !got.Updated.Equal(set.Updated) {
		t.Fatalf("read back: %+v %v", got, err)
	}
	// It is one tenant's own.
	if p, err := ReadProfile(bg, two); err != nil || p.Text != "" {
		t.Fatalf("the second tenant reads %+v %v", p, err)
	}
	// Too long is refused and the old text stays.
	if _, err := SetProfile(bg, one, strings.Repeat("é", 41), 40); !errors.Is(err, ErrTooLong) {
		t.Fatalf("a long profile returned %v", err)
	}
	if _, err := SetProfile(bg, one, strings.Repeat("é", 40), 40); err != nil {
		t.Fatalf("a profile at the limit was refused: %v", err)
	}
	if _, err := SetProfile(bg, one, "Bad \x00 text", 40); err == nil {
		t.Fatal("a profile with a zero byte was kept")
	}
	if _, err := SetProfile(bg, one, "x", 0); !errors.Is(err, ErrLimits) {
		t.Fatalf("a limit of 0 returned %v", err)
	}
	// A profile kept under a wider limit is cut for a call, not in the store.
	l.ProfileMaxChars = 10
	p, err := ForContext(bg, one, nil, false, l)
	if err != nil || p.Profile != strings.Repeat("é", 10) || p.ProfileTokens != 4 {
		t.Fatalf("cut for a call: %+v %v", p, err)
	}
	if got, _ := ReadProfile(bg, one); got.Text != strings.Repeat("é", 40) {
		t.Fatalf("the kept profile changed: %q", got.Text)
	}
	// Replaced, then cleared.
	if _, err := SetProfile(bg, one, "Prefers short answers.", 2000); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadProfile(bg, one); got.Text != "Prefers short answers." {
		t.Fatalf("replaced: %q", got.Text)
	}
	if _, err := SetProfile(bg, one, " \n ", 2000); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadProfile(bg, one); err != nil || got.Text != "" || !got.Updated.IsZero() {
		t.Fatalf("cleared: %+v %v", got, err)
	}
}
