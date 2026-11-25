package contextbuild

import (
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Items the person took out of a call from the turn inspector (P5-04).

// sent says whether a text is anywhere in the request.
func sent(req seam.Request, text string) bool {
	for _, b := range req.System {
		if strings.Contains(b.Text, text) {
			return true
		}
	}
	for _, m := range req.Messages {
		if strings.Contains(seam.Text(m), text) {
			return true
		}
	}
	return false
}

// A removed recalled item is not sent, takes no room, and is listed as
// removed with the tokens it would have taken. A pinned item removed is
// not sent either. Its name goes from the input to the record.
func TestARemovedItemIsNotSent(t *testing.T) {
	o, b := shipped(t)
	in := Input{Model: model(32000, 4000), Budget: b, Options: o, Messages: chat(4, 200),
		Recall: []Item{
			{Label: "garden: Runner beans (page 7)", Ref: "page:7", Text: "Runner beans go in the north bed. " + filler(300)},
			{Label: "garden: Sweet peas (page 9)", Ref: "page:9", Text: "Sweet peas climb the south fence. " + filler(300)},
			{Label: "a pinned source", Ref: "source:3", Text: "The ferry timetable for the winter. " + filler(300), Pinned: true},
		},
	}
	req, before := build(t, in)
	fits(t, in, req, before)
	if !sent(req, "north bed") || !sent(req, "south fence") || !sent(req, "ferry timetable") {
		t.Fatal("the items were not all sent before any was removed")
	}
	if got := before.Part(PartRecall).Items[0]; got.Ref != "page:7" || got.Removed {
		t.Fatalf("the record does not carry the item's name: %+v", got)
	}

	in.Recall[0].Removed, in.Recall[2].Removed = true, true
	req, rec := build(t, in)
	fits(t, in, req, rec)
	if sent(req, "north bed") || sent(req, "ferry timetable") || !sent(req, "south fence") {
		t.Fatal("a removed item was sent, or the one left was not")
	}
	part := rec.Part(PartRecall)
	if part.State != StatePartly || part.Kept != 1 || part.Removed != 2 || part.Dropped != 0 || part.DroppedTokens != 0 {
		t.Fatalf("the recall part is %+v", part)
	}
	for i, want := range []bool{true, false, true} {
		it := part.Items[i]
		if it.Removed != want || it.Kept == want || it.Tokens == 0 {
			t.Errorf("item %d is %+v, want removed %v", i, it, want)
		}
	}
	if got, was := part.Tokens, before.Part(PartRecall).Tokens; got >= was || rec.Tokens >= before.Tokens {
		t.Errorf("the removed items still take room: %d tokens of recall, %d before", got, was)
	}

	// Every item removed: the part was not sent.
	in.Recall[1].Removed = true
	req, rec = build(t, in)
	fits(t, in, req, rec)
	if part := rec.Part(PartRecall); part.State != StateDropped || part.Kept != 0 || part.Removed != 3 {
		t.Fatalf("with every item removed the recall part is %+v", part)
	}
}

// A pinned message removed is not sent on its own, and is held no room. One
// whose turn is sent is there in its turn, as before, and the record says
// so. The summary's name goes from the input to the record.
func TestARemovedPinIsNotSentOnItsOwn(t *testing.T) {
	o, b := shipped(t)
	messages := chat(40, 300)
	turns := Turns(messages)
	pin := func(i int, ref string) Pin {
		return Pin{Message: i, Item: Item{Label: "a pinned message", Ref: ref, Text: "Pinned: " + seam.Text(messages[i])}}
	}
	in := Input{
		Model: model(3000, 500), Budget: b, Options: o, Messages: messages,
		Summary: "Earlier: " + filler(200), SummaryRef: "summary:12",
		Pinned: []Pin{
			pin(0, "message:1"),                      // in the first turn, which is left out
			pin(turns[len(turns)-2][0], "message:2"), // in the last earlier turn, which is sent
		},
	}
	req, before := build(t, in)
	fits(t, in, req, before)
	if !before.Part(PartPinned).Items[0].Kept || !sent(req, "Pinned: Question 1.") {
		t.Fatalf("the first pin was not sent on its own before it was removed: %+v", before.Part(PartPinned))
	}
	if got := before.Part(PartSummary); got.Ref != "summary:12" {
		t.Fatalf("the summary part does not name the summary: %+v", got)
	}

	in.Pinned[0].Removed, in.Pinned[1].Removed = true, true
	req, rec := build(t, in)
	fits(t, in, req, rec)
	if sent(req, "Pinned: Question 1.") {
		t.Fatal("a removed pin was sent on its own")
	}
	part := rec.Part(PartPinned)
	if part.Kept != 0 || part.Removed != 2 || part.State != StateDropped {
		t.Fatalf("the pinned part is %+v", part)
	}
	if first, second := part.Items[0], part.Items[1]; !first.Removed || first.Kept || first.InTurn || first.Ref != "message:1" ||
		!second.Removed || second.Kept || !second.InTurn {
		t.Fatalf("the removed pins are recorded as %+v and %+v", first, second)
	}
	// The pin's room went to the earlier turns, so as many or more are sent.
	if after, was := firstKept(rec), firstKept(before); after > was {
		t.Errorf("with the pin removed the oldest turn sent is %d, and %d before", after, was)
	}

	// A removed pin in a turn that is sent leaves the part empty, since
	// nothing of it was left out.
	in.Pinned = in.Pinned[1:]
	_, rec = build(t, in)
	if part := rec.Part(PartPinned); part.State != StateEmpty || part.Removed != 1 || !part.Items[0].InTurn {
		t.Fatalf("a removed pin in a sent turn: %+v", part)
	}
}
