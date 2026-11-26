package builtin

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
)

// RemoveSource takes a source out of the store for good (P6-07): its row,
// its place in the search index, and the citations of it on any page. A
// page that cited it stays, and its revision moves on, because what it
// cites changed, so its vector is made again.
//
// The delete runs with SQLite's secure delete on, so the source's text is
// written over in the database file and not only marked free, and the
// source index is optimised afterwards, so its words leave the index too.
// What a remove does not reach is in docs/architecture/memory-screens.md:
// a backup made before it, and the audit log's record of the memory call
// that found the facts.
func (s *store) RemoveSource(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n, ok := rowOf(sourcePrefix, id)
	if !ok {
		return fmt.Errorf("%w: source %q", knowledge.ErrNotFound, id)
	}
	return inTx(ctx, s.t, func(tx *sql.Tx) error {
		var there int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM knowledge_sources WHERE id = ?", n).Scan(&there); err != nil {
			return fail(ctx, err)
		}
		if there == 0 {
			return fmt.Errorf("%w: source %q", knowledge.ErrNotFound, id)
		}
		if _, err := tx.ExecContext(ctx, "PRAGMA secure_delete = ON"); err != nil {
			return fail(ctx, err)
		}
		// The pages that cited it move on a revision, and lose the citation,
		// before the source goes: a citation names a source that is there.
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_pages SET revision = revision + 1, updated_at = ?
			WHERE id IN (SELECT page_id FROM knowledge_page_sources WHERE source_id = ?)`, stamp(), n); err != nil {
			return fail(ctx, err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM knowledge_page_sources WHERE source_id = ?", n); err != nil {
			return fail(ctx, err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM knowledge_sources WHERE id = ?", n); err != nil {
			return fail(ctx, err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM knowledge_source_search WHERE rowid = ?", n); err != nil {
			return fail(ctx, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO knowledge_source_search(knowledge_source_search) VALUES('optimize')"); err != nil {
			return fail(ctx, err)
		}
		return nil
	})
}
