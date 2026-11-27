package chat

import (
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// whole joins an excerpt's parts back into its text.
func whole(ps []Part) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.Text)
	}
	return b.String()
}

// marked returns the parts a search marked.
func marked(ps []Part) []string {
	var out []string
	for _, p := range ps {
		if p.Match {
			out = append(out, p.Text)
		}
	}
	return out
}

// The item's first proof, in the chat record: a search finds a chat by a
// word in a message, once however many of its messages hold the word, with
// the word marked in the snippet, and reads one tenant's chats only.
func TestSearchByChatFindsEachChatOnce(t *testing.T) {
	a, b := twoStores(t)
	walk := newChat(t, a, "Planning the lighthouse walk")
	m1 := say(t, a, walk.ID, 0, seam.RoleUser, "What should I bring to the old lighthouse?")
	m2 := say(t, a, walk.ID, m1.ID, seam.RoleAssistant, "A windproof jacket, water and a paper map.\nThe lighthouse path is steep, so bring a second map of the lighthouse quay.")
	// A second branch: an edit of the first message, which also holds the word.
	say(t, a, walk.ID, 0, seam.RoleUser, "What should I bring to the lighthouse in winter?")
	ferry := newChat(t, a, "North ferry times")
	f1 := say(t, a, ferry.ID, 0, seam.RoleUser, "When does the north ferry leave? I need a map of the quay too.")
	bakery := newChat(t, a, "Names for a bakery")
	say(t, a, bakery.ID, 0, seam.RoleUser, "Suggest three names for a small bakery by the harbour.")
	theirs := newChat(t, b, "A lighthouse of my own")
	say(t, b, theirs.ID, 0, seam.RoleUser, "Is the lighthouse map on sale on Sunday?")

	// A word only messages hold: two chats, each once.
	found, err := SearchByChat(a, "map")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("a word in the messages of two chats found %d chats: %+v", len(found), found)
	}
	byID := map[string]Found{}
	for _, f := range found {
		if f.ChatID == theirs.ID {
			t.Fatalf("another tenant's chat was found: %+v", f)
		}
		byID[f.ChatID] = f
	}
	if f := byID[walk.ID]; f.Title || f.Messages != 1 || f.MessageID != m2.ID {
		t.Errorf("the walk was found as %+v, want the one message %d", f, m2.ID)
	}
	if f := byID[ferry.ID]; f.Title || f.Messages != 1 || f.MessageID != f1.ID {
		t.Errorf("the ferry chat was found as %+v, want the one message %d", f, f1.ID)
	}

	// A word in a title and in three messages of that chat, on two branches:
	// the chat is found once, with its title and all three counted.
	found, err = SearchByChat(a, "LIGHTHOUSE")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ChatID != walk.ID || !found[0].Title || found[0].Messages != 3 {
		t.Fatalf("the word of a title and three messages found %+v", found)
	}

	// The excerpt: the title with the word marked, and the words round the
	// match in the best message, with its line break read as a space.
	ex, err := Excerpts(a, limits(t), "lighthouse", found)
	if err != nil {
		t.Fatal(err)
	}
	e := ex[walk.ID]
	if whole(e.Title) != "Planning the lighthouse walk" || len(marked(e.Title)) != 1 || marked(e.Title)[0] != "lighthouse" {
		t.Errorf("the title's parts are %+v", e.Title)
	}
	if e.MessageID != found[0].MessageID || e.Role == "" || len(marked(e.Snippet)) == 0 || strings.ContainsAny(whole(e.Snippet), "\n\x02\x03") {
		t.Errorf("the snippet is %+v for message %d (%s)", e.Snippet, e.MessageID, e.Role)
	}
	for _, w := range marked(e.Snippet) {
		if !strings.EqualFold(w, "lighthouse") {
			t.Errorf("the snippet marks %q, which was not searched for", w)
		}
	}

	// Title matches come before chats only a message found.
	found, err = SearchByChat(a, "ferry")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || !found[0].Title || found[0].Messages != 1 {
		t.Fatalf("ferry found %+v", found)
	}
	say(t, a, bakery.ID, 0, seam.RoleUser, "Is there a ferry to the bakery?")
	found, err = SearchByChat(a, "ferry")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].ChatID != ferry.ID || found[1].ChatID != bakery.ID || found[1].Title {
		t.Fatalf("the title match is not first: %+v", found)
	}

	// The start of a word matches, every word must be in one title or one
	// message, and what a person types is never read as search syntax.
	if found, _ := SearchByChat(a, "windpr"); len(found) != 1 || found[0].ChatID != walk.ID {
		t.Errorf("the start of a word found %+v", found)
	}
	if found, _ := SearchByChat(a, "windproof ferry"); len(found) != 0 {
		t.Errorf("two words of two different messages found %+v", found)
	}
	for _, q := range []string{`"`, `map OR`, `NEAR(map`, `map*`, `-map`, `title:map`, "?!", ""} {
		if _, err := SearchByChat(a, q); err != nil {
			t.Errorf("the query %q failed: %v", q, err)
		}
	}
	if found, err := SearchByChat(a, "?!"); err != nil || found != nil {
		t.Errorf("a query with no word found %v %v", found, err)
	}

	// A deleted chat is found no more.
	if err := Delete(a, walk.ID); err != nil {
		t.Fatal(err)
	}
	if found, _ := SearchByChat(a, "lighthouse"); len(found) != 0 {
		t.Errorf("a deleted chat is still found: %+v", found)
	}
	// The other tenant still finds its own, and only its own.
	if found, _ := SearchByChat(b, "lighthouse map"); len(found) != 1 || found[0].ChatID != theirs.ID {
		t.Errorf("the other tenant found %+v", found)
	}

	// A snippet length no index can cut is refused, and nothing asked for is nothing read.
	lim := limits(t)
	lim.SnippetWords = 65
	if _, err := Excerpts(a, lim, "map", []Found{{ChatID: ferry.ID, MessageID: f1.ID, Messages: 1}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a snippet of 65 words gave %v", err)
	}
	if ex, err := Excerpts(a, limits(t), "map", nil); err != nil || len(ex) != 0 {
		t.Errorf("no chats gave %v %v", ex, err)
	}
}

// parts cuts marked text into its pieces, and a mark with no partner does
// no harm.
func TestPartsCutMarkedText(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []Part
	}{
		{"", nil},
		{"plain words", []Part{{Text: "plain words"}}},
		{"a \x02map\x03 of the \x02quay\x03", []Part{{Text: "a "}, {Text: "map", Match: true}, {Text: " of the "}, {Text: "quay", Match: true}}},
		{"\x02map\x03\x02s\x03", []Part{{Text: "maps", Match: true}}},
		{"a \x02map with no end", []Part{{Text: "a map with no end"}}},
		{"an end\x03 with no start", []Part{{Text: "an end with no start"}}},
	} {
		got := parts(c.in)
		if len(got) != len(c.want) {
			t.Errorf("parts(%q) = %+v, want %+v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parts(%q) = %+v, want %+v", c.in, got, c.want)
				break
			}
		}
	}
}
