package responses

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// maxEventBytes is the most one server-sent event may hold. It is a guard
// against an endpoint that never ends a line, not a value to tune.
const maxEventBytes = 32 << 20

var errEventTooBig = errors.New("the endpoint sent one event larger than the adapter reads")

// readEvents reads server-sent events from r and calls handle for each one
// with its event name and its data. It returns handle's error, or the
// error that ended the reading. A clean end is io.EOF.
//
// This is the adapter's own reader. The other adapters keep theirs.
func readEvents(r io.Reader, handle func(name string, data []byte) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var name string
	var data []byte
	hasData := false
	for {
		line, err := br.ReadBytes('\n')
		if len(data)+len(line) > maxEventBytes {
			return errEventTooBig
		}
		if len(line) > 0 && (err == nil || len(bytes.TrimRight(line, "\r\n")) > 0) {
			line = bytes.TrimRight(line, "\r\n")
			switch {
			case len(line) == 0:
				if hasData {
					if herr := handle(name, data); herr != nil {
						return herr
					}
				}
				name, data, hasData = "", nil, false
			case line[0] == ':':
				// A comment, which some servers send to keep the line open.
			default:
				field, value, _ := bytes.Cut(line, []byte(":"))
				value = bytes.TrimPrefix(value, []byte(" "))
				switch string(field) {
				case "event":
					name = string(value)
				case "data":
					if hasData {
						data = append(data, '\n')
					}
					data = append(data, value...)
					hasData = true
				}
			}
		}
		if err != nil {
			// An event with no blank line after it was cut off, and is
			// dropped.
			return err
		}
	}
}

// wireEvent is one event of the stream. Which fields are set depends on
// its type.
type wireEvent struct {
	Type         string        `json:"type"`
	ItemID       string        `json:"item_id"`
	ContentIndex int           `json:"content_index"`
	SummaryIndex int           `json:"summary_index"`
	Delta        string        `json:"delta"`
	Item         *wireItem     `json:"item"`
	Response     *wireResponse `json:"response"`
	// An error event carries its code and message at the top. Some
	// gateways put them under error.
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
	Error   *wireError      `json:"error"`
}

type wireItem struct {
	ID               string        `json:"id"`
	Type             string        `json:"type"`
	Name             string        `json:"name"`
	CallID           string        `json:"call_id"`
	Arguments        string        `json:"arguments"`
	EncryptedContent string        `json:"encrypted_content"`
	Summary          []wireSummary `json:"summary"`
	Content          []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
}

type wireResponse struct {
	Status            string     `json:"status"`
	Error             *wireError `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Usage *wireUsage `json:"usage"`
}

type wireUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

type wireError struct {
	Code    json.RawMessage `json:"code"`
	Type    string          `json:"type"`
	Message string          `json:"message"`
}

// code is the error's code as text. A provider sends a string or null, and
// some gateways send a number.
func codeText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	t := strings.TrimSpace(string(raw))
	if t == "null" {
		return ""
	}
	return t
}

// translator folds the provider's events into the seam's. The seam has one
// block open at a time, in order, and so does the stream it reads.
type translator struct {
	model string
	send  func(seam.Event) error

	started bool
	done    bool // the stop event has been sent
	next    int  // the index the next block opens with

	open  bool
	kind  seam.BlockType
	key   string // which item, or which part of an item, the open block is
	wrote bool   // the open block has had a delta

	// delivered holds the items whose content arrived in deltas, so the
	// whole item at its end is not sent again.
	delivered   map[string]bool
	sawToolCall bool
	sawRefusal  bool
}

func (t *translator) start() error {
	if t.started {
		return nil
	}
	t.started = true
	return t.send(seam.Event{Type: seam.EventStart, Model: t.model})
}

func (t *translator) closeBlock() error {
	if !t.open {
		return nil
	}
	t.open = false
	return t.send(seam.Event{Type: seam.EventBlockStop, Index: t.next - 1})
}

// openBlock closes whatever is open and opens a block.
func (t *translator) openBlock(kind seam.BlockType, key string, b seam.Block) error {
	if err := t.start(); err != nil {
		return err
	}
	if err := t.closeBlock(); err != nil {
		return err
	}
	b.Type = kind
	t.open, t.kind, t.key, t.wrote = true, kind, key, false
	t.next++
	return t.send(seam.Event{Type: seam.EventBlockStart, Index: t.next - 1, Block: &b})
}

func (t *translator) isOpen(kind seam.BlockType, key string) bool {
	return t.open && t.kind == kind && t.key == key
}

// delta adds to the open block. An empty piece is not an event.
func (t *translator) delta(item string, e seam.Event) error {
	if e.Text == "" && e.Input == "" && e.Signature == "" {
		return nil
	}
	e.Type, e.Index = seam.EventBlockDelta, t.next-1
	t.wrote = true
	if t.delivered == nil {
		t.delivered = map[string]bool{}
	}
	t.delivered[item] = true
	return t.send(e)
}

// text adds to a text block, opening it if this part of the item has none.
func (t *translator) text(item string, part int, s string) error {
	if s == "" {
		return nil
	}
	key := fmt.Sprintf("%s/%d", item, part)
	if !t.isOpen(seam.BlockText, key) {
		if err := t.openBlock(seam.BlockText, key, seam.Block{}); err != nil {
			return err
		}
	}
	return t.delta(item, seam.Event{Text: s})
}

// event takes one event of the stream. An error it returns ends the call:
// a *seam.Error for a failure the provider reported, or the caller's own.
func (t *translator) event(name string, data []byte) error {
	if t.done {
		return nil
	}
	if string(bytes.TrimSpace(data)) == "[DONE]" {
		return nil
	}
	var e wireEvent
	if err := json.Unmarshal(data, &e); err != nil {
		return &seam.Error{Kind: seam.ErrOther, Message: "an event in the reply could not be read as the Responses API's"}
	}
	if e.Type == "" {
		e.Type = name
	}
	switch e.Type {
	case "response.created", "response.in_progress":
		// The start event waits for the first block or the stop, so a
		// reply that fails before it says anything can be sent again.
		return nil

	case "response.output_item.added":
		if e.Item == nil {
			return nil
		}
		switch e.Item.Type {
		case "function_call":
			return t.openToolCall(e.Item)
		case "reasoning":
			return t.openBlock(seam.BlockThinking, e.Item.ID, seam.Block{})
		}

	case "response.output_text.delta":
		return t.text(e.ItemID, e.ContentIndex, e.Delta)
	case "response.refusal.delta":
		t.sawRefusal = true
		return t.text(e.ItemID, e.ContentIndex, e.Delta)
	case "response.content_part.done":
		if t.isOpen(seam.BlockText, fmt.Sprintf("%s/%d", e.ItemID, e.ContentIndex)) {
			return t.closeBlock()
		}

	case "response.function_call_arguments.delta":
		if t.isOpen(seam.BlockToolCall, e.ItemID) {
			return t.delta(e.ItemID, seam.Event{Input: e.Delta})
		}

	case "response.reasoning_summary_part.added":
		// A second part of a summary is a new paragraph of the same block.
		if t.isOpen(seam.BlockThinking, e.ItemID) && t.wrote && e.SummaryIndex > 0 {
			return t.delta(e.ItemID, seam.Event{Text: "\n\n"})
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if e.Delta == "" {
			return nil
		}
		if !t.isOpen(seam.BlockThinking, e.ItemID) {
			if err := t.openBlock(seam.BlockThinking, e.ItemID, seam.Block{}); err != nil {
				return err
			}
		}
		return t.delta(e.ItemID, seam.Event{Text: e.Delta})

	case "response.output_item.done":
		if e.Item == nil {
			return nil
		}
		return t.itemDone(e.Item)

	case "response.completed":
		return t.stop(e.Response, "")
	case "response.incomplete":
		reason := "unknown"
		if e.Response != nil && e.Response.IncompleteDetails != nil && e.Response.IncompleteDetails.Reason != "" {
			reason = e.Response.IncompleteDetails.Reason
		}
		return t.stop(e.Response, reason)

	case "response.failed":
		if e.Response != nil && e.Response.Error != nil {
			return codeError(codeText(e.Response.Error.Code), e.Response.Error.Message)
		}
		return &seam.Error{Kind: seam.ErrOther, Message: "the provider said the reply failed and gave no reason"}
	case "error":
		code, msg := codeText(e.Code), e.Message
		if e.Error != nil {
			if code == "" {
				code = codeText(e.Error.Code)
			}
			if msg == "" {
				msg = e.Error.Message
			}
		}
		return codeError(code, msg)
	}
	// Every other event is one this adapter has no use for.
	return nil
}

func (t *translator) openToolCall(it *wireItem) error {
	id := it.CallID
	if id == "" {
		id = it.ID
	}
	if id == "" || it.Name == "" {
		return &seam.Error{Kind: seam.ErrOther, Message: "the reply holds a function call with no id or no name"}
	}
	t.sawToolCall = true
	return t.openBlock(seam.BlockToolCall, it.ID, seam.Block{ID: id, Name: it.Name})
}

// itemDone closes an item's block. If the item's content did not arrive in
// deltas, it is sent now, whole.
func (t *translator) itemDone(it *wireItem) error {
	switch it.Type {
	case "function_call":
		if !t.isOpen(seam.BlockToolCall, it.ID) {
			if t.delivered[it.ID] {
				return nil
			}
			if err := t.openToolCall(it); err != nil {
				return err
			}
		}
		if !t.wrote {
			if err := t.delta(it.ID, seam.Event{Input: it.Arguments}); err != nil {
				return err
			}
		}
		return t.closeBlock()

	case "reasoning":
		if !t.isOpen(seam.BlockThinking, it.ID) {
			if t.delivered[it.ID] && it.EncryptedContent == "" {
				return nil
			}
			whole := !t.delivered[it.ID]
			if err := t.openBlock(seam.BlockThinking, it.ID, seam.Block{}); err != nil {
				return err
			}
			if !whole {
				t.wrote = true
			}
		}
		if !t.wrote {
			var parts []string
			for _, s := range it.Summary {
				if s.Text != "" {
					parts = append(parts, s.Text)
				}
			}
			if err := t.delta(it.ID, seam.Event{Text: strings.Join(parts, "\n\n")}); err != nil {
				return err
			}
		}
		if it.EncryptedContent != "" {
			if err := t.delta(it.ID, seam.Event{Signature: packSignature(it.ID, it.EncryptedContent)}); err != nil {
				return err
			}
		}
		return t.closeBlock()

	case "message":
		if !t.delivered[it.ID] {
			for i, c := range it.Content {
				s := c.Text
				if c.Type == "refusal" {
					s = c.Refusal
					t.sawRefusal = t.sawRefusal || s != ""
				}
				if err := t.text(it.ID, i, s); err != nil {
					return err
				}
			}
		}
		if t.open && t.kind == seam.BlockText && strings.HasPrefix(t.key, it.ID+"/") {
			return t.closeBlock()
		}
	}
	return nil
}

// stop ends a reply that worked. incomplete is the provider's reason when
// the reply was cut short, and empty when it was not.
func (t *translator) stop(r *wireResponse, incomplete string) error {
	if err := t.start(); err != nil {
		return err
	}
	reason := seam.StopEnd
	switch incomplete {
	case "":
		switch {
		case t.sawToolCall:
			reason = seam.StopToolCall
		case t.sawRefusal:
			reason = seam.StopRefused
		}
	case "max_output_tokens":
		reason = seam.StopMaxTokens
		if t.open && t.kind == seam.BlockToolCall {
			// Half a tool call's input is not JSON, so the block cannot
			// be closed as a whole one.
			return &seam.Error{Kind: seam.ErrOther, Message: "the reply reached its output limit part of the way through a tool call"}
		}
	case "content_filter":
		reason = seam.StopRefused
	default:
		return &seam.Error{Kind: seam.ErrOther, Message: "the provider cut the reply short and gave a reason this adapter does not know: " + incomplete}
	}
	if err := t.closeBlock(); err != nil {
		return err
	}
	usage := seam.Usage{Estimated: true}
	if r != nil && r.Usage != nil {
		usage = usageOf(*r.Usage)
	}
	if err := t.send(seam.Event{Type: seam.EventStop, StopReason: reason, Usage: &usage}); err != nil {
		return err
	}
	t.done = true
	return nil
}

// usageOf splits the provider's counts into the seam's, which do not
// overlap: the provider's input count includes what it read from its
// cache, and the seam's does not.
func usageOf(u wireUsage) seam.Usage {
	cached := min(max(u.InputTokensDetails.CachedTokens, 0), max(u.InputTokens, 0))
	out := max(u.OutputTokens, 0)
	return seam.Usage{
		InputTokens:     max(u.InputTokens, 0) - cached,
		OutputTokens:    out,
		CacheReadTokens: cached,
		ThinkingTokens:  min(max(u.OutputTokensDetails.ReasoningTokens, 0), out),
	}
}

// codeError sorts a failure by the provider's error code.
func codeError(code, msg string) *seam.Error {
	if msg == "" {
		msg = "the provider gave no message"
	}
	e := &seam.Error{Kind: kindOfCode(code), Message: msg}
	if code != "" {
		e.Message = code + ": " + msg
	}
	switch e.Kind {
	case seam.ErrAuth:
		// The provider's own words for a refused key repeat part of it.
		e.Message = "the provider refused the key (" + code + ")"
	case seam.ErrRateLimited:
		e.Retryable = code != "insufficient_quota"
	case seam.ErrOverloaded, seam.ErrUnavailable:
		e.Retryable = true
	}
	return e
}

// kindOfCode is the kind for an error code, and other for a code this
// adapter does not know.
func kindOfCode(code string) seam.ErrorKind {
	switch code {
	case "invalid_api_key", "incorrect_api_key", "authentication_error", "permission_denied", "account_deactivated":
		return seam.ErrAuth
	case "rate_limit_exceeded", "insufficient_quota":
		return seam.ErrRateLimited
	case "server_is_overloaded", "overloaded", "overloaded_error", "service_unavailable":
		return seam.ErrOverloaded
	case "server_error", "internal_error", "internal_server_error":
		return seam.ErrUnavailable
	case "context_length_exceeded", "string_above_max_length":
		return seam.ErrTooLong
	case "invalid_prompt", "invalid_request_error", "model_not_found", "invalid_type", "invalid_value", "missing_required_parameter", "unknown_parameter", "unsupported_parameter", "unsupported_value":
		return seam.ErrInvalid
	}
	return seam.ErrOther
}

// statusError sorts a call the endpoint refused, by its code when it gave
// one this adapter knows, and by the HTTP status otherwise.
func statusError(status int, body []byte) *seam.Error {
	var w struct {
		Error *wireError `json:"error"`
	}
	code, msg := "", ""
	if json.Unmarshal(body, &w) == nil && w.Error != nil {
		code, msg = codeText(w.Error.Code), w.Error.Message
	}
	if kindOfCode(code) != seam.ErrOther && status != 401 && status != 403 {
		e := codeError(code, msg)
		e.Message = fmt.Sprintf("the provider answered %d: %s", status, e.Message)
		return e
	}
	said := ""
	if msg != "" {
		said = ": " + msg
		if code != "" {
			said = ": " + code + ": " + msg
		}
	}
	e := &seam.Error{Message: fmt.Sprintf("the provider answered %d%s", status, said)}
	low := strings.ToLower(msg)
	switch {
	case status == 401 || status == 403:
		e.Kind = seam.ErrAuth
		e.Message = fmt.Sprintf("the provider refused the key (%d)", status)
		if code != "" {
			e.Message = fmt.Sprintf("the provider refused the key (%d %s)", status, code)
		}
	case status == 429:
		e.Kind, e.Retryable = seam.ErrRateLimited, true
	case status == 413, status == 400 && (strings.Contains(low, "context window") || strings.Contains(low, "context length")):
		e.Kind = seam.ErrTooLong
	case status == 400, status == 404, status == 405, status == 422:
		e.Kind = seam.ErrInvalid
	case status == 503, status == 529:
		e.Kind, e.Retryable = seam.ErrOverloaded, true
	case status == 408, status == 409, status >= 500:
		e.Kind, e.Retryable = seam.ErrUnavailable, true
	default:
		e.Kind = seam.ErrOther
	}
	return e
}
