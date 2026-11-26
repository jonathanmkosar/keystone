package adapters

import (
	"context"
	"errors"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// An embedding model in the registry opens as an embedder and makes
// vectors; a chat model, and a model whose adapter makes none, do not.
func TestOpenEmbedder(t *testing.T) {
	s := store(t)
	lim := limits(t)
	embed := models.Model{Name: "test-embed", Label: "Test embed", Adapter: models.AdapterFake,
		ContextWindow: 8192, OutputLimit: 1, Capabilities: []models.Capability{models.CapEmbedding}, Class: models.ClassLocal}
	for _, m := range []models.Model{embed, scripted} {
		if err := models.Add(context.Background(), s, lim, m); err != nil {
			t.Fatal(err)
		}
	}
	set, err := Fake(nil, fake.Options{EmbeddingSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	m, e, err := OpenEmbedder(context.Background(), s, set, "test-embed")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := e.Embed(context.Background(), seam.EmbedRequest{Model: m.Name, Texts: []string{"Runner beans"}})
	if err != nil || len(resp.Vectors) != 1 || len(resp.Vectors[0]) != 16 {
		t.Fatalf("the embedder answered %+v, error %v", resp, err)
	}
	if _, _, err := OpenEmbedder(context.Background(), s, set, "scripted"); !errors.Is(err, ErrNotEmbedder) {
		t.Errorf("a chat model opened as an embedder: %v", err)
	}
	// An adapter whose provider makes no vectors.
	plain := Set{models.AdapterFake: func(models.Model, string) (seam.Provider, error) { return replyOnly{}, nil }}
	if _, _, err := OpenEmbedder(context.Background(), s, plain, "test-embed"); !errors.Is(err, ErrNotEmbedder) {
		t.Errorf("a provider with no Embed opened as an embedder: %v", err)
	}
	if _, _, err := OpenEmbedder(context.Background(), s, set, "missing"); !errors.Is(err, models.ErrUnknown) {
		t.Errorf("a model the registry does not hold answered %v", err)
	}
	for adapter, want := range map[string]bool{
		models.AdapterFake: true, models.AdapterChatCompletions: true, models.AdapterAnthropic: false, models.AdapterResponses: false,
	} {
		if Embeds(adapter) != want {
			t.Errorf("Embeds(%s) is %v", adapter, !want)
		}
	}
}

// replyOnly is a provider that writes replies and makes no vectors.
type replyOnly struct{}

func (replyOnly) Stream(context.Context, seam.Request, func(seam.Event) error) error { return nil }
