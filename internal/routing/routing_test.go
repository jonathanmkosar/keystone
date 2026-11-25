package routing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Every model here is invented.
func model(name string, class models.Class, caps ...models.Capability) models.Model {
	return models.Model{Name: name, Label: name, Class: class, ContextWindow: 8000, OutputLimit: 1000, Capabilities: caps}
}

var registry = []models.Model{
	model("hosted-large", models.ClassHosted, models.CapTools, models.CapThinking),
	model("hosted-small", models.ClassHosted),
	model("house-small", models.ClassLocal),
	model("trusted-large", models.ClassTrusted, models.CapVision),
	model("house-embed", models.ClassLocal, models.CapEmbedding),
}

func names(ms []models.Model) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return strings.Join(out, " ")
}

func TestPlanFollowsTheRuleForTheKind(t *testing.T) {
	rules := Rules{
		Models: map[Kind]string{
			KindChat: "house-small", KindTitle: "hosted-small", KindSummary: "not-there",
			KindMemory: "house-small", KindEmbedding: "house-embed",
		},
		Fallback:     []string{"house-small", "hosted-large", "trusted-large"},
		MaxFallbacks: 2,
	}
	for _, c := range []struct {
		ask  Ask
		want string
	}{
		// Chat uses the model picked, whatever a rule says.
		{Ask{Kind: KindChat, Model: "hosted-large"}, "hosted-large house-small trusted-large"},
		// The rule's model first, then the chat's, then the chain.
		{Ask{Kind: KindTitle, Model: "hosted-large"}, "hosted-small hosted-large house-small"},
		// A rule that names no usable model falls to the chat's.
		{Ask{Kind: KindSummary, Model: "hosted-large"}, "hosted-large house-small trusted-large"},
		{Ask{Kind: KindMemory, Model: "hosted-large"}, "house-small hosted-large trusted-large"},
		// Only an embedding model embeds, and it does nothing else.
		{Ask{Kind: KindEmbedding, Model: "hosted-large"}, "house-embed"},
		{Ask{Kind: KindChat, Model: "house-embed"}, "house-small hosted-large trusted-large"},
		// A call that needs a capability goes only to models that have it.
		{Ask{Kind: KindChat, Model: "hosted-large", Needs: []models.Capability{models.CapVision}}, "trusted-large"},
	} {
		route, err := Plan(rules, c.ask, registry)
		if err != nil {
			t.Errorf("%+v: %v", c.ask, err)
			continue
		}
		if got := names(route.Models); got != c.want {
			t.Errorf("%s on %s: the route is %q, want %q", c.ask.Kind, c.ask.Model, got, c.want)
		}
	}
	// No fallback at all.
	route, err := Plan(Rules{Fallback: []string{"house-small"}}, Ask{Kind: KindChat, Model: "hosted-large"}, registry)
	if err != nil || names(route.Models) != "hosted-large" || len(route.Withheld) != 1 || route.Withheld[0].Why != WithheldLimit {
		t.Errorf("with no fallbacks allowed the route is %+v (%v)", route, err)
	}
	if _, err := Plan(rules, Ask{Kind: "poem", Model: "hosted-large"}, registry); err == nil {
		t.Error("an unknown kind of task was routed")
	}
	if _, err := Plan(Rules{}, Ask{Kind: KindChat, Model: "not-there"}, registry); !errors.Is(err, ErrNoModel) || errors.Is(err, ErrSensitive) {
		t.Errorf("a model that is not there gave %v", err)
	}
}

// Fixed rule 3: sensitive content reaches only local and trusted models.
func TestPlanKeepsSensitiveContentToLocalAndTrustedModels(t *testing.T) {
	rules := Rules{
		Models:       map[Kind]string{KindTitle: "hosted-small"},
		Fallback:     []string{"hosted-small", "house-small", "trusted-large"},
		MaxFallbacks: 5,
	}
	for _, kind := range []Kind{KindChat, KindTitle, KindSummary, KindMemory} {
		route, err := Plan(rules, Ask{Kind: kind, Model: "hosted-large", Sensitive: true}, registry)
		if err != nil {
			t.Fatal(err)
		}
		if got := names(route.Models); got != "house-small trusted-large" {
			t.Errorf("%s: a sensitive call is routed to %q", kind, got)
		}
		for _, m := range route.Models {
			if !m.MayReadSensitive() {
				t.Errorf("%s: %s is in a sensitive route", kind, m.Name)
			}
		}
		withheld := 0
		for _, w := range route.Withheld {
			if w.Why == WithheldSensitive {
				withheld++
			}
		}
		if withheld != 2 {
			t.Errorf("%s: the route says %d models were withheld as sensitive, want 2: %+v", kind, withheld, route.Withheld)
		}
	}
	// With nothing but hosted models there is no route, and the error says why.
	hosted := registry[:2]
	_, err := Plan(rules, Ask{Kind: KindChat, Model: "hosted-large", Sensitive: true}, hosted)
	if !errors.Is(err, ErrNoModel) || !errors.Is(err, ErrSensitive) {
		t.Errorf("a sensitive call with only hosted models gave %v", err)
	}
	// A class the registry does not know may not read sensitive content.
	odd := []models.Model{model("odd", "elsewhere")}
	if _, err := Plan(Rules{}, Ask{Kind: KindChat, Model: "odd", Sensitive: true}, odd); !errors.Is(err, ErrSensitive) {
		t.Errorf("a model of an unknown class was given a sensitive call: %v", err)
	}
}

// scripted is a provider that plays one list of events, and counts its calls.
type scripted struct {
	events []seam.Event
	calls  int
	got    []seam.Request
}

func (p *scripted) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	p.calls++
	p.got = append(p.got, req)
	for _, e := range p.events {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(e); err != nil {
			return err
		}
		if e.Type == seam.EventError {
			return e.Error
		}
	}
	return nil
}

func replies(text string) *scripted {
	events, err := seam.Events(seam.Response{
		Message:    seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}},
		StopReason: seam.StopEnd, Usage: seam.Usage{InputTokens: 10, OutputTokens: 5},
	})
	if err != nil {
		panic(err)
	}
	return &scripted{events: events}
}

func fails(kind seam.ErrorKind) *scripted {
	return &scripted{events: []seam.Event{
		{Type: seam.EventStart},
		{Type: seam.EventError, Error: &seam.Error{Kind: kind, Message: "an invented failure"}},
	}}
}

// breaksOff sends the start of a reply and then fails.
func breaksOff() *scripted {
	return &scripted{events: []seam.Event{
		{Type: seam.EventStart},
		{Type: seam.EventBlockStart, Index: 0, Block: &seam.Block{Type: seam.BlockText}},
		{Type: seam.EventBlockDelta, Index: 0, Text: "Half a"},
		{Type: seam.EventError, Error: &seam.Error{Kind: seam.ErrUnavailable, Message: "the stream ended before its stop event"}},
	}}
}

var ask = seam.Request{Messages: []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Hello"}}}}}

func TestAChainFallsBackBeforeAnyOfTheReplyIsPassedOn(t *testing.T) {
	first, second, third := fails(seam.ErrUnavailable), replies("From the second."), replies("From the third.")
	var moves []string
	chain, err := NewChain(false, []Link{
		{registry[0], first}, {registry[2], second}, {registry[3], third},
	}, Hooks{Moved: func(_ context.Context, failed Attempt, next models.Model, req seam.Request) error {
		moves = append(moves, failed.Model.Name+">"+next.Name+":"+string(failed.Error.Kind)+":"+req.Model)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	var events []seam.Event
	var col seam.Collector
	err = chain.Stream(context.Background(), ask, func(e seam.Event) error {
		events = append(events, e)
		return col.Add(e)
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := col.Response()
	if err != nil || seam.Text(resp.Message) != "From the second." {
		t.Fatalf("the reply is %q (%v)", seam.Text(resp.Message), err)
	}
	for _, e := range events {
		if e.Type == seam.EventError {
			t.Error("the first model's error reached the caller")
		}
	}
	if events[0].Type != seam.EventStart || len(events) < 4 {
		t.Errorf("the caller saw %d events, the first a %s", len(events), events[0].Type)
	}
	if first.calls != 1 || second.calls != 1 || third.calls != 0 {
		t.Errorf("the models were called %d, %d and %d times", first.calls, second.calls, third.calls)
	}
	if len(moves) != 1 || moves[0] != "hosted-large>house-small:unavailable:house-small" {
		t.Errorf("the moves were %v", moves)
	}
	if first.got[0].Model != "hosted-large" || second.got[0].Model != "house-small" {
		t.Errorf("the requests were named %s and %s", first.got[0].Model, second.got[0].Model)
	}
	if chain.Last().Name != "house-small" || Answered(chain, registry[0]).Name != "house-small" || Answered(first, registry[0]).Name != "hosted-large" {
		t.Errorf("the chain says %s answered", chain.Last().Name)
	}
}

func TestAChainDoesNotFallBackOnceAReplyHasBegun(t *testing.T) {
	first, second := breaksOff(), replies("From the second.")
	chain, err := NewChain(false, []Link{{registry[0], first}, {registry[2], second}}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	var col seam.Collector
	err = chain.Stream(context.Background(), ask, col.Add)
	var failure *seam.Error
	if !errors.As(err, &failure) || failure.Kind != seam.ErrUnavailable {
		t.Fatalf("the call ended with %v", err)
	}
	if second.calls != 0 {
		t.Error("another model was called after part of a reply had been passed on")
	}
	if got := seam.Text(col.Partial()); got != "Half a" {
		t.Errorf("the caller holds %q", got)
	}
	if chain.Last().Name != "hosted-large" {
		t.Errorf("the chain says the call ended on %s", chain.Last().Name)
	}
}

func TestAChainStopsWhereAnotherModelWouldNotHelp(t *testing.T) {
	// A request the provider calls malformed is not sent to another model.
	first, second := fails(seam.ErrInvalid), replies("From the second.")
	chain, _ := NewChain(false, []Link{{registry[0], first}, {registry[2], second}}, Hooks{})
	var events []seam.Event
	err := chain.Stream(context.Background(), ask, func(e seam.Event) error { events = append(events, e); return nil })
	var failure *seam.Error
	if !errors.As(err, &failure) || failure.Kind != seam.ErrInvalid || second.calls != 0 {
		t.Errorf("an invalid request ended with %v, and the next model was called %d times", err, second.calls)
	}
	if len(events) != 1 || events[0].Type != seam.EventError {
		t.Errorf("the caller saw %d events, want the one error", len(events))
	}

	// Every model fails: the last failure is the call's, and each was called once.
	a, b := fails(seam.ErrRateLimited), fails(seam.ErrOverloaded)
	chain, _ = NewChain(false, []Link{{registry[0], a}, {registry[2], b}}, Hooks{})
	err = chain.Stream(context.Background(), ask, func(seam.Event) error { return nil })
	if !errors.As(err, &failure) || failure.Kind != seam.ErrOverloaded || a.calls != 1 || b.calls != 1 {
		t.Errorf("two failing models ended with %v after %d and %d calls", err, a.calls, b.calls)
	}

	// A stopped call is not tried again.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a, b = fails(seam.ErrUnavailable), replies("no")
	chain, _ = NewChain(false, []Link{{registry[0], a}, {registry[2], b}}, Hooks{})
	if err := chain.Stream(ctx, ask, func(seam.Event) error { return nil }); !errors.Is(err, context.Canceled) || b.calls != 0 {
		t.Errorf("a stopped call ended with %v, and the next model was called %d times", err, b.calls)
	}

	// A move that cannot be recorded ends the call.
	a, b = fails(seam.ErrUnavailable), replies("no")
	broken := errors.New("the log is full")
	chain, _ = NewChain(false, []Link{{registry[0], a}, {registry[2], b}}, Hooks{
		Moved: func(context.Context, Attempt, models.Model, seam.Request) error { return broken },
	})
	if err := chain.Stream(context.Background(), ask, func(seam.Event) error { return nil }); !errors.Is(err, broken) || b.calls != 0 {
		t.Errorf("a move that was not recorded ended with %v, and the next model was called %d times", err, b.calls)
	}
}

func TestAChainAsksItsGateBeforeEachModel(t *testing.T) {
	paid, free := replies("From the paid model."), replies("From the house.")
	gate := func(_ context.Context, m models.Model, _ seam.Request) *seam.Error {
		if m.Class != models.ClassLocal {
			return &seam.Error{Kind: seam.ErrSpendCap, Message: "over the cap"}
		}
		return nil
	}
	var refused []Attempt
	chain, _ := NewChain(false, []Link{{registry[0], paid}, {registry[2], free}}, Hooks{
		Gate: gate,
		Moved: func(_ context.Context, a Attempt, _ models.Model, _ seam.Request) error {
			refused = append(refused, a)
			return nil
		},
	})
	resp, err := seam.Call(context.Background(), chain, ask)
	if err != nil || seam.Text(resp.Message) != "From the house." || paid.calls != 0 {
		t.Errorf("the reply is %q (%v), and the paid model was called %d times", seam.Text(resp.Message), err, paid.calls)
	}
	if len(refused) != 1 || !refused[0].Refused || refused[0].Error.Kind != seam.ErrSpendCap {
		t.Errorf("the refused attempts are %+v", refused)
	}
	// With no model the gate allows, the refusal is the call's error.
	chain, _ = NewChain(false, []Link{{registry[0], paid}}, Hooks{Gate: gate})
	_, err = seam.Call(context.Background(), chain, ask)
	var failure *seam.Error
	if !errors.As(err, &failure) || failure.Kind != seam.ErrSpendCap || paid.calls != 0 {
		t.Errorf("a call the gate refused ended with %v after %d calls", err, paid.calls)
	}
}

func TestAChainRefusesASensitiveRouteWithAHostedModel(t *testing.T) {
	p := replies("no")
	if _, err := NewChain(true, []Link{{registry[2], p}, {registry[0], p}}, Hooks{}); !errors.Is(err, ErrSensitive) {
		t.Errorf("a sensitive chain with a hosted model gave %v", err)
	}
	if _, err := NewChain(true, []Link{{registry[2], p}, {registry[3], p}}, Hooks{}); err != nil {
		t.Errorf("a sensitive chain of a local and a trusted model was refused: %v", err)
	}
	if _, err := NewChain(false, nil, Hooks{}); !errors.Is(err, ErrNoModel) {
		t.Errorf("an empty chain gave %v", err)
	}
	if _, err := NewChain(false, []Link{{registry[0], nil}}, Hooks{}); err == nil {
		t.Error("a link with no provider was accepted")
	}
}

func TestARequestIsFittedToTheModelItGoesTo(t *testing.T) {
	req := seam.Request{
		Model: "hosted-large", MaxOutputTokens: 4000, ThinkingTokens: 2000,
		Messages: []seam.Message{
			{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "one"}}},
			{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockThinking, Text: "mine", Signature: "sig"}}},
			{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "two"}}},
		},
	}
	same := For(registry[0], req, false)
	if same.Model != "hosted-large" || same.ThinkingTokens != 2000 || same.MaxOutputTokens != 1000 || len(same.Messages) != 3 {
		t.Errorf("for its own model the request is %+v", same)
	}
	other := For(registry[2], req, true)
	if other.Model != "house-small" || other.ThinkingTokens != 0 || other.MaxOutputTokens != 1000 {
		t.Errorf("for another model the request is %+v", other)
	}
	if len(other.Messages) != 1 || len(other.Messages[0].Blocks) != 2 {
		t.Errorf("for another model the messages are %+v", other.Messages)
	}
	if err := seam.ValidateRequest(other); err != nil {
		t.Error(err)
	}
	if len(req.Messages) != 3 || len(req.Messages[0].Blocks) != 1 {
		t.Error("fitting a request changed the caller's own")
	}
}

type counting struct{ *scripted }

func (counting) CountTokens(context.Context, seam.Request) (int, error) { return 42, nil }

func TestAChainCountsWithItsFirstModel(t *testing.T) {
	chain, _ := NewChain(false, []Link{{registry[0], counting{replies("a")}}, {registry[2], replies("b")}}, Hooks{})
	if n, err := chain.CountTokens(context.Background(), ask); err != nil || n != 42 {
		t.Errorf("the count is %d (%v)", n, err)
	}
	chain, _ = NewChain(false, []Link{{registry[2], replies("b")}}, Hooks{})
	if _, err := chain.CountTokens(context.Background(), ask); err == nil {
		t.Error("a provider that does not count gave a count")
	}
}

func TestNames(t *testing.T) {
	if got := strings.Join(Names(" a , ,b,"), "|"); got != "a|b" {
		t.Errorf("the names are %q", got)
	}
	if Names("") != nil {
		t.Error("an empty list gave names")
	}
}
