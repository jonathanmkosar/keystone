package seam

import (
	"math"
	"slices"
	"testing"
)

// An embedding request needs a model and texts that are not blank, and it
// goes through JSON and comes back the same.
func TestEmbedRequest(t *testing.T) {
	good := EmbedRequest{Model: "house-embed", Texts: []string{"The apple tree is pruned in February.", "Tomatoes"}}
	if err := ValidateEmbedRequest(good); err != nil {
		t.Fatalf("a good request is refused: %v", err)
	}
	raw, err := EncodeEmbedRequest(good)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeEmbedRequest(raw)
	if err != nil || back.Model != good.Model || !slices.Equal(back.Texts, good.Texts) {
		t.Errorf("the request came back as %+v, error %v", back, err)
	}
	for name, bad := range map[string]EmbedRequest{
		"no model":   {Texts: []string{"a"}},
		"no texts":   {Model: "house-embed"},
		"blank text": {Model: "house-embed", Texts: []string{"a", "  "}},
	} {
		if err := ValidateEmbedRequest(bad); err == nil {
			t.Errorf("a request with %s is accepted", name)
		}
	}
	if _, err := DecodeEmbedRequest([]byte(`{"model":"m","texts":["a"],"extra":1}`)); err == nil {
		t.Error("a request with an unknown field is read")
	}
}

// An answer must hold one usable vector for each text, all of one size.
func TestEmbedResponse(t *testing.T) {
	req := EmbedRequest{Model: "house-embed", Texts: []string{"a", "b"}}
	good := EmbedResponse{Model: "house-embed", Vectors: [][]float32{{1, 0}, {0.6, 0.8}}, Usage: Usage{InputTokens: 2}}
	if err := ValidateEmbedResponse(req, good); err != nil {
		t.Fatalf("a good answer is refused: %v", err)
	}
	nan := float32(math.NaN())
	for name, bad := range map[string]EmbedResponse{
		"one vector short": {Vectors: [][]float32{{1, 0}}},
		"two sizes":        {Vectors: [][]float32{{1, 0}, {1, 0, 0}}},
		"an empty vector":  {Vectors: [][]float32{{1, 0}, {}}},
		"a NaN":            {Vectors: [][]float32{{1, 0}, {nan, 1}}},
		"all zeros":        {Vectors: [][]float32{{1, 0}, {0, 0}}},
		"negative tokens":  {Vectors: [][]float32{{1, 0}, {0, 1}}, Usage: Usage{InputTokens: -1}},
	} {
		if err := ValidateEmbedResponse(req, bad); err == nil {
			t.Errorf("an answer with %s is accepted", name)
		}
	}
}
