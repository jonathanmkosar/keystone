package chat

import (
	"sort"
	"strings"
)

// How a search of messages stays quick over years of chats (P13-03, the
// load test; decision 0530).
//
// Ranking a match costs a little for every message that matches, and
// showing one costs more: its chat is read, and the words around the match
// are cut out of the message. A common word matches most of what a person
// ever wrote. A search that ranked every match, read its chat and cut its
// snippet before keeping the best few took seconds over a few hundred
// thousand messages. So:
//
//   - Only the matches that are given are cut to a snippet, and only their
//     chats are read: the index is asked for its best few in its own
//     order, which it walks without touching the rest (topMessages).
//   - At most rankMax matches are ranked. When more match, the search is
//     too wide for a rank to mean much, and the newest matches are given,
//     which the index reaches from its newest end.
//   - In a search for any of several words, a word that more than rankMax
//     messages hold is left out while another word is in fewer (rarer):
//     the rarer words say which chats are meant.
//
// A rankMax below 1 ranks every match, as a small record always is.

// ranked is one message a search found: its id, the words around the
// match, and its rank, where lower is better.
type ranked struct {
	id      int64
	snippet string
	score   float64
}

// matches counts the messages that match, up to most.
func matches(q queryer, match string, most int) (int, error) {
	var n int
	err := q.QueryRow(`SELECT count(*) FROM (
		SELECT rowid FROM message_search WHERE message_search MATCH ? LIMIT ?)`, match, most).Scan(&n)
	return n, err
}

// topMessages returns the messages that match, at most most of them: the
// best first and, among equals, the newest first; or, when more than
// rankMax match, the newest first.
func topMessages(q queryer, match string, snippetWords, most, rankMax int) ([]ranked, error) {
	// ORDER BY rank alone, or the row's id alone, is an order the index
	// keeps itself: it steps to the rows asked for and no further, so the
	// snippet is cut for those only.
	order, byRank := "rank", true
	if rankMax > 0 {
		n, err := matches(q, match, rankMax+1)
		if err != nil {
			return nil, err
		}
		if n > rankMax {
			order, byRank = "rowid DESC", false
		}
	}
	rows, err := q.Query(`SELECT rowid, snippet(message_search, 0, '', '', '…', ?), rank
		FROM message_search WHERE message_search MATCH ? ORDER BY `+order+` LIMIT ?`, snippetWords, match, most)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ranked
	for rows.Next() {
		var r ranked
		if err := rows.Scan(&r.id, &r.snippet, &r.score); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if byRank {
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].score != out[j].score {
				return out[i].score < out[j].score
			}
			return out[i].id > out[j].id
		})
	}
	return out, nil
}

// among is the part of a query that keeps it to the messages found:
// "m.id IN (?, ...)" and its arguments.
func among(found []ranked) (string, []any) {
	args := make([]any, len(found))
	for i, r := range found {
		args[i] = r.id
	}
	return "m.id IN (?" + strings.Repeat(", ?", len(found)-1) + ")", args
}

// rarer returns the terms of a search for any word that are worth ranking
// by: every term, unless some are held by more than rankMax messages and
// others by fewer, when only the others are.
func rarer(q queryer, terms []string, rankMax int) ([]string, error) {
	if rankMax < 1 || len(terms) < 2 {
		return terms, nil
	}
	var few []string
	for _, term := range terms {
		n, err := matches(q, term, rankMax+1)
		if err != nil {
			return nil, err
		}
		if n <= rankMax {
			few = append(few, term)
		}
	}
	if len(few) == 0 {
		return terms, nil
	}
	return few, nil
}
