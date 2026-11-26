package isolationdrill

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// A look at one person's folder, database and audit log, to compare before
// and after a part of the drill.
type look struct {
	// files are every file in the person's folder but the database's own,
	// by path, with its SHA-256.
	files map[string]string
	// tables are every table of the database but the audit log and the
	// ones the server's own samplers write, with a hash of its rows.
	tables map[string]string
	// audit is the audit log's entries, as their action and outcome.
	audit []string
}

// unwatched are the tables a look leaves out, and why.
var unwatched = map[string]string{
	// The audit log is compared entry by entry, below.
	"audit_log": "compared as entries",
	// Operations' sampler records every person's limits on a timer, from
	// the server, for each person in their own database (P11-03).
	"operations_snapshots": "written by the install's sampler, for each person on their own",
}

// dbFiles are the database's own files, compared as tables.
var dbFiles = map[string]bool{"keystone.db": true, "keystone.db-wal": true, "keystone.db-shm": true}

func (g *rig) look() (look, error) { return g.lookAt(g.one.id) }

// lookAt looks at one person's folder, database and audit log.
func (g *rig) lookAt(id string) (look, error) {
	l := look{files: map[string]string{}, tables: map[string]string{}}
	err := g.with(id, func(s *tenant.Store) error {
		folder := filepath.Dir(s.Files().Name())
		err := filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || dbFiles[d.Name()] {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(folder, p)
			sum := sha256.Sum256(raw)
			l.files[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
			return nil
		})
		if err != nil {
			return err
		}
		names, err := tableNames(s.DB())
		if err != nil {
			return err
		}
		for _, name := range names {
			if _, skip := unwatched[name]; skip {
				continue
			}
			rows, err := tableRows(s.DB(), name)
			if err != nil {
				return fmt.Errorf("reading %s: %w", name, err)
			}
			sum := sha256.Sum256([]byte(strings.Join(rows, "\n")))
			l.tables[name] = hex.EncodeToString(sum[:])
		}
		entries, err := audit.List(context.Background(), s, audit.Filter{})
		if err != nil {
			return err
		}
		for _, e := range entries {
			l.audit = append(l.audit, fmt.Sprintf("%s %s %s", e.Kind, e.Action, e.Outcome))
		}
		return nil
	})
	return l, err
}

// sum is one text for the whole look, to tell whether anything moved.
func (l look) sum() string {
	var parts []string
	for k, v := range l.files {
		parts = append(parts, "f "+k+" "+v)
	}
	for k, v := range l.tables {
		parts = append(parts, "t "+k+" "+v)
	}
	sort.Strings(parts)
	return fmt.Sprintf("%s\n%d", strings.Join(parts, "\n"), len(l.audit))
}

// changes says what differs from l in m.
func (l look) changes(m look) []string {
	var out []string
	for p, sum := range l.files {
		switch now, ok := m.files[p]; {
		case !ok:
			out = append(out, "file "+p+", which is gone")
		case now != sum:
			out = append(out, "file "+p+", whose bytes changed")
		}
	}
	for p := range m.files {
		if _, ok := l.files[p]; !ok {
			out = append(out, "folder, where a file "+p+" was written")
		}
	}
	for t, sum := range l.tables {
		if m.tables[t] != sum {
			out = append(out, "table "+t)
		}
	}
	for t := range m.tables {
		if _, ok := l.tables[t]; !ok {
			out = append(out, "database, where a table "+t+" was made")
		}
	}
	if len(m.audit) != len(l.audit) {
		var added []string
		if len(m.audit) > len(l.audit) {
			added = m.audit[len(l.audit):]
		}
		out = append(out, fmt.Sprintf("audit log, which went from %d entries to %d (%s)", len(l.audit), len(m.audit), strings.Join(added, "; ")))
	}
	sort.Strings(out)
	return out
}

func tableNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// tableRows returns every row of a table as text, sorted, so the order the
// database returns them in does not matter.
func tableRows(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query(`SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		var b strings.Builder
		for i, v := range vals {
			if i > 0 {
				b.WriteString("\x1f")
			}
			switch x := v.(type) {
			case []byte:
				b.WriteString(string(x))
			case nil:
				b.WriteString("\x00")
			default:
				fmt.Fprint(&b, x)
			}
		}
		out = append(out, b.String())
	}
	sort.Strings(out)
	return out, rows.Err()
}

// everything is every text a person's folder and database hold: each file's
// bytes and each row, for looking for the other person's marks.
func (g *rig) everything(id string) (map[string]string, error) {
	out := map[string]string{}
	err := g.with(id, func(s *tenant.Store) error {
		folder := filepath.Dir(s.Files().Name())
		err := filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || dbFiles[d.Name()] {
				return err
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(folder, p)
			out["file "+filepath.ToSlash(rel)] = string(raw)
			return nil
		})
		if err != nil {
			return err
		}
		names, err := tableNames(s.DB())
		if err != nil {
			return err
		}
		for _, name := range names {
			rows, err := tableRows(s.DB(), name)
			if err != nil {
				return fmt.Errorf("reading %s: %w", name, err)
			}
			out["table "+name] = strings.Join(rows, "\n")
		}
		return nil
	})
	return out, err
}
