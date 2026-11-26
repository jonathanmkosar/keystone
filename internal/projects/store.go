package projects

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

// now is the clock. Tests replace it.
var now = time.Now

func stamp() string { return now().UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) { return time.Parse(timeLayout, s) }

func inTx(ctx context.Context, s *tenant.Store, f func(tx *sql.Tx) error) error {
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// cleanName puts a project's name on one line with single spaces, and
// refuses one that is empty or too long.
func cleanName(name string, max int) (string, error) {
	name = strings.Join(strings.FieldsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if n := utf8.RuneCountInString(name); n == 0 || n > max {
		return "", invalid("a project's name is 1 to %d characters", max)
	}
	return name, nil
}

// cleanInstructions keeps the instructions as the person wrote them, with
// line ends made \n and the spaces at either end taken off.
func cleanInstructions(text string, max int) (string, error) {
	text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	if n := utf8.RuneCountInString(text); n > max {
		return "", invalid("a project's instructions are at most %d characters, and these are %d", max, n)
	}
	return text, nil
}

const projectColumns = "id, name, instructions, memory_space, archived_at, created_at, updated_at"

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var archived sql.NullString
	var created, changed string
	if err := row.Scan(&p.ID, &p.Name, &p.Instructions, &p.MemorySpace, &archived, &created, &changed); err != nil {
		return Project{}, err
	}
	p.Archived = archived.Valid
	var err error
	if p.Created, err = parseTime(created); err != nil {
		return Project{}, fmt.Errorf("project %s: %w", p.ID, err)
	}
	if p.Changed, err = parseTime(changed); err != nil {
		return Project{}, fmt.Errorf("project %s: %w", p.ID, err)
	}
	return p, nil
}

func noProject(id string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("project %q: %w", id, ErrNotFound)
	}
	return err
}

// Create makes a project with a name and instructions, which may be empty,
// and names its memory space from o.MemorySpace.
func Create(ctx context.Context, s *tenant.Store, o Options, name, instructions string) (Project, error) {
	name, err := cleanName(name, o.NameMaxChars)
	if err != nil {
		return Project{}, err
	}
	if instructions, err = cleanInstructions(instructions, o.InstructionsMaxChars); err != nil {
		return Project{}, err
	}
	id, err := newID()
	if err != nil {
		return Project{}, err
	}
	var p Project
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM projects").Scan(&n); err != nil {
			return err
		}
		if n >= o.MaxProjects {
			return full("there are %d projects, as many as projects.max_projects allows; delete one first", n)
		}
		at := stamp()
		p, err = scanProject(tx.QueryRowContext(ctx,
			"INSERT INTO projects(id, name, instructions, memory_space, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?) RETURNING "+projectColumns,
			id, name, instructions, SpaceFor(o.MemorySpace, name, id), at, at))
		return err
	})
	return p, err
}

// Get returns one project.
func Get(ctx context.Context, s *tenant.Store, id string) (Project, error) {
	p, err := scanProject(s.DB().QueryRowContext(ctx, "SELECT "+projectColumns+" FROM projects WHERE id = ?", id))
	return p, noProject(id, err)
}

// List returns the person's projects, the one changed last first, archived
// ones included.
func List(ctx context.Context, s *tenant.Store) ([]Project, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT "+projectColumns+" FROM projects ORDER BY updated_at DESC, seq DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Change is what a person changes of a project. A field that is nil is
// left as it is.
type Change struct {
	Name         *string
	Instructions *string
	Archived     *bool
	// MemorySpace names the space the project's facts go to from now on.
	// Empty keeps them with the person's other facts. Facts kept before
	// stay where they are.
	MemorySpace *string
}

// spaceMaxChars is the longest name of a memory space. It bounds a name a
// store is given, and is not a limit a person tunes.
const spaceMaxChars = 120

// Update changes a project and returns it.
func Update(ctx context.Context, s *tenant.Store, o Options, id string, c Change) (Project, error) {
	sets, args := []string{}, []any{}
	if c.Name != nil {
		name, err := cleanName(*c.Name, o.NameMaxChars)
		if err != nil {
			return Project{}, err
		}
		sets, args = append(sets, "name = ?"), append(args, name)
	}
	if c.Instructions != nil {
		text, err := cleanInstructions(*c.Instructions, o.InstructionsMaxChars)
		if err != nil {
			return Project{}, err
		}
		sets, args = append(sets, "instructions = ?"), append(args, text)
	}
	if c.Archived != nil {
		if *c.Archived {
			sets = append(sets, "archived_at = coalesce(archived_at, ?)")
			args = append(args, stamp())
		} else {
			sets = append(sets, "archived_at = NULL")
		}
	}
	if c.MemorySpace != nil {
		space := strings.Join(strings.Fields(*c.MemorySpace), " ")
		if utf8.RuneCountInString(space) > spaceMaxChars {
			return Project{}, invalid("a memory space's name is at most %d characters", spaceMaxChars)
		}
		sets, args = append(sets, "memory_space = ?"), append(args, space)
	}
	if len(sets) == 0 {
		return Get(ctx, s, id)
	}
	sets, args = append(sets, "updated_at = ?"), append(args, stamp(), id)
	p, err := scanProject(s.DB().QueryRowContext(ctx,
		"UPDATE projects SET "+strings.Join(sets, ", ")+" WHERE id = ? RETURNING "+projectColumns, args...))
	return p, noProject(id, err)
}

// Delete removes a project: its rows, its files' rows, and each of its
// files that nothing else holds. Its chats are kept, as ordinary chats
// (decision 0402). A file that cannot be removed leaves the project as it
// was, so Delete can be run again.
func Delete(ctx context.Context, s *tenant.Store, o Options, id string) error {
	var hashes []string
	err := inTx(ctx, s, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT hash FROM project_files WHERE project_id = ?", id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var h string
			if err := rows.Scan(&h); err != nil {
				rows.Close()
				return err
			}
			hashes = append(hashes, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return noProject(id, sql.ErrNoRows)
		}
		return nil
	})
	if err != nil {
		return err
	}
	var failed error
	for _, h := range hashes {
		failed = errors.Join(failed, o.blobs().Release(ctx, s, h))
	}
	return failed
}

// touch marks a project as changed now, so it rises in the list.
func touch(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, "UPDATE projects SET updated_at = ? WHERE id = ?", stamp(), id)
	return err
}

// Of returns the project a chat is in, and false when it is in none.
func Of(ctx context.Context, s *tenant.Store, chatID string) (Project, bool, error) {
	p, err := scanProject(s.DB().QueryRowContext(ctx,
		"SELECT "+prefixed("p.", projectColumns)+" FROM project_chats c JOIN projects p ON p.id = c.project_id WHERE c.chat_id = ?", chatID))
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, false, nil
	}
	if err != nil {
		return Project{}, false, err
	}
	return p, true, nil
}

func prefixed(prefix, columns string) string {
	parts := strings.Split(columns, ", ")
	for i, c := range parts {
		parts[i] = prefix + c
	}
	return strings.Join(parts, ", ")
}

// Chats returns the ids of the chats in a project, the one put in last
// first.
func Chats(ctx context.Context, s *tenant.Store, projectID string) ([]string, error) {
	if _, err := Get(ctx, s, projectID); err != nil {
		return nil, err
	}
	rows, err := s.DB().QueryContext(ctx, "SELECT chat_id FROM project_chats WHERE project_id = ? ORDER BY added_at DESC, chat_id", projectID)
	if err != nil {
		return nil, err
	}
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

// Counts returns how many files and chats each project has, by project.
func Counts(ctx context.Context, s *tenant.Store) (files, chats map[string]int, err error) {
	files, chats = map[string]int{}, map[string]int{}
	for table, into := range map[string]map[string]int{"project_files": files, "project_chats": chats} {
		rows, err := s.DB().QueryContext(ctx, "SELECT project_id, count(*) FROM "+table+" GROUP BY project_id")
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var id string
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				rows.Close()
				return nil, nil, err
			}
			into[id] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
	}
	return files, chats, nil
}

// Move puts a chat into a project, out of any other it was in, and reports
// whether it moved: a chat already in the project is left as it is. Both
// must be in this tenant's record.
func Move(ctx context.Context, s *tenant.Store, projectID, chatID string) (bool, error) {
	moved := false
	err := inTx(ctx, s, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM chats WHERE id = ?) + (SELECT count(*) FROM projects WHERE id = ?)", chatID, projectID).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			return fmt.Errorf("chat %q or project %q: %w", chatID, projectID, ErrNotFound)
		}
		var have string
		err := tx.QueryRowContext(ctx, "SELECT project_id FROM project_chats WHERE chat_id = ?", chatID).Scan(&have)
		switch {
		case err == nil && have == projectID:
			return nil
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_chats(chat_id, project_id, added_at) VALUES(?, ?, ?)
			ON CONFLICT(chat_id) DO UPDATE SET project_id = excluded.project_id, added_at = excluded.added_at`, chatID, projectID, stamp()); err != nil {
			return err
		}
		moved = true
		return touch(ctx, tx, projectID)
	})
	return moved, err
}

// Leave takes a chat out of a project, and reports whether it was in it. A
// chat in another project, or in none, is left as it is.
func Leave(ctx context.Context, s *tenant.Store, projectID, chatID string) (bool, error) {
	if _, err := Get(ctx, s, projectID); err != nil {
		return false, err
	}
	res, err := s.DB().ExecContext(ctx, "DELETE FROM project_chats WHERE chat_id = ? AND project_id = ?", chatID, projectID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
