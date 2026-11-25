package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

var (
	// ErrUnknown is wrapped when the tenant has no model of that name.
	ErrUnknown = errors.New("no model of that name")
	// ErrExists is wrapped when the tenant already has a model of that name.
	ErrExists = errors.New("a model of that name is already there")
	// ErrFull is wrapped when the tenant holds as many models as it may.
	ErrFull = errors.New("the registry is full")
)

// Limits are the values a caller reads from settings and passes in. This
// package reads no setting itself.
type Limits struct {
	// MaxModels is the most entries one tenant may hold: the setting
	// models.max_per_tenant.
	MaxModels int
}

// Entry is one row of a tenant's registry, as List returns it.
type Entry struct {
	Model     Model
	UpdatedAt time.Time
	// Problem is why the stored entry is refused today, and empty when it
	// is in use. A refused entry has only its name, and can be replaced or
	// removed.
	Problem string
}

const timeLayout = "2006-01-02T15:04:05Z"

// now is the clock, so a test can set it.
var now = time.Now

// Add stores a new model for the tenant. It refuses an entry Check refuses,
// a name the tenant already has, and one more than the limit.
func Add(ctx context.Context, s *tenant.Store, lim Limits, m Model) error {
	raw, err := Encode(m)
	if err != nil {
		return err
	}
	if lim.MaxModels <= 0 {
		return errors.New("models: the limit on models must be more than zero")
	}
	return inTx(ctx, s, func(tx *sql.Tx) error {
		var n, same int
		if err := tx.QueryRowContext(ctx, "SELECT count(*), coalesce(sum(name = ?), 0) FROM models", m.Name).Scan(&n, &same); err != nil {
			return err
		}
		if same > 0 {
			return fmt.Errorf("%w: %s", ErrExists, m.Name)
		}
		if n >= lim.MaxModels {
			return fmt.Errorf("%w: it holds %d models, and that is the most a tenant may have", ErrFull, n)
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO models(name, entry, updated_at) VALUES(?, ?, ?)", m.Name, string(raw), stamp())
		return err
	})
}

// Replace stores a changed entry over the one with the same name.
func Replace(ctx context.Context, s *tenant.Store, m Model) error {
	raw, err := Encode(m)
	if err != nil {
		return err
	}
	res, err := s.DB().ExecContext(ctx, "UPDATE models SET entry = ?, updated_at = ? WHERE name = ?", string(raw), stamp(), m.Name)
	if err != nil {
		return err
	}
	return oneRow(res, m.Name)
}

// Remove takes a model out of the tenant's registry.
func Remove(ctx context.Context, s *tenant.Store, name string) error {
	res, err := s.DB().ExecContext(ctx, "DELETE FROM models WHERE name = ?", name)
	if err != nil {
		return err
	}
	return oneRow(res, name)
}

// Get returns one model. A stored entry that is refused today comes back as
// an error that wraps ErrInvalid, so it is never used.
func Get(ctx context.Context, s *tenant.Store, name string) (Model, error) {
	var raw string
	err := s.DB().QueryRowContext(ctx, "SELECT entry FROM models WHERE name = ?", name).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Model{}, fmt.Errorf("%w: %s", ErrUnknown, short(name))
	}
	if err != nil {
		return Model{}, err
	}
	return read(name, raw)
}

// List returns every row of the tenant's registry, in order of name. A
// stored entry that is refused today is listed with its Problem.
func List(ctx context.Context, s *tenant.Store) ([]Entry, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT name, entry, updated_at FROM models ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var name, raw, at string
		if err := rows.Scan(&name, &raw, &at); err != nil {
			return nil, err
		}
		e := Entry{}
		if e.UpdatedAt, err = time.Parse(timeLayout, at); err != nil {
			return nil, fmt.Errorf("models: %s has a time that cannot be read", name)
		}
		if e.Model, err = read(name, raw); err != nil {
			e.Model, e.Problem = Model{Name: name}, err.Error()
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Usable returns the models in the tenant's registry that pass Check today,
// in order of name. It is what a model picker and the routing rules read.
func Usable(ctx context.Context, s *tenant.Store) ([]Model, error) {
	entries, err := List(ctx, s)
	if err != nil {
		return nil, err
	}
	var out []Model
	for _, e := range entries {
		if e.Problem == "" {
			out = append(out, e.Model)
		}
	}
	return out, nil
}

// read checks a stored entry. The rules may have changed since it was
// stored, and a row can be written with SQL, so nothing is trusted because
// it is in the table.
func read(name, raw string) (Model, error) {
	m, err := Decode([]byte(raw))
	if err != nil {
		return Model{}, fmt.Errorf("the stored entry for %s is refused: %w", name, err)
	}
	if m.Name != name {
		return Model{}, fmt.Errorf("%w: the stored entry for %s is named %s", ErrInvalid, name, m.Name)
	}
	return m, nil
}

func stamp() string { return now().UTC().Format(timeLayout) }

func oneRow(res sql.Result, name string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrUnknown, short(name))
	}
	return nil
}

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
