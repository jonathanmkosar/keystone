// Package importer brings a person's chats and routines from New Keystone,
// the owner's earlier Python app, into one tenant (P12-03).
//
// It reads a copy of New Keystone's workspace.sqlite, read-only, and writes
// to one tenant's store and to nothing else: it calls no model, no
// knowledge store and no network. internal/imported keeps the record of
// what came, so a second run brings nothing twice. The command is
// "tools/tenant import". docs/architecture/import.md is the contract.
package importer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	// ErrSource says the file cannot be imported from: it is not there, is
	// not a whole copy, or is not a New Keystone database. The rest of the
	// error says which.
	ErrSource = errors.New("the source cannot be read")
	// ErrWorkspace says the source holds no workspace by the name given,
	// or several and none was named.
	ErrWorkspace = errors.New("the workspace is not settled")
)

func sourcef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrSource, fmt.Sprintf(format, args...))
}

// Source is a copy of New Keystone's database, open for reading only.
type Source struct {
	db   *sql.DB
	path string
	// sum is the SHA-256 of the file, which says which copy it is.
	sum string
	// tables are the tables it holds, each with its columns.
	tables map[string]map[string]bool
}

// dsn is the address of a database file as SQLite reads one. The
// characters SQLite gives a meaning in an address are written as codes.
func dsn(path, query string) string {
	p := filepath.ToSlash(path)
	p = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(p)
	return "file:" + p + "?" + query
}

// OpenSource opens a copy of New Keystone's workspace.sqlite. The file is
// opened read-only and as a file nothing else is writing, so nothing is
// written to it or beside it.
//
// New Keystone keeps its database in write-ahead mode, where the newest
// turns can sit in a second file, workspace.sqlite-wal. A file with such a
// log beside it is the live database, or a plain copy of it that may be
// torn, and is refused: Copy makes a whole copy.
func OpenSource(path string) (*Source, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, sourcef("%v", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, sourcef("%v", err)
	}
	if !st.Mode().IsRegular() {
		return nil, sourcef("%s is not a file", abs)
	}
	for _, beside := range []string{"-wal", "-journal"} {
		if side, err := os.Stat(abs + beside); err == nil && side.Size() > 0 {
			return nil, sourcef("%s has a log beside it (%s), so it is a database in use or a plain copy of one, and its newest turns may not be in it. Make a whole copy with \"tenant import-copy\" and import that", abs, filepath.Base(abs)+beside)
		}
	}
	sum, err := fileSum(abs)
	if err != nil {
		return nil, sourcef("%v", err)
	}
	db, err := sql.Open("sqlite", dsn(abs, "mode=ro&immutable=1"))
	if err != nil {
		return nil, sourcef("%v", err)
	}
	db.SetMaxOpenConns(1)
	s := &Source{db: db, path: abs, sum: sum}
	if err := s.inspect(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the file.
func (s *Source) Close() error { return s.db.Close() }

// SHA256 is the SHA-256 of the file that was opened, in lower-case hex.
func (s *Source) SHA256() string { return s.sum }

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// quote writes a name the file gave as an SQL identifier.
func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// The columns a New Keystone database has had since its first build. A
// file without them is something else.
var needs = map[string][]string{
	"jobs":     {"workspace", "id", "prompt", "status", "created", "updated", "reply", "error"},
	"messages": {"workspace", "id", "role", "content", "job_id", "created"},
}

// inspect checks that the file is a sound SQLite database in New
// Keystone's schema, and reads which tables and columns it has: a database
// last opened by an older build lacks the later ones.
func (s *Source) inspect() error {
	var check string
	if err := s.db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil {
		return sourcef("%s does not open as a SQLite database: %v", s.path, err)
	}
	if check != "ok" {
		return sourcef("%s is damaged: SQLite's check says %q", s.path, check)
	}
	rows, err := s.db.Query("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return sourcef("%v", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return sourcef("%v", err)
		}
		names = append(names, name)
	}
	if err := rows.Close(); err != nil {
		return sourcef("%v", err)
	}
	s.tables = map[string]map[string]bool{}
	for _, name := range names {
		cols := map[string]bool{}
		info, err := s.db.Query("SELECT name FROM pragma_table_info(?)", name)
		if err != nil {
			return sourcef("%v", err)
		}
		for info.Next() {
			var col string
			if err := info.Scan(&col); err != nil {
				info.Close()
				return sourcef("%v", err)
			}
			cols[col] = true
		}
		if err := info.Close(); err != nil {
			return sourcef("%v", err)
		}
		s.tables[name] = cols
	}
	for table, cols := range needs {
		for _, col := range cols {
			if !s.tables[table][col] {
				return sourcef("%s is not a New Keystone database: it has no %s.%s", s.path, table, col)
			}
		}
	}
	return nil
}

// col is a column of a table as a query names it, or the fallback, an SQL
// expression, when this database does not have the column.
func (s *Source) col(table, name, fallback string) string {
	if s.tables[table][name] {
		return name
	}
	return fallback + " AS " + name
}

// Workspace is one workspace of the source: one person's continuing chat.
type Workspace struct {
	Name string
	// Turns is how many turns it holds.
	Turns int
}

// Workspaces lists the workspaces the source holds turns of, by name.
func (s *Source) Workspaces() ([]Workspace, error) {
	rows, err := s.db.Query("SELECT workspace, count(*) FROM jobs GROUP BY workspace ORDER BY workspace")
	if err != nil {
		return nil, sourcef("%v", err)
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.Name, &w.Turns); err != nil {
			return nil, sourcef("%v", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Settle returns the workspace to read: the one named, or the only one
// there is.
func (s *Source) Settle(named string) (string, error) {
	all, err := s.Workspaces()
	if err != nil {
		return "", err
	}
	if named != "" {
		for _, w := range all {
			if w.Name == named {
				return named, nil
			}
		}
		return "", fmt.Errorf("%w: the source holds no turns of a workspace named %q", ErrWorkspace, named)
	}
	switch len(all) {
	case 0:
		return "", fmt.Errorf("%w: the source holds no turns at all", ErrWorkspace)
	case 1:
		return all[0].Name, nil
	}
	var names []string
	for _, w := range all {
		names = append(names, fmt.Sprintf("%s (%d turns)", w.Name, w.Turns))
	}
	return "", fmt.Errorf("%w: the source holds %d workspaces, and one tenant takes one: name it with --workspace. They are %s", ErrWorkspace, len(all), strings.Join(names, ", "))
}

// Said is one row of the source's messages table.
type Said struct {
	Text string
	At   time.Time
}

// Link is one web source New Keystone kept with a reply.
type Link struct {
	Number     string
	Title, URL string
}

// Attachment is the one text file a turn's message could carry.
type Attachment struct {
	Name, Content string
}

// Release is a row of the source's artifacts table: an app a build made.
// The database holds its summary, its state and the names of its files,
// and none of the files.
type Release struct {
	Summary, Status string
	Files           int
}

// Turn is one job of the source: a person's message and the reply to it.
type Turn struct {
	ID string
	// Kind is chat or build. Status is New Keystone's: queued, running,
	// stopping, waiting_decision, succeeded, failed or cancelled.
	Kind, Status string
	// Prompt and Reply are the job's own copies of what was said.
	Prompt, Reply string
	// Model is the model New Keystone named for the reply: the one its
	// model.completed or model.started event names, else the one the
	// person picked for the turn. Empty when it named none.
	Model            string
	Created, Updated time.Time
	// User and Assistant are the turn's rows in the messages table.
	User, Assistant *Said
	Sources         []Link
	Attachment      *Attachment
	// Events counts the turn's events by type.
	Events map[string]int
	// ReadWiki says New Keystone read the person's Waymarks wiki in this
	// turn: pages were recalled into the call, or the model looked one up.
	ReadWiki bool
	// Routine is the id of the routine that started the turn, if one did.
	Routine string
	Release *Release
}

// Routine is a saved prompt on a schedule.
type Routine struct {
	ID, Name, Prompt string
	// Cadence is hourly, daily, weekly, weekdays, manual or once.
	Cadence string
	// Hour is absent for hourly and manual routines. Weekday counts from
	// Monday as 0, for a weekly routine. RunOn is the day of a one-time
	// routine, YYYY-MM-DD.
	Hour    *int
	Minute  int
	Weekday *int
	RunOn   string
	// Status is pending (proposed, never scheduled), active, paused or
	// deleted.
	Status  string
	Builtin bool
}

// TableCount is how many rows of a table belong to the workspace.
type TableCount struct {
	Table string
	Rows  int
}

// Data is everything the importer reads of one workspace.
type Data struct {
	Workspace string
	// Turns are in the order they were sent.
	Turns    []Turn
	Routines []Routine
	// Tables counts the workspace's rows in every table of the source, by
	// table name.
	Tables []TableCount
	// Memories counts the workspace's notebook entries by their status.
	Memories map[string]int
}

// when reads a time New Keystone kept as seconds since 1970.
func when(seconds float64) time.Time {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(math.Round(seconds * 1000))).UTC()
}

// Read reads one workspace.
func (s *Source) Read(workspace string) (*Data, error) {
	d := &Data{Workspace: workspace, Memories: map[string]int{}}
	byID := map[string]*Turn{}

	rows, err := s.db.Query("SELECT id, prompt, status, created, updated, reply, error, "+
		s.col("jobs", "kind", "'chat'")+", "+s.col("jobs", "chat_model", "NULL")+
		" FROM jobs WHERE workspace = ? ORDER BY created, rowid", workspace)
	if err != nil {
		return nil, sourcef("%v", err)
	}
	for rows.Next() {
		var t Turn
		var created, updated float64
		var failure string
		var kind, model sql.NullString
		if err := rows.Scan(&t.ID, &t.Prompt, &t.Status, &created, &updated, &t.Reply, &failure, &kind, &model); err != nil {
			rows.Close()
			return nil, sourcef("jobs: %v", err)
		}
		t.Kind, t.Model = kind.String, model.String
		if t.Kind == "" {
			t.Kind = "chat"
		}
		t.Created, t.Updated = when(created), when(updated)
		t.Events = map[string]int{}
		d.Turns = append(d.Turns, t)
	}
	if err := rows.Close(); err != nil {
		return nil, sourcef("jobs: %v", err)
	}
	for i := range d.Turns {
		byID[d.Turns[i].ID] = &d.Turns[i]
	}

	rows, err = s.db.Query("SELECT job_id, role, content, created, "+s.col("messages", "sources", "NULL")+
		" FROM messages WHERE workspace = ? ORDER BY created, rowid", workspace)
	if err != nil {
		return nil, sourcef("%v", err)
	}
	for rows.Next() {
		var jobID, role, content string
		var created float64
		var sources sql.NullString
		if err := rows.Scan(&jobID, &role, &content, &created, &sources); err != nil {
			rows.Close()
			return nil, sourcef("messages: %v", err)
		}
		t := byID[jobID]
		if t == nil {
			continue
		}
		said := &Said{Text: content, At: when(created)}
		switch role {
		case "user":
			t.User = said
		case "assistant":
			t.Assistant = said
			t.Sources = links(sources.String)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, sourcef("messages: %v", err)
	}

	if s.tables["events"] != nil {
		started := map[string]string{}
		rows, err = s.db.Query("SELECT job_id, event_type, detail FROM events WHERE workspace = ? AND job_id IS NOT NULL ORDER BY sequence", workspace)
		if err != nil {
			return nil, sourcef("%v", err)
		}
		for rows.Next() {
			var jobID, kind, detail string
			if err := rows.Scan(&jobID, &kind, &detail); err != nil {
				rows.Close()
				return nil, sourcef("events: %v", err)
			}
			t := byID[jobID]
			if t == nil {
				continue
			}
			t.Events[kind]++
			switch kind {
			case "model.completed":
				if name := modelNamed(detail); name != "" {
					started[jobID] = name
				}
			case "model.started":
				if name := modelNamed(detail); name != "" && started[jobID] == "" {
					started[jobID] = name
				}
			case "wiki.lookup":
				t.ReadWiki = true
			case "context.assembled":
				var stats struct {
					Pages int `json:"waymarks_pages"`
				}
				if json.Unmarshal([]byte(detail), &stats) == nil && stats.Pages > 0 {
					t.ReadWiki = true
				}
			}
		}
		if err := rows.Close(); err != nil {
			return nil, sourcef("events: %v", err)
		}
		for id, name := range started {
			byID[id].Model = name
		}
	}

	if s.tables["routine_runs"] != nil {
		rows, err = s.db.Query("SELECT job_id, routine_id FROM routine_runs WHERE workspace = ?", workspace)
		if err != nil {
			return nil, sourcef("%v", err)
		}
		for rows.Next() {
			var jobID, routineID string
			if err := rows.Scan(&jobID, &routineID); err != nil {
				rows.Close()
				return nil, sourcef("routine_runs: %v", err)
			}
			if t := byID[jobID]; t != nil {
				t.Routine = routineID
			}
		}
		if err := rows.Close(); err != nil {
			return nil, sourcef("routine_runs: %v", err)
		}
	}

	if s.tables["chat_attachments"] != nil {
		rows, err = s.db.Query("SELECT job_id, name, content FROM chat_attachments WHERE workspace = ?", workspace)
		if err != nil {
			return nil, sourcef("%v", err)
		}
		for rows.Next() {
			var jobID string
			var a Attachment
			if err := rows.Scan(&jobID, &a.Name, &a.Content); err != nil {
				rows.Close()
				return nil, sourcef("chat_attachments: %v", err)
			}
			if t := byID[jobID]; t != nil {
				t.Attachment = &a
			}
		}
		if err := rows.Close(); err != nil {
			return nil, sourcef("chat_attachments: %v", err)
		}
	}

	if s.tables["artifacts"] != nil {
		rows, err = s.db.Query("SELECT job_id, summary, files_json, status FROM artifacts WHERE workspace = ?", workspace)
		if err != nil {
			return nil, sourcef("%v", err)
		}
		for rows.Next() {
			var jobID, files string
			var r Release
			if err := rows.Scan(&jobID, &r.Summary, &files, &r.Status); err != nil {
				rows.Close()
				return nil, sourcef("artifacts: %v", err)
			}
			var names []json.RawMessage
			if json.Unmarshal([]byte(files), &names) == nil {
				r.Files = len(names)
			}
			if t := byID[jobID]; t != nil {
				t.Release = &r
			}
		}
		if err := rows.Close(); err != nil {
			return nil, sourcef("artifacts: %v", err)
		}
	}

	if s.tables["routines"] != nil {
		rows, err = s.db.Query("SELECT id, name, prompt, cadence, hour, minute, weekday, "+s.col("routines", "run_on", "NULL")+
			", status, builtin FROM routines WHERE workspace = ? ORDER BY created, rowid", workspace)
		if err != nil {
			return nil, sourcef("%v", err)
		}
		for rows.Next() {
			var r Routine
			var hour, minute, weekday sql.NullInt64
			var runOn sql.NullString
			var builtin sql.NullInt64
			if err := rows.Scan(&r.ID, &r.Name, &r.Prompt, &r.Cadence, &hour, &minute, &weekday, &runOn, &r.Status, &builtin); err != nil {
				rows.Close()
				return nil, sourcef("routines: %v", err)
			}
			if hour.Valid {
				h := int(hour.Int64)
				r.Hour = &h
			}
			if weekday.Valid {
				w := int(weekday.Int64)
				r.Weekday = &w
			}
			r.Minute, r.RunOn, r.Builtin = int(minute.Int64), runOn.String, builtin.Int64 != 0
			d.Routines = append(d.Routines, r)
		}
		if err := rows.Close(); err != nil {
			return nil, sourcef("routines: %v", err)
		}
	}

	if s.tables["memories"] != nil {
		rows, err = s.db.Query("SELECT status, count(*) FROM memories WHERE workspace = ? GROUP BY status", workspace)
		if err != nil {
			return nil, sourcef("%v", err)
		}
		for rows.Next() {
			var status string
			var n int
			if err := rows.Scan(&status, &n); err != nil {
				rows.Close()
				return nil, sourcef("memories: %v", err)
			}
			d.Memories[status] = n
		}
		if err := rows.Close(); err != nil {
			return nil, sourcef("memories: %v", err)
		}
	}

	var names []string
	for name := range s.tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		q, args := "SELECT count(*) FROM "+quote(name), []any{}
		if s.tables[name]["workspace"] {
			q, args = q+" WHERE workspace = ?", []any{workspace}
		}
		var n int
		if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
			return nil, sourcef("%s: %v", name, err)
		}
		d.Tables = append(d.Tables, TableCount{Table: name, Rows: n})
	}
	return d, nil
}

// modelNamed reads the model's name from the detail of New Keystone's
// model.started ("Model: name.") and model.completed ("Model: name;
// duration_ms: 1200.") events.
func modelNamed(detail string) string {
	rest, ok := strings.CutPrefix(detail, "Model: ")
	if !ok {
		return ""
	}
	if name, _, found := strings.Cut(rest, "; duration_ms:"); found {
		return strings.TrimSpace(name)
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "."))
}

// links reads the web sources New Keystone kept with a reply: a JSON list
// of objects with an id, a title and a url. What does not read is left out.
func links(raw string) []Link {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var items []struct {
		ID    json.RawMessage `json:"id"`
		Title string          `json:"title"`
		URL   string          `json:"url"`
	}
	if json.Unmarshal([]byte(raw), &items) != nil {
		return nil
	}
	var out []Link
	for _, it := range items {
		l := Link{Title: strings.Join(strings.Fields(it.Title), " "), URL: strings.TrimSpace(it.URL)}
		var text string
		var number json.Number
		switch {
		case json.Unmarshal(it.ID, &text) == nil:
			l.Number = strings.TrimSpace(text)
		case json.Unmarshal(it.ID, &number) == nil:
			l.Number = number.String()
		}
		if l.Title == "" && l.URL == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}

// Copy makes a whole copy of a New Keystone database that may be in use:
// one file that holds everything the database and its write-ahead log
// hold at one moment. It reads the source and never writes to it. The copy
// must not be there already.
//
// A database in write-ahead mode that some program has open has a
// shared-memory file beside it (workspace.sqlite-shm), and a log that may
// hold its newest turns. Such a database is read as any reader reads one,
// through those files. One with neither is open nowhere, and is read as a
// file nothing writes, so that SQLite does not make the two files beside
// it: after a copy of a database at rest, its folder holds what it held.
func Copy(from, to string) error {
	src, err := filepath.Abs(from)
	if err != nil {
		return sourcef("%v", err)
	}
	if st, err := os.Stat(src); err != nil {
		return sourcef("%v", err)
	} else if !st.Mode().IsRegular() {
		return sourcef("%s is not a file", src)
	}
	dst, err := filepath.Abs(to)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s is already there, and a copy never replaces a file: give it a new name", dst)
	}
	query := "mode=ro&immutable=1"
	if inUse(src) {
		query = "mode=ro&_pragma=busy_timeout(10000)"
	}
	db, err := sql.Open("sqlite", dsn(src, query))
	if err != nil {
		return sourcef("%v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("VACUUM INTO ?", dst); err != nil {
		os.Remove(dst) // ours alone: it was not there before
		return sourcef("%s could not be copied: %v", src, err)
	}
	if err := db.Close(); err != nil {
		return sourcef("%v", err)
	}
	copied, err := OpenSource(dst)
	if err != nil {
		return fmt.Errorf("the copy was written and does not read back: %w", err)
	}
	return copied.Close()
}

// inUse says a database file has a shared-memory file beside it, or a log
// that holds something: a program has it open, or stopped without closing
// it.
func inUse(path string) bool {
	if _, err := os.Stat(path + "-shm"); err == nil {
		return true
	}
	side, err := os.Stat(path + "-wal")
	return err == nil && side.Size() > 0
}
