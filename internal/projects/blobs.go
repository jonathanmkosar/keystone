package projects

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"regexp"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Blobs is the seam between a project's files and their bytes (decision
// 0401). A file is named by its SHA-256 in lower-case hex, in the tenant's
// files folder, as the uploads lane names a chat's uploads, so one file
// added to a chat and to a project is one file on disk. The integrator may
// give Options.Blobs the store the uploads use, once the two share one.
type Blobs interface {
	// Keep writes data under hash, unless a file of that name is there.
	Keep(s *tenant.Store, hash string, data []byte) error
	// Has reports whether a file of that name is there.
	Has(s *tenant.Store, hash string) bool
	// Release removes the file named hash when nothing holds it any more:
	// no project file and no chat. A file that is not there is no error.
	Release(ctx context.Context, s *tenant.Store, hash string) error
}

// Folder is the Blobs this package ships: the tenant's files folder,
// through the store's os.Root, so no name can leave the folder and no
// tenant's store reaches another's files.
type Folder struct{}

func (o Options) blobs() Blobs {
	if o.Blobs == nil {
		return Folder{}
	}
	return o.Blobs
}

// hashShape is the shape of a file's name. Anything else names no file.
var hashShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

// IsFileID reports whether s has the shape of a file's id.
func IsFileID(s string) bool { return hashShape.MatchString(s) }

// Keep writes the file under another name first and then renames it, so a
// reader never finds half a file.
func (Folder) Keep(s *tenant.Store, hash string, data []byte) error {
	if !IsFileID(hash) {
		return invalid("%q is not a file's name", hash)
	}
	if _, err := s.FilesFolder().Stat(hash); err == nil {
		return nil
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	part := hash + "." + hex.EncodeToString(b[:]) + ".part"
	if err := s.FilesFolder().WriteFile(part, data, 0o600); err != nil {
		return err
	}
	if err := s.FilesFolder().Rename(part, hash); err != nil {
		s.FilesFolder().Remove(part)
		return err
	}
	return nil
}

// Has reports whether the file is in the folder.
func (Folder) Has(s *tenant.Store, hash string) bool {
	if !IsFileID(hash) {
		return false
	}
	_, err := s.FilesFolder().Stat(hash)
	return err == nil
}

// Release removes the file when no project file names it and no chat
// holds it (the chat record's chat_files, which the uploads use too).
func (Folder) Release(ctx context.Context, s *tenant.Store, hash string) error {
	if !IsFileID(hash) {
		return nil
	}
	var n int
	err := s.DB().QueryRowContext(ctx,
		"SELECT (SELECT count(*) FROM project_files WHERE hash = ?1) + (SELECT count(*) FROM chat_files WHERE name = ?1)", hash).Scan(&n)
	if err != nil || n > 0 {
		return err
	}
	if err := s.FilesFolder().Remove(hash); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
