package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

// Every chat, name and fact in these tests is invented.

// P6-07: an incognito chat is found and read from no other chat, by its
// title, its recap or its messages. From inside itself it can still be read.
func TestAnIncognitoChatIsLeftOutOfThePastChatTools(t *testing.T) {
	g := newRig(t, 40)
	quiet := g.chat(g.one, "Lighthouse keeper rota", "Who keeps the lighthouse in March?", "The lighthouse rota is pinned in the hut.")
	open := g.chat(g.one, "Lighthouse paint", "What paint suits the lighthouse door?", "A marine enamel.")
	here := g.chat(g.one, "Today", "What did we say about the lighthouse?")
	g.recapOf(g.one, quiet, recap.Recap{Summary: "The lighthouse rota.", Topics: []string{"keepers"}})
	if err := memory.SetIncognito(context.Background(), g.one, quiet, true); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"lighthouse", "rota", "keepers"} {
		text, status := g.run(g.one, here, SearchChats, `{"query":"`+query+`"}`)
		if status != tools.StatusOK || strings.Contains(text, quiet) || strings.Contains(text, "keeper rota") || strings.Contains(text, "pinned in the hut") {
			t.Errorf("searching %q finds the incognito chat (%s): %q", query, status, text)
		}
		if query == "lighthouse" && !strings.Contains(text, open) {
			t.Errorf("searching %q lost the ordinary chat: %q", query, text)
		}
	}
	if text, status := g.run(g.one, here, ReadChat, `{"chat_id":"`+quiet+`"}`); status == tools.StatusOK || strings.Contains(text, "pinned in the hut") {
		t.Errorf("read_chat read the incognito chat from another (%s): %q", status, text)
	}
	if text, status := g.run(g.one, quiet, ReadChat, `{"chat_id":"`+quiet+`"}`); status != tools.StatusOK || !strings.Contains(text, "pinned in the hut") {
		t.Errorf("read_chat from inside the incognito chat (%s): %q", status, text)
	}
}
