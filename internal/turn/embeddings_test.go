package turn

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/embedding"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Every page, name and word group in these tests is invented.

var (
	houseEmbed = models.Model{
		Name: "house-embed", Label: "House Embed", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:11434/v1",
		ProviderModel: "example-embed:v1", ContextWindow: 8192, OutputLimit: 1, Capabilities: []models.Capability{models.CapEmbedding},
		Class: models.ClassLocal,
	}
	cloudEmbed = models.Model{
		Name: "cloud-embed", Label: "Cloud Embed", Adapter: models.AdapterChatCompletions, Endpoint: "https://api.provider-two.example/v1",
		ProviderModel: "example-embed-small", ContextWindow: 8192, OutputLimit: 1, Capabilities: []models.Capability{models.CapEmbedding},
		Class: models.ClassHosted, Price: models.Price{Input: 0.02},
	}
)

// embedRecorder is an embedding model that keeps every text it is sent,
// and makes the fake's vectors with a few invented word groups.
type embedRecorder struct {
	*fake.Provider
	mu    *sync.Mutex
	texts *[]string
}

func (e embedRecorder) Embed(ctx context.Context, req seam.EmbedRequest) (seam.EmbedResponse, error) {
	e.mu.Lock()
	*e.texts = append(*e.texts, req.Texts...)
	e.mu.Unlock()
	return e.Provider.Embed(ctx, req)
}

// embedRig is the ordinary tenant with the built-in store, a local chat
// model, and embedding models whose texts are kept by name.
type embedRig struct {
	*rig
	sv    *served
	mu    sync.Mutex
	texts map[string]*[]string
	chat  []string
	h     http.Handler
}

func newEmbedRig(t *testing.T, embedders ...models.Model) *embedRig {
	t.Helper()
	g := &embedRig{rig: newRig(t), texts: map[string]*[]string{}}
	maker, err := fake.New(nil, fake.Options{EmbeddingSize: 1024, Meanings: [][]string{{"apple", "orchard"}, {"pruned", "trim", "trimmed"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range embedders {
		g.texts[m.Name] = new([]string)
	}
	g.extra = adapters.Set{models.AdapterChatCompletions: func(m models.Model, _ string) (seam.Provider, error) {
		if m.Has(models.CapEmbedding) {
			return embedRecorder{Provider: maker, mu: &g.mu, texts: g.texts[m.Name]}, nil
		}
		return kept{mu: &g.mu, got: &g.chat}, nil
	}}
	g.addModels(append([]models.Model{houseModel}, embedders...)...)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	seedKnowledge(g.rig)
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if _, err := builtin.WritePage(context.Background(), s, builtin.NewPage{Space: "garden", Path: "plants/apple-tree", Title: "Apple tree", Kind: "plant",
			Body: "The apple tree by the gate is pruned in February, before the buds open."}); err != nil {
			t.Fatal(err)
		}
	})
	g.sv = g.serve(nil)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, EmbeddingRoutes(g.sv.runner))
	if err != nil {
		t.Fatal(err)
	}
	g.h = h
	return g
}

// embeddings calls one of the embedding routes as the ordinary tenant.
func (g *embedRig) embeddings(method, path string) EmbeddingsReply {
	g.t.Helper()
	req := httptest.NewRequest(method, api.Prefix+"knowledge/embeddings"+path, nil)
	req.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.sv.tokens[g.ordinary.ID]})
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, req)
	raw, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusOK {
		g.t.Fatalf("%s %s answered %d: %s", method, path, rec.Code, raw)
	}
	var out EmbeddingsReply
	if err := json.Unmarshal(raw, &out); err != nil {
		g.t.Fatal(err)
	}
	return out
}

// sent reports whether an embedding model was sent a text holding word.
func (g *embedRig) sent(model, word string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, t := range *g.texts[model] {
		if strings.Contains(t, word) {
			return true
		}
	}
	return false
}

// A local embedding model makes every page's vector, sensitive ones too,
// and a question that shares no word with a page recalls it by meaning,
// with the call recorded; with recall by meaning off, it does not.
func TestRecallByMeaningInATurn(t *testing.T) {
	g := newEmbedRig(t, houseEmbed)
	if st := g.embeddings("GET", ""); st.Model == nil || st.Model.Name != houseEmbed.Name || !st.TakesVectors || !st.SearchesByMeaning || st.Last != nil {
		t.Fatalf("before a refresh: %+v", st)
	}
	done := g.embeddings("POST", "/refresh")
	if !done.Refreshed || done.Last == nil || done.Last.Embedded != 3 || done.Last.Held != 0 || done.Last.VectorModel != "example-embed:v1" {
		t.Fatalf("the refresh: %+v, last %+v", done, done.Last)
	}
	if !g.sent(houseEmbed.Name, "Melatonin") {
		t.Error("the local embedding model was not sent the sensitive page")
	}

	ask, _ := json.Marshal(SendBody{Text: "When is the orchard trimmed?", Model: houseModel.Name})
	events, turnID, requestID := g.sv.send(g.ordinary.ID, g.sv.newChat(g.ordinary.ID), string(ask))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	if call := sentFor(g.rig, requestID); !strings.Contains(call, "pruned in February") {
		t.Errorf("the page was not recalled by meaning:\n%s", call)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		ctx := context.Background()
		rec, ok, err := recall.Saved(ctx, s, turnID)
		if err != nil || !ok || rec.MeaningModel != "example-embed:v1" || rec.Kept == 0 || rec.Hits[0].FoundBy != recall.FoundByMeaning {
			t.Errorf("the record: %+v (%v, %v)", rec, ok, err)
		}
		entries, err := audit.List(ctx, s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil {
			t.Fatal(err)
		}
		ended := false
		for _, e := range entries {
			if e.Action == houseEmbed.Name && e.Outcome == audit.OutcomeOK && strings.Contains(e.Received, "sha256") && e.Usage.InputTokens > 0 {
				ended = true
			}
		}
		if !ended {
			t.Errorf("the query's embedding call has no end entry in the audit log: %+v", entries)
		}
		records, err := usage.List(ctx, s, usage.Filter{Month: usage.MonthOf(time.Now()), Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range records {
			found = found || (r.RequestID == requestID && r.Kind == embedding.Kind && r.Model == houseEmbed.Name && r.Outcome == usage.OutcomeOK)
		}
		if !found {
			t.Error("the query's embedding call has no usage record")
		}
	})

	g.set(tenantsettings.Person, recall.SettingMeaning, "false")
	_, _, requestID = g.sv.send(g.ordinary.ID, g.sv.newChat(g.ordinary.ID), string(ask))
	if call := sentFor(g.rig, requestID); strings.Contains(call, "pruned in February") {
		t.Errorf("with recall by meaning off the page was still recalled:\n%s", call)
	}
}

// A hosted embedding model is never sent a sensitive page, nor the query of
// a chat that holds sensitive content; the person is told, and the reserve
// stops a call that would pass it.
func TestNoSensitivePageReachesAHostedEmbeddingModel(t *testing.T) {
	g := newEmbedRig(t, cloudEmbed)
	if st := g.embeddings("GET", ""); st.Model != nil {
		t.Fatalf("a hosted embedding model was chosen unasked: %+v", st.Model)
	}
	g.set(tenantsettings.Person, KeyEmbeddingModel, cloudEmbed.Name)
	done := g.embeddings("POST", "/refresh")
	if done.Last == nil || done.Last.Embedded != 2 || done.Last.Held != 1 || done.Model == nil || done.Model.MayReadSensitive {
		t.Fatalf("the hosted refresh: %+v, last %+v", done, done.Last)
	}
	if g.sent(cloudEmbed.Name, "Melatonin") || g.sent(cloudEmbed.Name, "Sleep notes") {
		t.Error("the hosted embedding model was sent the sensitive page")
	}
	if !strings.Contains(done.Note, "1 page in a sensitive space has no vector") {
		t.Errorf("the person is not told: %q", done.Note)
	}
	if st := g.embeddings("GET", ""); st.Last == nil || st.Last.Held != 1 || st.Note != done.Note {
		t.Errorf("the status after the refresh: %+v", st)
	}

	// A local chat model recalls the sensitive page, which holds the chat;
	// the next message's query does not go to the hosted embedding model.
	chat := g.sv.newChat(g.ordinary.ID)
	first, _ := json.Marshal(SendBody{Text: "What helped with the insomnia?", Model: houseModel.Name})
	if end := last(t, mustSend(g, chat, first)); end.Status != StatusDone {
		t.Fatalf("the first turn ended as %+v", end)
	}
	next, _ := json.Marshal(SendBody{Text: "Did the clinic mention evening routines?", Model: houseModel.Name})
	events, turnID, _ := g.sv.send(g.ordinary.ID, chat, string(next))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the held turn ended as %+v", end)
	}
	if g.sent(cloudEmbed.Name, "routines") {
		t.Error("the query of a held chat went to the hosted embedding model")
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		rec, ok, _ := recall.Saved(context.Background(), s, turnID)
		if !ok || !strings.Contains(rec.MeaningNote, "sensitive") || rec.MeaningModel != "" {
			t.Errorf("the held turn's record: %+v", rec)
		}
	})

	// The reserve at zero: a changed page waits, and the refused call is
	// recorded.
	g.set(tenantsettings.Person, embedding.SettingReserve, "0")
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if _, err := builtin.WritePage(context.Background(), s, builtin.NewPage{Space: "garden", Path: "plants/runner-beans", Title: "Runner beans", Kind: "plant",
			Body: "Runner beans go in the north bed after the last frost, two weeks later on a cold spring."}); err != nil {
			t.Fatal(err)
		}
	})
	stopped := g.embeddings("POST", "/refresh")
	if stopped.Last == nil || stopped.Last.Embedded != 0 || !strings.Contains(stopped.Last.Stopped, "embedding reserve") || stopped.Last.Waiting != 1 {
		t.Errorf("a refresh past the reserve: %+v", stopped.Last)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		records, err := usage.List(context.Background(), s, usage.Filter{Month: usage.MonthOf(time.Now()), Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		refused := false
		for _, r := range records {
			refused = refused || (r.Kind == embedding.Kind && r.Outcome == usage.OutcomeRefused && r.CostMicros == 0)
		}
		if !refused {
			t.Error("the refused embedding call has no usage record")
		}
	})
}

// With no embedding model, recall is by words alone, nothing is recorded
// of meaning, and no embedding call is made.
func TestNoEmbeddingModelChangesNothing(t *testing.T) {
	g := newEmbedRig(t)
	st := g.embeddings("POST", "/refresh")
	if st.Model != nil || st.Refreshed || !strings.Contains(st.Note, "no embedding model") {
		t.Errorf("with no embedding model the refresh: %+v", st)
	}
	ask, _ := json.Marshal(SendBody{Text: "When do the runner beans go in?", Model: houseModel.Name})
	events, turnID, requestID := g.sv.send(g.ordinary.ID, g.sv.newChat(g.ordinary.ID), string(ask))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		rec, ok, _ := recall.Saved(context.Background(), s, turnID)
		if !ok || rec.MeaningModel != "" || rec.MeaningNote != "" || rec.Kept != 1 || rec.Hits[0].FoundBy != "" {
			t.Errorf("the record with no embedding model: %+v", rec)
		}
		entries, _ := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		for _, e := range entries {
			if e.Action != houseModel.Name {
				t.Errorf("a model call to %s was made", e.Action)
			}
		}
	})
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if _, ok, _ := embedding.Load(context.Background(), s, builtin.Name); ok {
			t.Error("a state was kept with no embedding model")
		}
	})
}

func mustSend(g *embedRig, chatID string, body []byte) []event {
	g.t.Helper()
	events, _, _ := g.sv.send(g.ordinary.ID, chatID, string(body))
	return events
}
