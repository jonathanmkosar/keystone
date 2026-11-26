package subagents

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// errNoSubagent says the sub-agent is not in this tenant's record, or not
// in the chat it was asked for under.
var errNoSubagent = errors.New("no such sub-agent")

// Who stopped a sub-agent, as stop_why keeps it.
const (
	byPerson  = "person"  // the person, from its lane
	byParent  = "parent"  // the turn that started it was stopped
	byTimeout = "timeout" // its call ran past agents.subagent_timeout
	byPause   = "paused"  // the switch that pauses every agent
)

// Times are stored as the chat record and the turns store them: text of
// one fixed width, in UTC, so they sort as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

func stamp() string { return time.Now().UTC().Format(timeLayout) }

// row is a sub-agent as the runner needs it: what the API shows, and what
// it runs with.
type row struct {
	Subagent
	context   string
	requestID string
	actor     string
	stopWhy   string
}

const columns = `id, chat_id, turn_id, call_id, position, work_chat_id, work_turn_id, title, task, context, request_id,
	actor, model, budget_micros, max_steps, status, steps, spent_micros, result, error, notice, outside, sensitive,
	passed, stop_why, created_at, started_at, ended_at`

func scan(sc interface{ Scan(...any) error }) (row, error) {
	var a row
	var failure, created, started, ended string
	err := sc.Scan(&a.ID, &a.ChatID, &a.TurnID, &a.CallID, &a.Position, &a.WorkChatID, &a.WorkTurnID, &a.Title, &a.Task, &a.context, &a.requestID,
		&a.actor, &a.Model, &a.BudgetMicros, &a.MaxSteps, &a.Status, &a.Steps, &a.SpentMicros, &a.Result, &failure, &a.Notice, &a.Outside, &a.Sensitive,
		&a.Passed, &a.stopWhy, &created, &started, &ended)
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, errNoSubagent
	}
	if err != nil {
		return row{}, err
	}
	if failure != "" {
		a.Error = &SubagentError{}
		if err := json.Unmarshal([]byte(failure), a.Error); err != nil {
			return row{}, fmt.Errorf("sub-agent %s: its stored error does not read: %w", a.ID, err)
		}
	}
	if a.Created, err = time.Parse(timeLayout, created); err != nil {
		return row{}, fmt.Errorf("sub-agent %s: %w", a.ID, err)
	}
	for _, x := range []struct {
		text string
		into **time.Time
	}{{started, &a.Started}, {ended, &a.Ended}} {
		if x.text == "" {
			continue
		}
		at, err := time.Parse(timeLayout, x.text)
		if err != nil {
			return row{}, fmt.Errorf("sub-agent %s: %w", a.ID, err)
		}
		*x.into = &at
	}
	return a, nil
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// get returns one sub-agent by its id.
func get(ctx context.Context, q queryer, id string) (row, error) {
	return scan(q.QueryRowContext(ctx, "SELECT "+columns+" FROM subagents WHERE id = ?", id))
}

// getInChat returns one sub-agent of a chat: one started from it, or
// working in it.
func getInChat(ctx context.Context, s *tenant.Store, chatID, id string) (row, error) {
	return scan(s.DB().QueryRowContext(ctx, "SELECT "+columns+" FROM subagents WHERE id = ? AND (chat_id = ? OR work_chat_id = ?)", id, chatID, chatID))
}

// list returns the sub-agents that match where, by call and place.
func list(ctx context.Context, q queryer, where string, args ...any) ([]row, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+columns+" FROM subagents WHERE "+where+" ORDER BY seq", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// isWork reports whether a chat is a sub-agent's own. A chat whose mark
// cannot be read counts as one, so it is treated with a sub-agent's care.
func isWork(ctx context.Context, s *tenant.Store, chatID string) bool {
	var yes bool
	if err := s.DB().QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM subagents WHERE work_chat_id = ?)", chatID).Scan(&yes); err != nil {
		return true
	}
	return yes
}

func ended(status string) bool {
	return status != StatusQueued && status != StatusRunning
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

// change writes to a sub-agent's row. A sub-agent that is gone, with the
// chat it was started from, is errNoSubagent.
func change(ctx context.Context, s *tenant.Store, id, set string, args ...any) error {
	res, err := s.DB().ExecContext(ctx, "UPDATE subagents SET "+set+" WHERE id = ?", append(args, id)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSubagent
	}
	return nil
}
