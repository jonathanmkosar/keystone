// Package uploads keeps the files and images a person adds to a chat, reads
// the text out of the text files, readies them for each model call, and
// serves them back.
//
// A file is written to the tenant's files folder under its SHA-256, through
// the store's os.Root, so no name can leave the folder and no tenant's
// store reaches another's files. It is tied to its chat in chat_files, so
// deleting the chat removes it when no other chat holds it, and a row in
// uploads keeps its name, kind, media type, size and text. A message names
// an upload by its hash, in an image or file block's ref.
//
// Prepare turns a branch's image and file blocks into what one call is
// given: an image as its bytes only when every model the call can reach can
// see, a text file's text as an attachment, and a note in place of what is
// withheld. What each call was given is kept for the turn inspector.
//
// The package reads no setting: its limits and its words are passed in.
// docs/architecture/uploads.md describes it.
package uploads

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

var (
	// ErrNotFound is returned for a chat or an upload that is not in this
	// tenant's record.
	ErrNotFound = errors.New("uploads: not found")
	// ErrRefused is wrapped by the error for a file that is not taken: its
	// kind, its size or its text. The error's text says why.
	ErrRefused = errors.New("uploads: the file is not taken")
	// ErrInUse is returned for an upload a message of the chat holds.
	ErrInUse = errors.New("uploads: a message of the chat holds the file")
)

// Upload is one file added to a chat.
type Upload struct {
	// ID is the file's SHA-256 in lower-case hex: its name in the files
	// folder and the ref a message's block carries.
	ID     string `json:"id"`
	ChatID string `json:"chat_id"`
	// Name is the name the person's browser gave it, on one line. It is
	// shown and told to a model, and never used as a path.
	Name      string `json:"name"`
	Kind      Kind   `json:"kind"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	// Chars is how many characters of text were read from a text file.
	Chars   int       `json:"chars"`
	Created time.Time `json:"created"`
	// InUse says a message of the chat holds it, so it cannot be removed.
	InUse bool `json:"in_use"`

	text string
}

// Text is the text read from a text file, and empty for anything else.
func (u Upload) Text() string { return u.text }

const timeLayout = "2006-01-02T15:04:05.000Z"

func stamp() string { return time.Now().UTC().Format(timeLayout) }

// idShape is the shape of an upload's id. Anything else names no upload.
var idShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

// IsID reports whether s has the shape of an upload's id.
func IsID(s string) bool { return idShape.MatchString(s) }

func chatThere(s *tenant.Store, chatID string) error {
	if _, err := chat.Get(s, chatID); err != nil {
		if errors.Is(err, chat.ErrNotFound) {
			return fmt.Errorf("chat %q: %w", chatID, ErrNotFound)
		}
		return err
	}
	return nil
}

// Add keeps a file a person uploaded to a chat and returns it, with added
// true. A file that is already an upload of the chat is returned as it is,
// with added false. A file that is not taken is refused with ErrRefused.
func Add(ctx context.Context, s *tenant.Store, o Options, chatID, name string, data []byte) (up Upload, added bool, err error) {
	if err := chatThere(s, chatID); err != nil {
		return Upload{}, false, err
	}
	name = cleanName(name)
	if int64(len(data)) > o.MaxBytes {
		return Upload{}, false, refusef("the file %s is %s, and the most one upload may be is %s", name, Size(int64(len(data))), Size(o.MaxBytes))
	}
	kind, media, err := classify(name, data, o)
	if err != nil {
		return Upload{}, false, err
	}
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:])
	if have, err := Get(ctx, s, chatID, id); err == nil {
		return have, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Upload{}, false, err
	}
	text := ""
	if kind == KindText {
		text = Extract(media, data)
	}
	if err := keepFile(s, id, data); err != nil {
		return Upload{}, false, err
	}
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO uploads(chat_id, hash, name, kind, media_type, size, text, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, chatID, id, name, string(kind), media, len(data), text, stamp()); err != nil {
			return err
		}
		// The chat record's table of the files a chat holds (chat.md), so
		// deleting the chat removes the file.
		_, err := tx.Exec("INSERT OR IGNORE INTO chat_files(chat_id, name) VALUES(?, ?)", chatID, id)
		return err
	})
	if err != nil {
		dropUnheld(s, id)
		return Upload{}, false, err
	}
	// A chat that held the same file may have been deleted, and the file
	// with it, between the write above and the rows: it is written again.
	if _, statErr := s.Files().Stat(id); errors.Is(statErr, fs.ErrNotExist) {
		if err := keepFile(s, id, data); err != nil {
			return Upload{}, false, err
		}
	}
	up, err = Get(ctx, s, chatID, id)
	return up, err == nil, err
}

// keepFile writes a file to the files folder under its id, unless it is
// there already. It is written under another name first and then renamed,
// so a reader never finds half a file.
func keepFile(s *tenant.Store, id string, data []byte) error {
	if _, err := s.Files().Stat(id); err == nil {
		return nil
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	part := id + "." + hex.EncodeToString(b[:]) + ".part"
	if err := s.Files().WriteFile(part, data, 0o600); err != nil {
		return err
	}
	if err := s.Files().Rename(part, id); err != nil {
		s.Files().Remove(part)
		return err
	}
	return nil
}

// dropUnheld removes a file no chat and no project holds: a project's
// knowledge file of the same bytes is the same file (P9-01). A failure
// leaves the file, which a later upload of the same bytes finds and keeps.
func dropUnheld(s *tenant.Store, id string) {
	var n int
	const held = "SELECT (SELECT count(*) FROM chat_files WHERE name = ?) + (SELECT count(*) FROM project_files WHERE hash = ?)"
	if err := s.DB().QueryRow(held, id, id).Scan(&n); err == nil && n == 0 {
		s.Files().Remove(id)
	}
}

const columns = "chat_id, hash, name, kind, media_type, size, text, created_at"

func scan(sc interface{ Scan(...any) error }) (Upload, error) {
	var u Upload
	var kind, created string
	if err := sc.Scan(&u.ChatID, &u.ID, &u.Name, &kind, &u.MediaType, &u.Size, &u.text, &created); err != nil {
		return Upload{}, err
	}
	u.Kind = Kind(kind)
	u.Chars = len([]rune(u.text))
	t, err := time.Parse(timeLayout, created)
	if err != nil {
		return Upload{}, fmt.Errorf("upload %s: %w", u.ID, err)
	}
	u.Created = t
	return u, nil
}

// Get returns one upload of a chat.
func Get(ctx context.Context, s *tenant.Store, chatID, id string) (Upload, error) {
	if !IsID(id) {
		return Upload{}, fmt.Errorf("upload %q: %w", id, ErrNotFound)
	}
	u, err := scan(s.DB().QueryRowContext(ctx, "SELECT "+columns+" FROM uploads WHERE chat_id = ? AND hash = ?", chatID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, fmt.Errorf("upload %s of chat %q: %w", id, chatID, ErrNotFound)
	}
	if err != nil {
		return Upload{}, err
	}
	u.InUse, err = inUse(ctx, s, chatID, id)
	return u, err
}

// List returns a chat's uploads, the oldest first.
func List(ctx context.Context, s *tenant.Store, chatID string) ([]Upload, error) {
	if err := chatThere(s, chatID); err != nil {
		return nil, err
	}
	rows, err := s.DB().QueryContext(ctx, "SELECT "+columns+" FROM uploads WHERE chat_id = ? ORDER BY created_at, hash", chatID)
	if err != nil {
		return nil, err
	}
	var out []Upload
	for rows.Next() {
		u, err := scan(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].InUse, err = inUse(ctx, s, chatID, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// inUse reports whether a message of the chat names the file. A message is
// stored as JSON, where a block's ref is written "ref":"<id>", and an id is
// hex, so the text can be searched for as it is.
func inUse(ctx context.Context, s *tenant.Store, chatID, id string) (bool, error) {
	var one int
	err := s.DB().QueryRowContext(ctx, "SELECT 1 FROM messages WHERE chat_id = ? AND instr(content, ?) > 0 LIMIT 1", chatID, `"ref":"`+id+`"`).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Read returns an upload's bytes.
func Read(s *tenant.Store, id string) ([]byte, error) {
	if !IsID(id) {
		return nil, fmt.Errorf("upload %q: %w", id, ErrNotFound)
	}
	data, err := s.Files().ReadFile(id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the file of upload %s: %w", id, ErrNotFound)
	}
	return data, err
}

// Remove takes an upload off a chat, and removes its file when no other
// chat holds it. An upload a message of the chat holds is refused with
// ErrInUse: it goes when the chat is deleted. The file goes before the
// rows' transaction commits, as chat.Delete does, so a file is never left
// with nothing that names it.
func Remove(ctx context.Context, s *tenant.Store, chatID, id string) error {
	up, err := Get(ctx, s, chatID, id)
	if err != nil {
		return err
	}
	if up.InUse {
		return fmt.Errorf("%w: the file %s", ErrInUse, up.Name)
	}
	return inTx(ctx, s, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM uploads WHERE chat_id = ? AND hash = ?", chatID, id); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM chat_files WHERE chat_id = ? AND name = ?", chatID, id); err != nil {
			return err
		}
		var held int // a project's knowledge file of the same bytes holds it too (P9-01)
		if err := tx.QueryRow("SELECT (SELECT count(*) FROM chat_files WHERE name = ?) + (SELECT count(*) FROM project_files WHERE hash = ?)", id, id).Scan(&held); err != nil {
			return err
		}
		if held == 0 {
			if err := s.Files().Remove(id); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		return nil
	})
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

// Size writes a number of bytes for a person: 512 bytes, 34 kB, 5.2 MB.
func Size(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d bytes", n)
	case n < 1000*1000:
		return fmt.Sprintf("%d kB", (n+500)/1000)
	}
	mb := fmt.Sprintf("%.1f", float64(n)/1e6)
	return strings.TrimSuffix(mb, ".0") + " MB"
}
