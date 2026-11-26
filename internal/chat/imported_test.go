package chat

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

func words(role seam.Role, text string, at time.Time) ImportedMessage {
	return ImportedMessage{Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}, At: at}
}

// An imported chat is written whole, with the times it was given, takes
// its true place in the list, reads as one branch, is found by the search,
// and goes like any other chat.
func TestAnImportedChatKeepsItsTimes(t *testing.T) {
	a, b := twoStores(t)
	lim := limits(t)
	today := newChat(t, a, "Made today")
	then := time.Date(2026, 10, 3, 8, 15, 0, 123e6, time.UTC)
	var seen []int64
	old, err := Import(a, lim, ImportedChat{Title: "  The   orchard\nladder ", Messages: []ImportedMessage{
		words(seam.RoleUser, "Where is the orchard ladder kept?", then),
		words(seam.RoleAssistant, "In the long shed, behind the wheelbarrow.", then.Add(4*time.Second)),
		words(seam.RoleUser, "And the pruning saw?", then.Add(time.Minute)),
	}}, func(tx *sql.Tx, c Chat, ids []int64) error {
		seen = ids
		// What the caller writes is in the chat's own transaction.
		_, err := tx.Exec("INSERT INTO chat_files(chat_id, name) VALUES(?, ?)", c.ID, "caller-wrote-this")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if old.Title != "The orchard ladder" || !old.Created.Equal(then) || !old.Active.Equal(then.Add(time.Minute)) {
		t.Errorf("the imported chat is %+v", old)
	}
	path, err := Path(a, old.ID)
	if err != nil || len(path) != 3 || len(seen) != 3 {
		t.Fatalf("its branch has %d messages, and the caller was given %d ids: %v", len(path), len(seen), err)
	}
	for i, m := range path {
		if m.ID != seen[i] || len(m.Siblings) != 1 {
			t.Errorf("message %d is %d with siblings %v; the caller was told %d", i+1, m.ID, m.Siblings, seen[i])
		}
	}
	if path[0].ParentID != 0 || path[1].ParentID != path[0].ID || path[2].ParentID != path[1].ID {
		t.Errorf("the messages are not one branch: %+v", path)
	}
	if !path[1].Created.Equal(then.Add(4*time.Second)) || path[1].Role != seam.RoleAssistant {
		t.Errorf("the reply is %s at %s", path[1].Role, path[1].Created)
	}
	// Older than today's chat, so after it in the list.
	list, err := List(a, Filter{})
	if err != nil || len(list) != 2 || list[0].ID != today.ID || list[1].ID != old.ID {
		t.Errorf("the list is %+v: %v", list, err)
	}
	for query, want := range map[string]int64{"orchard ladder": 0, "wheelbarrow": path[1].ID, "pruning": path[2].ID} {
		hits, err := Search(a, lim, query)
		if err != nil || len(hits) == 0 || hits[0].ChatID != old.ID || hits[0].MessageID != want {
			t.Errorf("the search for %q found %+v: %v", query, hits, err)
		}
	}
	if hits, _ := Search(b, lim, "wheelbarrow"); len(hits) != 0 {
		t.Errorf("another tenant's search found it: %+v", hits)
	}
	if files, _ := Files(a, old.ID); len(files) != 1 {
		t.Errorf("what the caller wrote in the transaction is not there: %v", files)
	}
	// A message appended later follows the imported ones, at today's time.
	next, err := Append(a, old.ID, NewMessage{ParentID: path[2].ID, Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: "On the nail by the door."}}})
	if err != nil || next.ID <= path[2].ID {
		t.Fatalf("appending to an imported chat: %+v %v", next, err)
	}
	if path, _ := Path(a, old.ID); len(path) != 4 {
		t.Errorf("after an append the branch has %d messages", len(path))
	}
	if err := Delete(a, old.ID); err != nil {
		t.Fatal(err)
	}
	if hits, _ := Search(a, lim, "wheelbarrow"); len(hits) != 0 {
		t.Errorf("a deleted imported chat is still found: %+v", hits)
	}
}

// What cannot be a chat is refused, and a caller's failure leaves nothing.
func TestAnImportThatFailsLeavesNothing(t *testing.T) {
	a, _ := twoStores(t)
	lim := limits(t)
	at := time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)
	one := []ImportedMessage{words(seam.RoleUser, "A line.", at)}
	for name, in := range map[string]ImportedChat{
		"no title":       {Title: "  ", Messages: one},
		"no message":     {Title: "Empty"},
		"no time":        {Title: "Timeless", Messages: []ImportedMessage{words(seam.RoleUser, "A line.", time.Time{})}},
		"no words":       {Title: "Wordless", Messages: []ImportedMessage{{Role: seam.RoleUser, At: at}}},
		"a file":         {Title: "With a file", Messages: []ImportedMessage{{Role: seam.RoleUser, At: at, Blocks: []seam.Block{{Type: seam.BlockFile, Ref: "abc", Name: "a.txt", MediaType: "text/plain"}}}}},
		"a made-up role": {Title: "Odd role", Messages: []ImportedMessage{words("system", "A line.", at)}},
	} {
		if _, err := Import(a, lim, in, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	broke := errors.New("the caller's own write failed")
	if _, err := Import(a, lim, ImportedChat{Title: "Half written", Messages: one}, func(*sql.Tx, Chat, []int64) error { return broke }); !errors.Is(err, broke) {
		t.Errorf("the caller's failure was lost: %v", err)
	}
	for _, table := range []string{"chats", "messages", "chat_title_search", "message_search"} {
		var n int
		if err := a.DB().QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s holds %d rows after imports that failed (%v)", table, n, err)
		}
	}
}
