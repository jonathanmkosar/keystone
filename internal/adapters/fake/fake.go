// Package fake is a scripted model. It is a seam.Provider that needs no
// network and no keys: it answers each request from a set of scripts, which
// are read from fixture files. The tests, verify and the test tenant use it
// in place of a real model.
//
// It keeps no memory of earlier calls, so the same request always gets the
// same stream. docs/architecture/fake-provider.md has the script format.
package fake

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/settings"
)

// Options says how a reply is streamed. The zero value sends each block in
// one piece with no wait, which is what tests want.
type Options struct {
	// DeltaSize is how many characters go in each piece of text, thinking
	// or tool input. Zero sends each block whole.
	DeltaSize int
	// Delay is how long the fake waits before each piece.
	Delay time.Duration
}

// DefaultOptions reads the options from the defaults in the settings
// registry: fake.delta_size and fake.delta_delay.
func DefaultOptions() (Options, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Options{}, err
	}
	size, ok := settings.Lookup(reg, "fake.delta_size")
	delay, ok2 := settings.Lookup(reg, "fake.delta_delay")
	if !ok || !ok2 {
		return Options{}, errors.New("fake.delta_size or fake.delta_delay is missing from the settings registry")
	}
	var opts Options
	if opts.DeltaSize, err = strconv.Atoi(size.Default); err != nil {
		return Options{}, err
	}
	if opts.Delay, err = time.ParseDuration(delay.Default); err != nil {
		return Options{}, err
	}
	return opts, nil
}

// Provider plays a set of scripts. It holds nothing that changes, so it is
// safe to use from several goroutines at once.
type Provider struct {
	scripts []Script
	opts    Options
}

// New checks the scripts and returns a provider that plays them. Their
// order matters only when two scripts match a request equally well.
func New(scripts []Script, opts Options) (*Provider, error) {
	if opts.DeltaSize < 0 || opts.Delay < 0 {
		return nil, errors.New("the delta size and the delay cannot be negative")
	}
	scripts, err := prepare(scripts)
	if err != nil {
		return nil, err
	}
	if err := checkSet(scripts); err != nil {
		return nil, err
	}
	return &Provider{scripts: scripts, opts: opts}, nil
}

// Stream answers one request, as seam.Provider describes. A request no
// script answers, or one a real provider would refuse, ends with an error
// event of kind invalid.
func (p *Provider) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	events := p.events(req)
	for _, e := range events {
		if e.Type == seam.EventBlockDelta && p.opts.Delay > 0 {
			wait := time.NewTimer(p.opts.Delay)
			select {
			case <-ctx.Done():
				wait.Stop()
				return ctx.Err()
			case <-wait.C:
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(e); err != nil {
			return err
		}
	}
	if last := events[len(events)-1]; last.Type == seam.EventError {
		return last.Error
	}
	return nil
}

// events works out the whole stream for a request. It always ends with a
// stop event or an error event.
func (p *Provider) events(req seam.Request) []seam.Event {
	step, err := p.step(req)
	if err != nil {
		return []seam.Event{failed(refused(err))}
	}
	if step.Reply == nil {
		failure := *step.Error
		return []seam.Event{failed(&failure)}
	}
	events, err := seam.Events(fill(*step.Reply, req))
	if err != nil {
		return []seam.Event{failed(refused(err))}
	}
	events = split(events, p.opts.DeltaSize)
	if step.CutAfter == nil {
		return events
	}
	// What a dropped connection looks like through the seam.
	failure := seam.Error{Kind: seam.ErrUnavailable, Message: "the stream ended before its stop event", Retryable: true}
	if step.Error != nil {
		failure = *step.Error
	}
	return cut(events, *step.CutAfter, &failure)
}

// step finds the script that answers a request, and the step of it the
// request has reached.
func (p *Provider) step(req seam.Request) (Step, error) {
	if err := seam.ValidateRequest(req); err != nil {
		return Step{}, fmt.Errorf("the request is not valid: %w", err)
	}
	msgs := req.Messages
	if err := answered(msgs); err != nil {
		return Step{}, err
	}
	// The script is chosen by the last message the person wrote: the last
	// user message that is not a set of tool results.
	at := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == seam.RoleUser && !slices.ContainsFunc(msgs[i].Blocks, isToolResult) {
			at = i
			break
		}
	}
	if at < 0 {
		return Step{}, errors.New("the request holds no message from the person")
	}
	// Each assistant message since then is one step already played.
	played := 0
	for _, m := range msgs[at+1:] {
		if m.Role == seam.RoleAssistant {
			played++
		}
	}
	user := fold(seam.Text(msgs[at]))
	// The context builder puts recalled knowledge before the person's words
	// in the same message (P5-03), so their own words are the message's
	// last text block too.
	own := ""
	for _, b := range msgs[at].Blocks {
		if b.Type == seam.BlockText {
			own = fold(b.Text)
		}
	}
	var system []string
	for _, b := range req.System {
		system = append(system, b.Text)
	}
	s, ok := p.match(user, own, fold(strings.Join(system, "\n")), req.Model)
	if !ok {
		return Step{}, fmt.Errorf("no script answers a request whose last user text is %q", short(user))
	}
	if played >= len(s.Steps) {
		return Step{}, fmt.Errorf("script %q has no step %d, and the request has reached it", s.Name, played+1)
	}
	return s.Steps[played], nil
}

// match picks the script whose conditions all hold. The one with the most
// conditions wins, and a tie goes to the earlier script.
func (p *Provider) match(user, own, system, model string) (Script, bool) {
	best, most := -1, -1
	for i, s := range p.scripts {
		if n, ok := s.When.holds(user, own, system, model); ok && n > most {
			best, most = i, n
		}
	}
	if best < 0 {
		return Script{}, false
	}
	return p.scripts[best], true
}

// holds reports whether every condition that is set is met, and how many
// are set. The whole text is user; own is its last text block, which the
// whole text of a script may match as well.
func (w When) holds(user, own, system, model string) (int, bool) {
	n := 0
	if t := fold(w.UserText); t != "" {
		if user != t && own != t {
			return 0, false
		}
		n++
	}
	if t := fold(w.UserTextContains); t != "" {
		if !strings.Contains(user, t) {
			return 0, false
		}
		n++
	}
	if t := fold(w.SystemContains); t != "" {
		if !strings.Contains(system, t) {
			return 0, false
		}
		n++
	}
	if w.Model != "" {
		if model != w.Model {
			return 0, false
		}
		n++
	}
	return n, true
}

// answered refuses what every real provider refuses: a request that does
// not end with a user message, and a tool call with no result in the
// message after it.
func answered(msgs []seam.Message) error {
	last := len(msgs) - 1
	if msgs[last].Role != seam.RoleUser {
		return errors.New("the last message is the assistant's, so there is nothing to answer")
	}
	for i, m := range msgs[:last] {
		if m.Role != seam.RoleAssistant {
			continue
		}
		for _, call := range seam.ToolCalls(m) {
			hasResult := func(b seam.Block) bool { return isToolResult(b) && b.ID == call.ID }
			if !slices.ContainsFunc(msgs[i+1].Blocks, hasResult) {
				return fmt.Errorf("tool call %s has no result in the message after it", call.ID)
			}
		}
	}
	return nil
}

func isToolResult(b seam.Block) bool { return b.Type == seam.BlockToolResult }

// fill completes a scripted reply for one request. A reply with no model
// takes the request's. A tool call keeps its scripted id unless the request
// already holds that id, as it does when a script is used twice in one
// chat. Then it gets -2, -3 and so on, so the next request stays valid.
func fill(r seam.Response, req seam.Request) seam.Response {
	if r.Model == "" {
		r.Model = req.Model
	}
	used := map[string]bool{}
	for _, m := range req.Messages {
		for _, call := range seam.ToolCalls(m) {
			used[call.ID] = true
		}
	}
	blocks := slices.Clone(r.Message.Blocks)
	for i, b := range blocks {
		if b.Type != seam.BlockToolCall {
			continue
		}
		id := b.ID
		for n := 2; used[id]; n++ {
			id = fmt.Sprintf("%s-%d", b.ID, n)
		}
		used[id] = true
		blocks[i].ID = id
	}
	r.Message.Blocks = blocks
	return r
}

// split breaks each delta into pieces of size characters. A thinking
// block's signature is opaque and stays whole.
func split(events []seam.Event, size int) []seam.Event {
	if size <= 0 {
		return events
	}
	var out []seam.Event
	for _, e := range events {
		if e.Type != seam.EventBlockDelta {
			out = append(out, e)
			continue
		}
		for _, t := range pieces(e.Text, size) {
			out = append(out, seam.Event{Type: seam.EventBlockDelta, Index: e.Index, Text: t})
		}
		if e.Signature != "" {
			out = append(out, seam.Event{Type: seam.EventBlockDelta, Index: e.Index, Signature: e.Signature})
		}
		for _, t := range pieces(e.Input, size) {
			out = append(out, seam.Event{Type: seam.EventBlockDelta, Index: e.Index, Input: t})
		}
	}
	return out
}

func pieces(s string, size int) []string {
	var out []string
	for r := []rune(s); len(r) > 0; {
		n := min(size, len(r))
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	return out
}

// cut keeps a stream up to the point where after characters of text,
// thinking and tool input have been sent, and ends it there with failure.
func cut(events []seam.Event, after int, failure *seam.Error) []seam.Event {
	var out []seam.Event
	left := after
	for _, e := range events {
		if e.Type == seam.EventStop || (e.Type == seam.EventBlockDelta && left == 0) {
			break
		}
		if e.Type != seam.EventBlockDelta {
			out = append(out, e)
			continue
		}
		whole := utf8.RuneCountInString(e.Text) + utf8.RuneCountInString(e.Signature) + utf8.RuneCountInString(e.Input)
		had := left
		e.Text = take(e.Text, &left)
		e.Signature = take(e.Signature, &left)
		e.Input = take(e.Input, &left)
		out = append(out, e)
		if had < whole {
			// The cut fell inside this piece, so nothing follows it.
			break
		}
	}
	return append(out, failed(failure))
}

// take returns as much of s as left allows, and lowers left by that much.
func take(s string, left *int) string {
	r := []rune(s)
	n := min(len(r), *left)
	*left -= n
	return string(r[:n])
}

func failed(e *seam.Error) seam.Event {
	return seam.Event{Type: seam.EventError, Error: e}
}

// refused is the error for a request the fake cannot answer. It is not
// worth sending again, because the answer would be the same.
func refused(err error) *seam.Error {
	return &seam.Error{Kind: seam.ErrInvalid, Message: "fake provider: " + err.Error()}
}

// short trims text for an error message.
func short(s string) string {
	const most = 80
	if utf8.RuneCountInString(s) <= most {
		return s
	}
	return string([]rune(s)[:most]) + "..."
}
