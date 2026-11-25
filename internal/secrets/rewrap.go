package secrets

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
)

// A backup carries a tenant's secrets to another install, where another
// keeper holds the master key (decision 0261). The data key goes with them,
// wrapped by a key of the backup's own, and is wrapped again by the new
// install's keeper when the backup is restored. It never leaves this
// package unwrapped.

// KeyKeeper returns a keeper that wraps with key, 32 bytes the caller made,
// under the given name. A backup file is such a keeper: its key comes from
// the person's passphrase. The key is copied, and never printed.
func KeyKeeper(name string, key []byte) (Keeper, error) {
	if name == "" {
		return nil, errors.New("a keeper has a name")
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("a keeper's key is %d bytes, and this one is %d", keySize, len(key))
	}
	return &keyKeeper{name: name, key: append([]byte(nil), key...)}, nil
}

// Rewrap takes the bytes of a secrets file that belongs to the tenant with
// this id, unwraps its data key with from, and returns the same secrets
// with the same data key wrapped by to. It also returns how many secrets
// the file holds, and never their names or values.
//
// The secrets are opened on the way, so a damaged file, or a key that from
// cannot unwrap, is refused before to is asked for anything. Rewrapping
// with the same keeper as from and to checks a file and changes nothing.
func Rewrap(raw []byte, tenantID string, from, to Keeper) ([]byte, int, error) {
	src := &Vault{tenant: tenantID, keeper: from}
	c, err := src.parse(raw)
	if err != nil {
		return nil, 0, err
	}
	dst := &Vault{tenant: tenantID, keeper: to}
	wrapped, err := to.Wrap(c.key, dst.keyContext())
	if err != nil {
		return nil, 0, fmt.Errorf("tenant %s: the data key cannot be wrapped: %w", tenantID, err)
	}
	if len(wrapped) == 0 || len(wrapped) > math.MaxUint16 {
		return nil, 0, fmt.Errorf("tenant %s: the keeper returned a wrapped key of %d bytes", tenantID, len(wrapped))
	}
	c.wrapped = wrapped
	out, err := dst.encode(c)
	if err != nil {
		return nil, 0, err
	}
	return out, len(c.values), nil
}

// Rewrapped returns the vault's file with its data key wrapped by to, as
// Rewrap does, and how many secrets it holds. ok is false when the tenant
// has stored no secret, so there is no file. The file is read under the
// vault's lock, so a write from this process is never half seen.
func (v *Vault) Rewrapped(to Keeper) (raw []byte, count int, ok bool, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	file, err := os.ReadFile(v.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	raw, count, err = Rewrap(file, v.tenant, v.keeper, to)
	if err != nil {
		return nil, 0, false, err
	}
	return raw, count, true, nil
}
