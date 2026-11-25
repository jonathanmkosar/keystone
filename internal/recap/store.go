package recap

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

const timeLayout = "2006-01-02T15:04:05.000Z"

// now is the clock. Tests replace it.
var now = time.Now

// Save keeps a chat's recap in place of the one it had, and returns it as
// kept. A recap of a chat that had a sensitive recap stays sensitive: the
// mark is never taken back, as a chat that was sent sensitive content is
// held for good.
func Save(ctx context.Context, s *tenant.Store, r Recap) (Recap, error) {
	r.Summary = strings.TrimSpace(r.Summary)
	switch {
	case r.ChatID == "", r.Summary == "", r.Model == "", r.Turns < 0, r.ThroughID < 0:
		return Recap{}, fmt.Errorf("%w: it needs its chat, a summary and the model that wrote it", ErrInvalid)
	}
	if r.Topics == nil {
		r.Topics = []string{}
	}
	if r.Decisions == nil {
		r.Decisions = []string{}
	}
	topics, err := json.Marshal(r.Topics)
	if err != nil {
		return Recap{}, err
	}
	decisions, err := json.Marshal(r.Decisions)
	if err != nil {
		return Recap{}, err
	}
	r.Made = now().UTC().Truncate(time.Millisecond)
	_, err = s.DB().ExecContext(ctx, `
		INSERT INTO chat_recaps (chat_id, summary, topics, decisions, through_id, turns, model, sensitive, request_id, made_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10)
		ON CONFLICT(chat_id) DO UPDATE SET
			summary = excluded.summary, topics = excluded.topics, decisions = excluded.decisions,
			through_id = excluded.through_id, turns = excluded.turns, model = excluded.model,
			sensitive = max(sensitive, excluded.sensitive),
			request_id = excluded.request_id, made_at = excluded.made_at`,
		r.ChatID, r.Summary, string(topics), string(decisions), r.ThroughID, r.Turns, r.Model, r.Sensitive, r.RequestID, r.Made.Format(timeLayout))
	if err != nil {
		return Recap{}, err
	}
	kept, ok, err := Get(ctx, s, r.ChatID)
	if err == nil && !ok {
		err = errors.New("recap: the recap was not kept")
	}
	return kept, err
}

const recapColumns = "chat_id, summary, topics, decisions, through_id, turns, model, sensitive, request_id, made_at"

func scan(row interface{ Scan(...any) error }) (Recap, error) {
	var r Recap
	var topics, decisions, made string
	if err := row.Scan(&r.ChatID, &r.Summary, &topics, &decisions, &r.ThroughID, &r.Turns, &r.Model, &r.Sensitive, &r.RequestID, &made); err != nil {
		return Recap{}, err
	}
	if err := json.Unmarshal([]byte(topics), &r.Topics); err != nil {
		return Recap{}, fmt.Errorf("the recap of chat %s: its topics do not read: %w", r.ChatID, err)
	}
	if err := json.Unmarshal([]byte(decisions), &r.Decisions); err != nil {
		return Recap{}, fmt.Errorf("the recap of chat %s: its decisions do not read: %w", r.ChatID, err)
	}
	var err error
	if r.Made, err = time.Parse(timeLayout, made); err != nil {
		return Recap{}, fmt.Errorf("the recap of chat %s: %w", r.ChatID, err)
	}
	return r, nil
}

// Get returns a chat's recap. ok is false when it has none.
func Get(ctx context.Context, s *tenant.Store, chatID string) (Recap, bool, error) {
	r, err := scan(s.DB().QueryRowContext(ctx, "SELECT "+recapColumns+" FROM chat_recaps WHERE chat_id = ?", chatID))
	if errors.Is(err, sql.ErrNoRows) {
		return Recap{}, false, nil
	}
	if err != nil {
		return Recap{}, false, err
	}
	return r, true, nil
}

// Summaries returns the summary of each of the chats that has a recap, by
// the chat's id. It is for the person's own chat list, which shows a
// sensitive chat's summary too: fixed rule 3 is about what reaches a
// model.
func Summaries(ctx context.Context, s *tenant.Store, chatIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(chatIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(chatIDs))
	for i, id := range chatIDs {
		args[i] = id
	}
	rows, err := s.DB().QueryContext(ctx, "SELECT chat_id, summary FROM chat_recaps WHERE chat_id IN (?"+strings.Repeat(", ?", len(chatIDs)-1)+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, summary string
		if err := rows.Scan(&id, &summary); err != nil {
			return nil, err
		}
		out[id] = summary
	}
	return out, rows.Err()
}
