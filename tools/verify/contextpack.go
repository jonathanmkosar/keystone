package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// overWindow is how many times a model's window the generated chat is.
const overWindow = 4

// packingProblems is the packing step of the "context fit" check, the
// plan's own wording of it: for every entry in the fixture models, the
// largest test chat is packed under its window. A generated chat several
// times the window is packed under it too.
//
// The context builder (internal/contextbuild) does the packing, with the
// shipped settings. For each model and each chat the step proves:
//
//   - the request is one a provider takes, and ends with the chat's
//     current message;
//   - the budget accepts it, by the estimate and by a provider's own count
//     that runs higher than the estimate;
//   - the record says it fits, and counts what the budget counts;
//   - the test chat, which fits every fixture window, is sent whole;
//   - the generated chat loses turns, and only its oldest.
func packingProblems(entries []models.Model, o budget.Options) ([]string, error) {
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	opts, err := contextbuild.FromSettings(reg, nil)
	if err != nil {
		return nil, err
	}
	largest, name, err := largestTestChat(o)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	var problems []string
	for _, m := range entries {
		chats := []struct {
			name     string
			messages []seam.Message
			whole    bool
		}{
			{"the test chat " + name, largest, true},
			{"a generated chat", generatedChat(m, o), false},
		}
		for _, c := range chats {
			for _, p := range []seam.Provider{nil, countsHigh{o}} {
				bad := func(format string, args ...any) {
					how := "estimated"
					if p != nil {
						how = "counted by the provider"
					}
					problems = append(problems, fmt.Sprintf("model %s, %s, %s: %s", m.Name, c.name, how, fmt.Sprintf(format, args...)))
				}
				in := contextbuild.Input{Model: m, Budget: o, Options: opts, Provider: p, Messages: c.messages}
				req, rec, err := contextbuild.Build(ctx, in)
				if err != nil {
					bad("it was not packed: %v", err)
					continue
				}
				if err := seam.ValidateRequest(req); err != nil {
					bad("the packed request is not valid: %v", err)
					continue
				}
				fit, err := budget.Check(ctx, m, p, req, o)
				if err != nil {
					bad("the packed request does not fit: %v", err)
					continue
				}
				if rec.Over != 0 || rec.Tokens != fit.Input.Tokens || rec.Exact != (p != nil) {
					bad("the record says %d tokens and %d over, and the budget counts %d", rec.Tokens, rec.Over, fit.Input.Tokens)
				}
				if last := req.Messages[len(req.Messages)-1]; seam.Text(last) != seam.Text(c.messages[len(c.messages)-1]) {
					bad("the packed request does not end with the current message")
				}
				dropped, gap := 0, false
				for i, t := range rec.Turns {
					if !t.Kept {
						dropped++
						gap = gap || (i > 0 && rec.Turns[i-1].Kept)
					}
				}
				switch {
				case gap:
					bad("a turn was left out after one that was kept")
				case c.whole && (dropped != 0 || len(req.Messages) != len(c.messages)):
					bad("%d of its %d turns were left out, and it fits", dropped, len(rec.Turns))
				case !c.whole && (dropped == 0 || dropped == len(rec.Turns)):
					bad("%d of its %d turns were left out", dropped, len(rec.Turns))
				}
			}
		}
	}
	return problems, nil
}

// largestTestChat returns the current branch of the test tenant's largest
// chat, by the estimate, with the next message a person might send.
func largestTestChat(o budget.Options) ([]seam.Message, string, error) {
	fx, err := testtenant.Shipped()
	if err != nil {
		return nil, "", err
	}
	var largest []seam.Message
	name, size := "", -1
	for _, c := range fx.Chats {
		// The branch each message was added to, as the loader makes it: an
		// edit goes where the message it replaces went.
		before := make([][]seam.Message, len(c.Messages))
		var branch []seam.Message
		for i, m := range c.Messages {
			switch {
			case m.Edits > 0:
				before[i] = before[m.Edits-1]
			default:
				before[i] = branch
			}
			branch = append(append([]seam.Message(nil), before[i]...), m.Message, c.Replies[i])
		}
		if n := budget.Estimate(seam.Request{Messages: branch}, o); n > size {
			largest, name, size = branch, c.File, n
		}
	}
	if size < 0 {
		return nil, "", errors.New("the test tenant has no chats")
	}
	next := seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Thank you. One more question about that."}}}
	return append(largest, next), name, nil
}

// generatedChat is an invented chat estimated at overWindow times the
// model's window. Its turns are small beside the smallest fixture window,
// and every fourth one uses a tool.
func generatedChat(m models.Model, o budget.Options) []seam.Message {
	text := func(role seam.Role, s string) seam.Message {
		return seam.Message{Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: s}}}
	}
	filler := strings.Repeat("the tide table for the harbour mouth ", 4)
	var out []seam.Message
	want := m.ContextWindow * overWindow
	for i, size := 1, 0; size < want; i++ {
		turn := []seam.Message{text(seam.RoleUser, fmt.Sprintf("Question %d: %s", i, filler))}
		if i%4 == 0 {
			id := fmt.Sprintf("call-%d", i)
			turn = append(turn,
				seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{
					{Type: seam.BlockToolCall, ID: id, Name: "tide_table", Input: json.RawMessage(`{"day":"tomorrow"}`)},
				}},
				seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{
					{Type: seam.BlockToolResult, ID: id, Content: []seam.Block{{Type: seam.BlockText, Text: "High water at six."}}},
				}},
			)
		}
		turn = append(turn, text(seam.RoleAssistant, fmt.Sprintf("Answer %d: %s", i, filler)))
		size += budget.Estimate(seam.Request{Messages: turn}, o)
		out = append(out, turn...)
	}
	return append(out, text(seam.RoleUser, "And the day after?"))
}

// countsHigh is a provider that counts every request as half as much
// again as the estimate, and is never called. It stands for a provider
// whose tokens run smaller than Keystone assumes.
type countsHigh struct{ o budget.Options }

func (countsHigh) Stream(context.Context, seam.Request, func(seam.Event) error) error {
	return errors.New("the context fit check sends nothing")
}

func (c countsHigh) CountTokens(_ context.Context, req seam.Request) (int, error) {
	return budget.Estimate(req, c.o) * 3 / 2, nil
}
