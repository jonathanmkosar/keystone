package fake

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"unicode"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The fake as an embedding model (P6-08). Its vectors are deterministic: the
// same text always gives the same vector, and needs no network and no key.
//
// A text's words are lower-cased, and each one that differs from another
// only by a plural or a verb ending (s, es, ies, ed, ing) is taken as the
// same word. A word of a meaning group stands for the group, so "orchard"
// and "apple" are one meaning when a group holds both. Each distinct word
// or meaning is hashed to one of the vector's places, with a sign, and adds
// to it once; a meaning weighs four times a word in no group, which keeps a
// text's few known meanings ahead of its many other words. The vector is
// then made unit length, so the dot product of two is their cosine.
//
// So two texts are near when they share meanings, and a question can find
// a page that holds none of its words. The groups are test data, given in
// Options.Meanings.

// SettingEmbeddingSize is the setting the size of the fake's vectors is
// read from by DefaultOptions.
const SettingEmbeddingSize = "fake.embedding_size"

// otherWordWeight is how much a word in no meaning group adds, against 1
// for a meaning. It is part of what the fake is, as its scripts are, and
// not a value a person tunes.
const otherWordWeight = 0.25

// embeddingSizeMax is the most numbers a fake vector may hold.
const embeddingSizeMax = 1 << 16

// meanings checks the options' embedding size and meaning groups, and maps
// each word of a group to the group.
func meanings(opts Options) (map[string]int, error) {
	if opts.EmbeddingSize < 0 || opts.EmbeddingSize > embeddingSizeMax {
		return nil, fmt.Errorf("the embedding size must be from 0 to %d", embeddingSizeMax)
	}
	out := map[string]int{}
	for g, group := range opts.Meanings {
		if len(group) < 2 {
			return nil, fmt.Errorf("meaning group %d has fewer than two words", g+1)
		}
		for _, w := range group {
			word := strings.ToLower(strings.TrimSpace(w))
			if word == "" || strings.ContainsFunc(word, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
				return nil, fmt.Errorf("meaning group %d holds %q, and a word is letters and digits only", g+1, w)
			}
			if other, ok := out[word]; ok && other != g {
				return nil, fmt.Errorf("the word %q is in meaning groups %d and %d", word, other+1, g+1)
			}
			out[word] = g
		}
	}
	return out, nil
}

// Embed turns each text into a vector, as seam.Embedder describes. A fake
// with no embedding size refuses, as a model that makes no vectors would.
func (p *Provider) Embed(ctx context.Context, req seam.EmbedRequest) (seam.EmbedResponse, error) {
	if err := ctx.Err(); err != nil {
		return seam.EmbedResponse{}, err
	}
	if err := seam.ValidateEmbedRequest(req); err != nil {
		return seam.EmbedResponse{}, &seam.Error{Kind: seam.ErrInvalid, Message: "the request is not valid: " + err.Error()}
	}
	if p.opts.EmbeddingSize == 0 {
		return seam.EmbedResponse{}, &seam.Error{Kind: seam.ErrInvalid, Message: "this fake makes no vectors: its embedding size is not set"}
	}
	resp := seam.EmbedResponse{Model: req.Model}
	for _, text := range req.Texts {
		vec, words := p.vector(text)
		resp.Vectors = append(resp.Vectors, vec)
		resp.Usage.InputTokens += words
	}
	if err := seam.ValidateEmbedResponse(req, resp); err != nil {
		return seam.EmbedResponse{}, errors.New("the fake made an answer the seam refuses: " + err.Error())
	}
	return resp, nil
}

// vector is one text's vector, and how many words it read.
func (p *Provider) vector(text string) ([]float32, int) {
	size := p.opts.EmbeddingSize
	sum := make([]float64, size)
	seen := map[string]bool{}
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, w := range words {
		if len([]rune(w)) < 2 {
			continue
		}
		key, weight := p.key(w)
		if seen[key] {
			continue
		}
		seen[key] = true
		at, sign := place(key, size)
		sum[at] += sign * weight
	}
	if len(seen) == 0 {
		// A text with no word still points somewhere, the same place for
		// every such text, so it is near nothing that has words.
		at, sign := place("", size)
		sum[at] = sign
	}
	var norm float64
	for _, x := range sum {
		norm += x * x
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		// Every word cancelled another out at one place. Point at the place
		// of the empty text, as above.
		at, sign := place("", size)
		sum[at], norm = sign, 1
	}
	out := make([]float32, size)
	for i, x := range sum {
		out[i] = float32(x / norm)
	}
	return out, len(words)
}

// key is what a word stands for in a vector, and how much it weighs: its
// meaning group when it, or it without an ending, is in one, and otherwise
// the shortest of its forms.
func (p *Provider) key(word string) (string, float64) {
	forms := append([]string{word}, stems(word)...)
	short := word
	for _, f := range forms {
		if g, ok := p.meaning[f]; ok {
			return fmt.Sprintf("meaning %d", g), 1
		}
		if len(f) < len(short) {
			short = f
		}
	}
	return "word " + short, otherWordWeight
}

// stems returns a word without each ending it might have, longest ending
// first. A short word keeps its ending: "is" and "as" are words.
func stems(word string) []string {
	var out []string
	cut := func(suffix, with string) {
		if strings.HasSuffix(word, suffix) && len(word)-len(suffix) >= 3 {
			out = append(out, strings.TrimSuffix(word, suffix)+with)
		}
	}
	cut("ies", "y")
	cut("ing", "")
	cut("ed", "")
	cut("es", "")
	if !strings.HasSuffix(word, "ss") {
		cut("s", "")
	}
	return out
}

// place is where a key adds to a vector, and with which sign.
func place(key string, size int) (int, float64) {
	h := fnv.New64a()
	h.Write([]byte(key))
	sum := h.Sum64()
	sign := 1.0
	if sum>>63 == 1 {
		sign = -1
	}
	return int(sum % uint64(size)), sign
}
