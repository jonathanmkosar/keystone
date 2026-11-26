package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Status is where one memory pass stands.
type Status string

const (
	// StatusQueued is a pass that waits for the chat's worker.
	StatusQueued Status = "queued"
	// StatusRunning is a pass the worker has taken.
	StatusRunning Status = "running"
	// StatusDone is a pass that read the exchange. Its facts, when it found
	// any, are in the store, in the source it names.
	StatusDone Status = "done"
	// StatusSkipped is a pass that was not made, or whose facts were kept
	// nowhere, and its reason says why: switched off, incognito, no store
	// that takes sources, no sensitive space, the reserve or the cap.
	StatusSkipped Status = "skipped"
	// StatusFailed is a pass that broke: the model call failed, the answer
	// did not read, or the store refused the source. Its reason says how.
	StatusFailed Status = "failed"
	// StatusInterrupted is a pass that was running when Keystone stopped.
	// It is not run again, because it may already have added its source.
	StatusInterrupted Status = "interrupted"
)

// ended reports whether a status is one a pass ends with.
func (s Status) ended() bool {
	return s == StatusDone || s == StatusSkipped || s == StatusFailed || s == StatusInterrupted
}

// The reasons a pass gives that are always the same, written for a person.
const (
	ReasonOff         = "The memory pass is switched off."
	ReasonIncognito   = "The chat is incognito, so no memory pass is made for it."
	ReasonNoStore     = "There is no knowledge store to add facts to."
	ReasonCannotAdd   = "The knowledge store cannot take new sources."
	ReasonNothing     = "The turn has no words to read."
	ReasonInterrupted = "Keystone stopped while this pass was running. It is not run again, because it may already have added its source."
)

// Pass is one turn's memory pass.
type Pass struct {
	TurnID string `json:"turn_id"`
	ChatID string `json:"chat_id"`
	Status Status `json:"status"`
	// Reason says why a pass was skipped, failed or interrupted.
	Reason string `json:"reason,omitempty"`
	// Model is the registry name of the model that read the exchange.
	Model string `json:"model,omitempty"`
	// Space and SourceID are where a pass that found facts put them, and
	// Sensitive says that space is sensitive.
	Space     string `json:"space,omitempty"`
	SourceID  string `json:"source_id,omitempty"`
	Sensitive bool   `json:"sensitive"`
	// Facts are the facts a pass added, as text. Facts that were kept
	// nowhere are not kept here either.
	Facts   []string   `json:"facts"`
	Created time.Time  `json:"created"`
	Ended   *time.Time `json:"ended,omitempty"`
}

// timeLayout is the chat record's: fixed width, in UTC, so it sorts as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

func stamp() string { return time.Now().UTC().Format(timeLayout) }

const passColumns = "turn_id, chat_id, status, reason, model, space, source_id, sensitive, facts, created_at, ended_at"

func scanPass(sc interface{ Scan(...any) error }) (Pass, error) {
	var p Pass
	var facts, created, ended string
	if err := sc.Scan(&p.TurnID, &p.ChatID, &p.Status, &p.Reason, &p.Model, &p.Space, &p.SourceID, &p.Sensitive, &facts, &created, &ended); err != nil {
		return Pass{}, err
	}
	if err := json.Unmarshal([]byte(facts), &p.Facts); err != nil {
		return Pass{}, fmt.Errorf("memory: the facts of turn %s's pass do not read: %w", p.TurnID, err)
	}
	if p.Facts == nil {
		p.Facts = []string{}
	}
	var err error
	if p.Created, err = time.Parse(timeLayout, created); err != nil {
		return Pass{}, fmt.Errorf("memory: turn %s's pass: %w", p.TurnID, err)
	}
	if ended != "" {
		at, err := time.Parse(timeLayout, ended)
		if err != nil {
			return Pass{}, fmt.Errorf("memory: turn %s's pass: %w", p.TurnID, err)
		}
		p.Ended = &at
	}
	return p, nil
}

// Queue adds a turn's pass to its chat's queue. A turn has one pass:
// queueing it again changes nothing and reports false.
func Queue(ctx context.Context, s *tenant.Store, turnID, chatID string) (bool, error) {
	res, err := s.DB().ExecContext(ctx,
		"INSERT INTO memory_passes(turn_id, chat_id, status, created_at) VALUES(?, ?, 'queued', ?) ON CONFLICT(turn_id) DO NOTHING",
		turnID, chatID, stamp())
	if err != nil {
		return false, fmt.Errorf("memory: queueing the pass of turn %s: %w", turnID, err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Claim takes the chat's oldest queued pass and marks it running. ok is
// false when none waits.
func Claim(ctx context.Context, s *tenant.Store, chatID string) (p Pass, ok bool, err error) {
	for {
		p, err = scanPass(s.DB().QueryRowContext(ctx,
			"SELECT "+passColumns+" FROM memory_passes WHERE chat_id = ? AND status = 'queued' ORDER BY seq LIMIT 1", chatID))
		if errors.Is(err, sql.ErrNoRows) {
			return Pass{}, false, nil
		}
		if err != nil {
			return Pass{}, false, err
		}
		res, err := s.DB().ExecContext(ctx, "UPDATE memory_passes SET status = 'running' WHERE turn_id = ? AND status = 'queued'", p.TurnID)
		if err != nil {
			return Pass{}, false, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			p.Status = StatusRunning
			return p, true, nil
		}
		// Taken or ended in the meantime: look again.
	}
}

// Interrupt marks the chat's passes that are running as interrupted, and
// returns their turns. A chat's worker calls it when it starts: in one
// process a chat has one worker, which ends every pass it takes, so a pass
// still running was left by a stop.
func Interrupt(ctx context.Context, s *tenant.Store, chatID string) ([]string, error) {
	rows, err := s.DB().QueryContext(ctx,
		"UPDATE memory_passes SET status = 'interrupted', reason = ?, ended_at = ? WHERE chat_id = ? AND status = 'running' RETURNING turn_id",
		ReasonInterrupted, stamp(), chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Finish ends a running pass with its outcome: its status, its reason,
// and for a pass that landed its model, space, source, label and facts.
func Finish(ctx context.Context, s *tenant.Store, p Pass) error {
	if !p.Status.ended() {
		return fmt.Errorf("memory: %q is not a status a pass ends with", p.Status)
	}
	facts := p.Facts
	if facts == nil {
		facts = []string{}
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return err
	}
	res, err := s.DB().ExecContext(ctx, `UPDATE memory_passes SET status = ?, reason = ?, model = ?, space = ?, source_id = ?,
		sensitive = ?, facts = ?, ended_at = ? WHERE turn_id = ? AND status = 'running'`,
		string(p.Status), p.Reason, p.Model, p.Space, p.SourceID, p.Sensitive, string(raw), stamp(), p.TurnID)
	if err != nil {
		return fmt.Errorf("memory: ending the pass of turn %s: %w", p.TurnID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("memory: the pass of turn %s is not running, or its chat is gone", p.TurnID)
	}
	return nil
}

// Get returns one turn's pass. ok is false when the turn has none.
func Get(ctx context.Context, s *tenant.Store, turnID string) (Pass, bool, error) {
	p, err := scanPass(s.DB().QueryRowContext(ctx, "SELECT "+passColumns+" FROM memory_passes WHERE turn_id = ?", turnID))
	if errors.Is(err, sql.ErrNoRows) {
		return Pass{}, false, nil
	}
	return p, err == nil, err
}

// Passes returns a chat's passes, the oldest first.
func Passes(ctx context.Context, s *tenant.Store, chatID string) ([]Pass, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT "+passColumns+" FROM memory_passes WHERE chat_id = ? ORDER BY seq", chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pass{}
	for rows.Next() {
		p, err := scanPass(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Waiting reports whether a pass of the chat is queued.
func Waiting(ctx context.Context, s *tenant.Store, chatID string) (bool, error) {
	var n int
	err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM memory_passes WHERE chat_id = ? AND status = 'queued'", chatID).Scan(&n)
	return n > 0, err
}

// Incognito reports whether a chat is incognito: its turns run no memory
// pass.
func Incognito(ctx context.Context, s *tenant.Store, chatID string) (bool, error) {
	var on bool
	err := s.DB().QueryRowContext(ctx, "SELECT incognito FROM memory_chats WHERE chat_id = ?", chatID).Scan(&on)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return on, err
}

// SetIncognito turns a chat's incognito flag on or off. The chat must be
// in the tenant's record. A pass already queued for the chat is skipped
// when its turn comes, while the flag is on.
func SetIncognito(ctx context.Context, s *tenant.Store, chatID string, on bool) error {
	_, err := s.DB().ExecContext(ctx, `INSERT INTO memory_chats(chat_id, incognito, changed_at) VALUES(?, ?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET incognito = excluded.incognito, changed_at = excluded.changed_at, left_at = ''`, chatID, on, stamp())
	if err != nil {
		return fmt.Errorf("memory: setting chat %s's incognito flag: %w", chatID, err)
	}
	return nil
}
