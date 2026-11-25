// Package builtin is the knowledge store Keystone keeps by itself, for a
// person who wants memory and runs no other store. It is the adapter named
// "built-in".
//
// Everything is in the tenant's own database: spaces and their labels,
// pages as Markdown text, sources, the vector of each page, and a full-text
// index. The package reaches that database only through the tenant's store
// handle, so it can touch one tenant's knowledge and nobody else's.
//
// A space's label is read in the same query as the content, on every call.
// A space with no label, or with any label but "normal", reads as sensitive.
//
// The Store interface has no call that writes a page or labels a space, so
// those are plain functions here: WritePage, RemovePage, AddText and
// SetLabel. They are for the test tenant's loader, an import, the wiki
// upkeep jobs and the memory screens.
package builtin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// Name is the adapter's name: a value of the knowledge.adapter setting.
const Name = "built-in"

// The settings this adapter reads when a store is opened.
const (
	SettingSearchResults = "knowledge.builtin_search_max_results"
	SettingSnippetWords  = "knowledge.builtin_snippet_words"
	SettingVectorsNeeded = "knowledge.builtin_vectors_needed_max"
)

func init() { knowledge.Register(Name, open) }

// limits are the settings' values, read once when the store is opened.
type limits struct {
	searchResults int
	snippetWords  int
	vectorsNeeded int
}

// store is one tenant's built-in store. It holds the tenant's handle and
// no content: every call reads the database.
type store struct {
	t   *tenant.Store
	lim limits
}

func open(ctx context.Context, t *tenant.Store) (knowledge.Store, error) {
	if t == nil {
		return nil, errors.New("the built-in store keeps a tenant's knowledge in that tenant's database, and was given no tenant")
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	var lim limits
	for _, want := range []struct {
		key      string
		into     *int
		min, max int
	}{
		{SettingSearchResults, &lim.searchResults, 1, 1000},
		{SettingSnippetWords, &lim.snippetWords, 1, 64},
		{SettingVectorsNeeded, &lim.vectorsNeeded, 1, 10000},
	} {
		text, err := tenantsettings.Get(ctx, t, reg, want.key)
		if err != nil {
			return nil, fail(ctx, err)
		}
		n, err := strconv.Atoi(text)
		if err != nil || n < want.min || n > want.max {
			return nil, fmt.Errorf("the setting %s is %q, and must be a number from %d to %d", want.key, text, want.min, want.max)
		}
		*want.into = n
	}
	return &store{t: t, lim: lim}, nil
}

// fail turns a database error into the store's answer: the context's own
// error when the context is done, and otherwise "unavailable", so a chat
// carries on without the store.
func fail(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	return fmt.Errorf("the built-in knowledge store: %w: %w", knowledge.ErrUnavailable, err)
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", knowledge.ErrInvalid, fmt.Sprintf(format, args...))
}

// labelOf turns what the spaces table holds into a label. Only the exact
// text "normal" is normal. A space with no row reads as "", and so as
// sensitive.
func labelOf(stored string) knowledge.Label {
	if stored == string(knowledge.LabelNormal) {
		return knowledge.LabelNormal
	}
	return knowledge.LabelSensitive
}

// A page id is "p" and the row's number, and a source id is "s" and the
// row's number, so one is never taken for the other.
const (
	pagePrefix   = "p"
	sourcePrefix = "s"
)

func pageID(n int64) string   { return pagePrefix + strconv.FormatInt(n, 10) }
func sourceID(n int64) string { return sourcePrefix + strconv.FormatInt(n, 10) }

// rowOf returns the row number inside an id, or false when the text is not
// an id this store gave.
func rowOf(prefix, id string) (int64, bool) {
	digits, ok := strings.CutPrefix(id, prefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 1 || strconv.FormatInt(n, 10) != digits {
		return 0, false
	}
	return n, true
}

// querier is a database or a transaction on it.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *store) Capabilities() knowledge.Capabilities {
	return knowledge.Capabilities{AddSource: true, CatalogMarker: true, VectorSearch: true, PageVectors: true}
}

func (s *store) Catalog(ctx context.Context) ([]knowledge.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.t.DB().QueryContext(ctx, `
		SELECT p.id, p.space, COALESCE(sp.label, ''), p.path, p.title, p.kind
		FROM knowledge_pages p LEFT JOIN knowledge_spaces sp ON sp.name = p.space
		ORDER BY p.space, p.path`)
	if err != nil {
		return nil, fail(ctx, err)
	}
	defer rows.Close()
	var out []knowledge.Entry
	for rows.Next() {
		var e knowledge.Entry
		var id int64
		var label string
		if err := rows.Scan(&id, &e.Space, &label, &e.Path, &e.Title, &e.Kind); err != nil {
			return nil, fail(ctx, err)
		}
		e.ID, e.Label = pageID(id), labelOf(label)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fail(ctx, err)
	}
	return out, nil
}

func (s *store) ReadPage(ctx context.Context, id string) (knowledge.Page, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.Page{}, err
	}
	n, ok := rowOf(pagePrefix, id)
	if !ok {
		return knowledge.Page{}, fmt.Errorf("%w: page %q", knowledge.ErrNotFound, id)
	}
	return readPage(ctx, s.t.DB(), n)
}

// readPage reads one page by its row number. The sources it cites come back
// in the same statement, so a page and its citations are of one moment.
func readPage(ctx context.Context, q querier, n int64) (knowledge.Page, error) {
	var p knowledge.Page
	var label, cites string
	var revision int64
	err := q.QueryRowContext(ctx, `
		SELECT p.space, COALESCE(sp.label, ''), p.path, p.title, p.kind, p.body, p.revision,
			COALESCE((SELECT group_concat(source_id, ' ') FROM
				(SELECT source_id FROM knowledge_page_sources WHERE page_id = p.id ORDER BY position)), '')
		FROM knowledge_pages p LEFT JOIN knowledge_spaces sp ON sp.name = p.space
		WHERE p.id = ?`, n).
		Scan(&p.Space, &label, &p.Path, &p.Title, &p.Kind, &p.Body, &revision, &cites)
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.Page{}, fmt.Errorf("%w: page %q", knowledge.ErrNotFound, pageID(n))
	}
	if err != nil {
		return knowledge.Page{}, fail(ctx, err)
	}
	p.ID, p.Label, p.Revision = pageID(n), labelOf(label), strconv.FormatInt(revision, 10)
	for _, c := range strings.Fields(cites) {
		p.Sources = append(p.Sources, sourcePrefix+c)
	}
	return p, nil
}

func (s *store) ReadSource(ctx context.Context, id string) (knowledge.Source, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.Source{}, err
	}
	n, ok := rowOf(sourcePrefix, id)
	if !ok {
		return knowledge.Source{}, fmt.Errorf("%w: source %q", knowledge.ErrNotFound, id)
	}
	return readSource(ctx, s.t.DB(), n)
}

func readSource(ctx context.Context, q querier, n int64) (knowledge.Source, error) {
	var src knowledge.Source
	var label string
	err := q.QueryRowContext(ctx, `
		SELECT s.space, COALESCE(sp.label, ''), s.name, s.origin, s.dated, s.text
		FROM knowledge_sources s LEFT JOIN knowledge_spaces sp ON sp.name = s.space
		WHERE s.id = ?`, n).
		Scan(&src.Space, &label, &src.Name, &src.Origin, &src.Dated, &src.Text)
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.Source{}, fmt.Errorf("%w: source %q", knowledge.ErrNotFound, sourceID(n))
	}
	if err != nil {
		return knowledge.Source{}, fail(ctx, err)
	}
	src.ID, src.Label = sourceID(n), labelOf(label)
	return src, nil
}

func (s *store) AddSource(ctx context.Context, n knowledge.NewSource) (knowledge.Source, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.Source{}, err
	}
	if err := knowledge.CheckNewSource(n); err != nil {
		return knowledge.Source{}, err
	}
	// The facts become the source's text, one to a line, as a Markdown list.
	var text strings.Builder
	for _, f := range n.Facts {
		text.WriteString("- " + strings.TrimSpace(f.Text) + "\n")
	}
	return AddText(ctx, s.t, NewText{Space: n.Space, Name: n.Name, Origin: n.Origin, Dated: n.Dated, Text: text.String()})
}

func (s *store) CatalogMarker(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var changes int64
	err := s.t.DB().QueryRowContext(ctx, "SELECT value FROM knowledge_meta WHERE key = ?", metaCatalogChanges).Scan(&changes)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fail(ctx, err)
	}
	return "change " + strconv.FormatInt(changes, 10), nil
}

// Close lets go of nothing. The tenant's handle was given to the store and
// belongs to whoever opened it.
func (s *store) Close() error { return nil }
