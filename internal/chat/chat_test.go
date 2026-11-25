package chat

import (
	"encoding/json"
	"errors"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// twoStores makes an install with two tenants and returns an open handle
// for each. The clock steps one second each time it is read, so the order
// of things never rests on how fast the test runs. Every name and every
// line of chat here is invented.
func twoStores(t *testing.T) (a, b *tenant.Store) {
	t.Helper()
	old, at := now, time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	now = func() time.Time { at = at.Add(time.Second); return at }
	t.Cleanup(func() { now = old })

	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"Test Person One", "Test Person Two"} {
		ten, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(ten.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		if i == 0 {
			a = s
		} else {
			b = s
		}
	}
	return a, b
}

func limits(t *testing.T) Limits {
	t.Helper()
	lim, err := DefaultLimits()
	if err != nil {
		t.Fatal(err)
	}
	return lim
}

func newChat(t *testing.T, s *tenant.Store, title string) Chat {
	t.Helper()
	c, err := Create(s)
	if err != nil {
		t.Fatal(err)
	}
	if title != "" {
		if c, err = Rename(s, limits(t), c.ID, title); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func text(words string) []seam.Block {
	return []seam.Block{{Type: seam.BlockText, Text: words}}
}

// say appends a text message and returns it.
func say(t *testing.T, s *tenant.Store, chatID string, parent int64, role seam.Role, words string) Message {
	t.Helper()
	m, err := Append(s, chatID, NewMessage{ParentID: parent, Role: role, Blocks: text(words)})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// wantPath checks that the chat's current branch is exactly these messages.
func wantPath(t *testing.T, s *tenant.Store, chatID string, want ...Message) {
	t.Helper()
	path, err := Path(s, chatID)
	if err != nil {
		t.Fatal(err)
	}
	var got, ids []int64
	for _, m := range path {
		got = append(got, m.ID)
	}
	for _, m := range want {
		ids = append(ids, m.ID)
	}
	if !slices.Equal(got, ids) {
		t.Fatalf("the current branch is %v, want %v", got, ids)
	}
	for i, m := range path {
		if !reflect.DeepEqual(m.Blocks, want[i].Blocks) || m.Role != want[i].Role || m.ParentID != want[i].ParentID {
			t.Errorf("message %d changed: %+v, written as %+v", m.ID, m, want[i])
		}
	}
}

func choose(t *testing.T, s *tenant.Store, chatID string, m Message) {
	t.Helper()
	if err := Select(s, chatID, m.ID); err != nil {
		t.Fatal(err)
	}
}

// count runs a query that returns one number.
func count(t *testing.T, s *tenant.Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func writeFile(t *testing.T, s *tenant.Store, name string) {
	t.Helper()
	f, err := s.Files().Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("invented bytes for " + name); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// filesOnDisk lists what is in the tenant's files folder.
func filesOnDisk(t *testing.T, s *tenant.Store) []string {
	t.Helper()
	entries, err := fs.ReadDir(s.Files().FS(), ".")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestFullTextSearchIsCompiledIn(t *testing.T) {
	a, _ := twoStores(t)
	if count(t, a, "SELECT sqlite_compileoption_used('ENABLE_FTS5')") != 1 {
		t.Fatal("this SQLite was built without FTS5")
	}
	// The migration made both search tables, and they answer a query.
	for _, table := range []string{"chat_title_search", "message_search"} {
		if n := count(t, a, "SELECT count(*) FROM "+table+" WHERE "+table+" MATCH 'anything'"); n != 0 {
			t.Errorf("%s holds %d rows in a new database", table, n)
		}
	}
}

func TestDefaultLimitsComeFromTheRegistry(t *testing.T) {
	lim := limits(t)
	if lim.TitleLength < 1 || lim.SearchResults < 1 || lim.SnippetWords < 1 || lim.SnippetWords > 64 {
		t.Errorf("the defaults are not usable: %+v", lim)
	}
}

func TestChatsAreListedRenamedAndStarred(t *testing.T) {
	a, _ := twoStores(t)
	lim := limits(t)
	one, two, three := newChat(t, a, ""), newChat(t, a, ""), newChat(t, a, "")
	if one.ID == two.ID || two.ID == three.ID || one.Title != "" || one.Starred {
		t.Fatalf("new chats are wrong: %+v %+v %+v", one, two, three)
	}
	list := func(f Filter) string {
		t.Helper()
		chats, err := List(a, f)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, c := range chats {
			names = append(names, map[string]string{one.ID: "one", two.ID: "two", three.ID: "three"}[c.ID])
		}
		return strings.Join(names, " ")
	}
	if got := list(Filter{}); got != "three two one" {
		t.Errorf("new chats are listed %q, want the newest first", got)
	}

	// A new message is activity. A new name and a star are not.
	say(t, a, one.ID, 0, seam.RoleUser, "What is a keystone in an arch?")
	if _, err := Rename(a, lim, two.ID, "  Stone \n arches  "); err != nil {
		t.Fatal(err)
	}
	if _, err := Star(a, two.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := list(Filter{}); got != "one three two" {
		t.Errorf("after a message in the oldest chat the list is %q", got)
	}
	if got := list(Filter{Limit: 2}); got != "one three" {
		t.Errorf("the first two are %q", got)
	}
	if got := list(Filter{Limit: 2, Offset: 2}); got != "two" {
		t.Errorf("the next two are %q", got)
	}
	if got := list(Filter{Starred: true}); got != "two" {
		t.Errorf("the starred chats are %q", got)
	}

	got, err := Get(a, two.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Stone arches" || !got.Starred || !got.Created.Equal(two.Created) || !got.Active.Equal(two.Active) {
		t.Errorf("the renamed, starred chat reads back as %+v", got)
	}
	if got, err = Get(a, one.ID); err != nil || !got.Active.After(got.Created) {
		t.Errorf("a message did not move the chat's activity: %+v %v", got, err)
	}
	if got, err = Star(a, two.ID, false); err != nil || got.Starred {
		t.Errorf("the star was not cleared: %+v %v", got, err)
	}
	if got := list(Filter{Starred: true}); got != "" {
		t.Errorf("the starred chats are %q after the star was cleared", got)
	}

	// Titles are checked. The limit counts characters, not bytes.
	for _, title := range []string{"", " \n ", strings.Repeat("x", lim.TitleLength+1)} {
		if _, err := Rename(a, lim, one.ID, title); !errors.Is(err, ErrInvalid) {
			t.Errorf("Rename(%q) gave %v, want it refused", title, err)
		}
	}
	if _, err := Rename(a, lim, one.ID, strings.Repeat("é", lim.TitleLength)); err != nil {
		t.Errorf("a title of exactly the limit was refused: %v", err)
	}
	if _, err := List(a, Filter{Limit: -1}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a negative limit gave %v", err)
	}
}

// The item's proof: edit and retry create branches that can be walked back
// and forth.
func TestEditAndRetryMakeBranchesThatCanBeWalked(t *testing.T) {
	a, _ := twoStores(t)
	c := newChat(t, a, "")
	wantPath(t, a, c.ID)

	ask := say(t, a, c.ID, 0, seam.RoleUser, "Name a river.")
	reply := say(t, a, c.ID, ask.ID, seam.RoleAssistant, "The Invented River.")
	more := say(t, a, c.ID, reply.ID, seam.RoleUser, "How long is it?")
	answer := say(t, a, c.ID, more.ID, seam.RoleAssistant, "Ninety made-up miles.")
	wantPath(t, a, c.ID, ask, reply, more, answer)

	// Retry the last reply: a second reply under the same question.
	retry := say(t, a, c.ID, more.ID, seam.RoleAssistant, "About a hundred made-up miles.")
	if !slices.Equal(retry.Siblings, []int64{answer.ID, retry.ID}) || retry.ParentID != more.ID {
		t.Fatalf("the retry is %+v, want it beside the first reply", retry)
	}
	wantPath(t, a, c.ID, ask, reply, more, retry)

	// Edit the second question: a new question under the same reply, and
	// then its own answer.
	edit := say(t, a, c.ID, reply.ID, seam.RoleUser, "How wide is it?")
	wantPath(t, a, c.ID, ask, reply, edit)
	wide := say(t, a, c.ID, edit.ID, seam.RoleAssistant, "Twelve made-up yards.")
	wantPath(t, a, c.ID, ask, reply, edit, wide)

	// Walk back to the first question. Its branch is as it was left, on
	// the retry.
	choose(t, a, c.ID, more)
	wantPath(t, a, c.ID, ask, reply, more, retry)
	// Step to the first reply and back to the retry.
	choose(t, a, c.ID, answer)
	wantPath(t, a, c.ID, ask, reply, more, answer)
	choose(t, a, c.ID, retry)
	wantPath(t, a, c.ID, ask, reply, more, retry)
	choose(t, a, c.ID, answer)
	// Forth to the edit and back again: each side is as it was left.
	choose(t, a, c.ID, edit)
	wantPath(t, a, c.ID, ask, reply, edit, wide)
	choose(t, a, c.ID, more)
	wantPath(t, a, c.ID, ask, reply, more, answer)

	// Edit the very first message. The chat now has two first messages.
	first := say(t, a, c.ID, 0, seam.RoleUser, "Name a mountain.")
	if !slices.Equal(first.Siblings, []int64{ask.ID, first.ID}) {
		t.Fatalf("the edited first message has siblings %v", first.Siblings)
	}
	wantPath(t, a, c.ID, first)
	choose(t, a, c.ID, ask)
	wantPath(t, a, c.ID, ask, reply, more, answer)

	// A message deep in a branch that is not current can be opened in one
	// step, as a search result would be.
	choose(t, a, c.ID, first)
	choose(t, a, c.ID, wide)
	wantPath(t, a, c.ID, ask, reply, edit, wide)

	// A message that lands in a branch the person has left does not move
	// them. It is there when they go back to that branch.
	late := say(t, a, c.ID, retry.ID, seam.RoleUser, "And how deep?")
	wantPath(t, a, c.ID, ask, reply, edit, wide)
	choose(t, a, c.ID, more)
	wantPath(t, a, c.ID, ask, reply, more, answer)
	choose(t, a, c.ID, retry)
	wantPath(t, a, c.ID, ask, reply, more, retry, late)

	// Nothing was overwritten: all nine messages are there, as written.
	if n := count(t, a, "SELECT count(*) FROM messages WHERE chat_id = ?", c.ID); n != 9 {
		t.Errorf("the chat holds %d messages, want 9", n)
	}
	for _, m := range []Message{ask, reply, more, answer, retry, edit, wide, first, late} {
		got, err := GetMessage(a, c.ID, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Blocks, m.Blocks) || got.Role != m.Role || got.ParentID != m.ParentID || !got.Created.Equal(m.Created) {
			t.Errorf("message %d changed: %+v, written as %+v", m.ID, got, m)
		}
	}
	// The siblings at the branch point list every edit, oldest first.
	got, err := GetMessage(a, c.ID, more.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Siblings, []int64{more.ID, edit.ID}) {
		t.Errorf("the second question's siblings are %v, want %v", got.Siblings, []int64{more.ID, edit.ID})
	}
}

func TestALongChatIsWalked(t *testing.T) {
	a, _ := twoStores(t)
	c := newChat(t, a, "")
	var all []Message
	var parent int64
	for i := 0; i < 600; i++ {
		role := seam.RoleUser
		if i%2 == 1 {
			role = seam.RoleAssistant
		}
		m := say(t, a, c.ID, parent, role, "An invented line.")
		all, parent = append(all, m), m.ID
	}
	wantPath(t, a, c.ID, all...)
	// Branch near the top, then come back down the long side in one step.
	side := say(t, a, c.ID, all[0].ID, seam.RoleAssistant, "Another invented line.")
	wantPath(t, a, c.ID, all[0], side)
	choose(t, a, c.ID, all[len(all)-1])
	wantPath(t, a, c.ID, all...)
}

func TestContentIsStoredInTheSeamFormat(t *testing.T) {
	a, _ := twoStores(t)
	c := newChat(t, a, "")
	turns := []seam.Message{
		{Role: seam.RoleUser, Blocks: []seam.Block{
			{Type: seam.BlockText, Text: "What does this picture show?"},
			{Type: seam.BlockImage, MediaType: "image/png", Ref: "picture-one.png"},
			{Type: seam.BlockFile, MediaType: "text/plain", Name: "notes.txt", Data: []byte("invented notes")},
		}},
		{Role: seam.RoleAssistant, Blocks: []seam.Block{
			{Type: seam.BlockThinking, Text: "A lookup would help.", Signature: "made-up-signature"},
			{Type: seam.BlockText, Text: "Let me look that up."},
			{Type: seam.BlockToolCall, ID: "call-1", Name: "lookup", Input: json.RawMessage(`{"q":"lighthouse"}`)},
		}},
		{Role: seam.RoleUser, Blocks: []seam.Block{
			{Type: seam.BlockToolResult, ID: "call-1", Content: []seam.Block{
				{Type: seam.BlockText, Text: "An invented lighthouse."},
				{Type: seam.BlockFile, MediaType: "text/plain", Name: "report.txt", Ref: "report-one.txt"},
			}},
		}},
	}
	var parent int64
	for _, turn := range turns {
		m, err := Append(a, c.ID, NewMessage{ParentID: parent, Role: turn.Role, Blocks: turn.Blocks})
		if err != nil {
			t.Fatal(err)
		}
		parent = m.ID
	}
	path, err := Path(a, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != len(turns) {
		t.Fatalf("the path holds %d messages, want %d", len(path), len(turns))
	}
	for i, m := range path {
		if m.Role != turns[i].Role || !reflect.DeepEqual(m.Blocks, turns[i].Blocks) {
			t.Errorf("message %d reads back as %+v, want %+v", i, m, turns[i])
		}
		if m.ChatID != c.ID || m.Created.IsZero() {
			t.Errorf("message %d has chat %q and time %v", i, m.ChatID, m.Created)
		}
	}
	// What is stored is the seam's own JSON, and the seam reads it.
	var stored string
	if err := a.DB().QueryRow("SELECT content FROM messages WHERE id = ?", path[1].ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if sm, err := seam.DecodeMessage([]byte(stored)); err != nil || !reflect.DeepEqual(sm, turns[1]) {
		t.Errorf("the stored content does not decode to the message: %v\n%s", err, stored)
	}
	// Files the blocks refer to are tied to the chat, a tool result's too.
	if names, err := Files(a, c.ID); err != nil || !slices.Equal(names, []string{"picture-one.png", "report-one.txt"}) {
		t.Errorf("the chat's files are %v %v", names, err)
	}

	// What the seam refuses, the record refuses, and nothing is written.
	before := count(t, a, "SELECT count(*) FROM messages")
	refused := map[string]NewMessage{
		"no blocks":                {Role: seam.RoleUser},
		"an unknown role":          {Role: "system", Blocks: text("Invented.")},
		"thinking from the person": {Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockThinking, Text: "Invented."}}},
		"a text block with a name": {Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Invented.", Name: "x"}}},
		"a file outside the folder": {Role: seam.RoleUser, Blocks: []seam.Block{
			{Type: seam.BlockImage, MediaType: "image/png", Ref: "../picture-one.png"}}},
		"a negative parent": {ParentID: -1, Role: seam.RoleUser, Blocks: text("Invented.")},
	}
	for name, m := range refused {
		if _, err := Append(a, c.ID, m); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want it refused", name, err)
		}
	}
	other := newChat(t, a, "")
	elsewhere := say(t, a, other.ID, 0, seam.RoleUser, "Invented.")
	for name, m := range map[string]NewMessage{
		"a parent that does not exist": {ParentID: 999999, Role: seam.RoleUser, Blocks: text("Invented.")},
		"a parent in another chat":     {ParentID: elsewhere.ID, Role: seam.RoleUser, Blocks: text("Invented.")},
	} {
		if _, err := Append(a, c.ID, m); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: got %v, want not found", name, err)
		}
	}
	if after := count(t, a, "SELECT count(*) FROM messages"); after != before+1 {
		t.Errorf("refused messages left rows behind: %d before, %d after", before, after)
	}
	// A message is reached only through its own chat.
	if _, err := GetMessage(a, c.ID, elsewhere.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a message was read through another chat: %v", err)
	}
	if err := Select(a, c.ID, elsewhere.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a message was selected through another chat: %v", err)
	}
	wantPath(t, a, other.ID, elsewhere)
}

func TestSearchFindsTitlesAndText(t *testing.T) {
	a, _ := twoStores(t)
	lim := limits(t)
	garden := newChat(t, a, "Garden plans for spring")
	ask := say(t, a, garden.ID, 0, seam.RoleUser, "How deep should I plant tulip bulbs?")
	reply := say(t, a, garden.ID, ask.ID, seam.RoleAssistant, "Plant tulip bulbs about fifteen centimetres deep.")
	bread := newChat(t, a, "Bread baking notes")
	starter := say(t, a, bread.ID, 0, seam.RoleUser, "My sourdough starter smells of apples at the café.")

	search := func(query string) []Hit {
		t.Helper()
		hits, err := Search(a, lim, query)
		if err != nil {
			t.Fatalf("Search(%q): %v", query, err)
		}
		return hits
	}

	// Text: the result names the chat and the message.
	hits := search("tulip")
	if len(hits) != 2 {
		t.Fatalf("tulip found %d results, want 2: %+v", len(hits), hits)
	}
	found := map[int64]Hit{}
	for _, h := range hits {
		found[h.MessageID] = h
		if h.ChatID != garden.ID || h.Title != "Garden plans for spring" || !strings.Contains(h.Snippet, "tulip") {
			t.Errorf("a tulip result is %+v", h)
		}
	}
	if found[ask.ID].Role != seam.RoleUser || found[reply.ID].Role != seam.RoleAssistant || !found[ask.ID].When.Equal(ask.Created) {
		t.Errorf("the results do not name the two messages: %+v", hits)
	}

	// A title: no message is named.
	hits = search("garden")
	if len(hits) != 1 || hits[0].ChatID != garden.ID || hits[0].MessageID != 0 || hits[0].Role != "" || hits[0].Snippet != garden.Title {
		t.Errorf("garden found %+v, want the chat by its title", hits)
	}

	// The start of a word, capitals, an accent left off, and two words.
	for query, want := range map[string]int64{"sourd": starter.ID, "SOURDOUGH": starter.ID, "cafe": starter.ID, "apples starter": starter.ID, "centim deep": reply.ID} {
		if hits := search(query); len(hits) != 1 || hits[0].MessageID != want {
			t.Errorf("%q found %+v, want message %d", query, hits, want)
		}
	}
	// Every word must be in the same title or message.
	if hits := search("tulip sourdough"); len(hits) != 0 {
		t.Errorf("words from two chats found %+v", hits)
	}
	// Nothing a person types is read as search syntax.
	for _, query := range []string{"", "   ", `"`, `""`, "-", "*", "tulip*", `"tulip`, "tulip AND", "NOT tulip", "body:tulip", "(tulip", "tulip OR sourdough", "^ + ' ; --", "NEAR(tulip bulbs)"} {
		if _, err := Search(a, lim, query); err != nil {
			t.Errorf("Search(%q) failed: %v", query, err)
		}
	}
	if hits := search(`"tulip`); len(hits) != 2 {
		t.Errorf("a stray quote lost the results: %+v", hits)
	}

	// A new title replaces the old one in the index. A title match comes
	// before a message match.
	if _, err := Rename(a, lim, bread.ID, "Starter care"); err != nil {
		t.Fatal(err)
	}
	if hits := search("bread"); len(hits) != 0 {
		t.Errorf("the old title still finds %+v", hits)
	}
	hits = search("starter")
	if len(hits) != 2 || hits[0].MessageID != 0 || hits[0].Title != "Starter care" || hits[1].MessageID != starter.ID {
		t.Errorf("starter found %+v, want the title and then the message", hits)
	}
	one := lim
	one.SearchResults = 1
	if hits, err := Search(a, one, "starter"); err != nil || len(hits) != 1 {
		t.Errorf("a limit of one gave %d results, %v", len(hits), err)
	}
	if hits, err := Search(a, one, "tulip"); err != nil || len(hits) != 1 {
		t.Errorf("a limit of one gave %d results, %v", len(hits), err)
	}
	for _, bad := range []Limits{{}, {SearchResults: 5}, {SearchResults: 5, SnippetWords: 65}} {
		if _, err := Search(a, bad, "tulip"); !errors.Is(err, ErrInvalid) {
			t.Errorf("limits %+v gave %v, want them refused", bad, err)
		}
	}

	// A message on a branch that is not current is still found, and
	// selecting it opens that branch.
	edit := say(t, a, garden.ID, 0, seam.RoleUser, "Where do daffodils grow best?")
	wantPath(t, a, garden.ID, edit)
	hits = search("centimetres")
	if len(hits) != 1 || hits[0].MessageID != reply.ID {
		t.Fatalf("the message on the old branch was not found: %+v", hits)
	}
	choose(t, a, hits[0].ChatID, reply)
	wantPath(t, a, garden.ID, ask, reply)

	// Only the words of text blocks are indexed.
	if _, err := Append(a, garden.ID, NewMessage{ParentID: reply.ID, Role: seam.RoleUser, Blocks: []seam.Block{
		{Type: seam.BlockImage, MediaType: "image/png", Ref: "zinnia.png"}}}); err != nil {
		t.Fatal(err)
	}
	if hits := search("zinnia"); len(hits) != 0 {
		t.Errorf("a file's name was indexed: %+v", hits)
	}
	if n, m := count(t, a, "SELECT count(*) FROM message_search"), count(t, a, "SELECT count(*) FROM messages"); n != m-1 {
		t.Errorf("%d search rows for %d messages, one of them with no text", n, m)
	}
}

// The item's proof: deletion removes a chat and its files.
func TestDeleteRemovesTheChatAndItsFiles(t *testing.T) {
	a, _ := twoStores(t)
	lim := limits(t)
	for _, name := range []string{"only-first.png", "in-both.png", "only-second.png", "added-by-hand.txt", "tied-to-nothing.txt"} {
		writeFile(t, a, name)
	}
	image := func(name string) seam.Block {
		return seam.Block{Type: seam.BlockImage, MediaType: "image/png", Ref: name}
	}

	first := newChat(t, a, "Harbour sketches")
	ask, err := Append(a, first.ID, NewMessage{Role: seam.RoleUser, Blocks: []seam.Block{
		{Type: seam.BlockText, Text: "Two harbour sketches to compare."}, image("only-first.png"), image("in-both.png")}})
	if err != nil {
		t.Fatal(err)
	}
	reply := say(t, a, first.ID, ask.ID, seam.RoleAssistant, "The first harbour sketch is bolder.")
	say(t, a, first.ID, ask.ID, seam.RoleAssistant, "The second harbour sketch is calmer.")
	if err := AddFile(a, first.ID, "added-by-hand.txt"); err != nil {
		t.Fatal(err)
	}
	if err := AddFile(a, first.ID, "added-by-hand.txt"); err != nil {
		t.Errorf("tying a file twice failed: %v", err)
	}
	if err := AddFile(a, first.ID, "../added-by-hand.txt"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a name outside the folder gave %v", err)
	}

	second := newChat(t, a, "Harbour walk")
	keep, err := Append(a, second.ID, NewMessage{Role: seam.RoleUser, Blocks: []seam.Block{
		{Type: seam.BlockText, Text: "Photos from the harbour walk."}, image("in-both.png"), image("only-second.png")}})
	if err != nil {
		t.Fatal(err)
	}
	if hits, err := Search(a, lim, "harbour"); err != nil || len(hits) != 6 {
		t.Fatalf("before the delete, harbour found %d results, %v", len(hits), err)
	}
	// A table from a later item, tied to its chat the way the doc asks.
	if _, err := a.DB().Exec(`CREATE TABLE test_later_item (
		chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE, note TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.ID, second.ID} {
		if _, err := a.DB().Exec("INSERT INTO test_later_item(chat_id, note) VALUES(?, 'invented')", id); err != nil {
			t.Fatal(err)
		}
	}

	if err := Delete(a, first.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, a, "SELECT count(*) FROM test_later_item WHERE chat_id = ?", second.ID); n != 1 {
		t.Errorf("the other chat's row in a later table: %d, want 1", n)
	}

	// The chat is gone from every function that takes its id.
	gone := map[string]error{}
	_, gone["Get"] = Get(a, first.ID)
	_, gone["Path"] = Path(a, first.ID)
	_, gone["Files"] = Files(a, first.ID)
	_, gone["GetMessage"] = GetMessage(a, first.ID, ask.ID)
	_, gone["Append"] = Append(a, first.ID, NewMessage{ParentID: reply.ID, Role: seam.RoleUser, Blocks: text("Invented.")})
	_, gone["Rename"] = Rename(a, lim, first.ID, "Invented")
	_, gone["Star"] = Star(a, first.ID, true)
	gone["Select"] = Select(a, first.ID, reply.ID)
	gone["AddFile"] = AddFile(a, first.ID, "only-second.png")
	gone["Delete"] = Delete(a, first.ID)
	for name, err := range gone {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s on a deleted chat gave %v, want not found", name, err)
		}
	}
	// No row of it is left in any table, and only the other chat's rows
	// are in the search index.
	for table, want := range map[string]int{"chats": 1, "messages": 1, "chat_files": 2, "chat_title_search": 1, "message_search": 1, "test_later_item": 1} {
		if n := count(t, a, "SELECT count(*) FROM "+table); n != want {
			t.Errorf("%s holds %d rows after the delete, want %d", table, n, want)
		}
	}
	for _, q := range []string{
		"SELECT count(*) FROM messages WHERE chat_id = ?",
		"SELECT count(*) FROM chat_files WHERE chat_id = ?",
	} {
		if n := count(t, a, q, first.ID); n != 0 {
			t.Errorf("%s: %d rows left", q, n)
		}
	}
	for _, word := range []string{"sketches", "sketch", "bolder", "calmer", "compare"} {
		if hits, err := Search(a, lim, word); err != nil || len(hits) != 0 {
			t.Errorf("%q still finds %+v, %v", word, hits, err)
		}
	}
	for _, table := range []string{"chat_title_search", "message_search"} {
		if _, err := a.DB().Exec("INSERT INTO " + table + "(" + table + ") VALUES('integrity-check')"); err != nil {
			t.Errorf("%s is damaged after the delete: %v", table, err)
		}
	}
	// Its own files are gone. The file the other chat also holds, the other
	// chat's own file, and a file no chat was tied to are untouched.
	if got, want := filesOnDisk(t, a), []string{"in-both.png", "only-second.png", "tied-to-nothing.txt"}; !slices.Equal(got, want) {
		t.Errorf("the files folder holds %v, want %v", got, want)
	}
	// The other chat is whole.
	wantPath(t, a, second.ID, keep)
	if names, err := Files(a, second.ID); err != nil || !slices.Equal(names, []string{"in-both.png", "only-second.png"}) {
		t.Errorf("the other chat's files are %v %v", names, err)
	}
	if hits, err := Search(a, lim, "harbour"); err != nil || len(hits) != 2 || hits[0].ChatID != second.ID || hits[1].MessageID != keep.ID {
		t.Errorf("the other chat is found as %+v, %v", hits, err)
	}
	if chats, err := List(a, Filter{}); err != nil || len(chats) != 1 || chats[0].ID != second.ID {
		t.Errorf("the list is %+v %v", chats, err)
	}

	// Deleting the last chat that holds a file removes it. A file that is
	// already gone from the folder does not stop the delete.
	if err := a.Files().Remove("only-second.png"); err != nil {
		t.Fatal(err)
	}
	if err := Delete(a, second.ID); err != nil {
		t.Fatal(err)
	}
	if got := filesOnDisk(t, a); !slices.Equal(got, []string{"tied-to-nothing.txt"}) {
		t.Errorf("the files folder holds %v after the last delete", got)
	}
	for _, table := range []string{"chats", "messages", "chat_files", "chat_title_search", "message_search", "test_later_item"} {
		if n := count(t, a, "SELECT count(*) FROM "+table); n != 0 {
			t.Errorf("%s holds %d rows after every chat was deleted", table, n)
		}
	}
	// A message id is never used twice, even after a delete.
	fresh := newChat(t, a, "")
	if m := say(t, a, fresh.ID, 0, seam.RoleUser, "Invented."); m.ID <= keep.ID {
		t.Errorf("message id %d was used again", m.ID)
	}
}

func TestAFailedDeleteKeepsTheChat(t *testing.T) {
	a, _ := twoStores(t)
	lim := limits(t)
	// A folder that is not empty cannot be removed, on any system. It
	// stands in for a file that will not go.
	if err := a.Files().Mkdir("stuck", 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, a, "stuck/inside.txt")
	c := newChat(t, a, "Kite designs")
	m := say(t, a, c.ID, 0, seam.RoleUser, "A kite with an invented tail.")
	if err := AddFile(a, c.ID, "stuck"); err != nil {
		t.Fatal(err)
	}

	if err := Delete(a, c.ID); err == nil {
		t.Fatal("the delete passed with a file it could not remove")
	}
	// The chat is whole, so nothing is left with no chat that names it.
	wantPath(t, a, c.ID, m)
	if hits, err := Search(a, lim, "kite"); err != nil || len(hits) != 2 {
		t.Errorf("after a failed delete, kite finds %+v %v", hits, err)
	}
	if names, err := Files(a, c.ID); err != nil || !slices.Equal(names, []string{"stuck"}) {
		t.Errorf("after a failed delete, the chat's files are %v %v", names, err)
	}

	// Once the file can go, the same delete finishes.
	if err := a.Files().Remove("stuck/inside.txt"); err != nil {
		t.Fatal(err)
	}
	if err := Delete(a, c.ID); err != nil {
		t.Fatal(err)
	}
	if got := filesOnDisk(t, a); len(got) != 0 {
		t.Errorf("the files folder holds %v", got)
	}
	if _, err := Get(a, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the chat is still there: %v", err)
	}
}

func TestTwoTenantsNeverMix(t *testing.T) {
	a, b := twoStores(t)
	lim := limits(t)
	writeFile(t, a, "same-name.png")
	writeFile(t, b, "same-name.png")
	image := seam.Block{Type: seam.BlockImage, MediaType: "image/png", Ref: "same-name.png"}

	mine := newChat(t, a, "Lantern festival")
	mineMsg, err := Append(a, mine.ID, NewMessage{Role: seam.RoleUser, Blocks: []seam.Block{
		{Type: seam.BlockText, Text: "The lantern festival is in the first tenant."}, image}})
	if err != nil {
		t.Fatal(err)
	}
	theirs := newChat(t, b, "Lantern repair")
	theirsMsg, err := Append(b, theirs.ID, NewMessage{Role: seam.RoleUser, Blocks: []seam.Block{
		{Type: seam.BlockText, Text: "The lantern repair is in the second tenant."}, image}})
	if err != nil {
		t.Fatal(err)
	}

	// Each tenant lists and finds its own chat only.
	for _, c := range []struct {
		s    *tenant.Store
		chat Chat
		msg  Message
	}{{a, mine, mineMsg}, {b, theirs, theirsMsg}} {
		chats, err := List(c.s, Filter{})
		if err != nil || len(chats) != 1 || chats[0].ID != c.chat.ID {
			t.Errorf("a tenant lists %+v %v, want its own chat only", chats, err)
		}
		hits, err := Search(c.s, lim, "lantern")
		if err != nil || len(hits) != 2 {
			t.Fatalf("a tenant's search found %+v %v, want its own title and message", hits, err)
		}
		for _, h := range hits {
			if h.ChatID != c.chat.ID || (h.MessageID != 0 && h.MessageID != c.msg.ID) {
				t.Errorf("a tenant's search returned another tenant's chat: %+v", h)
			}
		}
	}
	if hits, err := Search(a, lim, "repair second"); err != nil || len(hits) != 0 {
		t.Errorf("the first tenant found the second's words: %+v %v", hits, err)
	}

	// One tenant's ids mean nothing in the other's store.
	other := map[string]error{}
	_, other["Get"] = Get(a, theirs.ID)
	_, other["Path"] = Path(a, theirs.ID)
	_, other["GetMessage"] = GetMessage(a, theirs.ID, theirsMsg.ID)
	_, other["Append"] = Append(a, theirs.ID, NewMessage{Role: seam.RoleUser, Blocks: text("Invented.")})
	_, other["Rename"] = Rename(a, lim, theirs.ID, "Invented")
	_, other["Star"] = Star(a, theirs.ID, true)
	other["Select"] = Select(a, theirs.ID, theirsMsg.ID)
	other["Delete"] = Delete(a, theirs.ID)
	for name, err := range other {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s with another tenant's chat id gave %v, want not found", name, err)
		}
	}

	// Deleting in one tenant leaves the other's chat and its file of the
	// same name alone.
	if err := Delete(a, mine.ID); err != nil {
		t.Fatal(err)
	}
	if got := filesOnDisk(t, a); len(got) != 0 {
		t.Errorf("the first tenant's folder still holds %v", got)
	}
	if got := filesOnDisk(t, b); !slices.Equal(got, []string{"same-name.png"}) {
		t.Errorf("the second tenant's folder holds %v", got)
	}
	wantPath(t, b, theirs.ID, theirsMsg)
	if hits, err := Search(b, lim, "lantern"); err != nil || len(hits) != 2 {
		t.Errorf("the second tenant's search changed: %+v %v", hits, err)
	}
}
