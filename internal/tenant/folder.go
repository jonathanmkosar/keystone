package tenant

import (
	"io/fs"
	"os"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/atrest"
)

// Folder is one of a tenant's folders as the code that keeps files in it
// sees it (P15-06): uploads and project files in files, artifact versions
// in artifacts. For a sealed tenant every file is sealed on the way in and
// opened on the way out, and every part of a name is a keyed hash on disk
// (decision 0662); for a plain tenant it is the folder as it is. Its
// methods are os.Root's, so a caller changes s.Files() to s.FilesFolder()
// and nothing else.
//
// Stat reports on the file on disk: a sealed file is larger than what it
// holds. Paths are slash-separated, as os.Root takes them.
type Folder struct {
	root *os.Root
	keys *atrest.Keys // nil for a plain tenant
}

// FilesFolder returns the tenant's files folder: uploads and project files,
// each kept by its hash.
func (s *Store) FilesFolder() *Folder { return &Folder{root: s.files, keys: s.keys} }

// ArtifactsFolder returns the tenant's artifacts folder.
func (s *Store) ArtifactsFolder() *Folder { return &Folder{root: s.artifacts, keys: s.keys} }

// Sealed reports whether the tenant's data is encrypted on disk.
func (s *Store) Sealed() bool { return s.keys != nil }

// disk returns the name a file has on disk.
func (f *Folder) disk(name string) string {
	if f.keys == nil {
		return name
	}
	parts := strings.Split(name, "/")
	for i, p := range parts {
		if p != "" && p != "." {
			parts[i] = f.keys.Name(p)
		}
	}
	return strings.Join(parts, "/")
}

// WriteFile writes data to the named file, sealed for a sealed tenant.
func (f *Folder) WriteFile(name string, data []byte, perm fs.FileMode) error {
	if f.keys != nil {
		sealed, err := f.keys.SealFile(data)
		if err != nil {
			return err
		}
		data = sealed
	}
	return f.root.WriteFile(f.disk(name), data, perm)
}

// ReadFile returns what the named file holds.
func (f *Folder) ReadFile(name string) ([]byte, error) {
	raw, err := f.root.ReadFile(f.disk(name))
	if err != nil || f.keys == nil {
		return raw, err
	}
	return f.keys.OpenFile(raw)
}

// Stat describes the named file on disk.
func (f *Folder) Stat(name string) (fs.FileInfo, error) { return f.root.Stat(f.disk(name)) }

// Remove removes the named file or empty folder.
func (f *Folder) Remove(name string) error { return f.root.Remove(f.disk(name)) }

// RemoveAll removes the named file or folder and everything in it.
func (f *Folder) RemoveAll(name string) error { return f.root.RemoveAll(f.disk(name)) }

// Rename renames a file.
func (f *Folder) Rename(oldname, newname string) error {
	return f.root.Rename(f.disk(oldname), f.disk(newname))
}

// MkdirAll makes the named folder and any it is in.
func (f *Folder) MkdirAll(name string, perm fs.FileMode) error {
	return f.root.MkdirAll(f.disk(name), perm)
}
