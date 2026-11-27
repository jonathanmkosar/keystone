// Package cite is the check on a research report's citations (P8-04): a
// number in square brackets, as [3], must name a source that a part of the
// research read, or it is taken out. Nothing here calls a model or reads a
// setting: these are plain functions on text and numbers, so the rule is
// the same whatever a model wrote and whatever a person set.
//
// A citation is one or more numbers in square brackets: [3], or [3, 5].
// A Markdown link's text, [3](https://…), a reference's definition, [3]:,
// and anything inside a fenced code block are not citations and are left
// as they are.
//
// docs/architecture/research-mode.md is the whole of it.
package cite

import (
	"regexp"
	"strconv"
	"strings"
)

// Source is one source a part of a research task read: a result a recorded
// web_search returned, or a page a recorded web_fetch read.
type Source struct {
	// N is its number: among everything the parts read while the research
	// runs, and among the sources the report cites once it is written.
	N     int    `json:"n"`
	Title string `json:"title"`
	URL   string `json:"url"`
	// Part is the place of the first part that read it, from 1.
	Part int `json:"part"`
	// Fetched says its page was read with web_fetch. One that is not was
	// read as a search result's extract.
	Fetched bool `json:"fetched"`
}

// group is one pair of square brackets that holds only numbers.
var group = regexp.MustCompile(`\[(\d{1,4}(?:\s*[,;]\s*\d{1,4})*)\]`)

var number = regexp.MustCompile(`\d+`)

// Rewrite returns text with each citation's numbers changed by to, and the
// numbers to refused taken out. A citation left with no number goes, with
// the space before it. kept are the new numbers, each once, in the order
// they first appear; dropped are the numbers to refused, each once, in the
// same order.
func Rewrite(text string, to func(n int) (int, bool)) (out string, kept, dropped []int) {
	seenKept, seenDropped := map[int]bool{}, map[int]bool{}
	lines := strings.Split(text, "\n")
	fenced := false
	for i, line := range lines {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		var b strings.Builder
		last := 0
		// run is the numbers written in the citations that stand side by side
		// here, so two numbers that became one source are written once.
		run := map[int]bool{}
		for _, m := range group.FindAllStringSubmatchIndex(line, -1) {
			start, end := m[0], m[1]
			if end < len(line) && (line[end] == '(' || line[end] == ':') {
				continue // a link's text, or a reference's definition
			}
			if start > last || last == 0 {
				run = map[int]bool{}
			}
			var nums []string
			for _, raw := range number.FindAllString(line[m[2]:m[3]], -1) {
				old, _ := strconv.Atoi(raw)
				n, ok := to(old)
				if !ok {
					if !seenDropped[old] {
						seenDropped[old] = true
						dropped = append(dropped, old)
					}
					continue
				}
				if !seenKept[n] {
					seenKept[n] = true
					kept = append(kept, n)
				}
				if !run[n] {
					run[n] = true
					nums = append(nums, "["+strconv.Itoa(n)+"]")
				}
			}
			before := line[last:start]
			if len(nums) == 0 {
				// The citation goes, and the space that led to it.
				before = strings.TrimRight(before, " \t")
			}
			b.WriteString(before)
			b.WriteString(strings.Join(nums, ""))
			last = end
		}
		b.WriteString(line[last:])
		lines[i] = b.String()
	}
	return strings.Join(lines, "\n"), kept, dropped
}

// Numbers returns the numbers a text cites, each once, in the order they
// first appear.
func Numbers(text string) []int {
	_, kept, _ := Rewrite(text, func(n int) (int, bool) { return n, true })
	return kept
}

// Check is the rule. It returns report with every citation that names no
// source in read taken out, the citations that are left renumbered from 1
// in the order they first appear, the sources they name under their new
// numbers, and the numbers that named no source.
func Check(report string, read []Source) (out string, cited []Source, dropped []int) {
	byN := map[int]Source{}
	for _, s := range read {
		if _, ok := byN[s.N]; !ok && s.N > 0 {
			byN[s.N] = s
		}
	}
	// The order the sources that were read are first cited in.
	order := map[int]int{}
	for _, n := range Numbers(report) {
		if _, ok := byN[n]; ok {
			order[n] = len(order) + 1
		}
	}
	out, _, dropped = Rewrite(report, func(n int) (int, bool) {
		to, ok := order[n]
		return to, ok
	})
	cited = make([]Source, len(order))
	for old, to := range order {
		s := byN[old]
		s.N = to
		cited[to-1] = s
	}
	return out, cited, dropped
}

// Remap returns text with its citations changed from one numbering to
// another: a part's own numbers to the research's. A number that is not in
// to is taken out, and returned in dropped.
func Remap(text string, to map[int]int) (out string, dropped []int) {
	out, _, dropped = Rewrite(text, func(n int) (int, bool) {
		g, ok := to[n]
		return g, ok
	})
	return out, dropped
}
