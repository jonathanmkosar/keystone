package wikijobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/wikijobs/scope"
)

// Two guards keep a wiki run to models that may read sensitive content
// (fixed rule 3), whatever its route says:
//
//   - The chat is held for sensitive content from its first model call
//     (connectors.ForCall), so every later call of it, a task's rounds,
//     recaps and compaction, is planned for local and trusted models only.
//   - Gate refuses, before it is sent, any call of a wiki run to a model the
//     registry marks hosted, the first call included, so a hosted model in
//     the person's fallback chain is passed over.
//
// And a wiki run's chat runs no memory pass (SkipMemory), so a job's report
// never comes back to the store as a source of its own.

// Gate wraps the turn's gate. A call of a wiki run to a model that may not
// serve one is refused with the kind unavailable, so the route goes on to
// its next model and no notice of the cap is kept; every other call is
// asked of inner. A turn whose chat cannot be told is taken as a wiki run's.
func Gate(inner turn.Gate) turn.Gate {
	return func(ctx context.Context, s *tenant.Store, turnID string, m models.Model, mostMicros int64) *seam.Error {
		if !scope.MayServe(string(m.Class)) {
			wiki, err := turnIsRun(ctx, s, turnID)
			if err != nil || wiki {
				return &seam.Error{Kind: seam.ErrUnavailable, Message: fmt.Sprintf(
					"The call to %s was not sent. A wiki upkeep job reads your knowledge store, which may hold sensitive pages, so it runs only on a local or trusted model, and %s is neither.", m.Name, m.Name)}
			}
		}
		if inner == nil {
			return nil
		}
		return inner(ctx, s, turnID, m, mostMicros)
	}
}

// turnIsRun reports whether a turn is in a wiki run's chat.
func turnIsRun(ctx context.Context, s *tenant.Store, turnID string) (bool, error) {
	if s == nil {
		return false, nil
	}
	var chatID string
	err := s.DB().QueryRowContext(ctx, "SELECT chat_id FROM turns WHERE id = ?", turnID).Scan(&chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, ok, err := JobOfChat(ctx, s, chatID)
	return ok, err
}

// SkipMemory reports whether a chat's turns run no memory pass: a wiki
// run's chat, or one that cannot be told.
func SkipMemory(ctx context.Context, s *tenant.Store, chatID string) bool {
	_, ok, err := JobOfChat(ctx, s, chatID)
	return ok || err != nil
}
