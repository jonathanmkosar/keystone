package seam

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// Embedding is a call of its own shape (P6-08): texts in, one vector for
// each text out. An embedding model writes no reply, so it is not a
// Provider: an adapter whose provider can embed also has the Embed method,
// and a registry entry says so with the embedding capability.

// EmbedRequest is one embedding call.
type EmbedRequest struct {
	// Model is the registry name of the embedding model.
	Model string `json:"model"`
	// Texts are what is turned into vectors, in order. None may be blank.
	Texts []string `json:"texts"`
}

// EmbedResponse is what an embedding call gave back.
type EmbedResponse struct {
	// Model is the registry name of the model that made the vectors.
	Model string `json:"model"`
	// Vectors holds one vector for each text of the request, in the same
	// order, all of one size.
	Vectors [][]float32 `json:"vectors"`
	// Usage counts the call's tokens. An embedding call reads and writes
	// nothing else, so only InputTokens is ever more than zero.
	Usage Usage `json:"usage"`
}

// Embedder is anything that turns texts into vectors: the Chat Completions
// adapter, and the fake. Like a Provider, it is safe to use from several
// goroutines at once, it never puts a key in an error, and a failed call
// returns a *Error. A call whose context is done returns the context's
// error.
type Embedder interface {
	Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error)
}

// ValidateEmbedRequest refuses a request with no model, no texts, or a
// blank text.
func ValidateEmbedRequest(r EmbedRequest) error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("an embedding request needs the model's name")
	}
	if len(r.Texts) == 0 {
		return fmt.Errorf("an embedding request needs at least one text")
	}
	for i, t := range r.Texts {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("text %d of the embedding request is blank", i+1)
		}
	}
	return nil
}

// ValidateEmbedResponse refuses an answer that is not one usable vector
// for each text of the request: a missing or extra vector, vectors of more
// than one size, an empty one, a number that is not finite, or a vector of
// nothing but zeros, which points nowhere.
func ValidateEmbedResponse(req EmbedRequest, resp EmbedResponse) error {
	if len(resp.Vectors) != len(req.Texts) {
		return fmt.Errorf("the embedding answer has %d vectors for %d texts", len(resp.Vectors), len(req.Texts))
	}
	size := 0
	for i, v := range resp.Vectors {
		if len(v) == 0 {
			return fmt.Errorf("vector %d of the embedding answer is empty", i+1)
		}
		if i == 0 {
			size = len(v)
		} else if len(v) != size {
			return fmt.Errorf("vector %d of the embedding answer has %d numbers, and the first has %d", i+1, len(v), size)
		}
		zero := true
		for _, x := range v {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return fmt.Errorf("vector %d of the embedding answer holds a number that is not finite", i+1)
			}
			if x != 0 {
				zero = false
			}
		}
		if zero {
			return fmt.Errorf("vector %d of the embedding answer is all zeros", i+1)
		}
	}
	return ValidateUsage(resp.Usage)
}

func EncodeEmbedRequest(r EmbedRequest) ([]byte, error) { return encode(r, ValidateEmbedRequest) }
func DecodeEmbedRequest(raw []byte) (EmbedRequest, error) {
	return decode(raw, ValidateEmbedRequest)
}
