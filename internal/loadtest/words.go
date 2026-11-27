package loadtest

import (
	"math"
	"math/rand/v2"
	"strings"
)

// The load's words are invented: each is two to four syllables put together
// by a seeded generator, so a run is repeatable and holds nothing a person
// wrote. Common words are drawn far more often than rare ones, as in real
// text, which is what makes a search for a common word a different test
// from a search for a rare one.

var (
	onsets = []string{"b", "d", "f", "g", "k", "l", "m", "n", "p", "r", "s", "t", "v", "z", "br", "dr", "fl", "gr", "kl", "pr", "st", "tr", "sh", "th"}
	vowels = []string{"a", "e", "i", "o", "u", "ai", "ei", "ou"}
	codas  = []string{"", "", "", "n", "r", "l", "s", "m", "k", "th"}
)

// vocabulary is the invented words, the most common first.
type vocabulary struct {
	words []string
	// cumulative is the running share of each word, for drawing one by a
	// number from 0 to 1: a word's share falls with its place, as 1/place.
	cumulative []float64
}

// newVocabulary makes size different invented words from a seed.
func newVocabulary(seed uint64, size int) *vocabulary {
	r := rand.New(rand.NewPCG(seed, 0x6c6f6164)) // the second word is fixed: "load"
	seen := map[string]bool{}
	v := &vocabulary{}
	for len(v.words) < size {
		var b strings.Builder
		// The commonest words are short, as they are in any language.
		syllables := 2 + r.IntN(2)
		if len(v.words) > size/4 {
			syllables = 2 + r.IntN(3)
		}
		for i := 0; i < syllables; i++ {
			b.WriteString(onsets[r.IntN(len(onsets))])
			b.WriteString(vowels[r.IntN(len(vowels))])
		}
		b.WriteString(codas[r.IntN(len(codas))])
		w := b.String()
		if seen[w] {
			continue
		}
		seen[w] = true
		v.words = append(v.words, w)
	}
	total := 0.0
	for i := range v.words {
		total += 1 / math.Pow(float64(i+1), 1.07)
	}
	sum := 0.0
	for i := range v.words {
		sum += 1 / math.Pow(float64(i+1), 1.07) / total
		v.cumulative = append(v.cumulative, sum)
	}
	return v
}

// draw returns one word, a common one more often than a rare one.
func (v *vocabulary) draw(r *rand.Rand) string {
	x := r.Float64()
	lo, hi := 0, len(v.cumulative)-1
	for lo < hi {
		mid := (lo + hi) / 2
		if v.cumulative[mid] < x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return v.words[lo]
}

// rare returns a word from the rare half of the vocabulary, each as likely
// as another. A chat's topic is made of these, so a search for a topic
// finds few chats.
func (v *vocabulary) rare(r *rand.Rand) string {
	half := len(v.words) / 2
	return v.words[half+r.IntN(len(v.words)-half)]
}

// text writes about n words as sentences. The first sentence has at least
// eight words and no mark inside it, so a chat's title, taken from the
// first words of its first message, is words alone. topic words are put in
// here and there.
func (v *vocabulary) text(r *rand.Rand, n int, topic []string) string {
	if n < 8 {
		n = 8
	}
	var b strings.Builder
	b.Grow(n * 8)
	left := 0
	for i := 0; i < n; i++ {
		start := left == 0
		if start {
			left = 8 + r.IntN(9)
		}
		w := v.draw(r)
		if len(topic) > 0 && r.IntN(12) == 0 {
			w = topic[r.IntN(len(topic))]
		}
		if start {
			if i > 0 {
				b.WriteString(". ")
			}
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		} else {
			b.WriteByte(' ')
			b.WriteString(w)
		}
		left--
	}
	b.WriteByte('.')
	return b.String()
}
