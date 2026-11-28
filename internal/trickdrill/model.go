package trickdrill

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The drill's model: one scripted model on the seam that answers for the
// person's local model and their hosted one, and keeps every request each
// is sent. It decides each reply from the request alone: it finds a
// directive, "drill:<name>", in the last message the person wrote (for a
// sub-agent, its task), and plays that directive's steps, one step for
// each reply since that message. A step with tool calls stops for them.
//
// A step that obeys a planted instruction names the planted words it
// needs. It is played only when the request holds them, in a tool result
// or in the task a sub-agent was given: the model obeys only what reached
// it. A step whose words are missing answers with a line saying so, and
// the drill reports the case as one whose plant never arrived.
type model struct {
	mu    sync.Mutex
	plans map[string][]step
	// heard keeps every request's text, by the model's registry name.
	heard map[string][]string
	// missed are the directives whose planted words did not reach them.
	missed []string
}

// step is one reply of a directive.
type step struct {
	// needs are the planted words the step obeys. Empty for a step that
	// obeys nothing, such as the first, which only reads.
	needs string
	// blocks are the reply.
	blocks []seam.Block
}

// missedLine opens what the model says when a step's planted words are not
// in its request.
const missedLine = "The planted words did not reach me: "

var directive = regexp.MustCompile(`drill:([a-z0-9-]+)`)

func newModel() *model {
	return &model{plans: map[string][]step{}, heard: map[string][]string{}}
}

// build is the adapters' builder for both models in the drill's server.
func (m *model) build(entry models.Model, _ string) (seam.Provider, error) {
	return &ear{m: m, name: entry.Name}, nil
}

// plan sets a directive's steps.
func (m *model) plan(name string, steps ...step) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.plans[name] = steps
}

// requests returns the text of every request one model was sent.
func (m *model) requests(name string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.heard[name]...)
}

// missing returns the directives whose planted words did not arrive.
func (m *model) missing() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.missed...)
}

// ear is the model as one registry entry sees it.
type ear struct {
	m    *model
	name string
}

// reads is a step that reads and obeys nothing.
func reads(blocks ...seam.Block) step { return step{blocks: blocks} }

// obeys is a step played only when the planted words reached the model.
func obeys(needs string, blocks ...seam.Block) step { return step{needs: needs, blocks: blocks} }

// text is a text block.
func text(s string) seam.Block { return seam.Block{Type: seam.BlockText, Text: s} }

// use is a tool call block with the id the drill finds it by.
func use(id, name string, input any) seam.Block {
	raw, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}
	return seam.Block{Type: seam.BlockToolCall, ID: id, Name: name, Input: raw}
}

func (e *ear) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	raw, _ := json.Marshal(req)
	// The person's last message, and how many replies follow it.
	person, replies := "", 0
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == seam.RoleAssistant {
			replies++
			continue
		}
		if hasResults(msg) {
			continue
		}
		person = seam.Text(msg)
		break
	}
	var all strings.Builder
	for _, msg := range req.Messages {
		all.WriteString(seam.Text(msg))
		for _, b := range msg.Blocks {
			for _, c := range b.Content {
				all.WriteString(c.Text)
			}
		}
		all.WriteString("\n")
	}
	// The tools offered are read too: a connector's own words are in them.
	for _, t := range req.Tools {
		all.WriteString(t.Description)
		all.WriteString("\n")
	}

	var blocks []seam.Block
	e.m.mu.Lock()
	e.m.heard[e.name] = append(e.m.heard[e.name], string(raw))
	name := ""
	if found := directive.FindStringSubmatch(person); found != nil {
		name = found[1]
	}
	steps := e.m.plans[name]
	if replies < len(steps) {
		s := steps[replies]
		if s.needs != "" && !strings.Contains(all.String(), s.needs) {
			e.m.missed = append(e.m.missed, fmt.Sprintf("%s (step %d needs %q)", name, replies+1, s.needs))
			blocks = []seam.Block{text(missedLine + s.needs)}
		} else {
			blocks = s.blocks
		}
	}
	e.m.mu.Unlock()
	if len(blocks) == 0 {
		blocks = []seam.Block{text("Done.")}
	}

	stop := seam.StopEnd
	for _, b := range blocks {
		if b.Type == seam.BlockToolCall {
			stop = seam.StopToolCall
		}
	}
	evs, err := seam.Events(seam.Response{Model: req.Model, Message: seam.Message{Role: seam.RoleAssistant, Blocks: blocks},
		StopReason: stop, Usage: seam.Usage{InputTokens: 100, OutputTokens: 20}})
	if err != nil {
		return err
	}
	for _, ev := range evs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(ev); err != nil {
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

// runSuffix makes the run's marker words its own.
func runSuffix() string {
	var b [4]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
