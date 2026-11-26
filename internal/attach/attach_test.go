package attach

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every chat and word here is invented.

func stores(t *testing.T) (one, two *tenant.Store) {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	open := func(name string) *tenant.Store {
		made, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	return open("Person One"), open("Person Two")
}

func newChat(t *testing.T, s *tenant.Store) string {
	t.Helper()
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func ids(links []Link) string {
	var out []string
	for _, l := range links {
		mark := ""
		if l.Handoff {
			mark = "^"
		}
		out = append(out, mark+l.AttachedID)
	}
	return strings.Join(out, ",")
}

func TestChatsAreAttachedListedAndTakenOff(t *testing.T) {
	s, _ := stores(t)
	ctx := context.Background()
	a, b, c, d := newChat(t, s), newChat(t, s), newChat(t, s), newChat(t, s)
	for _, x := range []string{b, c} {
		if added, err := Add(ctx, s, a, x, 2); err != nil || !added {
			t.Fatalf("attaching %s: %v %v", x, added, err)
		}
	}
	if added, err := Add(ctx, s, a, b, 2); err != nil || added {
		t.Fatalf("attaching a chat twice: %v %v", added, err)
	}
	if _, err := Add(ctx, s, a, d, 2); !errors.Is(err, ErrFull) {
		t.Fatalf("a third chat past a limit of two: %v", err)
	}
	if _, err := Add(ctx, s, a, a, 2); !errors.Is(err, ErrSelf) {
		t.Fatalf("a chat attached to itself: %v", err)
	}
	if _, err := Add(ctx, s, a, "0000000000000000", 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a chat that is not there: %v", err)
	}
	links, err := List(ctx, s, a)
	if err != nil || ids(links) != b+","+c {
		t.Fatalf("the list is %s (%v)", ids(links), err)
	}
	if is, err := Is(ctx, s, a, b); err != nil || !is {
		t.Fatalf("b is not attached to a: %v", err)
	}
	if is, _ := Is(ctx, s, b, a); is {
		t.Fatal("a link goes both ways")
	}
	if removed, err := Remove(ctx, s, a, b); err != nil || !removed {
		t.Fatalf("taking b off: %v %v", removed, err)
	}
	if removed, err := Remove(ctx, s, a, b); err != nil || removed {
		t.Fatalf("taking b off twice: %v %v", removed, err)
	}
	if links, _ := List(ctx, s, a); ids(links) != c {
		t.Fatalf("after b was taken off the list is %s", ids(links))
	}
	// Deleting either chat takes the link with it.
	if err := chat.Delete(s, c); err != nil {
		t.Fatal(err)
	}
	if links, _ := List(ctx, s, a); len(links) != 0 {
		t.Fatalf("a deleted chat is still attached: %s", ids(links))
	}
}

// Fixed rule 1: a chat of another tenant cannot be named, from either end.
func TestAnotherTenantsChatCannotBeAttached(t *testing.T) {
	one, two := stores(t)
	ctx := context.Background()
	mine, theirs := newChat(t, one), newChat(t, two)
	if _, err := Add(ctx, one, mine, theirs, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another tenant's chat was attached: %v", err)
	}
	if _, err := Add(ctx, two, mine, theirs, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a chat was attached to another tenant's chat: %v", err)
	}
	if err := Handoff(ctx, one, theirs, mine); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a chat was handed off from another tenant's chat: %v", err)
	}
	if _, err := one.DB().Exec("INSERT INTO chat_attachments(chat_id, attached_id, handoff, added_at) VALUES(?, ?, 0, 'x')", mine, theirs); err == nil {
		t.Fatal("the table took a link to a chat that is not in this tenant's record")
	}
}

func TestAHandoffIsAttachedForGood(t *testing.T) {
	s, _ := stores(t)
	ctx := context.Background()
	parent, child, other := newChat(t, s), newChat(t, s), newChat(t, s)
	if _, err := Add(ctx, s, child, other, 5); err != nil {
		t.Fatal(err)
	}
	if err := Handoff(ctx, s, parent, child); err != nil {
		t.Fatal(err)
	}
	if links, _ := List(ctx, s, child); ids(links) != "^"+parent+","+other {
		t.Fatalf("the child's list is %s, want its parent first", ids(links))
	}
	if kids, err := Handoffs(ctx, s, parent); err != nil || len(kids) != 1 || kids[0].ChatID != child || !kids[0].Handoff {
		t.Fatalf("the parent's handoffs are %+v (%v)", kids, err)
	}
	if kids, _ := Handoffs(ctx, s, other); len(kids) != 0 {
		t.Fatalf("an attached chat has handoffs: %+v", kids)
	}
	if _, err := Remove(ctx, s, child, parent); !errors.Is(err, ErrParent) {
		t.Fatalf("the parent was taken off: %v", err)
	}
	if err := Handoff(ctx, s, other, child); !errors.Is(err, ErrHandedOff) {
		t.Fatalf("a chat continues from two: %v", err)
	}
	if err := Handoff(ctx, s, child, child); !errors.Is(err, ErrSelf) {
		t.Fatalf("a chat handed off from itself: %v", err)
	}
}

func TestAnIncognitoChatIsOutsideIt(t *testing.T) {
	s, _ := stores(t)
	ctx := context.Background()
	a, quiet, b := newChat(t, s), newChat(t, s), newChat(t, s)
	if err := memory.SetIncognito(ctx, s, quiet, true); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(ctx, s, a, quiet, 5); !errors.Is(err, ErrIncognito) {
		t.Fatalf("an incognito chat was attached: %v", err)
	}
	if _, err := Add(ctx, s, quiet, a, 5); !errors.Is(err, ErrIncognito) {
		t.Fatalf("an incognito chat attached a chat: %v", err)
	}
	if err := Handoff(ctx, s, quiet, b); !errors.Is(err, ErrIncognito) {
		t.Fatalf("an incognito chat was handed off: %v", err)
	}
	// One that becomes incognito later is not shown.
	if _, err := Add(ctx, s, a, b, 5); err != nil {
		t.Fatal(err)
	}
	if err := memory.SetIncognito(ctx, s, b, true); err != nil {
		t.Fatal(err)
	}
	if got, err := MayShow(ctx, s, b, true); err != nil || got.Show || !got.Incognito {
		t.Fatalf("an incognito attached chat: %+v (%v)", got, err)
	}
}

// Fixed rule 3: a held chat, or one whose recap is marked, is shown only to
// a call whose every model may read sensitive content.
func TestASensitiveChatIsShownOnlyWhereItMayBeRead(t *testing.T) {
	s, _ := stores(t)
	ctx := context.Background()
	plain, held, marked := newChat(t, s), newChat(t, s), newChat(t, s)
	if _, err := s.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES('turn-held', ?, 'done', 'req-held', 'person', '{}', '2026-11-25T10:00:00.000Z')", held); err != nil {
		t.Fatal(err)
	}
	if err := recall.Hold(ctx, s, "turn-held", held); err != nil {
		t.Fatal(err)
	}
	if _, err := recap.Save(ctx, s, recap.Recap{ChatID: marked, Summary: "A quiet note.", Model: "local-one", Sensitive: true}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id        string
		mayRead   bool
		show, sen bool
	}{
		{plain, false, true, false},
		{plain, true, true, false},
		{held, false, false, true},
		{held, true, true, true},
		{marked, false, false, true},
		{marked, true, true, true},
	} {
		got, err := MayShow(ctx, s, c.id, c.mayRead)
		if err != nil || got.Show != c.show || got.Sensitive != c.sen || got.Incognito {
			t.Errorf("chat %s for a call that may read sensitive content %v: %+v (%v)", c.id, c.mayRead, got, err)
		}
	}
}

func TestTheTextACallCarries(t *testing.T) {
	when := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	rc := &recap.Recap{Summary: "Planning a walk to the old lighthouse.", Topics: []string{"the old lighthouse"}, Decisions: []string{"Go on Saturday."}}
	label, text := Text(Card{ID: "aaaa", Title: "Lighthouse", Active: when, Recap: rc, Tool: true})
	for _, want := range []string{`attached their chat "Lighthouse" (chat aaaa, last message November 20, 2026)`, "Summary: Planning a walk", "Topics: the old lighthouse", "- Go on Saturday.", ToolName + `, chat_id "aaaa"`} {
		if !strings.Contains(text, want) {
			t.Errorf("the attached chat's text does not hold %q:\n%s", want, text)
		}
	}
	if label != `attached chat "Lighthouse"` {
		t.Errorf("the label is %q", label)
	}
	label, text = Text(Card{ID: "bbbb", Active: when, Parent: true})
	if label != `continued from "(no title)"` || !strings.Contains(text, "continues from an earlier chat") || !strings.Contains(text, "It has no summary yet.") || strings.Contains(text, ToolName) {
		t.Errorf("the parent with no recap and no tool: %q\n%s", label, text)
	}
	if _, text = Text(Card{ID: "cccc", Active: when, Empty: true, Tool: true, Recap: rc}); !strings.Contains(text, "It has no messages yet.") || strings.Contains(text, "Summary:") || strings.Contains(text, ToolName) {
		t.Errorf("an empty chat:\n%s", text)
	}
	if !strings.Contains(LeftOut(1), "1 chat attached") || !strings.Contains(LeftOut(3), "3 chats attached") {
		t.Error("the line for chats left out does not give their number")
	}
}

func say(role seam.Role, text string) seam.Message {
	return seam.Message{Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}}
}

func TestAChatIsReadAPageAtATime(t *testing.T) {
	o := Options{Max: 5, PageMessages: 3, PageMaxChars: 500}
	var msgs []seam.Message
	for i := 1; i <= 7; i++ {
		msgs = append(msgs, say(seam.RoleUser, fmt.Sprintf("Question %d.", i)))
	}
	// A tool's result and thinking are left out; a tool call is named.
	msgs = append(msgs,
		seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockThinking, Text: "private"}, {Type: seam.BlockToolCall, ID: "c1", Name: "web_fetch", Input: []byte(`{}`)}}},
		seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockToolResult, ID: "c1", Content: []seam.Block{{Type: seam.BlockText, Text: "outside words"}}}}},
	)
	one, err := Page("aaaa", "Questions", msgs, 1, o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(one, "Page 1 of 3: messages 1 to 3 of 8") || !strings.Contains(one, "The person: Question 3.") || strings.Contains(one, "Question 4.") || !strings.Contains(one, "Page 2 holds") {
		t.Fatalf("page 1:\n%s", one)
	}
	three, err := Page("aaaa", "Questions", msgs, 3, o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(three, "messages 7 to 8 of 8") || !strings.Contains(three, "The reply: [used the tool web_fetch]") || strings.Contains(three, "outside words") || strings.Contains(three, "private") || strings.Contains(three, "holds the messages after") {
		t.Fatalf("page 3:\n%s", three)
	}
	if _, err := Page("aaaa", "Questions", msgs, 4, o); !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "has 3 pages") {
		t.Fatalf("a page past the end: %v", err)
	}
	if _, err := Page("aaaa", "Questions", msgs, 0, o); !errors.Is(err, ErrInput) {
		t.Fatalf("page 0: %v", err)
	}
	// The size limit makes pages too, and cuts a message larger than one.
	long := []seam.Message{say(seam.RoleUser, strings.Repeat("a", 300)), say(seam.RoleAssistant, strings.Repeat("b", 300)), say(seam.RoleUser, strings.Repeat("c", 2000))}
	first, _ := Page("bbbb", "Long", long, 1, o)
	if !strings.Contains(first, "Page 1 of 3") {
		t.Fatalf("pages by size:\n%s", first)
	}
	last, _ := Page("bbbb", "Long", long, 3, o)
	if !strings.Contains(last, "[cut short]") || strings.Count(last, "c") > 500 {
		t.Fatalf("a message larger than a page is not cut:\n%s", last)
	}
	if empty, err := Page("cccc", "", nil, 1, o); err != nil || !strings.Contains(empty, "It has no messages.") {
		t.Fatalf("an empty chat: %q %v", empty, err)
	}
}

func TestCheckOptionsNamesTheSetting(t *testing.T) {
	good := Options{Max: 5, PageMessages: 40, PageMaxChars: 12000}
	if err := CheckOptions(good); err != nil {
		t.Fatal(err)
	}
	for key, o := range map[string]Options{
		SettingMax:          {Max: 0, PageMessages: 40, PageMaxChars: 12000},
		SettingPageMessages: {Max: 5, PageMessages: 0, PageMaxChars: 12000},
		SettingPageMaxChars: {Max: 5, PageMessages: 40, PageMaxChars: 100},
	} {
		if err := CheckOptions(o); !errors.Is(err, ErrOptions) || !strings.Contains(err.Error(), key) {
			t.Errorf("%s out of range: %v", key, err)
		}
	}
}
