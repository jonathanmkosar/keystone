package projects

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Size says a number of bytes for a person.
func Size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// AddFile keeps a file the person added to a project and returns it, with
// added true. A file the project has already is returned as it is, with
// added false. A file that is not taken is refused with ErrInvalid, and one
// past o.MaxFiles with ErrFull.
func AddFile(ctx context.Context, s *tenant.Store, o Options, projectID, name string, data []byte) (File, bool, error) {
	if _, err := Get(ctx, s, projectID); err != nil {
		return File{}, false, err
	}
	name = FileName(name)
	if int64(len(data)) > o.FileMaxBytes {
		return File{}, false, invalid("the file %s is %s, and the most a project file may be is %s", name, Size(int64(len(data))), Size(o.FileMaxBytes))
	}
	media, text, err := o.read()(name, data, o.TextExtensions)
	if err != nil {
		return File{}, false, err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if have, err := GetFile(ctx, s, projectID, hash); err == nil {
		return have, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return File{}, false, err
	}
	if err := o.blobs().Keep(s, hash, data); err != nil {
		return File{}, false, err
	}
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM project_files WHERE project_id = ?", projectID).Scan(&n); err != nil {
			return err
		}
		if n >= o.MaxFiles {
			return full("the project has %d files, as many as projects.max_files allows; remove one first", n)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_files(project_id, hash, name, media_type, size, text, added_at)
			VALUES(?, ?, ?, ?, ?, ?, ?)`, projectID, hash, name, media, len(data), text, stamp()); err != nil {
			return err
		}
		return touch(ctx, tx, projectID)
	})
	if err != nil {
		if rerr := o.blobs().Release(ctx, s, hash); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return File{}, false, err
	}
	// Something else that held the same file may have let it go, and the
	// file with it, between the write above and the row: it is written again.
	if !o.blobs().Has(s, hash) {
		if err := o.blobs().Keep(s, hash, data); err != nil {
			return File{}, false, err
		}
	}
	f, err := GetFile(ctx, s, projectID, hash)
	return f, err == nil, err
}

const fileColumns = "project_id, hash, name, media_type, size, text, sensitive, added_at"

func scanFile(row interface{ Scan(...any) error }) (File, error) {
	var f File
	var added string
	if err := row.Scan(&f.ProjectID, &f.ID, &f.Name, &f.MediaType, &f.Size, &f.text, &f.Sensitive, &added); err != nil {
		return File{}, err
	}
	f.Chars = len([]rune(f.text))
	t, err := parseTime(added)
	if err != nil {
		return File{}, fmt.Errorf("project file %s: %w", f.ID, err)
	}
	f.Added = t
	return f, nil
}

func noFile(id string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("project file %q: %w", id, ErrNotFound)
	}
	return err
}

// GetFile returns one file of a project, with its text.
func GetFile(ctx context.Context, s *tenant.Store, projectID, id string) (File, error) {
	if !IsFileID(id) {
		return File{}, noFile(id, sql.ErrNoRows)
	}
	f, err := scanFile(s.DB().QueryRowContext(ctx, "SELECT "+fileColumns+" FROM project_files WHERE project_id = ? AND hash = ?", projectID, id))
	return f, noFile(id, err)
}

// Files returns a project's files, with their text, in the order they were
// added.
func Files(ctx context.Context, s *tenant.Store, projectID string) ([]File, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT "+fileColumns+" FROM project_files WHERE project_id = ? ORDER BY added_at, name", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// MarkFile marks a file sensitive, or ordinary again, and returns it.
func MarkFile(ctx context.Context, s *tenant.Store, projectID, id string, sensitive bool) (File, error) {
	if !IsFileID(id) {
		return File{}, noFile(id, sql.ErrNoRows)
	}
	err := inTx(ctx, s, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE project_files SET sensitive = ? WHERE project_id = ? AND hash = ?", sensitive, projectID, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return noFile(id, sql.ErrNoRows)
		}
		return touch(ctx, tx, projectID)
	})
	if err != nil {
		return File{}, err
	}
	return GetFile(ctx, s, projectID, id)
}

// RemoveFile takes a file off a project, removes its bytes when nothing
// else holds them, and reports whether the project had it.
func RemoveFile(ctx context.Context, s *tenant.Store, o Options, projectID, id string) (bool, error) {
	if _, err := Get(ctx, s, projectID); err != nil {
		return false, err
	}
	if !IsFileID(id) {
		return false, nil
	}
	removed := false
	err := inTx(ctx, s, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM project_files WHERE project_id = ? AND hash = ?", projectID, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		removed = true
		return touch(ctx, tx, projectID)
	})
	if err != nil || !removed {
		return removed, err
	}
	return true, o.blobs().Release(ctx, s, id)
}

// Sensitive reports whether a project holds a file marked sensitive.
func Sensitive(ctx context.Context, s *tenant.Store, projectID string) (bool, error) {
	var n int
	err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM project_files WHERE project_id = ? AND sensitive = 1", projectID).Scan(&n)
	return n > 0, err
}
