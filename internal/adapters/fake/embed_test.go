package fake

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

func embedder(t *testing.T, meanings ...[]string) *Provider {
	t.Helper()
	p, err := New(nil, Options{EmbeddingSize: 1024, Meanings: meanings})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / math.Sqrt(na*nb)
}

func vectors(t *testing.T, p *Provider, texts ...string) [][]float32 {
	t.Helper()
	resp, err := p.Embed(context.Background(), seam.EmbedRequest{Model: "test-embed", Texts: texts})
	if err != nil {
		t.Fatal(err)
	}
	if err := seam.ValidateEmbedResponse(seam.EmbedRequest{Model: "test-embed", Texts: texts}, resp); err != nil {
		t.Fatal(err)
	}
	return resp.Vectors
}

// The same text always gives the same vector, unit length and of the size
// set, and the call is counted.
func TestEmbedIsDeterministic(t *testing.T) {
	p := embedder(t)
	one := vectors(t, p, "The apple tree is pruned in February.")
	two := vectors(t, p, "The apple tree is pruned in February.")
	if !slices.Equal(one[0], two[0]) || len(one[0]) != 1024 {
		t.Fatalf("two vectors of one text differ, or have %d numbers", len(one[0]))
	}
	if n := cosine(one[0], one[0]); math.Abs(n-1) > 1e-5 {
		t.Errorf("a vector's cosine with itself is %v", n)
	}
	resp, err := p.Embed(context.Background(), seam.EmbedRequest{Model: "test-embed", Texts: []string{"one two three", "four"}})
	if err != nil || resp.Usage.InputTokens != 4 || resp.Model != "test-embed" {
		t.Errorf("the call counted %d tokens for model %q, error %v", resp.Usage.InputTokens, resp.Model, err)
	}
}

// Words of a meaning group land together, so a question finds a page that
// holds none of its words, and an unrelated question finds nothing near.
func TestEmbedByMeaning(t *testing.T) {
	p := embedder(t, []string{"apple", "orchard"}, []string{"prune", "pruned", "trim"})
	v := vectors(t, p,
		"Apple tree. The apple tree by the gate is pruned in February, before the buds open.",
		"When is the orchard trimmed?",
		"Shed roof. The shed roof needs new felt before winter.",
		"What is the capital of a country far away?",
	)
	page, question, other, nothing := v[0], v[1], v[2], v[3]
	if c := cosine(page, question); c < 0.5 {
		t.Errorf("the question by meaning has a cosine of %.3f with its page, want at least 0.5", c)
	}
	if c := cosine(other, question); c > 0.2 {
		t.Errorf("the question by meaning has a cosine of %.3f with another page", c)
	}
	if c := cosine(page, nothing); c > 0.2 {
		t.Errorf("a question about nothing has a cosine of %.3f with the page", c)
	}
	// Without the groups, the question's words, as recall sends them, are
	// near nothing.
	plain := vectors(t, embedder(t), "Apple tree. The apple tree by the gate is pruned in February.", "orchard trimmed")
	if c := cosine(plain[0], plain[1]); c > 0.2 {
		t.Errorf("with no meaning groups the question still has a cosine of %.3f with the page", c)
	}
}

// A fake with no size makes no vectors, and bad requests and groups are
// refused.
func TestEmbedRefuses(t *testing.T) {
	p, err := New(nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var se *seam.Error
	if _, err := p.Embed(context.Background(), seam.EmbedRequest{Model: "m", Texts: []string{"a"}}); !errors.As(err, &se) || se.Kind != seam.ErrInvalid {
		t.Errorf("a fake with no embedding size answered %v", err)
	}
	if _, err := embedder(t).Embed(context.Background(), seam.EmbedRequest{Model: "m", Texts: []string{" "}}); !errors.As(err, &se) || se.Kind != seam.ErrInvalid {
		t.Errorf("a blank text answered %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := embedder(t).Embed(ctx, seam.EmbedRequest{Model: "m", Texts: []string{"a"}}); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled call answered %v", err)
	}
	for name, opts := range map[string]Options{
		"a negative size":       {EmbeddingSize: -1},
		"a group of one":        {EmbeddingSize: 8, Meanings: [][]string{{"apple"}}},
		"a word in two groups":  {EmbeddingSize: 8, Meanings: [][]string{{"apple", "orchard"}, {"orchard", "grove"}}},
		"a word with a space":   {EmbeddingSize: 8, Meanings: [][]string{{"apple tree", "orchard"}}},
		"a size past the limit": {EmbeddingSize: embeddingSizeMax + 1},
	} {
		if _, err := New(nil, opts); err == nil {
			t.Errorf("options with %s are accepted", name)
		}
	}
	// A text of no words still points somewhere.
	if v := vectors(t, embedder(t), "?!"); len(v[0]) != 1024 {
		t.Errorf("a text of no words gave %d numbers", len(v[0]))
	}
}
