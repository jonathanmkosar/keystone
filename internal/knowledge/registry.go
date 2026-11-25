package knowledge

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// SettingAdapter is the key of the setting that names a tenant's adapter.
const SettingAdapter = "knowledge.adapter"

// Opener opens one tenant's store. An adapter reaches what it needs, such as
// a folder, an address or a token, through the tenant's handle. The handle
// is nil when the adapter needs nothing from the tenant.
type Opener func(ctx context.Context, t *tenant.Store) (Store, error)

var (
	nameShape = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

	mu      sync.RWMutex
	openers = map[string]Opener{}
)

// Register adds an adapter under a name. An adapter calls it once, from an
// init function. A name that is malformed or already taken is a mistake in
// the code, so it panics.
func Register(name string, open Opener) {
	if !nameShape.MatchString(name) {
		panic(fmt.Sprintf("knowledge: adapter name %q must be lower-case letters, digits and hyphens", name))
	}
	if open == nil {
		panic(fmt.Sprintf("knowledge: adapter %q has no opener", name))
	}
	mu.Lock()
	defer mu.Unlock()
	if _, taken := openers[name]; taken {
		panic(fmt.Sprintf("knowledge: adapter %q is registered twice", name))
	}
	openers[name] = open
}

// Adapters returns the names of the adapters in this build, sorted.
func Adapters() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(openers))
	for name := range openers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Open opens the tenant's store with the adapter named. The name is the
// tenant's value of the SettingAdapter setting.
func Open(ctx context.Context, name string, t *tenant.Store) (Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mu.RLock()
	open, ok := openers[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("there is no knowledge store adapter %q: this build has %s", name, strings.Join(Adapters(), ", "))
	}
	s, err := open(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("knowledge store %s: %w", name, err)
	}
	if s == nil {
		return nil, fmt.Errorf("knowledge store %s: the adapter returned no store", name)
	}
	return s, nil
}
