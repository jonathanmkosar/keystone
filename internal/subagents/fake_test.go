package subagents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The fake models of these tests. Every call goes to one provider, the
// director, which tells a sub-agent's call (its message holds the brief)
// from the parent's, and answers each as the test wrote it.

var (
	paidModel = models.Model{
		Name: "paid-large", Label: "Paid Large", Adapter: models.AdapterAnthropic, Endpoint: "https://api.provider-one.example",
		ProviderModel: "example-large-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassHosted,
		Capabilities: []models.Capability{models.CapTools}, Price: models.Price{Input: 2, Output: 10},
	}
	houseModel = models.Model{
		Name: "house-small", Label: "House Small", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
		ProviderModel: "small-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal,
		Capabilities: []models.Capability{models.CapTools},
	}
)

// director answers the parent's calls and its sub-agents'. The parent's
// call whose person's message is a key of asks makes the tool call given;
// a parent's call that follows a tool result answers with done; any other
// parent's call answers plainly. A sub-agent's call is answered by sub,
// with its task.
type director struct {
	brief string
	asks  map[string]toolCall
	done  string
	sub   func(ctx context.Context, task string, req seam.Request, emit func(seam.Event) error) error

	mu     sync.Mutex
	parent []seam.Request // the parent's calls, in order
	agents []seam.Request // the sub-agents' calls, in order
}

type toolCall struct {
	name  string
	input any
}

func (p *director) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	all := userText(req)
	if strings.Contains(all, p.brief) {
		p.mu.Lock()
		p.agents = append(p.agents, req)
		p.mu.Unlock()
		return p.sub(ctx, taskOf(all), req, emit)
	}
	p.mu.Lock()
	p.parent = append(p.parent, req)
	p.mu.Unlock()
	if _, ok := lastResult(req); ok {
		return say(emit, req.Model, p.done)
	}
	last := lastUserText(req)
	for prompt, c := range p.asks {
		if strings.Contains(last, prompt) {
			return ask(emit, req.Model, "call-"+c.name, c.name, c.input)
		}
	}
	return say(emit, req.Model, "Nothing to add.")
}

// parentCalls returns the parent's calls so far.
func (p *director) parentCalls() []seam.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]seam.Request(nil), p.parent...)
}

// carried is the tool result the parent's newest call that carries one
// was sent: what came back to its turn. A title call may follow it.
func (p *director) carried() string {
	calls := p.parentCalls()
	for i := len(calls) - 1; i >= 0; i-- {
		if res, ok := lastResult(calls[i]); ok {
			return res
		}
	}
	return ""
}

// agentCalls returns the sub-agents' calls so far.
func (p *director) agentCalls() []seam.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]seam.Request(nil), p.agents...)
}

// userText is every text block the person's side of a request holds.
func userText(req seam.Request) string {
	var b strings.Builder
	for _, m := range req.Messages {
		if m.Role != seam.RoleUser {
			continue
		}
		for _, bl := range m.Blocks {
			if bl.Type == seam.BlockText {
				b.WriteString(bl.Text + "\n")
			}
		}
	}
	return b.String()
}

// lastUserText is the text of a request's last message from the person.
func lastUserText(req seam.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == seam.RoleUser {
			return seam.Text(req.Messages[i])
		}
	}
	return ""
}

// lastResult is the text of the tool results in a request's last message,
// and whether it has any.
func lastResult(req seam.Request) (string, bool) {
	if len(req.Messages) == 0 {
		return "", false
	}
	var b strings.Builder
	found := false
	for _, bl := range req.Messages[len(req.Messages)-1].Blocks {
		if bl.Type == seam.BlockToolResult {
			found = true
			b.WriteString(seam.Text(seam.Message{Blocks: bl.Content}) + "\n")
		}
	}
	return b.String(), found
}

// taskOf is the task in a sub-agent's message.
func taskOf(text string) string {
	_, after, ok := strings.Cut(text, "Your task:\n")
	if !ok {
		return ""
	}
	task, _, _ := strings.Cut(after, "\n")
	return strings.TrimSpace(task)
}

func emitAll(emit func(seam.Event) error, r seam.Response) error {
	events, err := seam.Events(r)
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := emit(e); err != nil {
			return err
		}
	}
	return nil
}

// say answers with text.
func say(emit func(seam.Event) error, model, text string) error {
	return emitAll(emit, seam.Response{
		Model: model, Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}},
		StopReason: seam.StopEnd, Usage: seam.Usage{InputTokens: 1000, OutputTokens: 200},
	})
}

// ask answers with one tool call.
func ask(emit func(seam.Event) error, model, id, name string, input any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return emitAll(emit, seam.Response{
		Model: model, Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockToolCall, ID: id, Name: name, Input: raw}}},
		StopReason: seam.StopToolCall, Usage: seam.Usage{InputTokens: 1000, OutputTokens: 50},
	})
}

// agentsInput is run_subagents' input for tasks, each with a title.
func agentsInput(tasks ...string) map[string]any {
	var list []map[string]any
	for _, t := range tasks {
		list = append(list, map[string]any{"task": t, "title": strings.TrimSuffix(t, ".")})
	}
	return map[string]any{"agents": list}
}

// barrier is a sub-agent's model whose calls each wait until want calls are
// under way at the same time, or five seconds have passed, and then answer
// with the task. It keeps when each call began and ended, and the most that
// were under way at once.
type barrier struct {
	want  int
	mu    sync.Mutex
	in    int
	most  int
	spans []span
}

type span struct {
	task       string
	began, end time.Time
}

func (b *barrier) run(ctx context.Context, task string, req seam.Request, emit func(seam.Event) error) error {
	began := time.Now()
	b.mu.Lock()
	b.in++
	b.most = max(b.most, b.in)
	b.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b.mu.Lock()
		ok := b.most >= b.want
		b.mu.Unlock()
		if ok || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	b.mu.Lock()
	b.in--
	b.spans = append(b.spans, span{task, began, time.Now()})
	b.mu.Unlock()
	return say(emit, req.Model, fmt.Sprintf("Counted for %q: fourteen.", task))
}

// blocker is a sub-agent's model that writes the start of a reply and then
// waits until its call is cancelled, as a model in the middle of a long
// answer. It counts the calls under way and the ones cancelled.
type blocker struct {
	inFlight  atomic.Int32
	cancelled atomic.Int32
}

func (b *blocker) run(ctx context.Context, _ string, req seam.Request, emit func(seam.Event) error) error {
	b.inFlight.Add(1)
	defer b.inFlight.Add(-1)
	for _, e := range []seam.Event{
		{Type: seam.EventStart, Model: req.Model},
		{Type: seam.EventBlockStart, Index: 0, Block: &seam.Block{Type: seam.BlockText}},
		{Type: seam.EventBlockDelta, Index: 0, Text: "Counting the boats along the"},
	} {
		if err := emit(e); err != nil {
			return err
		}
	}
	<-ctx.Done()
	b.cancelled.Add(1)
	return ctx.Err()
}

// counting is a provider that always gives the same reply and counts its
// calls.
type counting struct {
	reply string
	calls *atomic.Int32
}

func (p counting) Stream(_ context.Context, req seam.Request, emit func(seam.Event) error) error {
	p.calls.Add(1)
	return say(emit, req.Model, p.reply)
}
