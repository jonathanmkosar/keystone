package attach

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

// now is the clock. Tests replace it.
var now = time.Now

func stamp() string { return now().UTC().Format(timeLayout) }

// Incognito reports whether a chat is incognito. A flag that cannot be read
// counts as on, so nothing of the chat goes anywhere it should not.
func Incognito(ctx context.Context, s *tenant.Store, chatID string) (bool, error) {
	on, err := memory.Incognito(ctx, s, chatID)
	if err != nil {
		return true, err
	}
	return on, nil
}

// both checks that two chats are in this tenant's record.
func both(ctx context.Context, tx *sql.Tx, a, b string) error {
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM chats WHERE id IN (?, ?)", a, b).Scan(&n); err != nil {
		return err
	}
	if n != 2 {
		return ErrNotFound
	}
	return nil
}

// notIncognito refuses when either chat is incognito, or its flag cannot be
// read.
func notIncognito(ctx context.Context, s *tenant.Store, ids ...string) error {
	for _, id := range ids {
		on, err := Incognito(ctx, s, id)
		if err != nil {
			return fmt.Errorf("attach: reading whether chat %s is incognito: %w", id, err)
		}
		if on {
			return ErrIncognito
		}
	}
	return nil
}

func inTx(ctx context.Context, s *tenant.Store, f func(tx *sql.Tx) error) error {
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Add attaches one chat to another, both of this tenant, and reports
// whether it was attached now: a chat attached already is left as it is.
// max is chats.attach_max, and counts the chat this one continues from.
func Add(ctx context.Context, s *tenant.Store, chatID, attachedID string, max int) (bool, error) {
	if chatID == attachedID {
		return false, ErrSelf
	}
	if err := notIncognito(ctx, s, chatID, attachedID); err != nil {
		return false, err
	}
	added := false
	err := inTx(ctx, s, func(tx *sql.Tx) error {
		if err := both(ctx, tx, chatID, attachedID); err != nil {
			return err
		}
		var have, already int
		if err := tx.QueryRowContext(ctx, "SELECT count(*), coalesce(sum(attached_id = ?), 0) FROM chat_attachments WHERE chat_id = ?", attachedID, chatID).Scan(&have, &already); err != nil {
			return err
		}
		switch {
		case already > 0:
			return nil
		case have >= max:
			return ErrFull
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO chat_attachments(chat_id, attached_id, handoff, added_at) VALUES(?, ?, 0, ?)", chatID, attachedID, stamp()); err != nil {
			return err
		}
		added = true
		return nil
	})
	return added, err
}

// Remove takes an attached chat off a chat, and reports whether it was
// attached. The chat a chat continues from is not taken off: ErrParent.
func Remove(ctx context.Context, s *tenant.Store, chatID, attachedID string) (bool, error) {
	removed := false
	err := inTx(ctx, s, func(tx *sql.Tx) error {
		var handoff bool
		err := tx.QueryRowContext(ctx, "SELECT handoff FROM chat_attachments WHERE chat_id = ? AND attached_id = ?", chatID, attachedID).Scan(&handoff)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return err
		case handoff:
			return ErrParent
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM chat_attachments WHERE chat_id = ? AND attached_id = ?", chatID, attachedID); err != nil {
			return err
		}
		removed = true
		return nil
	})
	return removed, err
}

// Handoff records that child was handed off from parent: child continues
// from parent, which is attached to it for good. The parent must not be
// incognito, and the child must not continue from a chat already.
func Handoff(ctx context.Context, s *tenant.Store, parentID, childID string) error {
	if parentID == childID {
		return ErrSelf
	}
	if err := notIncognito(ctx, s, parentID); err != nil {
		return err
	}
	return inTx(ctx, s, func(tx *sql.Tx) error {
		if err := both(ctx, tx, childID, parentID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM chat_attachments WHERE chat_id = ? AND handoff = 1", childID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrHandedOff
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_attachments(chat_id, attached_id, handoff, added_at) VALUES(?, ?, 1, ?)
			ON CONFLICT(chat_id, attached_id) DO UPDATE SET handoff = 1`, childID, parentID, stamp())
		return err
	})
}

const linkColumns = "chat_id, attached_id, handoff, added_at"

func scanLinks(rows *sql.Rows) ([]Link, error) {
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var l Link
		var added string
		if err := rows.Scan(&l.ChatID, &l.AttachedID, &l.Handoff, &added); err != nil {
			return nil, err
		}
		var err error
		if l.Added, err = time.Parse(timeLayout, added); err != nil {
			return nil, fmt.Errorf("attach: the link from chat %s to chat %s: %w", l.ChatID, l.AttachedID, err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// List returns the chats attached to a chat: the one it continues from
// first, then the rest in the order they were attached.
func List(ctx context.Context, s *tenant.Store, chatID string) ([]Link, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT "+linkColumns+" FROM chat_attachments WHERE chat_id = ? ORDER BY handoff DESC, seq", chatID)
	if err != nil {
		return nil, err
	}
	return scanLinks(rows)
}

// Handoffs returns the links of the chats handed off from a chat, oldest
// first. Each link's ChatID is a chat that continues from it.
func Handoffs(ctx context.Context, s *tenant.Store, chatID string) ([]Link, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT "+linkColumns+" FROM chat_attachments WHERE attached_id = ? AND handoff = 1 ORDER BY seq", chatID)
	if err != nil {
		return nil, err
	}
	return scanLinks(rows)
}

// Is reports whether attachedID is attached to chatID, as an attached chat
// or as the chat it continues from.
func Is(ctx context.Context, s *tenant.Store, chatID, attachedID string) (bool, error) {
	var n int
	err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM chat_attachments WHERE chat_id = ? AND attached_id = ?", chatID, attachedID).Scan(&n)
	return n > 0, err
}
