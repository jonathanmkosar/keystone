package imported

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Everything here is invented.

func stores(t *testing.T) (a, b *tenant.Store) {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	for _, into := range []**tenant.Store{&a, &b} {
		made, err := in.Create("Invented Person")
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		*into = s
	}
	return a, b
}

func TestTheRecordOfWhatCame(t *testing.T) {
	a, b := stores(t)
	ctx := context.Background()
	lim, err := chat.DefaultLimits()
	if err != nil {
		t.Fatal(err)
	}
	then := time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)
	at := time.Date(2026, 11, 26, 9, 30, 0, 0, time.UTC)
	if err := Begin(ctx, a.DB(), Run{ID: "00000000000000a1", Source: NewKeystone, Workspace: "Wren", FileSHA256: "abc", RequestID: "import-00000000000000a1", Started: at}); err != nil {
		t.Fatal(err)
	}
	var replyID int64
	made, err := chat.Import(a, lim, chat.ImportedChat{Title: "The orchard ladder", Messages: []chat.ImportedMessage{
		{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Where is the orchard ladder?"}}, At: then},
		{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: "In the long shed."}}, At: then.Add(time.Second)},
	}}, func(tx *sql.Tx, c chat.Chat, ids []int64) error {
		replyID = ids[1]
		if err := MarkChat(ctx, tx, c.ID, NewMark{Source: NewKeystone, ImportID: "00000000000000a1", Mark: Mark{
			Kind: ChatStretch, At: at, Turns: 1, First: then, Last: then.Add(time.Second),
			Models: []string{"harbour-local-12b"}, Steps: map[string]int{"search.completed": 2}, Note: "Two searches were recorded.",
		}}); err != nil {
			return err
		}
		if err := MarkMessage(ctx, tx, ids[0], "turn-1", ""); err != nil {
			return err
		}
		if err := MarkMessage(ctx, tx, ids[1], "turn-1", "harbour-local-12b"); err != nil {
			return err
		}
		return Note(ctx, tx, Came{Source: NewKeystone, Workspace: "Wren", Kind: KindTurn, SourceID: "turn-1", ImportID: "00000000000000a1", ChatID: c.ID, At: at})
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := chat.Create(a)
	if err != nil {
		t.Fatal(err)
	}

	marks, err := Marks(ctx, a, []string{made.ID, plain.ID})
	if err != nil || len(marks) != 1 {
		t.Fatalf("%d marks: %v", len(marks), err)
	}
	m := marks[made.ID]
	if m.From != "New Keystone" || m.Kind != ChatStretch || m.Turns != 1 || !m.At.Equal(at) || !m.First.Equal(then) || !m.Last.Equal(then.Add(time.Second)) ||
		len(m.Models) != 1 || m.Steps["search.completed"] != 2 || m.Note != "Two searches were recorded." {
		t.Errorf("the mark reads %+v", m)
	}
	if _, ok, err := MarkOf(ctx, a, plain.ID); ok || err != nil {
		t.Errorf("a chat made here has a mark (%v)", err)
	}
	if none, err := Marks(ctx, a, nil); err != nil || len(none) != 0 {
		t.Errorf("no chats gave %v, %v", none, err)
	}
	models, err := Models(ctx, a, made.ID)
	if err != nil || len(models) != 1 || models[replyID] != "harbour-local-12b" {
		t.Errorf("the models read %v: %v", models, err)
	}

	// A source id is noted once: the table refuses it a second time.
	if err := Note(ctx, a.DB(), Came{Source: NewKeystone, Workspace: "Wren", Kind: KindTurn, SourceID: "turn-1", ImportID: "00000000000000a2", ChatID: "another", At: at}); err == nil {
		t.Error("a source id was noted twice")
	}
	// The same id in another workspace, or of another kind, is another thing.
	for _, c := range []Came{
		{Source: NewKeystone, Workspace: "Marlow", Kind: KindTurn, SourceID: "turn-1", ImportID: "00000000000000a1", ChatID: made.ID, At: at},
		{Source: NewKeystone, Workspace: "Wren", Kind: KindRoutine, SourceID: "turn-1", ImportID: "00000000000000a1", ScheduleID: "a-schedule", At: at},
	} {
		if err := Note(ctx, a.DB(), c); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	seen, err := Seen(ctx, a, NewKeystone, "Wren", KindTurn)
	if err != nil || len(seen) != 1 || seen["turn-1"] != made.ID {
		t.Errorf("seen turns: %v, %v", seen, err)
	}
	if routines, _ := Seen(ctx, a, NewKeystone, "Wren", KindRoutine); routines["turn-1"] != "a-schedule" {
		t.Errorf("seen routines: %v", routines)
	}
	if err := End(ctx, a.DB(), "00000000000000a1", 1, 2, 1, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, err := Count(ctx, a, NewKeystone, "Wren"); err != nil || got != (Totals{Runs: 1, Turns: 1, Routines: 1, Chats: 1}) {
		t.Errorf("the totals are %+v: %v", got, err)
	}

	// The other tenant's record holds none of it.
	if seen, _ := Seen(ctx, b, NewKeystone, "Wren", KindTurn); len(seen) != 0 {
		t.Errorf("the other tenant has seen %v", seen)
	}
	if marks, _ := Marks(ctx, b, []string{made.ID}); len(marks) != 0 {
		t.Errorf("the other tenant reads the mark: %v", marks)
	}

	// The mark and the models go with the chat; the source id stays, so
	// what the person deleted does not come back.
	if err := chat.Delete(a, made.ID); err != nil {
		t.Fatal(err)
	}
	if marks, _ := Marks(ctx, a, []string{made.ID}); len(marks) != 0 {
		t.Errorf("a deleted chat keeps its mark: %v", marks)
	}
	var left int
	if err := a.DB().QueryRow("SELECT count(*) FROM imported_messages").Scan(&left); err != nil || left != 0 {
		t.Errorf("a deleted chat left %d rows of imported_messages (%v)", left, err)
	}
	if seen, _ := Seen(ctx, a, NewKeystone, "Wren", KindTurn); len(seen) != 1 {
		t.Errorf("deleting the chat forgot its source id: %v", seen)
	}
}
