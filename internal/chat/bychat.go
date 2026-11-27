package chat

import (
	"strings"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The search by chat (P4-06), for the table of every chat. Search returns
// hits, at most a page of them: several may be in one chat, and a chat past
// the limit is not found at all. The table needs the chats: every one that
// matches, once each, so they can be counted, paged, filtered and selected.
// SearchByChat reads the same two full-text tables with the same reading of
// a query (matchQuery), and Excerpts cuts what the screen shows of the ones
// on a page. docs/architecture/manage-chats.md is the whole of it.

// Found is what a search found in one chat.
type Found struct {
	ChatID string
	// Title says the chat's title holds every word.
	Title bool
	// Messages is how many of its messages hold every word, on every branch.
	Messages int
	// MessageID is the best of them. Zero when only the title matched.
	MessageID int64
}

// SearchByChat finds every chat whose title, or one of whose messages on any
// branch, holds every word of query, a word also matching as the start of a
// longer one, as Search does. Each chat is returned once, the best match
// first: the chats whose title matched, by the title's rank and then the
// newest, and after them the rest by their best message's rank. A query
// with no word finds nothing.
func SearchByChat(s *tenant.Store, query string) ([]Found, error) {
	match := matchQuery(query)
	if match == "" {
		return nil, nil
	}
	at := map[string]int{}
	var found []Found
	rows, err := s.DB().Query(`
		SELECT c.id FROM chat_title_search JOIN chats c ON c.seq = chat_title_search.rowid
		WHERE chat_title_search MATCH ? ORDER BY rank, c.active_at DESC, c.seq DESC`, match)
	if err != nil {
		return nil, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		at[id] = len(found)
		found = append(found, Found{ChatID: id, Title: true})
	}
	// The best message of each chat is the first of its rows, and a chat
	// only a message found comes after the titles, in the order its best
	// message came.
	rows, err = s.DB().Query(`
		SELECT m.chat_id, m.id FROM message_search JOIN messages m ON m.id = message_search.rowid
		WHERE message_search MATCH ? ORDER BY rank, m.id DESC`, match)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var chatID string
		var messageID int64
		if err := rows.Scan(&chatID, &messageID); err != nil {
			return nil, err
		}
		i, ok := at[chatID]
		if !ok {
			i = len(found)
			at[chatID] = i
			found = append(found, Found{ChatID: chatID})
		}
		if found[i].Messages == 0 {
			found[i].MessageID = messageID
		}
		found[i].Messages++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return found, nil
}

func scanIDs(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Part is a piece of a title or of a message's text. Match marks the words
// a search found.
type Part struct {
	Text  string `json:"text"`
	Match bool   `json:"match,omitempty"`
}

// Excerpt is what a search shows of one chat: its title with the words
// marked, when the title matched, and the words around the match in its
// best message, when a message did.
type Excerpt struct {
	Title []Part
	// MessageID and Role are the best message's. Snippet is about
	// Limits.SnippetWords words of it around the match.
	MessageID int64
	Role      seam.Role
	Snippet   []Part
}

// The marks the full-text index puts round a matching word, taken out again
// by parts. They are control characters no title holds (Rename folds every
// run of white space) and a message is most unlikely to.
const (
	markOpen  = "\x02"
	markClose = "\x03"
)

// parts cuts marked text into its pieces. A mark with no partner is dropped.
func parts(marked string) []Part {
	var out []Part
	add := func(text string, match bool) {
		text = strings.NewReplacer(markOpen, "", markClose, "").Replace(text)
		if text == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].Match == match {
			out[n-1].Text += text
			return
		}
		out = append(out, Part{Text: text, Match: match})
	}
	for marked != "" {
		open := strings.Index(marked, markOpen)
		if open < 0 {
			add(marked, false)
			break
		}
		add(marked[:open], false)
		rest := marked[open+len(markOpen):]
		end := strings.Index(rest, markClose)
		if end < 0 {
			add(rest, false)
			break
		}
		add(rest[:end], true)
		marked = rest[end+len(markClose):]
	}
	return out
}

// Excerpts returns what a search for query shows of each chat in found, by
// the chat's id. found is what SearchByChat returned for the same query, or
// the part of it a page shows: only those are read.
func Excerpts(s *tenant.Store, lim Limits, query string, found []Found) (map[string]Excerpt, error) {
	if lim.SnippetWords < 1 || lim.SnippetWords > 64 {
		return nil, invalid("a snippet is 1 to 64 words")
	}
	out := map[string]Excerpt{}
	match := matchQuery(query)
	if match == "" || len(found) == 0 {
		return out, nil
	}
	var titled, messages []any
	for _, f := range found {
		if f.Title {
			titled = append(titled, f.ChatID)
		}
		if f.MessageID != 0 {
			messages = append(messages, f.MessageID)
		}
	}
	in := func(n int) string { return "(?" + strings.Repeat(", ?", n-1) + ")" }
	if len(titled) > 0 {
		args := append([]any{markOpen, markClose, match}, titled...)
		rows, err := s.DB().Query(`
			SELECT c.id, highlight(chat_title_search, 0, ?, ?)
			FROM chat_title_search JOIN chats c ON c.seq = chat_title_search.rowid
			WHERE chat_title_search MATCH ? AND c.id IN `+in(len(titled)), args...)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var id, marked string
				if err := rows.Scan(&id, &marked); err != nil {
					return err
				}
				e := out[id]
				e.Title = parts(marked)
				out[id] = e
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, err
		}
	}
	if len(messages) > 0 {
		args := append([]any{markOpen, markClose, lim.SnippetWords, match}, messages...)
		rows, err := s.DB().Query(`
			SELECT m.chat_id, m.id, m.role, snippet(message_search, 0, ?, ?, '…', ?)
			FROM message_search JOIN messages m ON m.id = message_search.rowid
			WHERE message_search MATCH ? AND message_search.rowid IN `+in(len(messages)), args...)
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var id, marked string
				var messageID int64
				var role seam.Role
				if err := rows.Scan(&id, &messageID, &role, &marked); err != nil {
					return err
				}
				e := out[id]
				e.MessageID, e.Role = messageID, role
				// A message's line breaks read as spaces in one line of a table.
				e.Snippet = parts(strings.Join(strings.Fields(marked), " "))
				out[id] = e
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
