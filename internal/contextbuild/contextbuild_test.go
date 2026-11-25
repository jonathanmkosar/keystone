package contextbuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/settings"
)

// shipped returns the options and the budget options the settings ship
// with.
func shipped(t *testing.T) (Options, budget.Options) {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	o, err := FromSettings(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := budget.FromSettings(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return o, b
}

func model(window, output int) models.Model {
	return models.Model{Name: "invented", ContextWindow: window, OutputLimit: output}
}

// filler is invented text of n characters.
func filler(n int) string {
	const words = "the ferry leaves the north pier at seven and the bakery opens before it "
	return strings.Repeat(words, n/len(words)+1)[:n]
}

func say(role seam.Role, text string) seam.Message {
	return seam.Message{Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}}
}

// chat is a branch of n turns of about chars characters each. Every third
// turn uses a tool: the reply asks for it, the result comes back in a
// message of the person's side, and a second reply follows.
func chat(n, chars int) []seam.Message {
	var out []seam.Message
	for i := 1; i <= n; i++ {
		out = append(out, say(seam.RoleUser, fmt.Sprintf("Question %d. %s", i, filler(chars/2))))
		if i%3 == 0 {
			id := fmt.Sprintf("call-%d", i)
			out = append(out,
				seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{
					{Type: seam.BlockText, Text: "I will look that up."},
					{Type: seam.BlockToolCall, ID: id, Name: "timetable", Input: json.RawMessage(`{"pier":"north"}`)},
				}},
				seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{
					{Type: seam.BlockToolResult, ID: id, Content: []seam.Block{{Type: seam.BlockText, Text: "Seven, and then every hour."}}},
				}},
			)
		}
		out = append(out, say(seam.RoleAssistant, fmt.Sprintf("Answer %d. %s", i, filler(chars/2))))
	}
	// The current message, with no reply yet.
	return append(out, say(seam.RoleUser, "And what about Sunday?"))
}

func build(t *testing.T, in Input) (seam.Request, Record) {
	t.Helper()
	req, rec, err := Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := seam.ValidateRequest(req); err != nil {
		t.Fatalf("the request is not one a provider takes: %v", err)
	}
	if len(rec.Parts) != len(Parts) {
		t.Fatalf("the record has %d parts, not %d", len(rec.Parts), len(Parts))
	}
	for i, p := range Parts {
		if rec.Parts[i].Part != p {
			t.Fatalf("part %d of the record is %s, not %s", i, rec.Parts[i].Part, p)
		}
	}
	// The record must be the request: what the record says was sent is
	// put together again and compared.
	if again := rebuild(in, rec); !reflect.DeepEqual(again, req) {
		t.Fatalf("the record does not describe the request:\nrecord:  %+v\nrequest: %+v", again, req)
	}
	return req, rec
}

// rebuild makes the request a record describes, from the input alone.
func rebuild(in Input, rec Record) seam.Request {
	req := seam.Request{Model: in.Model.Name, Tools: in.Tools, MaxOutputTokens: in.MaxOutputTokens, ThinkingTokens: in.ThinkingTokens}
	text := map[Part]string{
		PartRules: in.Options.SystemText, PartProfile: in.Profile, PartPersonMap: in.PersonMap,
		PartProject: in.Project, PartStyle: in.Style, PartSummary: in.Summary, PartNote: in.Options.DroppedNote,
	}
	items := map[Part][]Item{PartAttached: in.Attached, PartRecall: in.Recall}
	for _, p := range in.Pinned {
		if p.Text != "" {
			items[PartPinned] = append(items[PartPinned], p.Item)
		}
	}
	if items[PartPinned] == nil {
		items[PartPinned] = []Item{}
	}
	for _, p := range rec.Parts {
		if p.Place != PlaceSystem {
			continue
		}
		if list, ok := items[p.Part]; ok {
			for i, it := range p.Items {
				if it.Kept {
					req.System = append(req.System, seam.Block{Type: seam.BlockText, Text: list[i].Text})
				}
			}
		} else if p.Kept == 1 {
			req.System = append(req.System, seam.Block{Type: seam.BlockText, Text: text[p.Part]})
		}
	}
	for _, turn := range rec.Turns {
		if turn.Kept {
			req.Messages = append(req.Messages, in.Messages[turn.First:turn.First+turn.Messages]...)
		}
	}
	return req
}

func fits(t *testing.T, in Input, req seam.Request, rec Record) {
	t.Helper()
	if rec.Over != 0 {
		t.Fatalf("the record says the request is %d tokens over", rec.Over)
	}
	fit, err := budget.Check(context.Background(), in.Model, in.Provider, req, in.Budget)
	if err != nil {
		t.Fatalf("the budget refuses the packed request: %v", err)
	}
	if fit.Input.Tokens != rec.Tokens || fit.Input.Tokens > rec.Limit {
		t.Fatalf("the budget counts %d tokens, the record says %d, and the limit is %d", fit.Input.Tokens, rec.Tokens, rec.Limit)
	}
}

func keptTurns(rec Record) (kept, dropped int) {
	for _, turn := range rec.Turns {
		if turn.Kept {
			kept++
		} else {
			dropped++
		}
	}
	return kept, dropped
}

func TestAChatThatFitsIsSentWhole(t *testing.T) {
	o, b := shipped(t)
	in := Input{Model: model(32000, 4000), Budget: b, Options: o, Messages: chat(6, 200), MaxOutputTokens: 1000, ThinkingTokens: 500}
	req, rec := build(t, in)
	fits(t, in, req, rec)
	if !reflect.DeepEqual(req.Messages, in.Messages) {
		t.Fatal("a chat that fits was not sent whole")
	}
	if len(req.System) != 1 || req.System[0].Text != o.SystemText {
		t.Fatalf("the system text is not the setting alone: %+v", req.System)
	}
	if req.MaxOutputTokens != 1000 || req.ThinkingTokens != 500 || req.Model != "invented" {
		t.Fatalf("the request's own fields were not passed on: %+v", req)
	}
	if rec.Output != 1000 || rec.Room != 31000 || rec.Limit != 31000 || rec.Window != 32000 {
		t.Fatalf("the record's room is wrong: %+v", rec)
	}
	if kept, dropped := keptTurns(rec); kept != 7 || dropped != 0 {
		t.Fatalf("%d turns kept and %d dropped, not 7 and 0", kept, dropped)
	}
	want := map[Part]string{PartRules: StateKept, PartCurrent: StateKept, PartRecent: StateKept}
	for _, p := range rec.Parts {
		state := want[p.Part]
		if state == "" {
			state = StateEmpty
		}
		if p.State != state {
			t.Errorf("part %s is %s, not %s", p.Part, p.State, state)
		}
	}
}

func TestTheOldestTurnsAreLeftOutWhole(t *testing.T) {
	o, b := shipped(t)
	in := Input{Model: model(3000, 500), Budget: b, Options: o, Messages: chat(60, 300)}
	req, rec := build(t, in)
	fits(t, in, req, rec)
	kept, dropped := keptTurns(rec)
	if kept < 2 || dropped == 0 {
		t.Fatalf("%d turns kept and %d dropped: the chat was meant to be too long, and not by that much", kept, dropped)
	}
	// What is kept is the end of the branch, with no gap in it.
	seenKept := false
	for _, turn := range rec.Turns {
		if seenKept && !turn.Kept {
			t.Fatal("a turn was left out after one that was kept")
		}
		seenKept = seenKept || turn.Kept
	}
	if last := rec.Turns[len(rec.Turns)-1]; !last.Kept || last.Part != PartCurrent {
		t.Fatalf("the current turn is not kept: %+v", last)
	}
	if req.Messages[0].Role != seam.RoleUser || !strings.HasPrefix(req.Messages[0].Blocks[0].Text, "Question ") {
		t.Fatalf("the request does not begin at the start of a turn: %+v", req.Messages[0])
	}
	if got := req.Messages[len(req.Messages)-1]; !reflect.DeepEqual(got, in.Messages[len(in.Messages)-1]) {
		t.Fatal("the request does not end with the current message")
	}
	// The next older turn would not have fitted.
	recent := rec.Part(PartRecent)
	if recent.State != StatePartly || recent.Dropped != dropped || recent.DroppedTokens == 0 {
		t.Fatalf("the record of the earlier turns is wrong: %+v", recent)
	}
	// The model is told, in the last system block.
	if note := rec.Part(PartNote); note.State != StateKept || req.System[len(req.System)-1].Text != o.DroppedNote {
		t.Fatalf("the model was not told that turns were left out: %+v", note)
	}
	// With no note, one sentence less is sent and nothing else changes.
	in.Options.DroppedNote = ""
	req, rec = build(t, in)
	if len(req.System) != 1 || rec.Part(PartNote).State != StateEmpty {
		t.Fatalf("an empty note was sent: %+v", req.System)
	}
}

func TestAToolCallIsNeverPartedFromItsResult(t *testing.T) {
	o, b := shipped(t)
	msgs := chat(30, 240)
	// Every window from one that holds almost nothing to one that holds
	// it all: build validates each request, and a request with a result
	// and no call, or a call and no result after it, is not valid.
	for window := 700; window < 9000; window += 37 {
		in := Input{Model: model(window, 400), Budget: b, Options: o, Messages: msgs}
		req, rec := build(t, in)
		fits(t, in, req, rec)
		calls, results := 0, 0
		for _, m := range req.Messages {
			for _, bl := range m.Blocks {
				switch bl.Type {
				case seam.BlockToolCall:
					calls++
				case seam.BlockToolResult:
					results++
				}
			}
		}
		if calls != results {
			t.Fatalf("window %d: %d tool calls and %d results", window, calls, results)
		}
	}
	// A turn is cut only where no call is waiting.
	turns := Turns(msgs)
	if len(turns) != 31 {
		t.Fatalf("the branch was cut into %d turns, not 31", len(turns))
	}
	for i, turn := range turns[:30] {
		want := 2
		if (i+1)%3 == 0 {
			want = 4
		}
		if turn[1]-turn[0] != want {
			t.Fatalf("turn %d has %d messages, not %d", i+1, turn[1]-turn[0], want)
		}
	}
}

func TestTheCurrentTurnAndTheStartAreNeverLeftOut(t *testing.T) {
	o, b := shipped(t)
	msgs := chat(4, 200)
	msgs[len(msgs)-1] = say(seam.RoleUser, filler(9000)) // about 3000 tokens
	in := Input{
		Model: model(2000, 200), Budget: b, Options: o, Messages: msgs,
		Profile: "The person keeps a bakery by the north pier.", Project: "Write for the bakery's newsletter.",
		Attached: []Item{{Label: "an attached chat", Text: filler(300)}},
		Recall:   []Item{{Label: "a recalled note", Text: filler(300)}},
		Summary:  filler(300),
	}
	req, rec := build(t, in)
	if rec.Over <= 0 {
		t.Fatalf("a current message larger than the window was said to fit: %+v", rec)
	}
	if len(req.Messages) != 1 || !reflect.DeepEqual(req.Messages[0], msgs[len(msgs)-1]) {
		t.Fatalf("the request does not hold the current message alone: %d messages", len(req.Messages))
	}
	if len(req.System) != 3 || req.System[0].Text != o.SystemText || req.System[1].Text != in.Profile || req.System[2].Text != in.Project {
		t.Fatalf("the standing start is not whole: %+v", req.System)
	}
	for _, p := range []Part{PartAttached, PartRecent, PartRecall, PartSummary, PartNote} {
		if got := rec.Part(p); got.State != StateDropped {
			t.Errorf("part %s is %s, not dropped", p, got.State)
		}
	}
	// The budget refuses it, as it refuses any call that cannot fit.
	_, err := budget.Check(context.Background(), in.Model, nil, req, b)
	var big *budget.TooLarge
	if !errors.As(err, &big) || big.Over != rec.Tokens-rec.Room {
		t.Fatalf("the budget did not refuse it by the number the record gives: %v", err)
	}
}

func TestThePartsArePackedInTheirOrder(t *testing.T) {
	o, b := shipped(t)
	o.DroppedNote = ""
	item := func(label string, chars int) Item { return Item{Label: label, Text: label + ": " + filler(chars)} }
	in := Input{
		Budget: b, Options: o, Messages: chat(40, 300),
		Profile: "The person keeps a bakery by the north pier.", PersonMap: "The bakery, the ferry, the lighthouse walk.",
		Project: "Write for the bakery's newsletter.", Style: "Short sentences.",
		Tools:    []seam.Tool{{Name: "timetable", Description: "Reads the ferry timetable.", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Attached: []Item{item("first chat", 600), item("second chat", 600), item("third chat", 600)},
		Recall:   []Item{item("most relevant", 240), item("less relevant", 240), item("least relevant", 240)},
		Summary:  "Earlier: " + filler(300),
	}

	// Room for everything.
	in.Model = model(40000, 1000)
	req, rec := build(t, in)
	fits(t, in, req, rec)
	for _, p := range rec.Parts {
		if p.State != StateKept && p.Part != PartNote && p.Part != PartPinned {
			t.Errorf("with room for everything, part %s is %s", p.Part, p.State)
		}
	}
	var system []string
	for _, bl := range req.System {
		system = append(system, bl.Text)
	}
	want := []string{o.SystemText, in.Profile, in.PersonMap, in.Project, in.Style,
		in.Attached[0].Text, in.Attached[1].Text, in.Attached[2].Text,
		in.Recall[0].Text, in.Recall[1].Text, in.Recall[2].Text, in.Summary}
	if !reflect.DeepEqual(system, want) {
		t.Fatalf("the system blocks are not in the fixed order:\n%q", system)
	}
	if len(req.Tools) != 1 || rec.Part(PartTools).Tokens == 0 || rec.Part(PartTools).Place != PlaceTools {
		t.Fatalf("the tools are not in the request and the record: %+v", rec.Part(PartTools))
	}

	// A long chat and a small window. Earlier turns are left out, and
	// recall and the summary still have the room that is held for them.
	in.Model = model(5000, 500)
	req, rec = build(t, in)
	fits(t, in, req, rec)
	if _, dropped := keptTurns(rec); dropped == 0 {
		t.Fatal("no turn was left out of a chat several times the window")
	}
	if got := rec.Part(PartSummary); got.State != StateKept {
		t.Errorf("the summary lost its held room to earlier turns: %+v", got)
	}
	recall := rec.Part(PartRecall)
	if recall.Kept == 0 || !recall.Items[0].Kept {
		t.Errorf("the most relevant recalled item lost its held room to earlier turns: %+v", recall)
	}
	// Attachments come before earlier turns, up to their share.
	attached := rec.Part(PartAttached)
	if attached.Kept == 0 || !attached.Items[0].Kept {
		t.Errorf("no attachment was kept ahead of earlier turns: %+v", attached)
	}

	// The shares are limits. With none for recall or the summary, both
	// are left out and the turns have the room.
	before, _ := keptTurns(rec)
	in.Options.RecallPercent, in.Options.SummaryPercent = 0, 0
	req, rec = build(t, in)
	fits(t, in, req, rec)
	if rec.Part(PartRecall).State != StateDropped || rec.Part(PartSummary).State != StateDropped {
		t.Errorf("a part with no share was sent: %+v %+v", rec.Part(PartRecall), rec.Part(PartSummary))
	}
	if after, _ := keptTurns(rec); after <= before {
		t.Errorf("%d turns kept with recall and the summary out, and %d with them in", after, before)
	}

	// A pinned item is never left out, where the same item unpinned is.
	in.Options.AttachedPercent = 0
	_, rec = build(t, in)
	if got := rec.Part(PartAttached); got.State != StateDropped {
		t.Fatalf("attachments with no share were sent: %+v", got)
	}
	in.Attached[2].Pinned = true
	req, rec = build(t, in)
	fits(t, in, req, rec)
	if got := rec.Part(PartAttached); got.State != StatePartly || !got.Items[2].Kept || !got.Items[2].Pinned || got.Items[0].Kept {
		t.Fatalf("the pinned attachment was not the one kept: %+v", got)
	}

	// An item too large for what is left is passed over, and a smaller
	// one after it is still sent.
	in.Options.RecallPercent = 10
	in.Recall[0] = item("far too large", 6000)
	_, rec = build(t, in)
	if got := rec.Part(PartRecall); got.Items[0].Kept || !got.Items[1].Kept {
		t.Fatalf("a smaller recalled item was not sent after one that was too large: %+v", got)
	}
}

func TestTheMostEarlierTurnsIsASetting(t *testing.T) {
	o, b := shipped(t)
	o.RecentTurnsMax = 3
	in := Input{Model: model(32000, 4000), Budget: b, Options: o, Messages: chat(10, 100)}
	req, rec := build(t, in)
	fits(t, in, req, rec)
	if kept, dropped := keptTurns(rec); kept != 4 || dropped != 7 {
		t.Fatalf("%d turns kept and %d dropped, not the current one, 3 before it, and 7 left out", kept, dropped)
	}
	if rec.Part(PartNote).State != StateKept {
		t.Fatal("turns were left out and the model was not told")
	}
}

func TestTheFillIsAShareOfTheRoom(t *testing.T) {
	o, b := shipped(t)
	o.FillPercent = 50
	in := Input{Model: model(10000, 2000), Budget: b, Options: o, Messages: chat(80, 300)}
	req, rec := build(t, in)
	fits(t, in, req, rec)
	if rec.Room != 8000 || rec.Limit != 4000 || rec.Tokens > 4000 {
		t.Fatalf("half the room was not the limit: %+v", rec)
	}
}

func TestTheRecordCountsWhatWasSent(t *testing.T) {
	o, b := shipped(t)
	in := Input{
		Model: model(6000, 500), Budget: b, Options: o, Messages: chat(40, 300),
		Profile: "The person keeps a bakery by the north pier.",
		Recall:  []Item{{Label: "a recalled note", Text: filler(200)}},
	}
	req, rec := build(t, in)
	fits(t, in, req, rec)

	// The whole: the record's count is the budget's count of the request.
	whole := budget.Estimated(req, b)
	if rec.Tokens != whole.Tokens || rec.Margin != whole.Margin || rec.Exact {
		t.Fatalf("the record counts %d with a margin of %d, and the budget %d with %d", rec.Tokens, rec.Margin, whole.Tokens, whole.Margin)
	}
	// The parts: each is the estimate of its own piece of the request, so
	// together they are the estimate of the whole, give or take the
	// rounding up of each piece.
	sum, pieces := 0, 0
	for _, p := range rec.Parts {
		sum += p.Tokens
		pieces += p.Kept
	}
	raw := budget.Estimate(req, b)
	if sum < raw || sum > raw+pieces {
		t.Fatalf("the parts count %d tokens in %d pieces, and the request is estimated at %d", sum, pieces, raw)
	}
	if got, want := rec.Part(PartRules).Tokens, budget.Estimate(seam.Request{System: req.System[:1]}, b); got != want {
		t.Errorf("the rules are recorded as %d tokens and estimated at %d", got, want)
	}
	turnSum := 0
	for _, turn := range rec.Turns {
		if got := budget.Estimate(seam.Request{Messages: in.Messages[turn.First : turn.First+turn.Messages]}, b); got != turn.Tokens {
			t.Fatalf("the turn at message %d is recorded as %d tokens and estimated at %d", turn.First, turn.Tokens, got)
		}
		if turn.Kept {
			turnSum += turn.Tokens
		}
	}
	if got := rec.Part(PartRecent).Tokens + rec.Part(PartCurrent).Tokens; got != turnSum {
		t.Fatalf("the parts count %d tokens of turns and the turns %d", got, turnSum)
	}

	// The record is stored as JSON, and reads back the same.
	raw2, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var back Record
	if err := json.Unmarshal(raw2, &back); err != nil || !reflect.DeepEqual(back, rec) {
		t.Fatalf("the record does not read back from JSON: %v", err)
	}
}

// counts is a provider that counts every request as a multiple of the
// estimate, and is never called.
type counts struct {
	times float64
	o     budget.Options
	asked *int
}

func (counts) Stream(context.Context, seam.Request, func(seam.Event) error) error {
	return errors.New("the builder sends nothing")
}

func (c counts) CountTokens(_ context.Context, req seam.Request) (int, error) {
	*c.asked++
	return int(float64(budget.Estimate(req, c.o)) * c.times), nil
}

func TestAProvidersCountIsBelieved(t *testing.T) {
	o, b := shipped(t)
	asked := 0
	in := Input{Model: model(6000, 500), Budget: b, Options: o, Messages: chat(60, 300), Provider: counts{times: 1.6, o: b, asked: &asked}}
	req, rec := build(t, in)
	asked1 := asked
	fits(t, in, req, rec)
	if !rec.Exact || rec.Margin != 0 || rec.Recounts == 0 || asked1 != rec.Recounts+1 {
		t.Fatalf("the provider's larger count did not lead to a smaller request: %+v, asked %d times", rec, asked1)
	}
	// A provider that counts less than the estimate is believed too, and
	// asked once.
	asked = 0
	in.Provider = counts{times: 0.5, o: b, asked: &asked}
	req, rec = build(t, in)
	asked1 = asked
	fits(t, in, req, rec)
	if !rec.Exact || rec.Recounts != 0 || asked1 != 1 {
		t.Fatalf("a provider that counts less was asked %d times: %+v", asked1, rec)
	}
	// With no recounts allowed, the request is returned over, to be
	// refused.
	in.Options.RecountMax = 0
	in.Provider = counts{times: 1.6, o: b, asked: &asked}
	if _, rec, _ := Build(context.Background(), in); rec.Over == 0 {
		t.Fatal("a request the provider counts over the window was said to fit")
	}
}

func TestTheStartOfACallDoesNotChangeWithTheChat(t *testing.T) {
	o, b := shipped(t)
	short := Input{Model: model(4000, 500), Budget: b, Options: o, Messages: chat(1, 100), Profile: "The person keeps a bakery.", Style: "Short sentences."}
	long := short
	long.Messages = chat(70, 300)
	long.Recall = []Item{{Label: "a recalled note", Text: filler(100)}}
	a, _ := build(t, short)
	c, rec := build(t, long)
	if _, dropped := keptTurns(rec); dropped == 0 {
		t.Fatal("the long chat was meant to lose turns")
	}
	if len(c.System) < 4 || !reflect.DeepEqual(a.System, c.System[:3]) {
		t.Fatalf("the start of the call changed with the chat:\n%+v\n%+v", a.System, c.System)
	}
}

func TestWhatIsRefused(t *testing.T) {
	o, b := shipped(t)
	if _, _, err := Build(context.Background(), Input{Model: model(4000, 500), Budget: b, Options: o}); !errors.Is(err, ErrNoMessages) {
		t.Fatalf("a branch with no messages: %v", err)
	}
	bad := []func(*Options){
		func(o *Options) { o.FillPercent = 0 },
		func(o *Options) { o.FillPercent = 101 },
		func(o *Options) { o.RecentTurnsMax = -1 },
		func(o *Options) { o.RecountMax = -1 },
		func(o *Options) { o.AttachedPercent = 101 },
		func(o *Options) { o.RecallPercent, o.SummaryPercent = 60, 50 },
		func(o *Options) { o.SummaryPercent = -1 },
	}
	for i, change := range bad {
		x := o
		change(&x)
		if _, _, err := Build(context.Background(), Input{Model: model(4000, 500), Budget: b, Options: x, Messages: chat(1, 10)}); err == nil {
			t.Errorf("options %d were accepted: %+v", i, x)
		}
	}
	// A person's own value is read before the default, and checked.
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	own, err := FromSettings(reg, func(key string) (string, bool) {
		if key == KeySystemText {
			return "", true
		}
		return "", false
	})
	if err != nil || own.SystemText != "" || own.FillPercent != o.FillPercent {
		t.Fatalf("a person's own empty system text was not read: %+v, %v", own, err)
	}
	if _, err := FromSettings(reg, func(key string) (string, bool) { return "many", key == KeyFillPercent }); err == nil {
		t.Fatal("a fill that is not a number was accepted")
	}
	// With no system text and nothing in any slot, the request has none.
	req, _ := build(t, Input{Model: model(4000, 500), Budget: b, Options: own, Messages: chat(1, 10)})
	if len(req.System) != 0 {
		t.Fatalf("an empty system text was sent: %+v", req.System)
	}
}
