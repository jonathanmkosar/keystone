package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

// ToolName is the tool a model starts a background task with.
const ToolName = "start_background_task"

// Tool returns start_background_task, the tool a model starts a task
// with from the chat its turn runs in. Starting a task changes something:
// it starts work that runs tools and spends money without the person, so
// it asks before it runs unless the person sets it to always, and it
// always asks once the chat has read outside content. Its result is
// Keystone's own words.
func (r *Runner) Tool() tools.Tool {
	return tools.Tool{
		Name: ToolName,
		Description: "Start a background task: work that goes on by itself, in a chat of its own with the same tools, while the person carries on. " +
			"Use it when the person asks for something to be done in the background, or for long work of many steps that need not be answered now. " +
			"Write the task so it stands alone, with everything it needs, because its chat does not see this conversation. " +
			"When the task ends, its result is written in this chat; do not wait for it.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"title":{"type":"string","description":"The task's name, in a few words."},` +
			`"task":{"type":"string","description":"What to do, in full."}},"required":["task"]}`),
		Inside: true,
		Describe: func(input json.RawMessage) string {
			var args struct {
				Title string `json:"title"`
				Task  string `json:"task"`
			}
			json.Unmarshal(input, &args)
			title := r.titleOf(args.Title, args.Task)
			if title == "" {
				return "Start a background task."
			}
			return fmt.Sprintf("Start a background task: “%s”. It runs in a chat of its own, with the same tools, and spends up to its budget.", title)
		},
		Run: func(ctx context.Context, in tools.Input) (string, error) {
			var args struct {
				Title string `json:"title"`
				Task  string `json:"task"`
			}
			if err := json.Unmarshal(in.Args, &args); err != nil {
				return "", fmt.Errorf("the input does not read: give task, and title if you like")
			}
			t, err := r.Start(ctx, in.Store, in.ChatID, audit.RequestID(ctx), api.ActorPerson, StartedByTool,
				StartBody{Text: args.Task, Title: args.Title})
			if err != nil {
				return "", fmt.Errorf("the background task was not started: %s", strings.TrimPrefix(err.Error(), errInvalid.Error()+": "))
			}
			return fmt.Sprintf("The background task “%s” has started, as task %s. It runs in a chat of its own, and its result will be written in this chat when it ends. "+
				"Tell the person it has started; do not wait for it.", t.Title, t.ID), nil
		},
	}
}

// AddTool adds start_background_task to a registry.
func (r *Runner) AddTool(reg *tools.Registry) error { return reg.Add(r.Tool()) }
