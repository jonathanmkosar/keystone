package waymarks

import (
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
)

// P6-07 with Waymarks (decision 0371): Waymarks keeps every source and only
// its owner erases one, so forgetting a fact the memory pass added
// withdraws its source in Keystone and adds the other facts back under the
// intake token. Keystone never reads or finds the old source again; the
// instance still holds it.
func TestForgettingInWaymarksWithdrawsTheSource(t *testing.T) {
	f := newFake(t)
	ts := setUp(t, f, readerToken, intakeToken)
	ks := openStore(t, ts)
	if ks.Capabilities().RemoveSource {
		t.Fatal("the Waymarks adapter claims RemoveSource")
	}

	// A turn whose memory pass added two facts, as the turn's worker would.
	c, err := chat.Create(ts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES('turn-1', ?, 'done', 'req', 'person', '{}', '2026-11-25T09:00:00.000Z')", c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Queue(bg, ts, "turn-1", c.ID); err != nil {
		t.Fatal(err)
	}
	p, _, err := memory.Claim(bg, ts, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	o := memory.Options{Space: "memory", SourceName: "Facts from the chat \"{title}\", {date}"}
	x := memory.Exchange{ChatID: c.ID, TurnID: "turn-1", Title: "Bees"}
	facts := []knowledge.Fact{{Text: "The person keeps two beehives."}, {Text: "The person bakes rye bread on Saturdays."}}
	src, err := memory.Add(bg, ks, o, x, facts, "memory", false, true)
	if err != nil {
		t.Fatal(err)
	}
	p.Status, p.Space, p.SourceID, p.Facts = memory.StatusDone, src.Space, src.ID, []string{facts[0].Text, facts[1].Text}
	if err := memory.Finish(bg, ts, p); err != nil {
		t.Fatal(err)
	}

	ch, err := memory.Apply(bg, ts, ks, Name, memory.Edit{SourceID: src.ID, Fact: facts[0].Text}, 300)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Removed || ch.NewSourceID == "" || len(ch.Facts) != 1 {
		t.Errorf("the change is %+v", ch)
	}
	if _, err := ks.ReadSource(bg, src.ID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("the withdrawn source reads with error %v, want ErrNotFound", err)
	}
	hits, err := ks.Search(bg, knowledge.Query{Text: "beehives"})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.ID == src.ID && h.Type == knowledge.HitSource {
			t.Errorf("a search still finds the withdrawn source: %+v", h)
		}
	}
	kept, err := ks.ReadSource(bg, ch.NewSourceID)
	if err != nil || !strings.Contains(kept.Text, "rye bread") || strings.Contains(kept.Text, "beehives") {
		t.Errorf("the kept facts' source reads %+v, %v", kept, err)
	}
	// The instance itself still holds the old source: only its owner can
	// erase it.
	if _, err := knowledge.Unwrap(ks).ReadSource(bg, src.ID); err != nil {
		t.Errorf("the fake Waymarks lost the source, which no call Keystone makes can do: %v", err)
	}
}
