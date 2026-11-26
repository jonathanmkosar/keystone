package subagents

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/subagents/carry"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// report is the tool's result once every sub-agent of a call has ended: a
// line for the call, then a section for each sub-agent with its status,
// steps, spend, any model that stood in for it, its result cut at
// agents.subagent_result_max_chars, and its chat, which holds the whole of
// its work.
//
// What passes is decided by carry.Up (fixed rules 2 and 3): the result of a
// sub-agent that was sent sensitive content stays in its chat unless the
// parent chat is held too; one that read outside content marks the parent
// chat first, and the whole result then opens with the outside mark. A mark
// that cannot be written gives no result at all.
func (r *Runner) report(ctx context.Context, s *tenant.Store, b batch) outcome {
	rows, err := r.rows(ctx, s, b)
	if err != nil {
		r.logf("sub-agents: reading the sub-agents of tenant %s for their results: %v", b.tenantID, err)
		return outcome{err: errors.New("the sub-agents' results could not be read; the reason is in the server's log")}
	}
	from := r.bound().Carried(ctx, s, b.chatID)
	parent := carry.Marks{Outside: from.Outside != "", Sensitive: from.Sensitive}
	var sections []string
	counts := map[string]int{}
	outside := false
	for _, a := range rows {
		pass, out := carry.Up(parent, carry.Marks{Outside: a.Outside, Sensitive: a.Sensitive})
		outside = outside || out
		if err := change(ctx, s, a.ID, "passed = ?", pass); err != nil && !errors.Is(err, errNoSubagent) {
			r.logf("sub-agents: %s noting whether the result of sub-agent %s was passed back: %v", a.requestID, a.ID, err)
		}
		counts[a.Status]++
		sections = append(sections, r.section(a, len(rows), pass))
	}
	if outside && !parent.Outside {
		if err := r.markOutside(ctx, s, b); err != nil {
			r.logf("sub-agents: marking chat %s of tenant %s as having read outside content, so no result is given: %v", b.chatID, b.tenantID, err)
			return outcome{err: errors.New("the sub-agents' results could not be passed on: one of them read outside content, and this chat could not be marked as having read it. Their chats hold their work")}
		}
	}
	text := head(len(rows), counts) + "\n\n" + strings.Join(sections, "\n\n")
	if outside {
		text = tools.MarkOutside(text)
	}
	return outcome{text: text}
}

// markOutside marks the parent chat as having read outside content, under
// the turn that made the call, so every later tool call there that changes
// something asks (fixed rule 2).
func (r *Runner) markOutside(ctx context.Context, s *tenant.Store, b batch) error {
	if b.turnID == "" {
		return errors.New("the call has no turn to mark the chat under")
	}
	_, err := s.DB().ExecContext(ctx, "INSERT INTO tool_outside(turn_id, chat_id, tool, why, at) VALUES(?, ?, ?, ?, ?)",
		b.turnID, b.chatID, ToolName, "this chat read the result of a sub-agent that read outside content", stamp())
	return err
}

// head is the result's first line: how many sub-agents ran and how they
// ended.
func head(n int, counts map[string]int) string {
	var parts []string
	for _, x := range []struct{ status, words string }{
		{StatusDone, "done"}, {StatusFailed, "did not finish"}, {StatusStopped, "stopped"}, {StatusPaused, "paused"},
	} {
		if counts[x.status] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[x.status], x.words))
		}
	}
	noun := "sub-agents"
	if n == 1 {
		noun = "sub-agent"
	}
	return fmt.Sprintf("%d %s ran: %s. Each one's result follows; its whole work is in its own chat.", n, noun, strings.Join(parts, ", "))
}

// section is what the turn is told of one sub-agent.
func (r *Runner) section(a row, n int, pass bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Sub-agent %d of %d, “%s”: %s.\n", a.Position, n, a.Title, statusWords(a))
	calls := "model calls"
	if a.Steps == 1 {
		calls = "model call"
	}
	fmt.Fprintf(&b, "It made %d %s and spent %s of its budget of %s.", a.Steps, calls, usage.Dollars(a.SpentMicros), usage.Dollars(a.BudgetMicros))
	if a.Error != nil && strings.TrimSpace(a.Error.Message) != "" {
		b.WriteString("\n" + strings.TrimSpace(a.Error.Message))
	}
	if a.Notice != "" {
		b.WriteString("\n" + a.Notice)
	}
	switch {
	case !pass:
		b.WriteString("\nIt was sent content from a sensitive space, so its result stays in its own chat and is not passed to this one: this chat may reach a model that may not read it.")
	case a.Result != "" && a.Status == StatusDone:
		b.WriteString("\nIts result:\n" + r.cut(a.Result))
	case a.Result != "":
		b.WriteString("\nWhat it had written:\n" + r.cut(a.Result))
	case a.Status == StatusDone:
		b.WriteString("\nIt gave no result in words.")
	}
	fmt.Fprintf(&b, "\nIts chat is %s; read_chat with that chat_id reads its whole transcript.", a.WorkChatID)
	return b.String()
}

// statusWords says how a sub-agent ended, for the turn.
func statusWords(a row) string {
	switch a.Status {
	case StatusDone:
		return "done"
	case StatusStopped:
		if a.stopWhy == byParent {
			return "stopped, with the turn that started it"
		}
		return "stopped by the person before it finished"
	case StatusPaused:
		return "paused: every agent of the person's was paused, so it was stopped before it finished"
	}
	return "did not finish"
}

// cut keeps the first agents.subagent_result_max_chars characters of a
// result, and says so when it left some out.
func (r *Runner) cut(text string) string {
	most := r.o.Config.ResultChars
	runes := []rune(text)
	if most <= 0 || len(runes) <= most {
		return text
	}
	return string(runes[:most]) + fmt.Sprintf("\n[Cut here: it had %d characters, and the rest is in its chat.]", len(runes))
}
