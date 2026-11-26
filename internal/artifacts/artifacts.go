// Package artifacts keeps the documents a model makes in a chat: code,
// HTML, React components, SVG pictures, Mermaid diagrams and Markdown,
// each with every version it has had.
//
// An artifact belongs to the chat it was made in. Its rows are in the
// tenant's database (artifacts and artifact_versions), and each version's
// content is a file in the tenant's artifacts folder, <chat>/<artifact>/v<n>,
// written through the store's os.Root, so no name can leave the folder and
// no tenant's store reaches another's artifacts. Deleting the chat removes
// its folder and its rows. A version is never changed: an update, and a
// restore of an older version, each write a new one.
//
// A version written while its chat is held for content from a sensitive
// space is marked sensitive, and every artifact of a held chat reads as
// sensitive (fixed rule 3): the person is told, on the panel and on the
// publish link. An artifact's content reaches no model call but those of
// its own chat, which is held already.
//
// The tools a model makes and changes artifacts with are in tool.go, the
// routes in routes.go, and the sandboxed page each is shown in in
// sandbox.go. The package reads no setting: its limits come in Options.
// docs/architecture/artifacts.md describes it.
package artifacts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

var (
	// ErrNotFound is returned for a chat, an artifact or a version that is
	// not in this tenant's record.
	ErrNotFound = errors.New("artifacts: not found")
	// ErrRefused is wrapped by the error for something that is not written:
	// its type, its title, its size, or a change that does not apply. The
	// error's text says why.
	ErrRefused = errors.New("artifacts: refused")
)

func refusef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, args...))
}

// Kind is what an artifact is drawn as.
type Kind string

const (
	KindCode     Kind = "code"
	KindHTML     Kind = "html"
	KindReact    Kind = "react"
	KindSVG      Kind = "svg"
	KindMermaid  Kind = "mermaid"
	KindMarkdown Kind = "markdown"
)

// Kinds are every kind, in the order the tools list them.
var Kinds = []Kind{KindCode, KindHTML, KindReact, KindSVG, KindMermaid, KindMarkdown}

// ParseKind reads a kind.
func ParseKind(s string) (Kind, error) {
	k := Kind(strings.ToLower(strings.TrimSpace(s)))
	for _, known := range Kinds {
		if k == known {
			return k, nil
		}
	}
	names := make([]string, len(Kinds))
	for i, k := range Kinds {
		names[i] = string(k)
	}
	return "", refusef("%q is not an artifact's type; it is one of %s", s, strings.Join(names, ", "))
}

// Artifact is one artifact, as its newest version has it.
type Artifact struct {
	ID     string `json:"id"`
	ChatID string `json:"chat_id"`
	Type   Kind   `json:"type"`
	// Language is a code artifact's language, such as python. Empty for
	// every other type.
	Language string `json:"language,omitempty"`
	Title    string `json:"title"`
	// Latest is the newest version's number.
	Latest  int       `json:"latest"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	// Sensitive says a version was written while the chat was held for
	// content from a sensitive space, or the chat is held now.
	Sensitive bool `json:"sensitive"`
	// Link is the publish link: the address on this Keystone that shows
	// the artifact alone, to the person signed in and to no one else.
	Link string `json:"link"`
}

// Origins of a version.
const (
	OriginModel   = "model"
	OriginRestore = "restore"
)

// Version is one version of an artifact, without its content.
type Version struct {
	ArtifactID string `json:"artifact_id"`
	Version    int    `json:"version"`
	Title      string `json:"title"`
	Size       int    `json:"size"`
	SHA256     string `json:"sha256"`
	// Origin is model for a version a model wrote, restore for one the
	// person made by restoring RestoredFrom.
	Origin       string    `json:"origin"`
	RestoredFrom int       `json:"restored_from,omitempty"`
	Sensitive    bool      `json:"sensitive"`
	RequestID    string    `json:"request_id,omitempty"`
	Created      time.Time `json:"created"`
}

// Draft is a new artifact.
type Draft struct {
	Type     Kind
	Language string
	Title    string
	Content  string
}

// Change is a new version of an artifact: either its whole new Content, or
// one replacement, OldText by NewText, where OldText appears in the newest
// version exactly once. Title, when not empty, is its new title.
type Change struct {
	Content    *string
	OldText    string
	NewText    string
	Title      string
	HasOldText bool
}

const timeLayout = "2006-01-02T15:04:05.000Z"

// now is the clock. Tests replace it.
var now = time.Now

func stamp() string { return now().UTC().Format(timeLayout) }

var (
	idShape       = regexp.MustCompile(`^[0-9a-f]{16}$`)
	languageShape = regexp.MustCompile(`^[a-z0-9][a-z0-9+#._-]{0,31}$`)
)

// IsID reports whether s has the shape of an artifact's id.
func IsID(s string) bool { return idShape.MatchString(s) }

// LinkOf is an artifact's publish link.
func LinkOf(id string) string { return "/artifacts/" + id }

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// fileOf is where a version's content is kept in the artifacts folder. The
// chat's id comes from the record and the artifact's is checked to be hex,
// and the folder refuses any path that leaves it besides.
func fileOf(chatID, id string, version int) string {
	return path.Join(chatID, id, "v"+strconv.Itoa(version))
}

func chatThere(s *tenant.Store, chatID string) error {
	if _, err := chat.Get(s, chatID); err != nil {
		if errors.Is(err, chat.ErrNotFound) {
			return fmt.Errorf("chat %q: %w", chatID, ErrNotFound)
		}
		return err
	}
	return nil
}

// cleanTitle is a title on one line, checked for its length.
func cleanTitle(o Options, title string) (string, error) {
	title = strings.Join(strings.Fields(title), " ")
	if n := utf8.RuneCountInString(title); n == 0 || n > o.TitleMaxChars {
		return "", refusef("a title is 1 to %d characters", o.TitleMaxChars)
	}
	return title, nil
}

// checkContent refuses content that is empty, too large or not UTF-8.
func checkContent(o Options, content string) error {
	switch {
	case strings.TrimSpace(content) == "":
		return refusef("an artifact's content is empty")
	case len(content) > o.MaxBytes:
		return refusef("the content is %d bytes, and the most one artifact may be is %d bytes; make it smaller, or split it into several artifacts", len(content), o.MaxBytes)
	case !utf8.ValidString(content):
		return refusef("the content is not UTF-8 text")
	}
	return nil
}

// cleanLanguage is a code artifact's language in lower case, or text when
// none is given. Every other type has none.
func cleanLanguage(k Kind, language string) (string, error) {
	if k != KindCode {
		return "", nil
	}
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" {
		return "text", nil
	}
	if !languageShape.MatchString(language) {
		return "", refusef("%q is not a language's name, such as python, go or typescript", language)
	}
	return language, nil
}

func digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Create keeps a new artifact in a chat, as its version 1, written for the
// API call requestID names.
func Create(ctx context.Context, s *tenant.Store, o Options, chatID string, d Draft, requestID string) (Artifact, Version, error) {
	if err := chatThere(s, chatID); err != nil {
		return Artifact{}, Version{}, err
	}
	kind, err := ParseKind(string(d.Type))
	if err != nil {
		return Artifact{}, Version{}, err
	}
	language, err := cleanLanguage(kind, d.Language)
	if err != nil {
		return Artifact{}, Version{}, err
	}
	title, err := cleanTitle(o, d.Title)
	if err != nil {
		return Artifact{}, Version{}, err
	}
	if err := checkContent(o, d.Content); err != nil {
		return Artifact{}, Version{}, err
	}
	sensitive, err := recall.Held(ctx, s, chatID)
	if err != nil {
		return Artifact{}, Version{}, err
	}
	id, err := newID()
	if err != nil {
		return Artifact{}, Version{}, err
	}
	at := stamp()
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO artifacts(id, chat_id, kind, language, title, latest, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, 1, ?, ?)`, id, chatID, string(kind), language, title, at, at); err != nil {
			return err
		}
		return writeVersion(tx, s, chatID, id, 1, title, d.Content, OriginModel, 0, sensitive, requestID, at)
	})
	if err != nil {
		s.Artifacts().RemoveAll(path.Join(chatID, id))
		return Artifact{}, Version{}, err
	}
	return getBoth(ctx, s, id, 1)
}

// writeVersion writes a version's file and its row, inside the caller's
// transaction, which holds the database's write lock, so no other write
// can take the same number. The file is written under another name first
// and then renamed, so a reader never finds half a version.
func writeVersion(tx *sql.Tx, s *tenant.Store, chatID, id string, version int, title, content, origin string, restoredFrom int, sensitive bool, requestID, at string) error {
	dir := path.Join(chatID, id)
	if err := s.Artifacts().MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	name := fileOf(chatID, id, version)
	part := name + "." + hex.EncodeToString(b[:]) + ".part"
	if err := s.Artifacts().WriteFile(part, []byte(content), 0o600); err != nil {
		return err
	}
	if err := s.Artifacts().Rename(part, name); err != nil {
		s.Artifacts().Remove(part)
		return err
	}
	_, err := tx.Exec(`INSERT INTO artifact_versions(artifact_id, version, title, size, sha256, origin, restored_from, sensitive, request_id, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, version, title, len(content), digest(content), origin, restoredFrom, sensitive, requestID, at)
	if err != nil {
		s.Artifacts().Remove(name)
	}
	return err
}

// Update writes a new version of an artifact of the chat chatID names. An
// artifact of another chat is not found: a model changes only the
// artifacts of the chat it is in.
func Update(ctx context.Context, s *tenant.Store, o Options, chatID, id string, c Change, requestID string) (Artifact, Version, error) {
	a, err := Get(ctx, s, id)
	if err != nil {
		return Artifact{}, Version{}, err
	}
	if a.ChatID != chatID {
		return Artifact{}, Version{}, fmt.Errorf("artifact %s is not of this chat: %w", id, ErrNotFound)
	}
	_, current, err := Content(ctx, s, id, a.Latest)
	if err != nil {
		return Artifact{}, Version{}, err
	}
	var next string
	switch {
	case c.Content != nil && c.HasOldText:
		return Artifact{}, Version{}, refusef("give either the whole new content, or old_text and new_text, not both")
	case c.Content != nil:
		next = *c.Content
	case c.HasOldText:
		if c.OldText == "" {
			return Artifact{}, Version{}, refusef("old_text is empty; give the exact text to replace")
		}
		switch n := strings.Count(current, c.OldText); n {
		case 0:
			return Artifact{}, Version{}, refusef("old_text does not appear in version %d of the artifact; read it with read_artifact and give text it holds, exactly", a.Latest)
		case 1:
			next = strings.Replace(current, c.OldText, c.NewText, 1)
		default:
			return Artifact{}, Version{}, refusef("old_text appears %d times in version %d of the artifact; give a longer piece that appears once", n, a.Latest)
		}
	default:
		return Artifact{}, Version{}, refusef("give the whole new content, or old_text and new_text")
	}
	title := a.Title
	if strings.TrimSpace(c.Title) != "" {
		if title, err = cleanTitle(o, c.Title); err != nil {
			return Artifact{}, Version{}, err
		}
	}
	if next == current && title == a.Title {
		return Artifact{}, Version{}, refusef("the new version reads the same as version %d, so nothing was written", a.Latest)
	}
	return addVersion(ctx, s, o, a, title, next, OriginModel, 0, requestID)
}

// Restore writes an older version of an artifact again, as its newest. The
// older version stays as it was.
func Restore(ctx context.Context, s *tenant.Store, o Options, id string, version int, requestID string) (Artifact, Version, error) {
	a, err := Get(ctx, s, id)
	if err != nil {
		return Artifact{}, Version{}, err
	}
	old, content, err := Content(ctx, s, id, version)
	if err != nil {
		return Artifact{}, Version{}, err
	}
	if version == a.Latest {
		return Artifact{}, Version{}, refusef("version %d is already the newest", version)
	}
	return addVersion(ctx, s, o, a, old.Title, content, OriginRestore, version, requestID)
}

// addVersion writes the next version of a, and lets go of the oldest when
// more are kept than the options keep.
func addVersion(ctx context.Context, s *tenant.Store, o Options, a Artifact, title, content, origin string, restoredFrom int, requestID string) (Artifact, Version, error) {
	if err := checkContent(o, content); err != nil {
		return Artifact{}, Version{}, err
	}
	sensitive, err := recall.Held(ctx, s, a.ChatID)
	if err != nil {
		return Artifact{}, Version{}, err
	}
	var version int
	var dropped []int
	at := stamp()
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		if err := tx.QueryRow("SELECT latest + 1 FROM artifacts WHERE id = ?", a.ID).Scan(&version); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("artifact %s: %w", a.ID, ErrNotFound)
			}
			return err
		}
		if err := writeVersion(tx, s, a.ChatID, a.ID, version, title, content, origin, restoredFrom, sensitive, requestID, at); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE artifacts SET latest = ?, title = ?, updated_at = ? WHERE id = ?", version, title, at, a.ID); err != nil {
			return err
		}
		if o.VersionsKept > 0 {
			rows, err := tx.Query("SELECT version FROM artifact_versions WHERE artifact_id = ? AND version <= ?", a.ID, version-o.VersionsKept)
			if err != nil {
				return err
			}
			for rows.Next() {
				var v int
				if err := rows.Scan(&v); err != nil {
					rows.Close()
					return err
				}
				dropped = append(dropped, v)
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if _, err := tx.Exec("DELETE FROM artifact_versions WHERE artifact_id = ? AND version <= ?", a.ID, version-o.VersionsKept); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Artifact{}, Version{}, err
	}
	// A file no row names any longer is removed after the rows; one that
	// cannot be is harmless, and goes with the chat.
	for _, v := range dropped {
		s.Artifacts().Remove(fileOf(a.ChatID, a.ID, v))
	}
	return getBoth(ctx, s, a.ID, version)
}

func getBoth(ctx context.Context, s *tenant.Store, id string, version int) (Artifact, Version, error) {
	a, err := Get(ctx, s, id)
	if err != nil {
		return Artifact{}, Version{}, err
	}
	v, err := getVersion(ctx, s, id, version)
	return a, v, err
}

const artifactColumns = `a.id, a.chat_id, a.kind, a.language, a.title, a.latest, a.created_at, a.updated_at,
	EXISTS (SELECT 1 FROM artifact_versions v WHERE v.artifact_id = a.id AND v.sensitive = 1)`

func scanArtifact(sc interface{ Scan(...any) error }) (Artifact, error) {
	var a Artifact
	var kind, created, updated string
	if err := sc.Scan(&a.ID, &a.ChatID, &kind, &a.Language, &a.Title, &a.Latest, &created, &updated, &a.Sensitive); err != nil {
		return Artifact{}, err
	}
	a.Type = Kind(kind)
	a.Link = LinkOf(a.ID)
	var err error
	if a.Created, err = time.Parse(timeLayout, created); err != nil {
		return Artifact{}, fmt.Errorf("artifact %s: %w", a.ID, err)
	}
	if a.Updated, err = time.Parse(timeLayout, updated); err != nil {
		return Artifact{}, fmt.Errorf("artifact %s: %w", a.ID, err)
	}
	return a, nil
}

// Get returns one artifact of this tenant.
func Get(ctx context.Context, s *tenant.Store, id string) (Artifact, error) {
	if !IsID(id) {
		return Artifact{}, fmt.Errorf("artifact %q: %w", id, ErrNotFound)
	}
	a, err := scanArtifact(s.DB().QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts a WHERE a.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, fmt.Errorf("artifact %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Artifact{}, err
	}
	if !a.Sensitive {
		// A chat held after the artifact was written may have been given
		// sensitive content the artifact's later reading draws on.
		if a.Sensitive, err = recall.Held(ctx, s, a.ChatID); err != nil {
			return Artifact{}, err
		}
	}
	return a, nil
}

// List returns a chat's artifacts, the oldest first.
func List(ctx context.Context, s *tenant.Store, chatID string) ([]Artifact, error) {
	if err := chatThere(s, chatID); err != nil {
		return nil, err
	}
	return listWhere(ctx, s, "WHERE a.chat_id = ? ORDER BY a.created_at, a.id", chatID)
}

// ListAll returns every artifact of this tenant, the one changed last first.
func ListAll(ctx context.Context, s *tenant.Store) ([]Artifact, error) {
	return listWhere(ctx, s, "ORDER BY a.updated_at DESC, a.id")
}

func listWhere(ctx context.Context, s *tenant.Store, where string, args ...any) ([]Artifact, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT "+artifactColumns+" FROM artifacts a "+where, args...)
	if err != nil {
		return nil, err
	}
	var out []Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for i := range out {
		if out[i].Sensitive {
			continue
		}
		h, seen := held[out[i].ChatID]
		if !seen {
			if h, err = recall.Held(ctx, s, out[i].ChatID); err != nil {
				return nil, err
			}
			held[out[i].ChatID] = h
		}
		out[i].Sensitive = h
	}
	return out, nil
}

const versionColumns = "artifact_id, version, title, size, sha256, origin, restored_from, sensitive, request_id, created_at"

func scanVersion(sc interface{ Scan(...any) error }) (Version, error) {
	var v Version
	var created string
	if err := sc.Scan(&v.ArtifactID, &v.Version, &v.Title, &v.Size, &v.SHA256, &v.Origin, &v.RestoredFrom, &v.Sensitive, &v.RequestID, &created); err != nil {
		return Version{}, err
	}
	t, err := time.Parse(timeLayout, created)
	if err != nil {
		return Version{}, fmt.Errorf("artifact %s version %d: %w", v.ArtifactID, v.Version, err)
	}
	v.Created = t
	return v, nil
}

func getVersion(ctx context.Context, s *tenant.Store, id string, version int) (Version, error) {
	v, err := scanVersion(s.DB().QueryRowContext(ctx, "SELECT "+versionColumns+" FROM artifact_versions WHERE artifact_id = ? AND version = ?", id, version))
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, fmt.Errorf("artifact %s version %d: %w", id, version, ErrNotFound)
	}
	return v, err
}

// Versions returns an artifact's versions, the oldest first.
func Versions(ctx context.Context, s *tenant.Store, id string) ([]Version, error) {
	if _, err := Get(ctx, s, id); err != nil {
		return nil, err
	}
	rows, err := s.DB().QueryContext(ctx, "SELECT "+versionColumns+" FROM artifact_versions WHERE artifact_id = ? ORDER BY version", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Content returns one version of an artifact and its content. A file that
// does not match its row's digest is not returned.
func Content(ctx context.Context, s *tenant.Store, id string, version int) (Version, string, error) {
	a, err := Get(ctx, s, id)
	if err != nil {
		return Version{}, "", err
	}
	v, err := getVersion(ctx, s, id, version)
	if err != nil {
		return Version{}, "", err
	}
	raw, err := s.Artifacts().ReadFile(fileOf(a.ChatID, id, version))
	if errors.Is(err, fs.ErrNotExist) {
		return Version{}, "", fmt.Errorf("the content of artifact %s version %d: %w", id, version, ErrNotFound)
	}
	if err != nil {
		return Version{}, "", err
	}
	if digest(string(raw)) != v.SHA256 {
		return Version{}, "", fmt.Errorf("the content of artifact %s version %d does not match its record", id, version)
	}
	return v, string(raw), nil
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
