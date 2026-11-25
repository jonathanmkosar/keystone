package chat

import (
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// A person pins a message so it is never left out of a call (P5-02). The
// pins are rows in chat_pins, from the migration
// 20261125T110000_compaction.sql, and go with their chat.

// Pin pins a message of a chat, or with pinned false takes the pin off. A
// message on any branch may be pinned. Pinning a message twice, or taking
// off a pin that is not there, changes nothing. A chat or a message that is
// not in the record gives ErrNotFound.
func Pin(s *tenant.Store, chatID string, messageID int64, pinned bool) error {
	if _, err := getMessage(s.DB(), chatID, messageID); err != nil {
		return err
	}
	if !pinned {
		_, err := s.DB().Exec("DELETE FROM chat_pins WHERE chat_id = ? AND message_id = ?", chatID, messageID)
		return err
	}
	_, err := s.DB().Exec("INSERT OR IGNORE INTO chat_pins (chat_id, message_id, pinned_at) VALUES (?, ?, ?)", chatID, messageID, stamp())
	return err
}

// Pins returns the ids of a chat's pinned messages, the oldest message
// first. A chat that is not in the record gives ErrNotFound.
func Pins(s *tenant.Store, chatID string) ([]int64, error) {
	if _, err := Get(s, chatID); err != nil {
		return nil, err
	}
	rows, err := s.DB().Query("SELECT message_id FROM chat_pins WHERE chat_id = ? ORDER BY message_id", chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
