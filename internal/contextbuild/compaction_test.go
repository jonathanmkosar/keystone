package contextbuild

import (
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The summary and pinned messages (P5-02).

// firstKept is the index of the oldest turn that was sent.
func firstKept(rec Record) int {
	for i, t := range rec.Turns {
		if t.Kept {
			return i
		}
	}
	return len(rec.Turns)
}

func TestTheSummaryStandsForTheTurnsItCovers(t *testing.T) {
	o, b := shipped(t)
	messages := chat(40, 300)
	turns := Turns(messages)
	in := Input{Model: model(3000, 500), Budget: b, Options: o, Messages: messages, Summary: "Earlier: " + filler(240)}

	// A summary that covers the first twenty turns is sent in their place.
	in.SummaryCovers = turns[20][0]
	req, rec := build(t, in)
	fits(t, in, req, rec)
	sum := rec.Part(PartSummary)
	if sum.State != StateKept || sum.Covers != 20 {
		t.Fatalf("the summary was not sent in place of twenty turns: %+v", sum)
	}
	for i, turn := range rec.Turns[:20] {
		if turn.Kept || !turn.Summarised {
			t.Fatalf("turn %d is covered by the summary and is %+v", i, turn)
		}
	}
	from := firstKept(rec)
	if from < 20 {
		t.Fatalf("turn %d was sent, and the summary covers it", from)
	}
	for i, turn := range rec.Turns[20:] {
		if turn.Summarised {
			t.Fatalf("turn %d is marked as summarised and the summary does not cover it", 20+i)
		}
	}
	if gotNote := rec.Part(PartNote).State == StateKept; gotNote != (from > 20) {
		t.Fatalf("turns %d to %d were left out with no summary for them, and the note is %s", 20, from, rec.Part(PartNote).State)
	}
	recent := rec.Part(PartRecent)
	if recent.Kept+recent.Dropped != len(rec.Turns)-1-20 {
		t.Fatalf("the earlier turns count the summarised ones: %+v", recent)
	}
	if rec.SummaryRoom <= 0 || rec.TurnRoom <= 0 || rec.TurnRoom < recent.Tokens {
		t.Fatalf("the record's rooms are wrong: turns %d in %d, summary room %d", recent.Tokens, rec.TurnRoom, rec.SummaryRoom)
	}

	// A summary that leaves only a few turns needs no note, and the call
	// runs on from the first message it does not cover.
	in.SummaryCovers = turns[36][0]
	req, rec = build(t, in)
	fits(t, in, req, rec)
	if rec.Part(PartSummary).Covers != 36 || rec.Part(PartNote).State != StateEmpty || firstKept(rec) != 36 {
		t.Fatalf("a summary that leaves four turns: summary %+v, note %+v, first sent %d", rec.Part(PartSummary), rec.Part(PartNote), firstKept(rec))
	}
	if seam.Text(req.Messages[0]) != seam.Text(messages[in.SummaryCovers]) {
		t.Fatalf("the call does not run on from the first message the summary does not cover: %.40q", seam.Text(req.Messages[0]))
	}

	// A summary that does not fit its share is not sent, and the turns it
	// covers are packed as if there were none, with the note.
	in.Summary = "Earlier: " + filler(9000)
	req, rec = build(t, in)
	fits(t, in, req, rec)
	without := in
	without.SummaryCovers = 0
	_, plain := build(t, without)
	if rec.Part(PartSummary).State != StateDropped || rec.Part(PartNote).State != StateKept || firstKept(rec) != firstKept(plain) {
		t.Fatalf("a summary too large for its share: summary %+v, note %+v, first sent %d, and %d with no summary", rec.Part(PartSummary), rec.Part(PartNote), firstKept(rec), firstKept(plain))
	}
	for i, turn := range rec.Turns {
		if turn.Summarised {
			t.Fatalf("turn %d is marked as summarised and the summary was not sent", i)
		}
	}

	// Shares that add up to more than the open room. Attachments fill it,
	// so the summary cannot be sent after all, and no turn is counted as
	// summarised.
	in.Summary = "Earlier: " + filler(240)
	in.SummaryCovers = turns[20][0]
	in.Options.AttachedPercent, in.Options.RecallPercent, in.Options.SummaryPercent = 100, 50, 50
	for i := 0; i < 80; i++ {
		in.Attached = append(in.Attached, Item{Label: "a small attachment", Text: filler(150)})
	}
	req, rec = build(t, in)
	fits(t, in, req, rec)
	if rec.Part(PartSummary).State == StateKept {
		t.Fatalf("the summary was sent with no room for it: %+v", rec.Part(PartSummary))
	}
	for i, turn := range rec.Turns {
		if turn.Summarised {
			t.Fatalf("turn %d is marked as summarised and the summary was not sent", i)
		}
	}
}

func TestPinnedMessagesAreNeverLeftOut(t *testing.T) {
	o, b := shipped(t)
	messages := chat(40, 300)
	turns := Turns(messages)
	pin := func(i int) Pin {
		return Pin{Message: i, Item: Item{Label: "a pinned message", Text: "Pinned: " + seam.Text(messages[i])}}
	}
	last := len(messages) - 1
	in := Input{
		Model: model(3000, 500), Budget: b, Options: o, Messages: messages,
		Pinned: []Pin{
			pin(0),                      // in the first turn, which is left out
			pin(turns[len(turns)-2][0]), // in the last earlier turn, which is sent
			pin(last),                   // in the current turn
			{Message: -1, Item: Item{Label: "from a summarised part", Text: "Pinned: the bakery opens at six."}},
		},
	}
	req, rec := build(t, in)
	fits(t, in, req, rec)
	got := rec.Part(PartPinned)
	if len(got.Items) != 4 {
		t.Fatalf("the record lists %d pins", len(got.Items))
	}
	want := []struct{ kept, inTurn bool }{{true, false}, {false, true}, {false, true}, {true, false}}
	for i, w := range want {
		if it := got.Items[i]; it.Kept != w.kept || it.InTurn != w.inTurn || !it.Pinned {
			t.Errorf("pin %d is %+v, want sent %v and in its turn %v", i, it, w.kept, w.inTurn)
		}
	}
	if got.State != StateKept || got.Kept != 2 {
		t.Fatalf("the pinned part is %+v", got)
	}

	// At every window, from one that holds only what is never left out to
	// one that holds everything, each pinned message is in the call once:
	// in its turn, or as its item, and never both.
	in.Pinned = in.Pinned[:2]
	for window := 900; window <= 20000; window += 300 {
		in.Model = model(window, 500)
		req, rec := build(t, in)
		if rec.Over > 0 {
			continue
		}
		fits(t, in, req, rec)
		for i, p := range in.Pinned {
			inTurn := 0
			for _, m := range req.Messages {
				if seam.Text(m) == seam.Text(messages[p.Message]) {
					inTurn++
				}
			}
			asItem := 0
			for _, bl := range req.System {
				if bl.Text == p.Text {
					asItem++
				}
			}
			if inTurn+asItem != 1 {
				t.Fatalf("window %d: pin %d is in the call %d times in its turn and %d as its item", window, i, inTurn, asItem)
			}
		}
	}

	// A pinned message in a turn the summary stands for is sent as its item.
	in.Model = model(3000, 500)
	in.Summary, in.SummaryCovers = "Earlier: "+filler(240), turns[20][0]
	in.Pinned = []Pin{pin(turns[5][0])}
	req, rec = build(t, in)
	fits(t, in, req, rec)
	if !rec.Part(PartPinned).Items[0].Kept || !strings.Contains(req.System[len(req.System)-1].Text+req.System[len(req.System)-2].Text, "Pinned: Question 6.") {
		t.Fatalf("a pin in a summarised turn was not sent: %+v", rec.Part(PartPinned))
	}

	// A pin too large for the window is still in the request, which is
	// then over, as a message too large for it is.
	in.Pinned = []Pin{{Message: -1, Item: Item{Label: "huge", Text: filler(20000)}}}
	req, rec = build(t, in)
	if rec.Over == 0 || !rec.Part(PartPinned).Items[0].Kept {
		t.Fatalf("a pin larger than the window was left out, or the request is not over: %+v", rec.Part(PartPinned))
	}
	_ = req
}
