package builtin

import (
	"errors"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
)

// A withdrawn source (P6-07) is left out of every answer of a store Open
// hands back: it is not read, not found and not cited. It is still in the
// store, so the adapter's own handle reads it, and another tenant's
// withdrawals touch nothing here.
func TestAWithdrawnSourceIsLeftOutOfEveryAnswer(t *testing.T) {
	in := newInstall(t)
	ts := newTenant(t, in, "Test Person One")
	other := newTenant(t, in, "Test Person Two")
	s := openStore(t, ts)

	kept, err := AddText(bg, ts, NewText{Space: "home", Name: "kept.txt", Text: "The shed key hangs by the back door."})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := AddText(bg, ts, NewText{Space: "home", Name: "gone.txt", Text: "The shed roof leaks over the potting bench."})
	if err != nil {
		t.Fatal(err)
	}
	page, err := WritePage(bg, ts, NewPage{Space: "home", Path: "shed", Title: "Shed", Body: "The shed needs work.", Sources: []string{kept.ID, gone.ID}})
	if err != nil {
		t.Fatal(err)
	}

	// Another tenant's withdrawal of the same id changes nothing here.
	if err := knowledge.Withdraw(bg, other, Name, gone.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadSource(bg, gone.ID); err != nil {
		t.Fatalf("another tenant's withdrawal hid a source of this one: %v", err)
	}

	if err := knowledge.Withdraw(bg, ts, Name, gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := knowledge.Withdraw(bg, ts, Name, gone.ID); err != nil {
		t.Fatalf("withdrawing a source twice: %v", err)
	}
	if _, err := s.ReadSource(bg, gone.ID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("a withdrawn source reads with error %v, want ErrNotFound", err)
	}
	hits, err := s.Search(bg, knowledge.Query{Text: "shed"})
	if err != nil {
		t.Fatal(err)
	}
	var sawKept, sawPage bool
	for _, h := range hits {
		switch {
		case h.Type == knowledge.HitSource && h.ID == gone.ID:
			t.Errorf("a search finds the withdrawn source: %+v", h)
		case h.Type == knowledge.HitSource && h.ID == kept.ID:
			sawKept = true
		case h.Type == knowledge.HitPage && h.ID == page.ID:
			sawPage = true
		}
	}
	if !sawKept || !sawPage {
		t.Errorf("a search for shed lost what was not withdrawn: %+v", hits)
	}
	read, err := s.ReadPage(bg, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Sources) != 1 || read.Sources[0] != kept.ID {
		t.Errorf("the page cites %v, want only %s", read.Sources, kept.ID)
	}

	// The adapter's own handle still holds it: a withdrawal is Keystone's,
	// and the store is not changed.
	if _, err := knowledge.Unwrap(s).ReadSource(bg, gone.ID); err != nil {
		t.Errorf("the store itself lost the withdrawn source: %v", err)
	}
	if got, err := knowledge.Withdrawn(bg, ts, Name); err != nil || len(got) != 1 || !got[gone.ID] {
		t.Errorf("Withdrawn = %v, %v", got, err)
	}
	if got, err := knowledge.Withdrawn(bg, ts, "waymarks"); err != nil || len(got) != 0 {
		t.Errorf("a withdrawal in one adapter shows in another: %v, %v", got, err)
	}
	if err := knowledge.Withdraw(bg, ts, Name, " "); !errors.Is(err, knowledge.ErrInvalid) {
		t.Errorf("withdrawing a blank id: error %v, want ErrInvalid", err)
	}
}
