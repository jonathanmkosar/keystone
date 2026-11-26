package recall

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
)

// Recall by meaning (P6-08). With a query vector, made from the query by
// the embedding model the store's page vectors came from, the store is
// searched twice: by words, as before, and by the vector alone. The two
// lists are merged by weighted reciprocal rank (decision 0411): a hit that
// passes a list's threshold scores weight / (k + its place among the hits
// of that list that pass), summed over the lists, and the merged list is
// read best first. A store's scores are on its own scale, and a cosine is
// on another, so places are merged and never scores. Each list keeps its
// own threshold, on its own scale.
//
// The vector is made by the caller, which reads no setting into this
// package: the vector, its model and the merge's numbers come in as
// Options.Meaning. Fixed rule 3 is untouched: a hit found by meaning
// carries the label the store gave it, and Found.For filters it as any.

// The settings a caller reads to fill Meaning, in settings/knowledge.json.
const (
	SettingMeaning            = "knowledge.recall_meaning"
	SettingMeaningResults     = "knowledge.recall_meaning_max_results"
	SettingMergeWordsWeight   = "knowledge.recall_merge_words_weight"
	SettingMergeMeaningWeight = "knowledge.recall_merge_meaning_weight"
	SettingMergeRankConstant  = "knowledge.recall_merge_rank_constant"

	settingMeaningMinScorePrefix = "knowledge.recall_meaning_min_score_"
)

// SettingMeaningMinScore returns the key of the threshold for hits found by
// meaning in one adapter's store: knowledge.recall_meaning_min_score_ and
// the adapter's name, with each hyphen as an underscore. A store gives a
// search by vector its own scale, as it gives a search by words.
func SettingMeaningMinScore(adapter string) string {
	return settingMeaningMinScorePrefix + strings.ReplaceAll(adapter, "-", "_")
}

// Which search found a hit.
const (
	FoundByWords   = "words"
	FoundByMeaning = "meaning"
	FoundByBoth    = "both"
)

// Meaning is a search by meaning beside the search by words.
type Meaning struct {
	// Vector is the query as a vector. With none, recall is by words alone.
	Vector []float32
	// Model is the name the store's page vectors carry, the model the
	// query's vector came from too.
	Model string
	// MaxResults is the most hits the search by meaning asks for: how many
	// hits by meaning may join recall.
	MaxResults int
	// MinScore is the threshold for a hit found by meaning, on the scale of
	// the store's search by vector.
	MinScore float64
	// WordsWeight and MeaningWeight say how much a place in each list
	// counts in the merged list. Either may be zero, not both.
	WordsWeight, MeaningWeight float64
	// RankConstant is k in weight / (k + place): the larger it is, the less
	// the first places of a list count above the later ones.
	RankConstant float64
	// Note says why there is no vector when there could have been one,
	// such as a chat that holds sensitive content and an embedding model
	// that may not read it. Find keeps it in the record for the turn
	// inspector. With a vector it is not read.
	Note string
}

// checkMeaning refuses a search by meaning that cannot be merged. With no
// vector there is nothing to check.
func checkMeaning(m Meaning) error {
	if len(m.Vector) == 0 {
		return nil
	}
	finite := func(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
	switch {
	case strings.TrimSpace(m.Model) == "":
		return fmt.Errorf("%w: a query vector needs the name of its model", ErrOptions)
	case m.MaxResults < 1 || m.MaxResults > 100:
		return fmt.Errorf("%w: %s must be from 1 to 100", ErrOptions, SettingMeaningResults)
	case !finite(m.MinScore) || m.MinScore < 0:
		return fmt.Errorf("%w: the threshold for hits found by meaning cannot be negative", ErrOptions)
	case !finite(m.WordsWeight) || m.WordsWeight < 0 || m.WordsWeight > 100:
		return fmt.Errorf("%w: %s must be from 0 to 100", ErrOptions, SettingMergeWordsWeight)
	case !finite(m.MeaningWeight) || m.MeaningWeight < 0 || m.MeaningWeight > 100:
		return fmt.Errorf("%w: %s must be from 0 to 100", ErrOptions, SettingMergeMeaningWeight)
	case m.WordsWeight == 0 && m.MeaningWeight == 0:
		return fmt.Errorf("%w: %s and %s cannot both be 0", ErrOptions, SettingMergeWordsWeight, SettingMergeMeaningWeight)
	case !finite(m.RankConstant) || m.RankConstant < 1 || m.RankConstant > 1000:
		return fmt.Errorf("%w: %s must be from 1 to 1000", ErrOptions, SettingMergeRankConstant)
	}
	for _, x := range m.Vector {
		if !finite(float64(x)) {
			return fmt.Errorf("%w: the query vector holds a number that is not finite", ErrOptions)
		}
	}
	return nil
}

// meaningRecord is what the search by meaning was, for the record.
type meaningRecord struct {
	model    string
	found    int
	minScore float64
	note     string
}

func (m meaningRecord) into(r *Record) {
	r.MeaningModel, r.MeaningFound, r.MeaningMinScore, r.MeaningNote = m.model, m.found, m.minScore, m.note
}

// NoteMeaningFailed is the record's note when the search by meaning failed
// and recall went on by words alone.
const NoteMeaningFailed = "The search by meaning failed, so the store was searched by words alone. The reason is in the server's log."

// ranked is one hit in the order it is read, and whether it passes.
type ranked struct {
	hit          knowledge.Hit
	passes       bool
	foundBy      string
	meaningScore float64
	merged       float64
	wordsAt      int // its place among the words' hits that pass, from 1; 0 when it has none
	meaningAt    int // the same among the meaning's
}

// rank orders the hits for reading. With no vector, it is the words' list
// as the store gave it, each hit passing by its own score. With one, the
// store is searched by meaning and the lists are merged. A search by
// meaning that fails leaves the words' list, with a note; only the
// context's error stops recall.
func (f *Found) rank(ctx context.Context, ks knowledge.Store, words []knowledge.Hit, o Options) ([]ranked, error) {
	if len(o.Meaning.Vector) == 0 {
		if o.Meaning.Note != "" {
			f.meaning = meaningRecord{note: o.Meaning.Note}
		}
		out := make([]ranked, len(words))
		for i, h := range words {
			out[i] = ranked{hit: h, passes: h.Score >= o.MinScore}
		}
		return out, nil
	}
	m := o.Meaning
	near, err := ks.Search(ctx, knowledge.Query{Vector: m.Vector, Model: m.Model, Limit: m.MaxResults})
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		f.meaning = meaningRecord{model: m.Model, minScore: m.MinScore, note: NoteMeaningFailed}
		o.Meaning = Meaning{}
		return f.rank(ctx, ks, words, o)
	}
	f.meaning = meaningRecord{model: m.Model, found: len(near), minScore: m.MinScore}
	return merge(words, near, o), nil
}

// merge joins the words' list and the meaning's by weighted reciprocal
// rank. A hit in both is one hit, with the label held to sensitive when
// either list said so. The hits that pass come first, best merged score
// first; a tie goes to the better place among the words, then among the
// meaning's, then to the page before the source and the lower id. The hits
// that pass neither threshold follow, in the order they were found.
func merge(words, near []knowledge.Hit, o Options) []ranked {
	m := o.Meaning
	at := map[string]int{}
	var out []ranked
	key := func(h knowledge.Hit) string { return h.Type + "\x00" + h.ID }
	place := 0
	for _, h := range words {
		r := ranked{hit: h, foundBy: FoundByWords}
		if h.Score >= o.MinScore {
			place++
			r.passes, r.wordsAt = true, place
			r.merged += m.WordsWeight / (m.RankConstant + float64(place))
		}
		at[key(h)] = len(out)
		out = append(out, r)
	}
	place = 0
	for _, h := range near {
		passes := h.Score >= m.MinScore
		score := 0.0
		if passes {
			place++
			score = m.MeaningWeight / (m.RankConstant + float64(place))
		}
		i, ok := at[key(h)]
		if !ok {
			r := ranked{hit: h, foundBy: FoundByMeaning, meaningScore: h.Score}
			if passes {
				r.passes, r.meaningAt, r.merged = true, place, score
			}
			at[key(h)] = len(out)
			out = append(out, r)
			continue
		}
		r := &out[i]
		r.foundBy, r.meaningScore = FoundByBoth, h.Score
		if h.Label.Sensitive() {
			r.hit.Label = knowledge.LabelSensitive
		}
		if passes {
			r.passes, r.meaningAt = true, place
			r.merged += score
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.passes != b.passes {
			return a.passes
		}
		if !a.passes {
			return false // stable: the order they were found
		}
		if a.merged != b.merged {
			return a.merged > b.merged
		}
		if x, y := placeOrLast(a.wordsAt), placeOrLast(b.wordsAt); x != y {
			return x < y
		}
		if x, y := placeOrLast(a.meaningAt), placeOrLast(b.meaningAt); x != y {
			return x < y
		}
		if a.hit.Type != b.hit.Type {
			return a.hit.Type == knowledge.HitPage
		}
		return a.hit.ID < b.hit.ID
	})
	return out
}

func placeOrLast(n int) int {
	if n == 0 {
		return math.MaxInt
	}
	return n
}
