// Package adapters turns an entry in a tenant's model registry into a
// provider that can be called. It finds the builder for the entry's
// adapter and hands it the entry and, when the entry names one, the key
// from the tenant's vault. A key comes from the vault and from nowhere
// else: not from a setting and not from the environment.
//
// The adapters themselves are in the folders below this one. The fake is
// the only one built so far. docs/architecture/models.md describes this
// package with the registry.
package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Builder makes the provider for one registry entry. key is the value of
// the secret the entry names, and empty when it names none. A builder must
// never put the key in an error.
type Builder func(m models.Model, key string) (seam.Provider, error)

// Set holds the builders this binary has, by adapter name. An adapter the
// registry knows and the set does not hold is one that is not built yet.
type Set map[string]Builder

var (
	// ErrNotBuilt is wrapped when the entry's adapter has no builder.
	ErrNotBuilt = errors.New("the adapter is not built")
	// ErrNoKey is wrapped when the entry names a secret the tenant's vault
	// does not hold.
	ErrNoKey = errors.New("the model's key is not in the vault")
)

// Open returns a tenant's model and a provider that calls it. The errors
// are the registry's for a name it does not know or an entry it refuses,
// ErrNotBuilt, ErrNoKey, and the builder's own.
func Open(ctx context.Context, s *tenant.Store, set Set, name string) (models.Model, seam.Provider, error) {
	m, err := models.Get(ctx, s, name)
	if err != nil {
		return models.Model{}, nil, err
	}
	build := set[m.Adapter]
	if build == nil {
		return models.Model{}, nil, fmt.Errorf("%w: model %s uses the %s adapter, and this build does not have it", ErrNotBuilt, m.Name, m.Adapter)
	}
	key := ""
	if m.Key != "" {
		value, ok, err := s.Secrets().Get(m.Key)
		if err != nil {
			return models.Model{}, nil, fmt.Errorf("model %s: reading the secret %s: %w", m.Name, m.Key, err)
		}
		if !ok || value == "" {
			return models.Model{}, nil, fmt.Errorf("%w: model %s needs the secret named %s", ErrNoKey, m.Name, m.Key)
		}
		key = value
	}
	p, err := build(m, key)
	if err != nil {
		// A builder should not put the key in its error. If one does, the
		// key stops here.
		why := err.Error()
		if key != "" {
			why = strings.ReplaceAll(why, key, "(the key)")
		}
		return models.Model{}, nil, fmt.Errorf("model %s: the %s adapter refused the entry: %s", m.Name, m.Adapter, why)
	}
	if p == nil {
		return models.Model{}, nil, fmt.Errorf("model %s: the %s adapter gave no provider", m.Name, m.Adapter)
	}
	return m, p, nil
}

// Fake returns the set that holds the fake adapter alone, playing scripts.
// Every model that names the fake adapter gets the same provider, which
// tells them apart by the request's model name.
func Fake(scripts []fake.Script, opts fake.Options) (Set, error) {
	p, err := fake.New(scripts, opts)
	if err != nil {
		return nil, err
	}
	return Set{models.AdapterFake: func(models.Model, string) (seam.Provider, error) { return p, nil }}, nil
}

// Limits reads the registry's limits from the settings registry's defaults.
// The registry package reads no setting itself, so its caller reads them
// here and passes them in.
func Limits(reg []settings.Setting) (models.Limits, error) {
	const key = "models.max_per_tenant"
	st, ok := settings.Lookup(reg, key)
	if !ok {
		return models.Limits{}, errors.New(key + " is missing from the settings registry")
	}
	n, err := strconv.Atoi(st.Default)
	if err != nil || n <= 0 {
		return models.Limits{}, errors.New(key + " must be a whole number more than zero")
	}
	return models.Limits{MaxModels: n}, nil
}
