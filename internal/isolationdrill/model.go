package isolationdrill

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The drill's model: one local model on the seam that both people use. It
// keeps nothing between calls but what the drill gives it, and decides each
// reply from the request alone:
//
//   - the memory pass's call, known by the shipped memory prompt, is
//     answered with facts from the exchange it reads;
//   - the built-in store's compile job, known by its prompt, reads with its
//     tools and then answers with one batch;
//   - any other call looks for a directive, "drill:<name>", in the last
//     message the person wrote, and plays that directive's steps: each step
//     is the blocks of one reply, and a step with tool calls stops for them.
//     The step is the number of replies since the person's message, so a
//     turn that calls tools gets the next step when the results come back.
//
// The directives' steps are set by the drill once the ids they name exist.
type model struct {
	mu    sync.Mutex
	plans map[string][][]seam.Block
	// wiki is the compile job's first step and its answer.
	wiki [2][]seam.Block
	// facts maps a phrase of an exchange to the facts the memory pass finds
	// in it.
	facts map[string][]string
	// keys are the keys the model was opened with, as the vault gave them.
	keys []string
	// calls counts the requests, for the tool calls' ids.
	calls atomic.Int64
}

// The phrases the model knows its other callers by: the shipped prompts of
// the memory pass and of the built-in store's compile job.
const (
	memoryPhrase = "long-term memory of a personal assistant"
	wikiPhrase   = "compile job of this person's built-in knowledge store"
)

var directive = regexp.MustCompile(`drill:([a-z0-9-]+)`)

func newModel() *model {
	return &model{plans: map[string][][]seam.Block{}, facts: map[string][]string{}}
}

// build is the Chat Completions adapter's builder in the drill's server.
func (m *model) build(_ models.Model, key string) (seam.Provider, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key != "" {
		m.keys = append(m.keys, key)
	}
	return m, nil
}

// plan sets a directive's steps.
func (m *model) plan(name string, steps ...[]seam.Block) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.plans[name] = steps
}

// openedWith returns the keys the model was opened with.
func (m *model) openedWith() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.keys...)
}

// text is a text block.
func text(s string) seam.Block { return seam.Block{Type: seam.BlockText, Text: s} }

// use is a tool call block. Its id is set when it is sent.
func use(name string, input any) seam.Block {
	raw, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}
	return seam.Block{Type: seam.BlockToolCall, Name: name, Input: raw}
}

func (m *model) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	var system strings.Builder
	for _, b := range req.System {
		system.WriteString(b.Text)
	}
	// The person's last message, and how many replies follow it.
	person, replies := "", 0
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == seam.RoleAssistant {
			replies++
			continue
		}
		if results := hasResults(msg); results {
			continue
		}
		person = seam.Text(msg)
		break
	}
	var all strings.Builder
	all.WriteString(system.String())
	for _, msg := range req.Messages {
		all.WriteString(seam.Text(msg))
	}

	var blocks []seam.Block
	m.mu.Lock()
	switch {
	case strings.Contains(system.String(), memoryPhrase):
		facts := []string{}
		for phrase, found := range m.facts {
			if strings.Contains(all.String(), phrase) {
				facts = append(facts, found...)
			}
		}
		blocks = []seam.Block{text(jsonOf(map[string][]string{"facts": facts}))}
	case strings.Contains(all.String(), wikiPhrase):
		if replies == 0 {
			blocks = m.wiki[0]
		} else {
			blocks = m.wiki[1]
		}
	default:
		name := ""
		if found := directive.FindStringSubmatch(person); found != nil {
			name = found[1]
		}
		steps := m.plans[name]
		if replies < len(steps) {
			blocks = steps[replies]
		}
	}
	m.mu.Unlock()
	if len(blocks) == 0 {
		blocks = []seam.Block{text("Noted.")}
	}

	// Each tool call gets an id no other call of the drill has.
	stop := seam.StopEnd
	out := make([]seam.Block, len(blocks))
	for i, b := range blocks {
		if b.Type == seam.BlockToolCall {
			b.ID = fmt.Sprintf("call_%d_%d", m.calls.Add(1), i)
			stop = seam.StopToolCall
		}
		out[i] = b
	}
	evs, err := seam.Events(seam.Response{Model: req.Model, Message: seam.Message{Role: seam.RoleAssistant, Blocks: out},
		StopReason: stop, Usage: seam.Usage{InputTokens: 100, OutputTokens: 20}})
	if err != nil {
		return err
	}
	for _, e := range evs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(e); err != nil {
			return err
		}
	}
	return nil
}

func hasResults(msg seam.Message) bool {
	for _, b := range msg.Blocks {
		if b.Type == seam.BlockToolResult {
			return true
		}
	}
	return false
}
