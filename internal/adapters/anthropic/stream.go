package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// errIdle is the cause given to a call that is given up because its stream
// went quiet.
var errIdle = errors.New("the stream sent nothing for too long")

// attempt makes one Messages call and passes its reply on. The error is
// the caller's own: the context's, or the one emit returned. Everything
// the endpoint or the network did wrong is in the result.
func (p *Provider) attempt(ctx context.Context, body []byte, emit func(seam.Event) error) (result, error) {
	callCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	resp, fault := p.post(callCtx, p.messagesURL, body, "text/event-stream")
	if fault != nil {
		if err := ctx.Err(); err != nil {
			return result{}, err
		}
		return result{fault: fault}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fault, after := p.statusFault(resp)
		if err := ctx.Err(); err != nil {
			return result{}, err
		}
		return result{fault: fault, after: after}, nil
	}

	// The watchdog ends a call whose stream stops sending. Every read
	// that brings something winds it back.
	var watchdog *time.Timer
	if p.opts.IdleTimeout > 0 {
		watchdog = time.AfterFunc(p.opts.IdleTimeout, func() { cancel(errIdle) })
		defer watchdog.Stop()
	}
	events := newEventReader(&watched{r: resp.Body, timer: watchdog, wait: p.opts.IdleTimeout})

	t := translator{model: p.model.Name}
	var res result
	for {
		ev, readErr := events.next()
		if readErr != nil {
			if err := ctx.Err(); err != nil {
				return result{}, err
			}
			res.fault = &seam.Error{Kind: seam.ErrUnavailable, Retryable: true, Message: "the reply broke off before it was complete"}
			if context.Cause(callCtx) == errIdle {
				res.fault.Message = "the endpoint sent nothing for " + p.opts.IdleTimeout.String() + ", and the call was given up"
			}
			return res, nil
		}
		out, done, fault := t.take(ev)
		if fault != nil {
			fault.Message = p.clean(fault.Message)
			res.fault = fault
			return res, nil
		}
		for _, e := range out {
			if err := ctx.Err(); err != nil {
				return result{}, err
			}
			res.started = true
			if err := emit(e); err != nil {
				return result{}, err
			}
		}
		if done {
			return res, nil
		}
	}
}

// count makes one token count call.
func (p *Provider) count(ctx context.Context, body []byte) (int, result, error) {
	resp, fault := p.post(ctx, p.countURL, body, "application/json")
	if fault != nil {
		if err := ctx.Err(); err != nil {
			return 0, result{}, err
		}
		return 0, result{fault: fault}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fault, after := p.statusFault(resp)
		if err := ctx.Err(); err != nil {
			return 0, result{}, err
		}
		return 0, result{fault: fault, after: after}, nil
	}
	var reply struct {
		InputTokens *int `json:"input_tokens"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || json.Unmarshal(raw, &reply) != nil || reply.InputTokens == nil || *reply.InputTokens < 0 {
		if err := ctx.Err(); err != nil {
			return 0, result{}, err
		}
		return 0, result{fault: &seam.Error{Kind: seam.ErrOther, Message: "the endpoint's token count could not be read"}}, nil
	}
	return *reply.InputTokens, result{}, nil
}

// watched winds the watchdog back each time a read brings bytes.
type watched struct {
	r     io.Reader
	timer *time.Timer
	wait  time.Duration
	mu    sync.Mutex
}

func (w *watched) Read(b []byte) (int, error) {
	n, err := w.r.Read(b)
	if n > 0 && w.timer != nil {
		w.mu.Lock()
		w.timer.Reset(w.wait)
		w.mu.Unlock()
	}
	return n, err
}

// serverEvent is one server-sent event: its name and its data.
type serverEvent struct {
	name string
	data []byte
}

// eventReader reads server-sent events from a stream. This is the
// adapter's own copy: each adapter keeps its reader in its own folder.
type eventReader struct {
	r *bufio.Reader
}

func newEventReader(r io.Reader) *eventReader {
	return &eventReader{r: bufio.NewReaderSize(r, 32<<10)}
}

// mostEventBytes is the most one event may hold. A tool call's input comes
// in pieces, so no honest event is near it.
const mostEventBytes = 32 << 20

// next returns the next event that carries data. It returns io.EOF when
// the stream ends. An event the stream ends in the middle of is dropped,
// as the format says.
func (e *eventReader) next() (serverEvent, error) {
	var ev serverEvent
	has := false
	for {
		line, err := e.r.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return serverEvent{}, io.EOF
			}
			return serverEvent{}, err
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			if has {
				return ev, nil
			}
			ev = serverEvent{}
			continue
		}
		if line[0] == ':' {
			continue // a comment, sent to keep the connection open
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			ev.name = string(value)
		case "data":
			if has {
				ev.data = append(ev.data, '\n')
			}
			ev.data = append(ev.data, value...)
			has = true
			if len(ev.data) > mostEventBytes {
				return serverEvent{}, errors.New("one event is larger than the adapter reads")
			}
		}
	}
}

// The reply, in the provider's format. Only the fields Keystone reads.

type wireEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage wireUsage `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
		Data      string `json:"data"`
		ID        string `json:"id"`
		Name      string `json:"name"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *wireUsage `json:"usage"`
	Error *wireError `json:"error"`
}

// wireUsage holds pointers because a later event repeats only the counts
// that changed.
type wireUsage struct {
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
}

type wireError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// translator turns the provider's events into the seam's, one at a time.
// The provider numbers every block it sends. The seam numbers only the
// ones it has a type for, so the translator keeps its own count.
type translator struct {
	model string

	started bool
	next    int // the seam index of the next block

	open      bool
	openWire  int            // the provider's index of the open block
	openKind  seam.BlockType // empty for a block the seam has no type for
	toolCalls int

	usage seam.Usage
	stop  seam.StopReason
}

func malformed(why string) *seam.Error {
	return &seam.Error{Kind: seam.ErrOther, Message: "the endpoint's stream was not in order: " + why}
}

// take reads one event. It returns the seam events to pass on, whether the
// reply is complete, and the fault that ends the stream if there is one.
func (t *translator) take(ev serverEvent) (out []seam.Event, done bool, fault *seam.Error) {
	var w wireEvent
	if err := json.Unmarshal(ev.data, &w); err != nil {
		return nil, false, &seam.Error{Kind: seam.ErrOther, Message: "the endpoint's stream held an event that is not JSON"}
	}
	if w.Type == "" {
		w.Type = ev.name
	}
	switch w.Type {
	case "ping":
		return nil, false, nil
	case "error":
		e := wireError{}
		if w.Error != nil {
			e = *w.Error
		}
		kind, retryable := classify(0, e.Type, e.Message)
		return nil, false, &seam.Error{Kind: kind, Retryable: retryable, Message: describe(0, e, "")}
	case "message_start":
		if t.started {
			return nil, false, malformed("it began twice")
		}
		t.started = true
		if w.Message != nil {
			t.count(w.Message.Usage)
		}
		return []seam.Event{{Type: seam.EventStart, Model: t.model}}, false, nil
	}
	if !t.started {
		return nil, false, malformed("a " + w.Type + " event came before the start")
	}
	i := t.next - 1 // the seam index of the open block
	switch w.Type {
	case "content_block_start":
		if t.open {
			return nil, false, malformed("a block opened while another was open")
		}
		if w.ContentBlock == nil {
			return nil, false, malformed("a block opened with nothing in it")
		}
		t.open, t.openWire, t.openKind = true, w.Index, ""
		b := w.ContentBlock
		var opening seam.Block
		var first seam.Event
		switch b.Type {
		case "text":
			opening = seam.Block{Type: seam.BlockText}
			first.Text = b.Text
		case "thinking":
			opening = seam.Block{Type: seam.BlockThinking}
			first.Text, first.Signature = b.Thinking, b.Signature
		case "redacted_thinking":
			opening = seam.Block{Type: seam.BlockThinking}
			first.Signature = redactedMark + b.Data
		case "tool_use":
			if b.ID == "" || b.Name == "" {
				return nil, false, malformed("a tool call opened without an id or a name")
			}
			opening = seam.Block{Type: seam.BlockToolCall, ID: b.ID, Name: b.Name}
			t.toolCalls++
		default:
			// A kind of block the seam has no type for, such as one of
			// the provider's own tools. It is read past.
			return nil, false, nil
		}
		t.openKind = opening.Type
		i = t.next
		t.next++
		out = append(out, seam.Event{Type: seam.EventBlockStart, Index: i, Block: &opening})
		if first.Text != "" || first.Signature != "" {
			first.Type, first.Index = seam.EventBlockDelta, i
			out = append(out, first)
		}
		return out, false, nil

	case "content_block_delta":
		if !t.open || w.Index != t.openWire {
			return nil, false, malformed("a delta came for a block that is not open")
		}
		if t.openKind == "" || w.Delta == nil {
			return nil, false, nil
		}
		e := seam.Event{Type: seam.EventBlockDelta, Index: i}
		d := w.Delta
		switch d.Type {
		case "text_delta":
			if t.openKind != seam.BlockText {
				return nil, false, malformed("text came for a block that is not text")
			}
			e.Text = d.Text
		case "thinking_delta":
			if t.openKind != seam.BlockThinking {
				return nil, false, malformed("thinking came for a block that is not thinking")
			}
			e.Text = d.Thinking
		case "signature_delta":
			if t.openKind != seam.BlockThinking {
				return nil, false, malformed("a signature came for a block that is not thinking")
			}
			e.Signature = d.Signature
		case "input_json_delta":
			if t.openKind != seam.BlockToolCall {
				return nil, false, malformed("tool input came for a block that is not a tool call")
			}
			e.Input = d.PartialJSON
		}
		if e.Text == "" && e.Signature == "" && e.Input == "" {
			return nil, false, nil // an empty piece, or a kind the seam has no place for
		}
		return []seam.Event{e}, false, nil

	case "content_block_stop":
		if !t.open || w.Index != t.openWire {
			return nil, false, malformed("a block closed that is not open")
		}
		t.open = false
		if t.openKind == "" {
			return nil, false, nil
		}
		return []seam.Event{{Type: seam.EventBlockStop, Index: i}}, false, nil

	case "message_delta":
		if w.Delta != nil && w.Delta.StopReason != "" {
			t.stop = stopReason(w.Delta.StopReason)
		}
		if w.Usage != nil {
			t.count(*w.Usage)
		}
		return nil, false, nil

	case "message_stop":
		if t.open {
			return nil, false, malformed("it stopped while a block was open")
		}
		stop := t.stop
		if stop == "" || (stop == seam.StopToolCall && t.toolCalls == 0) {
			stop = seam.StopEnd
		}
		usage := t.usage
		return []seam.Event{{Type: seam.EventStop, StopReason: stop, Usage: &usage}}, true, nil
	}
	// An event this adapter does not know is one a later version added.
	return nil, false, nil
}

// count takes the token counts an event carries. The provider's input
// count already leaves out what was read from and written to the cache,
// which is how the seam counts.
func (t *translator) count(u wireUsage) {
	set := func(to *int, from *int) {
		if from != nil && *from >= 0 {
			*to = *from
		}
	}
	set(&t.usage.InputTokens, u.InputTokens)
	set(&t.usage.OutputTokens, u.OutputTokens)
	set(&t.usage.CacheWriteTokens, u.CacheCreationInputTokens)
	set(&t.usage.CacheReadTokens, u.CacheReadInputTokens)
}

func stopReason(s string) seam.StopReason {
	switch s {
	case "tool_use":
		return seam.StopToolCall
	case "max_tokens", "model_context_window_exceeded":
		return seam.StopMaxTokens
	case "refusal":
		return seam.StopRefused
	}
	// end_turn, stop_sequence, pause_turn and anything new.
	return seam.StopEnd
}
