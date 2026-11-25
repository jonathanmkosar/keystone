package builtin

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
)

// Search finds pages and sources by words, and pages by a vector.
//
// Words: a page or a source is found when it holds any word of the query,
// in its title or name or in its text. Words are stemmed. The more of the
// query's words a thing holds, and the rarer they are, the higher it scores:
// the score is SQLite's own ranking, turned so that higher is better.
//
// A vector: a page is found when its stored vector is of the query's model
// and size and points the same way at all. Its score is the cosine between
// the two, from just above 0 to 1.
//
// Both: a page found both ways scores the sum.
func (s *store) Search(ctx context.Context, q knowledge.Query) ([]knowledge.Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(q.Vector) > 0 {
		if err := checkVector(q.Model, q.Vector); err != nil {
			return nil, err
		}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = s.lim.searchResults
	}
	match := matchQuery(q.Text)
	if match == "" && len(q.Vector) == 0 {
		return nil, nil
	}

	var hits []knowledge.Hit
	pageAt := map[int64]int{} // a page's row number to its place in hits
	db := s.t.DB()

	if match != "" {
		rows, err := db.QueryContext(ctx, `
			SELECT p.id, p.space, COALESCE(sp.label, ''), p.path, p.title,
				snippet(knowledge_page_search, 1, '', '', '…', ?1), -bm25(knowledge_page_search)
			FROM knowledge_page_search
				JOIN knowledge_pages p ON p.id = knowledge_page_search.rowid
				LEFT JOIN knowledge_spaces sp ON sp.name = p.space
			WHERE knowledge_page_search MATCH ?2 AND (?3 = '' OR p.space = ?3)
			ORDER BY bm25(knowledge_page_search), p.id LIMIT ?4`,
			s.lim.snippetWords, match, q.Space, limit)
		if err != nil {
			return nil, fail(ctx, err)
		}
		err = eachRow(rows, func() error {
			h := knowledge.Hit{Type: knowledge.HitPage}
			var id int64
			var label string
			if err := rows.Scan(&id, &h.Space, &label, &h.Path, &h.Title, &h.Snippet, &h.Score); err != nil {
				return err
			}
			h.ID, h.Label = pageID(id), labelOf(label)
			pageAt[id] = len(hits)
			hits = append(hits, h)
			return nil
		})
		if err != nil {
			return nil, fail(ctx, err)
		}

		rows, err = db.QueryContext(ctx, `
			SELECT s.id, s.space, COALESCE(sp.label, ''), s.name,
				snippet(knowledge_source_search, 1, '', '', '…', ?1), -bm25(knowledge_source_search)
			FROM knowledge_source_search
				JOIN knowledge_sources s ON s.id = knowledge_source_search.rowid
				LEFT JOIN knowledge_spaces sp ON sp.name = s.space
			WHERE knowledge_source_search MATCH ?2 AND (?3 = '' OR s.space = ?3)
			ORDER BY bm25(knowledge_source_search), s.id LIMIT ?4`,
			s.lim.snippetWords, match, q.Space, limit)
		if err != nil {
			return nil, fail(ctx, err)
		}
		err = eachRow(rows, func() error {
			h := knowledge.Hit{Type: knowledge.HitSource}
			var id int64
			var label string
			if err := rows.Scan(&id, &h.Space, &label, &h.Title, &h.Snippet, &h.Score); err != nil {
				return err
			}
			h.ID, h.Label = sourceID(id), labelOf(label)
			hits = append(hits, h)
			return nil
		})
		if err != nil {
			return nil, fail(ctx, err)
		}
	}

	if len(q.Vector) > 0 {
		near, err := s.nearest(ctx, q)
		if err != nil {
			return nil, err
		}
		// A page the words found gains its nearness. Of the rest, the
		// nearest are added, up to the limit.
		added := 0
		for _, n := range near {
			if i, found := pageAt[n.page]; found {
				hits[i].Score += n.cosine
				continue
			}
			if added == limit {
				continue
			}
			h, err := s.pageHit(ctx, n.page)
			if errors.Is(err, knowledge.ErrNotFound) {
				continue // removed since the vectors were read
			}
			if err != nil {
				return nil, err
			}
			h.Score = n.cosine
			hits = append(hits, h)
			added++
		}
	}

	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.ID < b.ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// eachRow calls scan for every row and closes the rows.
func eachRow(rows *sql.Rows, scan func() error) error {
	defer rows.Close()
	for rows.Next() {
		if err := scan(); err != nil {
			return err
		}
	}
	return rows.Err()
}

// matchQuery turns plain words into a full-text query that matches any of
// them. Each word is quoted, so nothing in the text is read as search
// syntax. Text with no letter or digit in it gives no query.
func matchQuery(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := map[string]bool{}
	var terms []string
	for _, w := range words {
		if seen[w] {
			continue
		}
		seen[w] = true
		terms = append(terms, `"`+w+`"`)
	}
	return strings.Join(terms, " OR ")
}

// near is one page and how near its vector is to the query's.
type near struct {
	page   int64
	cosine float64
}

// nearest returns the pages whose vector is of the query's model and size
// and has a cosine above zero with the query's, nearest first. The vectors
// are read and compared here, one by one: a person's store is small enough
// that an index would cost more than it saves.
func (s *store) nearest(ctx context.Context, q knowledge.Query) ([]near, error) {
	rows, err := s.t.DB().QueryContext(ctx, `
		SELECT v.page_id, v.vector
		FROM knowledge_page_vectors v JOIN knowledge_pages p ON p.id = v.page_id
		WHERE v.model = ?1 AND v.size = ?2 AND (?3 = '' OR p.space = ?3)`,
		q.Model, len(q.Vector), q.Space)
	if err != nil {
		return nil, fail(ctx, err)
	}
	var out []near
	err = eachRow(rows, func() error {
		var n near
		var raw []byte
		if err := rows.Scan(&n.page, &raw); err != nil {
			return err
		}
		vec, err := decodeVector(raw)
		if err != nil {
			return fmt.Errorf("the vector of page %s: %w", pageID(n.page), err)
		}
		if n.cosine = cosine(q.Vector, vec); n.cosine > 0 {
			out = append(out, n)
		}
		return nil
	})
	if err != nil {
		return nil, fail(ctx, err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].cosine != out[j].cosine {
			return out[i].cosine > out[j].cosine
		}
		return out[i].page < out[j].page
	})
	return out, nil
}

// pageHit is the hit for a page the vector found and the words did not. Its
// snippet is the start of the page.
func (s *store) pageHit(ctx context.Context, n int64) (knowledge.Hit, error) {
	p, err := readPage(ctx, s.t.DB(), n)
	if err != nil {
		return knowledge.Hit{}, err
	}
	snippet := p.Body
	if words := strings.Fields(p.Body); len(words) > s.lim.snippetWords {
		snippet = strings.Join(words[:s.lim.snippetWords], " ") + "…"
	}
	return knowledge.Hit{Type: knowledge.HitPage, ID: p.ID, Space: p.Space, Label: p.Label, Path: p.Path, Title: p.Title, Snippet: snippet}, nil
}

func (s *store) VectorsNeeded(ctx context.Context, limit int) ([]knowledge.PageNeed, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = s.lim.vectorsNeeded
	}
	rows, err := s.t.DB().QueryContext(ctx, `
		SELECT p.id, p.space, COALESCE(sp.label, ''), p.title, v.page_id IS NOT NULL
		FROM knowledge_pages p
			LEFT JOIN knowledge_page_vectors v ON v.page_id = p.id
			LEFT JOIN knowledge_spaces sp ON sp.name = p.space
		WHERE v.page_id IS NULL OR v.revision <> p.revision
		ORDER BY p.space, p.path LIMIT ?`, limit)
	if err != nil {
		return nil, fail(ctx, err)
	}
	var out []knowledge.PageNeed
	err = eachRow(rows, func() error {
		var n knowledge.PageNeed
		var id int64
		var label string
		if err := rows.Scan(&id, &n.Space, &label, &n.Title, &n.Stale); err != nil {
			return err
		}
		n.PageID, n.Label = pageID(id), labelOf(label)
		out = append(out, n)
		return nil
	})
	if err != nil {
		return nil, fail(ctx, err)
	}
	return out, nil
}

func (s *store) PutVector(ctx context.Context, v knowledge.PageVector) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkVector(v.Model, v.Vector); err != nil {
		return err
	}
	n, ok := rowOf(pagePrefix, v.PageID)
	if !ok {
		return fmt.Errorf("%w: page %q", knowledge.ErrNotFound, v.PageID)
	}
	return inTx(ctx, s.t, func(tx *sql.Tx) error {
		var revision int64
		err := tx.QueryRowContext(ctx, "SELECT revision FROM knowledge_pages WHERE id = ?", n).Scan(&revision)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: page %q", knowledge.ErrNotFound, v.PageID)
		}
		if err != nil {
			return fail(ctx, err)
		}
		if v.Revision != strconv.FormatInt(revision, 10) {
			return fmt.Errorf("%w: page %q is at revision %d, and the vector was made from %q", knowledge.ErrStale, v.PageID, revision, v.Revision)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_page_vectors(page_id, revision, model, size, vector) VALUES(?, ?, ?, ?, ?)
			ON CONFLICT(page_id) DO UPDATE SET revision = excluded.revision, model = excluded.model, size = excluded.size, vector = excluded.vector`,
			n, revision, v.Model, len(v.Vector), encodeVector(v.Vector))
		if err != nil {
			return fail(ctx, err)
		}
		return nil
	})
}

// checkVector refuses a vector the store cannot compare: one with no model,
// no numbers, a number that is not finite, or no direction.
func checkVector(model string, vec []float32) error {
	if strings.TrimSpace(model) == "" {
		return invalid("a vector needs the name of the model that made it")
	}
	if len(vec) == 0 {
		return invalid("a vector needs at least one number")
	}
	zero := true
	for _, x := range vec {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return invalid("a vector's numbers must be finite")
		}
		if x != 0 {
			zero = false
		}
	}
	if zero {
		return invalid("a vector of zeros points nowhere")
	}
	return nil
}

// A vector is stored as its numbers in order, four bytes each, low byte
// first.
func encodeVector(vec []float32) []byte {
	raw := make([]byte, 4*len(vec))
	for i, x := range vec {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(x))
	}
	return raw
}

func decodeVector(raw []byte) ([]float32, error) {
	if len(raw) == 0 || len(raw)%4 != 0 {
		return nil, fmt.Errorf("%d bytes is not a whole number of 32-bit floats", len(raw))
	}
	vec := make([]float32, len(raw)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return vec, nil
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}
