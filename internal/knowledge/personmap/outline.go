package personmap

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
)

// layoutVersion goes up when the text of the map changes for the same
// catalog, so every kept map is built again.
const layoutVersion = 1

// NoKind is what the map calls pages whose kind the store left empty.
const NoKind = "other"

// One space as the map shows it.
type spaceOutline struct {
	name      string
	sensitive bool
	pages     int
	kinds     []kindOutline // most pages first, then by name
}

type kindOutline struct {
	name   string
	pages  int
	titles []string // in the catalog's order, at most Limits.TitlesPerKind
}

// Build makes both versions of the map from a catalog. It reads nothing and
// keeps nothing: Load is the call that does. The same catalog and limits
// always give the same texts.
//
// A space is sensitive when any entry in it has a label that is not
// normal, so a missing label reads as sensitive. A sensitive space is left
// out of the open version whole, and only counted.
func Build(entries []knowledge.Entry, l Limits) Map {
	spaces := outline(entries, l)
	m := Map{Spaces: len(spaces), Pages: len(entries)}
	var open []spaceOutline
	for _, s := range spaces {
		if s.sensitive {
			m.Withheld++
		} else {
			open = append(open, s)
		}
	}
	m.whole = fit(spaces, 0, l)
	m.open = fit(open, m.Withheld, l)
	return m
}

// outline groups a catalog by space and by kind.
func outline(entries []knowledge.Entry, l Limits) []spaceOutline {
	sorted := append([]knowledge.Entry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Space != sorted[j].Space {
			return sorted[i].Space < sorted[j].Space
		}
		return sorted[i].Path < sorted[j].Path
	})
	var spaces []spaceOutline
	at := map[string]int{}
	kindAt := map[[2]string]int{}
	for _, e := range sorted {
		i, ok := at[e.Space]
		if !ok {
			i = len(spaces)
			at[e.Space] = i
			name := clean(e.Space, l.TitleMaxChars)
			if name == "" {
				name = "(unnamed)"
			}
			spaces = append(spaces, spaceOutline{name: name})
		}
		s := &spaces[i]
		s.pages++
		s.sensitive = s.sensitive || e.Label.Sensitive()

		kind := clean(e.Kind, l.TitleMaxChars)
		if kind == "" {
			kind = NoKind
		}
		k, ok := kindAt[[2]string{e.Space, kind}]
		if !ok {
			k = len(s.kinds)
			kindAt[[2]string{e.Space, kind}] = k
			s.kinds = append(s.kinds, kindOutline{name: kind})
		}
		s.kinds[k].pages++
		if len(s.kinds[k].titles) < l.TitlesPerKind {
			title := clean(e.Title, l.TitleMaxChars)
			if title == "" {
				title = clean(e.Path, l.TitleMaxChars)
			}
			if title != "" {
				s.kinds[k].titles = append(s.kinds[k].titles, title)
			}
		}
	}
	for i := range spaces {
		kinds := spaces[i].kinds
		sort.SliceStable(kinds, func(a, b int) bool {
			if kinds[a].pages != kinds[b].pages {
				return kinds[a].pages > kinds[b].pages
			}
			return kinds[a].name < kinds[b].name
		})
	}
	return spaces
}

// clean puts a name from the store on one line and cuts it to max
// characters, so no title can break the map's lines or fill it alone.
func clean(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:max-1])) + "…"
}

// fit writes the map at the most detail that stays inside the token limit.
// It gives up detail in a fixed order: titles first, then the smaller kinds
// of each space, then the smaller spaces. Each thing left out is counted in
// the text, so a model knows the map is not the whole of it. withheld is how
// many sensitive spaces were left out before fit was called.
func fit(spaces []spaceOutline, withheld int, l Limits) string {
	if len(spaces) == 0 && withheld == 0 {
		return ""
	}
	fits := func(text string) bool { return EstimateTokens(text, l.CharsPerToken) <= l.MapMaxTokens }

	mostKinds := 0
	for _, s := range spaces {
		mostKinds = max(mostKinds, len(s.kinds))
	}
	for titles := l.TitlesPerKind; titles >= 0; titles-- {
		if text := write(spaces, withheld, titles, mostKinds, len(spaces)); fits(text) {
			return text
		}
	}
	for kinds := mostKinds - 1; kinds >= 0; kinds-- {
		if text := write(spaces, withheld, 0, kinds, len(spaces)); fits(text) {
			return text
		}
	}
	for keep := len(spaces) - 1; keep >= 0; keep-- {
		if text := write(spaces, withheld, 0, 0, keep); fits(text) {
			return text
		}
	}
	return ""
}

// write is the map as text: one line for each space, with at most that many
// titles under a kind, that many kinds in a space, and that many spaces.
// The spaces kept are the ones with the most pages, in order of name.
func write(spaces []spaceOutline, withheld, titles, kinds, keep int) string {
	shown := spaces
	if keep < len(spaces) {
		byPages := append([]spaceOutline(nil), spaces...)
		sort.SliceStable(byPages, func(a, b int) bool { return byPages[a].pages > byPages[b].pages })
		shown = byPages[:keep]
		sort.SliceStable(shown, func(a, b int) bool { return shown[a].name < shown[b].name })
	}
	var b strings.Builder
	for _, s := range shown {
		b.WriteString("- ")
		b.WriteString(s.name)
		if s.sensitive {
			b.WriteString(" (sensitive)")
		}
		b.WriteString(", ")
		b.WriteString(count(s.pages, "page", "pages"))
		for i, k := range s.kinds {
			if kinds == 0 {
				break
			}
			if i >= kinds {
				b.WriteString("; and ")
				b.WriteString(count(len(s.kinds)-i, "more kind", "more kinds"))
				break
			}
			if i == 0 {
				b.WriteString(": ")
			} else {
				b.WriteString("; ")
			}
			b.WriteString(k.name)
			b.WriteByte(' ')
			b.WriteString(strconv.Itoa(k.pages))
			if n := min(titles, len(k.titles)); n > 0 {
				b.WriteString(" (")
				b.WriteString(strings.Join(k.titles[:n], "; "))
				b.WriteByte(')')
			}
		}
		b.WriteByte('\n')
	}
	if more := len(spaces) - len(shown); more > 0 {
		b.WriteString("- and ")
		b.WriteString(count(more, "more space", "more spaces"))
		b.WriteByte('\n')
	}
	switch {
	case withheld == 1:
		b.WriteString("- 1 sensitive space is not shown to this model\n")
	case withheld > 1:
		b.WriteString("- " + strconv.Itoa(withheld) + " sensitive spaces are not shown to this model\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
