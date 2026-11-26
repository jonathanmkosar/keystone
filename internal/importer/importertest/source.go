// Package importertest makes databases in New Keystone's schema, with
// invented people and invented words, for the import's tests and its check
// in verify, and holds the import's proof, which both run. Nothing here is
// in the server Keystone ships, and nothing here reads a real database.
package importertest

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Schema is New Keystone's schema as its newest build leaves a database:
// the tables its store.py, trials.py and memory_wiki.py make, with every
// column later builds added.
const Schema = `
CREATE TABLE jobs(
    workspace TEXT NOT NULL, id TEXT NOT NULL, prompt TEXT NOT NULL,
    status TEXT NOT NULL, created REAL NOT NULL, updated REAL NOT NULL,
    reply TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
    lease_until REAL, constitution_version TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'chat',
    search_mode TEXT, review_query TEXT, review_status TEXT, base_job_id TEXT, base_artifact TEXT,
    feedback_id TEXT, trial_id TEXT, chat_model TEXT,
    PRIMARY KEY(workspace,id));
CREATE INDEX jobs_queue ON jobs(workspace,status,created);
CREATE TABLE messages(
    workspace TEXT NOT NULL, id TEXT NOT NULL, role TEXT NOT NULL,
    content TEXT NOT NULL, job_id TEXT NOT NULL, created REAL NOT NULL, sources TEXT,
    PRIMARY KEY(workspace,id), UNIQUE(workspace,job_id,role),
    FOREIGN KEY(workspace,job_id) REFERENCES jobs(workspace,id));
CREATE INDEX messages_workspace ON messages(workspace,created);
CREATE TABLE chat_attachments(
    workspace TEXT NOT NULL, job_id TEXT NOT NULL, name TEXT NOT NULL,
    content TEXT NOT NULL, byte_count INTEGER NOT NULL,
    PRIMARY KEY(workspace,job_id),
    FOREIGN KEY(workspace,job_id) REFERENCES jobs(workspace,id));
CREATE TABLE decisions(
    workspace TEXT NOT NULL, id TEXT NOT NULL, job_id TEXT NOT NULL,
    status TEXT NOT NULL, artifact TEXT NOT NULL, summary TEXT NOT NULL,
    created REAL NOT NULL, resolved REAL,
    PRIMARY KEY(workspace,id), UNIQUE(workspace,job_id),
    FOREIGN KEY(workspace,job_id) REFERENCES jobs(workspace,id));
CREATE TABLE events(
    sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
    workspace TEXT NOT NULL, event_type TEXT NOT NULL, job_id TEXT,
    created REAL NOT NULL, detail TEXT NOT NULL,
    FOREIGN KEY(workspace,job_id) REFERENCES jobs(workspace,id));
CREATE INDEX events_workspace_job ON events(workspace,job_id,sequence);
CREATE TABLE proposals(
    workspace TEXT NOT NULL, job_id TEXT NOT NULL, title TEXT NOT NULL,
    brief TEXT NOT NULL, created REAL NOT NULL, build_job_id TEXT, browser_checks_json TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY(workspace,job_id),
    FOREIGN KEY(workspace,job_id) REFERENCES jobs(workspace,id));
CREATE TABLE artifacts(
    workspace TEXT NOT NULL, job_id TEXT NOT NULL, artifact TEXT NOT NULL,
    summary TEXT NOT NULL, files_json TEXT NOT NULL, checks_json TEXT NOT NULL,
    status TEXT NOT NULL, created REAL NOT NULL, url TEXT NOT NULL DEFAULT '', origin_url TEXT NOT NULL DEFAULT '',
    acceptance_json TEXT NOT NULL DEFAULT '', publication_scope TEXT NOT NULL DEFAULT 'workstation',
    release_check_json TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(workspace,job_id),
    FOREIGN KEY(workspace,job_id) REFERENCES jobs(workspace,id));
CREATE TABLE lan_app_origins(
    workspace TEXT NOT NULL, access TEXT NOT NULL, port INTEGER NOT NULL,
    PRIMARY KEY(workspace,access), UNIQUE(workspace,port));
CREATE TABLE release_feedback(
    workspace TEXT NOT NULL, id TEXT NOT NULL, job_id TEXT NOT NULL, artifact TEXT NOT NULL,
    accepted INTEGER NOT NULL, feedback TEXT NOT NULL, recorded REAL NOT NULL,
    PRIMARY KEY(workspace,id),
    FOREIGN KEY(workspace,job_id) REFERENCES artifacts(workspace,job_id));
CREATE TABLE memories(
    workspace TEXT NOT NULL, id TEXT NOT NULL, content TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '', status TEXT NOT NULL,
    created REAL NOT NULL, updated REAL NOT NULL, replaces_id TEXT, topic TEXT NOT NULL DEFAULT 'Notes',
    source_job TEXT, wiki_slug TEXT,
    PRIMARY KEY(workspace,id));
CREATE TABLE memory_reviews(
    workspace TEXT NOT NULL, job_id TEXT NOT NULL, review_version INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY(workspace,job_id),
    FOREIGN KEY(workspace,job_id) REFERENCES jobs(workspace,id));
CREATE TABLE routines(
    workspace TEXT NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL, prompt TEXT NOT NULL,
    cadence TEXT NOT NULL, hour INTEGER, minute INTEGER NOT NULL DEFAULT 0, weekday INTEGER,
    status TEXT NOT NULL, builtin INTEGER NOT NULL DEFAULT 0, source_job TEXT,
    armed REAL NOT NULL, created REAL NOT NULL, updated REAL NOT NULL, run_on TEXT,
    PRIMARY KEY(workspace,id));
CREATE TABLE routine_runs(
    workspace TEXT NOT NULL, id TEXT NOT NULL, routine_id TEXT NOT NULL, job_id TEXT NOT NULL,
    trigger TEXT NOT NULL, slot TEXT NOT NULL DEFAULT '', created REAL NOT NULL,
    PRIMARY KEY(workspace,id));
CREATE UNIQUE INDEX routine_one_slot ON routine_runs(workspace,routine_id,slot) WHERE slot<>'';
CREATE TABLE calendar_drafts(
    workspace TEXT NOT NULL, id TEXT NOT NULL, source_job TEXT,
    title TEXT NOT NULL, starts TEXT NOT NULL, ends TEXT NOT NULL,
    status TEXT NOT NULL, created REAL NOT NULL, updated REAL NOT NULL, guests TEXT NOT NULL DEFAULT '[]',
    event_url TEXT NOT NULL DEFAULT '', operation TEXT NOT NULL DEFAULT 'create', target TEXT NOT NULL DEFAULT '{}',
    changes TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY(workspace,id));
CREATE TABLE email_drafts(
    workspace TEXT NOT NULL, id TEXT NOT NULL, source_job TEXT,
    recipient TEXT NOT NULL, subject TEXT NOT NULL, body TEXT NOT NULL,
    status TEXT NOT NULL, created REAL NOT NULL, updated REAL NOT NULL,
    PRIMARY KEY(workspace,id), UNIQUE(workspace,source_job),
    FOREIGN KEY(workspace,source_job) REFERENCES jobs(workspace,id));
CREATE TABLE acceptance_trials(
    workspace TEXT NOT NULL, id TEXT NOT NULL, baseline TEXT NOT NULL,
    started REAL NOT NULL, ended REAL, environment TEXT NOT NULL, outcome_json TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(workspace,id));
CREATE TABLE trial_observations(
    workspace TEXT NOT NULL, trial_id TEXT NOT NULL, job_id TEXT NOT NULL,
    checkpoint TEXT NOT NULL, version TEXT NOT NULL, changed INTEGER NOT NULL, recorded REAL NOT NULL,
    PRIMARY KEY(workspace,trial_id,job_id,checkpoint,version),
    FOREIGN KEY(workspace,trial_id) REFERENCES acceptance_trials(workspace,id));
CREATE TABLE wiki_article_jobs(
    workspace TEXT NOT NULL, slug TEXT NOT NULL, revision INTEGER NOT NULL,
    status TEXT NOT NULL, attempts INTEGER NOT NULL, updated REAL NOT NULL,
    PRIMARY KEY(workspace,slug));
CREATE TABLE wiki_revisions(
    workspace TEXT NOT NULL, slug TEXT NOT NULL, revision INTEGER NOT NULL,
    markdown TEXT NOT NULL, sections TEXT NOT NULL, updated REAL NOT NULL, article TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(workspace,slug,revision));
CREATE TABLE wiki_topics(
    workspace TEXT NOT NULL, slug TEXT NOT NULL, title TEXT NOT NULL, terms TEXT NOT NULL,
    PRIMARY KEY(workspace,slug));
`

// FirstBuildSchema is the schema New Keystone's first build made: jobs and
// messages without the columns later builds added, and none of the later
// tables. A database no later build opened looks like this.
const FirstBuildSchema = `
CREATE TABLE jobs(
    workspace TEXT NOT NULL, id TEXT NOT NULL, prompt TEXT NOT NULL,
    status TEXT NOT NULL, created REAL NOT NULL, updated REAL NOT NULL,
    reply TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
    lease_until REAL, constitution_version TEXT NOT NULL,
    PRIMARY KEY(workspace,id));
CREATE TABLE messages(
    workspace TEXT NOT NULL, id TEXT NOT NULL, role TEXT NOT NULL,
    content TEXT NOT NULL, job_id TEXT NOT NULL, created REAL NOT NULL,
    PRIMARY KEY(workspace,id), UNIQUE(workspace,job_id,role),
    FOREIGN KEY(workspace,job_id) REFERENCES jobs(workspace,id));
`

// Event is one row of the events table for a turn.
type Event struct {
	Type, Detail string
}

// Link is one web source kept with a reply.
type Link struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Release is the app a build made.
type Release struct {
	Summary, Status string
	Files           []string
}

// Turn is one job, with its messages and what New Keystone recorded.
type Turn struct {
	// ID is 32 hex characters. Empty means one is made from the turn's
	// place in the fixture.
	ID string
	// Kind is chat when empty. Status is succeeded when empty.
	Kind, Status string
	Prompt       string
	// Reply is the assistant's message. Empty writes none, as for a turn
	// that failed.
	Reply string
	// Picked is the model the person picked for the turn (jobs.chat_model).
	Picked string
	// At is when the turn was sent, and Took how long the reply took.
	At   time.Time
	Took time.Duration
	// Events are added after the job.queued and job.started events every
	// turn has.
	Events  []Event
	Sources []Link
	// AttachmentName and AttachmentText are the turn's text file.
	AttachmentName, AttachmentText string
	// Routine is the id of the routine that started the turn.
	Routine string
	Release *Release
}

// Routine is one row of the routines table.
type Routine struct {
	ID, Name, Prompt, Cadence string
	Hour                      *int
	Minute                    int
	Weekday                   *int
	RunOn                     string
	// Status is active when empty.
	Status  string
	Builtin bool
}

// Fixture is one workspace to write.
type Fixture struct {
	Workspace string
	Turns     []Turn
	Routines  []Routine
	// Memories are notebook entries: the status of each. Their words are
	// invented by Build.
	Memories []string
	// Decisions, CalendarDrafts, EmailDrafts and Proposals are how many
	// rows of each to write, hung on the fixture's first turns.
	Decisions, CalendarDrafts, EmailDrafts, Proposals int
}

// TurnID is the id Build gives the fixture's nth turn, counted from 0, when
// the turn names none.
func TurnID(n int) string { return fmt.Sprintf("%032x", 0xa000+n) }

func unix(t time.Time) float64 { return float64(t.UnixMilli()) / 1000 }

// dsn is the address of a database file as SQLite reads one.
func dsn(path string) string {
	p := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.ToSlash(path))
	return "file:" + p + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
}

// Build makes a new database at path in New Keystone's schema, in
// write-ahead mode as New Keystone keeps its own, writes the fixtures into
// it, and closes it, which leaves one whole file.
func Build(path string, fixtures ...Fixture) error {
	db, err := Open(path, Schema)
	if err != nil {
		return err
	}
	for _, f := range fixtures {
		if err := Write(db, f); err != nil {
			db.Close()
			return err
		}
	}
	return db.Close()
}

// Open makes a new database at path with a schema, in write-ahead mode, and
// leaves it open: while it is, what is written to it sits in the log
// beside it, as in a New Keystone that is running.
func Open(path, schema string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", schema} {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

// Write writes one workspace into an open database.
func Write(db *sql.DB, f Fixture) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	has := func(table string) bool {
		var n int
		tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&n)
		return n > 0
	}
	later := has("events")
	// Row ids are made from the workspace and a count, so two workspaces
	// in one database never share one.
	n := 0
	id := func() string {
		n++
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s row %d", f.Workspace, n)))
		return hex.EncodeToString(sum[:])[:32]
	}
	var ids []string
	for i, t := range f.Turns {
		if t.ID == "" {
			t.ID = TurnID(i)
		}
		ids = append(ids, t.ID)
		kind, status := t.Kind, t.Status
		if kind == "" {
			kind = "chat"
		}
		if status == "" {
			status = "succeeded"
		}
		ended := t.At.Add(t.Took)
		reply, failure := "", ""
		if status == "succeeded" {
			reply = t.Reply
		}
		if status == "failed" {
			failure = "The job could not complete. Review its activity and try again."
		}
		if later {
			var picked any
			if t.Picked != "" {
				picked = t.Picked
			}
			_, err = tx.Exec(`INSERT INTO jobs(workspace,id,prompt,status,created,updated,reply,error,constitution_version,kind,search_mode,chat_model)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, f.Workspace, t.ID, t.Prompt, status, unix(t.At), unix(ended), reply, failure, "sha256:test-data", kind, "workspace", picked)
		} else {
			_, err = tx.Exec(`INSERT INTO jobs(workspace,id,prompt,status,created,updated,reply,error,constitution_version)
				VALUES (?,?,?,?,?,?,?,?,?)`, f.Workspace, t.ID, t.Prompt, status, unix(t.At), unix(ended), reply, failure, "sha256:test-data")
		}
		if err != nil {
			return fmt.Errorf("jobs: %w", err)
		}
		if _, err := tx.Exec("INSERT INTO messages(workspace,id,role,content,job_id,created) VALUES (?,?,?,?,?,?)",
			f.Workspace, id(), "user", t.Prompt, t.ID, unix(t.At)); err != nil {
			return fmt.Errorf("messages: %w", err)
		}
		if t.Reply != "" {
			if _, err := tx.Exec("INSERT INTO messages(workspace,id,role,content,job_id,created) VALUES (?,?,?,?,?,?)",
				f.Workspace, id(), "assistant", t.Reply, t.ID, unix(ended)); err != nil {
				return fmt.Errorf("messages: %w", err)
			}
			if len(t.Sources) > 0 && later {
				raw, _ := json.Marshal(t.Sources)
				if _, err := tx.Exec("UPDATE messages SET sources=? WHERE workspace=? AND job_id=? AND role='assistant'", string(raw), f.Workspace, t.ID); err != nil {
					return fmt.Errorf("messages: %w", err)
				}
			}
		}
		if !later {
			continue
		}
		events := append([]Event{{"job.queued", "Message accepted and queued."}, {"job.started", "Worker claimed job."}}, t.Events...)
		switch status {
		case "succeeded":
			events = append(events, Event{"job.succeeded", "Assistant reply saved."})
		case "failed":
			events = append(events, Event{"job.failed", failure})
		case "cancelled":
			events = append(events, Event{"job.cancelled", "Owner cancelled the job; existing external effects are not undone."})
		}
		for _, e := range events {
			if _, err := tx.Exec("INSERT INTO events(id,workspace,event_type,job_id,created,detail) VALUES (?,?,?,?,?,?)",
				id(), f.Workspace, e.Type, t.ID, unix(t.At), e.Detail); err != nil {
				return fmt.Errorf("events: %w", err)
			}
		}
		if t.AttachmentName != "" {
			if _, err := tx.Exec("INSERT INTO chat_attachments VALUES (?,?,?,?,?)", f.Workspace, t.ID, t.AttachmentName, t.AttachmentText, len(t.AttachmentText)); err != nil {
				return fmt.Errorf("chat_attachments: %w", err)
			}
		}
		if t.Routine != "" {
			if _, err := tx.Exec("INSERT INTO routine_runs(workspace,id,routine_id,job_id,trigger,slot,created) VALUES (?,?,?,?,?,?,?)",
				f.Workspace, id(), t.Routine, t.ID, "schedule", t.At.UTC().Format("2006-01-02T15:04"), unix(t.At)); err != nil {
				return fmt.Errorf("routine_runs: %w", err)
			}
		}
		if r := t.Release; r != nil {
			files, _ := json.Marshal(r.Files)
			if _, err := tx.Exec(`INSERT INTO artifacts(workspace,job_id,artifact,summary,files_json,checks_json,status,created)
				VALUES (?,?,?,?,?,?,?,?)`, f.Workspace, t.ID, strings.Repeat("ab", 32), r.Summary, string(files), `{"passed":true}`, r.Status, unix(ended)); err != nil {
				return fmt.Errorf("artifacts: %w", err)
			}
		}
		if _, err := tx.Exec("INSERT INTO memory_reviews(workspace,job_id,review_version) VALUES (?,?,2)", f.Workspace, t.ID); err != nil {
			return fmt.Errorf("memory_reviews: %w", err)
		}
	}
	if !later {
		return tx.Commit()
	}
	at := unix(time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC))
	for _, r := range f.Routines {
		status := r.Status
		if status == "" {
			status = "active"
		}
		var hour, weekday, runOn any
		if r.Hour != nil {
			hour = *r.Hour
		}
		if r.Weekday != nil {
			weekday = *r.Weekday
		}
		if r.RunOn != "" {
			runOn = r.RunOn
		}
		builtin := 0
		if r.Builtin {
			builtin = 1
		}
		if _, err := tx.Exec(`INSERT INTO routines(workspace,id,name,prompt,cadence,hour,minute,weekday,run_on,status,builtin,armed,created,updated)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, f.Workspace, r.ID, r.Name, r.Prompt, r.Cadence, hour, r.Minute, weekday, runOn, status, builtin, at, at, at); err != nil {
			return fmt.Errorf("routines: %w", err)
		}
	}
	for i, status := range f.Memories {
		if _, err := tx.Exec("INSERT INTO memories(workspace,id,content,status,created,updated,topic) VALUES (?,?,?,?,?,?,?)",
			f.Workspace, id(), fmt.Sprintf("Invented notebook entry %d: the greenhouse key hangs by the water butt.", i+1), status, at, at, "Notes"); err != nil {
			return fmt.Errorf("memories: %w", err)
		}
	}
	turn := func(i int) (string, error) {
		if i >= len(ids) {
			return "", fmt.Errorf("the fixture has %d turns, and row %d needs one of its own", len(ids), i+1)
		}
		return ids[i], nil
	}
	for i := 0; i < f.Decisions; i++ {
		job, err := turn(i)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO decisions VALUES (?,?,?,'approved',?,?,?,NULL)", f.Workspace, id(), job, strings.Repeat("cd", 32), "Publish the reviewed local build: an invented tide page.", at); err != nil {
			return fmt.Errorf("decisions: %w", err)
		}
	}
	for i := 0; i < f.CalendarDrafts; i++ {
		if _, err := tx.Exec("INSERT INTO calendar_drafts(workspace,id,title,starts,ends,status,created,updated) VALUES (?,?,?,?,?,?,?,?)",
			f.Workspace, id(), "Invented: allotment committee", "2026-11-20T18:00", "2026-11-20T19:00", "draft", at, at); err != nil {
			return fmt.Errorf("calendar_drafts: %w", err)
		}
	}
	for i := 0; i < f.EmailDrafts; i++ {
		job, err := turn(i)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO email_drafts(workspace,id,source_job,recipient,subject,body,status,created,updated) VALUES (?,?,?,?,?,?,?,?,?)",
			f.Workspace, id(), job, "the allotment committee (invented)", "Invented: the water rota", "An invented draft about the water rota.", "draft", at, at); err != nil {
			return fmt.Errorf("email_drafts: %w", err)
		}
	}
	for i := 0; i < f.Proposals; i++ {
		job, err := turn(i)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO proposals(workspace,job_id,title,brief,created) VALUES (?,?,?,?,?)", f.Workspace, job, "Invented: a tide page", "An invented brief for a page of tide times.", at); err != nil {
			return fmt.Errorf("proposals: %w", err)
		}
	}
	return tx.Commit()
}
