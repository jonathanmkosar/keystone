package embedding

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// State is where a person's refresh stands for one knowledge store: the
// last refresh's result, when it ran, the model's class then, and what the
// person is told. It is kept in embedding_state, one row for each adapter,
// from the migration 20261125T234753_embeddings.sql.
type State struct {
	Adapter string    `json:"adapter"`
	At      time.Time `json:"at"`
	// Class is the embedding model's class when it ran: local, trusted or
	// hosted. Empty when there was no model.
	Class string `json:"class,omitempty"`
	Result
	// Note is what the person is told, in a sentence or two: why nothing
	// was done, what was left without a vector and why.
	Note string `json:"note,omitempty"`
}

// timeLayout is the chat record's: fixed width, in UTC, so it sorts as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

// Save keeps a refresh's state, in place of the one before for the same
// adapter.
func Save(ctx context.Context, s *tenant.Store, st State) error {
	if st.Adapter == "" {
		return errors.New("embedding: a state needs its adapter")
	}
	if st.At.IsZero() {
		st.At = time.Now()
	}
	_, err := s.DB().ExecContext(ctx, `INSERT INTO embedding_state(adapter, model, class, vector_model, at, listed, embedded, held, stale, failed, waiting, stopped, note)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(adapter) DO UPDATE SET model = excluded.model, class = excluded.class, vector_model = excluded.vector_model,
			at = excluded.at, listed = excluded.listed, embedded = excluded.embedded, held = excluded.held, stale = excluded.stale,
			failed = excluded.failed, waiting = excluded.waiting, stopped = excluded.stopped, note = excluded.note`,
		st.Adapter, st.Model, st.Class, st.VectorModel, st.At.UTC().Format(timeLayout), st.Listed, st.Embedded, st.Held,
		st.Stale, st.Failed, st.Waiting, st.Stopped, st.Note)
	if err != nil {
		return fmt.Errorf("embedding: keeping the state: %w", err)
	}
	return nil
}

// Load reads the state for one adapter. ok is false when no refresh has
// run for it.
func Load(ctx context.Context, s *tenant.Store, adapter string) (State, bool, error) {
	st := State{Adapter: adapter}
	var at string
	err := s.DB().QueryRowContext(ctx, `SELECT model, class, vector_model, at, listed, embedded, held, stale, failed, waiting, stopped, note
		FROM embedding_state WHERE adapter = ?`, adapter).Scan(&st.Model, &st.Class, &st.VectorModel, &at, &st.Listed, &st.Embedded,
		&st.Held, &st.Stale, &st.Failed, &st.Waiting, &st.Stopped, &st.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return State{Adapter: adapter}, false, nil
	}
	if err != nil {
		return State{}, false, fmt.Errorf("embedding: reading the state: %w", err)
	}
	if st.At, err = time.Parse(timeLayout, at); err != nil {
		return State{}, false, fmt.Errorf("embedding: the state's time %q does not read: %w", at, err)
	}
	return st, true, nil
}

// Note is what a person is told about a refresh: what was left without a
// vector and why. It names no page: a page's title may be sensitive.
func Note(r Result) string {
	var parts []string
	if r.Held > 0 {
		parts = append(parts, fmt.Sprintf("%d %s in a sensitive space %s no vector, because the embedding model %s is not local or trusted, so recall finds %s by %s words alone. Name a local or trusted embedding model in routing.embedding_model to search %s by meaning too.",
			r.Held, plural(r.Held, "page", "pages"), plural(r.Held, "has", "have"), r.Model, plural(r.Held, "it", "them"), plural(r.Held, "its", "their"), plural(r.Held, "it", "them")))
	}
	if r.Stopped != "" {
		parts = append(parts, "The refresh stopped early: "+strings.TrimSuffix(r.Stopped, ".")+".")
	}
	if r.Waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d %s for the next refresh.", r.Waiting, plural(r.Waiting, "page waits", "pages wait")))
	}
	return strings.Join(parts, " ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
