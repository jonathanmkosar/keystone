package compact

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Summary is one summary of the start of a chat's branch, as it is kept in
// chat_summaries (the migration 20261125T110000_compaction.sql).
type Summary struct {
	ID     int64  `json:"id"`
	ChatID string `json:"chat_id"`
	// FirstID and ThroughID are the first and the last message it stands
	// for. Messages are a tree, so the path down to ThroughID is one
	// branch, and the summary stands for all of it.
	FirstID   int64 `json:"first_id"`
	ThroughID int64 `json:"through_id"`
	// Turns is how many turns it stands for.
	Turns int `json:"turns"`
	// Extends is the summary it was made from, and 0 for a chat's first.
	Extends int64 `json:"extends,omitempty"`
	// Text is the summary.
	Text string `json:"text"`
	// Model is the registry name of the model that wrote it.
	Model string `json:"model"`
	// Sensitive says the chat had been sent content from a sensitive space
	// when it was made.
	Sensitive bool `json:"sensitive"`
	// Tokens is Keystone's estimate of Text, with no margin.
	Tokens int `json:"tokens"`
	// TurnID is the turn whose call it was made beside.
	TurnID  string    `json:"turn_id,omitempty"`
	Created time.Time `json:"created"`
}

// Usable says whether a summary may be given to a call: one made from a
// chat that had been sent sensitive content goes only to a call whose
// every model may read such content (fixed rule 3). mayReadSensitive is
// that answer, models.Model.MayReadSensitive for each model of the route.
func Usable(s Summary, mayReadSensitive bool) bool {
	return !s.Sensitive || mayReadSensitive
}

// ErrInvalid says a summary was refused before it was kept.
var ErrInvalid = errors.New("compact: the summary is refused")

const timeLayout = "2006-01-02T15:04:05.000Z"

// Save keeps a summary and returns it with its id and time.
func Save(ctx context.Context, s *tenant.Store, sum Summary) (Summary, error) {
	switch {
	case sum.ChatID == "", sum.FirstID <= 0, sum.ThroughID < sum.FirstID, sum.Turns < 1, sum.Text == "", sum.Model == "", sum.Tokens < 0:
		return Summary{}, fmt.Errorf("%w: it needs a chat, the first and last message it stands for, its turns, its text and its model", ErrInvalid)
	}
	sum.Created = time.Now().UTC().Truncate(time.Millisecond)
	err := s.DB().QueryRowContext(ctx, `INSERT INTO chat_summaries
		(chat_id, first_id, through_id, turns, extends, text, model, sensitive, tokens, turn_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		sum.ChatID, sum.FirstID, sum.ThroughID, sum.Turns, sum.Extends, sum.Text, sum.Model, sum.Sensitive, sum.Tokens, sum.TurnID,
		sum.Created.Format(timeLayout)).Scan(&sum.ID)
	if err != nil {
		return Summary{}, err
	}
	return sum, nil
}

// List returns a chat's summaries, the one that stands for the most first,
// and of two that stand for the same, the newer.
func List(ctx context.Context, s *tenant.Store, chatID string) ([]Summary, error) {
	rows, err := s.DB().QueryContext(ctx, `SELECT id, chat_id, first_id, through_id, turns, extends, text, model, sensitive, tokens, turn_id, created_at
		FROM chat_summaries WHERE chat_id = ? ORDER BY through_id DESC, id DESC`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var sum Summary
		var created string
		if err := rows.Scan(&sum.ID, &sum.ChatID, &sum.FirstID, &sum.ThroughID, &sum.Turns, &sum.Extends, &sum.Text, &sum.Model,
			&sum.Sensitive, &sum.Tokens, &sum.TurnID, &created); err != nil {
			return nil, err
		}
		if sum.Created, err = time.Parse(timeLayout, created); err != nil {
			return nil, fmt.Errorf("summary %d: %w", sum.ID, err)
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}
