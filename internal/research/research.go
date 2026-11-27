// Package research is research mode (P8-04): a person asks a question from
// the composer, and Keystone works it into a long report from many sources,
// in the background, within a budget of its own. The plan's words: a long
// multi-source report built by sub-agents.
//
// A research task is not a second loop beside the turns. It is three steps,
// each built on what is there:
//
//   - The plan is one ordinary turn of a chat of the task's own, sent under
//     the task's own request id. Its reply splits the question into parts.
//   - Each part is a sub-agent (internal/subagents), in a chat of its own,
//     that searches the web and reads pages (internal/tools/web).
//   - The report is one more turn of the task's chat, written from what the
//     parts found and the sources their recorded results returned.
//
// So every model call and tool call is a turn's: streamed, kept in the chat
// record, recorded in the audit log, asked about under the approval policy,
// held by the sensitive rules and stopped the way a turn is. What this
// package keeps is the task's row, the numbering of the sources across the
// parts, the check on the report's citations (internal/research/cite), and
// the delivery: the report as a Markdown artifact (internal/artifacts) and
// a note with its summary in the chat the task was started from.
//
// The task's budget is one sum for its own turns and its parts, each an
// unattended run (internal/unattended), so the gate, the fallback at the
// cap and the switch that pauses every agent hold for it.
//
// docs/architecture/research-mode.md is the contract.
package research

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/research/cite"
	"github.com/jonathanmkosar/keystone/internal/subagents"
	"github.com/jonathanmkosar/keystone/settings"
)

// The statuses of a research task.
const (
	// StatusQueued is a task that has not sent its plan.
	StatusQueued = "queued"
	// StatusRunning is a task at work on its plan, its parts or its report.
	StatusRunning = "running"
	// StatusPaused is a task the switch that pauses every agent holds. It
	// has not ended, and goes on when the switch goes off.
	StatusPaused = "paused"
	// StatusDone is a task whose report is written.
	StatusDone = "done"
	// StatusStopped is a task the person stopped.
	StatusStopped = "stopped"
	// StatusFailed is a task that ended with no report. Error says why.
	StatusFailed = "failed"
)

// The stages of a task that has not ended.
const (
	StagePlan  = "plan"  // the question is being split into parts
	StageParts = "parts" // the parts are searching and reading
	StageWrite = "write" // the report is being written
)

// The codes of a task's error, beside the code of the turn its plan ended
// with (spend_cap, no_model, model_unavailable, sensitive and the seam's
// kinds), which is passed on as it is.
const (
	// CodeNothingFound says no part found anything to write a report from.
	CodeNothingFound = "nothing_found"
	// CodeRoundLimit says its own chat took agents.research_max_rounds turns
	// and the report was not written.
	CodeRoundLimit = "round_limit"
	// CodeChatDeleted says its own chat was deleted while it worked.
	CodeChatDeleted = "chat_deleted"
	// CodeFailed says it failed inside Keystone. The reason is in the
	// server's log under its request id.
	CodeFailed = "failed"
)

// ResearchError says why a task ended with no report.
type ResearchError struct {
	Code string `json:"code"`
	// Message is for a person.
	Message string `json:"message"`
}

// PlanPart is one part of a task's plan.
type PlanPart struct {
	// Title names the part in a few words, and Task is what its sub-agent is
	// to find out.
	Title string `json:"title"`
	Task  string `json:"task"`
}

// Research is one research task, as the routes show it.
type Research struct {
	ID string `json:"id"`
	// ChatID is the chat it was started from: its report and its note go
	// there. WorkChatID is the chat of its own its plan and its report are
	// written in.
	ChatID     string `json:"chat_id"`
	WorkChatID string `json:"work_chat_id"`
	// Title names it in a few words, and Question is what was asked.
	Title    string `json:"title"`
	Question string `json:"question"`
	// Status is queued, running, paused, done, stopped or failed.
	Status string `json:"status"`
	// Stage is plan, parts or write while it has not ended.
	Stage string `json:"stage,omitempty"`
	// Progress says in a few words where it is.
	Progress string `json:"progress"`
	// Waiting says a tool call of one of its turns waits for the person's
	// approval, in its own chat or a part's.
	Waiting bool `json:"waiting"`
	// BudgetMicros is the most the whole task may spend on models that are
	// not local, its parts included, and SpentMicros what it has, in
	// millionths of a US dollar.
	BudgetMicros int64 `json:"budget_micros"`
	SpentMicros  int64 `json:"spent_micros"`
	// AtBudget says the task reached its budget: a part or the report step
	// was refused a call, and the report is what there was.
	AtBudget bool `json:"at_budget"`
	// Plan is the parts the question was split into, once the plan is read.
	Plan []PlanPart `json:"plan"`
	// Parts are the sub-agents, one for each part of the plan, by place.
	Parts []subagents.Subagent `json:"parts"`
	// Sources are the sources the parts have read so far, numbered across
	// all of them; once the report is written, the ones it cites, numbered
	// as it cites them.
	Sources []cite.Source `json:"sources"`
	// SourcesRead counts every source the parts read, cited or not.
	SourcesRead int `json:"sources_read"`
	// Dropped counts the citations taken out of the report because they
	// named no source a part read.
	Dropped int `json:"dropped_citations"`
	// Summary is what the note says of the report, once it is written.
	Summary string `json:"summary,omitempty"`
	// Notice says what else the person is told: a part that did not finish,
	// a model that stood in at the budget.
	Notice string `json:"notice,omitempty"`
	// ArtifactID is the report, a Markdown artifact, and ArtifactChatID the
	// chat it is in: ChatID, or WorkChatID when the task was sent sensitive
	// content and the chat it was started from is not held.
	ArtifactID     string `json:"artifact_id,omitempty"`
	ArtifactChatID string `json:"artifact_chat_id,omitempty"`
	// Error is set on a task that failed.
	Error *ResearchError `json:"error,omitempty"`
	// NoteMessageID is the note in ChatID, once it is written.
	NoteMessageID int64      `json:"note_message_id,omitempty"`
	Created       time.Time  `json:"created"`
	Started       *time.Time `json:"started,omitempty"`
	Ended         *time.Time `json:"ended,omitempty"`
}

// ListReply answers GET chats/{id}/research.
type ListReply struct {
	// Research are the tasks started from the chat, or working in it, oldest
	// first.
	Research []Research `json:"research"`
}

// StartBody is the body of POST chats/{id}/research.
type StartBody struct {
	// Question is what to research.
	Question string `json:"question"`
	// Title names the task in a few words. Absent means the first words of
	// the question.
	Title string `json:"title,omitempty"`
	// BudgetUSD is this task's own budget, in US dollars. Absent means the
	// person's agents.research_budget_usd.
	BudgetUSD *float64 `json:"budget_usd,omitempty"`
}

// Terms answers GET research: what a task started now would run with, so a
// person sees the budget before they start one.
type Terms struct {
	// BudgetMicros is the person's budget for one task, in millionths of a
	// US dollar.
	BudgetMicros int64 `json:"budget_micros"`
	// Parts is the most parts a question is split into, SourcesPerPart how
	// many sources each part is asked to read, and ReportWords about how
	// long the report is asked to be.
	Parts          int `json:"parts"`
	SourcesPerPart int `json:"sources_per_part"`
	ReportWords    int `json:"report_words"`
	// Open is how many of the person's tasks have not ended, of OpenMax at
	// most.
	Open    int `json:"open"`
	OpenMax int `json:"open_max"`
	// Paused says the person's switch that pauses every agent is on: a task
	// started now waits until it goes off.
	Paused bool `json:"paused"`
}

// KeyBudget is the person's setting that holds a research task's budget.
const KeyBudget = "agents.research_budget_usd"

// Config is the values of the install's settings that research runs with,
// in settings/agents.json. agents.research_budget_usd is a person's own,
// and is read for each task when it starts.
type Config struct {
	ReportSharePercent int    // agents.research_report_share_percent
	Parts              int    // agents.research_parts
	SourcesPerPart     int    // agents.research_sources_per_part
	ReportWords        int    // agents.research_report_words
	SummaryChars       int    // agents.research_summary_max_chars
	OpenMax            int    // agents.research_open_max
	MaxRounds          int    // agents.research_max_rounds
	PlanPrompt         string // agents.research_plan_prompt
	PartBrief          string // agents.research_part_brief
	ReportPrompt       string // agents.research_report_prompt
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
			*p, err = strconv.Atoi(strings.TrimSpace(v))
		case *string:
			*p = strings.TrimSpace(v)
		}
		if err != nil {
			err = fmt.Errorf("%s: %w", key, err)
		}
	}
	read("agents.research_report_share_percent", &c.ReportSharePercent)
	read("agents.research_parts", &c.Parts)
	read("agents.research_sources_per_part", &c.SourcesPerPart)
	read("agents.research_report_words", &c.ReportWords)
	read("agents.research_summary_max_chars", &c.SummaryChars)
	read("agents.research_open_max", &c.OpenMax)
	read("agents.research_max_rounds", &c.MaxRounds)
	read("agents.research_plan_prompt", &c.PlanPrompt)
	read("agents.research_part_brief", &c.PartBrief)
	read("agents.research_report_prompt", &c.ReportPrompt)
	if err != nil {
		return Config{}, err
	}
	switch {
	case c.ReportSharePercent < 1 || c.ReportSharePercent > 99:
		return Config{}, errors.New("agents.research_report_share_percent must be from 1 to 99: the plan and the report need a share of the budget, and so do the parts")
	case c.Parts < 1 || c.Parts > 12:
		return Config{}, errors.New("agents.research_parts must be from 1 to 12")
	case c.SourcesPerPart < 1 || c.ReportWords < 1 || c.SummaryChars < 1:
		return Config{}, errors.New("agents.research_sources_per_part, agents.research_report_words and agents.research_summary_max_chars must each be 1 or more")
	case c.OpenMax < 1:
		return Config{}, errors.New("agents.research_open_max must be 1 or more")
	case c.MaxRounds < 2:
		return Config{}, errors.New("agents.research_max_rounds must be 2 or more: a task takes one turn for its plan and one for its report")
	case c.PlanPrompt == "" || c.PartBrief == "" || c.ReportPrompt == "":
		return Config{}, errors.New("agents.research_plan_prompt, agents.research_part_brief and agents.research_report_prompt cannot be empty: they tell each step what it is")
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
