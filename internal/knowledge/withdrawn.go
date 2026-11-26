package knowledge

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Withdrawn sources (P6-07). A person may ask Keystone to forget a source
// its memory pass added. A store with the RemoveSource capability removes
// it. A store without it, such as Waymarks, keeps every source unchanged,
// so Keystone withdraws the source instead: its id is kept in the tenant's
// own database (knowledge_withdrawn), and every store Open hands back
// leaves it out, so no search, read or page's citation brings it back to
// recall, the lookups or the memory pass. The store itself still holds it;
// docs/architecture/memory-screens.md says what that means for Waymarks.
//
// A withdrawn id belongs to one adapter: ids mean something only to the
// store that gave them, so a person who changes stores starts with none
// withdrawn in the new one.

// Withdraw keeps a source out of everything the tenant's store of this
// adapter answers from now on. Withdrawing it again changes nothing.
func Withdraw(ctx context.Context, t *tenant.Store, adapter, sourceID string) error {
	if t == nil || strings.TrimSpace(adapter) == "" || strings.TrimSpace(sourceID) == "" {
		return fmt.Errorf("%w: a withdrawn source needs a tenant, an adapter and an id", ErrInvalid)
	}
	_, err := t.DB().ExecContext(ctx, `INSERT INTO knowledge_withdrawn(adapter, source_id, withdrawn_at) VALUES(?, ?, ?)
		ON CONFLICT(adapter, source_id) DO NOTHING`, adapter, sourceID, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	if err != nil {
		return fmt.Errorf("knowledge: withdrawing source %s: %w", sourceID, err)
	}
	return nil
}

// Withdrawn returns the ids of the sources withdrawn from the tenant's
// store of this adapter, as a set.
func Withdrawn(ctx context.Context, t *tenant.Store, adapter string) (map[string]bool, error) {
	rows, err := t.DB().QueryContext(ctx, "SELECT source_id FROM knowledge_withdrawn WHERE adapter = ?", adapter)
	if err != nil {
		return nil, fmt.Errorf("knowledge: reading the withdrawn sources: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// isWithdrawn reports whether one source is withdrawn.
func isWithdrawn(ctx context.Context, t *tenant.Store, adapter, sourceID string) (bool, error) {
	var n int
	err := t.DB().QueryRowContext(ctx, "SELECT count(*) FROM knowledge_withdrawn WHERE adapter = ? AND source_id = ?", adapter, sourceID).Scan(&n)
	return n > 0, err
}

// withdrawing is the store Open hands back for a tenant: the adapter's own
// store, with the tenant's withdrawn sources left out of every answer.
// Every other call goes straight through.
type withdrawing struct {
	Store
	t       *tenant.Store
	adapter string
}

// Unwrap returns the adapter's own store inside one Open handed back. An
// adapter's tests use it to reach their own type; nothing else should.
func Unwrap(s Store) Store {
	if w, ok := s.(*withdrawing); ok {
		return w.Store
	}
	return s
}

// unread is what a read of the withdrawn list becomes: the store's answer
// when it cannot be reached, so a chat carries on without it rather than
// with a source it was asked to forget.
func unread(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	return fmt.Errorf("%w: the list of withdrawn sources could not be read: %w", ErrUnavailable, err)
}

func (w *withdrawing) Search(ctx context.Context, q Query) ([]Hit, error) {
	hits, err := w.Store.Search(ctx, q)
	if err != nil || !slices.ContainsFunc(hits, func(h Hit) bool { return h.Type == HitSource }) {
		return hits, err
	}
	gone, err := Withdrawn(ctx, w.t, w.adapter)
	if err != nil {
		return nil, unread(ctx, err)
	}
	return slices.DeleteFunc(hits, func(h Hit) bool { return h.Type == HitSource && gone[h.ID] }), nil
}

func (w *withdrawing) ReadSource(ctx context.Context, id string) (Source, error) {
	if err := ctx.Err(); err != nil {
		return Source{}, err
	}
	gone, err := isWithdrawn(ctx, w.t, w.adapter, id)
	if err != nil {
		return Source{}, unread(ctx, err)
	}
	if gone {
		return Source{}, fmt.Errorf("%w: source %q was withdrawn", ErrNotFound, id)
	}
	return w.Store.ReadSource(ctx, id)
}

func (w *withdrawing) ReadPage(ctx context.Context, id string) (Page, error) {
	p, err := w.Store.ReadPage(ctx, id)
	if err != nil || len(p.Sources) == 0 {
		return p, err
	}
	gone, err := Withdrawn(ctx, w.t, w.adapter)
	if err != nil {
		return Page{}, unread(ctx, err)
	}
	p.Sources = slices.DeleteFunc(p.Sources, func(s string) bool { return gone[s] })
	return p, nil
}
