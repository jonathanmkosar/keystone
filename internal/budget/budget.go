// Package budget keeps a model call inside the model's context window. It
// counts the tokens a seam request will use, with the provider's own count
// where the provider offers one and an estimate with a safety margin where
// it does not, and it refuses a call that cannot fit before it is sent.
//
// The package holds no state, opens no connection and reads no store. Its
// tunables are settings, read once by FromSettings and passed in as
// Options. docs/architecture/budget.md describes it.
package budget

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Counter is the optional half of a provider: one that can ask its model
// how many tokens a request's input holds. An adapter offers it by having
// this method on the value it returns as a seam.Provider. It does not need
// to import this package.
//
// CountTokens returns the input tokens of req: the system text, every
// message and every tool definition, as the provider would bill them. The
// room for the reply is not part of it. It sends nothing to the model that
// answers, and must never put a key in its error.
type Counter interface {
	CountTokens(ctx context.Context, req seam.Request) (int, error)
}

// Options are the tunables of the estimate. They come from the settings
// registry through FromSettings.
type Options struct {
	// CharsPerToken is how many characters of text count as one token.
	// Lower counts more tokens, which is the safe side.
	CharsPerToken float64
	// MarginPercent is added to an estimate, as a share of it. A
	// provider's own count gets no margin.
	MarginPercent int
	// MessageTokens is counted for each message, for its role and framing.
	MessageTokens int
	// ToolTokens is counted for each tool offered, on top of its text.
	ToolTokens int
	// ImageTokens is counted for each image, whatever its size.
	ImageTokens int
	// RefFileTokens is counted for a file named by reference, whose size
	// this package cannot see.
	RefFileTokens int
}

// CheckOptions refuses options no estimate can be made with.
func CheckOptions(o Options) error {
	switch {
	case !(o.CharsPerToken > 0) || math.IsInf(o.CharsPerToken, 0):
		return errors.New("budget: characters per token must be more than zero")
	case o.MarginPercent < 0:
		return errors.New("budget: the margin cannot be negative")
	case o.MessageTokens < 0 || o.ToolTokens < 0 || o.ImageTokens < 0 || o.RefFileTokens < 0:
		return errors.New("budget: a flat token count cannot be negative")
	}
	return nil
}

// Count is how many input tokens a request holds.
type Count struct {
	// Tokens is the number to budget with. For an estimate the margin is
	// already in it.
	Tokens int
	// Margin is the part of Tokens that is the safety margin. It is zero
	// for a provider's own count.
	Margin int
	// Exact says the provider counted. Otherwise Keystone estimated.
	Exact bool
}

// Fit is the outcome of checking one call against one model.
type Fit struct {
	Model string
	// Window is the entry's context window.
	Window int
	// Input is the request's input tokens.
	Input Count
	// Output is the room kept for the reply.
	Output int
	// Room is what is left of the window. It is negative when the call
	// does not fit.
	Room int
}

// ErrTooLarge is wrapped by every refusal of a call that cannot fit.
var ErrTooLarge = errors.New("the call does not fit the model's context window")

// TooLarge is the refusal of a call that cannot fit. Over says by how many
// tokens.
type TooLarge struct {
	Fit
	Over int
}

func (e *TooLarge) Error() string {
	how := "estimated with a margin"
	if e.Input.Exact {
		how = "counted by the provider"
	}
	return fmt.Sprintf("model %s: the call needs %d tokens (%d of input, %s, and %d kept for the reply) and the context window is %d: %d too many",
		e.Model, e.Input.Tokens+e.Output, e.Input.Tokens, how, e.Output, e.Window, e.Over)
}

// Unwrap makes errors.Is(err, ErrTooLarge) true.
func (e *TooLarge) Unwrap() error { return ErrTooLarge }

// SeamError is the refusal in the seam's terms: a too_long error, which
// routing reads the same way as a provider's own.
func (e *TooLarge) SeamError() *seam.Error {
	return &seam.Error{Kind: seam.ErrTooLong, Message: e.Error()}
}

// Estimate is Keystone's own count of a request's input tokens, with no
// margin. It never undercounts on purpose: text is counted by its
// characters, bytes sent inline by their length as base64, and an image or
// a referenced file by a flat number.
func Estimate(req seam.Request, o Options) int {
	chars := 0
	flat := 0
	for _, b := range req.System {
		c, f := blockSize(b, o)
		chars, flat = chars+c, flat+f
	}
	for _, m := range req.Messages {
		flat += o.MessageTokens
		for _, b := range m.Blocks {
			c, f := blockSize(b, o)
			chars, flat = chars+c, flat+f
		}
	}
	for _, t := range req.Tools {
		flat += o.ToolTokens
		chars += utf8.RuneCountInString(t.Name) + utf8.RuneCountInString(t.Description) + len(t.InputSchema)
	}
	return flat + int(math.Ceil(float64(chars)/o.CharsPerToken))
}

// blockSize returns a block's characters and its flat tokens.
func blockSize(b seam.Block, o Options) (chars, flat int) {
	switch b.Type {
	case seam.BlockText:
		return utf8.RuneCountInString(b.Text), 0
	case seam.BlockThinking:
		return utf8.RuneCountInString(b.Text) + len(b.Signature), 0
	case seam.BlockImage:
		return 0, o.ImageTokens
	case seam.BlockFile:
		chars = utf8.RuneCountInString(b.Name)
		switch {
		case b.Ref != "":
			return chars, o.RefFileTokens
		case strings.HasPrefix(b.MediaType, "text/"):
			return chars + len(b.Data), 0
		default:
			// Sent as base64: four characters for every three bytes.
			return chars + (len(b.Data)+2)/3*4, 0
		}
	case seam.BlockToolCall:
		return len(b.ID) + utf8.RuneCountInString(b.Name) + len(b.Input), 0
	case seam.BlockToolResult:
		chars = len(b.ID)
		for _, c := range b.Content {
			cc, cf := blockSize(c, o)
			chars, flat = chars+cc, flat+cf
		}
		return chars, flat
	}
	return 0, 0
}

// Estimated is the estimate with the safety margin added.
func Estimated(req seam.Request, o Options) Count {
	raw := Estimate(req, o)
	margin := int(math.Ceil(float64(raw) * float64(o.MarginPercent) / 100))
	return Count{Tokens: raw + margin, Margin: margin}
}

// Tokens counts a request's input tokens for the model behind p. When p is
// a Counter its count is used as it is. When p is not one, or its count
// fails, the answer is the estimate with the margin. p may be nil. The
// only error is a cancelled ctx or options that are not valid.
func Tokens(ctx context.Context, p seam.Provider, req seam.Request, o Options) (Count, error) {
	if err := CheckOptions(o); err != nil {
		return Count{}, err
	}
	if c, ok := p.(Counter); ok {
		n, err := c.CountTokens(ctx, req)
		if err == nil && n >= 0 {
			return Count{Tokens: n, Exact: true}, nil
		}
		if ctx.Err() != nil {
			return Count{}, ctx.Err()
		}
		// The provider could not count. The estimate still protects the
		// call, so the failure is not passed on.
	}
	return Estimated(req, o), nil
}

// Reply is the room a call keeps for the model's reply: what the request
// asks for, and the entry's output limit when it asks for nothing or for
// more than the model writes.
func Reply(m models.Model, req seam.Request) int {
	if req.MaxOutputTokens > 0 && req.MaxOutputTokens < m.OutputLimit {
		return req.MaxOutputTokens
	}
	return m.OutputLimit
}

// InputRoom is how many input tokens a call to m may hold when maxOutput
// tokens are kept for the reply. Zero or less for maxOutput keeps the
// entry's output limit. This is the number a context builder packs under.
func InputRoom(m models.Model, maxOutput int) int {
	return max(m.ContextWindow-Reply(m, seam.Request{MaxOutputTokens: max(maxOutput, 0)}), 0)
}

// Measure checks an input count against a model's window. It returns the
// Fit either way, and a *TooLarge when the input and the room for the
// reply together are more than the window.
func Measure(m models.Model, req seam.Request, input Count) (Fit, error) {
	f := Fit{Model: m.Name, Window: m.ContextWindow, Input: input, Output: Reply(m, req)}
	f.Room = f.Window - f.Input.Tokens - f.Output
	if f.Room < 0 {
		return f, &TooLarge{Fit: f, Over: -f.Room}
	}
	return f, nil
}

// Check is what a turn calls before it sends: it counts req for the model
// behind p and refuses it with a *TooLarge when it cannot fit m's window.
// Nothing is sent to the model that answers.
func Check(ctx context.Context, m models.Model, p seam.Provider, req seam.Request, o Options) (Fit, error) {
	input, err := Tokens(ctx, p, req, o)
	if err != nil {
		return Fit{Model: m.Name, Window: m.ContextWindow}, err
	}
	return Measure(m, req, input)
}

// Guard wraps a provider so that every call is checked first. A call that
// cannot fit ends with a too_long error event and is never sent to p. The
// wrapper keeps the seam's rules for a Provider, and is itself a Counter
// when p is one.
func Guard(m models.Model, p seam.Provider, o Options) seam.Provider {
	g := guard{m: m, p: p, o: o}
	if c, ok := p.(Counter); ok {
		return countingGuard{guard: g, Counter: c}
	}
	return g
}

type guard struct {
	m models.Model
	p seam.Provider
	o Options
}

type countingGuard struct {
	guard
	Counter
}

func (g guard) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	if _, err := Check(ctx, g.m, g.p, req, g.o); err != nil {
		var big *TooLarge
		if !errors.As(err, &big) {
			return err
		}
		refusal := big.SeamError()
		if err := emit(seam.Event{Type: seam.EventError, Error: refusal}); err != nil {
			return err
		}
		return refusal
	}
	return g.p.Stream(ctx, req, emit)
}
