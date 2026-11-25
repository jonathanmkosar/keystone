package chat

import (
	"errors"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// MatchAny finds titles and messages holding any of the words, ranks a row
// with more of them first, and reads one tenant's chats only.
func TestMatchAnyFindsAnyWord(t *testing.T) {
	a, b := twoStores(t)
	walk := newChat(t, a, "Planning the lighthouse walk")
	m1 := say(t, a, walk.ID, 0, seam.RoleUser, "What should I bring to the old lighthouse?")
	say(t, a, walk.ID, m1.ID, seam.RoleAssistant, "A windproof jacket, water and a paper map.")
	ferry := newChat(t, a, "North ferry times")
	say(t, a, ferry.ID, 0, seam.RoleUser, "When does the north ferry leave? I need a map of the quay too.")
	theirs := newChat(t, b, "A lighthouse of my own")
	say(t, b, theirs.ID, 0, seam.RoleUser, "Is the lighthouse open on Sunday?")

	titles, messages, err := MatchAny(a, "lighthouse ferry", 10, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(titles) != 2 || len(messages) != 2 {
		t.Fatalf("found %d titles and %d messages, want 2 of each: %+v %+v", len(titles), len(messages), titles, messages)
	}
	for _, m := range append(titles, messages...) {
		if m.ChatID == theirs.ID {
			t.Fatalf("another tenant's chat was found: %+v", m)
		}
		if m.Score >= 0 || m.When.IsZero() {
			t.Errorf("a match has no rank or no time: %+v", m)
		}
	}
	if titles[0].MessageID != 0 || titles[0].Role != "" {
		t.Errorf("a title match names a message: %+v", titles[0])
	}

	// A message holding more of the words ranks first, and the snippet is
	// the words around the match.
	_, messages, err = MatchAny(a, "map quay", 10, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].ChatID != ferry.ID || messages[0].Role != seam.RoleUser || messages[0].Snippet == "" {
		t.Fatalf("the message with both words is not first: %+v", messages)
	}
	// The start of a word matches, and the limit holds.
	if _, messages, _ := MatchAny(a, "windpr", 10, 8); len(messages) != 1 || messages[0].Role != seam.RoleAssistant {
		t.Errorf("the start of a word found %+v", messages)
	}
	if titles, _, _ := MatchAny(a, "lighthouse ferry", 1, 8); len(titles) != 1 {
		t.Errorf("the limit gave %d titles", len(titles))
	}
	if titles, messages, err := MatchAny(a, "?!", 10, 8); err != nil || titles != nil || messages != nil {
		t.Errorf("a query with no word found %v %v %v", titles, messages, err)
	}
	if _, _, err := MatchAny(a, "map", 0, 8); !errors.Is(err, ErrInvalid) {
		t.Errorf("no limit gave %v", err)
	}
}
