package chat

import (
	"errors"
	"slices"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

func TestMessagesArePinnedAndUnpinned(t *testing.T) {
	a, b := twoStores(t)
	c := newChat(t, a, "Planning the harbour walk")
	q := say(t, a, c.ID, 0, seam.RoleUser, "Which path is shorter, the harbour road or the cliff path?")
	r := say(t, a, c.ID, q.ID, seam.RoleAssistant, "The harbour road, by about a mile.")
	other := say(t, a, c.ID, q.ID, seam.RoleAssistant, "The cliff path, if the tide is out.") // another branch

	pins := func() []int64 {
		t.Helper()
		got, err := Pins(a, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := pins(); len(got) != 0 {
		t.Fatalf("a new chat has pins: %v", got)
	}
	for _, id := range []int64{r.ID, q.ID, other.ID, q.ID} {
		if err := Pin(a, c.ID, id, true); err != nil {
			t.Fatal(err)
		}
	}
	if got := pins(); !slices.Equal(got, []int64{q.ID, r.ID, other.ID}) {
		t.Fatalf("the pins are %v, want the three messages oldest first", got)
	}
	if err := Pin(a, c.ID, r.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := Pin(a, c.ID, r.ID, false); err != nil {
		t.Fatalf("taking off a pin that is not there: %v", err)
	}
	if got := pins(); !slices.Equal(got, []int64{q.ID, other.ID}) {
		t.Fatalf("after one pin came off the pins are %v", got)
	}

	// Another chat's message, a message that does not exist and another
	// tenant's store are each not found.
	second := newChat(t, a, "")
	if err := Pin(a, second.ID, q.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a message of another chat was pinned: %v", err)
	}
	if err := Pin(a, c.ID, 99999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a message that does not exist was pinned: %v", err)
	}
	if err := Pin(b, c.ID, q.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another tenant pinned a message: %v", err)
	}
	if _, err := Pins(b, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another tenant read the pins: %v", err)
	}

	// The pins go with their chat.
	if err := Delete(a, c.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, a, "SELECT count(*) FROM chat_pins"); n != 0 {
		t.Fatalf("%d pins are left after their chat was deleted", n)
	}
}
