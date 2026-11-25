package chatcompletions

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// maxEvent is the most bytes one event of a reply may hold.
const maxEvent = 8 << 20

// readEvents reads a server-sent event stream and calls each with the data
// of one event. It stops when each returns false or the stream ends. Only
// data lines are used: comments, which some endpoints send to keep the
// connection open, and other fields are skipped.
//
// This is the adapter's own reader. It is not shared with the other
// adapters.
func readEvents(r io.Reader, each func(data []byte) bool) error {
	br := bufio.NewReaderSize(r, 32<<10)
	var data []byte
	have := false
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 || err == nil {
			line = bytes.TrimRight(line, "\r\n")
			switch {
			case len(line) == 0:
				if have {
					if !each(data) {
						return nil
					}
					data, have = nil, false
				}
			case bytes.HasPrefix(line, []byte("data:")):
				value := bytes.TrimPrefix(line[len("data:"):], []byte(" "))
				if have {
					data = append(data, '\n')
				}
				data = append(data, value...)
				have = true
				if len(data) > maxEvent {
					return errors.New("one event of the reply is too large")
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				// An event the endpoint did not end with a blank line.
				if have {
					each(data)
				}
				return nil
			}
			return err
		}
	}
}

// The reply as Chat Completions sends it. A streamed piece carries a delta,
// and a reply that was not streamed carries a message of the same shape.

type wireChunk struct {
	Choices []wireChoice    `json:"choices"`
	Usage   *wireUsage      `json:"usage"`
	Error   json.RawMessage `json:"error"`
}

type wireChoice struct {
	Index        int        `json:"index"`
	Delta        *wireDelta `json:"delta"`
	Message      *wireDelta `json:"message"`
	FinishReason *string    `json:"finish_reason"`
}

type wireDelta struct {
	Content loose `json:"content"`
	Refusal loose `json:"refusal"`
	// Reasoning text has two names among the endpoints of this kind.
	ReasoningContent loose           `json:"reasoning_content"`
	Reasoning        loose           `json:"reasoning"`
	ToolCalls        []wireCallDelta `json:"tool_calls"`
}

type wireCallDelta struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments loose  `json:"arguments"`
	} `json:"function"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	PromptDetails    *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// loose is text that an endpoint may send as a string, as null, or, for a
// tool call's arguments, as a JSON object. Anything else reads as empty.
type loose string

func (l *loose) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0:
		*l = ""
	case raw[0] == '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		*l = loose(s)
	case raw[0] == '{':
		*l = loose(raw)
	default:
		*l = ""
	}
	return nil
}

// call is one tool call as it has arrived so far.
type call struct {
	index int
	id    string
	name  string
	input strings.Builder
}

// reply turns the pieces of one reply into seam events. Text and thinking
// are sent on as they arrive. Tool calls are kept until the reply ends, and
// then sent one after another, because the format lets their pieces arrive
// mixed and the seam opens one block at a time.
type reply struct {
	ctx       context.Context
	emit      func(seam.Event) error
	model     string
	sentBytes int

	started bool           // the start event has gone to the caller
	next    int            // the index of the next block
	open    seam.BlockType // the block that is open, or empty
	calls   []*call
	finishR string
	refused bool
	usage   *wireUsage
	done    bool // the endpoint said the reply is complete
	wrote   int  // bytes of text, thinking and tool input received

	err     error       // what emit returned
	failure *seam.Error // the call's own failure
}

func (r *reply) send(e seam.Event) bool {
	if r.err != nil || r.failure != nil {
		return false
	}
	// A cancelled call sends nothing more, even from what has arrived.
	if r.err = r.ctx.Err(); r.err != nil {
		return false
	}
	if !r.started {
		r.started = true
		if r.err = r.emit(seam.Event{Type: seam.EventStart, Model: r.model}); r.err != nil {
			return false
		}
	}
	r.err = r.emit(e)
	return r.err == nil
}

// write adds text to a text or thinking block, opening it first if another
// kind of block, or none, is open.
func (r *reply) write(kind seam.BlockType, text string) {
	if text == "" {
		return
	}
	if r.open != kind {
		r.close()
		if !r.send(seam.Event{Type: seam.EventBlockStart, Index: r.next, Block: &seam.Block{Type: kind}}) {
			return
		}
		r.open = kind
	}
	r.wrote += len(text)
	r.send(seam.Event{Type: seam.EventBlockDelta, Index: r.next, Text: text})
}

func (r *reply) close() {
	if r.open == "" {
		return
	}
	if r.send(seam.Event{Type: seam.EventBlockStop, Index: r.next}) {
		r.open = ""
		r.next++
	}
}

// take reads one piece of the reply.
func (r *reply) take(data []byte) {
	var chunk wireChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		r.failure = &seam.Error{Kind: seam.ErrUnavailable, Retryable: true, Message: "the endpoint sent a piece of the reply that is not JSON"}
		return
	}
	if e := bytes.TrimSpace(chunk.Error); len(e) > 0 && string(e) != "null" {
		r.failure = bodyFailure(data)
		return
	}
	if chunk.Usage != nil {
		r.usage = chunk.Usage
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			// One answer is asked for, and only the first is read.
			continue
		}
		d := choice.Delta
		if d == nil {
			d = choice.Message
		}
		if d != nil {
			r.write(seam.BlockThinking, string(d.ReasoningContent))
			r.write(seam.BlockThinking, string(d.Reasoning))
			r.write(seam.BlockText, string(d.Content))
			if d.Refusal != "" {
				r.refused = true
				r.write(seam.BlockText, string(d.Refusal))
			}
			for _, tc := range d.ToolCalls {
				r.takeCall(tc)
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			r.finishR = *choice.FinishReason
		}
	}
}

// takeCall adds a piece of a tool call. Pieces of one call share an index.
// Some endpoints send every call whole with the same index and a new id,
// so a different id at an index is a new call.
func (r *reply) takeCall(tc wireCallDelta) {
	index := 0
	if tc.Index != nil {
		index = *tc.Index
	}
	var c *call
	for i := len(r.calls) - 1; i >= 0; i-- {
		if r.calls[i].index == index {
			c = r.calls[i]
			break
		}
	}
	if c == nil || (tc.ID != "" && c.id != "" && tc.ID != c.id) {
		c = &call{index: index}
		r.calls = append(r.calls, c)
	}
	if c.id == "" {
		c.id = tc.ID
	}
	if c.name == "" {
		c.name = tc.Function.Name
	}
	r.wrote += len(tc.Function.Arguments)
	c.input.WriteString(string(tc.Function.Arguments))
}

// finish ends a reply whose stream closed without a failure: it sends the
// tool calls and the stop event, or fails if the reply is not whole.
func (r *reply) finish() {
	if r.err != nil || r.failure != nil {
		return
	}
	if !r.done && r.finishR == "" {
		r.failure = &seam.Error{Kind: seam.ErrUnavailable, Retryable: true, Message: "the reply ended before the endpoint said it was complete"}
		return
	}
	r.close()

	cut := r.finishR == "length"
	sort.SliceStable(r.calls, func(i, j int) bool { return r.calls[i].index < r.calls[j].index })
	used := map[string]bool{}
	sent := 0
	for _, c := range r.calls {
		input := strings.TrimSpace(c.input.String())
		if input == "" {
			input = "{}"
		}
		whole := strings.HasPrefix(input, "{") && json.Valid([]byte(input))
		if !whole || c.name == "" {
			if cut {
				// The reply reached its limit part of the way through this
				// call, and half a call is no use.
				continue
			}
			what := "input that is not a JSON object"
			if c.name == "" {
				what = "no name"
			}
			r.failure = &seam.Error{Kind: seam.ErrOther, Retryable: true, Message: "the model wrote a tool call with " + what}
			return
		}
		id := c.id
		if id == "" || used[id] {
			id = newCallID()
		}
		used[id] = true
		ok := r.send(seam.Event{Type: seam.EventBlockStart, Index: r.next, Block: &seam.Block{Type: seam.BlockToolCall, ID: id, Name: c.name}}) &&
			r.send(seam.Event{Type: seam.EventBlockDelta, Index: r.next, Input: input}) &&
			r.send(seam.Event{Type: seam.EventBlockStop, Index: r.next})
		if !ok {
			return
		}
		r.next++
		sent++
	}

	reason := seam.StopEnd
	switch {
	case r.refused || r.finishR == "content_filter":
		reason = seam.StopRefused
	case cut:
		reason = seam.StopMaxTokens
	case sent > 0:
		// Some endpoints say "stop" for a reply that calls tools.
		reason = seam.StopToolCall
	}
	usage := r.count()
	r.send(seam.Event{Type: seam.EventStop, StopReason: reason, Usage: &usage})
}

// count turns the endpoint's usage into the seam's. The format counts
// cached input inside the prompt and reasoning inside the completion. When
// the endpoint reported nothing, the counts are a rough estimate from the
// sizes, and are marked as one.
func (r *reply) count() seam.Usage {
	if r.usage == nil {
		return seam.Usage{InputTokens: roughTokens(r.sentBytes), OutputTokens: roughTokens(r.wrote), Estimated: true}
	}
	prompt, completion := max(r.usage.PromptTokens, 0), max(r.usage.CompletionTokens, 0)
	u := seam.Usage{InputTokens: prompt, OutputTokens: completion}
	if d := r.usage.PromptDetails; d != nil {
		u.CacheReadTokens = min(max(d.CachedTokens, 0), prompt)
		u.InputTokens = prompt - u.CacheReadTokens
	}
	if d := r.usage.CompletionDetails; d != nil {
		u.ThinkingTokens = min(max(d.ReasoningTokens, 0), completion)
	}
	return u
}

// roughTokens estimates tokens from bytes, at four bytes a token.
func roughTokens(n int) int {
	return (n + 3) / 4
}

// newCallID makes an id for a tool call the endpoint gave none.
func newCallID() string {
	var b [8]byte
	rand.Read(b[:])
	return "call_" + hex.EncodeToString(b[:])
}

// maxReason is the most of the endpoint's own words put in a message.
const maxReason = 300

// statusFailure sorts a refusal by its HTTP status and its body.
func statusFailure(status int, body []byte) *seam.Error {
	code, kindOf, reason := readError(body)
	kind, retry := classify(status, code+" "+kindOf+" "+reason)
	msg := fmt.Sprintf("the endpoint answered %d", status)
	if reason != "" {
		msg += ": " + reason
	}
	return &seam.Error{Kind: kind, Retryable: retry, Message: msg}
}

// bodyFailure sorts an error that arrived inside a reply that had begun
// with a success status.
func bodyFailure(body []byte) *seam.Error {
	code, kindOf, reason := readError(body)
	status, _ := strconv.Atoi(code)
	kind, retry := classify(status, code+" "+kindOf+" "+reason)
	msg := "the endpoint reported an error in its reply"
	if reason != "" {
		msg += ": " + reason
	}
	return &seam.Error{Kind: kind, Retryable: retry, Message: msg}
}

// readError finds the code and the reason in an error body. The shapes are
// {"error": {"message", "type", "code"}}, {"error": "text"}, and
// {"message"} or {"detail"} at the top. A body that is none of them, such
// as a proxy's page, gives nothing.
func readError(body []byte) (code, kindOf, reason string) {
	var top struct {
		Error   json.RawMessage `json:"error"`
		Message loose           `json:"message"`
		Detail  loose           `json:"detail"`
	}
	if json.Unmarshal(body, &top) != nil {
		return "", "", ""
	}
	var inner struct {
		Message loose           `json:"message"`
		Type    loose           `json:"type"`
		Code    json.RawMessage `json:"code"`
	}
	var text string
	switch {
	case json.Unmarshal(top.Error, &text) == nil:
		reason = text
	case json.Unmarshal(top.Error, &inner) == nil:
		reason = string(inner.Message)
		code = strings.Trim(string(bytes.TrimSpace(inner.Code)), `"`)
		if code == "null" {
			code = ""
		}
		kindOf = string(inner.Type)
	}
	if reason == "" {
		reason = string(top.Message)
	}
	if reason == "" {
		reason = string(top.Detail)
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if r := []rune(reason); len(r) > maxReason {
		reason = string(r[:maxReason]) + "..."
	}
	return code, kindOf, reason
}

// tooLongWords are how endpoints of this kind say a request does not fit
// the model's context window.
var tooLongWords = []string{
	"context_length_exceeded", "context length", "context window", "maximum context",
	"too many tokens", "prompt is too long", "exceeds the available context",
}

// classify gives the seam's kind for a status and the words that came with
// it, and whether the same call may work if sent again. status is zero when
// the error came inside a reply and carried no number.
func classify(status int, words string) (seam.ErrorKind, bool) {
	words = strings.ToLower(words)
	has := func(list ...string) bool {
		for _, w := range list {
			if strings.Contains(words, w) {
				return true
			}
		}
		return false
	}
	switch {
	case status == 401 || status == 403:
		return seam.ErrAuth, false
	case status == 413:
		return seam.ErrTooLong, false
	case status == 429:
		// A quota that has run out does not come back in a few seconds.
		return seam.ErrRateLimited, !has("insufficient_quota")
	case status == 408:
		return seam.ErrUnavailable, true
	case status == 503 || status == 529:
		return seam.ErrOverloaded, true
	case status >= 500:
		return seam.ErrUnavailable, true
	case has(tooLongWords...):
		return seam.ErrTooLong, false
	case status == 400 || status == 404 || status == 405 || status == 409 || status == 422:
		return seam.ErrInvalid, false
	case status == 0 && has("overloaded"):
		return seam.ErrOverloaded, true
	case status == 0 && has("rate limit", "rate_limit"):
		return seam.ErrRateLimited, true
	}
	return seam.ErrOther, false
}
