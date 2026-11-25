package recall

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// This file keeps two things in the tenant's own database, in the table
// turn_recall: what each turn's recall found, for the turn inspector, and
// which chats have been sent content from a sensitive space.

// Save keeps the record of a turn's recall. A turn has one record: saving
// again replaces it. The sensitive mark of a turn is never taken back by a
// later save.
func Save(ctx context.Context, ts *tenant.Store, turnID, chatID string, rec Record) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return put(ctx, ts, turnID, chatID, rec.Sensitive, string(raw))
}

// Hold marks a chat as one that has been sent content from a sensitive
// space in this turn, by something other than automatic recall. The tool
// loop calls it when a lookup's Result says Sensitive.
func Hold(ctx context.Context, ts *tenant.Store, turnID, chatID string) error {
	return put(ctx, ts, turnID, chatID, true, "")
}

func put(ctx context.Context, ts *tenant.Store, turnID, chatID string, sensitive bool, record string) error {
	if turnID == "" || chatID == "" {
		return errors.New("recall: a record needs its turn and its chat")
	}
	_, err := ts.DB().ExecContext(ctx, `
		INSERT INTO turn_recall (turn_id, chat_id, sensitive, record) VALUES (?1, ?2, ?3, ?4)
		ON CONFLICT(turn_id) DO UPDATE SET
			sensitive = max(sensitive, excluded.sensitive),
			record = CASE WHEN excluded.record = '' THEN record ELSE excluded.record END`,
		turnID, chatID, sensitive, record)
	return err
}

// Saved returns the record of a turn's recall, and whether it has one.
func Saved(ctx context.Context, ts *tenant.Store, turnID string) (Record, bool, error) {
	var raw string
	err := ts.DB().QueryRowContext(ctx, "SELECT record FROM turn_recall WHERE turn_id = ?", turnID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && raw == "") {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var rec Record
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return Record{}, false, fmt.Errorf("turn %s: its stored recall does not read: %w", turnID, err)
	}
	return rec, true, nil
}

// Held reports whether a chat has been sent content from a sensitive
// space, in any turn. Such a chat's history may repeat that content, so
// every later call of the chat is marked sensitive for routing, and only
// a model that may read it is sent the chat.
func Held(ctx context.Context, ts *tenant.Store, chatID string) (bool, error) {
	var held bool
	err := ts.DB().QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM turn_recall WHERE chat_id = ? AND sensitive = 1)", chatID).Scan(&held)
	return held, err
}
