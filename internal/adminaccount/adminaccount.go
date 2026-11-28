// Package adminaccount keeps the admin accounts (P15-01, decision 0650).
//
// An admin is a separate account, not a person: it lives in its own
// database, data/admin/admin.db, with its password and passkeys, its
// sessions and the admin audit log, and has no tenant database, so it has
// nothing of a person's to show. Accounts are made and removed from the
// command line alone (go run ./tools/admin); no route can.
//
// An admin's session token has a form of its own, ksa_ and 32 random bytes
// as hex, so the API's door tells it from a person's (kss_) before anything
// is read. A session lasts admin.session_for and ends after admin.idle_for
// without use. Passwords and passkeys follow a person's rules, through the
// same code in internal/signin: the same hashing, the same count of wrong
// tries and the same wait.
//
// Every function here works on one opened Store, which the caller closes.
// The database is opened for a use and closed after it, as a person's is,
// so the command line and a running server can both use it.
package adminaccount

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	_ "modernc.org/sqlite"

	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/settings"
)

const (
	// Folder is the admin database's folder, under the data folder.
	Folder = "admin"
	// File is the admin database's file name.
	File = "admin.db"
	// TokenPrefix starts every admin session token.
	TokenPrefix = "ksa_"
	// ActorPrefix starts the actor of an admin's entries, in the admin
	// audit log and in a person's: the account's id follows it.
	ActorPrefix = "admin:"
	// ActorUnknown is the actor of a sign-in for an account that is not
	// there.
	ActorUnknown = "admin"

	stampLayout = "2006-01-02T15:04:05Z"
)

// Path returns where the admin database of the data folder dataDir is.
func Path(dataDir string) string { return filepath.Join(dataDir, Folder, File) }

// Options are what admin sign-in runs with: the person's sign-in rules, and
// the two admin settings in settings/core.json.
type Options struct {
	// SignIn holds the password and passkey rules, the signin. settings.
	SignIn signin.Options
	// SessionFor is how long a session lasts: admin.session_for.
	SessionFor time.Duration
	// IdleFor is how long a session may go unused before it ends:
	// admin.idle_for.
	IdleFor time.Duration
}

// Defaults returns the options as the settings registry ships them.
func Defaults() (Options, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Options{}, err
	}
	return ReadOptions(func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	})
}

// ReadOptions builds the options from wherever setting values are kept.
func ReadOptions(get func(key string) (string, bool)) (Options, error) {
	var o Options
	var err error
	if o.SignIn, err = signin.ReadOptions(get); err != nil {
		return Options{}, err
	}
	for key, into := range map[string]*time.Duration{"admin.session_for": &o.SessionFor, "admin.idle_for": &o.IdleFor} {
		v, ok := get(key)
		if !ok {
			return Options{}, fmt.Errorf("the %s setting is missing", key)
		}
		if *into, err = time.ParseDuration(v); err != nil {
			return Options{}, fmt.Errorf("%s: %w", key, err)
		}
	}
	return o, o.check()
}

func (o Options) check() error {
	switch {
	case o.SessionFor <= 0:
		return errors.New("admin.session_for must be longer than nothing")
	case o.IdleFor <= 0:
		return errors.New("admin.idle_for must be longer than nothing")
	}
	return nil
}

// now is the clock. Tests move it.
var now = func() time.Time { return time.Now().UTC() }

func stamp(t time.Time) string { return t.UTC().Format(stampLayout) }

func parseStamp(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse(stampLayout, v)
}

// Store is the admin database, opened.
type Store struct {
	db *sql.DB
}

// Open opens the admin database of the data folder dataDir, making its
// folder and the database when they are not there, and brings it up to
// date. The caller closes it.
func Open(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("adminaccount: no data folder was given")
	}
	if err := os.MkdirAll(filepath.Join(dataDir, Folder), 0o700); err != nil {
		return nil, err
	}
	path := Path(dataDir)
	dsn := "file:" + filepath.ToSlash(path) +
		"?_txlock=immediate&_pragma=journal_mode(wal)&_pragma=synchronous(normal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

//go:embed migrations/*.sql
var migrationFiles embed.FS

var migrationName = regexp.MustCompile(`^\d{8}T\d{6}_[a-z0-9_]+\.sql$`)

// migrate applies every migration the database has not seen, in name
// order, each in its own transaction, as a person's database does.
func migrate(db *sql.DB) error {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !migrationName.MatchString(e.Name()) {
			return fmt.Errorf("migration %s: a name is a date and time, an underscore and a few words", e.Name())
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	applied := map[string]bool{}
	rows, err := db.Query("SELECT name FROM schema_migrations")
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		applied[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	for name := range applied {
		if !known[name] {
			return fmt.Errorf("the admin database has migration %s, which this binary does not know: use a newer binary", name)
		}
	}
	for _, name := range names {
		if applied[name] {
			continue
		}
		raw, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(raw)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(name, applied_at) VALUES(?, ?)", name, stamp(now())); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
