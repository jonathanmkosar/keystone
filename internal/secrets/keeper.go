package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Keeper stands for the install master key. It wraps a tenant's data key so
// the key can be stored, and unwraps it again. The context ties a wrapped
// key to its use: a key wrapped for one tenant does not unwrap for another.
//
// Each operating system's keeper is one file here, and System returns it.
type Keeper interface {
	// Name says which keeper this is. keystone.json records it.
	Name() string
	Wrap(key []byte, context string) ([]byte, error)
	Unwrap(wrapped []byte, context string) ([]byte, error)
}

// ErrNoKeeper is returned by System where the operating system offers no
// key store Keystone can use.
var ErrNoKeeper = errors.New("this system offers no key store Keystone can use, so secrets cannot be stored. An install can be told to keep its master key in a file instead: see docs/runbooks/secrets.md")

// Named returns the keeper with this name: "system" for the operating
// system's own, "file" for a key file in the user's profile, or the name
// that a keeper gave and keystone.json recorded.
func Named(name string) (Keeper, error) {
	switch name {
	case "system":
		return System()
	case "file":
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		return fileKeeper(filepath.Join(dir, "keystone", "master.key")), nil
	case "memory":
		return nil, errors.New("the memory keeper is for tests, and its key is gone when the test ends")
	}
	if k, err := System(); err == nil && k.Name() == name {
		return k, nil
	}
	return nil, fmt.Errorf("the install master key is kept by %q, which this system does not have", name)
}

// NewMemoryKeeper returns a keeper with a random master key that lives only
// as long as the process. Tests use it, so they need no operating system
// key store and leave nothing behind.
func NewMemoryKeeper() Keeper {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		panic(err) // crypto/rand does not fail
	}
	return &keyKeeper{name: "memory", key: key}
}

// fileKeeper returns a keeper whose master key is a file at path, outside
// the data folder. It is for a system with no key store, and it is weaker
// than one: whoever can read the file can unwrap every data key.
func fileKeeper(path string) Keeper {
	return &keyKeeper{
		name: "file",
		find: func() (string, bool, error) {
			raw, err := os.ReadFile(path)
			if errors.Is(err, fs.ErrNotExist) {
				return "", false, nil
			}
			return string(raw), err == nil, err
		},
		add: func(hexKey string) error {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if errors.Is(err, fs.ErrExist) {
				return nil // another process made the key first, and that one is used
			}
			if err != nil {
				return err
			}
			_, err = f.WriteString(hexKey + "\n")
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			return err
		},
	}
}

// keyKeeper wraps with a master key of its own: 32 random bytes that it
// keeps, as hex text, wherever find and add reach. That is the keychain on
// macOS, the Secret Service on Linux, and a file for the file keeper. The
// master key is made by the first Wrap and never by an Unwrap.
type keyKeeper struct {
	name string
	// find returns the stored master key, and whether there is one.
	find func() (string, bool, error)
	// add stores a new master key. It does not replace one that is there.
	add func(hexKey string) error

	mu  sync.Mutex
	key []byte // the master key, once read
}

func (k *keyKeeper) Name() string { return k.name }

// String and GoString keep the master key out of anything that prints a
// keeper.
func (k *keyKeeper) String() string   { return "the " + k.name + " keeper" }
func (k *keyKeeper) GoString() string { return k.String() }

func (k *keyKeeper) Wrap(key []byte, context string) ([]byte, error) {
	master, err := k.master(true)
	if err != nil {
		return nil, err
	}
	return seal(master, key, []byte(context))
}

func (k *keyKeeper) Unwrap(wrapped []byte, context string) ([]byte, error) {
	master, err := k.master(false)
	if err != nil {
		return nil, err
	}
	return unseal(master, wrapped, []byte(context))
}

func (k *keyKeeper) master(create bool) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.key != nil {
		return k.key, nil
	}
	text, found, err := k.find()
	if err != nil {
		return nil, fmt.Errorf("the %s keeper cannot read the install master key: %w", k.name, err)
	}
	if !found {
		if !create {
			return nil, fmt.Errorf("the %s keeper holds no install master key", k.name)
		}
		fresh := make([]byte, keySize)
		if _, err := rand.Read(fresh); err != nil {
			return nil, err
		}
		if err := k.add(hex.EncodeToString(fresh)); err != nil {
			return nil, fmt.Errorf("the %s keeper cannot store the install master key: %w", k.name, err)
		}
		// Read it back and use what is stored, so two processes that
		// start at the same moment end up with the same key.
		text, found, err = k.find()
		if err != nil || !found {
			return nil, fmt.Errorf("the %s keeper did not keep the install master key", k.name)
		}
	}
	// The text is never put in an error.
	key, err := hex.DecodeString(strings.TrimSpace(text))
	if err != nil || len(key) != keySize {
		return nil, fmt.Errorf("the install master key held by the %s keeper is damaged", k.name)
	}
	k.key = key
	return key, nil
}
