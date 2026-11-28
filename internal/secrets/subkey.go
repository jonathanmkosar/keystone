package secrets

import (
	"crypto/hkdf"
	"crypto/sha256"
	"fmt"
	"regexp"
)

// Encryption at rest (P15-06, decision 0660) seals a person's database and
// files with keys that come from their data key. The data key itself still
// never leaves this package (decision 0261): Subkey hands out a key derived
// from it for one named purpose, bound to the tenant id, and a key for one
// purpose tells nothing about the data key or about a key for another.

var purposeShape = regexp.MustCompile(`^[a-z0-9][a-z0-9 ._-]{0,99}$`)

// Subkey returns a 32-byte key derived from the tenant's data key with
// HKDF-SHA-256 for purpose, such as "keystone at rest v1". A tenant that
// has stored no secret has no data key yet: Subkey makes one, wraps it with
// the keeper and writes an empty secrets file, as the first Set does. The
// same purpose always gives the same key for as long as the data key is
// the same, and a restore keeps the data key (decision 0261).
func (v *Vault) Subkey(purpose string) ([]byte, error) {
	if !purposeShape.MatchString(purpose) {
		return nil, fmt.Errorf("%q is not a purpose a subkey is made for", purpose)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	c, err := v.read()
	if err != nil {
		return nil, err
	}
	if c.key == nil {
		// write makes the data key and wraps it; the values stay empty.
		if err := v.write(c); err != nil {
			return nil, err
		}
		if c, err = v.read(); err != nil {
			return nil, err
		}
	}
	return hkdf.Key(sha256.New, c.key, nil, "keystone subkey v1 for tenant "+v.tenant+": "+purpose, keySize)
}
