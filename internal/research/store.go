package research

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/research/cite"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// errNoResearch says the task is not in this tenant's record, or not in
// the chat it was asked for under.
var errNoResearch = errors.New("no such research task")

// Times are stored as the chat record and the turns store them: text of
// one fixed width, in UTC, so they sort as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

func stamp() string { return time.Now().UTC().Format(timeLayout) }

// row is a task as the runner needs it: what the API shows, and what it
// runs with.
type row struct {
	Research
	requestID      string
	startedRequest string
	actor          string
	model          string
	callID         string
	planTurnID     string
	writeTurnID    string
	noteTurnID     string
	report         string
	stopAsked      bool
	paused         bool
	maxParts       int
	sourcesPerPart int
	reportWords    int
	// stored is the status in the database, which Status shows as paused
	// while the switch holds the task.
	stored string
}

const columns = `id, chat_id, work_chat_id, title, question, status, stage, paused, stop_requested, request_id,
	started_request, actor, model, budget_micros, spent_micros, max_parts, sources_per_part, report_words, plan,
	plan_turn_id, call_id, write_turn_id, sources, sources_read, dropped, report, summary, at_budget, notice, artifact_id,
	artifact_chat_id, error, progress, note_turn_id, note_message_id, created_at, started_at, ended_at`

func scan(sc interface{ Scan(...any) error }) (row, error) {
	var j row
	var plan, sources, failure, created, started, finished string
	err := sc.Scan(&j.ID, &j.ChatID, &j.WorkChatID, &j.Title, &j.Question, &j.Status, &j.Stage, &j.paused, &j.stopAsked, &j.requestID,
		&j.startedRequest, &j.actor, &j.model, &j.BudgetMicros, &j.SpentMicros, &j.maxParts, &j.sourcesPerPart, &j.reportWords, &plan,
		&j.planTurnID, &j.callID, &j.writeTurnID, &sources, &j.SourcesRead, &j.Dropped, &j.report, &j.Summary, &j.AtBudget, &j.Notice, &j.ArtifactID,
		&j.ArtifactChatID, &failure, &j.Progress, &j.noteTurnID, &j.NoteMessageID, &created, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, errNoResearch
	}
	if err != nil {
		return row{}, err
	}
	j.stored = j.Status
	if j.paused && !ended(j.Status) {
		j.Status = StatusPaused
	}
	j.Plan, j.Sources, j.Parts = []PlanPart{}, []cite.Source{}, nil
	if plan != "" {
		if err := json.Unmarshal([]byte(plan), &j.Plan); err != nil {
			return row{}, fmt.Errorf("research task %s: its stored plan does not read: %w", j.ID, err)
		}
	}
	if sources != "" {
		if err := json.Unmarshal([]byte(sources), &j.Sources); err != nil {
			return row{}, fmt.Errorf("research task %s: its stored sources do not read: %w", j.ID, err)
		}
	}
	if failure != "" {
		j.Error = &ResearchError{}
		if err := json.Unmarshal([]byte(failure), j.Error); err != nil {
			return row{}, fmt.Errorf("research task %s: its stored error does not read: %w", j.ID, err)
		}
	}
	if j.Created, err = time.Parse(timeLayout, created); err != nil {
		return row{}, fmt.Errorf("research task %s: %w", j.ID, err)
	}
	for _, x := range []struct {
		text string
		into **time.Time
	}{{started, &j.Started}, {finished, &j.Ended}} {
		if x.text == "" {
			continue
		}
		at, err := time.Parse(timeLayout, x.text)
		if err != nil {
			return row{}, fmt.Errorf("research task %s: %w", j.ID, err)
		}
		*x.into = &at
	}
	return j, nil
}

// ended reports whether a stored status is one a task ends with.
func ended(status string) bool {
	return status == StatusDone || status == StatusStopped || status == StatusFailed
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// get returns one task by its id.
func get(ctx context.Context, q queryer, id string) (row, error) {
	return scan(q.QueryRowContext(ctx, "SELECT "+columns+" FROM research WHERE id = ?", id))
}

// getInChat returns one task of a chat: one started from it, or working in
// it.
func getInChat(ctx context.Context, s *tenant.Store, chatID, id string) (row, error) {
	return scan(s.DB().QueryRowContext(ctx, "SELECT "+columns+" FROM research WHERE id = ? AND (chat_id = ? OR work_chat_id = ?)", id, chatID, chatID))
}

// list returns the tasks that match where, oldest first.
func list(ctx context.Context, q queryer, where string, args ...any) ([]row, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+columns+" FROM research WHERE "+where+" ORDER BY seq", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		j, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// isWork reports whether a chat is a research task's own. A chat whose mark
// cannot be read counts as one.
func isWork(ctx context.Context, s *tenant.Store, chatID string) bool {
	var yes bool
	if err := s.DB().QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM research WHERE work_chat_id = ?)", chatID).Scan(&yes); err != nil {
		return true
	}
	return yes
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// inTx runs f in one transaction, and rolls back if f fails.
func inTx(ctx context.Context, s *tenant.Store, f func(tx *sql.Tx) error) error {
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// change writes to a task's row. A task that is gone, with the chat it was
// started from, is errNoResearch.
func change(ctx context.Context, s *tenant.Store, id, set string, args ...any) error {
	res, err := s.DB().ExecContext(ctx, "UPDATE research SET "+set+" WHERE id = ?", append(args, id)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoResearch
	}
	return nil
}
