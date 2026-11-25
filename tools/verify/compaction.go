package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/budget"
	"github.com/jonathanmkosar/keystone/internal/compact"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Compaction (P5-02) keeps the summaries of a chat's oldest turns, and a
// summary of a chat that was sent sensitive content goes only to a call
// whose every model may read it: compact.Usable takes the model's plain
// answer. The package enforces rule 3 beside the registry and routing.
func init() {
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/compact")
}

// compactWindows is how many times a model's window the grown chat is.
const compactWindows = 10

// compactionProblems is the compaction step of the "context fit" check,
// the item's proof: a chat ten times the window never exceeds it, and its
// pinned messages survive.
//
// For every entry in the fixture models, a chat is grown one turn at a
// time to ten times the model's window, with two messages pinned: the
// first question and an answer from early on. Each call is built by the
// context builder with the stored summary and the pins, as a turn builds
// it. After each call the oldest turns are summarised as the turn does it,
// with internal/compact, by the test tenant's fake provider behind the
// model's own guard, which answers with its recorded summary. The step
// proves:
//
//   - every call is one a provider takes, fits the window by the budget's
//     own check, and ends with the person's newest message;
//   - every pinned message is in every call, in its turn or on its own;
//   - each summary extends the one before it;
//   - for a model whose summary share can hold the recorded summary, the
//     last call carries it in place of the oldest turns.
func compactionProblems(entries []models.Model, o budget.Options) ([]string, error) {
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	opts, err := contextbuild.FromSettings(reg, nil)
	if err != nil {
		return nil, err
	}
	shipped := func(key string) string {
		s, _ := settings.Lookup(reg, key)
		return s.Default
	}
	whole := func(key string) int {
		n, _ := strconv.Atoi(shipped(key))
		return n
	}
	co := compact.Options{
		TriggerPercent: whole("chats.compact_trigger_percent"), KeepPercent: whole("chats.compact_keep_percent"),
		SummaryMaxTokens: whole("chats.compact_summary_max_tokens"), Prompt: shipped("chats.compact_prompt"),
		MessageMaxChars: whole("chats.compact_message_max_chars"), CallsMax: whole("chats.compact_calls_max"),
	}
	if err := compact.CheckOptions(co); err != nil {
		return nil, err
	}
	heading, pinHeading := shipped("chats.compact_summary_heading"), shipped("chats.context_pinned_heading")
	fx, err := testtenant.Shipped()
	if err != nil {
		return nil, err
	}
	summariser, err := testtenant.Provider(fx, fake.Options{})
	if err != nil {
		return nil, err
	}
	estimate := func(text string) int {
		return budget.Estimate(seam.Request{System: []seam.Block{{Type: seam.BlockText, Text: text}}}, o)
	}
	withHeading := func(h, text string) string { return h + "\n\n" + text }

	ctx := context.Background()
	var problems []string
	for _, m := range entries {
		bad := func(format string, args ...any) {
			problems = append(problems, fmt.Sprintf("model %s, a chat grown to %d times its window: %s", m.Name, compactWindows, fmt.Sprintf(format, args...)))
		}
		guarded := budget.Guard(m, summariser, o)
		fits := func(req seam.Request) bool {
			return budget.Estimated(req, o).Tokens <= budget.InputRoom(m, req.MaxOutputTokens)
		}
		say := func(role seam.Role, text string) seam.Message {
			return seam.Message{Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}}
		}
		// Each message is about a fortieth of the window, so a turn is
		// about a twentieth and the chat about two hundred turns.
		filler := strings.Repeat("the tide table for the harbour mouth ", m.ContextWindow*3/40/37+1)
		var messages []seam.Message
		var summary compact.Summary
		covers, size, summaries, broken := 0, 0, 0, false
		pinAt := []int{0, 2*59 + 1} // the first question, and the answer of turn 60
		var last contextbuild.Record
		for turn := 1; size < compactWindows*m.ContextWindow && !broken; turn++ {
			messages = append(messages, say(seam.RoleUser, fmt.Sprintf("Question %d: %s", turn, filler)))
			var pins []contextbuild.Pin
			for _, i := range pinAt {
				if i < len(messages)-1 {
					pins = append(pins, contextbuild.Pin{Message: i, Item: contextbuild.Item{Label: "pinned", Text: withHeading(pinHeading, seam.Text(messages[i]))}})
				}
			}
			in := contextbuild.Input{Model: m, Budget: o, Options: opts, Messages: messages, Pinned: pins}
			if summary.Text != "" {
				in.Summary, in.SummaryCovers = withHeading(heading, summary.Text), covers
			}
			req, rec, err := contextbuild.Build(ctx, in)
			if err != nil {
				bad("turn %d was not packed: %v", turn, err)
				break
			}
			last = rec
			if err := seam.ValidateRequest(req); err != nil {
				bad("turn %d: the request is not valid: %v", turn, err)
				break
			}
			if _, err := budget.Check(ctx, m, nil, req, o); err != nil || rec.Over != 0 {
				bad("turn %d: the call does not fit: %v", turn, err)
				break
			}
			if seam.Text(req.Messages[len(req.Messages)-1]) != seam.Text(messages[len(messages)-1]) {
				bad("turn %d: the call does not end with the person's newest message", turn)
			}
			for _, p := range pins {
				found := false
				for _, msg := range req.Messages {
					found = found || seam.Text(msg) == seam.Text(messages[p.Message])
				}
				for _, b := range req.System {
					found = found || b.Text == p.Text
				}
				if !found {
					bad("turn %d: the pinned message %d is not in the call", turn, p.Message)
					broken = true
				}
			}

			// The oldest turns are summarised as the turn does it, and the
			// summary is used from the next call on.
			earlier := rec.Turns[:len(rec.Turns)-1]
			turns := make([]compact.Turn, len(earlier))
			spans := make([][2]int, len(earlier))
			done := 0
			for i, x := range earlier {
				turns[i] = compact.Turn{Tokens: x.Tokens, Kept: x.Kept, Summarised: summary.Text != "" && x.First+x.Messages <= covers}
				spans[i] = [2]int{x.First, x.First + x.Messages}
				if turns[i].Summarised {
					done++
				}
			}
			span, ok := compact.Plan(turns, rec.TurnRoom, co)
			if !ok && summary.Text != "" && rec.Part(contextbuild.PartSummary).Kept == 0 && done < len(turns) {
				span, ok = compact.Span{From: done, To: done + 1}, true
			}
			for call := 0; ok && call < co.CallsMax && span.From < span.To; call++ {
				sreq, to, err := compact.Request(compact.Ask{
					Options: co, Model: m.Name, Previous: summary.Text, Messages: messages, Turns: spans, Span: span,
					Room: rec.SummaryRoom - estimate(withHeading(heading, " ")), CharsPerToken: o.CharsPerToken, Fits: fits,
				})
				if err != nil {
					break // no room for a summary: the oldest turns are left out with the note
				}
				// A summary is extended: the summary so far goes with the
				// turns that follow it, and the new one stands for more.
				if summary.Text != "" && (!strings.Contains(seam.Text(sreq.Messages[0]), summary.Text) || to <= summary.Turns) {
					bad("turn %d: a summary does not extend the one before it", turn)
				}
				resp, err := seam.Call(ctx, guarded, sreq)
				if err != nil {
					bad("turn %d: the summary call failed: %v", turn, err)
					broken = true
					break
				}
				text, err := compact.Answer(resp)
				if err != nil {
					bad("turn %d: %v", turn, err)
					broken = true
					break
				}
				summary = compact.Summary{ID: int64(summaries + 1), Extends: summary.ID, Turns: to, Text: text}
				summaries++
				covers, span.From = rec.Turns[to].First, to
			}

			reply := say(seam.RoleAssistant, fmt.Sprintf("Answer %d: %s", turn, filler))
			messages = append(messages, reply)
			size += budget.Estimate(seam.Request{Messages: messages[len(messages)-2:]}, o)
		}
		if broken {
			continue
		}
		recorded := ""
		if summary.Text != "" {
			recorded = withHeading(heading, summary.Text)
		}
		switch {
		case summaries == 0:
			bad("no summary was made")
		case estimate(recorded) <= last.SummaryRoom && (last.Part(contextbuild.PartSummary).State != contextbuild.StateKept || last.Part(contextbuild.PartSummary).Covers == 0):
			bad("the last call did not carry the summary in place of the oldest turns: %+v", last.Part(contextbuild.PartSummary))
		}
	}
	return problems, nil
}
