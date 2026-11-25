package responses

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// wireRequest is the body of POST /responses.
type wireRequest struct {
	Model           string         `json:"model"`
	Instructions    string         `json:"instructions,omitempty"`
	Input           []any          `json:"input"`
	Tools           []wireTool     `json:"tools,omitempty"`
	MaxOutputTokens int            `json:"max_output_tokens,omitempty"`
	Reasoning       *wireReasoning `json:"reasoning,omitempty"`
	Include         []string       `json:"include,omitempty"`
	Store           bool           `json:"store"`
	Stream          bool           `json:"stream"`
}

type wireTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type wireReasoning struct {
	Effort  string `json:"effort"`
	Summary string `json:"summary"`
}

// wireMessage is a user or assistant message in the input. Content is a
// string for the assistant and a list of parts for the user.
type wireMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// wirePart is one part of a user message or of a tool's output.
type wirePart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
}

type wireFunctionCall struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireFunctionOutput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	// Output is a string, or a list of parts when the tool returned an
	// image or a file.
	Output any `json:"output"`
}

type wireSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type wireReasoningItem struct {
	Type             string        `json:"type"`
	ID               string        `json:"id,omitempty"`
	Summary          []wireSummary `json:"summary"`
	EncryptedContent string        `json:"encrypted_content"`
}

// toolErrorPrefix marks the output of a tool that failed. The Responses
// API has no field for it.
const toolErrorPrefix = "Tool error: "

// signaturePrefix marks a thinking block's signature as one this adapter
// made. What follows it is a reasoning item's id and encrypted content.
const signaturePrefix = "responses/1:"

type signature struct {
	ID               string `json:"id,omitempty"`
	EncryptedContent string `json:"encrypted_content"`
}

func packSignature(id, encrypted string) string {
	raw, _ := json.Marshal(signature{ID: id, EncryptedContent: encrypted})
	return signaturePrefix + string(raw)
}

// unpackSignature reads a signature this adapter made. Another provider's
// signature is not one, and its thinking block is left out of the request.
func unpackSignature(s string) (signature, bool) {
	rest, ok := strings.CutPrefix(s, signaturePrefix)
	if !ok {
		return signature{}, false
	}
	var sig signature
	if err := json.Unmarshal([]byte(rest), &sig); err != nil || sig.EncryptedContent == "" {
		return signature{}, false
	}
	return sig, true
}

// body builds the request body. An error is one the person can read: it
// never holds the key, which is not part of the body.
func (p *Provider) body(req seam.Request) ([]byte, error) {
	w := wireRequest{Model: p.model.ProviderModel, Input: []any{}, Stream: true}

	var system []string
	for _, b := range req.System {
		system = append(system, b.Text)
	}
	w.Instructions = strings.Join(system, "\n\n")

	for i, m := range req.Messages {
		items, err := inputItems(m)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		w.Input = append(w.Input, items...)
	}

	for _, t := range req.Tools {
		w.Tools = append(w.Tools, wireTool{Type: "function", Name: t.Name, Description: t.Description, Parameters: compact(t.InputSchema)})
	}

	w.MaxOutputTokens = req.MaxOutputTokens
	if w.MaxOutputTokens > p.model.OutputLimit {
		w.MaxOutputTokens = p.model.OutputLimit
	}

	if p.model.Has(models.CapThinking) {
		// Nothing is stored at the provider, so the reasoning has to come
		// back here to be sent again with the next call of the turn.
		w.Include = []string{"reasoning.encrypted_content"}
		if req.ThinkingTokens > 0 {
			w.Reasoning = &wireReasoning{Effort: p.effort(req.ThinkingTokens), Summary: "auto"}
		}
	}
	return json.Marshal(w)
}

// effort turns a thinking budget into the effort the Responses API takes.
func (p *Provider) effort(tokens int) string {
	switch {
	case tokens >= p.opts.EffortHighFrom:
		return "high"
	case tokens >= p.opts.EffortMediumFrom:
		return "medium"
	}
	return "low"
}

// inputItems turns one message into the input items that carry it.
func inputItems(m seam.Message) ([]any, error) {
	var items []any
	if m.Role == seam.RoleAssistant {
		for _, b := range m.Blocks {
			switch b.Type {
			case seam.BlockText:
				items = append(items, wireMessage{Role: "assistant", Content: b.Text})
			case seam.BlockToolCall:
				items = append(items, wireFunctionCall{Type: "function_call", CallID: b.ID, Name: b.Name, Arguments: string(compact(b.Input))})
			case seam.BlockThinking:
				sig, ok := unpackSignature(b.Signature)
				if !ok {
					continue
				}
				item := wireReasoningItem{Type: "reasoning", ID: sig.ID, Summary: []wireSummary{}, EncryptedContent: sig.EncryptedContent}
				if b.Text != "" {
					item.Summary = append(item.Summary, wireSummary{Type: "summary_text", Text: b.Text})
				}
				items = append(items, item)
			}
		}
		return items, nil
	}

	// A user message: the tool results first, each an item of its own, and
	// then whatever the person wrote.
	var parts []wirePart
	for _, b := range m.Blocks {
		if b.Type == seam.BlockToolResult {
			out, err := toolOutput(b)
			if err != nil {
				return nil, err
			}
			items = append(items, wireFunctionOutput{Type: "function_call_output", CallID: b.ID, Output: out})
			continue
		}
		part, err := inputPart(b)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	if len(parts) > 0 {
		items = append(items, wireMessage{Role: "user", Content: parts})
	}
	return items, nil
}

// inputPart turns a text, image or file block into a part.
func inputPart(b seam.Block) (wirePart, error) {
	switch b.Type {
	case seam.BlockText:
		return wirePart{Type: "input_text", Text: b.Text}, nil
	case seam.BlockImage, seam.BlockFile:
		if b.Ref != "" {
			return wirePart{}, fmt.Errorf("a %s block names a stored file, and it has to be read into the block before the model is called", b.Type)
		}
		data := "data:" + b.MediaType + ";base64," + base64.StdEncoding.EncodeToString(b.Data)
		if b.Type == seam.BlockImage {
			return wirePart{Type: "input_image", ImageURL: data}, nil
		}
		name := b.Name
		if name == "" {
			name = "file"
		}
		return wirePart{Type: "input_file", Filename: name, FileData: data}, nil
	}
	return wirePart{}, fmt.Errorf("a %s block cannot be sent in a user message", b.Type)
}

// toolOutput is what a tool returned: one string when it is all text, and
// a list of parts when it holds an image or a file.
func toolOutput(b seam.Block) (any, error) {
	allText := true
	for _, c := range b.Content {
		if c.Type != seam.BlockText {
			allText = false
		}
	}
	if allText {
		var lines []string
		for _, c := range b.Content {
			lines = append(lines, c.Text)
		}
		text := strings.Join(lines, "\n")
		if b.IsError {
			text = toolErrorPrefix + text
		}
		return text, nil
	}
	var parts []wirePart
	if b.IsError {
		parts = append(parts, wirePart{Type: "input_text", Text: strings.TrimSpace(toolErrorPrefix)})
	}
	for _, c := range b.Content {
		part, err := inputPart(c)
		if err != nil {
			return nil, fmt.Errorf("tool result %s: %w", b.ID, err)
		}
		parts = append(parts, part)
	}
	return parts, nil
}

// compact returns JSON with the spaces between its tokens taken out.
func compact(raw json.RawMessage) json.RawMessage {
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return raw
	}
	return out.Bytes()
}
