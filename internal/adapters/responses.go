package adapters

import (
	"github.com/jonathanmkosar/keystone/internal/adapters/responses"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Responses returns the builder for the OpenAI Responses adapter. Whoever
// starts the server puts it in its Set under models.AdapterResponses:
//
//	opts, err := responses.DefaultOptions()
//	set[models.AdapterResponses] = adapters.Responses(opts)
//
// Each entry gets a provider of its own, since the endpoint, the
// provider's name for the model and the key are the entry's.
func Responses(opts responses.Options) Builder {
	return func(m models.Model, key string) (seam.Provider, error) {
		p, err := responses.New(m, key, opts)
		if err != nil {
			// A nil *Provider in the interface would not be nil to Open.
			return nil, err
		}
		return p, nil
	}
}
