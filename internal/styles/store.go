package styles

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

// now is the clock. Tests replace it.
var now = time.Now

func stamp() string { return now().UTC().Format(timeLayout) }

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

// newID is a new own style's id: sixteen hexadecimal characters.
func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

const styleColumns = "id, name, instructions, created_at, updated_at"

func scanStyle(row interface{ Scan(...any) error }) (Style, error) {
	var st Style
	var created, changed string
	if err := row.Scan(&st.ID, &st.Name, &st.Instructions, &created, &changed); err != nil {
		return Style{}, err
	}
	var err error
	if st.Created, err = time.Parse(timeLayout, created); err != nil {
		return Style{}, fmt.Errorf("style %s: %w", st.ID, err)
	}
	if st.Changed, err = time.Parse(timeLayout, changed); err != nil {
		return Style{}, fmt.Errorf("style %s: %w", st.ID, err)
	}
	return st, nil
}

func noStyle(id string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("style %q: %w", id, ErrNotFound)
	}
	return err
}

// builtIn finds a built-in style by its id.
func builtIn(o Options, id string) (Style, bool) {
	for _, st := range o.BuiltIn {
		if st.ID == id {
			return st, true
		}
	}
	return Style{}, false
}

// nameTaken refuses a name another style of the person's has, built-in or
// their own, whatever its letter case. except is the style being renamed.
func nameTaken(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, o Options, name, except string) error {
	for _, st := range o.BuiltIn {
		if strings.EqualFold(st.Name, name) {
			return invalid("there is a built-in style named %q", st.Name)
		}
	}
	rows, err := q.QueryContext(ctx, "SELECT id, name FROM styles")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, have string
		if err := rows.Scan(&id, &have); err != nil {
			return err
		}
		if id != except && strings.EqualFold(have, name) {
			return invalid("you have a style named %q already", have)
		}
	}
	return rows.Err()
}

// Create makes a style of the person's own, with a name and instructions.
// Past o.MaxStyles it is ErrFull.
func Create(ctx context.Context, s *tenant.Store, o Options, name, instructions string) (Style, error) {
	name, err := cleanName(name, o.NameMaxChars)
	if err != nil {
		return Style{}, err
	}
	if instructions, err = cleanInstructions(instructions, o.InstructionsMaxChars); err != nil {
		return Style{}, err
	}
	id, err := newID()
	if err != nil {
		return Style{}, err
	}
	var st Style
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM styles").Scan(&n); err != nil {
			return err
		}
		if n >= o.MaxStyles {
			return full("you have %d styles of your own, as many as styles.max_styles allows; delete one first", n)
		}
		if err := nameTaken(ctx, tx, o, name, ""); err != nil {
			return err
		}
		at := stamp()
		st, err = scanStyle(tx.QueryRowContext(ctx,
			"INSERT INTO styles(id, name, instructions, created_at, updated_at) VALUES(?, ?, ?, ?, ?) RETURNING "+styleColumns,
			id, name, instructions, at, at))
		return err
	})
	return st, err
}

// Get returns one style: a built-in one, or one of the person's own.
func Get(ctx context.Context, s *tenant.Store, o Options, id string) (Style, error) {
	if st, ok := builtIn(o, id); ok {
		return st, nil
	}
	if !IsOwnID(id) {
		return Style{}, fmt.Errorf("style %q: %w", id, ErrNotFound)
	}
	st, err := scanStyle(s.DB().QueryRowContext(ctx, "SELECT "+styleColumns+" FROM styles WHERE id = ?", id))
	return st, noStyle(id, err)
}

// List returns every style a person may choose: the built-in ones in their
// order, then their own by name.
func List(ctx context.Context, s *tenant.Store, o Options) ([]Style, error) {
	out := append([]Style{}, o.BuiltIn...)
	rows, err := s.DB().QueryContext(ctx, "SELECT "+styleColumns+" FROM styles ORDER BY name COLLATE NOCASE, seq")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		st, err := scanStyle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Count returns how many styles of their own the person has.
func Count(ctx context.Context, s *tenant.Store) (int, error) {
	var n int
	err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM styles").Scan(&n)
	return n, err
}

// Change is what a person changes of one of their own styles. A field that
// is nil is left as it is.
type Change struct {
	Name         *string
	Instructions *string
}

// Update changes one of the person's own styles and returns it. A built-in
// style is ErrBuiltIn. Every chat that uses the style is given the new
// words from its next call.
func Update(ctx context.Context, s *tenant.Store, o Options, id string, c Change) (Style, error) {
	if _, ok := builtIn(o, id); ok {
		return Style{}, fmt.Errorf("%w: %q cannot be changed; make a style of your own instead", ErrBuiltIn, id)
	}
	if !IsOwnID(id) {
		return Style{}, fmt.Errorf("style %q: %w", id, ErrNotFound)
	}
	sets, args := []string{}, []any{}
	var name string
	if c.Name != nil {
		var err error
		if name, err = cleanName(*c.Name, o.NameMaxChars); err != nil {
			return Style{}, err
		}
		sets, args = append(sets, "name = ?"), append(args, name)
	}
	if c.Instructions != nil {
		text, err := cleanInstructions(*c.Instructions, o.InstructionsMaxChars)
		if err != nil {
			return Style{}, err
		}
		sets, args = append(sets, "instructions = ?"), append(args, text)
	}
	if len(sets) == 0 {
		return Get(ctx, s, o, id)
	}
	sets, args = append(sets, "updated_at = ?"), append(args, stamp(), id)
	var st Style
	err := inTx(ctx, s, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM styles WHERE id = ?", id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return noStyle(id, sql.ErrNoRows)
		}
		if c.Name != nil {
			if err := nameTaken(ctx, tx, o, name, id); err != nil {
				return err
			}
		}
		var err error
		st, err = scanStyle(tx.QueryRowContext(ctx,
			"UPDATE styles SET "+strings.Join(sets, ", ")+" WHERE id = ? RETURNING "+styleColumns, args...))
		return noStyle(id, err)
	})
	return st, err
}

// Delete removes one of the person's own styles. A chat that used it has no
// style of its own any more, and uses the person's default from its next
// turn. A built-in style is ErrBuiltIn.
func Delete(ctx context.Context, s *tenant.Store, o Options, id string) error {
	if _, ok := builtIn(o, id); ok {
		return fmt.Errorf("%w: %q cannot be deleted", ErrBuiltIn, id)
	}
	if !IsOwnID(id) {
		return fmt.Errorf("style %q: %w", id, ErrNotFound)
	}
	return inTx(ctx, s, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM styles WHERE id = ?", id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return noStyle(id, sql.ErrNoRows)
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM chat_styles WHERE style_id = ?", id)
		return err
	})
}

// Of returns the id of a chat's own style, and false when it has none.
func Of(ctx context.Context, s *tenant.Store, chatID string) (string, bool, error) {
	var id string
	err := s.DB().QueryRowContext(ctx, "SELECT style_id FROM chat_styles WHERE chat_id = ?", chatID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// Choose gives a chat a style of its own, and returns the style. Both must
// be in this tenant's record: a chat or a style of another tenant is
// ErrNotFound. The chat's next call carries the new style.
func Choose(ctx context.Context, s *tenant.Store, o Options, chatID, styleID string) (Style, error) {
	st, err := Get(ctx, s, o, styleID)
	if err != nil {
		return Style{}, err
	}
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM chats WHERE id = ?", chatID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("chat %q: %w", chatID, ErrNotFound)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO chat_styles(chat_id, style_id, chosen_at) VALUES(?, ?, ?)
			ON CONFLICT(chat_id) DO UPDATE SET style_id = excluded.style_id, chosen_at = excluded.chosen_at`, chatID, st.ID, stamp())
		return err
	})
	if err != nil {
		return Style{}, err
	}
	return st, nil
}

// Keep makes a style a chat's own when the chat has none, and reports
// whether it did. A chat's first turn keeps the default it used this way,
// so a later change of the default leaves the chat as it was. A chat that
// is not in this tenant's record is ErrNotFound.
func Keep(ctx context.Context, s *tenant.Store, chatID, styleID string) (bool, error) {
	kept := false
	err := inTx(ctx, s, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM chats WHERE id = ?", chatID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("chat %q: %w", chatID, ErrNotFound)
		}
		res, err := tx.ExecContext(ctx, "INSERT INTO chat_styles(chat_id, style_id, chosen_at) VALUES(?, ?, ?) ON CONFLICT(chat_id) DO NOTHING", chatID, styleID, stamp())
		if err != nil {
			return err
		}
		n64, err := res.RowsAffected()
		kept = n64 > 0
		return err
	})
	return kept, err
}

// Resolve returns the style a chat uses and where it comes from: the chat's
// own (SourceChat), else the style defaultID names (SourceDefault), else
// none (SourceNone). A chat's own style that no longer exists, as a
// built-in style taken out of styles.builtin, counts as none of its own.
func Resolve(ctx context.Context, s *tenant.Store, o Options, chatID, defaultID string) (Style, string, error) {
	own, ok, err := Of(ctx, s, chatID)
	if err != nil {
		return Style{}, "", err
	}
	if ok {
		st, err := Get(ctx, s, o, own)
		if err == nil {
			return st, SourceChat, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Style{}, "", err
		}
	}
	if defaultID != "" {
		st, err := Get(ctx, s, o, defaultID)
		if err == nil {
			return st, SourceDefault, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Style{}, "", err
		}
	}
	return Style{}, SourceNone, nil
}
