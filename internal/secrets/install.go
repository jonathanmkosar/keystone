package secrets

// The install vault (P7-06): secrets that belong to the install and not to
// any one person, such as the keys of the search providers the admin sets
// for everyone. It is a vault like a tenant's, in data/admin/secrets.bin,
// with its data key wrapped under the same install master key, and both
// bound to InstallID instead of a tenant id.

// InstallID is what the install vault's data key and secrets are bound to.
// No tenant has it, since a tenant id is 16 hex digits, so a tenant's
// secrets.bin put in data/admin/, or the install's put in a tenant's folder,
// is refused.
const InstallID = "install"

// OpenInstall returns the install vault for the secrets file at path. Like
// Open, it reads nothing until a secret is used.
func OpenInstall(path string, k Keeper) *Vault {
	return Open(path, InstallID, k)
}
