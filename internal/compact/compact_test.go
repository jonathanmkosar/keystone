package compact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every line of chat here is invented.

func options() Options {
	return Options{
		TriggerPercent: 80, KeepPercent: 50, SummaryMaxTokens: 1500,
		Prompt:          "You keep the running summary of a long chat. Write at most {words} words.",
		MessageMaxChars: 4000, CallsMax: 4,
	}
}

func turns(tokens int, kept ...bool) []Turn {
	out := make([]Turn, len(kept))
	for i, k := range kept {
		out[i] = Turn{Tokens: tokens, Kept: k}
	}
	return out
}

func TestPlanSaysWhenAndWhichTurns(t *testing.T) {
	o := options()
	all := func(n int) []bool {
		k := make([]bool, n)
		for i := range k {
			k[i] = true
		}
		return k
	}
	// Ten turns of 100 in a room of 1000 take all of it: over 80 percent.
	// The oldest go until the rest take no more than 500.
	if got, ok := Plan(turns(100, all(10)...), 1000, o); !ok || got != (Span{0, 5}) {
		t.Fatalf("a chat over the trigger: %+v %v, want turns 0 to 5", got, ok)
	}
	// Eight of 100 take 80 percent, which is not over it.
	if got, ok := Plan(turns(100, all(8)...), 1000, o); ok {
		t.Fatalf("a chat at the trigger was summarised: %+v", got)
	}
	// A turn left out is summarised even under the trigger, and the span
	// reaches the first turn that was sent, so nothing is left in a gap.
	left := turns(100, false, false, false, true, true)
	if got, ok := Plan(left, 1000, o); !ok || got != (Span{0, 3}) {
		t.Fatalf("turns left out under the trigger: %+v %v, want turns 0 to 3", got, ok)
	}
	// Turns the summary already stands for are not summarised again: the
	// next summary takes in the turns after them.
	covered := turns(100, all(14)...)
	for i := range 4 {
		covered[i].Summarised, covered[i].Kept = true, false
	}
	if got, ok := Plan(covered, 1000, o); !ok || got != (Span{4, 9}) {
		t.Fatalf("a chat with a summary over the trigger: %+v %v, want turns 4 to 9", got, ok)
	}
	for i := range covered {
		covered[i].Summarised = true
	}
	if got, ok := Plan(covered, 1000, o); ok {
		t.Fatalf("a chat whose every turn is summarised was summarised: %+v", got)
	}
	// No room at all: every turn is summarised.
	if got, ok := Plan(turns(100, false, false), 0, o); !ok || got != (Span{0, 2}) {
		t.Fatalf("no room: %+v %v", got, ok)
	}
	// A chat that grows past the trigger, turn by turn, is summarised in
	// steps, not at every turn.
	var grown []Turn
	summaries, covers := 0, 0
	for range 200 {
		grown = append(grown, Turn{Tokens: 60, Kept: true})
		if got, ok := Plan(grown, 1000, o); ok {
			summaries++
			for i := got.From; i < got.To; i++ {
				grown[i].Summarised, grown[i].Kept = true, false
			}
			covers += got.To - got.From
		}
	}
	if summaries == 0 || summaries > 200/4 || covers < 150 {
		t.Fatalf("200 turns were summarised %d times, taking in %d turns", summaries, covers)
	}
}

func TestOptionsAreChecked(t *testing.T) {
	if err := CheckOptions(options()); err != nil {
		t.Fatal(err)
	}
	for i, change := range []func(*Options){
		func(o *Options) { o.TriggerPercent = 0 },
		func(o *Options) { o.TriggerPercent = 101 },
		func(o *Options) { o.KeepPercent = 80 },
		func(o *Options) { o.KeepPercent = -1 },
		func(o *Options) { o.SummaryMaxTokens = 0 },
		func(o *Options) { o.Prompt = "  " },
		func(o *Options) { o.MessageMaxChars = 0 },
		func(o *Options) { o.CallsMax = 0 },
	} {
		o := options()
		change(&o)
		if err := CheckOptions(o); !errors.Is(err, ErrOptions) {
			t.Errorf("options %d were accepted: %+v", i, o)
		}
	}
}

// branch is a chat of n turns, every third using a tool, and a thinking
// block on each reply.
func branch(n, chars int) ([]seam.Message, [][2]int) {
	text := func(role seam.Role, s string) seam.Message {
		return seam.Message{Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: s}}}
	}
	filler := strings.Repeat("the tide turns at the harbour mouth ", chars/36+1)[:chars]
	var out []seam.Message
	var spans [][2]int
	for i := 1; i <= n; i++ {
		first := len(out)
		out = append(out, text(seam.RoleUser, fmt.Sprintf("Question %d. %s", i, filler)))
		if i%3 == 0 {
			id := fmt.Sprintf("call-%d", i)
			out = append(out,
				seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockToolCall, ID: id, Name: "tide_table", Input: json.RawMessage(`{"day":"monday"}`)}}},
				seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockToolResult, ID: id, Content: []seam.Block{{Type: seam.BlockText, Text: "High water at six."}}}}},
			)
		}
		out = append(out, seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{
			{Type: seam.BlockThinking, Text: "A private thought about the tide.", Signature: "sig-invented"},
			{Type: seam.BlockText, Text: fmt.Sprintf("Answer %d. %s", i, filler)},
		}})
		spans = append(spans, [2]int{first, len(out)})
	}
	return out, spans
}

func TestTheRequestTakesInTheTurnsThatFit(t *testing.T) {
	messages, spans := branch(12, 300)
	everything := func(seam.Request) bool { return true }
	a := Ask{Options: options(), Model: "summary-model", Messages: messages, Turns: spans, Span: Span{0, 10}, Room: 600, CharsPerToken: 3, Fits: everything}

	req, to, err := Request(a)
	if err != nil || to != 10 {
		t.Fatalf("with room for every turn: up to turn %d, %v", to, err)
	}
	if err := seam.ValidateRequest(req); err != nil {
		t.Fatal(err)
	}
	body := seam.Text(req.Messages[0])
	switch {
	case req.Model != "summary-model" || req.MaxOutputTokens != 600:
		t.Fatalf("the request is for %q with %d tokens of reply", req.Model, req.MaxOutputTokens)
	case !strings.Contains(req.System[0].Text, "at most 300 words"):
		t.Fatalf("the prompt does not give the most words: %q", req.System[0].Text)
	case !strings.HasPrefix(body, "There is no summary yet."):
		t.Fatalf("a first summary does not say so: %.60q", body)
	case !strings.Contains(body, "The person: Question 1.") || !strings.Contains(body, "The assistant: Answer 10.") || strings.Contains(body, "Question 11."):
		t.Fatal("the request does not hold turns 1 to 10 and no more")
	case !strings.Contains(body, `[The assistant used the tool tide_table with {"day":"monday"}.]`) || !strings.Contains(body, "[The tool tide_table answered: High water at six.]"):
		t.Fatalf("the tool steps are not written out: %s", body)
	case strings.Contains(body, "private thought"):
		t.Fatal("a thinking block reached the summary model")
	}

	// The summary so far comes first, and the summary is asked to take
	// in what follows it.
	a.Previous = "The person is planning a walk by the harbour."
	a.Span = Span{10, 12}
	req, to, _ = Request(a)
	if body := seam.Text(req.Messages[0]); to != 12 || !strings.HasPrefix(body, "The summary so far:\nThe person is planning a walk by the harbour.\n\nThe turns that follow it") || !strings.Contains(body, "Question 11.") {
		t.Fatalf("an extension: up to turn %d, %.120q", to, body)
	}

	// A summary model with a small window takes fewer turns.
	small := func(r seam.Request) bool { return len(seam.Text(r.Messages[0])) < 2500 }
	a.Previous, a.Span, a.Fits = "", Span{0, 10}, small
	req, to, err = Request(a)
	if err != nil || to < 1 || to >= 10 || !small(req) {
		t.Fatalf("a small window: up to turn %d, %v", to, err)
	}

	// A turn too large for the window on its own is cut, and says so.
	a.Fits = func(r seam.Request) bool { return len(seam.Text(r.Messages[0])) < 200 }
	req, to, err = Request(a)
	if err != nil || to != 1 || !strings.HasSuffix(seam.Text(req.Messages[0]), "[The rest of this turn is cut.]") {
		t.Fatalf("a turn too large for the window: up to turn %d, %v", to, err)
	}

	// No room in the call, or none in the window, asks for nothing.
	a.Fits = func(seam.Request) bool { return false }
	if _, _, err := Request(a); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("a window with no room: %v", err)
	}
	a.Fits, a.Room = everything, 1
	if _, _, err := Request(a); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("a call with no room for a summary: %v", err)
	}
	a.Room, a.Span = 600, Span{3, 2}
	if _, _, err := Request(a); err == nil {
		t.Fatal("a span that ends before it starts was accepted")
	}

	// A message longer than the most the model is shown is cut.
	a.Span, a.Options.MessageMaxChars = Span{0, 1}, 50
	req, _, _ = Request(a)
	if body := seam.Text(req.Messages[0]); !strings.Contains(body, " [cut]") || strings.Contains(body, strings.Repeat("the tide turns", 3)) {
		t.Fatalf("a long message was not cut: %q", body)
	}
}

func TestTheAnswerIsRead(t *testing.T) {
	got, err := Answer(seam.Response{Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: "  The person asked about the ferry.  \n"}}}})
	if err != nil || got != "The person asked about the ferry." {
		t.Fatalf("%q, %v", got, err)
	}
	if _, err := Answer(seam.Response{Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: " "}}}}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("an empty answer: %v", err)
	}
}

func TestSummariesAreKept(t *testing.T) {
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	ten, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(ten.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Save(ctx, s, Summary{ChatID: c.ID, FirstID: 1, ThroughID: 8, Turns: 4, Text: "The person asked about the ferry.", Model: "summary-model", Tokens: 12})
	if err != nil || first.ID == 0 || first.Created.IsZero() {
		t.Fatalf("%+v, %v", first, err)
	}
	second, err := Save(ctx, s, Summary{ChatID: c.ID, FirstID: 1, ThroughID: 20, Turns: 10, Extends: first.ID, Text: "The person asked about the ferry and the bakery.", Model: "summary-model", Sensitive: true, Tokens: 18})
	if err != nil {
		t.Fatal(err)
	}
	list, err := List(ctx, s, c.ID)
	if err != nil || len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID || list[0].Extends != first.ID || !list[0].Sensitive {
		t.Fatalf("the list is %+v, %v", list, err)
	}
	if !list[0].Created.Equal(second.Created) {
		t.Fatalf("the time came back as %v, written as %v", list[0].Created, second.Created)
	}
	if _, err := Save(ctx, s, Summary{ChatID: c.ID, FirstID: 1, ThroughID: 8, Turns: 0, Text: "x", Model: "m"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a summary of no turns: %v", err)
	}

	// Fixed rule 3: a sensitive summary only for a call whose every model
	// may read sensitive content.
	if Usable(second, false) || !Usable(second, true) || !Usable(first, false) {
		t.Fatal("Usable does not follow the sensitive mark")
	}

	// The summaries go with their chat.
	if err := chat.Delete(s, c.ID); err != nil {
		t.Fatal(err)
	}
	if list, err := List(ctx, s, c.ID); err != nil || len(list) != 0 {
		t.Fatalf("summaries are left after their chat was deleted: %+v, %v", list, err)
	}
}
