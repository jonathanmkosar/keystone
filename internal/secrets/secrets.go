// Package secrets keeps what a tenant must keep secret: provider keys and
// connector tokens.
//
// There are three layers (decision 0006). A tenant's secrets are encrypted
// with that tenant's data key and stored in secrets.bin in the tenant's
// folder. The data key is stored only wrapped, at the head of the same
// file. It is wrapped by a Keeper, which stands for the install master key
// that the operating system holds. No key is written to the data folder in
// the clear.
//
// The wrap and the encryption are both bound to the tenant id, so a file,
// or a wrapped key, copied from one tenant's folder to another's is refused.
//
// Nothing here prints or logs a value, and no error carries one.
package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"regexp"
	"sort"
	"sync"
	"unicode/utf8"
)

const (
	keySize      = 32       // AES-256
	maxValueSize = 64 << 10 // the longest value, in bytes
)

var (
	// magic starts every secrets file. The digit is the format's version.
	magic     = []byte("KSS1")
	nameShape = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)
)

// Vault is one tenant's secrets file. It is safe for use by several
// goroutines.
type Vault struct {
	path   string
	tenant string
	keeper Keeper
	mu     sync.Mutex
}

// Open returns the vault for the secrets file at path, which belongs to the
// tenant with this id. It reads nothing, and asks the keeper for nothing,
// until a secret is used.
func Open(path, tenantID string, k Keeper) *Vault {
	return &Vault{path: path, tenant: tenantID, keeper: k}
}

// Set stores value under name, replacing any earlier value. A name is 1 to
// 100 lower-case letters, digits, dots, dashes and underscores, such as
// provider.anthropic.
func (v *Vault) Set(name, value string) error {
	if err := checkName(name); err != nil {
		return err
	}
	if value == "" || len(value) > maxValueSize || !utf8.ValidString(value) {
		return fmt.Errorf("the value of %s must be text of 1 to %d bytes", name, maxValueSize)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	c, err := v.read()
	if err != nil {
		return err
	}
	c.values[name] = value
	return v.write(c)
}

// Get returns the value stored under name, and whether there is one.
func (v *Vault) Get(name string) (string, bool, error) {
	if err := checkName(name); err != nil {
		return "", false, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	c, err := v.read()
	if err != nil {
		return "", false, err
	}
	value, ok := c.values[name]
	return value, ok, nil
}

// Names lists the names of the tenant's secrets, sorted. It never returns a
// value.
func (v *Vault) Names() ([]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	c, err := v.read()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(c.values))
	for name := range c.values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// Delete removes the secret stored under name. Removing one that is not
// there is not an error.
func (v *Vault) Delete(name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	c, err := v.read()
	if err != nil {
		return err
	}
	if _, ok := c.values[name]; !ok {
		return nil
	}
	delete(c.values, name)
	return v.write(c)
}

func checkName(name string) error {
	if !nameShape.MatchString(name) {
		return fmt.Errorf("%q is not a secret's name: use 1 to 100 lower-case letters, digits, dots, dashes and underscores, starting with a letter or a digit", name)
	}
	return nil
}

// contents is a secrets file, opened. key is nil when there is no file yet.
type contents struct {
	wrapped []byte
	key     []byte
	values  map[string]string
}

// The file is the magic, the length of the wrapped data key as two bytes,
// the wrapped data key, and then the secrets: a JSON object of names and
// values, encrypted with the data key.

// read opens the file. A file that is not there is an empty vault. A file
// that cannot be opened is an error, and is left as it is.
func (v *Vault) read() (contents, error) {
	raw, err := os.ReadFile(v.path)
	if errors.Is(err, fs.ErrNotExist) {
		return contents{values: map[string]string{}}, nil
	}
	if err != nil {
		return contents{}, err
	}
	return v.parse(raw)
}

// parse opens the bytes of a secrets file with the vault's keeper.
func (v *Vault) parse(raw []byte) (contents, error) {
	var err error
	head := len(magic) + 2
	if len(raw) < head || !bytes.Equal(raw[:len(magic)], magic) {
		return contents{}, v.refused("it is not a Keystone secrets file")
	}
	head += int(binary.BigEndian.Uint16(raw[len(magic):]))
	if len(raw) < head {
		return contents{}, v.refused("it is cut short")
	}
	c := contents{wrapped: raw[len(magic)+2 : head]}
	if c.key, err = v.keeper.Unwrap(c.wrapped, v.keyContext()); err != nil {
		return contents{}, fmt.Errorf("%w: %w", v.refused("its data key could not be unwrapped for this tenant"), err)
	}
	if len(c.key) != keySize {
		return contents{}, v.refused("its data key is the wrong length")
	}
	plain, err := unseal(c.key, raw[head:], v.fileContext())
	if err != nil {
		return contents{}, v.refused("it is damaged, or it belongs to another tenant")
	}
	if err := json.Unmarshal(plain, &c.values); err != nil || c.values == nil {
		return contents{}, v.refused("what it holds is not a list of secrets")
	}
	return c, nil
}

func (v *Vault) refused(why string) error {
	return fmt.Errorf("tenant %s: secrets.bin was refused and left as it is: %s", v.tenant, why)
}

// write seals the values and replaces the file. The first write makes the
// tenant's data key and asks the keeper to wrap it.
func (v *Vault) write(c contents) error {
	if c.key == nil {
		c.key = make([]byte, keySize)
		if _, err := rand.Read(c.key); err != nil {
			return err
		}
		wrapped, err := v.keeper.Wrap(c.key, v.keyContext())
		if err != nil {
			return fmt.Errorf("tenant %s: the data key cannot be wrapped: %w", v.tenant, err)
		}
		if len(wrapped) == 0 || len(wrapped) > math.MaxUint16 {
			return fmt.Errorf("tenant %s: the keeper returned a wrapped key of %d bytes", v.tenant, len(wrapped))
		}
		c.wrapped = wrapped
	}
	raw, err := v.encode(c)
	if err != nil {
		return err
	}
	return writeFile(v.path, raw)
}

// encode seals the values with the data key and returns the bytes of the
// file, with the wrapped data key at its head.
func (v *Vault) encode(c contents) ([]byte, error) {
	plain, err := json.Marshal(c.values)
	if err != nil {
		return nil, err
	}
	out := append([]byte{}, magic...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(c.wrapped)))
	out = append(out, c.wrapped...)
	sealed, err := seal(c.key, plain, v.fileContext())
	if err != nil {
		return nil, err
	}
	return append(out, sealed...), nil
}

// keyContext is what the wrapped data key is bound to.
func (v *Vault) keyContext() string {
	return "keystone data key v1 for tenant " + v.tenant
}

// fileContext is what the encrypted secrets are bound to.
func (v *Vault) fileContext() []byte {
	return []byte("keystone secrets v1 for tenant " + v.tenant)
}

// writeFile writes data to a temporary file beside path and renames it into
// place, so a crash never leaves half a file.
func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// seal encrypts plain with key using AES-256-GCM, bound to context. The
// result is a fresh random nonce followed by the ciphertext.
func seal(key, plain, context []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, context), nil
}

// unseal opens what seal made with the same key and context. Anything
// else, or a single changed bit, is an error.
func unseal(key, sealed, context []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	n := gcm.NonceSize()
	if len(sealed) < n+gcm.Overhead() {
		return nil, errors.New("it is cut short")
	}
	plain, err := gcm.Open(nil, sealed[:n], sealed[n:], context)
	if err != nil {
		return nil, errors.New("it does not open: the wrong key, the wrong context, or a damaged copy")
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("a key is %d bytes, and this one is %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
