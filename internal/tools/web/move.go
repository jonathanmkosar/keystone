package web

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The move (P7-06). Before search providers, each person's Tavily key was
// provider.tavily, and web search used it. That key becomes the person's
// first own provider, named tavily, the first time the new version sees
// it: at start, in the sweep's first pass, or at the person's first search,
// whichever comes first. The entry keeps provider.tavily as its key's name,
// so nothing is copied and the key keeps working, however it is stored
// later (decision 0681).

// movedKey records in search_meta that the move was done for this person.
const movedKey = "tavily_moved"

// moveTavily makes the person's first own provider from provider.tavily,
// once. A person with no provider.tavily has nothing to move yet, and is
// looked at again next time.
func moveTavily(ctx context.Context, s *tenant.Store, now time.Time) error {
	db := s.DB()
	var at string
	err := db.QueryRowContext(ctx, "SELECT value FROM search_meta WHERE key = ?", movedKey).Scan(&at)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	key, ok, err := s.Secrets().Get(KeyName)
	if err != nil {
		return err
	}
	if !ok || key == "" {
		return nil
	}
	pl := own(s)
	defer pl.lock()()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM search_providers WHERE name = ?", KindTavily).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		// First in the order: the provider every search used until now.
		if _, err := tx.ExecContext(ctx, "UPDATE search_providers SET position = position + 1"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO search_providers(name, position, kind, endpoint, key_name, enabled, added_at)
			VALUES(?, 0, ?, '', ?, 1, ?)`, KindTavily, KindTavily, KeyName, now.UTC().Format(timeLayout)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO search_meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING", movedKey, now.UTC().Format(timeLayout)); err != nil {
		return err
	}
	return tx.Commit()
}
