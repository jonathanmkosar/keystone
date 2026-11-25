package seam

import (
	"encoding/json"
	"fmt"
)

// EventType names one kind of stream event.
type EventType string

const (
	// EventStart opens a stream.
	EventStart EventType = "start"
	// EventBlockStart opens the block at Index.
	EventBlockStart EventType = "block_start"
	// EventBlockDelta adds to the open block at Index.
	EventBlockDelta EventType = "block_delta"
	// EventBlockStop closes the block at Index.
	EventBlockStop EventType = "block_stop"
	// EventStop ends a stream that worked. It carries the reason and the usage.
	EventStop EventType = "stop"
	// EventError ends a stream that failed.
	EventError EventType = "error"
)

// StopReason says why the model stopped.
type StopReason string

const (
	// StopEnd: the model finished its answer.
	StopEnd StopReason = "end"
	// StopToolCall: the model asked for one or more tools.
	StopToolCall StopReason = "tool_call"
	// StopMaxTokens: the reply reached its output limit.
	StopMaxTokens StopReason = "max_tokens"
	// StopRefused: the model or the provider declined to answer.
	StopRefused StopReason = "refused"
)

// ErrorKind sorts a failed call, so routing can decide whether to retry,
// fall back or give up.
type ErrorKind string

const (
	// ErrAuth: the key is missing, wrong or not allowed to use the model.
	ErrAuth ErrorKind = "auth"
	// ErrRateLimited: the provider asked Keystone to slow down.
	ErrRateLimited ErrorKind = "rate_limited"
	// ErrOverloaded: the provider is too busy.
	ErrOverloaded ErrorKind = "overloaded"
	// ErrTooLong: the request does not fit the model's context window.
	ErrTooLong ErrorKind = "too_long"
	// ErrInvalid: the provider refused the request as malformed.
	ErrInvalid ErrorKind = "invalid"
	// ErrUnavailable: the endpoint could not be reached, or it failed.
	ErrUnavailable ErrorKind = "unavailable"
	// ErrOther: anything else.
	ErrOther ErrorKind = "other"
)

var errorKinds = []ErrorKind{ErrAuth, ErrRateLimited, ErrOverloaded, ErrTooLong, ErrInvalid, ErrUnavailable, ErrOther}

// Error is a failed model call. The message must never hold a key.
type Error struct {
	Kind    ErrorKind `json:"kind"`
	Message string    `json:"message"`
	// Retryable says the same call may work if sent again.
	Retryable bool `json:"retryable,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("model call failed (%s): %s", e.Kind, e.Message)
}

// Event is one step of a streamed reply. Type says which fields are used:
//
//	start        Model
//	block_start  Index, Block
//	block_delta  Index, and Text, Input or Signature
//	block_stop   Index
//	stop         StopReason, Usage
//	error        Error
//
// A stream is one start, then blocks opened and closed in order of Index
// from zero, then one stop. An error may come at any point and ends it.
type Event struct {
	Type EventType `json:"type"`

	// Model is the registry name of the model that answers.
	Model string `json:"model,omitempty"`

	// Index is the block's place in the reply.
	Index int `json:"index,omitempty"`
	// Block opens a text, thinking or tool call block. A tool call carries
	// its id and name here. Its input arrives in the deltas.
	Block *Block `json:"block,omitempty"`

	// Text is added to the text of a text or thinking block.
	Text string `json:"text,omitempty"`
	// Input is the next piece of a tool call's input, as JSON text. The
	// pieces are whole only when joined.
	Input string `json:"input,omitempty"`
	// Signature is added to a thinking block's signature.
	Signature string `json:"signature,omitempty"`

	StopReason StopReason `json:"stop_reason,omitempty"`
	Usage      *Usage     `json:"usage,omitempty"`

	Error *Error `json:"error,omitempty"`
}

// Response is a whole reply: what a finished stream adds up to.
type Response struct {
	Model      string     `json:"model,omitempty"`
	Message    Message    `json:"message"`
	StopReason StopReason `json:"stop_reason"`
	Usage      Usage      `json:"usage"`
}

// ValidateEvent checks that an event has the fields its type needs and no
// others.
func ValidateEvent(e Event) error {
	var used struct{ model, index, block, delta, stop, err bool }
	switch e.Type {
	case EventStart:
		used.model = true
	case EventBlockStart:
		used.index, used.block = true, true
		if e.Block == nil {
			return fmt.Errorf("a block_start event has no block")
		}
		if err := validateOpening(*e.Block); err != nil {
			return fmt.Errorf("block_start %d: %w", e.Index, err)
		}
	case EventBlockDelta:
		used.index, used.delta = true, true
		if e.Text == "" && e.Input == "" && e.Signature == "" {
			return fmt.Errorf("block_delta %d adds nothing", e.Index)
		}
	case EventBlockStop:
		used.index = true
	case EventStop:
		used.stop = true
		if err := validateStop(e.StopReason); err != nil {
			return err
		}
		if e.Usage == nil {
			return fmt.Errorf("a stop event has no usage")
		}
		if err := ValidateUsage(*e.Usage); err != nil {
			return err
		}
	case EventError:
		used.err = true
		if e.Error == nil {
			return fmt.Errorf("an error event has no error")
		}
		if err := validateError(*e.Error); err != nil {
			return err
		}
	default:
		return fmt.Errorf("event type %q is not known", e.Type)
	}
	stray := ""
	switch {
	case !used.model && e.Model != "":
		stray = "a model"
	case !used.index && e.Index != 0:
		stray = "an index"
	case used.index && e.Index < 0:
		return fmt.Errorf("%s: index %d is negative", e.Type, e.Index)
	case !used.block && e.Block != nil:
		stray = "a block"
	case !used.delta && (e.Text != "" || e.Input != "" || e.Signature != ""):
		stray = "a delta"
	case !used.stop && (e.StopReason != "" || e.Usage != nil):
		stray = "a stop reason or usage"
	case !used.err && e.Error != nil:
		stray = "an error"
	}
	if stray != "" {
		return fmt.Errorf("a %s event cannot carry %s", e.Type, stray)
	}
	return nil
}

// ValidateResponse checks a whole reply: an assistant message of text,
// thinking and tool calls, a known stop
// reason and a sound usage. A reply that stopped for tools must hold a tool
// call.
func ValidateResponse(r Response) error {
	if r.Message.Role != RoleAssistant {
		return fmt.Errorf("a response is an assistant message, and this one's role is %q", r.Message.Role)
	}
	// A reply may be empty, when the model stops without saying anything.
	if len(r.Message.Blocks) > 0 {
		if err := ValidateMessage(r.Message); err != nil {
			return err
		}
	}
	for i, b := range r.Message.Blocks {
		if b.Type != BlockText && b.Type != BlockThinking && b.Type != BlockToolCall {
			return fmt.Errorf("block %d: a reply holds text, thinking and tool calls, not a %s block", i, b.Type)
		}
	}
	if err := validateStop(r.StopReason); err != nil {
		return err
	}
	if r.StopReason == StopToolCall && len(ToolCalls(r.Message)) == 0 {
		return fmt.Errorf("the reply stopped for a tool call and holds none")
	}
	return ValidateUsage(r.Usage)
}

// ToolCalls lists the tool call blocks in a message, in order.
func ToolCalls(m Message) []Block {
	var calls []Block
	for _, b := range m.Blocks {
		if b.Type == BlockToolCall {
			calls = append(calls, b)
		}
	}
	return calls
}

// Text joins the text blocks of a message. Thinking is left out.
func Text(m Message) string {
	s := ""
	for _, b := range m.Blocks {
		if b.Type == BlockText {
			s += b.Text
		}
	}
	return s
}

// validateOpening checks the block of a block_start event. Its content is
// still to come, so only the type and a tool call's id and name are set.
func validateOpening(b Block) error {
	switch b.Type {
	case BlockText, BlockThinking:
		if !blockIsOnlyType(b) {
			return fmt.Errorf("an opening %s block is empty, and its content comes in deltas", b.Type)
		}
	case BlockToolCall:
		if b.ID == "" || b.Name == "" {
			return fmt.Errorf("an opening tool call needs an id and a name")
		}
		b.ID, b.Name = "", ""
		if !blockIsOnlyType(b) {
			return fmt.Errorf("an opening tool call carries its id and name only, and its input comes in deltas")
		}
	default:
		return fmt.Errorf("a stream cannot open a %s block", b.Type)
	}
	return nil
}

// blockIsOnlyType reports whether every field but Type is empty.
func blockIsOnlyType(b Block) bool {
	return b.Text == "" && b.Signature == "" && b.MediaType == "" && len(b.Data) == 0 && b.Ref == "" &&
		b.Name == "" && b.ID == "" && len(b.Input) == 0 && len(b.Content) == 0 && !b.IsError
}

func validateStop(s StopReason) error {
	switch s {
	case StopEnd, StopToolCall, StopMaxTokens, StopRefused:
		return nil
	}
	return fmt.Errorf("stop reason %q is not known", s)
}

func validateError(e Error) error {
	if e.Message == "" {
		return fmt.Errorf("an error needs a message")
	}
	for _, k := range errorKinds {
		if e.Kind == k {
			return nil
		}
	}
	return fmt.Errorf("error kind %q is not known", e.Kind)
}

// Collector folds a stream into a Response, one event at a time. The zero
// value is ready to use.
type Collector struct {
	started bool
	done    bool
	open    bool   // the last block is still open
	next    int    // the index the next block must open with
	input   string // the open tool call's input so far
	resp    Response
}

// Add takes the next event. It returns an error when the event is malformed
// or out of order, and the stream's own *Error when the event is an error
// event. After an error the collector takes no more events.
func (c *Collector) Add(e Event) error {
	if c.done {
		return fmt.Errorf("the stream has ended, and a %s event came after it", e.Type)
	}
	if err := ValidateEvent(e); err != nil {
		c.done = true
		return err
	}
	if err := c.add(e); err != nil {
		c.done = true
		return err
	}
	return nil
}

func (c *Collector) add(e Event) error {
	if e.Type == EventError {
		return e.Error
	}
	if !c.started {
		if e.Type != EventStart {
			return fmt.Errorf("a stream begins with a start event, and this one begins with %s", e.Type)
		}
		c.started = true
		c.resp.Model = e.Model
		c.resp.Message.Role = RoleAssistant
		return nil
	}
	blocks := c.resp.Message.Blocks
	last := len(blocks) - 1
	switch e.Type {
	case EventStart:
		return fmt.Errorf("a second start event")
	case EventBlockStart:
		if c.open {
			return fmt.Errorf("block %d opened while block %d is still open", e.Index, c.next-1)
		}
		if e.Index != c.next {
			return fmt.Errorf("block %d opened, and the next block is %d", e.Index, c.next)
		}
		c.resp.Message.Blocks = append(blocks, *e.Block)
		c.open, c.input = true, ""
		c.next++
	case EventBlockDelta:
		if !c.open || e.Index != c.next-1 {
			return fmt.Errorf("a delta for block %d, which is not open", e.Index)
		}
		b := &blocks[last]
		if e.Input != "" && b.Type != BlockToolCall {
			return fmt.Errorf("block %d is a %s block and cannot take tool input", e.Index, b.Type)
		}
		if e.Text != "" && b.Type == BlockToolCall {
			return fmt.Errorf("block %d is a tool call and cannot take text", e.Index)
		}
		if e.Signature != "" && b.Type != BlockThinking {
			return fmt.Errorf("block %d is a %s block and cannot take a signature", e.Index, b.Type)
		}
		b.Text += e.Text
		b.Signature += e.Signature
		c.input += e.Input
	case EventBlockStop:
		if !c.open || e.Index != c.next-1 {
			return fmt.Errorf("a stop for block %d, which is not open", e.Index)
		}
		c.open = false
		b := &blocks[last]
		if b.Type != BlockToolCall && b.Text == "" && b.Signature == "" {
			// Some providers open a block and send nothing. It is dropped.
			c.resp.Message.Blocks = blocks[:last]
			return nil
		}
		if b.Type == BlockToolCall {
			// A tool with no arguments may send no input at all.
			if c.input == "" {
				c.input = "{}"
			}
			b.Input = json.RawMessage(c.input)
		}
		if err := ValidateBlock(*b); err != nil {
			return fmt.Errorf("block %d: %w", e.Index, err)
		}
	case EventStop:
		if c.open {
			return fmt.Errorf("the stream stopped while block %d is still open", c.next-1)
		}
		c.resp.StopReason = e.StopReason
		c.resp.Usage = *e.Usage
		c.done = true
		return ValidateResponse(c.resp)
	}
	return nil
}

// Response returns the whole reply. It fails until the stop event has been
// taken.
func (c *Collector) Response() (Response, error) {
	if !c.done || c.resp.StopReason == "" {
		return Response{}, fmt.Errorf("the stream has not reached its stop event")
	}
	return c.resp, nil
}

// Partial returns what has arrived so far: the closed blocks, and the text
// of the open one. A turn that is cancelled or fails keeps this much.
func (c *Collector) Partial() Message {
	m := Message{Role: RoleAssistant}
	for i, b := range c.resp.Message.Blocks {
		if c.open && i == len(c.resp.Message.Blocks)-1 && ValidateBlock(b) != nil {
			// An open tool call has no whole input yet, and an open text
			// block may still be empty.
			continue
		}
		m.Blocks = append(m.Blocks, b)
	}
	return m
}

// Collect folds a whole stream into a Response.
func Collect(events []Event) (Response, error) {
	var c Collector
	for i, e := range events {
		if err := c.Add(e); err != nil {
			if _, model := err.(*Error); model {
				return Response{}, err
			}
			return Response{}, fmt.Errorf("event %d: %w", i, err)
		}
	}
	return c.Response()
}

// Events turns a whole reply into the shortest stream that collects back to
// it: one delta for each block. The fake provider and the adapters for
// providers that do not stream build on it.
func Events(r Response) ([]Event, error) {
	if err := ValidateResponse(r); err != nil {
		return nil, err
	}
	events := []Event{{Type: EventStart, Model: r.Model}}
	for i, b := range r.Message.Blocks {
		opening := Block{Type: b.Type}
		delta := Event{Type: EventBlockDelta, Index: i}
		switch b.Type {
		case BlockText:
			delta.Text = b.Text
		case BlockThinking:
			delta.Text, delta.Signature = b.Text, b.Signature
		case BlockToolCall:
			opening.ID, opening.Name = b.ID, b.Name
			delta.Input = string(b.Input)
		}
		events = append(events,
			Event{Type: EventBlockStart, Index: i, Block: &opening},
			delta,
			Event{Type: EventBlockStop, Index: i})
	}
	usage := r.Usage
	return append(events, Event{Type: EventStop, StopReason: r.StopReason, Usage: &usage}), nil
}
