package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The request, in the provider's format. Only the fields Keystone sends.

type wireRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	System    []wireBlock   `json:"system,omitempty"`
	Messages  []wireMessage `json:"messages"`
	Tools     []wireTool    `json:"tools,omitempty"`
	Thinking  *wireThinking `json:"thinking,omitempty"`
	Stream    bool          `json:"stream,omitempty"`
}

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

type wireTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *wireCache      `json:"cache_control,omitempty"`
}

type wireThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
	Display      string `json:"display,omitempty"`
}

type wireCache struct {
	Type string `json:"type"`
}

type wireSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// wireBlock is one content block. Type says which fields are used, as in
// the seam.
type wireBlock struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	// Thinking is a pointer because a withheld thinking block is sent
	// back with empty text, and the field must still be there.
	Thinking  *string `json:"thinking,omitempty"`
	Signature string  `json:"signature,omitempty"`
	// Data is the content of a redacted_thinking block.
	Data string `json:"data,omitempty"`

	Source *wireSource `json:"source,omitempty"`
	Title  string      `json:"title,omitempty"`

	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   []wireBlock `json:"content,omitempty"`
	IsError   bool        `json:"is_error,omitempty"`

	CacheControl *wireCache `json:"cache_control,omitempty"`
}

// redactedMark opens the signature of a thinking block the provider sent
// as redacted_thinking. The seam has one thinking block, with text and a
// signature. A redacted block has neither, only data, so its data is kept
// in the signature behind this mark, and goes back as it came. A real
// signature is base64 and never holds a colon.
const redactedMark = "redacted:"

// body builds the JSON for one request. With stream it is the body of a
// Messages call. Without, it is the body of a token count, which takes no
// output limit.
func (p *Provider) body(req seam.Request, stream bool) ([]byte, error) {
	if err := seam.ValidateRequest(req); err != nil {
		return nil, err
	}
	if req.Model != "" && req.Model != p.model.Name {
		return nil, fmt.Errorf("the request is for model %s, and this provider calls %s", req.Model, p.model.Name)
	}
	out := wireRequest{Model: p.model.ProviderModel, Stream: stream}

	maxTokens := p.model.OutputLimit
	if req.MaxOutputTokens > 0 && req.MaxOutputTokens < maxTokens {
		maxTokens = req.MaxOutputTokens
	}
	if stream {
		out.MaxTokens = maxTokens
	}

	for _, b := range req.System {
		out.System = append(out.System, wireBlock{Type: "text", Text: b.Text})
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, wireTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	for i, m := range req.Messages {
		content, err := wireBlocks(m.Blocks)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		if len(content) == 0 {
			// Every block was thinking from somewhere else. A message
			// cannot be empty, so it is left out.
			continue
		}
		out.Messages = append(out.Messages, wireMessage{Role: string(m.Role), Content: content})
	}
	if len(out.Messages) == 0 {
		return nil, fmt.Errorf("nothing in the request can be sent")
	}

	out.Thinking = p.thinking(req.ThinkingTokens, maxTokens)

	if p.opts.CacheControl {
		mark := &wireCache{Type: "ephemeral"}
		// The provider reads tools, then system, then messages. A mark
		// covers everything before it, so one at the end of the system
		// blocks covers the tools as well.
		switch {
		case len(out.System) > 0:
			out.System[len(out.System)-1].CacheControl = mark
		case len(out.Tools) > 0:
			out.Tools[len(out.Tools)-1].CacheControl = mark
		}
		last := out.Messages[len(out.Messages)-1].Content
		for i := len(last) - 1; i >= 0; i-- {
			// A thinking block cannot carry a mark.
			if last[i].Type != "thinking" && last[i].Type != "redacted_thinking" {
				last[i].CacheControl = mark
				break
			}
		}
	}
	return json.Marshal(out)
}

// thinking works out the thinking part of a request. Nil means none is
// asked for.
func (p *Provider) thinking(asked, maxTokens int) *wireThinking {
	if asked <= 0 || !p.model.Has(models.CapThinking) {
		return nil
	}
	if !matchesAny(p.opts.BudgetThinkingModels, p.model.ProviderModel) {
		// The model decides how much to think. The summary is asked for,
		// since without it the thinking arrives as empty blocks.
		return &wireThinking{Type: "adaptive", Display: "summarized"}
	}
	// The budget form: at least 1024 tokens, and less than the output
	// limit it is part of.
	const least = 1024
	budget := min(asked, maxTokens-1)
	if budget < least {
		return nil
	}
	return &wireThinking{Type: "enabled", BudgetTokens: budget}
}

// matchesAny reports whether name is in the list. An entry that ends in *
// matches every name that starts with what comes before it.
func matchesAny(list []string, name string) bool {
	for _, entry := range list {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if start, ok := strings.CutSuffix(entry, "*"); ok {
			if strings.HasPrefix(name, start) {
				return true
			}
		} else if entry == name {
			return true
		}
	}
	return false
}

func wireBlocks(blocks []seam.Block) ([]wireBlock, error) {
	var out []wireBlock
	for i, b := range blocks {
		w, keep, err := toWire(b)
		if err != nil {
			return nil, fmt.Errorf("block %d: %w", i, err)
		}
		if keep {
			out = append(out, w)
		}
	}
	return out, nil
}

// toWire turns one seam block into the provider's. keep is false for a
// block that is left out.
func toWire(b seam.Block) (w wireBlock, keep bool, err error) {
	switch b.Type {
	case seam.BlockText:
		return wireBlock{Type: "text", Text: b.Text}, true, nil
	case seam.BlockThinking:
		if data, ok := strings.CutPrefix(b.Signature, redactedMark); ok {
			return wireBlock{Type: "redacted_thinking", Data: data}, true, nil
		}
		if b.Signature == "" {
			// Thinking with no signature came from another provider, or
			// from one that signs nothing. This API refuses it, and the
			// model does not need it.
			return wireBlock{}, false, nil
		}
		text := b.Text
		return wireBlock{Type: "thinking", Thinking: &text, Signature: b.Signature}, true, nil
	case seam.BlockToolCall:
		return wireBlock{Type: "tool_use", ID: b.ID, Name: b.Name, Input: b.Input}, true, nil
	case seam.BlockToolResult:
		content, err := wireBlocks(b.Content)
		if err != nil {
			return wireBlock{}, false, fmt.Errorf("tool result %s: %w", b.ID, err)
		}
		return wireBlock{Type: "tool_result", ToolUseID: b.ID, Content: content, IsError: b.IsError}, true, nil
	case seam.BlockImage, seam.BlockFile:
		w, err := mediaToWire(b)
		return w, err == nil, err
	}
	return wireBlock{}, false, fmt.Errorf("a %s block cannot be sent", b.Type)
}

// mediaToWire turns an image or a file into an image or document block.
func mediaToWire(b seam.Block) (wireBlock, error) {
	if b.Ref != "" {
		// The adapter cannot reach a tenant's files. Whoever builds the
		// call reads the file and sends its bytes.
		return wireBlock{}, fmt.Errorf("a %s block names a stored file, and its bytes must be read before the call", b.Type)
	}
	media := strings.ToLower(strings.TrimSpace(strings.SplitN(b.MediaType, ";", 2)[0]))
	switch {
	case strings.HasPrefix(media, "image/"):
		return wireBlock{Type: "image", Source: &wireSource{
			Type: "base64", MediaType: media, Data: base64.StdEncoding.EncodeToString(b.Data),
		}}, nil
	case media == "application/pdf":
		return wireBlock{Type: "document", Title: b.Name, Source: &wireSource{
			Type: "base64", MediaType: media, Data: base64.StdEncoding.EncodeToString(b.Data),
		}}, nil
	case strings.HasPrefix(media, "text/"), media == "application/json":
		if !utf8.Valid(b.Data) {
			return wireBlock{}, fmt.Errorf("the file %s is marked as text and is not UTF-8", b.Name)
		}
		// Every kind of text goes as plain text, the one kind the API takes.
		return wireBlock{Type: "document", Title: b.Name, Source: &wireSource{
			Type: "text", MediaType: "text/plain", Data: string(b.Data),
		}}, nil
	}
	return wireBlock{}, fmt.Errorf("the Messages API takes images, PDF and text, and the file %s is %s", b.Name, media)
}
