package recap

import (
	"context"
	"strings"
	"unicode"

	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Hit is one chat whose recap holds a word of a search.
type Hit struct {
	ChatID string
	// Score is the full-text rank: lower is a better match.
	Score float64
}

// Search finds the chats whose recap, its summary, topics or decisions,
// holds any of the words of query, the best first, at most most of them.
// A word also matches as the start of a longer one. Text with no letter or
// digit in it finds nothing.
//
// It finds sensitive recaps too. What may be given to a call is decided by
// the caller, chat by chat, with Sensitive and MayShow.
func Search(ctx context.Context, s *tenant.Store, query string, most int) ([]Hit, error) {
	match := MatchAny(query)
	if match == "" || most < 1 {
		return nil, nil
	}
	rows, err := s.DB().QueryContext(ctx, `
		SELECT r.chat_id, bm25(chat_recap_search)
		FROM chat_recap_search JOIN chat_recaps r ON r.rowid = chat_recap_search.rowid
		WHERE chat_recap_search MATCH ? ORDER BY bm25(chat_recap_search) LIMIT ?`, match, most)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ChatID, &h.Score); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// MatchAny turns what a model or a person typed into a full-text query that
// matches any of its words. Each word is quoted, so nothing typed is read
// as search syntax, and matches as a prefix. A word with no letter or digit
// in it is dropped, and so is a word said twice.
func MatchAny(query string) string {
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

// Sensitive says whether what a chat holds is sensitive: the chat was sent
// content from a sensitive space (recall.Held), or its recap was made
// while it was. A mark that cannot be read counts as sensitive, and the
// error says why.
func Sensitive(ctx context.Context, s *tenant.Store, chatID string) (bool, error) {
	held, err := recall.Held(ctx, s, chatID)
	if err != nil {
		return true, err
	}
	if held {
		return true, nil
	}
	var marked bool
	err = s.DB().QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM chat_recaps WHERE chat_id = ? AND sensitive = 1)", chatID).Scan(&marked)
	if err != nil {
		return true, err
	}
	return marked, nil
}

// MayShow is fixed rule 3 for past chats: whether anything of a chat, its
// title, its recap or its messages, may be given to a call. A chat that
// holds sensitive content may be given only to a call whose every model
// may read such content. mayReadSensitive is that answer for the least of
// the call's route: models.Model.MayReadSensitive for each of its models.
func MayShow(sensitive, mayReadSensitive bool) bool {
	return !sensitive || mayReadSensitive
}
