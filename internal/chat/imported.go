package chat

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Importing a chat (P12-03). Every other function here writes a chat as it
// happens, at the clock's time. A chat brought in from another app happened
// earlier, so Import writes it whole, with the times it was given.
// docs/architecture/import.md says who calls it.

// ImportedMessage is one message of a chat that is written as it was.
type ImportedMessage struct {
	Role   seam.Role
	Blocks []seam.Block
	// At is when the message was written, where it came from.
	At time.Time
}

// ImportedChat is a whole chat to write: a title and its messages in a row,
// oldest first, each the next one's parent.
type ImportedChat struct {
	Title    string
	Messages []ImportedMessage
}

// Import writes a chat that happened elsewhere: the chat, its title, its
// messages as one branch and their search rows, in one transaction. The
// chat is made at its first message's time and was last active at its
// newest, so it takes its true place in the list of chats.
//
// also, when it is not nil, runs in the same transaction once the chat is
// written, with the chat and its messages' ids in order: what it writes is
// kept with the chat or not at all. It must use the transaction it is given
// and nothing else of the store, which has one connection.
//
// The title is checked as Rename checks it, and the content as Append
// does. A message that refers to a file is refused: an imported chat holds
// words only.
func Import(s *tenant.Store, lim Limits, in ImportedChat, also func(tx *sql.Tx, c Chat, messageIDs []int64) error) (Chat, error) {
	title := strings.Join(strings.Fields(in.Title), " ")
	if n := utf8.RuneCountInString(title); n == 0 || n > lim.TitleLength {
		return Chat{}, invalid("a title is 1 to %d characters", lim.TitleLength)
	}
	if len(in.Messages) == 0 {
		return Chat{}, invalid("an imported chat needs a message")
	}
	contents := make([]string, len(in.Messages))
	first, last := in.Messages[0].At, in.Messages[0].At
	for i, m := range in.Messages {
		if m.At.IsZero() {
			return Chat{}, invalid("an imported message needs the time it was written")
		}
		if len(refs(m.Blocks)) > 0 {
			return Chat{}, invalid("an imported message refers to no file")
		}
		raw, err := seam.EncodeMessage(seam.Message{Role: m.Role, Blocks: m.Blocks})
		if err != nil {
			return Chat{}, invalid("%v", err)
		}
		contents[i] = string(raw)
		if m.At.After(last) {
			last = m.At
		}
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Chat{}, err
	}
	id := hex.EncodeToString(b[:])
	at := func(t time.Time) string { return t.UTC().Format(timeLayout) }
	var out Chat
	err := inTx(s, func(tx *sql.Tx) error {
		var seq int64
		if err := tx.QueryRow("INSERT INTO chats(id, title, created_at, active_at) VALUES(?, ?, ?, ?) RETURNING seq",
			id, title, at(first), at(last)).Scan(&seq); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO chat_title_search(rowid, title) VALUES(?, ?)", seq, title); err != nil {
			return err
		}
		ids := make([]int64, 0, len(in.Messages))
		var parent sql.NullInt64
		for i, m := range in.Messages {
			res, err := tx.Exec("INSERT INTO messages(chat_id, parent_id, role, content, created_at) VALUES(?, ?, ?, ?, ?)",
				id, parent, string(m.Role), contents[i], at(m.At))
			if err != nil {
				return err
			}
			mid, err := res.LastInsertId()
			if err != nil {
				return err
			}
			if parent.Valid {
				_, err = tx.Exec("UPDATE messages SET current_child = ? WHERE id = ?", mid, parent.Int64)
			} else {
				_, err = tx.Exec("UPDATE chats SET current_root = ? WHERE id = ?", mid, id)
			}
			if err != nil {
				return err
			}
			if text := searchText(m.Blocks); text != "" {
				if _, err := tx.Exec("INSERT INTO message_search(rowid, body) VALUES(?, ?)", mid, text); err != nil {
					return err
				}
			}
			ids = append(ids, mid)
			parent = sql.NullInt64{Int64: mid, Valid: true}
		}
		var err error
		if out, err = scanChat(tx.QueryRow("SELECT "+chatColumns+" FROM chats WHERE id = ?", id)); err != nil {
			return err
		}
		if also != nil {
			return also(tx, out, ids)
		}
		return nil
	})
	if err != nil {
		return Chat{}, err
	}
	return out, nil
}
