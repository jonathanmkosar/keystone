package turn

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

// The tool loop's part of a turn (P7-01). The tools themselves, and the
// one place a tool is run, are in internal/tools. What is here is the
// turn's side: which tools a call offers, what happens to the calls a
// model makes, and how a turn ends at its step limit.
// docs/architecture/tool-loop.md is the whole of it.

// KeyToolsEnabled is the setting a person may switch off to have no tools
// offered in their turns.
const KeyToolsEnabled = "agents.tools_enabled"

// StopStepLimit is the stop reason of the note a turn writes when it
// reaches chats.turn_max_steps while the model still asks for tools, and
// of the turn that wrote it. Such a turn ends as done.
const StopStepLimit = "step_limit"

// What a model reads in place of a result when a call was not run.
const (
	toolsOff    = "No tool was run. Tools are switched off for this person."
	toolAtLimit = "This tool was not run. The turn reached the most steps it may take."
)

// ToolLoop is the values of the install's settings that the tool loop runs
// with, all in settings/agents.json.
type ToolLoop struct {
	Timeout          time.Duration // agents.tool_timeout
	ResultMaxChars   int           // agents.tool_result_max_chars
	StepLimitNote    string        // agents.step_limit_note
	ReadChatMessages int           // agents.read_chat_max_messages
}

func readToolLoop(get func(key string) (string, bool)) (ToolLoop, error) {
	var c ToolLoop
	text := map[string]string{}
	for _, key := range []string{"agents.tool_timeout", "agents.tool_result_max_chars", "agents.step_limit_note", "agents.read_chat_max_messages"} {
		v, ok := get(key)
		if !ok {
			return ToolLoop{}, fmt.Errorf("the %s setting is missing", key)
		}
		text[key] = v
	}
	var err error
	if c.Timeout, err = time.ParseDuration(text["agents.tool_timeout"]); err != nil {
		return ToolLoop{}, fmt.Errorf("agents.tool_timeout: %w", err)
	}
	if c.ResultMaxChars, err = strconv.Atoi(text["agents.tool_result_max_chars"]); err != nil {
		return ToolLoop{}, fmt.Errorf("agents.tool_result_max_chars: %w", err)
	}
	if c.ReadChatMessages, err = strconv.Atoi(text["agents.read_chat_max_messages"]); err != nil {
		return ToolLoop{}, fmt.Errorf("agents.read_chat_max_messages: %w", err)
	}
	c.StepLimitNote = strings.TrimSpace(text["agents.step_limit_note"])
	switch {
	case c.Timeout <= 0:
		return ToolLoop{}, errors.New("agents.tool_timeout must be longer than nothing")
	case c.ResultMaxChars < 0:
		return ToolLoop{}, errors.New("agents.tool_result_max_chars cannot be negative")
	case c.StepLimitNote == "":
		return ToolLoop{}, errors.New("agents.step_limit_note cannot be empty: it is what the person reads when a turn is ended at its step limit")
	case c.ReadChatMessages < 1:
		return ToolLoop{}, errors.New("agents.read_chat_max_messages must be 1 or more")
	}
	return c, nil
}

// toolsOn reports whether this person's turns offer tools.
func (r *Runner) toolsOn(ctx context.Context, s *tenant.Store) bool {
	on, err := strconv.ParseBool(r.setting(ctx, s, KeyToolsEnabled))
	return err == nil && on
}

// offered returns the tools one model call offers: every tool in the
// registry, by name, for a model that can use tools, unless the person
// has switched tools off.
func (r *Runner) offered(ctx context.Context, s *tenant.Store, m models.Model) []seam.Tool {
	if r.o.Tools == nil || !m.Has(models.CapTools) || !r.toolsOn(ctx, s) {
		return nil
	}
	return r.o.Tools.Definitions()
}

// toolStep is what a turn does when a model call stops for tools. Every
// call is answered, in order, and the answers are stored under tail as one
// message. It returns the id of the last message it stored, and whether
// the model is to be called again.
//
//   - A call is run through the registry, which records it in the audit
//     log, lets only a tool that reads run, and turns a tool that fails,
//     panics or runs too long into an error result. Each result is
//     streamed as it comes.
//   - When the person stops the turn, the tool's context ends. The calls
//     that had not finished are stored as stopped, and the turn stops.
//   - At the step limit no tool is run. Each call is answered with a
//     refusal, and the turn writes a note the person can read as its last
//     message, with the stop reason step_limit. The turn then ends as done.
//
// When the runner is closing nothing more is written, and the turn is
// left for Recover.
func (r *Runner) toolStep(ctx context.Context, s *tenant.Store, t row, step int, tail int64, calls []seam.Block, say func(string, any)) (newTail int64, again bool, failure *TurnError, stopped bool) {
	broke := func(what string, err error) (int64, bool, *TurnError, bool) {
		r.logf("turns: %s turn %s: %s: %v", t.requestID, t.ID, what, err)
		return tail, false, &TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."}, false
	}
	atLimit := step >= r.o.Config.MaxSteps
	on := r.toolsOn(ctx, s)

	var results []seam.Block
	for _, c := range calls {
		call := tools.Call{
			Store: s, RequestID: t.requestID, Actor: t.actor, ChatID: t.ChatID,
			ID: c.ID, Name: c.Name, Input: c.Input,
			Timeout: r.o.Config.ToolLoop.Timeout, MaxResultChars: r.o.Config.ToolLoop.ResultMaxChars,
		}
		var res tools.Result
		switch {
		case atLimit:
			res = r.o.Tools.Decline(call, toolAtLimit)
		case !on:
			res = r.o.Tools.Decline(call, toolsOff)
		default:
			// After a stop this returns at once for each call that is left.
			res = r.o.Tools.Run(ctx, call)
		}
		if r.isClosing() {
			return tail, false, nil, false
		}
		if res.Log != "" {
			r.logf("turns: %s turn %s: %s", t.requestID, t.ID, res.Log)
		}
		results = append(results, res.Block)
		say(EventToolResult, ToolResultEvent{ID: res.Block.ID, IsError: res.Block.IsError, Content: res.Block.Content})
	}
	msg, err := r.store(s, t, tail, seam.RoleUser, results, meta{})
	if err != nil {
		return broke("storing the tool results", err)
	}
	tail = msg.ID
	say(EventMessage, msg)
	if ctx.Err() != nil {
		return tail, false, nil, true
	}
	if !atLimit {
		return tail, true, nil, false
	}

	note, err := r.store(s, t, tail, seam.RoleAssistant, []seam.Block{{Type: seam.BlockText, Text: r.o.Config.ToolLoop.StepLimitNote}}, meta{stopReason: StopStepLimit})
	if err != nil {
		return broke("storing the note at the step limit", err)
	}
	say(EventMessage, note)
	if _, err := s.DB().Exec("UPDATE turns SET stop_reason = ? WHERE id = ?", StopStepLimit, t.ID); err != nil {
		return broke("noting the step limit", err)
	}
	return note.ID, false, nil, false
}
