package chat

import (
	"fmt"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// A search over a long record ranks the newest matches only, finds a rare
// word however old its message is, and gives the same answer as before on
// a record under the bound (P13-03, decision 0530). Every word is invented.
func TestASearchRanksAtMostSoManyMatches(t *testing.T) {
	a, _ := twoStores(t)
	lim := limits(t)
	old := newChat(t, a, "Notes from the spring")
	// One old message holds a rare word; forty messages after it all hold
	// a common one.
	rare := say(t, a, old.ID, 0, seam.RoleUser, "The quillfeather ledger is kept by the harbour.")
	parent := rare.ID
	var all []int64
	for i := 0; i < 40; i++ {
		m := say(t, a, old.ID, parent, seam.RoleAssistant, fmt.Sprintf("The harbour was calm on day %d of the count.", i+1))
		parent = m.ID
		all = append(all, m.ID)
	}
	newest := map[int64]bool{}
	for _, id := range all[len(all)-10:] {
		newest[id] = true
	}

	// With no bound every match is ranked: 41 messages hold the common word.
	lim.SearchResults, lim.RankMax = 100, 0
	hits, err := Search(a, lim, "harbour")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 41 {
		t.Fatalf("an unbounded search found %d messages, want 41", len(hits))
	}
	// The best match first and, among equals, the newest first, as before.
	for i := 1; i < len(hits); i++ {
		if hits[i-1].Snippet == "" || hits[i-1].When.IsZero() {
			t.Fatalf("a hit has no snippet or no time: %+v", hits[i-1])
		}
	}

	// Ranked, the first of them is the old message: it is the shortest that
	// holds the word.
	lim.SearchResults = 10
	if hits, err = Search(a, lim, "harbour"); err != nil || len(hits) != 10 || hits[0].MessageID != rare.ID {
		t.Fatalf("an unbounded search for ten gave %d, the first %d, want message %d first: %v", len(hits), hits[0].MessageID, rare.ID, err)
	}

	// Bounded to ten matches, a word 41 messages hold is too wide to rank:
	// the newest ten that hold it are given, the newest first.
	lim.RankMax = 10
	if hits, err = Search(a, lim, "harbour"); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 10 {
		t.Fatalf("a search bounded to 10 matches found %d", len(hits))
	}
	for i, h := range hits {
		if !newest[h.MessageID] || h.Snippet == "" || (i > 0 && h.MessageID > hits[i-1].MessageID) {
			t.Errorf("a search too wide to rank gave message %d at place %d, with snippet %q: want the newest ten, the newest first", h.MessageID, i, h.Snippet)
		}
	}
	lim.SearchResults = 100
	// A word fewer messages hold is found whatever its age, bound or not.
	if hits, err = Search(a, lim, "quillfeather"); err != nil || len(hits) != 1 || hits[0].MessageID != rare.ID {
		t.Fatalf("the rare word found %+v, %v", hits, err)
	}
	// Every word must be there: the rare word narrows the common one.
	if hits, err = Search(a, lim, "harbour quillfeather"); err != nil || len(hits) != 1 || hits[0].MessageID != rare.ID {
		t.Fatalf("both words found %+v, %v", hits, err)
	}

	// Any word: unbounded, the message with both words is first of 41.
	_, messages, err := MatchAnyWithin(a, "harbour quillfeather", 100, 8, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 41 || messages[0].MessageID != rare.ID {
		t.Fatalf("an unbounded search for either word found %d messages, the first %d", len(messages), messages[0].MessageID)
	}
	// Bounded, the common word is left out, since the rare one is in fewer
	// messages than the bound: the old message is found, and not lost
	// behind the newest ten that hold only the common word.
	_, messages, err = MatchAnyWithin(a, "harbour quillfeather", 100, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].MessageID != rare.ID || messages[0].Score >= 0 || messages[0].Snippet == "" {
		t.Fatalf("a bounded search for either word found %+v", messages)
	}
	// When every word is common, the newest matches are given.
	_, messages, err = MatchAnyWithin(a, "harbour calm", 10, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 10 {
		t.Fatalf("a bounded search for two common words found %d messages, want 10", len(messages))
	}
	for _, m := range messages {
		if !newest[m.MessageID] {
			t.Errorf("a bounded search for two common words gave message %d, which is older", m.MessageID)
		}
	}
	// A bound the record is under changes nothing.
	_, bounded, err := MatchAnyWithin(a, "harbour quillfeather", 100, 8, 1000)
	if err != nil {
		t.Fatal(err)
	}
	_, free, err := MatchAny(a, "harbour quillfeather", 100, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded) != len(free) {
		t.Fatalf("a bound above the record found %d messages, and no bound %d", len(bounded), len(free))
	}
	for i := range free {
		if bounded[i] != free[i] {
			t.Fatalf("a bound above the record changed result %d: %+v, %+v", i, bounded[i], free[i])
		}
	}
}

// The registry's default bound reads.
func TestTheRankBoundComesFromTheRegistry(t *testing.T) {
	lim, err := DefaultLimits()
	if err != nil {
		t.Fatal(err)
	}
	if lim.RankMax < 1000 {
		t.Fatalf("chats.search_rank_max is %d", lim.RankMax)
	}
}
