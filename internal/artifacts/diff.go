package artifacts

import "strings"

// The ways a line of a comparison reads.
const (
	OpSame   = "same"
	OpAdd    = "add"
	OpRemove = "remove"
)

// DiffLine is one line of a comparison of two versions: a line both hold,
// one only the newer holds, or one only the older holds. From and To are
// its line numbers in the older and the newer version, counted from 1, and
// zero where it is not in that version.
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
	From int    `json:"from,omitempty"`
	To   int    `json:"to,omitempty"`
}

// maxEdits is the most changed lines a comparison works out line by line,
// which keeps its memory to a few tens of megabytes at worst. Two versions
// further apart are shown as the one taken out and the other put in, which
// is still true.
const maxEdits = 2000

// lines splits text into its lines. A last line break ends the last line
// and does not start an empty one.
func lines(text string) []string {
	if text == "" {
		return nil
	}
	out := strings.Split(text, "\n")
	if out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	for i, l := range out {
		out[i] = strings.TrimSuffix(l, "\r")
	}
	return out
}

// Diff compares two texts line by line, with Myers's algorithm: the
// shortest list of lines to take out and put in that turns older into
// newer, with the lines both hold between them.
func Diff(older, newer string) []DiffLine {
	a, b := lines(older), lines(newer)
	n, m := len(a), len(b)
	if n+m == 0 {
		return []DiffLine{}
	}
	limit := n + m
	if limit > maxEdits {
		limit = maxEdits
	}
	offset := n + m + 1
	v := make([]int, 2*offset+1)
	// trace keeps, for each d, the furthest x on each diagonal from -d-1 to
	// d+1 before step d: the part of v the way back reads.
	var trace [][]int
	found := false
	for d := 0; d <= limit && !found; d++ {
		trace = append(trace, append([]int(nil), v[offset-d-1:offset+d+2]...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1] // a line put in
			} else {
				x = v[offset+k-1] + 1 // a line taken out
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[offset+k] = x
			if x >= n && y >= m {
				found = true
				break
			}
		}
	}
	if !found {
		return wholesale(a, b)
	}
	var out []DiffLine
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		vd := trace[d]
		at := func(k int) int { return vd[k+d+1] }
		k := x - y
		var prev int
		if k == -d || (k != d && at(k-1) < at(k+1)) {
			prev = k + 1
		} else {
			prev = k - 1
		}
		px := at(prev)
		py := px - prev
		for x > px && y > py {
			out = append(out, DiffLine{Op: OpSame, Text: a[x-1], From: x, To: y})
			x, y = x-1, y-1
		}
		if d > 0 {
			if x == px {
				out = append(out, DiffLine{Op: OpAdd, Text: b[py], To: py + 1})
			} else {
				out = append(out, DiffLine{Op: OpRemove, Text: a[px], From: px + 1})
			}
		}
		x, y = px, py
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// wholesale is a comparison of two texts too far apart to work out line by
// line: every line of the older taken out, every line of the newer put in.
func wholesale(a, b []string) []DiffLine {
	out := make([]DiffLine, 0, len(a)+len(b))
	for i, l := range a {
		out = append(out, DiffLine{Op: OpRemove, Text: l, From: i + 1})
	}
	for i, l := range b {
		out = append(out, DiffLine{Op: OpAdd, Text: l, To: i + 1})
	}
	return out
}

// Counts returns how many lines a comparison puts in and takes out.
func Counts(diff []DiffLine) (added, removed int) {
	for _, l := range diff {
		switch l.Op {
		case OpAdd:
			added++
		case OpRemove:
			removed++
		}
	}
	return added, removed
}
