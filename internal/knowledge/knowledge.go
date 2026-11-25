// Package knowledge is the door to a tenant's knowledge store: the long-term
// memory a person chooses. Waymarks is one store, the built-in store is
// another, and a person may have none.
//
// Everything outside the adapters speaks the Store interface and the plain
// types in this file. An adapter translates them to and from one store.
// Callers ask a store's Capabilities and never its type.
//
// Every piece of content carries its space and that space's Label, because
// content from a sensitive space reaches only models the registry marks
// local or trusted. That is fixed rule 3 in the plan.
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// The errors a store answers with. An adapter wraps them, so test with
// errors.Is.
var (
	// ErrNotFound means the store holds no page or source with that id.
	ErrNotFound = errors.New("knowledge: not found")
	// ErrNotSupported means the store lacks the capability the call needs.
	// Ask Capabilities first.
	ErrNotSupported = errors.New("knowledge: this store does not support that")
	// ErrInvalid means the call itself is wrong, such as a source with no
	// facts or a vector of the wrong size.
	ErrInvalid = errors.New("knowledge: invalid")
	// ErrStale means a vector was made from a revision that is no longer the
	// page's own. Read the page again.
	ErrStale = errors.New("knowledge: the page has changed")
	// ErrUnavailable means the store could not be reached. A chat carries on
	// without it.
	ErrUnavailable = errors.New("knowledge: the store is unavailable")
)

// Label says whether a space is sensitive.
type Label string

const (
	LabelNormal    Label = "normal"
	LabelSensitive Label = "sensitive"
)

// Sensitive reports whether content under this label is limited to models
// marked local or trusted. Every label but LabelNormal is, so a label an
// adapter left empty reads as sensitive.
func (l Label) Sensitive() bool { return l != LabelNormal }

// Capabilities says which optional calls a store supports. They do not
// change while the store is open.
type Capabilities struct {
	// AddSource: the store takes new sources through AddSource.
	AddSource bool
	// CatalogMarker: the store can say cheaply whether its catalog changed.
	CatalogMarker bool
	// VectorSearch: Search takes a query vector and searches by meaning.
	VectorSearch bool
	// PageVectors: the store lists the pages that need a vector and takes
	// one through PutVector.
	PageVectors bool
}

// Entry is one page in the catalog.
type Entry struct {
	ID    string
	Space string
	Label Label
	// Path is where the page sits inside its space, as the store names it.
	Path  string
	Title string
	// Kind is what the page is about, such as project or account, in the
	// store's own words. It may be empty.
	Kind string
}

// Page is one page, whole.
type Page struct {
	ID    string
	Space string
	Label Label
	Path  string
	Title string
	Kind  string
	Body  string
	// Revision changes whenever the page changes. It means something only
	// to the store that gave it.
	Revision string
	// Sources are the ids of the sources the page cites.
	Sources []string
}

// Source is one thing that was brought into the store and is kept unchanged.
type Source struct {
	ID    string
	Space string
	Label Label
	Name  string
	// Origin says where it came from. For facts from a chat it points back
	// to that chat.
	Origin string
	// Dated is the source's own date, such as 2026-03-31. It may be empty.
	Dated string
	// Text is the words of the source. For a source added with facts it
	// holds the text of every fact.
	Text string
}

// Fact is one durable statement.
type Fact struct {
	Text string
}

// NewSource is a source to add: a name, where it came from, and its facts.
type NewSource struct {
	// Space is where it goes. A space the store does not hold yet is made,
	// as a normal space.
	Space  string
	Name   string
	Origin string
	Dated  string
	Facts  []Fact
}

// CheckNewSource reports whether a source can be added: it needs a space, a
// name and at least one fact, and no fact may be blank. Every adapter calls
// it first, so all of them refuse the same things.
func CheckNewSource(n NewSource) error {
	if strings.TrimSpace(n.Space) == "" {
		return fmt.Errorf("%w: a source needs a space", ErrInvalid)
	}
	if strings.TrimSpace(n.Name) == "" {
		return fmt.Errorf("%w: a source needs a name", ErrInvalid)
	}
	if len(n.Facts) == 0 {
		return fmt.Errorf("%w: a source needs at least one fact", ErrInvalid)
	}
	for i, f := range n.Facts {
		if strings.TrimSpace(f.Text) == "" {
			return fmt.Errorf("%w: fact %d is blank", ErrInvalid, i+1)
		}
	}
	return nil
}

// Query is one search.
type Query struct {
	// Text is plain words, not a query language.
	Text string
	// Vector is the same question as a vector, and Model is the embedding
	// model it came from. Only a store with VectorSearch takes them.
	Vector []float32
	Model  string
	// Space limits the search to one space. Empty searches every space.
	Space string
	// Limit is the most hits wanted. Zero or less leaves it to the store.
	Limit int
}

// The two things a search can find.
const (
	HitPage   = "page"
	HitSource = "source"
)

// Hit is one page or source a search found.
type Hit struct {
	// Type is HitPage or HitSource. ID is read with ReadPage or ReadSource
	// to match.
	Type  string
	ID    string
	Space string
	Label Label
	// Path is the page's path. A source has none.
	Path string
	// Title is the page's title or the source's name.
	Title string
	// Snippet is a few words around the match. It may be empty.
	Snippet string
	// Score is higher for a better match. Its scale is the store's own, so
	// a relevance threshold is set for each adapter.
	Score float64
}

// PageNeed is a page that has no vector, or whose vector is older than the
// page.
type PageNeed struct {
	PageID string
	Space  string
	Label  Label
	Title  string
	// Stale is true when the page has a vector from an earlier revision.
	Stale bool
}

// PageVector is the vector an embedding model made from one revision of a
// page.
type PageVector struct {
	PageID string
	// Revision is the Revision of the Page the vector was made from.
	Revision string
	Model    string
	Vector   []float32
}

// Store is one tenant's knowledge store. A handle is opened for one tenant
// and has no way to name another.
//
// Every call takes a context. A supported call whose context is done returns
// the context's error. An id means something only to the store that gave it,
// and only as a page id or as a source id.
//
// What every adapter promises is checked by the suite in the knowledgetest
// package, and an adapter's tests must run it.
type Store interface {
	// Capabilities says which of the optional calls below work.
	Capabilities() Capabilities

	// Catalog lists every page, ordered by space and then by path. A store
	// with no pages returns an empty list.
	Catalog(ctx context.Context) ([]Entry, error)

	// Search finds pages and sources, best first. A query with no text and
	// no vector finds nothing, and so does a space the store does not hold.
	// A query with a vector needs VectorSearch.
	Search(ctx context.Context, q Query) ([]Hit, error)

	// ReadPage returns one page by id, or ErrNotFound.
	ReadPage(ctx context.Context, id string) (Page, error)

	// ReadSource returns one source by id, or ErrNotFound.
	ReadSource(ctx context.Context, id string) (Source, error)

	// AddSource stores a new source and returns it as stored. It can then
	// be read and searched. Needs AddSource.
	AddSource(ctx context.Context, n NewSource) (Source, error)

	// CatalogMarker returns a short text that is the same while the catalog
	// is the same, and differs once a page is added, removed, renamed or
	// moved, or a space's label changes. It may also change when nothing in
	// the catalog did. Needs CatalogMarker.
	CatalogMarker(ctx context.Context) (string, error)

	// VectorsNeeded lists pages with no vector or a stale one. Limit is the
	// most wanted, and zero or less leaves it to the store. Needs
	// PageVectors.
	VectorsNeeded(ctx context.Context, limit int) ([]PageNeed, error)

	// PutVector stores the vector made from one revision of a page. It
	// returns ErrStale when that revision is no longer the page's own, and
	// ErrInvalid for a vector the store cannot take. Needs PageVectors.
	PutVector(ctx context.Context, v PageVector) error

	// Close lets go of whatever the handle holds.
	Close() error
}
