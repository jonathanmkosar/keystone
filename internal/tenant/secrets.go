package tenant

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/settings"
)

const (
	secretsFile    = "secrets.bin"
	masterKeyField = "master_key"
)

// masterKeyRef is what keystone.json records about the install master key:
// which keeper holds it. It never holds the key.
type masterKeyRef struct {
	Keeper string `json:"keeper"`
}

// Secrets returns the tenant's secrets: provider keys and connector tokens.
// Like the rest of the handle, it reaches this tenant's file and no other.
func (s *Store) Secrets() *secrets.Vault { return s.secrets }

// vault returns the one vault for a tenant, so every handle for that tenant
// shares it and two writers cannot lose each other's change. The caller
// holds in.mu.
func (in *Install) vault(id string) *secrets.Vault {
	if in.vaults == nil {
		in.vaults = map[string]*secrets.Vault{}
	}
	v, ok := in.vaults[id]
	if !ok {
		v = secrets.Open(filepath.Join(in.dir, "tenants", id, secretsFile), id, installKeeper{in})
		in.vaults[id] = v
	}
	return v
}

// installKeeper is the keeper every vault is opened with. It finds the
// install's keeper the first time a secret is used, not when the data
// folder is opened, so an install that stores no secret never touches the
// operating system's key store. After the first data key is wrapped it
// records which keeper wrapped it in keystone.json.
type installKeeper struct{ in *Install }

func (k installKeeper) Name() string {
	keeper, err := k.in.masterKeeper()
	if err != nil {
		return ""
	}
	return keeper.Name()
}

func (k installKeeper) Wrap(key []byte, context string) ([]byte, error) {
	keeper, err := k.in.masterKeeper()
	if err != nil {
		return nil, err
	}
	wrapped, err := keeper.Wrap(key, context)
	if err != nil {
		return nil, err
	}
	return wrapped, k.in.recordKeeper(keeper.Name())
}

func (k installKeeper) Unwrap(wrapped []byte, context string) ([]byte, error) {
	keeper, err := k.in.masterKeeper()
	if err != nil {
		return nil, err
	}
	return keeper.Unwrap(wrapped, context)
}

// masterKeeper returns the keeper of the install master key. That is the
// one the install was opened with, or else the one keystone.json names, or
// else the one the secrets.keeper setting names. A keeper that is not the
// one keystone.json names is refused: it could not unwrap what is stored.
func (in *Install) masterKeeper() (secrets.Keeper, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	recorded, err := in.recordedKeeper()
	if err != nil {
		return nil, err
	}
	if in.keeper == nil {
		name := recorded
		if name == "" {
			if name, err = keeperSetting(); err != nil {
				return nil, err
			}
		}
		if in.keeper, err = secrets.Named(name); err != nil {
			return nil, err
		}
	}
	if have := in.keeper.Name(); recorded != "" && recorded != have {
		return nil, fmt.Errorf("%s says the install master key is kept by %q, and this install was opened with %q", registryFile, recorded, have)
	}
	return in.keeper, nil
}

// recordedKeeper returns the keeper keystone.json names, or "" when it
// names none yet. The caller holds in.mu.
func (in *Install) recordedKeeper() (string, error) {
	raw, ok := in.other[masterKeyField]
	if !ok {
		return "", nil
	}
	var ref masterKeyRef
	if err := json.Unmarshal(raw, &ref); err != nil || ref.Keeper == "" {
		return "", fmt.Errorf("%s: %s does not name a keeper", registryFile, masterKeyField)
	}
	return ref.Keeper, nil
}

// recordKeeper writes the keeper's name to keystone.json, once.
func (in *Install) recordKeeper(name string) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if _, ok := in.other[masterKeyField]; ok {
		return nil
	}
	raw, err := json.Marshal(masterKeyRef{Keeper: name})
	if err != nil {
		return err
	}
	in.other[masterKeyField] = raw
	if err := in.save(); err != nil {
		delete(in.other, masterKeyField)
		return err
	}
	return nil
}

// keeperSetting reads the secrets.keeper setting: where a new install
// keeps its master key.
func keeperSetting() (string, error) {
	reg, err := settings.Registry()
	if err != nil {
		return "", err
	}
	s, ok := settings.Lookup(reg, "secrets.keeper")
	if !ok {
		return "", errors.New("the secrets.keeper setting is missing from the registry")
	}
	return s.Default, nil
}
