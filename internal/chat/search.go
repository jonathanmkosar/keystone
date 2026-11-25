package chat

import (
	"strings"
	"time"
	"unicode"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Hit is one search result: a chat whose title matched, or one message
// whose text matched.
type Hit struct {
	ChatID string `json:"chat_id"`
	Title  string `json:"title"`
	// MessageID is the message that matched. Zero means the title matched.
	MessageID int64 `json:"message_id,omitempty"`
	// Role is who wrote the message. Empty when the title matched.
	Role seam.Role `json:"role,omitempty"`
	// Snippet is the title, or the part of the message around the match.
	Snippet string `json:"snippet"`
	// When is the message's time, or the chat's last activity when the
	// title matched.
	When time.Time `json:"when"`
}

// Search finds chats by the words of their titles and of their messages'
// text, on every branch. Every word of the query must be there, and a word
// also matches as the start of a longer one. Title matches come first, then
// messages, each with the best match first.
//
// Select opens a chat at a message that is not on its current branch.
func Search(s *tenant.Store, lim Limits, query string) ([]Hit, error) {
	if lim.SearchResults < 1 || lim.SnippetWords < 1 || lim.SnippetWords > 64 {
		return nil, invalid("search needs a result limit of at least 1 and a snippet of 1 to 64 words")
	}
	match := matchQuery(query)
	if match == "" {
		return nil, nil
	}
	var hits []Hit
	rows, err := s.DB().Query(`
		SELECT c.id, c.title, 0, '', c.title, c.active_at
		FROM chat_title_search JOIN chats c ON c.seq = chat_title_search.rowid
		WHERE chat_title_search MATCH ? ORDER BY rank, c.active_at DESC LIMIT ?`,
		match, lim.SearchResults)
	if err != nil {
		return nil, err
	}
	if hits, err = scanHits(rows, hits); err != nil || len(hits) >= lim.SearchResults {
		return hits, err
	}
	rows, err = s.DB().Query(`
		SELECT c.id, c.title, m.id, m.role, snippet(message_search, 0, '', '', '…', ?), m.created_at
		FROM message_search JOIN messages m ON m.id = message_search.rowid JOIN chats c ON c.id = m.chat_id
		WHERE message_search MATCH ? ORDER BY rank, m.id DESC LIMIT ?`,
		lim.SnippetWords, match, lim.SearchResults-len(hits))
	if err != nil {
		return nil, err
	}
	return scanHits(rows, hits)
}

func scanHits(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}, hits []Hit) ([]Hit, error) {
	defer rows.Close()
	for rows.Next() {
		var h Hit
		var when string
		if err := rows.Scan(&h.ChatID, &h.Title, &h.MessageID, &h.Role, &h.Snippet, &when); err != nil {
			return nil, err
		}
		var err error
		if h.When, err = parseTime(when); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// matchQuery turns what a person typed into a full-text query. Each word
// is quoted, so nothing typed is read as search syntax, and matches as a
// prefix. A word with no letter or digit in it is dropped.
func matchQuery(query string) string {
	var terms []string
	for _, word := range strings.Fields(query) {
		if !strings.ContainsFunc(word, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(word, `"`, `""`)+`"*`)
	}
	return strings.Join(terms, " ")
}
