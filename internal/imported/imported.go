// Package imported is the record of what was brought into a tenant from
// another app's database (P12-03): which of the source's turns and routines
// are here already, the mark an imported chat wears, and the model the
// source named for an imported reply.
//
// It keeps the record and nothing else. internal/importer reads a source
// and decides what to bring; the chat routes read the marks to show them.
// Every function works on one tenant's store, so it reaches that tenant's
// record only. docs/architecture/import.md is the contract.
package imported

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// NewKeystone names the owner's earlier Python app as a source.
const NewKeystone = "new-keystone"

// The kinds of thing a source id names.
const (
	// KindTurn is one turn of the source: a person's message and the reply.
	KindTurn = "turn"
	// KindRoutine is a routine of the source, which becomes a schedule.
	KindRoutine = "routine"
)

// What an imported chat was in its source.
const (
	// ChatStretch is a stretch of the source's one continuing chat.
	ChatStretch = "chat"
	// ChatRoutine is one run of a routine.
	ChatRoutine = "routine"
	// ChatBuild is one build of an app.
	ChatBuild = "build"
)

// Label is a source's name for a person.
func Label(source string) string {
	if source == NewKeystone {
		return "New Keystone"
	}
	return source
}

// Times are stored as the chat record stores them, so they sort as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

func format(t time.Time) string { return t.UTC().Format(timeLayout) }

// Execer is what *sql.DB and *sql.Tx share for writing. The functions that
// write take one, so a caller writes a chat and its record in one
// transaction.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Run is one run of an import that wrote something.
type Run struct {
	// ID is 16 hex characters.
	ID string
	// Source and Workspace say what was read, and FileSHA256 which copy.
	Source, Workspace, FileSHA256 string
	// RequestID names the run's entries in the audit log.
	RequestID string
	Started   time.Time
}

// Begin writes a run's row before it brings anything in.
func Begin(ctx context.Context, q Execer, r Run) error {
	_, err := q.ExecContext(ctx, `INSERT INTO imports(id, source, workspace, file_sha256, request_id, started_at)
		VALUES(?, ?, ?, ?, ?, ?)`, r.ID, r.Source, r.Workspace, r.FileSHA256, r.RequestID, format(r.Started))
	return err
}

// End notes what a run wrote and when it ended.
func End(ctx context.Context, q Execer, id string, chats, messages, schedules int, at time.Time) error {
	_, err := q.ExecContext(ctx, "UPDATE imports SET chats = ?, messages = ?, schedules = ?, ended_at = ? WHERE id = ?",
		chats, messages, schedules, format(at), id)
	return err
}

// Came is one thing of the source that is now here.
type Came struct {
	Source, Workspace string
	// Kind is KindTurn or KindRoutine, and SourceID its id in the source.
	Kind, SourceID string
	ImportID       string
	// ChatID is the chat a turn went into; ScheduleID the schedule a
	// routine became.
	ChatID, ScheduleID string
	At                 time.Time
}

// Note records that a thing of the source is here. A source id noted twice
// is refused by the table, which is what makes a second run bring nothing
// twice.
func Note(ctx context.Context, q Execer, c Came) error {
	_, err := q.ExecContext(ctx, `INSERT INTO import_sources(source, workspace, kind, source_id, import_id, chat_id, schedule_id, imported_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, c.Source, c.Workspace, c.Kind, c.SourceID, c.ImportID, c.ChatID, c.ScheduleID, format(c.At))
	return err
}

// Seen returns the source ids of one kind that are here already, each with
// the chat or the schedule it became.
func Seen(ctx context.Context, s *tenant.Store, source, workspace, kind string) (map[string]string, error) {
	rows, err := s.DB().QueryContext(ctx, `SELECT source_id, chat_id, schedule_id FROM import_sources
		WHERE source = ? AND workspace = ? AND kind = ?`, source, workspace, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, chatID, scheduleID string
		if err := rows.Scan(&id, &chatID, &scheduleID); err != nil {
			return nil, err
		}
		out[id] = chatID + scheduleID
	}
	return out, rows.Err()
}

// Mark is what an imported chat says about where it came from.
type Mark struct {
	// From is the source's name for a person, such as New Keystone.
	From string `json:"from"`
	// Kind is chat (a stretch of the source's continuing chat), routine (a
	// routine's run) or build.
	Kind string `json:"kind"`
	// At is when it was imported.
	At time.Time `json:"at"`
	// Turns is how many turns it held there, and Unanswered how many of
	// them have no reply.
	Turns      int `json:"turns"`
	Unanswered int `json:"unanswered,omitempty"`
	// First and Last are the times of its first and last message there.
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
	// Models are the models the source named for its replies, in the order
	// they first answered.
	Models []string `json:"models"`
	// Steps counts the events the source recorded in it, by the source's
	// own event type.
	Steps map[string]int `json:"steps,omitempty"`
	// Note says, for a person, what the source recorded that the chat
	// record cannot hold. Empty when there is nothing to say.
	Note string `json:"note,omitempty"`
}

// NewMark is a mark to write, with the source and the run it came from.
type NewMark struct {
	Mark
	Source, ImportID string
}

// MarkChat writes the mark of a chat that was just imported.
func MarkChat(ctx context.Context, q Execer, chatID string, m NewMark) error {
	models := m.Models
	if models == nil {
		models = []string{}
	}
	rawModels, err := json.Marshal(models)
	if err != nil {
		return err
	}
	steps := m.Steps
	if steps == nil {
		steps = map[string]int{}
	}
	rawSteps, err := json.Marshal(steps)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO imported_chats(chat_id, import_id, source, kind, turns, unanswered, first_at, last_at, models, steps, note, imported_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		chatID, m.ImportID, m.Source, m.Kind, m.Turns, m.Unanswered, format(m.First), format(m.Last), string(rawModels), string(rawSteps), m.Note, format(m.At))
	return err
}

// MarkMessage records the source's turn an imported message belongs to,
// and for a reply the model the source named.
func MarkMessage(ctx context.Context, q Execer, messageID int64, sourceID, model string) error {
	_, err := q.ExecContext(ctx, "INSERT INTO imported_messages(message_id, source_id, model) VALUES(?, ?, ?)", messageID, sourceID, model)
	return err
}

// Marks returns the marks of those of the chats that were imported, by
// chat id. A chat that was made here has none.
func Marks(ctx context.Context, s *tenant.Store, chatIDs []string) (map[string]Mark, error) {
	out := map[string]Mark{}
	if len(chatIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(chatIDs))
	for i, id := range chatIDs {
		args[i] = id
	}
	rows, err := s.DB().QueryContext(ctx, `SELECT chat_id, source, kind, turns, unanswered, first_at, last_at, models, steps, note, imported_at
		FROM imported_chats WHERE chat_id IN (?`+strings.Repeat(", ?", len(chatIDs)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, source, first, last, models, steps, at string
		var m Mark
		if err := rows.Scan(&id, &source, &m.Kind, &m.Turns, &m.Unanswered, &first, &last, &models, &steps, &m.Note, &at); err != nil {
			return nil, err
		}
		m.From = Label(source)
		for _, f := range []struct {
			text string
			into *time.Time
		}{{first, &m.First}, {last, &m.Last}, {at, &m.At}} {
			if *f.into, err = time.Parse(timeLayout, f.text); err != nil {
				return nil, fmt.Errorf("imported chat %s: %w", id, err)
			}
		}
		if err := json.Unmarshal([]byte(models), &m.Models); err != nil {
			return nil, fmt.Errorf("imported chat %s: its models do not read: %w", id, err)
		}
		if err := json.Unmarshal([]byte(steps), &m.Steps); err != nil {
			return nil, fmt.Errorf("imported chat %s: its steps do not read: %w", id, err)
		}
		if m.Models == nil {
			m.Models = []string{}
		}
		if len(m.Steps) == 0 {
			m.Steps = nil
		}
		out[id] = m
	}
	return out, rows.Err()
}

// MarkOf returns one chat's mark, and whether it has one.
func MarkOf(ctx context.Context, s *tenant.Store, chatID string) (Mark, bool, error) {
	marks, err := Marks(ctx, s, []string{chatID})
	m, ok := marks[chatID]
	return m, ok, err
}

// Models returns the model the source named for each imported reply of a
// chat, by message id. A message with no model named is left out.
func Models(ctx context.Context, s *tenant.Store, chatID string) (map[int64]string, error) {
	rows, err := s.DB().QueryContext(ctx, `SELECT im.message_id, im.model FROM imported_messages im
		JOIN messages m ON m.id = im.message_id WHERE m.chat_id = ? AND im.model <> ''`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var model string
		if err := rows.Scan(&id, &model); err != nil {
			return nil, err
		}
		out[id] = model
	}
	return out, rows.Err()
}

// Totals counts what a tenant's record holds of a source's workspace.
type Totals struct {
	Runs, Turns, Routines, Chats int
}

// Count returns how much of a source's workspace is here.
func Count(ctx context.Context, s *tenant.Store, source, workspace string) (Totals, error) {
	var t Totals
	err := s.DB().QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM imports WHERE source = ?1 AND workspace = ?2),
		(SELECT count(*) FROM import_sources WHERE source = ?1 AND workspace = ?2 AND kind = 'turn'),
		(SELECT count(*) FROM import_sources WHERE source = ?1 AND workspace = ?2 AND kind = 'routine'),
		(SELECT count(*) FROM imported_chats WHERE source = ?1)`, source, workspace).Scan(&t.Runs, &t.Turns, &t.Routines, &t.Chats)
	if errors.Is(err, sql.ErrNoRows) {
		return Totals{}, nil
	}
	return t, err
}
