package inspector

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// store is a throwaway tenant with one chat.
func store(t *testing.T) (*tenant.Store, string) {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Skip Person")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	return s, c.ID
}

func names(list []Skip) []string {
	var out []string
	for _, sk := range list {
		out = append(out, sk.Name())
	}
	return out
}

// A skip waits for the chat's next turn, is claimed by that turn alone,
// and is read by every call of it. Taking the same item out twice keeps
// one, and one can be taken back before the turn.
func TestASkipIsUsedByOneTurn(t *testing.T) {
	s, chatID := store(t)
	other, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, sk := range []Skip{
		{Kind: KindPage, Ref: "7", Label: "garden: Runner beans (page 7)"},
		{Kind: KindPage, Ref: "7", Label: "the same page again"},
		{Kind: KindMessage, Ref: "42", Label: "pinned message 42"},
		{Kind: KindSource, Ref: "src-3", Label: "a source"},
	} {
		if err := Add(ctx, s, chatID, sk); err != nil {
			t.Fatal(err)
		}
	}
	if err := Add(ctx, s, other.ID, Skip{Kind: KindPage, Ref: "7"}); err != nil {
		t.Fatal(err)
	}
	waiting, err := Waiting(ctx, s, chatID)
	if err != nil || len(waiting) != 3 || waiting[0].Label != "garden: Runner beans (page 7)" || waiting[0].Asked.IsZero() || waiting[0].TurnID != "" {
		t.Fatalf("the waiting skips are %+v (%v)", waiting, err)
	}

	// Taken back before the turn.
	if err := Remove(ctx, s, chatID, KindSource, "src-3"); err != nil {
		t.Fatal(err)
	}

	// The next turn claims them; its later calls read the same.
	claimed, err := Claim(ctx, s, chatID, "turn-one")
	if err != nil || len(claimed) != 2 || claimed[0].Name() != "page:7" || claimed[1].Name() != "message:42" || claimed[0].TurnID != "turn-one" {
		t.Fatalf("the first turn claimed %v (%v)", names(claimed), err)
	}
	again, err := OfTurn(ctx, s, "turn-one")
	if err != nil || len(again) != 2 {
		t.Fatalf("the first turn's later calls read %v (%v)", names(again), err)
	}
	// Claiming twice in one turn changes nothing.
	if again, err = Claim(ctx, s, chatID, "turn-one"); err != nil || len(again) != 2 {
		t.Fatalf("claimed again: %v (%v)", names(again), err)
	}

	// The turn after finds nothing waiting, and nothing of the first turn.
	if next, err := Claim(ctx, s, chatID, "turn-two"); err != nil || len(next) != 0 {
		t.Fatalf("the second turn claimed %v (%v)", names(next), err)
	}
	if waiting, err := Waiting(ctx, s, chatID); err != nil || len(waiting) != 0 {
		t.Fatalf("after a turn, %v still wait (%v)", names(waiting), err)
	}
	// A used skip cannot be taken back, and the item can be taken out again.
	if err := Remove(ctx, s, chatID, KindPage, "7"); err != nil {
		t.Fatal(err)
	}
	if used, _ := OfTurn(ctx, s, "turn-one"); len(used) != 2 {
		t.Fatalf("taking back a used skip changed the turn's: %v", names(used))
	}
	if err := Add(ctx, s, chatID, Skip{Kind: KindPage, Ref: "7"}); err != nil {
		t.Fatal(err)
	}
	if waiting, _ := Waiting(ctx, s, chatID); len(waiting) != 1 {
		t.Fatalf("the page taken out again: %v", names(waiting))
	}

	// The other chat's skip is its own.
	if waiting, _ := Waiting(ctx, s, other.ID); len(waiting) != 1 {
		t.Fatalf("the other chat's skips are %v", names(waiting))
	}

	// They go with their chat.
	if err := chat.Delete(s, chatID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB().QueryRow("SELECT count(*) FROM chat_skips WHERE chat_id = ?", chatID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d skips are left of a deleted chat (%v)", n, err)
	}
}

// A skip of an unknown kind, or with an id not of its shape, is refused.
func TestASkipIsChecked(t *testing.T) {
	s, chatID := store(t)
	ctx := context.Background()
	for _, sk := range []Skip{
		{Kind: "turn", Ref: "7"},
		{Kind: KindPage, Ref: ""},
		{Kind: KindPage, Ref: "has a space"},
		{Kind: KindPage, Ref: "line\nbreak"},
		{Kind: KindMessage, Ref: "abc"},
		{Kind: KindMessage, Ref: "0"},
		{Kind: KindMessage, Ref: "007"},
		{Kind: KindSource, Ref: strings.Repeat("a", 201)},
	} {
		if err := Add(ctx, s, chatID, sk); !errors.Is(err, ErrInvalid) {
			t.Errorf("the skip %q %q was not refused: %v", sk.Kind, sk.Ref, err)
		}
	}
	if err := Add(ctx, s, "no-such-chat", Skip{Kind: KindPage, Ref: "7"}); err == nil {
		t.Error("a skip was kept for a chat that is not there")
	}
	if _, err := Claim(ctx, s, chatID, ""); err == nil {
		t.Error("a skip was claimed by no turn")
	}
}
