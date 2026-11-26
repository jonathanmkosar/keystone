package knowledge

import (
	"context"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// None is the name of the adapter for a tenant with no knowledge store.
const None = "none"

func init() {
	Register(None, func(context.Context, *tenant.Store) (Store, error) { return none{}, nil })
}

// none is the store of a tenant who has none. It behaves as an empty store
// with no optional capability, so no caller needs a special case for it:
// reads come back empty, no id is found, and the optional calls are not
// supported.
type none struct{}

func (none) Capabilities() Capabilities { return Capabilities{} }

func (none) Catalog(ctx context.Context) ([]Entry, error) {
	return nil, ctx.Err()
}

func (none) Search(ctx context.Context, q Query) ([]Hit, error) {
	if len(q.Vector) > 0 {
		return nil, ErrNotSupported
	}
	return nil, ctx.Err()
}

func (none) ReadPage(ctx context.Context, id string) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	return Page{}, ErrNotFound
}

func (none) ReadSource(ctx context.Context, id string) (Source, error) {
	if err := ctx.Err(); err != nil {
		return Source{}, err
	}
	return Source{}, ErrNotFound
}

func (none) AddSource(context.Context, NewSource) (Source, error) {
	return Source{}, ErrNotSupported
}

func (none) CatalogMarker(context.Context) (string, error) {
	return "", ErrNotSupported
}

func (none) VectorsNeeded(context.Context, string, int) ([]PageNeed, error) {
	return nil, ErrNotSupported
}

func (none) PutVector(context.Context, PageVector) error {
	return ErrNotSupported
}

func (none) RemoveSource(context.Context, string) error {
	return ErrNotSupported
}

func (none) Close() error { return nil }
