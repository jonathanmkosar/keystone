package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// ErrNotEmbedder is wrapped when a model asked to make vectors cannot: its
// entry does not have the embedding capability, or its adapter makes none.
// The fake and the Chat Completions adapter make vectors (P6-08); the
// Anthropic and Responses adapters do not.
var ErrNotEmbedder = errors.New("the model cannot make vectors")

// OpenEmbedder returns a tenant's embedding model and the seam.Embedder
// that calls it, as Open does for a model that writes replies. The errors
// are Open's, and ErrNotEmbedder.
func OpenEmbedder(ctx context.Context, s *tenant.Store, set Set, name string) (models.Model, seam.Embedder, error) {
	m, p, err := Open(ctx, s, set, name)
	if err != nil {
		return models.Model{}, nil, err
	}
	if !m.Has(models.CapEmbedding) {
		return models.Model{}, nil, fmt.Errorf("%w: model %s does not have the embedding capability", ErrNotEmbedder, m.Name)
	}
	e, ok := p.(seam.Embedder)
	if !ok {
		return models.Model{}, nil, fmt.Errorf("%w: model %s uses the %s adapter, which makes no vectors", ErrNotEmbedder, m.Name, m.Adapter)
	}
	return m, e, nil
}

// Embeds reports whether an adapter of this build makes vectors, so a
// choice among embedding models can pass over one that could not be
// opened as one. It builds nothing and reads no key.
func Embeds(adapter string) bool {
	return adapter == models.AdapterFake || adapter == models.AdapterChatCompletions
}
