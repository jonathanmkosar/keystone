// Package subagents is sub-agents (P8-02): helpers a model starts from a
// turn with the tool run_subagents, each doing one part of the work at the
// same time as the others, whose results come back to that turn as the
// tool's result. The plan's words: sub-agents run in parallel with their
// own context, each shown as a lane in the chat.
//
// A sub-agent is not a second loop beside the turns. It works through one
// ordinary turn of a chat of its own, its work chat, as a background task
// does (internal/jobs): the turn's message is a brief from the settings,
// its task and what the model passed it, and nothing else of the parent
// chat. So every model call and tool call it makes is a turn's: streamed,
// kept in the chat record, recorded in the audit log under its own request
// id, asked about under the approval policy, held by the sensitive rules,
// and stopped the way a turn is. What this package keeps is each
// sub-agent's row, the tool that starts them, the supervisor that runs
// them a few at a time and gathers their results, and the routes the lanes
// read.
//
// Each sub-agent is an unattended run (internal/unattended), registered
// with its own budget in the transaction that writes its row, so the
// budget gate, the fallback or stop at the cap and the switch that pauses
// every agent hold for it. internal/subagents/carry is the rule for what
// passes between a chat and its sub-agents, for fixed rules 2 and 3.
//
// docs/architecture/sub-agents.md is the contract.
package subagents

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/settings"
)

// ToolName is the tool a model starts sub-agents with.
const ToolName = "run_subagents"

// The statuses of a sub-agent.
const (
	// StatusQueued is a sub-agent waiting for a free lane: its call runs at
	// most agents.subagents_at_once at a time.
	StatusQueued = "queued"
	// StatusRunning is a sub-agent whose turn is under way, or about to be.
	StatusRunning = "running"
	// StatusDone is a sub-agent that finished: its result is in Result.
	StatusDone = "done"
	// StatusStopped is a sub-agent the person stopped from its lane, or
	// whose parent turn was stopped or ran out of time.
	StatusStopped = "stopped"
	// StatusFailed is a sub-agent that ended without finishing. Error says
	// why.
	StatusFailed = "failed"
	// StatusPaused is a sub-agent the switch that pauses every agent ended
	// (P8-05). Unlike a background task, it does not go on when the switch
	// goes off: the turn that started it has its result already.
	StatusPaused = "paused"
)

// The codes of a sub-agent's error, beside the codes of the turn it ended
// with (spend_cap, no_model, model_unavailable, too_long, sensitive and the
// seam's kinds), which are passed on as they are.
const (
	// CodeStepLimit says it made agents.subagent_max_steps model calls and
	// had not finished.
	CodeStepLimit = "step_limit"
	// CodeChatDeleted says its chat was deleted while it worked.
	CodeChatDeleted = "chat_deleted"
	// CodeInterrupted says Keystone stopped while it worked.
	CodeInterrupted = "interrupted"
	// CodeTimeout says its call ran past agents.subagent_timeout.
	CodeTimeout = "timeout"
	// CodeFailed says it failed inside Keystone. The reason is in the
	// server's log under its request id.
	CodeFailed = "failed"
)

// SubagentError says why a sub-agent ended without finishing.
type SubagentError struct {
	Code string `json:"code"`
	// Message is for a person, and for the model that started it.
	Message string `json:"message"`
}

// Subagent is one sub-agent, as the routes show it.
type Subagent struct {
	ID string `json:"id"`
	// ChatID is the chat it was started from, TurnID the turn there, and
	// CallID the run_subagents call that started it: its lane is drawn
	// under that call's step. Position is its place among the call's
	// sub-agents, from 1.
	ChatID   string `json:"chat_id"`
	TurnID   string `json:"turn_id"`
	CallID   string `json:"call_id"`
	Position int    `json:"position"`
	// WorkChatID is the chat of its own its turn runs in, its whole
	// transcript, and WorkTurnID that turn, once it is sent.
	WorkChatID string `json:"work_chat_id"`
	WorkTurnID string `json:"work_turn_id,omitempty"`
	// Title names it in a few words, and Task is the words it was given.
	Title string `json:"title"`
	Task  string `json:"task"`
	// Model is the model its turn names. Empty means the parent chat's.
	Model string `json:"model,omitempty"`
	// Status is queued, running, done, stopped, failed or paused.
	Status string `json:"status"`
	// Progress says in a few words where it is.
	Progress string `json:"progress"`
	// Waiting says a tool call of its turn waits for the person's approval.
	// The lane draws the card, from its turn's approvals.
	Waiting bool `json:"waiting"`
	// Steps counts the model calls it has finished, of MaxSteps at most.
	Steps    int `json:"steps"`
	MaxSteps int `json:"max_steps"`
	// BudgetMicros is what it may spend on models that are not local, and
	// SpentMicros what it has, in millionths of a US dollar.
	BudgetMicros int64 `json:"budget_micros"`
	SpentMicros  int64 `json:"spent_micros"`
	// Result is the text of its last reply, once it has ended. A sub-agent
	// that reached its step limit has what it had written.
	Result string `json:"result,omitempty"`
	// Error is set on a sub-agent that failed.
	Error *SubagentError `json:"error,omitempty"`
	// Notice says which model wrote its work when the monthly cap or its
	// budget refused the one it was given (P8-05).
	Notice string `json:"notice,omitempty"`
	// Outside says its chat read outside content, and Sensitive that it was
	// held for sensitive content. Both are known once it has ended.
	Outside   bool `json:"outside"`
	Sensitive bool `json:"sensitive"`
	// Passed says its result went back to the turn that started it: false
	// for a sub-agent that was sent sensitive content, started from a chat
	// that is not held (internal/subagents/carry).
	Passed  bool       `json:"passed"`
	Created time.Time  `json:"created"`
	Started *time.Time `json:"started,omitempty"`
	Ended   *time.Time `json:"ended,omitempty"`
}

// SubagentsReply answers GET chats/{id}/subagents.
type SubagentsReply struct {
	// Subagents are the chat's, oldest first, by call and place.
	Subagents []Subagent `json:"subagents"`
}

// Config is the values of the install's settings that sub-agents run with,
// in settings/agents.json. agents.subagent_budget_usd is a person's own,
// and is read for each call when it starts.
type Config struct {
	AtOnce      int           // agents.subagents_at_once
	PerTurn     int           // agents.subagents_per_turn
	MaxSteps    int           // agents.subagent_max_steps
	ResultChars int           // agents.subagent_result_max_chars
	Timeout     time.Duration // agents.subagent_timeout
	Brief       string        // agents.subagent_brief
}

// ReadConfig builds the configuration from wherever setting values are
// kept. get returns the value for a setting's key.
func ReadConfig(get func(key string) (string, bool)) (Config, error) {
	var c Config
	var err error
	read := func(key string, into any) {
		if err != nil {
			return
		}
		v, ok := get(key)
		if !ok {
			err = fmt.Errorf("the %s setting is missing", key)
			return
		}
		switch p := into.(type) {
		case *int:
			*p, err = strconv.Atoi(v)
		case *time.Duration:
			*p, err = time.ParseDuration(v)
		case *string:
			*p = strings.TrimSpace(v)
		}
		if err != nil {
			err = fmt.Errorf("%s: %w", key, err)
		}
	}
	read("agents.subagents_at_once", &c.AtOnce)
	read("agents.subagents_per_turn", &c.PerTurn)
	read("agents.subagent_max_steps", &c.MaxSteps)
	read("agents.subagent_result_max_chars", &c.ResultChars)
	read("agents.subagent_timeout", &c.Timeout)
	read("agents.subagent_brief", &c.Brief)
	if err != nil {
		return Config{}, err
	}
	switch {
	case c.AtOnce < 1 || c.PerTurn < 1 || c.MaxSteps < 1:
		return Config{}, errors.New("agents.subagents_at_once, agents.subagents_per_turn and agents.subagent_max_steps must each be 1 or more")
	case c.ResultChars < 1:
		return Config{}, errors.New("agents.subagent_result_max_chars must be 1 or more")
	case c.Timeout <= 0:
		return Config{}, errors.New("agents.subagent_timeout must be longer than nothing")
	case c.Brief == "":
		return Config{}, errors.New("agents.subagent_brief cannot be empty: it tells each sub-agent what it is")
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

// Options is everything a Runner is made with.
type Options struct {
	// Install is the data folder. The supervisor of each call opens a store
	// of its own.
	Install *tenant.Install
	// Log is the server's own log. Nil means none.
	Log *api.Log
	// Config is the install's settings for sub-agents.
	Config Config
	// Turns is what turns run with: how often a look comes when nothing has
	// woken it, how long a stop waits, and the chat record's limits.
	Turns turn.Config
}

// Runner starts sub-agents and gathers their results. It keeps nothing a
// restart would lose: a sub-agent is a row and a turn. What it holds in
// memory is who is waiting to be told a call's sub-agents have news.
type Runner struct {
	o Options

	mu      sync.Mutex
	turns   *turn.Runner
	waiting map[string]chan struct{} // tenant/turn/call (the supervisor) and tenant/id (a stop)
}

// NewRunner returns a runner. It starts nothing until it is bound to the
// turn runner, with Bind.
func NewRunner(o Options) *Runner {
	return &Runner{o: o, waiting: map[string]chan struct{}{}}
}

// Bind gives the runner the turn runner its sub-agents' turns run on. The
// first one given stays. Each call's supervisor runs under the turn runner,
// so closing it stops the supervisor and waits for it.
func (r *Runner) Bind(t *turn.Runner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turns == nil {
		r.turns = t
	}
}

func (r *Runner) bound() *turn.Runner {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.turns
}

func (r *Runner) logf(format string, args ...any) { r.o.Log.Printf(format, args...) }

func (r *Runner) waitFor(key string) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := r.waiting[key]
	if ch == nil {
		ch = make(chan struct{})
		r.waiting[key] = ch
	}
	return ch
}

func (r *Runner) wake(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch := r.waiting[key]; ch != nil {
		close(ch)
		delete(r.waiting, key)
	}
}

// MaxSteps is the step limit of a chat's turns, as turn.Options.MaxSteps:
// agents.subagent_max_steps for a sub-agent's chat, and 0, the turn's own,
// for any other.
func (r *Runner) MaxSteps(ctx context.Context, s *tenant.Store, chatID string) int {
	if r == nil {
		return 0
	}
	if isWork(ctx, s, chatID) {
		return r.o.Config.MaxSteps
	}
	return 0
}

// SkipMemory returns the memory pass's check, as turn.Options.SkipMemory,
// with a sub-agent's chat added to inner's: its message is written by a
// model and not the person, so no fact is taken from it. A chat whose mark
// cannot be read is skipped.
func (r *Runner) SkipMemory(inner func(context.Context, *tenant.Store, string) bool) func(context.Context, *tenant.Store, string) bool {
	return func(ctx context.Context, s *tenant.Store, chatID string) bool {
		if inner != nil && inner(ctx, s, chatID) {
			return true
		}
		return isWork(ctx, s, chatID)
	}
}
