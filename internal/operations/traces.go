package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// The kinds of trace.
const (
	// TraceChat is a turn a person sent in a chat, with every call made
	// under its request id: the reply, its title, a summary, the memory
	// pass.
	TraceChat = "chat"
	// TraceTask is a background task: every round it took.
	TraceTask = "task"
	// TraceSchedule is the run of a scheduled task.
	TraceSchedule = "schedule"
	// TraceSubagent is a sub-agent's run (P8-02).
	TraceSubagent = "subagent"
)

// ToolStep is one tool call of a trace, from the audit log.
type ToolStep struct {
	Name string `json:"name"`
	// Outcome is ok, refused or error.
	Outcome    string `json:"outcome"`
	DurationMS int64  `json:"duration_ms"`
}

// Trace is the work of one request id: a turn and the calls made for it,
// or every round of a background task or a scheduled run. Its tokens and
// cost are its usage records', so they equal what the turn inspector and
// the usage report show for it.
type Trace struct {
	RequestID string `json:"request_id"`
	// Kind is chat, task, schedule or subagent, and Title the chat's title
	// or the run's.
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// ChatID and TurnID are where the turn inspector opens: the chat and
	// its newest turn of the trace (a run's own chat and its round). Empty
	// when the chat is gone.
	ChatID string `json:"chat_id,omitempty"`
	TurnID string `json:"turn_id,omitempty"`
	// RunID is the task, for a run.
	RunID string `json:"run_id,omitempty"`
	// Turns is how many turns were sent under it.
	Turns int `json:"turns"`
	// Status is the newest turn's (queued, running, done, stopped, failed),
	// or the task's for a run (and paused), and Waiting says a call waits
	// for the person's approval.
	Status  string `json:"status"`
	Waiting bool   `json:"waiting,omitempty"`
	// Error is why it failed, for a person, passed through audit.Scrub.
	Error string `json:"error,omitempty"`
	// Models are the models that took its calls, in the order they first
	// did.
	Models []string `json:"models"`
	// Calls are its model calls, with the ones that failed and the ones
	// Keystone did not send.
	Calls    int `json:"calls"`
	Errors   int `json:"errors"`
	Refusals int `json:"refusals"`
	// Tools are its tool calls, in order.
	Tools []ToolStep `json:"tools"`
	// Usage is its tokens and cost, summed from its usage records.
	Usage usage.Total `json:"usage"`
	// Started is when its first turn was made, and Ended when its last
	// ended, while none is open.
	Started time.Time  `json:"started"`
	Ended   *time.Time `json:"ended,omitempty"`
}

// turnLine is one turn as a trace reads it.
type turnLine struct {
	id, chatID, requestID, status, failure, title string
	chatGone                                      bool
}

// readTraces returns the newest traces, up to cfg.TracesShown: the request
// ids of the person's newest turns, in the order they were last sent.
func readTraces(ctx context.Context, s *tenant.Store, cfg Settings) ([]Trace, error) {
	db := s.DB()
	rows, err := db.QueryContext(ctx, `SELECT t.id, t.chat_id, t.request_id, t.status, t.error, c.title, c.id IS NULL
		FROM turns t LEFT JOIN chats c ON c.id = t.chat_id ORDER BY t.seq DESC LIMIT ?`, cfg.TracesShown*20)
	if err != nil {
		return nil, err
	}
	var newest []turnLine
	seen := map[string]bool{}
	for rows.Next() {
		var l turnLine
		var title sql.NullString
		if err := rows.Scan(&l.id, &l.chatID, &l.requestID, &l.status, &l.failure, &title, &l.chatGone); err != nil {
			rows.Close()
			return nil, err
		}
		l.title = title.String
		if seen[l.requestID] || len(newest) >= cfg.TracesShown {
			continue
		}
		seen[l.requestID] = true
		newest = append(newest, l)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := []Trace{}
	for _, l := range newest {
		tr, err := readTrace(ctx, s, l)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, nil
}

// readTrace reads everything one request id set off.
func readTrace(ctx context.Context, s *tenant.Store, l turnLine) (Trace, error) {
	db := s.DB()
	tr := Trace{RequestID: l.requestID, Kind: TraceChat, Title: l.title, Status: l.status, Models: []string{}, Tools: []ToolStep{}}
	if !l.chatGone {
		tr.ChatID, tr.TurnID = l.chatID, l.id
	}
	if tr.Title == "" {
		tr.Title = "Untitled chat"
	}
	tr.Error = turnFailure(l.failure)

	var first, last string
	var open int
	if err := db.QueryRowContext(ctx, `SELECT count(*), coalesce(min(created_at), ''), coalesce(max(ended_at), ''),
		coalesce(sum(status IN ('queued', 'running')), 0) FROM turns WHERE request_id = ?`, l.requestID).Scan(&tr.Turns, &first, &last, &open); err != nil {
		return tr, err
	}
	if at, err := time.Parse(timeLayout, first); err == nil {
		tr.Started = at
	}
	if at, err := time.Parse(timeLayout, last); err == nil && open == 0 {
		tr.Ended = &at
	}

	// A run: its kind, title and status are the task's.
	run, isRun, err := unattended.ForRequest(ctx, db, l.requestID)
	if err != nil {
		return tr, err
	}
	if isRun {
		tr.Title, tr.RunID = run.Title, run.ID
		switch run.Kind {
		case unattended.KindSchedule:
			tr.Kind = TraceSchedule
		case unattended.KindSubagent:
			tr.Kind = TraceSubagent
		default:
			tr.Kind = TraceTask
		}
		if run.Kind != unattended.KindSubagent {
			task, err := jobs.Read(ctx, s, run.ID)
			switch {
			case err == nil:
				tr.Status, tr.Waiting = task.Status, task.Waiting
				tr.Error = ""
				if task.Error != nil {
					tr.Error = audit.Scrub(task.Error.Message)
				}
				// The inspector opens on the run's own chat and its round.
				if task.TurnID != "" {
					tr.ChatID, tr.TurnID = task.WorkChatID, task.TurnID
				}
			case errors.Is(err, jobs.ErrNoTask):
				// The task went with its chat; the trace stays.
			default:
				return tr, err
			}
		}
	}

	// Its model calls, tokens and cost, from its usage records.
	recs, err := usage.List(ctx, s, usage.Filter{RequestID: l.requestID})
	if err != nil {
		return tr, err
	}
	for _, r := range recs {
		tr.Calls++
		tr.Usage.Calls++
		tr.Usage.InputTokens += int64(r.InputTokens)
		tr.Usage.OutputTokens += int64(r.OutputTokens)
		tr.Usage.CacheReadTokens += int64(r.CacheReadTokens)
		tr.Usage.CacheWriteTokens += int64(r.CacheWriteTokens)
		tr.Usage.CostMicros += r.CostMicros
		switch r.Outcome {
		case usage.OutcomeError:
			tr.Errors++
		case usage.OutcomeRefused:
			tr.Refusals++
		}
		found := false
		for _, m := range tr.Models {
			found = found || m == r.Model
		}
		if !found {
			tr.Models = append(tr.Models, r.Model)
		}
	}

	// Its tool calls, from the audit log: the entries that ended a call.
	rows, err := db.QueryContext(ctx, `SELECT action, outcome, duration_ms FROM audit_log
		WHERE request_id = ? AND kind = 'tool' AND outcome <> 'started' ORDER BY id`, l.requestID)
	if err != nil {
		return tr, err
	}
	defer rows.Close()
	for rows.Next() {
		var t ToolStep
		if err := rows.Scan(&t.Name, &t.Outcome, &t.DurationMS); err != nil {
			return tr, err
		}
		tr.Tools = append(tr.Tools, t)
	}
	return tr, rows.Err()
}

// turnFailure is a failed turn's words, scrubbed, from its stored error.
func turnFailure(stored string) string {
	if stored == "" {
		return ""
	}
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(stored), &e) != nil {
		return ""
	}
	return audit.Scrub(e.Message)
}
