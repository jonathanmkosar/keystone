package tenant

import (
	"database/sql"
	"embed"
	"fmt"
	"regexp"
	"sort"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// A migration file is named by the date and time it was written, so two
// lanes can add migrations at once without picking the same number.
var migrationName = regexp.MustCompile(`^\d{8}T\d{6}_[a-z0-9_]+\.sql$`)

type migration struct {
	name string
	sql  string
}

// migrations returns the migration files in name order, which is date order.
func migrations() ([]migration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		if !migrationName.MatchString(e.Name()) {
			return nil, fmt.Errorf("migration %s: a name is a date and time, an underscore and a few words, such as 20261124T120000_tenant_core.sql", e.Name())
		}
		raw, err := migrationFiles.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{e.Name(), string(raw)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	for i := 1; i < len(out); i++ {
		if out[i].name[:15] == out[i-1].name[:15] {
			return nil, fmt.Errorf("migrations %s and %s share a date and time", out[i-1].name, out[i].name)
		}
	}
	return out, nil
}

// migrate applies every migration the database has not seen, in name order.
// It never goes back, and each migration runs in its own transaction, so a
// failure leaves the database at the last good one.
func migrate(db *sql.DB) error {
	all, err := migrations()
	if err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
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
	for _, m := range all {
		known[m.name] = true
	}
	for name := range applied {
		if !known[name] {
			return fmt.Errorf("the database has migration %s, which this binary does not know: use a newer binary", name)
		}
	}
	for _, m := range all {
		if applied[m.name] {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(name, applied_at) VALUES(?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))", m.name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Migrations returns the names of the migrations applied to this tenant's
// database, in the order they ran.
func (s *Store) Migrations() ([]string, error) {
	rows, err := s.db.Query("SELECT name FROM schema_migrations ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
