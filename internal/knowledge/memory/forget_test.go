package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every person, place and fact in these tests is invented.

// landed makes a turn whose memory pass added these facts to the space, as
// the turn's worker would, and returns the pass.
func landed(t *testing.T, s *tenant.Store, ks knowledge.Store, space string, sensitive bool, facts ...string) Pass {
	t.Helper()
	ctx := context.Background()
	turnID := newTurn(t, s, "")
	chatID := chatOf(t, s, turnID)
	if _, err := Queue(ctx, s, turnID, chatID); err != nil {
		t.Fatal(err)
	}
	p, ok, err := Claim(ctx, s, chatID)
	if err != nil || !ok {
		t.Fatalf("claiming the pass: %v, %v", ok, err)
	}
	x := exchange()
	x.ChatID, x.TurnID = chatID, turnID
	kf := make([]knowledge.Fact, len(facts))
	for i, f := range facts {
		kf[i] = knowledge.Fact{Text: f}
	}
	src, err := Add(ctx, ks, options(), x, kf, space, sensitive, true)
	if err != nil {
		t.Fatal(err)
	}
	p.Status, p.Space, p.SourceID, p.Sensitive, p.Facts = StatusDone, src.Space, src.ID, src.Label.Sensitive(), facts
	if err := Finish(ctx, s, p); err != nil {
		t.Fatal(err)
	}
	return p
}

// found reports whether a search for the words finds a source.
func found(t *testing.T, ks knowledge.Store, words, sourceID string) bool {
	t.Helper()
	hits, err := ks.Search(context.Background(), knowledge.Query{Text: words})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Type == knowledge.HitSource && h.ID == sourceID {
			return true
		}
	}
	return false
}

// The item's proof, in the package: a forgotten fact is gone from the store
// and from what a search finds, and the source's other facts stay, in a
// new source the ledger points at.
func TestAForgottenFactIsGoneFromTheStoreAndFromSearch(t *testing.T) {
	s := store(t)
	ks := seed(t, s)
	ctx := context.Background()
	p := landed(t, s, ks, "memory", false, "The person keeps two beehives.", "The person bakes rye bread on Saturdays.")

	ch, err := Apply(ctx, s, ks, builtin.Name, Edit{SourceID: p.SourceID, Fact: "The person keeps two beehives."}, 300)
	if err != nil {
		t.Fatal(err)
	}
	if !ch.Removed || ch.NewSourceID == "" || len(ch.Facts) != 1 || ch.Facts[0] != "The person bakes rye bread on Saturdays." || ch.Sensitive {
		t.Errorf("the change is %+v", ch)
	}
	if _, err := ks.ReadSource(ctx, p.SourceID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("the old source reads with error %v, want ErrNotFound", err)
	}
	if _, err := knowledge.Unwrap(ks).ReadSource(ctx, p.SourceID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("the built-in store still holds the old source: %v", err)
	}
	if found(t, ks, "beehives", p.SourceID) || found(t, ks, "beehives", ch.NewSourceID) {
		t.Error("a search for the forgotten fact still finds it")
	}
	if !found(t, ks, "rye", ch.NewSourceID) {
		t.Error("a search for the kept fact does not find the new source")
	}
	kept, err := ks.ReadSource(ctx, ch.NewSourceID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(kept.Text, "beehives") || !strings.Contains(kept.Text, "rye bread") || kept.Space != "memory" || kept.Origin != Origin(p.ChatID, p.TurnID) {
		t.Errorf("the new source is %+v", kept)
	}
	rows, total, err := Remembered(ctx, s, 0)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].SourceID != ch.NewSourceID || strings.Join(rows[0].Facts, "|") != "The person bakes rye bread on Saturdays." {
		t.Errorf("Remembered = %+v, %d, %v", rows, total, err)
	}

	// The last fact forgotten leaves no source, and the pass is no longer
	// remembered, though its row stays and says why, with no word of it.
	ch, err = Apply(ctx, s, ks, builtin.Name, Edit{SourceID: ch.NewSourceID, Fact: "The person bakes rye bread on Saturdays."}, 300)
	if err != nil {
		t.Fatal(err)
	}
	if ch.NewSourceID != "" || len(ch.Facts) != 0 {
		t.Errorf("forgetting the last fact gave %+v", ch)
	}
	if found(t, ks, "rye", ch.SourceID) {
		t.Error("the last fact is still found")
	}
	if rows, total, err := Remembered(ctx, s, 0); err != nil || total != 0 || len(rows) != 0 {
		t.Errorf("Remembered after everything was forgotten = %+v, %d, %v", rows, total, err)
	}
	row, ok, err := Get(ctx, s, p.TurnID)
	if err != nil || !ok || row.SourceID != "" || len(row.Facts) != 0 || row.Reason != ReasonForgotten || row.Status != StatusDone {
		t.Errorf("the pass's row reads %+v, %v, %v", row, ok, err)
	}
}

func TestACorrectedFactReplacesTheOldOne(t *testing.T) {
	s := store(t)
	ks := seed(t, s)
	ctx := context.Background()
	p := landed(t, s, ks, "memory", false, "The person keeps two beehives.", "The person bakes rye bread on Saturdays.")

	ch, err := Apply(ctx, s, ks, builtin.Name, Edit{SourceID: p.SourceID, Fact: "The person keeps two beehives.", Text: "  The person keeps   three beehives. "}, 300)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ch.Facts, "|") != "The person keeps three beehives.|The person bakes rye bread on Saturdays." {
		t.Errorf("the corrected facts are %q", ch.Facts)
	}
	if found(t, ks, "two", ch.NewSourceID) || found(t, ks, "two", p.SourceID) {
		t.Error("the fact as it was is still found")
	}
	if !found(t, ks, "three beehives", ch.NewSourceID) {
		t.Error("the corrected fact is not found")
	}
	row, _, _ := Get(ctx, s, p.TurnID)
	if row.SourceID != ch.NewSourceID || row.Reason != ReasonCorrected {
		t.Errorf("the pass's row reads %+v", row)
	}

	// What is refused.
	for name, e := range map[string]Edit{
		"a source the memory pass did not add": {SourceID: "s999", Fact: "x"},
		"the old source":                       {SourceID: p.SourceID, Fact: "The person keeps three beehives."},
	} {
		if _, err := Apply(ctx, s, ks, builtin.Name, e, 300); !errors.Is(err, ErrNotAdded) {
			t.Errorf("%s: error %v, want ErrNotAdded", name, err)
		}
	}
	if _, err := Apply(ctx, s, ks, builtin.Name, Edit{SourceID: ch.NewSourceID, Fact: "The person keeps two beehives."}, 300); !errors.Is(err, ErrNoFact) {
		t.Errorf("a fact the source does not hold: error %v, want ErrNoFact", err)
	}
	for name, text := range map[string]string{"too long": strings.Repeat("word ", 20), "blank": "   "} {
		e := Edit{SourceID: ch.NewSourceID, Fact: "The person keeps three beehives.", Text: text}
		if name == "blank" {
			// A blank correction is a forget only when Text is empty; spaces
			// alone are a correction with no words.
			e.Text = " \t "
		}
		if _, err := Apply(ctx, s, ks, builtin.Name, e, 60); !errors.Is(err, knowledge.ErrInvalid) {
			t.Errorf("a correction that is %s: error %v, want ErrInvalid", name, err)
		}
	}
	if _, err := ks.ReadSource(ctx, ch.NewSourceID); err != nil {
		t.Errorf("a refused correction lost the source: %v", err)
	}
}

// Fixed rule 1: a person changes only what the memory pass added in their
// own tenant. Another tenant's ledger is not read.
func TestAnotherTenantsSourceIsNotTheirs(t *testing.T) {
	a, b := store(t), store(t)
	ksA, ksB := seed(t, a), seed(t, b)
	p := landed(t, a, ksA, "memory", false, "The person keeps two beehives.")
	if _, err := Apply(context.Background(), b, ksB, builtin.Name, Edit{SourceID: p.SourceID, Fact: "The person keeps two beehives."}, 300); !errors.Is(err, ErrNotAdded) {
		t.Errorf("another tenant forgot a source of this one: error %v, want ErrNotAdded", err)
	}
	if _, err := ksA.ReadSource(context.Background(), p.SourceID); err != nil {
		t.Errorf("the source is gone: %v", err)
	}
}

// Fixed rule 3: the facts of a sensitive source stay in a sensitive space,
// and a store that would put them in an ordinary one has the new source
// taken back and the change refused.
func TestASensitiveSourceStaysSensitive(t *testing.T) {
	s := store(t)
	ks := seed(t, s)
	ctx := context.Background()
	p := landed(t, s, ks, "health", true, "The person sleeps badly in March.", "The person takes magnesium at night.")
	ch, err := Apply(ctx, s, ks, builtin.Name, Edit{SourceID: p.SourceID, Fact: "The person sleeps badly in March."}, 300)
	if err != nil {
		t.Fatal(err)
	}
	if !ch.Sensitive || ch.Space != "health" {
		t.Errorf("the change is %+v", ch)
	}
	if src, err := ks.ReadSource(ctx, ch.NewSourceID); err != nil || !src.Label.Sensitive() {
		t.Errorf("the new source reads %+v, %v", src, err)
	}

	relabels := &ordinary{Store: ks}
	before, _, _ := Remembered(ctx, s, 0)
	_, err = Apply(ctx, s, relabels, builtin.Name, Edit{SourceID: ch.NewSourceID, Fact: "The person takes magnesium at night.", Text: "The person takes magnesium at bedtime."}, 300)
	if !errors.Is(err, ErrLanded) {
		t.Fatalf("a store that lands sensitive facts in an ordinary space: error %v, want ErrLanded", err)
	}
	if found(t, ks, "bedtime", relabels.added) {
		t.Error("the source that landed in an ordinary space was not taken back")
	}
	after, _, _ := Remembered(ctx, s, 0)
	if len(after) != 1 || after[0].SourceID != before[0].SourceID {
		t.Errorf("a refused change moved the ledger: %+v", after)
	}
}

// ordinary is a store that says every source it adds is in an ordinary
// space.
type ordinary struct {
	knowledge.Store
	added string
}

func (o *ordinary) AddSource(ctx context.Context, n knowledge.NewSource) (knowledge.Source, error) {
	src, err := o.Store.AddSource(ctx, n)
	o.added = src.ID
	src.Label = knowledge.LabelNormal
	return src, err
}

// A store that cannot remove a source, as Waymarks cannot: the source is
// withdrawn, so nothing Keystone opens reads or finds it, though the store
// keeps it.
func TestAStoreThatCannotRemoveHasTheSourceWithdrawn(t *testing.T) {
	s := store(t)
	seed(t, s)
	ctx := context.Background()
	opened, err := knowledge.Open(ctx, builtin.Name, s)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	ks := keeps{opened}
	p := landed(t, s, ks, "memory", false, "The person keeps two beehives.", "The person bakes rye bread on Saturdays.")

	ch, err := Apply(ctx, s, ks, builtin.Name, Edit{SourceID: p.SourceID, Fact: "The person keeps two beehives."}, 300)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Removed {
		t.Errorf("a store with no RemoveSource says it removed the source: %+v", ch)
	}
	if _, err := ks.ReadSource(ctx, p.SourceID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("the withdrawn source reads with error %v", err)
	}
	if found(t, ks, "beehives", p.SourceID) {
		t.Error("the withdrawn source is still found")
	}
	if _, err := knowledge.Unwrap(opened).ReadSource(ctx, p.SourceID); err != nil {
		t.Errorf("the store itself should keep the source: %v", err)
	}
	if !found(t, ks, "rye", ch.NewSourceID) {
		t.Error("the kept fact is not found")
	}

	// A store that can take no source cannot keep the other facts, so the
	// change is refused and nothing moves.
	if _, err := Apply(ctx, s, noAdd{ks}, builtin.Name, Edit{SourceID: ch.NewSourceID, Fact: "The person bakes rye bread on Saturdays.", Text: "The person bakes on Sundays."}, 300); !errors.Is(err, ErrCannotChange) {
		t.Errorf("a correction in a store that takes no source: error %v, want ErrCannotChange", err)
	}
}

// keeps is a store with no RemoveSource.
type keeps struct{ knowledge.Store }

func (k keeps) Capabilities() knowledge.Capabilities {
	c := k.Store.Capabilities()
	c.RemoveSource = false
	return c
}

// noAdd is a store with neither RemoveSource nor AddSource.
type noAdd struct{ knowledge.Store }

func (n noAdd) Capabilities() knowledge.Capabilities {
	c := n.Store.Capabilities()
	c.AddSource = false
	return c
}

func TestAProfileLineThatSaysTheFactFollowsIt(t *testing.T) {
	profile := "I live by the sea.\n- The person keeps two beehives\n* Likes rye bread."
	got, changed := ProfileEdited(profile, Edit{Fact: "The person keeps two beehives."})
	if !changed || got != "I live by the sea.\n* Likes rye bread." {
		t.Errorf("forgetting gave %q, %v", got, changed)
	}
	got, changed = ProfileEdited(profile, Edit{Fact: "the person keeps TWO beehives.", Text: "The person keeps three beehives."})
	if !changed || got != "I live by the sea.\n- The person keeps three beehives.\n* Likes rye bread." {
		t.Errorf("correcting gave %q, %v", got, changed)
	}
	if got, changed := ProfileEdited(profile, Edit{Fact: "The person keeps bees."}); changed || got != profile {
		t.Errorf("a fact the profile does not say changed it: %q", got)
	}
}
