package research

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
)

// The scripted models of these tests. Every call goes to one provider, the
// researcher, which tells the three steps of a research task apart by words
// of the shipped prompts, and answers each as the test wrote it.

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

// Words of the shipped prompts that say which step a call is.
const (
	planWords   = "Split the research question"
	partWords   = "This is one part of a research task"
	reportWords = "Write the research report"
)

// researcher answers a research task's calls. The plan step is answered
// with plan. A part's first call searches the web with the query its task
// has in queries, and its next call, which carries the results, answers
// with what its task has in findings; a part with no query answers at once.
// The report step is answered by report, which is given the step's whole
// message. block, when set, is called before the plan, a part's first call
// and the report are answered, with which it is (plan, part or report) and
// a part's task; an error it gives ends the call.
type researcher struct {
	plan     string
	queries  map[string]string
	findings map[string]string
	report   func(message string) string
	block    func(ctx context.Context, step, task string) error
	// found is the usage a part reports with its findings. Nil means a small one.
	found *seam.Usage

	mu      sync.Mutex
	plans   []seam.Request
	parts   []seam.Request
	reports []seam.Request
	calls   atomic.Int32
}

func (p *researcher) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	p.calls.Add(1)
	asked := asked(req)
	switch {
	case strings.Contains(asked, partWords):
		p.mu.Lock()
		p.parts = append(p.parts, req)
		p.mu.Unlock()
		task := taskOf(asked)
		if _, ok := lastResult(req); ok {
			if p.found != nil {
				return emitAll(emit, seam.Response{
					Model: req.Model, Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: p.findings[task]}}},
					StopReason: seam.StopEnd, Usage: *p.found,
				})
			}
			return say(emit, req.Model, p.findings[task])
		}
		if err := p.held(ctx, "part", task, req, emit); err != nil {
			return err
		}
		if q := p.queries[task]; q != "" {
			return ask(emit, req.Model, "call-search", web.SearchName, map[string]string{"query": q})
		}
		return say(emit, req.Model, p.findings[task])
	case strings.Contains(asked, planWords):
		p.mu.Lock()
		p.plans = append(p.plans, req)
		p.mu.Unlock()
		if err := p.held(ctx, "plan", "", req, emit); err != nil {
			return err
		}
		return say(emit, req.Model, p.plan)
	case strings.Contains(asked, reportWords):
		p.mu.Lock()
		p.reports = append(p.reports, req)
		p.mu.Unlock()
		if err := p.held(ctx, "report", "", req, emit); err != nil {
			return err
		}
		return say(emit, req.Model, p.report(asked))
	}
	return say(emit, req.Model, "Nothing to add.")
}

// held asks block whether a call is to wait, and passes on what it says.
func (p *researcher) held(ctx context.Context, step, task string, _ seam.Request, _ func(seam.Event) error) error {
	if p.block == nil {
		return nil
	}
	return p.block(ctx, step, task)
}

// untilCancelled waits until a call is cancelled, as a model in the middle
// of a long answer, and counts the calls it held in n.
func untilCancelled(ctx context.Context, n *atomic.Int32) error {
	n.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

// reportMessages returns the messages the report step was sent, in order.
func (p *researcher) reportMessages() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, r := range p.reports {
		out = append(out, asked(r))
	}
	return out
}

func (p *researcher) count() (plans, parts, reports int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.plans), len(p.parts), len(p.reports)
}

// asked is the text of the last message of a request's that the person's
// side wrote in words: the step's message, not a set of tool results.
func asked(req seam.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role != seam.RoleUser {
			continue
		}
		var b strings.Builder
		for _, bl := range m.Blocks {
			if bl.Type == seam.BlockText {
				b.WriteString(bl.Text + "\n")
			}
		}
		if b.Len() > 0 {
			return b.String()
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

// taskOf is the task in a part's message.
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
