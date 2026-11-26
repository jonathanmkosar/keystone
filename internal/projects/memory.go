package projects

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
)

// A project's memory is a space of the person's knowledge store (decision
// 0400). The store interface has spaces and no tags, and a space is what
// every adapter can search alone and write to: the memory pass writes a
// project chat's facts there, and recall in a project chat searches it
// first.

// slugMaxChars is the longest slug a space is named with. It is a bound on
// a name, not a limit a person tunes.
const slugMaxChars = 60

// Slug is a project's name for a space: lower case, letters and digits,
// with one hyphen for each run of anything else, and none at either end.
func Slug(name string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			hyphen = false
			continue
		}
		hyphen = true
	}
	slug := b.String()
	if utf8.RuneCountInString(slug) > slugMaxChars {
		slug = strings.TrimRight(string([]rune(slug)[:slugMaxChars]), "-")
	}
	return slug
}

// SpaceFor names a new project's memory space from the pattern of
// projects.memory_space: {slug} becomes the project's name as a slug, or
// its id when the name has no letter or digit. An empty pattern gives no
// space, which keeps the project's facts with the person's other facts.
func SpaceFor(pattern, name, id string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return ""
	}
	slug := Slug(name)
	if slug == "" {
		slug = id
	}
	space := strings.Join(strings.Fields(strings.ReplaceAll(pattern, "{slug}", slug)), " ")
	if utf8.RuneCountInString(space) > spaceMaxChars {
		space = string([]rune(space)[:spaceMaxChars])
	}
	return space
}

// Prefer returns a store that answers a search of every space with the
// hits in space first, then the rest of the store's, each hit once, no more
// than the search's limit. A search of one space, or an empty space, goes
// to the store as it is. Every hit keeps the label the store gave it, so
// what recall does with a sensitive hit is unchanged. Recall in a project
// chat searches through it.
func Prefer(ks knowledge.Store, space string) knowledge.Store {
	if ks == nil || strings.TrimSpace(space) == "" {
		return ks
	}
	return preferring{Store: ks, space: space}
}

type preferring struct {
	knowledge.Store
	space string
}

func (p preferring) Search(ctx context.Context, q knowledge.Query) ([]knowledge.Hit, error) {
	if q.Space != "" {
		return p.Store.Search(ctx, q)
	}
	own := q
	own.Space = p.space
	first, err := p.Store.Search(ctx, own)
	if err != nil {
		return nil, err
	}
	rest, err := p.Store.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []knowledge.Hit
	for _, h := range append(first, rest...) {
		key := h.Type + "\x00" + h.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}
