package recall

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/settings"
)

// Every name and every line of content in these tests is invented.

const testVectorModel = "test-embed"

// withVectors gives the world's pages vectors: runner beans, the shed roof
// and the sleep notes each point along an axis of their own.
func withVectors(t *testing.T, w world) {
	t.Helper()
	for id, vec := range map[string][]float32{w.beans: {1, 0, 0}, w.roof: {0, 1, 0}, w.sleep: {0, 0, 1}} {
		p, err := w.ks.ReadPage(bg, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.ks.PutVector(bg, knowledge.PageVector{PageID: id, Revision: p.Revision, Model: testVectorModel, Vector: vec}); err != nil {
			t.Fatal(err)
		}
	}
}

// meaningDefaults are the shipped merge settings, with a vector.
func meaningDefaults(t *testing.T, vec []float32) Meaning {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	number := func(key string) float64 {
		st, ok := settings.Lookup(reg, key)
		if !ok {
			t.Fatalf("%s is not in the settings registry", key)
		}
		n, err := strconv.ParseFloat(st.Default, 64)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return n
	}
	if st, ok := settings.Lookup(reg, SettingMeaning); !ok || st.Default != "true" {
		t.Fatalf("%s is missing or ships off", SettingMeaning)
	}
	m := Meaning{
		Vector: vec, Model: testVectorModel, MaxResults: int(number(SettingMeaningResults)),
		MinScore: number(SettingMeaningMinScore(builtin.Name)), WordsWeight: number(SettingMergeWordsWeight),
		MeaningWeight: number(SettingMergeMeaningWeight), RankConstant: number(SettingMergeRankConstant),
	}
	if err := checkMeaning(m); err != nil {
		t.Fatalf("the shipped merge settings are refused: %v", err)
	}
	return m
}

func hitOf(rec Record, id string) (HitRecord, bool) {
	for _, h := range rec.Hits {
		if h.ID == id {
			return h, true
		}
	}
	return HitRecord{}, false
}

// A question that shares no word with a page finds it by meaning, and the
// record says so; with no vector, the same question finds nothing.
func TestRecallByMeaning(t *testing.T) {
	w := newWorld(t)
	withVectors(t, w)
	o, _ := defaults(t)
	ask := "When do we cover the hut?"
	words, err := Find(bg, w.ks, ask, o)
	if err != nil {
		t.Fatal(err)
	}
	if out := words.For(false); out.Record.Kept != 0 || out.Record.MeaningModel != "" {
		t.Fatalf("by words alone the question kept %d items: %+v", out.Record.Kept, out.Record)
	}
	o.Meaning = meaningDefaults(t, []float32{0.1, 0.9, 0})
	found, err := Find(bg, w.ks, ask, o)
	if err != nil {
		t.Fatal(err)
	}
	out := found.For(false)
	h, ok := hitOf(out.Record, w.roof)
	if !ok || !h.Kept || h.FoundBy != FoundByMeaning || h.MeaningScore <= 0 || h.Merged <= 0 {
		t.Fatalf("the shed roof by meaning: %+v (in the record: %v)", h, ok)
	}
	if len(out.Items) == 0 || !strings.Contains(out.Items[0].Text, "two rolls") {
		t.Errorf("the first item is not the shed roof: %+v", out.Items)
	}
	if out.Record.MeaningModel != testVectorModel || out.Record.MeaningFound == 0 {
		t.Errorf("the record does not say the store was searched by meaning: %+v", out.Record)
	}
}

// Words and meaning together: a page both find comes first, each list adds
// what the other missed, and a hit under the meaning's threshold is left
// out for it.
func TestMergeWordsAndMeaning(t *testing.T) {
	w := newWorld(t)
	withVectors(t, w)
	o, _ := defaults(t)
	// The words find the runner beans; the vector points between the beans
	// and the roof, nearer the beans.
	o.Meaning = meaningDefaults(t, []float32{0.8, 0.6, 0})
	found, err := Find(bg, w.ks, "runner beans", o)
	if err != nil {
		t.Fatal(err)
	}
	out := found.For(false)
	beans, _ := hitOf(out.Record, w.beans)
	roof, _ := hitOf(out.Record, w.roof)
	if beans.FoundBy != FoundByBoth || !beans.Kept || roof.FoundBy != FoundByMeaning || !roof.Kept {
		t.Fatalf("beans %+v, roof %+v", beans, roof)
	}
	if out.Record.Hits[0].ID != w.beans || beans.Merged <= roof.Merged {
		t.Errorf("the page both found is not first: %+v", out.Record.Hits)
	}
	// A threshold above the roof's cosine leaves it out for the meaning.
	o.Meaning.MinScore = 0.7
	found, err = Find(bg, w.ks, "runner beans", o)
	if err != nil {
		t.Fatal(err)
	}
	out = found.For(false)
	if roof, _ := hitOf(out.Record, w.roof); roof.Kept || roof.Reason != ReasonBelowThreshold {
		t.Errorf("a hit under the meaning's threshold: %+v", roof)
	}
	if beans, _ := hitOf(out.Record, w.beans); !beans.Kept {
		t.Errorf("the page the words found was left out: %+v", beans)
	}
	// The words' weight at zero: the merged order is the meaning's alone.
	o.Meaning.MinScore, o.Meaning.WordsWeight = 0, 0
	o.Meaning.Vector = []float32{0.1, 0.9, 0}
	found, err = Find(bg, w.ks, "runner beans", o)
	if err != nil {
		t.Fatal(err)
	}
	if first := found.For(false).Record.Hits[0]; first.ID != w.roof {
		t.Errorf("with the words weighing nothing the first hit is %+v", first)
	}
}

// A sensitive page found by meaning is withheld from a model that may not
// read it, and marks the outcome sensitive for one that may.
func TestMeaningKeepsTheRule(t *testing.T) {
	w := newWorld(t)
	withVectors(t, w)
	o, _ := defaults(t)
	o.Meaning = meaningDefaults(t, []float32{0, 0.1, 0.9})
	found, err := Find(bg, w.ks, "trouble resting", o)
	if err != nil {
		t.Fatal(err)
	}
	closed := found.For(false)
	if h, _ := hitOf(closed.Record, w.sleep); h.Kept || h.Reason != ReasonSensitive || closed.Sensitive {
		t.Errorf("for a model that may not read it, the sleep notes: %+v", h)
	}
	for _, it := range closed.Items {
		if strings.Contains(it.Text, "Melatonin") {
			t.Error("a model that may not read sensitive content was given the sleep notes")
		}
	}
	open := found.For(true)
	if h, _ := hitOf(open.Record, w.sleep); !h.Kept || !open.Sensitive {
		t.Errorf("for a model that may read it, the sleep notes: %+v, sensitive %v", h, open.Sensitive)
	}
}

// failingVectors is a store whose search by vector fails.
type failingVectors struct{ knowledge.Store }

func (f failingVectors) Search(ctx context.Context, q knowledge.Query) ([]knowledge.Hit, error) {
	if len(q.Vector) > 0 {
		return nil, knowledge.ErrUnavailable
	}
	return f.Store.Search(ctx, q)
}

// A search by meaning that fails leaves recall by words, with a note; bad
// merge options are refused.
func TestMeaningFails(t *testing.T) {
	w := newWorld(t)
	o, _ := defaults(t)
	o.Meaning = meaningDefaults(t, []float32{1, 0, 0})
	found, err := Find(bg, failingVectors{w.ks}, "runner beans", o)
	if err != nil {
		t.Fatal(err)
	}
	out := found.For(false)
	if h, _ := hitOf(out.Record, w.beans); !h.Kept || h.FoundBy != "" || out.Record.MeaningNote != NoteMeaningFailed {
		t.Errorf("after a failed search by meaning: %+v, note %q", h, out.Record.MeaningNote)
	}
	good := meaningDefaults(t, []float32{1, 0})
	for name, change := range map[string]func(*Meaning){
		"no model":             func(m *Meaning) { m.Model = "" },
		"no results":           func(m *Meaning) { m.MaxResults = 0 },
		"a negative minimum":   func(m *Meaning) { m.MinScore = -1 },
		"both weights zero":    func(m *Meaning) { m.WordsWeight, m.MeaningWeight = 0, 0 },
		"a rank constant of 0": func(m *Meaning) { m.RankConstant = 0 },
	} {
		m := good
		change(&m)
		o.Meaning = m
		if err := CheckOptions(o); !errors.Is(err, ErrOptions) {
			t.Errorf("merge options with %s: %v", name, err)
		}
	}
}
