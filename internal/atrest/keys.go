// Package atrest keeps a person's data encrypted on disk (P15-06): their
// database, page by page, through a SQLite VFS, and their files, sealed
// whole in chunks. Everything is AES-256-GCM, under keys that come from the
// person's data key (decision 0006), bound to their tenant id.
//
// The package knows nothing about tenants' folders or the registry:
// internal/tenant decides what is sealed and when. It reads no setting.
// Nothing here prints or logs a key, and no error carries one.
//
// See docs/architecture/encryption.md and decisions 0660 to 0664.
package atrest

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
)

// Purpose is what internal/tenant asks the person's vault for: one subkey,
// from which every key here is derived.
const Purpose = "keystone at rest v1"

const keySize = 32 // AES-256

var tenantShape = regexp.MustCompile(`^[a-f0-9]{16}$`)

// Keys are one person's keys for data at rest. The zero value is not
// usable; Derive makes them.
type Keys struct {
	tenant string
	pages  []byte // the database and its journal
	files  []byte // sealed files
	names  []byte // keyed names of files and folders
	check  string // what a sealed folder records, to tell the right keys
}

// Derive makes a person's keys from root, the subkey their vault gives for
// Purpose, and binds every one to the tenant id.
func Derive(root []byte, tenantID string) (*Keys, error) {
	if len(root) != keySize {
		return nil, fmt.Errorf("a root key is %d bytes, and this one is %d", keySize, len(root))
	}
	if !tenantShape.MatchString(tenantID) {
		return nil, fmt.Errorf("%q is not a tenant id", tenantID)
	}
	k := &Keys{tenant: tenantID}
	for _, d := range []struct {
		to   *[]byte
		what string
	}{{&k.pages, "pages"}, {&k.files, "files"}, {&k.names, "names"}} {
		key, err := hkdf.Key(sha256.New, root, nil, "keystone at rest v1 "+d.what+" for tenant "+tenantID, keySize)
		if err != nil {
			return nil, err
		}
		*d.to = key
	}
	k.check = hex.EncodeToString(mac(k.names, "keystone sealed folder check v1 for tenant "+tenantID))
	return k, nil
}

// Tenant returns the tenant id the keys are bound to.
func (k *Keys) Tenant() string { return k.tenant }

// Check returns the value a sealed folder records, so a folder opened with
// another person's keys, or under another master key, is told apart before
// anything else is read. It is a keyed hash and gives no key away.
func (k *Keys) Check() string { return k.check }

// Name returns the name a file or folder called part has on disk: a keyed
// hash, so the same name, or the same file kept by its hash, has a
// different name on disk for every person (decision 0662).
func (k *Keys) Name(part string) string {
	return hex.EncodeToString(mac(k.names, "keystone name v1\x00"+part)[:16])
}

// String and GoString keep the keys out of anything that prints them.
func (k *Keys) String() string   { return "the at-rest keys of tenant " + k.tenant }
func (k *Keys) GoString() string { return k.String() }

func mac(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// ErrSealed is wrapped by every error that says sealed bytes did not open:
// the wrong key, another person's file, or a changed or damaged copy.
var ErrSealed = errors.New("it does not open with this person's keys: it belongs to another person, was sealed under another master key, or was changed")
