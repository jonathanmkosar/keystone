package chatcompletions

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The request as Chat Completions takes it.

type wireRequest struct {
	Model         string         `json:"model"`
	Messages      []wireMessage  `json:"messages"`
	Tools         []wireTool     `json:"tools,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type wireMessage struct {
	Role string `json:"role"`
	// Content is a string, or a list of parts when the message holds an
	// image or a file. It is left out of an assistant message that only
	// calls tools.
	Content    any            `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name string `json:"name"`
	// Arguments is the input as JSON text, which is how the format carries it.
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireToolSpec `json:"function"`
}

type wireToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wirePart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *wireImageURL `json:"image_url,omitempty"`
	File     *wireFile     `json:"file,omitempty"`
}

type wireImageURL struct {
	URL string `json:"url"`
}

type wireFile struct {
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data"`
}

// betweenBlocks joins text blocks that the format carries as one string.
const betweenBlocks = "\n\n"

// buildBody turns a seam request into the JSON body for one entry. The
// request has passed seam.ValidateRequest.
func buildBody(m models.Model, req seam.Request) ([]byte, error) {
	if n := seam.Images(req); n > 0 && !m.Has(models.CapVision) { // P9-02: never an image to a model without vision
		return nil, fmt.Errorf("the request holds %d images, and the registry entry of %s does not say it can see: an image is never sent to a model without vision", n, m.Name)
	}
	out := wireRequest{
		Model:         m.ProviderModel,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
	if n := req.MaxOutputTokens; n > 0 {
		out.MaxTokens = min(n, m.OutputLimit)
	}
	if len(req.System) > 0 {
		var parts []string
		for _, b := range req.System {
			parts = append(parts, b.Text)
		}
		out.Messages = append(out.Messages, wireMessage{Role: "system", Content: strings.Join(parts, betweenBlocks)})
	}
	for i, msg := range req.Messages {
		var err error
		if msg.Role == seam.RoleAssistant {
			out.Messages = appendAssistant(out.Messages, msg)
		} else {
			out.Messages, err = appendUser(out.Messages, msg)
		}
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, wireTool{Type: "function", Function: wireToolSpec{
			Name: t.Name, Description: t.Description, Parameters: t.InputSchema,
		}})
	}
	return json.Marshal(out)
}

// appendAssistant adds an assistant message: its text and its tool calls.
// Thinking is left out, because the format has no place for it, and a
// message that held nothing else is left out whole.
func appendAssistant(msgs []wireMessage, msg seam.Message) []wireMessage {
	out := wireMessage{Role: "assistant"}
	var text []string
	for _, b := range msg.Blocks {
		switch b.Type {
		case seam.BlockText:
			text = append(text, b.Text)
		case seam.BlockToolCall:
			// The input is sent without spare spaces, so the same call is
			// always the same text.
			input := string(b.Input)
			var tight bytes.Buffer
			if json.Compact(&tight, b.Input) == nil {
				input = tight.String()
			}
			out.ToolCalls = append(out.ToolCalls, wireToolCall{ID: b.ID, Type: "function",
				Function: wireFunction{Name: b.Name, Arguments: input}})
		}
	}
	if len(text) > 0 {
		out.Content = strings.Join(text, betweenBlocks)
	}
	if out.Content == nil && len(out.ToolCalls) == 0 {
		return msgs
	}
	return append(msgs, out)
}

// appendUser adds a user message. Each tool result becomes a message of its
// own with the role "tool", and those come first, because they must follow
// the assistant message that made the calls. Whatever else the message
// holds follows as one user message.
func appendUser(msgs []wireMessage, msg seam.Message) ([]wireMessage, error) {
	var rest []seam.Block
	for _, b := range msg.Blocks {
		if b.Type != seam.BlockToolResult {
			rest = append(rest, b)
			continue
		}
		var text []string
		if b.IsError {
			text = append(text, "The tool failed.")
		}
		for _, c := range b.Content {
			if c.Type == seam.BlockText {
				text = append(text, c.Text)
				continue
			}
			// A tool message carries text only. An image or a file the
			// tool returned goes in the user message that follows.
			what := "an image"
			if c.Type == seam.BlockFile {
				what = "the file " + c.Name
			}
			text = append(text, "(The tool returned "+what+", which follows in the next message.)")
			rest = append(rest, c)
		}
		// The content is always sent, even when it is empty.
		msgs = append(msgs, wireMessage{Role: "tool", ToolCallID: b.ID, Content: strings.Join(text, "\n")})
	}
	if len(rest) == 0 {
		return msgs, nil
	}
	onlyText := true
	var parts []wirePart
	var text []string
	for _, b := range rest {
		part, err := toPart(b)
		if err != nil {
			return nil, err
		}
		if part.Type != "text" {
			onlyText = false
		}
		parts = append(parts, part)
		text = append(text, part.Text)
	}
	if onlyText {
		// A plain string is what every endpoint of this kind accepts.
		return append(msgs, wireMessage{Role: "user", Content: strings.Join(text, betweenBlocks)}), nil
	}
	return append(msgs, wireMessage{Role: "user", Content: parts}), nil
}

// toPart turns a text, image or file block into one content part.
func toPart(b seam.Block) (wirePart, error) {
	if b.Type == seam.BlockText {
		return wirePart{Type: "text", Text: b.Text}, nil
	}
	if len(b.Data) == 0 {
		return wirePart{}, fmt.Errorf("the %s block names a stored file and holds no bytes: whoever builds the call reads the file first", b.Type)
	}
	if b.Type == seam.BlockFile && isText(b.MediaType) {
		return wirePart{Type: "text", Text: "File " + b.Name + ":\n" + string(b.Data)}, nil
	}
	url := "data:" + b.MediaType + ";base64," + base64.StdEncoding.EncodeToString(b.Data)
	if b.Type == seam.BlockImage {
		return wirePart{Type: "image_url", ImageURL: &wireImageURL{URL: url}}, nil
	}
	return wirePart{Type: "file", File: &wireFile{Filename: b.Name, FileData: url}}, nil
}

// isText reports whether a file of this media type is sent as text.
func isText(mediaType string) bool {
	mediaType, _, _ = strings.Cut(strings.ToLower(mediaType), ";")
	mediaType = strings.TrimSpace(mediaType)
	return strings.HasPrefix(mediaType, "text/") || mediaType == "application/json"
}
