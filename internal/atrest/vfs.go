package atrest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"modernc.org/sqlite/vfs"
)

// The VFS (decision 0660). It is registered with the driver once, the first
// time it is needed, and serves every sealed database of the process. It
// opens only the two files Keystone's databases are made of, keystone.db
// and keystone.db-journal, and only in a folder whose keys were given to
// Use: any other name SQLite asks for is refused, so a name read from a
// journal cannot take it anywhere else.
//
// The driver keeps the connections of this process apart (it runs SQLite's
// locks between them), and keeps temporary files in memory, so they never
// reach the disk. Nothing in the driver keeps a second process out, so the
// VFS holds a lock on each database it has open, for as long as any
// connection of this process has it open; another process waits for it up
// to the time SetLockWait gives, and then is refused.

const (
	// DatabaseFile is the name of a person's database in their folder.
	DatabaseFile = "keystone.db"
	journalFile  = DatabaseFile + "-journal"
)

var (
	register struct {
		once sync.Once
		name string
		err  error
	}
	ring = struct {
		sync.RWMutex
		keys map[string]*Keys // by the folder's absolute path
	}{keys: map[string]*Keys{}}
	lockWait atomic.Int64
)

func init() { lockWait.Store(int64(30 * time.Second)) }

// SetLockWait sets how long opening a database waits for another process
// that has it open. The caller passes the encryption.lock_wait setting.
func SetLockWait(d time.Duration) {
	if d >= 0 {
		lockWait.Store(int64(d))
	}
}

// Use gives the VFS the keys for the databases in folder. A folder given
// again takes the newer keys.
func Use(folder string, k *Keys) error {
	if k == nil {
		return errors.New("no keys were given")
	}
	abs, err := filepath.Abs(folder)
	if err != nil {
		return err
	}
	ring.Lock()
	ring.keys[abs] = k
	ring.Unlock()
	return nil
}

// Forget takes back the keys given for folder. A folder that is only used
// for a while, as a restore's or a sealing's, is forgotten afterwards.
func Forget(folder string) {
	if abs, err := filepath.Abs(folder); err == nil {
		ring.Lock()
		delete(ring.keys, abs)
		ring.Unlock()
	}
}

// KeysFor returns the keys given for folder, or nil.
func KeysFor(folder string) *Keys {
	abs, err := filepath.Abs(folder)
	if err != nil {
		return nil
	}
	ring.RLock()
	defer ring.RUnlock()
	return ring.keys[abs]
}

// DSN returns the start of the data source name that opens the database at
// path through the VFS: "file:" and the path, and the vfs parameter. The
// caller adds its own parameters after "&".
func DSN(path string) (string, error) {
	register.once.Do(func() {
		register.name, _, register.err = vfs.Register(sealedVFS{})
	})
	if register.err != nil {
		return "", fmt.Errorf("the sealed VFS could not be registered: %w", register.err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return "file:" + filepath.ToSlash(abs) + "?vfs=" + register.name, nil
}

type sealedVFS struct{}

// keysOf returns the keys for the file at name, and whether it is the
// database or its journal. Any other file has none.
func keysOf(name string) (k *Keys, path string, db bool, err error) {
	path, err = filepath.Abs(name)
	if err != nil {
		return nil, "", false, err
	}
	dir, base := filepath.Split(path)
	switch base {
	case DatabaseFile:
		db = true
	case journalFile:
	default:
		return nil, "", false, fmt.Errorf("the sealed VFS opens only %s and %s, and was asked for %s", DatabaseFile, journalFile, base)
	}
	if k = KeysFor(filepath.Clean(dir)); k == nil {
		return nil, "", false, fmt.Errorf("no keys were given for the folder of %s", path)
	}
	return k, path, db, nil
}

func (sealedVFS) Open(name string, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	k, path, db, err := keysOf(name)
	if err != nil {
		return nil, 0, err
	}
	if db != (flags&vfs.OpenMainDB != 0) || !db && flags&vfs.OpenMainJournal == 0 {
		return nil, 0, fmt.Errorf("%s was asked for as another kind of file", path)
	}
	// Always opened for writing: the file is shared by every connection of
	// the process, and the first open must serve the ones after it.
	mode := os.O_RDWR
	if flags&vfs.OpenCreate != 0 {
		mode |= os.O_CREATE
	}
	if db {
		h := holderOf(path)
		p, err := h.take(path, k, mode)
		if err != nil {
			return nil, 0, err
		}
		return &handle{pageFile: p, close: h.give}, flags, nil
	}
	h := holderOf(strings.TrimSuffix(path, "-journal"))
	p, err := h.takeJournal(path, k, mode)
	if err != nil {
		return nil, 0, err
	}
	return &handle{pageFile: p, close: h.giveJournal}, flags, nil
}

func (sealedVFS) Delete(name string, syncDir bool) error {
	_, path, db, err := keysOf(name)
	if err != nil {
		return err
	}
	if !db {
		if err := holderOf(strings.TrimSuffix(path, "-journal")).dropJournal(); err != nil {
			return err
		}
	}
	return os.Remove(path) // a file that is not there wraps fs.ErrNotExist
}

func (sealedVFS) Access(name string, flags vfs.AccessFlag) (bool, error) {
	_, path, _, err := keysOf(name)
	if err != nil {
		return false, nil
	}
	_, err = os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (sealedVFS) FullPathname(name string) (string, error) {
	return filepath.Abs(name)
}

// A holder is the process's hold on one database (decision 0660). The first
// connection of the process to open the database opens the file, takes
// the lock that keeps other processes out, and reads the header; the
// connections after it share that file, and its journal's, so Windows is
// asked to open a file once and not at every transaction. The last
// connection to close lets go of all of it.
type holder struct {
	mu      sync.Mutex
	refs    int       // opens of the database in this process
	db      *pageFile // while refs > 0
	journal *pageFile // the journal, once opened, while refs > 0
	jopen   int       // opens of the journal
}

var holds = struct {
	sync.Mutex
	by map[string]*holder
}{by: map[string]*holder{}}

func holderOf(dbPath string) *holder {
	holds.Lock()
	defer holds.Unlock()
	h := holds.by[dbPath]
	if h == nil {
		h = &holder{}
		holds.by[dbPath] = h
	}
	return h
}

// ErrBusy is wrapped by the error of a database another process has open
// for longer than the lock wait.
var ErrBusy = errors.New("another Keystone process has this person's database open")

// take opens the database for one more connection.
func (h *holder) take(path string, k *Keys, mode int) (*pageFile, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.refs > 0 {
		if err := h.db.sameKeys(k); err != nil {
			return nil, err
		}
		h.refs++
		return h.db, nil
	}
	f, err := os.OpenFile(path, mode, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockWaiting(f, path); err != nil {
		f.Close()
		return nil, err
	}
	p, err := openPages(f, k, false, true)
	if err != nil {
		unlock(f)
		f.Close()
		return nil, err
	}
	h.db, h.refs = p, 1
	return p, nil
}

// give closes the database for one connection; the last closes the file
// and its journal's, and lets go of the lock.
func (h *holder) give() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.refs--; h.refs > 0 {
		return
	}
	if h.journal != nil {
		h.journal.Close()
		h.journal, h.jopen = nil, 0
	}
	unlock(h.db.f)
	h.db.Close()
	h.db = nil
}

// takeJournal opens the journal of the database for one connection.
func (h *holder) takeJournal(path string, k *Keys, mode int) (*pageFile, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.refs == 0 {
		return nil, fmt.Errorf("%s: the journal of a database that is not open", path)
	}
	if h.journal == nil {
		f, err := os.OpenFile(path, mode, 0o600)
		if err != nil {
			return nil, err
		}
		p, err := openPages(f, k, true, true)
		if err != nil {
			f.Close()
			return nil, err
		}
		h.journal = p
	} else if err := h.journal.sameKeys(k); err != nil {
		return nil, err
	}
	h.jopen++
	return h.journal, nil
}

func (h *holder) giveJournal() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.jopen > 0 {
		h.jopen--
	}
}

// dropJournal closes the shared journal before it is removed. One that a
// connection has open is not removed.
func (h *holder) dropJournal() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.journal == nil {
		return nil
	}
	if h.jopen > 0 {
		return errors.New("the journal is open")
	}
	err := h.journal.Close()
	h.journal = nil
	return err
}

// lockWaiting takes the lock that keeps other processes out, waiting for
// another process that has it, up to the lock wait.
func lockWaiting(f *os.File, path string) error {
	deadline := time.Now().Add(time.Duration(lockWait.Load()))
	for {
		err := tryLock(f)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errLocked) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: %w; stop it, or wait for it to finish", path, ErrBusy)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// errLocked is what tryLock returns when another process holds the lock.
var errLocked = errors.New("locked by another process")
