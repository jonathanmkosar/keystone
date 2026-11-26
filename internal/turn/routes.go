// Package turn is the chat and turn part of the API: the routes a chat
// screen calls, and the runner that takes a person's message through the
// model seam and streams the reply.
//
// A turn is a row in the tenant's own database, and so is every event it
// streams. The runner works a chat's turns one at a time, in its own
// goroutine with its own store, so a turn goes on when the person's
// connection drops, a client that comes back reads the events it missed,
// and a turn that was waiting when the server stopped runs when it starts
// again. docs/architecture/turns.md is the contract.
package turn

import (
	"encoding/json"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/imported"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The statuses of a turn.
const (
	// StatusQueued is a turn that waits for the chat's earlier turn.
	StatusQueued = "queued"
	// StatusRunning is a turn whose model call is under way.
	StatusRunning = "running"
	// StatusDone is a turn whose reply is whole.
	StatusDone = "done"
	// StatusStopped is a turn the person stopped. What had streamed is kept.
	StatusStopped = "stopped"
	// StatusFailed is a turn that could not finish. Its error says why.
	StatusFailed = "failed"
)

// The names of the events a turn streams.
const (
	EventTurn       = "turn"
	EventMessage    = "message"
	EventStart      = "start"
	EventThinking   = "thinking"
	EventText       = "text"
	EventToolCall   = "tool_call"
	EventToolResult = "tool_result"
	EventUsage      = "usage"
	EventTitle      = "title"
	EventError      = "error"
	EventEnd        = "end"
)

// The codes of a turn's error, beside the seam's own error kinds (auth,
// rate_limited, overloaded, too_long, invalid, unavailable, other).
const (
	// CodeNoModel says the person has no model a turn can use.
	CodeNoModel = "no_model"
	// CodeModelUnavailable says the chosen model cannot be opened: its
	// adapter is not in this build, its key is not in the vault, or its
	// entry is refused.
	CodeModelUnavailable = "model_unavailable"
	// CodeInterrupted says Keystone stopped while the turn was running.
	CodeInterrupted = "interrupted"
	// CodeStepLimit says the model asked for tools more times than a turn
	// allows.
	// Since the tool loop (P7-01) no turn fails with it: a turn at its step
	// limit ends as done, with a note (StopStepLimit). It stays for turns
	// an earlier build ended.
	CodeStepLimit = "step_limit"
	// CodeFailed says the turn failed inside Keystone. The reason is in
	// the server's log under the turn's request id.
	CodeFailed = "failed"
)

// Where a chat's model came from, in ModelChoice.Source.
const (
	SourceChat    = "chat"    // the chat's last turn used it
	SourceDefault = "default" // the chats.default_model setting names it
	SourceFirst   = "first"   // the first usable model in the registry
	SourceTest    = "test"    // the test tenant's recorded turns
	SourceNone    = "none"    // there is no model to use
)

// Chat is one chat, as the list and the chat screen show it.
type Chat struct {
	ID string `json:"id"`
	// Title is empty until the chat has one. The first reply gives it one.
	Title   string    `json:"title"`
	Starred bool      `json:"starred"`
	Created time.Time `json:"created"`
	// Active is when the last message was written.
	Active time.Time `json:"active"`
	// Busy says a turn is queued or running in the chat.
	Busy bool `json:"busy"`
	// Summary is the summary of the chat's recap, in the list of chats.
	// Absent until the chat has one (P5-05, recap.go).
	Summary string `json:"summary,omitempty"`
	// Imported is the mark of a chat that was brought in from another app:
	// where from, when, and what that app recorded in it. Absent on a chat
	// made here (P12-03, imported.go).
	Imported *imported.Mark `json:"imported,omitempty"`
}

// Message is one message of a chat. The first seven fields are the chat
// record's. The rest are what the turn that wrote the message recorded,
// and are absent on a message no turn wrote.
type Message struct {
	// ID is a number never used twice. A later message has a larger one.
	ID     int64  `json:"id"`
	ChatID string `json:"chat_id"`
	// ParentID is the message this one answers or follows. Absent on a
	// first message.
	ParentID int64 `json:"parent_id,omitempty"`
	// Role is user or assistant. Blocks are the content, in the model
	// seam's format: text, thinking, tool_call, tool_result, image, file.
	Role    seam.Role    `json:"role"`
	Blocks  []seam.Block `json:"blocks"`
	Created time.Time    `json:"created"`
	// Siblings are the ids of every message with the same parent, this one
	// included, oldest first. More than one means there are branches here.
	Siblings []int64 `json:"siblings"`

	// TurnID is the turn that wrote the message.
	TurnID string `json:"turn_id,omitempty"`
	// Model is the registry name of the model that wrote an assistant
	// message.
	Model string `json:"model,omitempty"`
	// StopReason is why an assistant message ends where it does: end,
	// tool_call, max_tokens or refused from the model, stopped when the
	// person stopped it, failed when the call broke off.
	// step_limit is the note a turn writes when it reaches its step limit.
	StopReason string `json:"stop_reason,omitempty"`
	// Usage is the token count of the model call that wrote the message.
	Usage *seam.Usage `json:"usage,omitempty"`
	// CostMicros is what that call cost, in millionths of a US dollar.
	CostMicros int64 `json:"cost_micros,omitempty"`
}

// ModelInfo is a model as a person sees it. It never holds an address or
// the name of a key.
type ModelInfo struct {
	// Name is the registry name, the one a turn is sent with.
	Name string `json:"name"`
	// Label is what a model picker shows.
	Label string `json:"label"`
	// Class is local, hosted or trusted.
	Class         string   `json:"class"`
	Capabilities  []string `json:"capabilities"`
	ContextWindow int      `json:"context_window"`
	OutputLimit   int      `json:"output_limit"`
	// Test says the model is the fake provider playing recorded turns.
	Test bool `json:"test"`
}

// ModelChoice says which model a chat's next turn will use, and which
// models the person may pick in its place.
type ModelChoice struct {
	// Model is the model the next turn uses when the turn names none. It
	// is absent when there is no model to use.
	Model *ModelInfo `json:"model,omitempty"`
	// Source says where Model came from: chat, default, first, test or none.
	Source string `json:"source"`
	// Reason says, for a person, why there is no model. Empty otherwise.
	Reason string `json:"reason,omitempty"`
	// Models are the models a turn in this chat may name.
	Models []ModelInfo `json:"models"`
}

// TurnError says why a turn failed.
type TurnError struct {
	// Code is a short fixed name: no_model, model_unavailable, interrupted,
	// step_limit, failed, or one of the seam's kinds (auth, rate_limited,
	// overloaded, too_long, invalid, unavailable, other).
	Code string `json:"code"`
	// Message is for a person.
	Message string `json:"message"`
	// Retryable says the same turn may work if it is sent again.
	Retryable bool `json:"retryable"`
}

// Turn is one turn: a person's message and the reply to it.
type Turn struct {
	ID     string `json:"id"`
	ChatID string `json:"chat_id"`
	// Status is queued, running, done, stopped or failed.
	Status string `json:"status"`
	// Model is the registry name of the model that answers. It is set when
	// the turn leaves the queue.
	Model string `json:"model,omitempty"`
	// UserMessageID is the person's message, once it is in the record.
	UserMessageID int64 `json:"user_message_id,omitempty"`
	// ReplyMessageID is the last assistant message the turn wrote.
	ReplyMessageID int64 `json:"reply_message_id,omitempty"`
	// StopReason is the model's last stop reason: end, tool_call,
	// max_tokens or refused.
	// step_limit is a turn that reached its step limit and ended as done.
	StopReason string `json:"stop_reason,omitempty"`
	// Error is set on a failed turn.
	Error *TurnError `json:"error,omitempty"`
	// Usage is the sum over the turn's model calls, and CostMicros is what
	// they cost, in millionths of a US dollar.
	Usage      seam.Usage `json:"usage"`
	CostMicros int64      `json:"cost_micros"`
	Created    time.Time  `json:"created"`
	Started    *time.Time `json:"started,omitempty"`
	Ended      *time.Time `json:"ended,omitempty"`
	// LastEventID is the id of the newest event the turn has streamed.
	LastEventID int64 `json:"last_event_id"`
}

// ListReply answers GET chats.
type ListReply struct {
	// Chats are in order of their last message, newest first.
	Chats []Chat `json:"chats"`
	// More says there are chats after these. Ask again with a larger offset.
	More bool `json:"more"`
}

// ChatReply is one chat with its current branch.
type ChatReply struct {
	Chat Chat `json:"chat"`
	// Messages are the current branch, first message first.
	Messages []Message `json:"messages"`
	// Model is the model the chat's next turn will use.
	Model ModelChoice `json:"model"`
	// Turns are the chat's turns that are queued or running, oldest first.
	// A screen that opens the chat reads their events to join the stream.
	Turns []Turn `json:"turns"`
}

// PatchBody changes a chat. A field that is absent is left as it is.
type PatchBody struct {
	Title   *string `json:"title,omitempty"`
	Starred *bool   `json:"starred,omitempty"`
}

// DeletedReply answers DELETE chats/{id}.
type DeletedReply struct {
	Deleted bool `json:"deleted"`
}

// SelectBody names the message to put on the current branch.
type SelectBody struct {
	MessageID int64 `json:"message_id"`
}

// SearchReply answers GET chats/search.
type SearchReply struct {
	Query string `json:"query"`
	// Hits are title matches first, then message matches, best first.
	Hits []chat.Hit `json:"hits"`
}

// SendBody is a person's message, and so the start of a turn.
type SendBody struct {
	// Text is the message. Give Text, or Blocks, or neither with RetryOf.
	Text string `json:"text,omitempty"`
	// Blocks is the message as content blocks, for a message that is more
	// than text: text, image and file blocks.
	Blocks []seam.Block `json:"blocks,omitempty"`
	// EditOf is the id of an earlier message of the person's that this one
	// replaces. The new message becomes a branch beside it.
	EditOf int64 `json:"edit_of,omitempty"`
	// RetryOf is the id of an assistant message to make again. No new
	// message is written: the new reply becomes a branch beside the old.
	RetryOf int64 `json:"retry_of,omitempty"`
	// Model is the registry name of the model to answer. Absent means the
	// chat's own, as GET chats/{id}/model gives it.
	Model string `json:"model,omitempty"`
}

// StartEvent is the data of a start event: a model call opens.
type StartEvent struct {
	// Step counts the turn's model calls from one. A turn has more than
	// one when the model asks for a tool.
	Step  int    `json:"step"`
	Model string `json:"model"`
}

// DeltaEvent is the data of a text or thinking event: more of one block.
type DeltaEvent struct {
	Step int `json:"step"`
	// Index is the block's place in the step's reply, from zero. A new
	// index is a new block.
	Index int    `json:"index"`
	Text  string `json:"text"`
}

// ToolCallEvent is the data of a tool_call event: the model asks for a
// tool. It is sent once, when the call is whole.
type ToolCallEvent struct {
	Step  int    `json:"step"`
	Index int    `json:"index"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	// Input is the call's arguments, a JSON object.
	Input json.RawMessage `json:"input"`
}

// ToolResultEvent is the data of a tool_result event: what a tool gave back.
type ToolResultEvent struct {
	// ID is the tool call's id.
	ID      string       `json:"id"`
	IsError bool         `json:"is_error"`
	Content []seam.Block `json:"content"`
}

// UsageEvent is the data of a usage event: one model call's count.
type UsageEvent struct {
	Step       int        `json:"step"`
	Model      string     `json:"model"`
	StopReason string     `json:"stop_reason"`
	Usage      seam.Usage `json:"usage"`
	CostMicros int64      `json:"cost_micros"`
}

// TitleEvent is the data of a title event: the chat was given a title.
type TitleEvent struct {
	ChatID string `json:"chat_id"`
	Title  string `json:"title"`
}

// streamEvents is what a turn's stream sends, for the OpenAPI document.
var streamEvents = []api.EventDoc{
	{Name: EventTurn, Description: "The turn's state. It is the first event, and is sent again when the turn leaves the queue and starts.", Data: Turn{}},
	{Name: EventMessage, Description: "A message was written to the chat record: the person's own at the start, each assistant message when it is whole, each set of tool results. It replaces whatever the screen built from the deltas.", Data: Message{}},
	{Name: EventStart, Description: "A model call opens.", Data: StartEvent{}},
	{Name: EventThinking, Description: "More of a thinking block.", Data: DeltaEvent{}},
	{Name: EventText, Description: "More of a text block.", Data: DeltaEvent{}},
	{Name: EventToolCall, Description: "The model asks for a tool. Sent once, when the call is whole.", Data: ToolCallEvent{}},
	{Name: EventToolResult, Description: "What a tool gave back.", Data: ToolResultEvent{}},
	{Name: EventUsage, Description: "The token count and cost of one model call, when it ends.", Data: UsageEvent{}},
	{Name: EventTitle, Description: "The chat was given its automatic title.", Data: TitleEvent{}},
	{Name: EventError, Description: "The turn failed. An end event with the status failed follows.", Data: TurnError{}},
	{Name: EventEnd, Description: "Always the last event: the turn as it ended, with the status done, stopped or failed. The stream closes after it.", Data: Turn{}},
}

// Routes returns the chat and turn routes, served by r.
func Routes(r *Runner) []api.Route {
	const chats = api.Prefix + "chats"
	const one = chats + "/{id}"
	const turns = one + "/turns"
	return []api.Route{
		{
			Method: "GET", Path: chats, Handle: r.list,
			Summary: "The person's chats, the one with the newest message first.",
			Query: []api.Param{
				{Name: "starred", Description: "true keeps starred chats only."},
				{Name: "limit", Description: "The most chats to return. The server.page_limit setting is the most and the default."},
				{Name: "offset", Description: "How many chats to skip from the top of the list."},
			},
			Reply: ListReply{},
		},
		{
			Method: "POST", Path: chats, Handle: r.create,
			Summary: "Make a chat with no title and no messages. It takes no body.",
			Reply:   ChatReply{},
		},
		{
			Method: "GET", Path: chats + "/search", Handle: r.search,
			Summary: "Search the person's chats by the words of their titles and messages, on every branch.",
			Query:   []api.Param{{Name: "q", Description: "The words to look for. Every word must be in the same title or the same message."}},
			Reply:   SearchReply{},
		},
		{
			Method: "GET", Path: one, Handle: r.read,
			Summary: "One chat: its current branch of messages, the model its next turn will use, and its turns that have not ended.",
			Reply:   ChatReply{},
		},
		{
			Method: "PATCH", Path: one, Handle: r.patch,
			Summary: "Rename a chat, star it or clear its star.",
			Body:    PatchBody{}, Reply: Chat{},
		},
		{
			Method: "DELETE", Path: one, Handle: r.remove,
			Summary: "Delete a chat, with its messages, its turns and its files. A turn that is running in it is stopped first.",
			Reply:   DeletedReply{},
		},
		{
			Method: "POST", Path: one + "/select", Handle: r.selectBranch,
			Summary: "Put a message on the chat's current branch: step to a sibling at a branch point, or open a search result.",
			Body:    SelectBody{}, Reply: ChatReply{},
		},
		{
			Method: "GET", Path: one + "/model", Handle: r.model,
			Summary: "Which model the chat's next turn will use, and the models a turn may name in its place.",
			Reply:   ModelChoice{},
		},
		{
			Method: "GET", Path: one + "/pins", Handle: r.pins,
			Summary: "The chat's pinned messages. A pinned message is in every call of its branch, even when the turns around it are left out or summarised.",
			Reply:   PinsReply{},
		},
		{
			Method: "PUT", Path: one + "/pins/{message}", Handle: r.pin,
			Summary: "Pin a message of the chat, on any branch. It takes no body. Pinning a message twice changes nothing.",
			Reply:   PinsReply{},
		},
		{
			Method: "DELETE", Path: one + "/pins/{message}", Handle: r.unpin,
			Summary: "Take the pin off a message of the chat. Taking off a pin that is not there changes nothing.",
			Reply:   PinsReply{},
		},
		{
			Method: "POST", Path: turns, Handle: r.send,
			Summary: "Send a message and stream the reply. The turn's id is in the X-Turn-Id header and in the first event. A turn sent while another runs in the chat waits its turn.",
			Body:    SendBody{}, Events: streamEvents,
		},
		{
			Method: "GET", Path: turns + "/{turn}", Handle: r.turn,
			Summary: "One turn's state.",
			Reply:   Turn{},
		},
		{
			Method: "GET", Path: turns + "/{turn}/events", Handle: r.events,
			Summary: "A turn's stream, from the start or from after the event named by Last-Event-ID. It is how a client joins a turn it did not send, and how it comes back after a dropped connection.",
			Query:   []api.Param{{Name: "after", Description: "The id of the last event the client has, for a client that cannot set Last-Event-ID. The header wins when both are sent."}},
			Events:  streamEvents,
		},
		{
			Method: "POST", Path: turns + "/{turn}/stop", Handle: r.stop,
			Summary: "Stop a turn. The model call is cancelled and what had streamed is kept. It takes no body. Stopping a turn that has ended changes nothing.",
			Reply:   Turn{},
		},
	}
}
