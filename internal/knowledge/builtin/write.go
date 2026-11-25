package builtin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

const metaCatalogChanges = "catalog_changes"

// now is the clock. Tests may replace it.
var now = time.Now

func stamp() string { return now().UTC().Format("2006-01-02T15:04:05.000Z") }

// NewPage is a page to write.
type NewPage struct {
	// Space is where the page goes. A space the store does not hold yet is
	// made, as a normal space.
	Space string
	// Path is where the page sits in its space: words joined by "/", such
	// as garden/beds.
	Path  string
	Title string
	// Kind is what the page is about, such as project. It may be empty.
	Kind string
	// Body is the page's Markdown.
	Body string
	// Sources are the ids of the sources the page cites, in order.
	Sources []string
}

// NewText is a source to add as it stands: a letter, a note, a file's text.
type NewText struct {
	Space  string
	Name   string
	Origin string
	Dated  string
	Text   string
}

// checkSpace refuses a space name that is blank or could not be a folder's
// name, so a store can always be written out as folders of files.
func checkSpace(space string) error {
	if strings.TrimSpace(space) != space || space == "" || space == "." || space == ".." || strings.ContainsAny(space, `/\`) {
		return invalid("%q is not a space's name: a name has no slash and no space at either end", space)
	}
	return nil
}

func checkPath(path string) error {
	if path == "" || strings.Contains(path, `\`) {
		return invalid("%q is not a page's path: a path is words joined by /", path)
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.TrimSpace(part) != part {
			return invalid("%q is not a page's path: a path is words joined by /", path)
		}
	}
	return nil
}

// inTx runs do in one write transaction on the tenant's database.
func inTx(ctx context.Context, t *tenant.Store, do func(tx *sql.Tx) error) error {
	if t == nil {
		return errors.New("the built-in knowledge store was given no tenant")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := t.DB().BeginTx(ctx, nil)
	if err != nil {
		return fail(ctx, err)
	}
	if err := do(tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fail(ctx, err)
	}
	return nil
}

// ensureSpace makes a space the store does not hold yet, as a normal one.
// A space that is there keeps its label.
func ensureSpace(ctx context.Context, tx *sql.Tx, space string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO knowledge_spaces(name, label) VALUES(?, 'normal') ON CONFLICT(name) DO NOTHING", space)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}

// catalogChanged moves the catalog marker on.
func catalogChanged(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO knowledge_meta(key, value) VALUES(?, 1) ON CONFLICT(key) DO UPDATE SET value = value + 1", metaCatalogChanges)
	if err != nil {
		return fail(ctx, err)
	}
	return nil
}

// WritePage adds a page, or replaces the page already at that space and
// path. A replaced page keeps its id. Its revision moves on only when its
// title, kind, body or cited sources changed, so writing the same page
// twice leaves its vector good.
func WritePage(ctx context.Context, t *tenant.Store, p NewPage) (knowledge.Page, error) {
	if err := checkSpace(p.Space); err != nil {
		return knowledge.Page{}, err
	}
	if err := checkPath(p.Path); err != nil {
		return knowledge.Page{}, err
	}
	if strings.TrimSpace(p.Title) == "" {
		return knowledge.Page{}, invalid("a page needs a title")
	}
	cites := make([]int64, len(p.Sources))
	for i, id := range p.Sources {
		n, ok := rowOf(sourcePrefix, id)
		if !ok {
			return knowledge.Page{}, invalid("the page cites %q, which is not a source's id", id)
		}
		cites[i] = n
	}
	var out knowledge.Page
	err := inTx(ctx, t, func(tx *sql.Tx) error {
		for i, n := range cites {
			var there int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM knowledge_sources WHERE id = ?", n).Scan(&there); err != nil {
				return fail(ctx, err)
			}
			if there == 0 {
				return invalid("the page cites source %q, which the store does not hold", p.Sources[i])
			}
		}
		if err := ensureSpace(ctx, tx, p.Space); err != nil {
			return err
		}
		var id int64
		var title, kind, body string
		err := tx.QueryRowContext(ctx, "SELECT id, title, kind, body FROM knowledge_pages WHERE space = ? AND path = ?", p.Space, p.Path).
			Scan(&id, &title, &kind, &body)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			res, err := tx.ExecContext(ctx, `INSERT INTO knowledge_pages(space, path, title, kind, body, revision, updated_at)
				VALUES(?, ?, ?, ?, ?, 1, ?)`, p.Space, p.Path, p.Title, p.Kind, p.Body, stamp())
			if err != nil {
				return fail(ctx, err)
			}
			if id, err = res.LastInsertId(); err != nil {
				return fail(ctx, err)
			}
			if err := writeCites(ctx, tx, id, cites); err != nil {
				return err
			}
			if err := indexPage(ctx, tx, id, p.Title, p.Body); err != nil {
				return err
			}
			if err := catalogChanged(ctx, tx); err != nil {
				return err
			}
		case err != nil:
			return fail(ctx, err)
		default:
			had, err := readCites(ctx, tx, id)
			if err != nil {
				return err
			}
			if title == p.Title && kind == p.Kind && body == p.Body && slices.Equal(had, cites) {
				break
			}
			if _, err := tx.ExecContext(ctx, `UPDATE knowledge_pages
				SET title = ?, kind = ?, body = ?, revision = revision + 1, updated_at = ? WHERE id = ?`,
				p.Title, p.Kind, p.Body, stamp(), id); err != nil {
				return fail(ctx, err)
			}
			if err := writeCites(ctx, tx, id, cites); err != nil {
				return err
			}
			if err := indexPage(ctx, tx, id, p.Title, p.Body); err != nil {
				return err
			}
			if title != p.Title || kind != p.Kind {
				if err := catalogChanged(ctx, tx); err != nil {
					return err
				}
			}
		}
		out, err = readPage(ctx, tx, id)
		return err
	})
	if err != nil {
		return knowledge.Page{}, err
	}
	return out, nil
}

func readCites(ctx context.Context, tx *sql.Tx, page int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT source_id FROM knowledge_page_sources WHERE page_id = ? ORDER BY position", page)
	if err != nil {
		return nil, fail(ctx, err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return nil, fail(ctx, err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fail(ctx, err)
	}
	return out, nil
}

func writeCites(ctx context.Context, tx *sql.Tx, page int64, cites []int64) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM knowledge_page_sources WHERE page_id = ?", page); err != nil {
		return fail(ctx, err)
	}
	for i, n := range cites {
		if _, err := tx.ExecContext(ctx, "INSERT INTO knowledge_page_sources(page_id, position, source_id) VALUES(?, ?, ?)", page, i, n); err != nil {
			return fail(ctx, err)
		}
	}
	return nil
}

// indexPage puts a page's words in the search index, in place of what the
// index held for it.
func indexPage(ctx context.Context, tx *sql.Tx, id int64, title, body string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM knowledge_page_search WHERE rowid = ?", id); err != nil {
		return fail(ctx, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO knowledge_page_search(rowid, title, body) VALUES(?, ?, ?)", id, title, body); err != nil {
		return fail(ctx, err)
	}
	return nil
}

// RemovePage takes a page out of the store, with its vector and its place
// in the search index. The sources it cited stay.
func RemovePage(ctx context.Context, t *tenant.Store, id string) error {
	n, ok := rowOf(pagePrefix, id)
	if !ok {
		return fmt.Errorf("%w: page %q", knowledge.ErrNotFound, id)
	}
	return inTx(ctx, t, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM knowledge_pages WHERE id = ?", n)
		if err != nil {
			return fail(ctx, err)
		}
		if gone, err := res.RowsAffected(); err != nil {
			return fail(ctx, err)
		} else if gone == 0 {
			return fmt.Errorf("%w: page %q", knowledge.ErrNotFound, id)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM knowledge_page_search WHERE rowid = ?", n); err != nil {
			return fail(ctx, err)
		}
		return catalogChanged(ctx, tx)
	})
}

// AddText stores a source as it stands and returns it as stored. A source
// is never changed afterwards. Adding the same thing twice makes two
// sources.
func AddText(ctx context.Context, t *tenant.Store, n NewText) (knowledge.Source, error) {
	if err := checkSpace(n.Space); err != nil {
		return knowledge.Source{}, err
	}
	if strings.TrimSpace(n.Name) == "" {
		return knowledge.Source{}, invalid("a source needs a name")
	}
	if strings.TrimSpace(n.Text) == "" {
		return knowledge.Source{}, invalid("a source needs some text")
	}
	var out knowledge.Source
	err := inTx(ctx, t, func(tx *sql.Tx) error {
		if err := ensureSpace(ctx, tx, n.Space); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO knowledge_sources(space, name, origin, dated, text, added_at)
			VALUES(?, ?, ?, ?, ?, ?)`, n.Space, n.Name, n.Origin, n.Dated, n.Text, stamp())
		if err != nil {
			return fail(ctx, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fail(ctx, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO knowledge_source_search(rowid, name, text) VALUES(?, ?, ?)", id, n.Name, n.Text); err != nil {
			return fail(ctx, err)
		}
		out, err = readSource(ctx, tx, id)
		return err
	})
	if err != nil {
		return knowledge.Source{}, err
	}
	return out, nil
}

// SetLabel marks a space normal or sensitive, whether or not the space
// holds anything yet. The next read of anything in the space carries the
// new label. Only the two labels are taken.
func SetLabel(ctx context.Context, t *tenant.Store, space string, label knowledge.Label) error {
	if err := checkSpace(space); err != nil {
		return err
	}
	if label != knowledge.LabelNormal && label != knowledge.LabelSensitive {
		return invalid("%q is not a label: a space is normal or sensitive", label)
	}
	return inTx(ctx, t, func(tx *sql.Tx) error {
		var had string
		err := tx.QueryRowContext(ctx, "SELECT label FROM knowledge_spaces WHERE name = ?", space).Scan(&had)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fail(ctx, err)
		}
		if had == string(label) {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_spaces(name, label) VALUES(?, ?)
			ON CONFLICT(name) DO UPDATE SET label = excluded.label`, space, string(label)); err != nil {
			return fail(ctx, err)
		}
		return catalogChanged(ctx, tx)
	})
}
