package routing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Link is one model of a route, opened: the entry and a provider for it.
type Link struct {
	Model    models.Model
	Provider seam.Provider
}

// Attempt is one link that did not answer.
type Attempt struct {
	Model models.Model
	// Began is when the link was reached, and Duration how long it took
	// to fail.
	Began    time.Time
	Duration time.Duration
	// Error is why it did not answer.
	Error *seam.Error
	// Refused says the gate turned the call down, so nothing was sent.
	Refused bool
}

// Hooks are what a chain asks its caller. Both may be nil.
type Hooks struct {
	// Gate is asked before a link is called, with the request as that link
	// would be sent it. A non-nil answer is why the call may not be made,
	// and the link is not called. The monthly spend cap is a gate.
	Gate func(ctx context.Context, m models.Model, req seam.Request) *seam.Error
	// Moved is told that a link did not answer and the next is about to be
	// tried, with the request as the next link will be sent it. It is
	// where the failed call is recorded and the next one is recorded as
	// started. An error ends the call: a call that cannot be recorded is
	// not made.
	Moved func(ctx context.Context, failed Attempt, next models.Model, req seam.Request) error
}

// Chain is a route's models as one seam.Provider. Stream calls the first
// link. When a link fails before anything of its reply has been passed on,
// and the failure is one another model may not share, the next link is
// called with the same request. Once a block of a reply has been passed
// on, a failure is the call's failure: half a reply from one model is
// never followed by another model's.
//
// Each link is called once. An adapter's own retries happen inside its
// provider, so a chain does not add to them.
//
// A chain is made for one turn. Stream may be called again for each step
// of the turn, and Last answers for the most recent call.
type Chain struct {
	links []Link
	hooks Hooks

	mu   sync.Mutex
	last models.Model
}

// NewChain makes a chain over the links, in order. sensitive says the
// calls will carry content from a sensitive space, and a chain is then
// refused if any link's model may not read it. Plan already leaves such a
// model out. This is the second lock on the same door.
func NewChain(sensitive bool, links []Link, hooks Hooks) (*Chain, error) {
	if len(links) == 0 {
		return nil, fmt.Errorf("%w: the route is empty", ErrNoModel)
	}
	for _, l := range links {
		if l.Provider == nil {
			return nil, fmt.Errorf("routing: model %s has no provider", l.Model.Name)
		}
		if sensitive && !l.Model.MayReadSensitive() {
			return nil, fmt.Errorf("%w: model %s is %s", ErrSensitive, l.Model.Name, l.Model.Class)
		}
	}
	return &Chain{links: append([]Link(nil), links...), hooks: hooks, last: links[0].Model}, nil
}

// Models returns the chain's models, in the order they are tried.
func (c *Chain) Models() []models.Model {
	out := make([]models.Model, len(c.links))
	for i, l := range c.links {
		out[i] = l.Model
	}
	return out
}

// Last returns the model the most recent Stream ended on: the one that
// answered, or the one whose failure was the call's. Before any call it is
// the first model.
func (c *Chain) Last() models.Model {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func (c *Chain) setLast(m models.Model) {
	c.mu.Lock()
	c.last = m
	c.mu.Unlock()
}

// Answered returns the model a call through p ended on, when p is a chain,
// and otherwise m, the model the caller opened p for.
func Answered(p seam.Provider, m models.Model) models.Model {
	if c, ok := p.(*Chain); ok {
		return c.Last()
	}
	return m
}

// FallsBack reports whether a failure of this kind is worth another model:
// the trouble is with the endpoint, the key, the model's window or the
// cap on paid models, and not with the request itself.
func FallsBack(kind seam.ErrorKind) bool {
	switch kind {
	case seam.ErrAuth, seam.ErrRateLimited, seam.ErrOverloaded, seam.ErrUnavailable, seam.ErrTooLong, seam.ErrSpendCap:
		return true
	}
	return false
}

// Stream sends the request to the chain's models in order, as seam.Provider
// describes. The request's model name is replaced by each link's own.
func (c *Chain) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	for i, link := range c.links {
		sent := For(link.Model, req, i > 0)
		att := Attempt{Model: link.Model, Began: time.Now()}
		c.setLast(link.Model)

		var failure *seam.Error
		if c.hooks.Gate != nil {
			if failure = c.hooks.Gate(ctx, link.Model, sent); failure != nil {
				att.Refused = true
			}
		}
		if failure == nil {
			// The start of a stream and its error are held back until the
			// first block arrives. Until then nothing of the reply has
			// reached the caller, and the next model can take the call.
			var held []seam.Event
			passedOn := false
			err := link.Provider.Stream(ctx, sent, func(e seam.Event) error {
				if !passedOn {
					if e.Type == seam.EventStart || e.Type == seam.EventError {
						held = append(held, e)
						return nil
					}
					passedOn = true
					for _, h := range held {
						if err := emit(h); err != nil {
							return err
						}
					}
					held = nil
				}
				return emit(e)
			})
			if err == nil {
				return nil
			}
			if passedOn || ctx.Err() != nil || !errors.As(err, &failure) {
				// Part of the reply is out, the call was stopped, or the
				// caller's own emit failed. The error event, if the
				// provider sent one, went with the rest.
				if !passedOn && ctx.Err() == nil {
					for _, h := range held {
						if emitErr := emit(h); emitErr != nil {
							return emitErr
						}
					}
				}
				return err
			}
		}

		att.Error, att.Duration = failure, time.Since(att.Began)
		if i+1 == len(c.links) || !FallsBack(failure.Kind) {
			if err := emit(seam.Event{Type: seam.EventError, Error: failure}); err != nil {
				return err
			}
			return failure
		}
		if c.hooks.Moved != nil {
			next := c.links[i+1].Model
			if err := c.hooks.Moved(ctx, att, next, For(next, req, true)); err != nil {
				return err
			}
		}
	}
	return errors.New("routing: the chain has no links") // NewChain refuses one
}

// CountTokens asks the first link's provider for its own count of a
// request's input, when it offers one. It makes a chain a budget.Counter,
// so a caller that packs a context for the model picked still gets that
// model's count.
func (c *Chain) CountTokens(ctx context.Context, req seam.Request) (int, error) {
	counter, ok := c.links[0].Provider.(interface {
		CountTokens(context.Context, seam.Request) (int, error)
	})
	if !ok {
		return 0, errors.New("routing: the model's provider does not count tokens")
	}
	return counter.CountTokens(ctx, For(c.links[0].Model, req, false))
}

// For returns the request as one model of a route is sent it: under that
// model's name, asking for no more reply than the model writes, and for no
// thinking from a model that has none. other says the request was built
// for another model. Thinking blocks are then left out, because a
// thinking block goes back only to the model that wrote it. A message
// left empty is dropped, and two in a row from one side become one.
func For(m models.Model, req seam.Request, other bool) seam.Request {
	req.Model = m.Name
	if req.MaxOutputTokens > m.OutputLimit {
		req.MaxOutputTokens = m.OutputLimit
	}
	if !m.Has(models.CapThinking) {
		req.ThinkingTokens = 0
	}
	if !other {
		return req
	}
	var out []seam.Message
	for _, msg := range req.Messages {
		var blocks []seam.Block
		for _, b := range msg.Blocks {
			if b.Type != seam.BlockThinking {
				blocks = append(blocks, b)
			}
		}
		if len(blocks) == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Role == msg.Role {
			out[n-1].Blocks = append(append([]seam.Block(nil), out[n-1].Blocks...), blocks...)
			continue
		}
		out = append(out, seam.Message{Role: msg.Role, Blocks: blocks})
	}
	req.Messages = out
	return req
}
