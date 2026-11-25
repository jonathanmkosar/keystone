// Package jobs is background tasks (P8-01): work a person or a model starts
// in a chat that goes on by itself, outlives the tab and the request that
// started it, resumes after a restart, and writes its result in the chat.
//
// A task is not a second runner beside the turns. It works through
// ordinary turns of a chat of its own, its work chat, one round at a time:
// the first round's message is the task, and a round that ends at the
// turn's step limit, or that a restart cut short, is followed by another.
// So every model call and tool call it makes is a turn's: streamed, kept in
// the chat record, recorded in the audit log, asked about under the
// approval policy, and stopped the way a turn is. What this package keeps
// is the task's own row and its events, and it writes the task's result in
// the chat it was started from when it ends.
//
// docs/architecture/jobs.md is the contract.
package jobs

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/settings"
)

// The statuses of a task.
const (
	// StatusQueued is a task that has not started its first round.
	StatusQueued = "queued"
	// StatusRunning is a task with a round under way, or about to be.
	StatusRunning = "running"
	// StatusDone is a task that finished: its result is in Result.
	StatusDone = "done"
	// StatusStopped is a task the person stopped.
	StatusStopped = "stopped"
	// StatusFailed is a task that ended without finishing. Error says why.
	StatusFailed = "failed"
)

// Who started a task.
const (
	StartedByPerson = "person" // through POST chats/{id}/tasks
	StartedByTool   = "tool"   // a model, with start_background_task
)

// The names of the events a task streams.
const (
	// EventTask is the task as it stands, sent whenever it changes.
	EventTask = "task"
	// EventEnd is always the last event: the task as it ended, once its
	// result is written in the chat it was started from.
	EventEnd = "end"
)

// The codes of a task's error, beside the codes of the turn a round ended
// with (spend_cap, no_model, model_unavailable, too_long, and the seam's
// kinds), which are passed on as they are.
const (
	// CodeRoundLimit says the task took agents.task_max_rounds rounds and
	// had not finished.
	CodeRoundLimit = "round_limit"
	// CodeChatDeleted says the task's work chat was deleted.
	CodeChatDeleted = "chat_deleted"
	// CodeFailed says the task failed inside Keystone. The reason is in the
	// server's log under the task's request id.
	CodeFailed = "failed"
)

// TaskError says why a task ended without finishing.
type TaskError struct {
	Code string `json:"code"`
	// Message is for a person.
	Message string `json:"message"`
}

// Task is one background task.
type Task struct {
	ID string `json:"id"`
	// ChatID is the chat it was started from. Its result is written there.
	ChatID string `json:"chat_id"`
	// WorkChatID is the chat of its own its turns run in. A person can open
	// it to watch every step, and answer its approval cards there.
	WorkChatID string `json:"work_chat_id"`
	// Title names it in a few words, and Task is the words it was given.
	Title string `json:"title"`
	Task  string `json:"task"`
	// StartedBy is person or tool.
	StartedBy string `json:"started_by"`
	// Status is queued, running, done, stopped or failed.
	Status string `json:"status"`
	// Waiting says a tool call of its round waits for the person's
	// approval, in its work chat.
	Waiting bool `json:"waiting"`
	// Progress says in a few words where it is.
	Progress string `json:"progress"`
	// Rounds counts the turns it has sent, of MaxRounds at most, and Steps
	// the model calls they have finished.
	Rounds    int `json:"rounds"`
	MaxRounds int `json:"max_rounds"`
	Steps     int `json:"steps"`
	// TurnID is its round's turn, in WorkChatID.
	TurnID string `json:"turn_id,omitempty"`
	// BudgetMicros is what it may spend on models that are not local, and
	// SpentMicros what it has, in millionths of a US dollar.
	BudgetMicros int64 `json:"budget_micros"`
	SpentMicros  int64 `json:"spent_micros"`
	// Result is the text of its last reply, once it is done.
	Result string `json:"result,omitempty"`
	// Error is set on a task that failed.
	Error *TaskError `json:"error,omitempty"`
	// NoteMessageID is the message in ChatID that holds its result, once
	// it is written.
	NoteMessageID int64      `json:"note_message_id,omitempty"`
	Created       time.Time  `json:"created"`
	Started       *time.Time `json:"started,omitempty"`
	Ended         *time.Time `json:"ended,omitempty"`
	// LastEventID is the id of the newest event it has streamed.
	LastEventID int64 `json:"last_event_id"`
}

// StartBody starts a task.
type StartBody struct {
	// Text is the task: what to do, written so it stands alone, since the
	// work chat does not see the chat it was started from.
	Text string `json:"text"`
	// Title names the task in a few words. Absent means the first words of
	// the text.
	Title string `json:"title,omitempty"`
}

// TasksReply answers GET chats/{id}/tasks.
type TasksReply struct {
	// Tasks are the ones started from the chat or working in it, oldest
	// first.
	Tasks []Task `json:"tasks"`
}

// streamEvents is what a task's stream sends, for the OpenAPI document.
var streamEvents = []api.EventDoc{
	{Name: EventTask, Description: "The task as it stands. It is the first event, and is sent again whenever the task changes: a round starts, a step ends, a call waits for the person's approval, the task ends.", Data: Task{}},
	{Name: EventEnd, Description: "Always the last event: the task as it ended, with the status done, stopped or failed, once its result is written in the chat it was started from. The stream closes after it.", Data: Task{}},
}

// Config is the values of the install's settings that tasks run with, in
// settings/agents.json. agents.task_budget_usd is a person's own, and is
// read for each task when it starts.
type Config struct {
	MaxRounds    int    // agents.task_max_rounds
	OpenMax      int    // agents.task_open_max
	ContinueText string // agents.task_continue_text
	ResumeText   string // agents.task_resume_text
}

// KeyBudget is the person's budget for each background task, in US dollars.
const KeyBudget = "agents.task_budget_usd"

// ReadConfig builds the configuration from wherever setting values are
// kept. get returns the value for a setting's key.
func ReadConfig(get func(key string) (string, bool)) (Config, error) {
	var c Config
	text := map[string]string{}
	for _, key := range []string{"agents.task_max_rounds", "agents.task_open_max", "agents.task_continue_text", "agents.task_resume_text"} {
		v, ok := get(key)
		if !ok {
			return Config{}, fmt.Errorf("the %s setting is missing", key)
		}
		text[key] = v
	}
	var err error
	if c.MaxRounds, err = strconv.Atoi(text["agents.task_max_rounds"]); err != nil {
		return Config{}, fmt.Errorf("agents.task_max_rounds: %w", err)
	}
	if c.OpenMax, err = strconv.Atoi(text["agents.task_open_max"]); err != nil {
		return Config{}, fmt.Errorf("agents.task_open_max: %w", err)
	}
	c.ContinueText = strings.TrimSpace(text["agents.task_continue_text"])
	c.ResumeText = strings.TrimSpace(text["agents.task_resume_text"])
	switch {
	case c.MaxRounds < 1:
		return Config{}, errors.New("agents.task_max_rounds must be 1 or more")
	case c.OpenMax < 1:
		return Config{}, errors.New("agents.task_open_max must be 1 or more")
	case c.ContinueText == "" || c.ResumeText == "":
		return Config{}, errors.New("agents.task_continue_text and agents.task_resume_text cannot be empty: each is the message a round is sent with")
	}
	return c, nil
}

// DefaultConfig reads the configuration from the settings registry's
// defaults.
func DefaultConfig() (Config, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Config{}, err
	}
	return ReadConfig(func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	})
}
