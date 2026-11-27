// Package tenant holds the install's tenant registry and the store handle
// for one tenant.
//
// A tenant is one person. Each has a folder under the data folder with its
// own database and its own files. A Store is opened for one tenant id and
// has no way to name another: it holds that tenant's database and its
// folders as os.Root values, which refuse any path that leaves the folder.
package tenant

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/jonathanmkosar/keystone/internal/secrets"
)

const (
	registryFile    = "keystone.json"
	registryVersion = 1
	maxNameLength   = 100
)

var idShape = regexp.MustCompile(`^[a-f0-9]{16}$`)

// Tenant is one entry in the install's registry.
type Tenant struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	// Admin is the admin mark (P15-01, decision 0640). Only SetAdmin
	// changes it, and only the command line calls SetAdmin. In a list from
	// Tenants it is as the server last read it, and may be old: code that
	// decides anything by it calls IsAdmin or Marks, which read the file.
	Admin bool `json:"admin"`
}

// Install is one data folder.
type Install struct {
	dir string

	mu      sync.Mutex
	tenants []Tenant
	// other holds the fields of keystone.json this package does not own,
	// so that saving the tenant list never drops them.
	other map[string]json.RawMessage
	// keeper holds the install master key. It is nil until a secret is
	// used, unless OpenWithKeeper gave one. See secrets.go.
	keeper secrets.Keeper
	// newKeeper names the keeper a new install uses. See NewInstallKeeper.
	newKeeper string
	vaults    map[string]*secrets.Vault
}

// Open opens the data folder at dir, creating it and an empty registry when
// they are not there, and brings every tenant's database up to date. The
// install master key is kept where keystone.json says, or where
// NewInstallKeeper says when the install has none yet.
func Open(dir string) (*Install, error) {
	return OpenWithKeeper(dir, nil)
}

// OpenWithKeeper opens the data folder as Open does, with k as the keeper of
// the install master key. Tests pass a keeper held in memory. A nil keeper
// is the one keystone.json names, or the one NewInstallKeeper names.
func OpenWithKeeper(dir string, k secrets.Keeper) (*Install, error) {
	if err := os.MkdirAll(filepath.Join(dir, "tenants"), 0o700); err != nil {
		return nil, err
	}
	in := &Install{dir: dir, other: map[string]json.RawMessage{}, keeper: k}
	raw, err := os.ReadFile(filepath.Join(dir, registryFile))
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := in.save(); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(raw, &in.other); err != nil {
			return nil, fmt.Errorf("%s: %w", registryFile, err)
		}
		if v, ok := in.other["tenants"]; ok {
			if err := json.Unmarshal(v, &in.tenants); err != nil {
				return nil, fmt.Errorf("%s: tenants: %w", registryFile, err)
			}
		}
		seen := map[string]bool{}
		for _, t := range in.tenants {
			if !idShape.MatchString(t.ID) {
				return nil, fmt.Errorf("%s: %q is not a tenant id", registryFile, t.ID)
			}
			if seen[t.ID] {
				return nil, fmt.Errorf("%s: tenant %s is listed twice", registryFile, t.ID)
			}
			seen[t.ID] = true
		}
	}
	for _, t := range in.tenants {
		s, err := in.open(t.ID)
		if err != nil {
			return nil, err
		}
		if err := s.Close(); err != nil {
			return nil, err
		}
	}
	return in, nil
}

// Tenants returns the registry, oldest first.
func (in *Install) Tenants() []Tenant {
	in.mu.Lock()
	defer in.mu.Unlock()
	return append([]Tenant(nil), in.tenants...)
}

// Create adds a tenant: its folder, its database with every migration
// applied, and its entry in the registry.
func (in *Install) Create(name string) (Tenant, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLength {
		return Tenant{}, fmt.Errorf("a tenant's name is 1 to %d characters", maxNameLength)
	}
	in.mu.Lock()
	defer in.mu.Unlock()

	id, err := newID()
	if err != nil {
		return Tenant{}, err
	}
	t := Tenant{ID: id, Name: name, Created: time.Now().UTC().Truncate(time.Second)}
	folder := filepath.Join(in.dir, "tenants", id)
	if err := os.Mkdir(folder, 0o700); err != nil {
		return Tenant{}, err
	}
	built := false
	defer func() {
		if !built {
			os.RemoveAll(folder) // ours alone: Mkdir above made it
		}
	}()
	for _, sub := range []string{"files", "artifacts"} {
		if err := os.Mkdir(filepath.Join(folder, sub), 0o700); err != nil {
			return Tenant{}, err
		}
	}
	db, err := openDB(filepath.Join(folder, "keystone.db"))
	if err != nil {
		return Tenant{}, err
	}
	err = migrate(db)
	if err == nil {
		_, err = db.Exec("INSERT INTO tenant_meta(key, value) VALUES('tenant_id', ?)", id)
	}
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Tenant{}, err
	}
	in.tenants = append(in.tenants, t)
	if err := in.save(); err != nil {
		in.tenants = in.tenants[:len(in.tenants)-1]
		return Tenant{}, err
	}
	built = true
	return t, nil
}

// Store opens the handle for one tenant. The id must be in the registry.
func (in *Install) Store(id string) (*Store, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.open(id)
}

func (in *Install) open(id string) (*Store, error) {
	if !idShape.MatchString(id) {
		return nil, fmt.Errorf("%q is not a tenant id", id)
	}
	known := false
	for _, t := range in.tenants {
		if t.ID == id {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("there is no tenant %s", id)
	}
	return openStore(filepath.Join(in.dir, "tenants", id), id, in.vault(id))
}

// openStore opens the handle for the tenant whose folder is folder: its
// database brought up to date and checked to be this tenant's, and its
// folders. A restore opens a tenant this way before it is put in place.
func openStore(folder, id string, vault *secrets.Vault) (*Store, error) {
	db, err := openDB(filepath.Join(folder, "keystone.db"))
	if err != nil {
		return nil, err
	}
	s := &Store{id: id, db: db, secrets: vault}
	fail := func(err error) (*Store, error) {
		s.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		return fail(fmt.Errorf("tenant %s: %w", id, err))
	}
	var have string
	if err := db.QueryRow("SELECT value FROM tenant_meta WHERE key = 'tenant_id'").Scan(&have); err != nil {
		return fail(fmt.Errorf("tenant %s: %w", id, err))
	}
	if have != id {
		return fail(fmt.Errorf("tenant %s: its database belongs to another tenant", id))
	}
	if s.files, err = os.OpenRoot(filepath.Join(folder, "files")); err != nil {
		return fail(err)
	}
	if s.artifacts, err = os.OpenRoot(filepath.Join(folder, "artifacts")); err != nil {
		return fail(err)
	}
	return s, nil
}

// save writes the registry. The admin marks are taken from the file on disk
// first (decision 0641): only SetAdmin changes a mark, from the command
// line, and a server that read the file before that change must not write
// the old mark back. A tenant the file does not list has no mark.
func (in *Install) save() error {
	marks, err := in.diskMarks()
	if err != nil {
		return err
	}
	for i := range in.tenants {
		in.tenants[i].Admin = marks[in.tenants[i].ID]
	}
	return in.write()
}

// write writes the registry as it is in memory.
func (in *Install) write() error {
	out := map[string]json.RawMessage{}
	for k, v := range in.other {
		out[k] = v
	}
	v, err := json.Marshal(registryVersion)
	if err != nil {
		return err
	}
	out["version"] = v
	tenants := in.tenants
	if tenants == nil {
		tenants = []Tenant{}
	}
	if out["tenants"], err = json.Marshal(tenants); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := filepath.Join(in.dir, registryFile+".tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(in.dir, registryFile))
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Store is the handle for one tenant: its database and its folders.
type Store struct {
	id        string
	db        *sql.DB
	files     *os.Root
	artifacts *os.Root
	secrets   *secrets.Vault
}

// TenantID returns the id of the tenant this handle belongs to.
func (s *Store) TenantID() string { return s.id }

// DB returns the tenant's database.
func (s *Store) DB() *sql.DB { return s.db }

// Files returns the tenant's uploads folder. Paths that leave it are refused.
func (s *Store) Files() *os.Root { return s.files }

// Artifacts returns the tenant's artifacts folder. Paths that leave it are refused.
func (s *Store) Artifacts() *os.Root { return s.artifacts }

// Close closes the database and the folders.
func (s *Store) Close() error {
	var first error
	if s.db != nil {
		first = s.db.Close()
	}
	for _, r := range []*os.Root{s.files, s.artifacts} {
		if r != nil {
			if err := r.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

// openDB opens one tenant's database file: one connection, write-ahead log,
// a wait instead of an error when the file is busy, foreign keys on, and
// write transactions that take the lock at once. These follow Waymarks.
func openDB(path string) (*sql.DB, error) {
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
	return db, nil
}
