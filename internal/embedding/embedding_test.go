package embedding

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Every name and every line of content in these tests is invented.

var bg = context.Background()

func model(name string, class models.Class, caps ...models.Capability) models.Model {
	return models.Model{Name: name, Label: name, Class: class, ContextWindow: 8192, OutputLimit: 1, Capabilities: caps}
}

var (
	localEmbed   = model("house-embed", models.ClassLocal, models.CapEmbedding)
	trustedEmbed = model("friend-embed", models.ClassTrusted, models.CapEmbedding)
	hostedEmbed  = model("cloud-embed", models.ClassHosted, models.CapEmbedding)
	localChat    = model("house-chat", models.ClassLocal, models.CapTools)
)

// A sensitive page goes only to a local or trusted model, and so does a
// held chat's query. A blank label reads as sensitive.
func TestTheRule(t *testing.T) {
	for _, c := range []struct {
		label knowledge.Label
		m     models.Model
		want  bool
	}{
		{knowledge.LabelNormal, hostedEmbed, true},
		{knowledge.LabelSensitive, hostedEmbed, false},
		{"", hostedEmbed, false},
		{"secret", hostedEmbed, false},
		{knowledge.LabelSensitive, localEmbed, true},
		{knowledge.LabelSensitive, trustedEmbed, true},
		{knowledge.LabelSensitive, model("no-class", "", models.CapEmbedding), false},
	} {
		if got := MayEmbed(c.label, c.m); got != c.want {
			t.Errorf("MayEmbed(%q, %s) = %v", c.label, c.m.Class, got)
		}
	}
	if MayEmbedQuery(true, hostedEmbed) || !MayEmbedQuery(true, localEmbed) || !MayEmbedQuery(false, hostedEmbed) {
		t.Error("a held chat's query may reach a hosted embedding model, or an open chat's may not")
	}
}

// The model named is used; with none, the first local, then the first
// trusted; a hosted model only when named; never a chat model.
func TestChoose(t *testing.T) {
	all := []models.Model{hostedEmbed, trustedEmbed, localChat, localEmbed}
	pick := func(named string, usable []models.Model, can func(models.Model) bool) string {
		m, err := Choose(named, usable, can)
		if err != nil {
			return "error"
		}
		return m.Name
	}
	for _, c := range []struct {
		named  string
		usable []models.Model
		can    func(models.Model) bool
		want   string
	}{
		{"", all, nil, "house-embed"},
		{"cloud-embed", all, nil, "cloud-embed"},
		{"house-chat", all, nil, "house-embed"},
		{"missing", all, nil, "house-embed"},
		{"", []models.Model{hostedEmbed, trustedEmbed}, nil, "friend-embed"},
		{"", []models.Model{hostedEmbed}, nil, "error"},
		{"missing", []models.Model{hostedEmbed}, nil, "error"},
		{"", all, func(m models.Model) bool { return m.Class != models.ClassLocal }, "friend-embed"},
	} {
		if got := pick(c.named, c.usable, c.can); got != c.want {
			t.Errorf("Choose(%q) gave %s, want %s", c.named, got, c.want)
		}
	}
	if _, err := Choose("", nil, nil); !errors.Is(err, ErrNoModel) {
		t.Errorf("no models gave %v", err)
	}
	withProvider := hostedEmbed
	withProvider.ProviderModel = "example-embed-small"
	if VectorModel(withProvider) != "example-embed-small" || VectorModel(localEmbed) != "house-embed" {
		t.Error("a vector's model name is not the provider's name, or the registry name without one")
	}
}

// A page's text is its title and body, cut at a word's end.
func TestPageText(t *testing.T) {
	p := knowledge.Page{Title: "Runner beans", Body: "Runner beans go in the north bed after the last frost."}
	if got := PageText(p, 1000); got != "Runner beans\n\nRunner beans go in the north bed after the last frost." {
		t.Errorf("the text is %q", got)
	}
	if got := PageText(p, 30); len(got) > 30 || strings.HasSuffix(got, " ") || !strings.HasPrefix(got, "Runner beans") {
		t.Errorf("the cut text is %q", got)
	}
}

// fixture is a tenant with a built-in store of three normal pages and one
// in a sensitive space.
func fixture(t *testing.T) (*tenant.Store, knowledge.Store) {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	ts, err := in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ts.Close() })
	for _, p := range []builtin.NewPage{
		{Space: "garden", Path: "apple-tree", Title: "Apple tree", Body: "The apple tree by the gate is pruned in February."},
		{Space: "garden", Path: "runner-beans", Title: "Runner beans", Body: "Runner beans go in the north bed."},
		{Space: "home", Path: "shed-roof", Title: "Shed roof", Body: "The shed roof needs new felt before winter."},
		{Space: "health", Path: "sleep", Title: "Sleep notes", Body: "Melatonin helped with the insomnia in March."},
	} {
		if _, err := builtin.WritePage(bg, ts, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := builtin.SetLabel(bg, ts, "health", knowledge.LabelSensitive); err != nil {
		t.Fatal(err)
	}
	ks, err := knowledge.Open(bg, builtin.Name, ts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ks.Close() })
	return ts, ks
}

// recorder is an EmbedFunc that keeps every text it is sent and gives each
// a vector of its own.
type recorder struct {
	texts []string
	calls int
	fail  error
}

func (r *recorder) embed(_ context.Context, texts []string) ([][]float32, error) {
	r.calls++
	if r.fail != nil {
		return nil, r.fail
	}
	r.texts = append(r.texts, texts...)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, float32(len(r.texts) + i)}
	}
	return out, nil
}

func (r *recorder) sent(word string) bool {
	for _, t := range r.texts {
		if strings.Contains(t, word) {
			return true
		}
	}
	return false
}

var opts = Options{BatchSize: 2, MaxPages: 100, ListMax: 1000, TextMaxChars: 2000}

// A hosted model is sent the normal pages, in batches, and never the
// sensitive one, which is held and stays without a vector; a local model
// then embeds it.
func TestRefreshHoldsSensitivePages(t *testing.T) {
	_, ks := fixture(t)
	rec := &recorder{}
	res, err := Refresh(bg, ks, hostedEmbed, rec.embed, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Embedded != 3 || res.Held != 1 || rec.calls != 2 || res.Listed != 4 {
		t.Errorf("the hosted refresh: %+v after %d calls, want three embedded in two calls and one held", res, rec.calls)
	}
	if rec.sent("Melatonin") || rec.sent("Sleep notes") {
		t.Error("the hosted model was sent the sensitive page")
	}
	if !rec.sent("Apple tree") || !rec.sent("north bed") {
		t.Errorf("the hosted model was not sent the normal pages: %q", rec.texts)
	}
	if n := Note(res); !strings.Contains(n, "1 page in a sensitive space has no vector") || strings.Contains(n, "Sleep") {
		t.Errorf("the note is %q", n)
	}
	needs, err := ks.VectorsNeeded(bg, VectorModel(hostedEmbed), 0)
	if err != nil || len(needs) != 1 || needs[0].Title != "Sleep notes" {
		t.Errorf("after the hosted refresh the pages needing a vector are %+v, error %v", needs, err)
	}
	// Again: nothing new is sent, and the sensitive page is still held.
	again := &recorder{}
	if res, err := Refresh(bg, ks, hostedEmbed, again.embed, opts); err != nil || again.calls != 0 || res.Held != 1 {
		t.Errorf("a second hosted refresh: %+v, %d calls, error %v", res, again.calls, err)
	}
	// A local model is another model: every page needs its vector, and the
	// sensitive one is sent too.
	local := &recorder{}
	res, err = Refresh(bg, ks, localEmbed, local.embed, opts)
	if err != nil || res.Embedded != 4 || res.Held != 0 || !local.sent("Melatonin") {
		t.Errorf("the local refresh: %+v, error %v", res, err)
	}
}

// A space made sensitive after the list was made is held when the page is
// read: the label is asked twice.
func TestRefreshAsksTheLabelAgain(t *testing.T) {
	ts, ks := fixture(t)
	rec := &recorder{}
	turned := false
	embed := func(ctx context.Context, texts []string) ([][]float32, error) {
		if !turned {
			// Between the first batch and the second, the home space is made
			// sensitive.
			turned = true
			if err := builtin.SetLabel(bg, ts, "home", knowledge.LabelSensitive); err != nil {
				return nil, err
			}
		}
		return rec.embed(ctx, texts)
	}
	res, err := Refresh(bg, ks, hostedEmbed, embed, opts)
	if err != nil {
		t.Fatal(err)
	}
	if rec.sent("Shed roof") || res.Held != 2 || res.Embedded != 2 {
		t.Errorf("a page in a space made sensitive mid-refresh: %+v, sent %q", res, rec.texts)
	}
}

// A refusal by the reserve stops the refresh and leaves the rest waiting;
// a page that changed while its vector was made is stale; a store that
// takes no vectors says so; the limits hold.
func TestRefreshStops(t *testing.T) {
	_, ks := fixture(t)
	rec := &recorder{fail: &seam.Error{Kind: seam.ErrSpendCap, Message: "The embedding reserve is spent."}}
	res, err := Refresh(bg, ks, localEmbed, rec.embed, opts)
	if err != nil || rec.calls != 1 || res.Embedded != 0 || res.Waiting != 4 || res.Stopped != "The embedding reserve is spent." {
		t.Errorf("a refused call: %+v after %d calls, error %v", res, rec.calls, err)
	}
	if n := Note(res); !strings.Contains(n, "stopped early") || !strings.Contains(n, "4 pages wait") {
		t.Errorf("the note is %q", n)
	}
	one := opts
	one.MaxPages = 1
	res, err = Refresh(bg, ks, localEmbed, (&recorder{}).embed, one)
	if err != nil || res.Embedded != 1 || res.Waiting != 3 {
		t.Errorf("a refresh of one page at most: %+v, error %v", res, err)
	}
	none, err := knowledge.Open(bg, knowledge.None, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(bg, none, localEmbed, (&recorder{}).embed, opts); !errors.Is(err, ErrNoVectors) {
		t.Errorf("a store with no vectors answered %v", err)
	}
	if _, err := Refresh(bg, ks, localChat, (&recorder{}).embed, opts); !errors.Is(err, ErrNoModel) {
		t.Errorf("a chat model was let make vectors: %v", err)
	}
	if _, err := Refresh(bg, ks, localEmbed, (&recorder{}).embed, Options{}); !errors.Is(err, ErrOptions) {
		t.Errorf("no options answered %v", err)
	}
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := Refresh(ctx, ks, trustedEmbed, (&recorder{}).embed, opts); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled refresh answered %v", err)
	}
}

// The reserve counts embedding calls to models that are not local, and the
// state is kept and read back.
func TestReserveAndState(t *testing.T) {
	ts, _ := fixture(t)
	reserve, err := NewReserve(0.5)
	if err != nil || reserve != 500000 {
		t.Fatalf("the reserve is %d, error %v", reserve, err)
	}
	if _, err := NewReserve(-1); err == nil {
		t.Error("a negative reserve is accepted")
	}
	now := time.Now()
	for _, rec := range []usage.Record{
		{RequestID: "embed-a", Kind: Kind, Model: "cloud-embed", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 400000},
		{RequestID: "embed-b", Kind: Kind, Model: "house-embed", Class: models.ClassLocal, Outcome: usage.OutcomeOK, CostMicros: 900000},
		{RequestID: "chat-a", Kind: "chat", Model: "cloud-chat", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 900000},
	} {
		if _, err := usage.Add(bg, ts, rec); err != nil {
			t.Fatal(err)
		}
	}
	if st, err := Allow(bg, ts, reserve, now, 100000); err != nil || st.State == usage.StateStopped {
		t.Errorf("a call that fits the reserve: %+v, error %v", st, err)
	}
	if st, err := Allow(bg, ts, reserve, now, 100001); err != nil || st.State != usage.StateStopped {
		t.Errorf("a call past the reserve: %+v, error %v", st, err)
	}

	if _, ok, err := Load(bg, ts, builtin.Name); ok || err != nil {
		t.Errorf("a state before any refresh: %v, error %v", ok, err)
	}
	st := State{Adapter: builtin.Name, Class: "hosted", Result: Result{Model: "cloud-embed", VectorModel: "cloud-embed", Listed: 4, Embedded: 3, Held: 1}, Note: "a note"}
	if err := Save(bg, ts, st); err != nil {
		t.Fatal(err)
	}
	st.Embedded = 0
	if err := Save(bg, ts, st); err != nil {
		t.Fatal(err)
	}
	back, ok, err := Load(bg, ts, builtin.Name)
	if err != nil || !ok || back.Held != 1 || back.Embedded != 0 || back.Note != "a note" || back.At.IsZero() {
		t.Errorf("the state read back is %+v, %v, error %v", back, ok, err)
	}
}
