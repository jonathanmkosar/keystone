package memory

import (
	"context"
	"fmt"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Incognito chats that keep no record (P6-07). The flag on a chat
// (memory_chats, P6-06) stops its memory pass. Since P6-07 it also means
// the chat is deleted when the person leaves it, or once it has been idle
// for knowledge.incognito_keep: the turn runner does the deleting, and
// this file keeps when the person left.

// Settings of incognito chats, in settings/knowledge.json. The turn reads
// them.
const (
	SettingIncognitoKeep  = "knowledge.incognito_keep"
	SettingIncognitoSweep = "knowledge.incognito_sweep_every"
)

// IncognitoChat is one incognito chat, as the sweep reads it.
type IncognitoChat struct {
	ID string
	// Left says the person left it: it goes as soon as nothing works in it.
	Left bool
}

// IncognitoChats returns the tenant's incognito chats, the oldest flag
// first.
func IncognitoChats(ctx context.Context, s *tenant.Store) ([]IncognitoChat, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT chat_id, left_at <> '' FROM memory_chats WHERE incognito = 1 ORDER BY changed_at, chat_id")
	if err != nil {
		return nil, fmt.Errorf("memory: reading the incognito chats: %w", err)
	}
	defer rows.Close()
	var out []IncognitoChat
	for rows.Next() {
		var c IncognitoChat
		if err := rows.Scan(&c.ID, &c.Left); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Leave marks that the person left an incognito chat. It changes nothing
// for a chat that is not incognito, and reports whether the chat is.
func Leave(ctx context.Context, s *tenant.Store, chatID string) (bool, error) {
	res, err := s.DB().ExecContext(ctx, "UPDATE memory_chats SET left_at = ? WHERE chat_id = ? AND incognito = 1 AND left_at = ''", stamp(), chatID)
	if err != nil {
		return false, fmt.Errorf("memory: leaving chat %s: %w", chatID, err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, nil
	}
	return Incognito(ctx, s, chatID)
}
