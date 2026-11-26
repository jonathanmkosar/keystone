// Package chat is the chat record: chats, their messages and branches,
// titles, stars, search and deletion.
//
// Every function works on one tenant's store, so it can only reach that
// tenant's chats. There are no routes here. The API and the turn runner
// call these functions.
//
// Messages are a tree. Each message names its parent, and each parent
// remembers its current child. An edit or a retry is a new message under
// the same parent, so nothing is ever overwritten. The current branch is
// the path that follows the current children from the first message down.
package chat

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

var (
	// ErrNotFound says the chat or the message is not in this tenant's record.
	ErrNotFound = errors.New("not found")
	// ErrInvalid says the caller's input was refused. The rest of the
	// error says why.
	ErrInvalid = errors.New("refused")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// now is the clock. Tests replace it.
var now = time.Now

// Times are stored as text of one fixed width, so they sort as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

func stamp() string { return now().UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) { return time.Parse(timeLayout, s) }

// Limits are the values of the chats lane's settings that this package
// reads. The caller passes them in, so a person's own values can be used
// once stored settings exist.
type Limits struct {
	// TitleLength is the most characters a title may have:
	// chats.title_max_length.
	TitleLength int
	// SearchResults is the most results one search returns:
	// chats.search_max_results.
	SearchResults int
	// SnippetWords is about how many words of a matching message a result
	// shows, from 1 to 64: chats.search_snippet_words.
	SnippetWords int
}

// DefaultLimits reads the limits from the settings registry's defaults.
func DefaultLimits() (Limits, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Limits{}, err
	}
	var lim Limits
	for key, into := range map[string]*int{
		"chats.title_max_length":     &lim.TitleLength,
		"chats.search_max_results":   &lim.SearchResults,
		"chats.search_snippet_words": &lim.SnippetWords,
	} {
		s, ok := settings.Lookup(reg, key)
		if !ok {
			return Limits{}, fmt.Errorf("the %s setting is missing from the registry", key)
		}
		if *into, err = strconv.Atoi(s.Default); err != nil {
			return Limits{}, fmt.Errorf("%s: %w", key, err)
		}
	}
	return lim, nil
}

// Chat is one conversation.
type Chat struct {
	ID string `json:"id"`
	// Title is empty until the chat is given one.
	Title   string    `json:"title"`
	Starred bool      `json:"starred"`
	Created time.Time `json:"created"`
	// Active is when the last message was written, or when the chat was
	// made if it has none.
	Active time.Time `json:"active"`
}

// Filter narrows List.
type Filter struct {
	// Starred keeps starred chats only.
	Starred bool
	// Limit is the most chats to return. Zero means all of them.
	Limit int
	// Offset skips that many chats from the top of the list.
	Offset int
}

// queryer is what *sql.DB and *sql.Tx share.
type queryer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// inTx runs f in one transaction, and rolls back if f fails.
func inTx(s *tenant.Store, f func(tx *sql.Tx) error) error {
	tx, err := s.DB().Begin()
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

const chatColumns = "id, title, starred, created_at, active_at"

func scanChat(row interface{ Scan(...any) error }) (Chat, error) {
	var c Chat
	var created, active string
	if err := row.Scan(&c.ID, &c.Title, &c.Starred, &created, &active); err != nil {
		return Chat{}, err
	}
	var err error
	if c.Created, err = parseTime(created); err != nil {
		return Chat{}, fmt.Errorf("chat %s: %w", c.ID, err)
	}
	if c.Active, err = parseTime(active); err != nil {
		return Chat{}, fmt.Errorf("chat %s: %w", c.ID, err)
	}
	return c, nil
}

// noChat turns "no such row" into ErrNotFound for the chat.
func noChat(chatID string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("chat %q: %w", chatID, ErrNotFound)
	}
	return err
}

// Create makes a chat with no title and no messages.
func Create(s *tenant.Store) (Chat, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Chat{}, err
	}
	id, at := hex.EncodeToString(b[:]), stamp()
	return scanChat(s.DB().QueryRow(
		"INSERT INTO chats(id, created_at, active_at) VALUES(?, ?, ?) RETURNING "+chatColumns, id, at, at))
}

// Get returns one chat.
func Get(s *tenant.Store, chatID string) (Chat, error) {
	c, err := scanChat(s.DB().QueryRow("SELECT "+chatColumns+" FROM chats WHERE id = ?", chatID))
	return c, noChat(chatID, err)
}

// List returns the tenant's chats, the one with the newest message first.
func List(s *tenant.Store, f Filter) ([]Chat, error) {
	if f.Limit < 0 || f.Offset < 0 {
		return nil, invalid("a limit or an offset cannot be negative")
	}
	q := "SELECT " + chatColumns + " FROM chats"
	if f.Starred {
		q += " WHERE starred = 1"
	}
	limit := f.Limit
	if limit == 0 {
		limit = -1 // SQLite reads a negative limit as no limit
	}
	rows, err := s.DB().Query(q+" ORDER BY active_at DESC, seq DESC LIMIT ? OFFSET ?", limit, f.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chats []Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		chats = append(chats, c)
	}
	return chats, rows.Err()
}

// Rename gives a chat its title. Runs of spaces and line breaks become one
// space. An empty title, or one longer than the limit, is refused.
func Rename(s *tenant.Store, lim Limits, chatID, title string) (Chat, error) {
	title = strings.Join(strings.Fields(title), " ")
	if n := utf8.RuneCountInString(title); n == 0 || n > lim.TitleLength {
		return Chat{}, invalid("a title is 1 to %d characters", lim.TitleLength)
	}
	var c Chat
	err := inTx(s, func(tx *sql.Tx) error {
		var seq int64
		if err := tx.QueryRow("UPDATE chats SET title = ? WHERE id = ? RETURNING seq", title, chatID).Scan(&seq); err != nil {
			return noChat(chatID, err)
		}
		if _, err := tx.Exec("DELETE FROM chat_title_search WHERE rowid = ?", seq); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO chat_title_search(rowid, title) VALUES(?, ?)", seq, title); err != nil {
			return err
		}
		var err error
		c, err = scanChat(tx.QueryRow("SELECT "+chatColumns+" FROM chats WHERE id = ?", chatID))
		return err
	})
	return c, err
}

// Star sets or clears a chat's star.
func Star(s *tenant.Store, chatID string, starred bool) (Chat, error) {
	c, err := scanChat(s.DB().QueryRow(
		"UPDATE chats SET starred = ? WHERE id = ? RETURNING "+chatColumns, starred, chatID))
	return c, noChat(chatID, err)
}

// Delete removes a chat: its messages, its search rows, the rows of any
// later table that refers to it with ON DELETE CASCADE, and each of its
// files that no other chat holds.
//
// The files go before the transaction commits. If removing one fails, the
// chat stays and Delete can be run again, so a file is never left behind
// with nothing that names it.
func Delete(s *tenant.Store, chatID string) error {
	return inTx(s, func(tx *sql.Tx) error {
		names, err := textColumn(tx, `SELECT name FROM chat_files WHERE chat_id = ?1
			AND name NOT IN (SELECT name FROM chat_files WHERE chat_id <> ?1)
			AND name NOT IN (SELECT hash FROM project_files)`, chatID) // nor a project's knowledge file (P9-01)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM message_search WHERE rowid IN (SELECT id FROM messages WHERE chat_id = ?)", chatID); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM chat_title_search WHERE rowid = (SELECT seq FROM chats WHERE id = ?)", chatID); err != nil {
			return err
		}
		res, err := tx.Exec("DELETE FROM chats WHERE id = ?", chatID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return noChat(chatID, sql.ErrNoRows)
		}
		for _, name := range names {
			if err := s.Files().Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		return nil
	})
}

// AddFile ties a file in the tenant's files folder to a chat, so that
// deleting the chat removes the file. Append does this for every file a
// message refers to. Tying a file twice does nothing.
func AddFile(s *tenant.Store, chatID, name string) error {
	if err := checkFileName(name); err != nil {
		return err
	}
	return inTx(s, func(tx *sql.Tx) error {
		var one int
		if err := tx.QueryRow("SELECT 1 FROM chats WHERE id = ?", chatID).Scan(&one); err != nil {
			return noChat(chatID, err)
		}
		_, err := tx.Exec("INSERT OR IGNORE INTO chat_files(chat_id, name) VALUES(?, ?)", chatID, name)
		return err
	})
}

// Files lists the files a chat holds, by name.
func Files(s *tenant.Store, chatID string) ([]string, error) {
	if _, err := Get(s, chatID); err != nil {
		return nil, err
	}
	names, err := textColumn(s.DB(), "SELECT name FROM chat_files WHERE chat_id = ?", chatID)
	sort.Strings(names)
	return names, err
}

// checkFileName refuses anything that is not a plain path inside the files
// folder. The folder itself refuses a path that leaves it; this keeps such
// a name out of the record in the first place.
func checkFileName(name string) error {
	if name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, `\:`) {
		return invalid("%q is not the name of a file in the files folder", name)
	}
	return nil
}

// textColumn runs a query that returns one text column.
func textColumn(q queryer, query string, args ...any) ([]string, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
