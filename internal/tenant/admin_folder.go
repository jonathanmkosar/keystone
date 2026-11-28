package tenant

import (
	"path/filepath"

	"github.com/jonathanmkosar/keystone/internal/secrets"
)

// The install's own folder (P7-06): data/admin/, beside data/tenants/. It
// holds what belongs to the install and to no one person: the search
// providers the admin sets for everyone, in their own database, and the
// install vault with their keys. The admin accounts (P15-01) keep their own
// database there too. No person's data is in it.

// adminFolder is the install's folder, in the data folder.
const adminFolder = "admin"

// AdminFolder returns the path of data/admin/. It makes nothing: a part
// that writes there makes the folder when it first writes.
func (in *Install) AdminFolder() string { return filepath.Join(in.dir, adminFolder) }

// InstallVault returns the install vault, data/admin/secrets.bin, sealed
// under the install master key like a person's vault and bound to
// secrets.InstallID. Every caller shares one vault, as each tenant's
// handles share theirs. It reads nothing until a secret is used.
func (in *Install) InstallVault() *secrets.Vault {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.vaults == nil {
		in.vaults = map[string]*secrets.Vault{}
	}
	v, ok := in.vaults[secrets.InstallID]
	if !ok {
		v = secrets.OpenInstall(filepath.Join(in.dir, adminFolder, secretsFile), installKeeper{in})
		in.vaults[secrets.InstallID] = v
	}
	return v
}
