package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

var limits = chat.Limits{TitleLength: 150, SearchResults: 50, SnippetWords: 24}

type rig struct {
	t        *testing.T
	one, two *tenant.Store
	reg      *tools.Registry
}

func newRig(t *testing.T, messages int) *rig {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	g := &rig{t: t, reg: tools.New()}
	for i, name := range []string{"Test Person One", "Test Person Two"} {
		made, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		if i == 0 {
			g.one = s
		} else {
			g.two = s
		}
	}
	at := time.Date(2026, 11, 25, 14, 3, 0, 0, time.FixedZone("", 3600))
	if err := Add(g.reg, Options{Limits: limits, ReadChatMessages: messages, Now: func() time.Time { return at }}); err != nil {
		t.Fatal(err)
	}
	return g
}

// chat makes a chat with a title and alternating messages, the person first.
func (g *rig) chat(s *tenant.Store, title string, texts ...string) string {
	g.t.Helper()
	c, err := chat.Create(s)
	if err != nil {
		g.t.Fatal(err)
	}
	if _, err := chat.Rename(s, limits, c.ID, title); err != nil {
		g.t.Fatal(err)
	}
	var parent int64
	for i, text := range texts {
		role := seam.RoleUser
		if i%2 == 1 {
			role = seam.RoleAssistant
		}
		m, err := chat.Append(s, c.ID, chat.NewMessage{ParentID: parent, Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}})
		if err != nil {
			g.t.Fatal(err)
		}
		parent = m.ID
	}
	return c.ID
}

func (g *rig) run(s *tenant.Store, chatID, name, input string) (string, tools.Status) {
	g.t.Helper()
	res := g.reg.Run(context.Background(), tools.Call{Store: s, RequestID: "req-1", Actor: "person", ChatID: chatID, ID: "call_1", Name: name, Input: json.RawMessage(input)})
	return seam.Text(seam.Message{Blocks: res.Block.Content}), res.Status
}

func TestEveryBuiltInToolOnlyReads(t *testing.T) {
	g := newRig(t, 40)
	list := g.reg.List()
	if len(list) != 3 {
		t.Fatalf("%d tools are built in, want 3: %+v", len(list), list)
	}
	for _, tool := range list {
		if !tool.ReadOnly {
			t.Errorf("%s is not marked as only reading", tool.Name)
		}
	}
	for _, d := range g.reg.Definitions() {
		var schema struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(d.InputSchema, &schema); err != nil || schema.Type != "object" {
			t.Errorf("the schema of %s is not an object's: %s", d.Name, d.InputSchema)
		}
	}
	if err := Add(tools.New(), Options{Limits: limits}); err == nil {
		t.Error("read_chat was added with room for no messages")
	}
}

func TestCurrentTime(t *testing.T) {
	g := newRig(t, 40)
	text, status := g.run(g.one, "", CurrentTime, `{}`)
	if status != tools.StatusOK || !strings.Contains(text, "Wednesday, November 25, 2026, 14:03") || !strings.Contains(text, "UTC+01:00") || !strings.Contains(text, "2026-11-25T13:03:00Z") {
		t.Errorf("the time came back %s: %q", status, text)
	}
}

func TestSearchChatsFindsThePersonsOwnAndNoOthers(t *testing.T) {
	g := newRig(t, 40)
	walk := g.chat(g.one, "Planning the lighthouse walk", "What should I bring to the lighthouse?", "A windproof jacket and water.")
	here := g.chat(g.one, "Today", "Remind me what I asked about the lighthouse.")
	theirs := g.chat(g.two, "A lighthouse of my own", "Is the lighthouse open on Sunday?", "It is open from ten.")

	text, status := g.run(g.one, here, SearchChats, `{"query":"lighthouse"}`)
	if status != tools.StatusOK {
		t.Fatalf("the search came back %s: %q", status, text)
	}
	for _, want := range []string{"Planning the lighthouse walk", walk, "The title matched.", "The person wrote:"} {
		if !strings.Contains(text, want) {
			t.Errorf("the search does not hold %q:\n%s", want, text)
		}
	}
	for what, not := range map[string]string{"another person's chat": theirs, "another person's title": "of my own", "another person's words": "Sunday", "the chat the turn is in": here} {
		if strings.Contains(text, not) {
			t.Errorf("the search holds %s:\n%s", what, text)
		}
	}
	if text, _ := g.run(g.one, here, SearchChats, `{"query":"windproof"}`); !strings.Contains(text, "The reply said:") || !strings.HasPrefix(text, "1 match for") {
		t.Errorf("a word of a reply found:\n%s", text)
	}
	if text, status := g.run(g.one, here, SearchChats, `{"query":"submarine"}`); status != tools.StatusOK || !strings.Contains(text, "No other chat matches") {
		t.Errorf("a search with no match came back %s: %q", status, text)
	}
	for _, input := range []string{`{}`, `{"query":"  "}`, `{"query":7}`} {
		if text, status := g.run(g.one, here, SearchChats, input); status != tools.StatusError || !strings.Contains(text, `needs a "query"`) {
			t.Errorf("search_chats with %s came back %s: %q", input, status, text)
		}
	}
}

func TestReadChatReadsThePersonsOwnAndNoOthers(t *testing.T) {
	g := newRig(t, 3)
	walk := g.chat(g.one, "Planning the lighthouse walk", "What should I bring?", "A windproof jacket.", "How long is it?", "Nine kilometres.", "And the cliff path?", "Seven, and slower.")
	theirs := g.chat(g.two, "A lighthouse of my own", "Is it open on Sunday?", "From ten.")

	text, status := g.run(g.one, "", ReadChat, `{"chat_id":"`+walk+`"}`)
	if status != tools.StatusOK {
		t.Fatalf("the chat came back %s: %q", status, text)
	}
	for _, want := range []string{`"Planning the lighthouse walk"`, "The first 3 messages are left out.", "The reply: Nine kilometres.", "The person: And the cliff path?", "The reply: Seven, and slower."} {
		if !strings.Contains(text, want) {
			t.Errorf("the chat does not hold %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "windproof") {
		t.Errorf("a message past the limit was returned:\n%s", text)
	}
	// Another person's chat is not there, by its real id.
	for _, input := range []string{`{"chat_id":"` + theirs + `"}`, `{"chat_id":"0000000000000000"}`, `{"chat_id":"../x"}`} {
		text, status := g.run(g.one, "", ReadChat, input)
		if status != tools.StatusError || !strings.Contains(text, "There is no chat with the id") || strings.Contains(text, "Sunday") {
			t.Errorf("read_chat with %s came back %s: %q", input, status, text)
		}
	}
	if text, status := g.run(g.one, "", ReadChat, `{}`); status != tools.StatusError || !strings.Contains(text, `needs a "chat_id"`) {
		t.Errorf("read_chat with no id came back %s: %q", status, text)
	}
	// The other person reads their own.
	if text, status := g.run(g.two, "", ReadChat, `{"chat_id":"`+theirs+`"}`); status != tools.StatusOK || !strings.Contains(text, "From ten.") {
		t.Errorf("a person's own chat came back %s: %q", status, text)
	}
}
