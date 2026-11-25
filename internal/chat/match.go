package chat

import (
	"strings"
	"time"
	"unicode"

	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Match is one row of a search for any of a query's words: a chat's title,
// or one of its messages. The past-chat search (P5-05) ranks chats by them.
type Match struct {
	ChatID string
	// MessageID is the message that matched. Zero means the title matched.
	MessageID int64
	// Role is who wrote the message. Empty when the title matched.
	Role seam.Role
	// Snippet is the title, or the words of the message around the match.
	Snippet string
	// When is the message's time, or the chat's last activity for a title.
	When time.Time
	// Score is the full-text rank: lower is a better match.
	Score float64
}

// MatchAny finds the titles and the messages, on every branch, that hold
// any of the words of query, each list the best first and at most most
// long. A word also matches as the start of a longer one, and a row that
// holds more of the words ranks higher. Search, by contrast, wants every
// word in one title or one message.
func MatchAny(s *tenant.Store, query string, most, snippetWords int) (titles, messages []Match, err error) {
	if most < 1 || snippetWords < 1 || snippetWords > 64 {
		return nil, nil, invalid("a search needs a result limit of at least 1 and a snippet of 1 to 64 words")
	}
	match := matchAnyQuery(query)
	if match == "" {
		return nil, nil, nil
	}
	rows, err := s.DB().Query(`
		SELECT c.id, 0, '', c.title, c.active_at, bm25(chat_title_search)
		FROM chat_title_search JOIN chats c ON c.seq = chat_title_search.rowid
		WHERE chat_title_search MATCH ? ORDER BY bm25(chat_title_search), c.active_at DESC LIMIT ?`,
		match, most)
	if err != nil {
		return nil, nil, err
	}
	if titles, err = scanMatches(rows); err != nil {
		return nil, nil, err
	}
	rows, err = s.DB().Query(`
		SELECT m.chat_id, m.id, m.role, snippet(message_search, 0, '', '', '…', ?), m.created_at, bm25(message_search)
		FROM message_search JOIN messages m ON m.id = message_search.rowid
		WHERE message_search MATCH ? ORDER BY bm25(message_search), m.id DESC LIMIT ?`,
		snippetWords, match, most)
	if err != nil {
		return nil, nil, err
	}
	if messages, err = scanMatches(rows); err != nil {
		return nil, nil, err
	}
	return titles, messages, nil
}

func scanMatches(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]Match, error) {
	defer rows.Close()
	var out []Match
	for rows.Next() {
		var m Match
		var when string
		if err := rows.Scan(&m.ChatID, &m.MessageID, &m.Role, &m.Snippet, &when, &m.Score); err != nil {
			return nil, err
		}
		var err error
		if m.When, err = parseTime(when); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// matchAnyQuery is matchQuery with OR between the words: each word quoted,
// so nothing typed is read as search syntax, and matching as a prefix. A
// word with no letter or digit in it is dropped, and so is a word said
// twice.
func matchAnyQuery(query string) string {
	seen := map[string]bool{}
	var terms []string
	for _, w := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if seen[w] {
			continue
		}
		seen[w] = true
		terms = append(terms, `"`+w+`"*`)
	}
	return strings.Join(terms, " OR ")
}
