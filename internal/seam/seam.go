// Package seam holds Keystone's own request and stream format for model
// calls. Everything outside the adapters speaks this format, and each adapter
// translates it to and from one provider's format.
//
// The types are plain data with JSON tags. A value that passes its Validate
// function reads back from JSON as the same value.
package seam

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// BlockType names one kind of content.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockImage      BlockType = "image"
	BlockFile       BlockType = "file"
	BlockToolCall   BlockType = "tool_call"
	BlockToolResult BlockType = "tool_result"
	BlockThinking   BlockType = "thinking"
)

// Block is one piece of content. Type says which fields are used:
//
//	text         Text
//	image        MediaType, and Data or Ref
//	file         MediaType, Name, and Data or Ref
//	tool_call    ID, Name, Input
//	tool_result  ID, Content, IsError
//	thinking     Text, Signature
//
// A field that does not belong to the block's type must be empty.
type Block struct {
	Type BlockType `json:"type"`

	// Text is the words of a text block or a thinking block.
	Text string `json:"text,omitempty"`

	// Signature is the provider's proof that a thinking block is its own.
	// It is opaque, and goes back to the same provider unchanged. A thinking
	// block the provider withheld has a signature and no text.
	Signature string `json:"signature,omitempty"`

	// MediaType is the type of an image or a file, such as image/png.
	MediaType string `json:"media_type,omitempty"`
	// Data is the bytes of an image or a file, sent inline.
	Data []byte `json:"data,omitempty"`
	// Ref names a file in the tenant's files folder by its hash. An image or
	// a file has Data or Ref, never both.
	Ref string `json:"ref,omitempty"`

	// Name is the tool's name in a tool call, or the file's name in a file.
	Name string `json:"name,omitempty"`

	// ID ties a tool result to its tool call.
	ID string `json:"id,omitempty"`
	// Input is the tool call's arguments, a JSON object.
	Input json.RawMessage `json:"input,omitempty"`

	// Content is what the tool returned: text, image and file blocks.
	Content []Block `json:"content,omitempty"`
	// IsError says the tool failed, and Content says how.
	IsError bool `json:"is_error,omitempty"`
}

// Role says who wrote a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn's content from one side. Tool calls and thinking are
// the assistant's. Tool results come back in a user message.
type Message struct {
	Role   Role    `json:"role"`
	Blocks []Block `json:"blocks"`
}

// Tool is one tool offered to the model.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// InputSchema is the JSON Schema of the tool's arguments, an object.
	InputSchema json.RawMessage `json:"input_schema"`
}

// Request is one model call.
type Request struct {
	// Model is the registry name of the model to call. Empty means the
	// routing rules choose.
	Model string `json:"model,omitempty"`
	// System is the standing instructions, as text blocks.
	System   []Block   `json:"system,omitempty"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
	// MaxOutputTokens caps the reply. Zero means the model's own limit.
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
	// ThinkingTokens is the most the model may spend on thinking. Zero
	// means no thinking is asked for.
	ThinkingTokens int `json:"thinking_tokens,omitempty"`
	// Effort is how hard the model is asked to work (P14-01). Empty
	// leaves the call as it was before effort existed. When both are set,
	// Effort wins over ThinkingTokens.
	Effort Effort `json:"effort,omitempty"`
	// Cache says where the parts of the request that repeat from one call
	// of a chat to the next end (P5-03). Nil leaves it to the adapter.
	Cache *Cache `json:"cache,omitempty"`
}

// Effort is one of the five levels a request may ask for, lowest first.
// Each adapter turns it into what its provider takes.
type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
	EffortXHigh  Effort = "xhigh"
	EffortMax    Effort = "max"
)

// Efforts are the levels, lowest first.
var Efforts = []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}

// Known reports whether e is one of the five levels. Empty is not.
func (e Effort) Known() bool {
	for _, k := range Efforts {
		if e == k {
			return true
		}
	}
	return false
}

// Cache is a request's cache points. A provider that caches only where it
// is told, as the Anthropic Messages API does, is told here; one that
// caches every prefix by itself needs none of it. Both counts are from the
// start, and the tools come before everything.
type Cache struct {
	// Start is how many system blocks, from the first, are the standing
	// start: the same on every call of a chat.
	Start int `json:"start"`
	// History is how many messages, from the first, an earlier call of the
	// same chat sent too, in the same order and the same bytes: the turns
	// before the current one.
	History int `json:"history"`
}

// Usage is the token count of one call. The four top counts do not overlap,
// so their sum is everything the call was billed for.
type Usage struct {
	// InputTokens is the input that was neither read from nor written to
	// the provider's cache.
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
	// ThinkingTokens is the part of OutputTokens spent on thinking.
	ThinkingTokens int `json:"thinking_tokens,omitempty"`
	// Estimated says Keystone counted the tokens because the provider did
	// not report them.
	Estimated bool `json:"estimated,omitempty"`
}

// Total is every token the call was billed for.
func (u Usage) Total() int {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// Add returns the sum of two usages. The sum is an estimate if either is.
func (u Usage) Add(v Usage) Usage {
	return Usage{
		InputTokens:      u.InputTokens + v.InputTokens,
		OutputTokens:     u.OutputTokens + v.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + v.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + v.CacheWriteTokens,
		ThinkingTokens:   u.ThinkingTokens + v.ThinkingTokens,
		Estimated:        u.Estimated || v.Estimated,
	}
}

// ValidateBlock checks that a block has the fields its type needs and no
// others.
func ValidateBlock(b Block) error {
	// used marks the fields this block's type owns. Anything else set is
	// refused below.
	var used struct{ text, signature, media, name, id, input, content bool }
	switch b.Type {
	case BlockText:
		used.text = true
		if b.Text == "" {
			return fmt.Errorf("a text block has no text")
		}
	case BlockThinking:
		used.text, used.signature = true, true
		if b.Text == "" && b.Signature == "" {
			return fmt.Errorf("a thinking block has neither text nor a signature")
		}
	case BlockImage, BlockFile:
		used.media = true
		used.name = b.Type == BlockFile
		if !strings.Contains(b.MediaType, "/") {
			return fmt.Errorf("%s block: media type %q is not a type such as image/png", b.Type, b.MediaType)
		}
		if b.Type == BlockImage && !strings.HasPrefix(b.MediaType, "image/") {
			return fmt.Errorf("image block: media type %q is not an image", b.MediaType)
		}
		if (len(b.Data) > 0) == (b.Ref != "") {
			return fmt.Errorf("%s block: it needs data or a ref, and not both", b.Type)
		}
	case BlockToolCall:
		used.id, used.name, used.input = true, true, true
		if b.ID == "" || b.Name == "" {
			return fmt.Errorf("a tool call needs an id and a name")
		}
		if !isJSONObject(b.Input) {
			return fmt.Errorf("tool call %s: the input is not a JSON object", b.ID)
		}
	case BlockToolResult:
		used.id, used.content = true, true
		if b.ID == "" {
			return fmt.Errorf("a tool result needs the id of its tool call")
		}
		for i, c := range b.Content {
			if c.Type != BlockText && c.Type != BlockImage && c.Type != BlockFile {
				return fmt.Errorf("tool result %s: content %d is a %s block, and only text, image and file are allowed", b.ID, i, c.Type)
			}
			if err := ValidateBlock(c); err != nil {
				return fmt.Errorf("tool result %s: content %d: %w", b.ID, i, err)
			}
		}
	default:
		return fmt.Errorf("block type %q is not known", b.Type)
	}
	stray := ""
	switch {
	case !used.text && b.Text != "":
		stray = "text"
	case !used.signature && b.Signature != "":
		stray = "a signature"
	case !used.media && (b.MediaType != "" || len(b.Data) > 0 || b.Ref != ""):
		stray = "media"
	case !used.name && b.Name != "":
		stray = "a name"
	case !used.id && b.ID != "":
		stray = "an id"
	case !used.input && len(b.Input) > 0:
		stray = "input"
	case !used.content && (len(b.Content) > 0 || b.IsError):
		stray = "tool result content"
	}
	if stray != "" {
		return fmt.Errorf("a %s block cannot carry %s", b.Type, stray)
	}
	return nil
}

// ValidateMessage checks a message's role and every block in it.
func ValidateMessage(m Message) error {
	if m.Role != RoleUser && m.Role != RoleAssistant {
		return fmt.Errorf("role %q is not user or assistant", m.Role)
	}
	if len(m.Blocks) == 0 {
		return fmt.Errorf("a message needs at least one block")
	}
	for i, b := range m.Blocks {
		if err := ValidateBlock(b); err != nil {
			return fmt.Errorf("block %d: %w", i, err)
		}
		switch {
		case m.Role == RoleUser && (b.Type == BlockToolCall || b.Type == BlockThinking):
			return fmt.Errorf("block %d: a user message cannot hold a %s block", i, b.Type)
		case m.Role == RoleAssistant && b.Type == BlockToolResult:
			return fmt.Errorf("block %d: an assistant message cannot hold a tool result", i)
		}
	}
	return nil
}

// ValidateRequest checks a whole request. Every tool result must answer a
// tool call in an earlier message, and no call may be answered twice.
func ValidateRequest(r Request) error {
	if r.MaxOutputTokens < 0 || r.ThinkingTokens < 0 {
		return fmt.Errorf("a token limit cannot be negative")
	}
	if r.Effort != "" && !r.Effort.Known() {
		return fmt.Errorf("effort %q is not low, medium, high, xhigh or max", r.Effort)
	}
	for i, b := range r.System {
		if b.Type != BlockText {
			return fmt.Errorf("system block %d is a %s block, and only text is allowed", i, b.Type)
		}
		if err := ValidateBlock(b); err != nil {
			return fmt.Errorf("system block %d: %w", i, err)
		}
	}
	names := map[string]bool{}
	for i, t := range r.Tools {
		if t.Name == "" {
			return fmt.Errorf("tool %d has no name", i)
		}
		if names[t.Name] {
			return fmt.Errorf("tool %s is offered twice", t.Name)
		}
		names[t.Name] = true
		if !isJSONObject(t.InputSchema) {
			return fmt.Errorf("tool %s: the input schema is not a JSON object", t.Name)
		}
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("a request needs at least one message")
	}
	if c := r.Cache; c != nil && (c.Start < 0 || c.Start > len(r.System) || c.History < 0 || c.History > len(r.Messages)) {
		return fmt.Errorf("the cache points (%d system blocks, %d messages) are not inside the request", c.Start, c.History)
	}
	// calls maps a tool call's id to whether it has been answered.
	calls := map[string]bool{}
	for i, m := range r.Messages {
		if err := ValidateMessage(m); err != nil {
			return fmt.Errorf("message %d: %w", i, err)
		}
		// Results are checked before this message's own calls are added,
		// so a result can only answer an earlier message.
		for _, b := range m.Blocks {
			if b.Type != BlockToolResult {
				continue
			}
			answered, known := calls[b.ID]
			if !known {
				return fmt.Errorf("message %d: tool result %s answers no earlier tool call", i, b.ID)
			}
			if answered {
				return fmt.Errorf("message %d: tool call %s is answered twice", i, b.ID)
			}
			calls[b.ID] = true
		}
		for _, b := range m.Blocks {
			if b.Type != BlockToolCall {
				continue
			}
			if _, dup := calls[b.ID]; dup {
				return fmt.Errorf("message %d: tool call id %s is used twice", i, b.ID)
			}
			calls[b.ID] = false
		}
	}
	return nil
}

// ValidateUsage refuses a negative count, and thinking that is more than
// the output it is part of.
func ValidateUsage(u Usage) error {
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.CacheReadTokens < 0 || u.CacheWriteTokens < 0 || u.ThinkingTokens < 0 {
		return fmt.Errorf("a token count cannot be negative")
	}
	if u.ThinkingTokens > u.OutputTokens {
		return fmt.Errorf("thinking tokens (%d) are part of output tokens (%d) and cannot be more", u.ThinkingTokens, u.OutputTokens)
	}
	return nil
}

func isJSONObject(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{' && json.Valid(t)
}
