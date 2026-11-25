package personmap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// ErrTooLong means a profile is longer than the limit it was checked
// against.
var ErrTooLong = errors.New("personmap: the profile is too long")

// Profile is the short text about a person that starts every call: a few
// facts and preferences, in their own words.
//
// It goes to every model the person uses, hosted ones too. It is not held
// in a space and carries no label, so what belongs in a sensitive space
// does not belong in it.
type Profile struct {
	Text string
	// Updated is when the text was last set. It is zero when the person
	// has no profile.
	Updated time.Time
}

// ReadProfile returns the tenant's profile. A person who has written none
// has an empty one.
func ReadProfile(ctx context.Context, t *tenant.Store) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if t == nil {
		return Profile{}, errors.New("personmap: no tenant")
	}
	var p Profile
	var updated string
	err := t.DB().QueryRowContext(ctx, "SELECT text, updated_at FROM person_profile WHERE id = 1").Scan(&p.Text, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, nil
	}
	if err != nil {
		return Profile{}, fmt.Errorf("personmap: reading the profile: %w", err)
	}
	p.Updated, _ = time.Parse(time.RFC3339, updated)
	return p, nil
}

// SetProfile replaces the tenant's profile and returns it as kept. Space
// around the text is dropped and line ends are made plain. An empty text
// clears the profile. A text of more than maxChars characters is refused
// with ErrTooLong and the old profile stays.
func SetProfile(ctx context.Context, t *tenant.Store, text string, maxChars int) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if t == nil {
		return Profile{}, errors.New("personmap: no tenant")
	}
	if maxChars < 1 || maxChars > MaxProfileChars {
		return Profile{}, fmt.Errorf("%w: %s is from 1 to %d", ErrLimits, SettingProfileMaxChars, MaxProfileChars)
	}
	text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return Profile{}, errors.New("personmap: the profile is not plain text")
	}
	if n := utf8.RuneCountInString(text); n > maxChars {
		return Profile{}, fmt.Errorf("%w: it has %d characters and the most is %d", ErrTooLong, n, maxChars)
	}
	if text == "" {
		if _, err := t.DB().ExecContext(ctx, "DELETE FROM person_profile WHERE id = 1"); err != nil {
			return Profile{}, fmt.Errorf("personmap: clearing the profile: %w", err)
		}
		return Profile{}, nil
	}
	p := Profile{Text: text, Updated: now().UTC().Truncate(time.Second)}
	_, err := t.DB().ExecContext(ctx, `INSERT INTO person_profile(id, text, updated_at) VALUES(1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET text = excluded.text, updated_at = excluded.updated_at`,
		p.Text, p.Updated.Format(time.RFC3339))
	if err != nil {
		return Profile{}, fmt.Errorf("personmap: keeping the profile: %w", err)
	}
	return p, nil
}

// cut shortens text to at most max characters, on a character boundary.
func cut(text string, max int) string {
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	return strings.TrimSpace(string([]rune(text)[:max]))
}
