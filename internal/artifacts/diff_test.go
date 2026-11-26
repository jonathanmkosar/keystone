package artifacts

import (
	"strings"
	"testing"
)

// apply rebuilds both sides of a comparison, to check it says what it
// should of each.
func apply(diff []DiffLine) (older, newer []string) {
	for _, l := range diff {
		switch l.Op {
		case OpSame:
			older, newer = append(older, l.Text), append(newer, l.Text)
		case OpRemove:
			older = append(older, l.Text)
		case OpAdd:
			newer = append(newer, l.Text)
		}
	}
	return older, newer
}

func TestDiffIsTheShortestAndRebuildsBothSides(t *testing.T) {
	for _, c := range []struct {
		name, older, newer string
		added, removed     int
	}{
		{"the same", "a\nb\nc\n", "a\nb\nc\n", 0, 0},
		{"one line changed", "a\nb\nc\n", "a\nB\nc\n", 1, 1},
		{"a line put in", "a\nc", "a\nb\nc", 1, 0},
		{"a line taken out", "a\nb\nc", "a\nc", 0, 1},
		{"from nothing", "", "a\nb", 2, 0},
		{"to nothing", "a\nb", "", 0, 2},
		{"moved", "a\nb\nc\nd", "b\nc\nd\na", 1, 1},
		{"line ends", "a\r\nb\r\n", "a\nb\n", 0, 0},
		{"interleaved", "x\na\ny\nb\nz", "a\nb\nc", 1, 3},
	} {
		diff := Diff(c.older, c.newer)
		added, removed := Counts(diff)
		if added != c.added || removed != c.removed {
			t.Errorf("%s: %d put in and %d taken out, want %d and %d: %+v", c.name, added, removed, c.added, c.removed, diff)
		}
		older, newer := apply(diff)
		if strings.Join(older, "\n") != strings.Join(lines(c.older), "\n") || strings.Join(newer, "\n") != strings.Join(lines(c.newer), "\n") {
			t.Errorf("%s: the comparison rebuilds %q and %q", c.name, older, newer)
		}
		// Line numbers count each side from 1, in order.
		from, to := 0, 0
		for _, l := range diff {
			if l.From != 0 {
				if l.From != from+1 {
					t.Errorf("%s: line %+v follows older line %d", c.name, l, from)
				}
				from = l.From
			}
			if l.To != 0 {
				if l.To != to+1 {
					t.Errorf("%s: line %+v follows newer line %d", c.name, l, to)
				}
				to = l.To
			}
		}
	}
}

func TestDiffOfVersionsFarApartIsStillTrue(t *testing.T) {
	var a, b strings.Builder
	for i := 0; i < 3000; i++ {
		a.WriteString("old line\n")
		b.WriteString("new line\n")
	}
	diff := Diff(a.String(), b.String())
	added, removed := Counts(diff)
	if added != 3000 || removed != 3000 {
		t.Fatalf("versions with nothing in common: %d put in and %d taken out", added, removed)
	}
	older, newer := apply(diff)
	if len(older) != 3000 || len(newer) != 3000 {
		t.Errorf("the comparison rebuilds %d and %d lines", len(older), len(newer))
	}
}
