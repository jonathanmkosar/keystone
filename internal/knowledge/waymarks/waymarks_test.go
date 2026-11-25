package waymarks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	_ "github.com/jonathanmkosar/keystone/internal/knowledge/builtin" // so every adapter is in this build
	"github.com/jonathanmkosar/keystone/internal/knowledge/knowledgetest"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// Every name, every token and every line of content in these tests is
// invented. The only Waymarks they talk to is the fake in fake_test.go,
// served on this machine.

var bg = context.Background()

// newTenant makes an install in a folder of the test's own, with its master
// key held in memory, and returns one tenant's open handle.
func newTenant(t *testing.T) *tenant.Store {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
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
	return ts
}

func setSetting(t *testing.T, ts *tenant.Store, key, value string) {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	if err := tenantsettings.Set(bg, ts, reg, tenantsettings.Person, key, value); err != nil {
		t.Fatal(err)
	}
}

// The secret each role's token is kept under.
var secretOf = map[string]string{readerToken: SecretReader, intakeToken: SecretIntake, embedderToken: SecretEmbedder}

// setUp points a tenant at a fake instance and gives it the tokens named.
func setUp(t *testing.T, f *fake, tokens ...string) *tenant.Store {
	t.Helper()
	ts := newTenant(t)
	setSetting(t, ts, SettingAddress, f.srv.URL)
	for _, token := range tokens {
		if err := ts.Secrets().Set(secretOf[token], token); err != nil {
			t.Fatal(err)
		}
	}
	return ts
}

// openStore opens the adapter the way the rest of Keystone does: by name,
// through the registry.
func openStore(t *testing.T, ts *tenant.Store) knowledge.Store {
	t.Helper()
	s, err := knowledge.Open(bg, Name, ts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func allTokens(t *testing.T) (knowledge.Store, *fake) {
	t.Helper()
	f := newFake(t)
	return openStore(t, setUp(t, f, readerToken, intakeToken, embedderToken)), f
}

// fakes remembers which fake each store the suite was given talks to, so
// Seed can put content in by the instance's own back door.
var fakes sync.Map

func harness(tokens ...string) knowledgetest.Harness {
	return knowledgetest.Harness{
		New: func(t *testing.T) knowledge.Store {
			f := newFake(t)
			s := openStore(t, setUp(t, f, tokens...))
			fakes.Store(s, f)
			t.Cleanup(func() { fakes.Delete(s) })
			return s
		},
		Seed: func(t *testing.T, s knowledge.Store, c knowledgetest.Content) {
			f, _ := fakes.Load(s)
			f.(*fake).seed(c)
		},
	}
}

// The item's proof, as far as it can be reached without a real instance:
// the adapter passes the contract every knowledge store must, against a
// Waymarks that answers as the real one does. With all three tokens every
// capability is on and no check is skipped.
func TestContract(t *testing.T) {
	knowledgetest.Run(t, harness(readerToken, intakeToken, embedderToken))
}

// With fewer tokens the flags are off, and the suite checks each call
// behind one says so.
func TestContractWithTheReaderAndIntakeTokens(t *testing.T) {
	knowledgetest.Run(t, harness(readerToken, intakeToken))
}

func TestContractWithTheReaderTokenAlone(t *testing.T) {
	knowledgetest.Run(t, harness(readerToken))
}

func TestCapabilitiesFollowTheTokens(t *testing.T) {
	for name, c := range map[string]struct {
		tokens []string
		want   knowledge.Capabilities
	}{
		"the reader alone":        {[]string{readerToken}, knowledge.Capabilities{CatalogMarker: true}},
		"the reader and intake":   {[]string{readerToken, intakeToken}, knowledge.Capabilities{CatalogMarker: true, AddSource: true}},
		"the reader and embedder": {[]string{readerToken, embedderToken}, knowledge.Capabilities{CatalogMarker: true, VectorSearch: true, PageVectors: true}},
		"all three":               {[]string{readerToken, intakeToken, embedderToken}, knowledge.Capabilities{CatalogMarker: true, AddSource: true, VectorSearch: true, PageVectors: true}},
	} {
		s := openStore(t, setUp(t, newFake(t), c.tokens...))
		if got := s.Capabilities(); got != c.want {
			t.Errorf("with %s: Capabilities() = %+v, want %+v", name, got, c.want)
		}
	}
}

// Every value the knowledge.adapter setting offers is an adapter in this
// build, and each opens for a tenant: none and the built-in store as they
// are, and Waymarks once the tenant has an address and a reader token.
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
	ts := setUp(t, newFake(t), readerToken)
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

// A tenant who chose Waymarks and has not set it up gets "unavailable", so
// a chat carries on, and the error says what is missing.
func TestNotSetUp(t *testing.T) {
	if s, err := knowledge.Open(bg, Name, nil); err == nil {
		t.Errorf("the adapter opened with no tenant: %v", s)
	}
	f := newFake(t)

	noAddress := newTenant(t)
	if err := noAddress.Secrets().Set(SecretReader, readerToken); err != nil {
		t.Fatal(err)
	}
	_, err := knowledge.Open(bg, Name, noAddress)
	if !errors.Is(err, knowledge.ErrUnavailable) || !strings.Contains(err.Error(), SettingAddress) {
		t.Errorf("with no address: error %v, want ErrUnavailable naming %s", err, SettingAddress)
	}

	// An intake token is not a reader: without the reader there is no store.
	_, err = knowledge.Open(bg, Name, setUp(t, f, intakeToken))
	if !errors.Is(err, knowledge.ErrUnavailable) || !strings.Contains(err.Error(), SecretReader) {
		t.Errorf("with no reader token: error %v, want ErrUnavailable naming %s", err, SecretReader)
	}

	for _, address := range []string{
		"127.0.0.1:7878", "ftp://127.0.0.1", "http://", "http://someone:a-password@127.0.0.1:7878",
		"http://127.0.0.1:7878/?token=in-the-address", "http://127.0.0.1:7878/#part", "not an address",
	} {
		ts := setUp(t, f, readerToken)
		setSetting(t, ts, SettingAddress, address)
		_, err := knowledge.Open(bg, Name, ts)
		if !errors.Is(err, knowledge.ErrUnavailable) {
			t.Errorf("with the address %q: error %v, want ErrUnavailable", address, err)
		} else if strings.Contains(err.Error(), "a-password") || strings.Contains(err.Error(), "in-the-address") {
			t.Errorf("the error repeats what was in the address: %v", err)
		}
	}

	// A value out of range stops the store from opening.
	for key, value := range map[string]string{SettingTimeout: "5ms", SettingSearchResults: "101"} {
		ts := setUp(t, f, readerToken)
		setSetting(t, ts, key, value)
		if s, err := knowledge.Open(bg, Name, ts); err == nil {
			t.Errorf("the store opened with %s set to %s: %v", key, value, s)
		}
	}

	// An address with a slash at its end, or a path, still reaches it.
	ts := setUp(t, f, readerToken)
	setSetting(t, ts, SettingAddress, f.srv.URL+"/")
	if _, err := openStore(t, ts).Catalog(bg); err != nil {
		t.Errorf("an address ending in a slash: %v", err)
	}
	if n := len(f.received()); n != 1 {
		t.Errorf("opening made %d calls and Catalog one: opening a store calls nothing", n-1)
	}
}

// everyCall makes each call of the interface once and returns the errors,
// by the call's name.
func everyCall(ctx context.Context, s knowledge.Store) map[string]error {
	out := map[string]error{}
	_, out["Catalog"] = s.Catalog(ctx)
	_, out["Search"] = s.Search(ctx, knowledge.Query{Text: "heron"})
	_, out["Search with a vector"] = s.Search(ctx, knowledge.Query{Vector: []float32{1, 0, 0, 0}, Model: knowledgetest.VectorModel})
	_, out["ReadPage"] = s.ReadPage(ctx, "1")
	_, out["ReadSource"] = s.ReadSource(ctx, "1")
	_, out["AddSource"] = s.AddSource(ctx, knowledge.NewSource{Space: "home", Name: "chat-facts", Facts: []knowledge.Fact{{Text: "The heron comes at dusk."}}})
	_, out["CatalogMarker"] = s.CatalogMarker(ctx)
	_, out["VectorsNeeded"] = s.VectorsNeeded(ctx, 0)
	out["PutVector"] = s.PutVector(ctx, knowledge.PageVector{PageID: "1", Revision: "1", Model: knowledgetest.VectorModel, Vector: []float32{1, 0, 0, 0}})
	return out
}

// wantUnavailable checks every call answered ErrUnavailable, and did so
// inside the time allowed.
func wantUnavailable(t *testing.T, what string, s knowledge.Store, within time.Duration) {
	t.Helper()
	start := time.Now()
	for call, err := range everyCall(bg, s) {
		if !errors.Is(err, knowledge.ErrUnavailable) {
			t.Errorf("%s: %s: error %v, want ErrUnavailable", what, call, err)
		}
	}
	if took := time.Since(start); took > within {
		t.Errorf("%s: the nine calls took %s, want under %s", what, took.Round(time.Millisecond), within)
	}
}

// The outage half of the item's proof, as far as it can be reached before
// the chat turn exists: an instance that is down answers every call with
// ErrUnavailable, at once, and comes back by itself when it is up.
func TestAnInstanceThatIsDown(t *testing.T) {
	s, f := allTokens(t)
	if _, err := s.Catalog(bg); err != nil {
		t.Fatal(err)
	}
	f.srv.Close()
	wantUnavailable(t, "a closed instance", s, 10*time.Second)

	// Nothing ever listened here.
	never := httptest.NewServer(http.NotFoundHandler())
	address := never.URL
	never.Close()
	ts := setUp(t, f, readerToken, intakeToken, embedderToken)
	setSetting(t, ts, SettingAddress, address)
	wantUnavailable(t, "an address where nothing listens", openStore(t, ts), 10*time.Second)
}

// An instance that takes the call and never answers is given up on after
// the timeout setting, and the caller's own deadline still wins.
func TestAnInstanceThatNeverAnswers(t *testing.T) {
	f := newFake(t)
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	f.set(func(f *fake) { f.hold = hold })
	ts := setUp(t, f, readerToken, intakeToken, embedderToken)
	setSetting(t, ts, SettingTimeout, "150ms")
	s := openStore(t, ts)
	wantUnavailable(t, "an instance that never answers", s, 8*time.Second)

	ctx, cancel := context.WithTimeout(bg, 20*time.Millisecond)
	defer cancel()
	if _, err := s.Catalog(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("with the caller's own deadline passed: error %v, want the context's error", err)
	}
}

// Whatever is at the address and is not a working Waymarks reads as
// unavailable: nothing it sends is taken for content.
func TestAnswersThatAreNotWaymarks(t *testing.T) {
	// The redirect points at a second server, which must never be called.
	var reached sync.Map
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(r.URL.Path, r.Header.Get("Authorization"))
	}))
	defer other.Close()

	for _, mode := range []string{
		"a page that is not JSON", "a fault that is not JSON", "a redirect", "JSON with no data",
		"a refused token", "a fault", "an answer too large",
	} {
		s, f := allTokens(t)
		s.(*store).replyMax = 1024
		f.set(func(f *fake) { f.mode, f.elsewhere = mode, other.URL })
		wantUnavailable(t, mode, s, 10*time.Second)
	}
	reached.Range(func(path, token any) bool {
		t.Errorf("a redirect was followed to %v, carrying %q", path, token)
		return true
	})

	// A token the instance does not know.
	f := newFake(t)
	ts := setUp(t, f)
	for _, name := range []string{SecretReader, SecretIntake, SecretEmbedder} {
		if err := ts.Secrets().Set(name, "an-invented-token-nobody-issued"); err != nil {
			t.Fatal(err)
		}
	}
	wantUnavailable(t, "a token the instance does not know", openStore(t, ts), 10*time.Second)
}

// No error carries a token, even when the instance sends one back.
func TestNoErrorCarriesAToken(t *testing.T) {
	check := func(what string, s knowledge.Store) {
		t.Helper()
		for call, err := range everyCall(bg, s) {
			if err == nil {
				t.Errorf("%s: %s returned no error", what, call)
				continue
			}
			for _, token := range []string{readerToken, intakeToken, embedderToken} {
				if strings.Contains(err.Error(), token) {
					t.Errorf("%s: the error of %s holds a token: %v", what, call, err)
				}
			}
		}
	}
	s, f := allTokens(t)
	f.set(func(f *fake) { f.mode = "a fault that echoes the token" })
	check("a fault that echoes the token", s)
	f.srv.Close()
	check("a closed instance", s)
}

// Each call goes out under the token of its role, and under no other.
func TestEachCallUsesTheTokenOfItsRole(t *testing.T) {
	s, f := allTokens(t)
	f.seed(knowledgetest.Content{
		Sensitive: []string{"health"},
		Sources:   []knowledgetest.SeedSource{{Space: "home", Name: "tide-table.txt", Text: "High water is at noon."}},
		Pages: []knowledgetest.SeedPage{
			{Space: "home", Path: "boat", Title: "Boat", Body: "The boat is launched at high water.", Cites: []string{"tide-table.txt"}},
			{Space: "health", Path: "knee", Title: "Knee", Body: "The knee is rested after rowing."},
		},
	})
	f.received()
	for call, err := range everyCall(bg, s) {
		if err != nil {
			t.Errorf("%s: %v", call, err)
		}
	}
	want := map[string]string{
		"GET /v1/catalog":        "reader",
		"GET /v1/spaces":         "reader embedder", // labels, for the reader's answers and for the embedder's
		"GET /v1/search":         "reader",
		"POST /v1/search/hybrid": "reader",
		"GET /v1/pages/1":        "reader",
		"GET /v1/sources/1/text": "reader",
		"POST /v1/sources":       "intake",
		"GET /v1/vectors/needed": "embedder",
		"PUT /v1/pages/1/vector": "embedder",
	}
	seen := map[string]bool{}
	for _, c := range f.received() {
		key := c.method + " " + c.path
		seen[key] = true
		if roles, ok := want[key]; !ok {
			t.Errorf("a call the adapter is not known to make: %s as %q", key, c.role)
		} else if c.role == "" || !slices.Contains(strings.Fields(roles), c.role) {
			t.Errorf("%s went out as %q, want %s", key, c.role, roles)
		}
	}
	for key := range want {
		if !seen[key] {
			t.Errorf("%s was never called", key)
		}
	}
}

// A sensitive space the reader's grant does not name is not in the store,
// as far as Keystone can tell: its pages are not listed, not found and not
// read. Waymarks answers a read of one with "forbidden", which reads here
// as not found, and never as the store being down.
func TestASensitiveSpaceTheGrantDoesNotName(t *testing.T) {
	s, f := allTokens(t)
	f.seed(knowledgetest.Content{
		Sources: []knowledgetest.SeedSource{{Space: "ledger", Name: "receipt.txt", Text: "The kiln was paid for in March."}},
		Pages:   []knowledgetest.SeedPage{{Space: "ledger", Path: "kiln", Title: "Kiln", Body: "The kiln fires on Fridays.", Cites: []string{"receipt.txt"}}},
	})
	entries, err := s.Catalog(bg)
	if err != nil || len(entries) != 2 {
		t.Fatalf("Catalog: %d entries, error %v", len(entries), err)
	}
	kiln := entries[0]
	if kiln.Space != "ledger" || kiln.Label != knowledge.LabelNormal {
		t.Fatalf("the kiln page before its space was marked: %+v", kiln)
	}
	page, err := s.ReadPage(bg, kiln.ID)
	if err != nil || len(page.Sources) != 1 {
		t.Fatalf("ReadPage: %+v, error %v", page, err)
	}

	// The owner marks the space and names it in no grant.
	f.set(func(f *fake) { f.spaces["ledger"] = "sensitive" })

	if entries, err := s.Catalog(bg); err != nil || len(entries) != 1 {
		t.Errorf("the catalog lists %d pages, error %v, want the schema page alone", len(entries), err)
	}
	if _, err := s.ReadPage(bg, kiln.ID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("ReadPage of a page the grant does not reach: error %v, want ErrNotFound", err)
	}
	if _, err := s.ReadSource(bg, page.Sources[0]); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("ReadSource of a source the grant does not reach: error %v, want ErrNotFound", err)
	}
	for _, q := range []knowledge.Query{{Text: "kiln"}, {Text: "kiln", Space: "ledger"}} {
		if hits, err := s.Search(bg, q); err != nil || len(hits) != 0 {
			t.Errorf("Search(%+v): %d hits, error %v, want none and no error", q, len(hits), err)
		}
	}
	if err := s.PutVector(bg, knowledge.PageVector{PageID: kiln.ID, Revision: page.Revision, Model: knowledgetest.VectorModel, Vector: []float32{1, 0, 0, 0}}); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("PutVector for a page the grant does not reach: error %v, want ErrNotFound", err)
	}
	// The intake token is refused there too, and that is the store's
	// answer, not a fault of the call.
	if _, err := s.AddSource(bg, knowledge.NewSource{Space: "ledger", Name: "chat-facts", Facts: []knowledge.Fact{{Text: "The kiln needs a new shelf."}}}); !errors.Is(err, knowledge.ErrUnavailable) {
		t.Errorf("AddSource in a space the grant does not reach: error %v, want ErrUnavailable", err)
	}
}

// The marker follows the catalog and the labels, and nothing else.
func TestTheMarker(t *testing.T) {
	s, f := allTokens(t)
	page := knowledgetest.SeedPage{Space: "home", Path: "well", Title: "Well", Kind: "note", Body: "The well is tested each spring."}
	f.seed(knowledgetest.Content{Pages: []knowledgetest.SeedPage{page}})
	marker := func() string {
		t.Helper()
		m, err := s.CatalogMarker(bg)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	first := marker()
	page.Body = "The well was tested and is clean."
	f.seed(knowledgetest.Content{Pages: []knowledgetest.SeedPage{page}})
	if marker() != first {
		t.Error("the marker changed when only a page's body did")
	}
	page.Title = "The well"
	f.seed(knowledgetest.Content{Pages: []knowledgetest.SeedPage{page}})
	renamed := marker()
	if renamed == first {
		t.Error("the marker did not change when a page's title did")
	}
	// A space with no page in it is made sensitive: no entry changes, and
	// the marker still does.
	f.set(func(f *fake) { f.spaces["attic"] = "normal" })
	withSpace := marker()
	f.seed(knowledgetest.Content{Sensitive: []string{"attic"}})
	if marker() == withSpace {
		t.Error("the marker did not change when an empty space was made sensitive")
	}
}

// What the adapter refuses by itself, without a call, and what it turns
// Waymarks' refusals into.
func TestWhatIsRefused(t *testing.T) {
	s, f := allTokens(t)
	f.received()
	for _, id := range []string{"", "0", "-1", "01", "1/../../v1/spaces", "p1", "1 ", "one"} {
		if _, err := s.ReadPage(bg, id); !errors.Is(err, knowledge.ErrNotFound) {
			t.Errorf("ReadPage(%q): error %v, want ErrNotFound", id, err)
		}
		if _, err := s.ReadSource(bg, id); !errors.Is(err, knowledge.ErrNotFound) {
			t.Errorf("ReadSource(%q): error %v, want ErrNotFound", id, err)
		}
	}
	if _, err := s.Search(bg, knowledge.Query{Vector: []float32{1, 0, 0, 0}}); !errors.Is(err, knowledge.ErrInvalid) {
		t.Errorf("Search with a vector and no model: error %v, want ErrInvalid", err)
	}
	if calls := f.received(); len(calls) != 0 {
		t.Errorf("a call that is wrong in itself reached the instance: %+v", calls)
	}
	// A model or a size the instance is not set up for is Waymarks' to refuse.
	if _, err := s.Search(bg, knowledge.Query{Vector: []float32{1, 0, 0, 0}, Model: "another-embed"}); !errors.Is(err, knowledge.ErrInvalid) {
		t.Errorf("Search with a vector of a model the instance does not take: error %v, want ErrInvalid", err)
	}
	for name, v := range map[string]knowledge.PageVector{
		"another model": {PageID: "1", Revision: "1", Model: "another-embed", Vector: []float32{1, 0, 0, 0}},
		"another size":  {PageID: "1", Revision: "1", Model: knowledgetest.VectorModel, Vector: []float32{1, 0}},
		"all zeros":     {PageID: "1", Revision: "1", Model: knowledgetest.VectorModel, Vector: []float32{0, 0, 0, 0}},
		"no model":      {PageID: "1", Revision: "1", Vector: []float32{1, 0, 0, 0}},
	} {
		if err := s.PutVector(bg, v); !errors.Is(err, knowledge.ErrInvalid) {
			t.Errorf("PutVector with %s: error %v, want ErrInvalid", name, err)
		}
	}
	// An instance that takes no vectors says so, as a call it will not take.
	f.set(func(f *fake) { f.model, f.size = "", 0 })
	if _, err := s.VectorsNeeded(bg, 0); !errors.Is(err, knowledge.ErrInvalid) {
		t.Errorf("VectorsNeeded on an instance that takes no vectors: error %v, want ErrInvalid", err)
	}
}

// The limits come from settings, and Waymarks' own ceiling is kept to.
func TestLimitsComeFromSettings(t *testing.T) {
	f := newFake(t)
	var pages []knowledgetest.SeedPage
	for _, path := range []string{"a", "b", "c"} {
		pages = append(pages, knowledgetest.SeedPage{Space: "home", Path: path, Title: path, Body: "The kettle is descaled monthly."})
	}
	f.seed(knowledgetest.Content{Pages: pages})
	ts := setUp(t, f, readerToken)
	setSetting(t, ts, SettingSearchResults, "2")
	s := openStore(t, ts)
	if hits, err := s.Search(bg, knowledge.Query{Text: "kettle"}); err != nil || len(hits) != 2 {
		t.Errorf("with the limit set to 2 a search returns %d hits, error %v", len(hits), err)
	}
	if hits, err := s.Search(bg, knowledge.Query{Text: "kettle", Limit: 3}); err != nil || len(hits) != 3 {
		t.Errorf("a search that names its own limit of 3 returns %d hits, error %v", len(hits), err)
	}
	if hits, err := s.Search(bg, knowledge.Query{Text: "kettle", Limit: 5000}); err != nil || len(hits) != 3 {
		t.Errorf("a search with a limit above Waymarks' own returns %d hits, error %v", len(hits), err)
	}
}

// Two tenants each have their own instance, and neither handle can name the
// other's.
func TestTwoTenantsDoNotShare(t *testing.T) {
	one, oneFake := allTokens(t)
	two, _ := allTokens(t)
	oneFake.seed(knowledgetest.Content{Pages: []knowledgetest.SeedPage{{Space: "home", Path: "bees", Title: "Beehive", Body: "The hive is inspected on Sundays."}}})
	if hits, err := one.Search(bg, knowledge.Query{Text: "hive"}); err != nil || len(hits) != 1 {
		t.Errorf("tenant one finds %d of its own one page, error %v", len(hits), err)
	}
	if hits, err := two.Search(bg, knowledge.Query{Text: "hive"}); err != nil || len(hits) != 0 {
		t.Errorf("tenant two finds %d of tenant one's pages, error %v", len(hits), err)
	}
	if entries, err := two.Catalog(bg); err != nil || len(entries) != 1 {
		t.Errorf("tenant two's catalog lists %d pages, error %v, want its own schema page alone", len(entries), err)
	}
}

// A space the list of spaces does not hold reads as sensitive.
func TestASpaceWithNoLabelReadsAsSensitive(t *testing.T) {
	l := labels{"home": knowledge.LabelNormal}
	if l.of("home") != knowledge.LabelNormal || l.of("elsewhere") != knowledge.LabelSensitive {
		t.Errorf("labels: home %q, elsewhere %q", l.of("home"), l.of("elsewhere"))
	}
	// A label that is not the word "normal" is sensitive.
	s, f := allTokens(t)
	f.seed(knowledgetest.Content{Pages: []knowledgetest.SeedPage{{Space: "home", Path: "a", Title: "A", Body: "Words."}}})
	f.set(func(f *fake) {
		f.spaces["home"] = "Normal"
		f.spaces["health"] = "sensitive" // so the catalog is marked, and the list of spaces is read
		f.grants[readerToken].named["health"] = true
	})
	entries, err := s.Catalog(bg)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if want := e.Space != "wiki"; e.Label.Sensitive() != want {
			t.Errorf("the entry in %s has label %q", e.Space, e.Label)
		}
	}
}
