package budget

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/settings"
)

var opts = Options{CharsPerToken: 4, MarginPercent: 10, MessageTokens: 5, ToolTokens: 10, ImageTokens: 1000, RefFileTokens: 2000}

var small = models.Model{Name: "tiny", ContextWindow: 1000, OutputLimit: 200}

func say(text string) seam.Request {
	return seam.Request{Messages: []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}}}}
}

// plain is a provider that cannot count. sent records whether it was called.
type plain struct{ sent *bool }

func (p plain) Stream(_ context.Context, _ seam.Request, emit func(seam.Event) error) error {
	if p.sent != nil {
		*p.sent = true
	}
	return emit(seam.Event{Type: seam.EventStop, StopReason: "end", Usage: &seam.Usage{}})
}

// counting is a provider that counts.
type counting struct {
	plain
	n   int
	err error
}

func (c counting) CountTokens(context.Context, seam.Request) (int, error) { return c.n, c.err }

func TestEstimate(t *testing.T) {
	req := seam.Request{
		System: []seam.Block{{Type: seam.BlockText, Text: strings.Repeat("s", 40)}},
		Messages: []seam.Message{
			{Role: seam.RoleUser, Blocks: []seam.Block{
				{Type: seam.BlockText, Text: strings.Repeat("é", 40)},
				{Type: seam.BlockImage, MediaType: "image/png", Ref: "abc"},
				{Type: seam.BlockFile, MediaType: "application/pdf", Name: "abcd", Ref: "def"},
				{Type: seam.BlockFile, MediaType: "text/plain", Name: "abcd", Data: []byte(strings.Repeat("x", 36))},
				{Type: seam.BlockFile, MediaType: "application/pdf", Name: "abcd", Data: make([]byte, 30)},
			}},
			{Role: seam.RoleAssistant, Blocks: []seam.Block{
				{Type: seam.BlockToolCall, ID: "c1", Name: "look", Input: json.RawMessage(`{"a":1}`)},
			}},
			{Role: seam.RoleUser, Blocks: []seam.Block{
				{Type: seam.BlockToolResult, ID: "c1", Content: []seam.Block{{Type: seam.BlockText, Text: "abcdef"}}},
			}},
		},
		Tools: []seam.Tool{{Name: "look", Description: "abcd", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}
	// Characters: 40 + 40 + 4 + (4+36) + (4+40) + (2+4+7) + (2+6) + (4+4+17) = 214.
	// Flat: 3 messages, an image, a referenced file, a tool.
	want := 3*5 + 1000 + 2000 + 10 + 54
	if got := Estimate(req, opts); got != want {
		t.Fatalf("Estimate = %d, want %d", got, want)
	}
	c := Estimated(say(strings.Repeat("a", 400)), opts)
	if c.Tokens != 116 || c.Margin != 11 || c.Exact {
		t.Fatalf("Estimated = %+v, want 105 and a margin of 11", c)
	}
}

func TestTokensUsesTheProvidersCount(t *testing.T) {
	ctx := context.Background()
	req := say(strings.Repeat("a", 400))
	c, err := Tokens(ctx, counting{n: 77}, req, opts)
	if err != nil || c != (Count{Tokens: 77, Exact: true}) {
		t.Fatalf("a provider that counts: %+v, %v", c, err)
	}
	for name, p := range map[string]seam.Provider{"no count": plain{}, "no provider": nil, "a count that fails": counting{err: errors.New("down")}} {
		c, err := Tokens(ctx, p, req, opts)
		if err != nil || c.Exact || c.Tokens != 116 {
			t.Fatalf("%s: %+v, %v, want the estimate", name, c, err)
		}
	}
	stopped, stop := context.WithCancel(ctx)
	stop()
	if _, err := Tokens(stopped, counting{err: stopped.Err()}, req, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled count: %v", err)
	}
	if _, err := Tokens(ctx, nil, req, Options{}); err == nil {
		t.Fatal("empty options were accepted")
	}
}

func TestCheck(t *testing.T) {
	ctx := context.Background()
	// 800 input and 200 for the reply is the window exactly.
	fit, err := Check(ctx, small, counting{n: 800}, say("hello"), opts)
	if err != nil || fit.Room != 0 || fit.Output != 200 {
		t.Fatalf("an exact fit: %+v, %v", fit, err)
	}
	_, err = Check(ctx, small, counting{n: 801}, say("hello"), opts)
	var big *TooLarge
	if !errors.As(err, &big) || !errors.Is(err, ErrTooLarge) || big.Over != 1 {
		t.Fatalf("one token over: %v", err)
	}
	for _, part := range []string{"model tiny", "1 too many", "counted by the provider", "1000"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("the refusal %q does not say %q", err, part)
		}
	}
	// A smaller reply makes room; a larger one is held to the output limit.
	req := say("hello")
	req.MaxOutputTokens = 50
	if fit, err := Check(ctx, small, counting{n: 950}, req, opts); err != nil || fit.Output != 50 {
		t.Fatalf("a short reply: %+v, %v", fit, err)
	}
	req.MaxOutputTokens = 5000
	if fit, _ := Check(ctx, small, counting{n: 1}, req, opts); fit.Output != 200 {
		t.Fatalf("a reply over the limit keeps %d", fit.Output)
	}
	// An estimate: 3200 characters is 800 tokens and 5 for the message,
	// 886 with the margin, 86 over.
	_, err = Check(ctx, small, plain{}, say(strings.Repeat("a", 3200)), opts)
	if !errors.As(err, &big) || big.Over != 86 || !strings.Contains(err.Error(), "estimated") {
		t.Fatalf("an estimate over the window: %v", err)
	}
	if InputRoom(small, 0) != 800 || InputRoom(small, 100) != 900 {
		t.Fatalf("InputRoom = %d, %d", InputRoom(small, 0), InputRoom(small, 100))
	}
}

func TestGuard(t *testing.T) {
	ctx := context.Background()
	sent := false
	g := Guard(small, plain{sent: &sent}, opts)
	if _, ok := g.(Counter); ok {
		t.Fatal("the guard of a provider that cannot count says it can")
	}
	var events []seam.Event
	err := g.Stream(ctx, say(strings.Repeat("a", 4000)), func(e seam.Event) error { events = append(events, e); return nil })
	var refusal *seam.Error
	if !errors.As(err, &refusal) || refusal.Kind != seam.ErrTooLong || sent {
		t.Fatalf("a call too large: err %v, sent %v", err, sent)
	}
	if len(events) != 1 || events[0].Type != seam.EventError || seam.ValidateEvent(events[0]) != nil {
		t.Fatalf("the refusal's events: %+v", events)
	}
	quiet := func(seam.Event) error { return nil }
	if err := g.Stream(ctx, say("hello"), quiet); err != nil || !sent {
		t.Fatalf("a call that fits: err %v, sent %v", err, sent)
	}
	if _, ok := Guard(small, counting{n: 1}, opts).(Counter); !ok {
		t.Fatal("the guard of a provider that counts lost the count")
	}
	if err := Guard(small, counting{n: 900}, opts).Stream(ctx, say("hello"), quiet); !errors.As(err, &refusal) {
		t.Fatalf("the provider's count was not believed: %v", err)
	}
}

func TestFromSettings(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	o, err := FromSettings(reg, nil)
	if err != nil || o.CharsPerToken <= 0 || o.MarginPercent <= 0 || o.ImageTokens <= 0 {
		t.Fatalf("the shipped settings: %+v, %v", o, err)
	}
	over, err := FromSettings(reg, func(key string) (string, bool) { return "30", key == KeyMarginPercent })
	if err != nil || over.MarginPercent != 30 || over.ImageTokens != o.ImageTokens {
		t.Fatalf("a stored value: %+v, %v", over, err)
	}
	if _, err := FromSettings(reg, func(key string) (string, bool) { return "lots", key == KeyMarginPercent }); err == nil {
		t.Fatal("a margin that is not a number was accepted")
	}
	if _, err := FromSettings(reg, func(key string) (string, bool) { return "0", key == KeyCharsPerToken }); err == nil {
		t.Fatal("zero characters per token was accepted")
	}
	if _, err := FromSettings(nil, nil); err == nil {
		t.Fatal("an empty registry was accepted")
	}
}
