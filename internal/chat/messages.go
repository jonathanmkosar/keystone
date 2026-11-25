package chat

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Message is one message in a chat's tree.
type Message struct {
	// ID is a number that is never used twice in one tenant's record.
	// A later message has a larger one.
	ID     int64  `json:"id"`
	ChatID string `json:"chat_id"`
	// ParentID is the message this one answers or follows. Zero means it
	// is a first message.
	ParentID int64 `json:"parent_id,omitempty"`
	// Role and Blocks are the content, in the seam's internal format.
	Role    seam.Role    `json:"role"`
	Blocks  []seam.Block `json:"blocks"`
	Created time.Time    `json:"created"`
	// Siblings are the ids of every message with the same parent, this
	// one included, oldest first. More than one means there are branches
	// here: each edit or retry added one.
	Siblings []int64 `json:"siblings"`
}

// NewMessage is a message to add. Later items add what a turn records
// (the model, the usage) as fields here.
type NewMessage struct {
	// ParentID is the message this one answers or follows. Zero makes a
	// first message.
	ParentID int64
	Role     seam.Role
	Blocks   []seam.Block
}

// messageColumns reads a message and the ids of its siblings.
const messageColumns = `m.id, m.chat_id, COALESCE(m.parent_id, 0), m.content, m.created_at,
	(SELECT group_concat(sib.id) FROM messages sib
	  WHERE sib.chat_id = m.chat_id AND sib.parent_id IS m.parent_id)`

func scanMessage(row interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var content, created, siblings string
	if err := row.Scan(&m.ID, &m.ChatID, &m.ParentID, &content, &created, &siblings); err != nil {
		return Message{}, err
	}
	sm, err := seam.DecodeMessage([]byte(content))
	if err != nil {
		return Message{}, fmt.Errorf("message %d: its stored content does not read: %w", m.ID, err)
	}
	m.Role, m.Blocks = sm.Role, sm.Blocks
	if m.Created, err = parseTime(created); err != nil {
		return Message{}, fmt.Errorf("message %d: %w", m.ID, err)
	}
	for _, part := range strings.Split(siblings, ",") {
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return Message{}, fmt.Errorf("message %d: %w", m.ID, err)
		}
		m.Siblings = append(m.Siblings, id)
	}
	slices.Sort(m.Siblings)
	return m, nil
}

func getMessage(q queryer, chatID string, id int64) (Message, error) {
	m, err := scanMessage(q.QueryRow(
		"SELECT "+messageColumns+" FROM messages m WHERE m.id = ? AND m.chat_id = ?", id, chatID))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, fmt.Errorf("message %d in chat %q: %w", id, chatID, ErrNotFound)
	}
	return m, err
}

// GetMessage returns one message of a chat, on the current branch or not.
func GetMessage(s *tenant.Store, chatID string, id int64) (Message, error) {
	return getMessage(s.DB(), chatID, id)
}

// Append adds a message under its parent and makes it that parent's
// current child. It never changes a message that is already there.
//
// A message whose parent already has children is a new branch. To edit a
// message, append the new text under the edited message's parent. To retry
// a reply, append the new reply under the retried reply's parent.
//
// The content must pass the seam's validation. Every file the blocks refer
// to is tied to the chat.
func Append(s *tenant.Store, chatID string, m NewMessage) (Message, error) {
	content, err := seam.EncodeMessage(seam.Message{Role: m.Role, Blocks: m.Blocks})
	if err != nil {
		return Message{}, invalid("%v", err)
	}
	if m.ParentID < 0 {
		return Message{}, invalid("%d is not a message id", m.ParentID)
	}
	files := refs(m.Blocks)
	for _, name := range files {
		if err := checkFileName(name); err != nil {
			return Message{}, err
		}
	}
	var out Message
	err = inTx(s, func(tx *sql.Tx) error {
		at := stamp()
		if res, err := tx.Exec("UPDATE chats SET active_at = ? WHERE id = ?", at, chatID); err != nil {
			return err
		} else if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return noChat(chatID, sql.ErrNoRows)
		}
		parent := sql.NullInt64{Int64: m.ParentID, Valid: m.ParentID != 0}
		if parent.Valid {
			if _, err := getMessage(tx, chatID, m.ParentID); err != nil {
				return err
			}
		}
		res, err := tx.Exec("INSERT INTO messages(chat_id, parent_id, role, content, created_at) VALUES(?, ?, ?, ?, ?)",
			chatID, parent, string(m.Role), string(content), at)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if parent.Valid {
			_, err = tx.Exec("UPDATE messages SET current_child = ? WHERE id = ?", id, m.ParentID)
		} else {
			_, err = tx.Exec("UPDATE chats SET current_root = ? WHERE id = ?", id, chatID)
		}
		if err != nil {
			return err
		}
		if text := searchText(m.Blocks); text != "" {
			if _, err := tx.Exec("INSERT INTO message_search(rowid, body) VALUES(?, ?)", id, text); err != nil {
				return err
			}
		}
		for _, name := range files {
			if _, err := tx.Exec("INSERT OR IGNORE INTO chat_files(chat_id, name) VALUES(?, ?)", chatID, name); err != nil {
				return err
			}
		}
		out, err = getMessage(tx, chatID, id)
		return err
	})
	return out, err
}

// Path returns the current branch: the messages from the first one down to
// the leaf, following each message's current child. A chat with no
// messages has an empty path.
func Path(s *tenant.Store, chatID string) ([]Message, error) {
	if _, err := Get(s, chatID); err != nil {
		return nil, err
	}
	rows, err := s.DB().Query(`
		WITH RECURSIVE path(id, depth) AS (
			SELECT current_root, 0 FROM chats WHERE id = ? AND current_root IS NOT NULL
			UNION ALL
			SELECT m.current_child, path.depth + 1 FROM messages m JOIN path ON m.id = path.id
			  WHERE m.current_child IS NOT NULL
		)
		SELECT `+messageColumns+` FROM path JOIN messages m ON m.id = path.id ORDER BY path.depth`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var path []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		path = append(path, m)
	}
	return path, rows.Err()
}

// Select puts a message on the current branch. It becomes its parent's
// current child, and so does each message above it, up to the first.
// Below it, the choices made earlier are kept, so the branch reads as it
// did when it was last left.
//
// To step between the branches at one point, select one of the message's
// Siblings.
func Select(s *tenant.Store, chatID string, id int64) error {
	// up is the message and every message above it.
	const up = `WITH RECURSIVE up(id, parent_id) AS (
			SELECT id, parent_id FROM messages WHERE id = ?1 AND chat_id = ?2
			UNION ALL
			SELECT m.id, m.parent_id FROM messages m JOIN up ON m.id = up.parent_id
		) `
	return inTx(s, func(tx *sql.Tx) error {
		if _, err := getMessage(tx, chatID, id); err != nil {
			return err
		}
		if _, err := tx.Exec(up+`UPDATE messages SET current_child = up.id
			FROM up WHERE messages.id = up.parent_id`, id, chatID); err != nil {
			return err
		}
		_, err := tx.Exec(up+`UPDATE chats
			SET current_root = (SELECT id FROM up WHERE parent_id IS NULL)
			WHERE id = ?2`, id, chatID)
		return err
	})
}

// refs lists the files the blocks refer to, tool results included.
func refs(blocks []seam.Block) []string {
	var out []string
	for _, b := range blocks {
		if b.Ref != "" {
			out = append(out, b.Ref)
		}
		out = append(out, refs(b.Content)...)
	}
	return out
}

// searchText is what search indexes of a message: the words of its text
// blocks. Thinking, tool calls and tool results are left out.
func searchText(blocks []seam.Block) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == seam.BlockText {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
