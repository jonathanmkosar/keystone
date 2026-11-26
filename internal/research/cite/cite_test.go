package cite

import (
	"reflect"
	"strings"
	"testing"
)

var read = []Source{
	{N: 1, Title: "Harbour Museum: opening hours", URL: "https://museum.example/hours", Part: 1},
	{N: 2, Title: "Visiting the harbour", URL: "https://visit.example/harbour", Part: 1, Fetched: true},
	{N: 3, Title: "Ferry timetable", URL: "https://ferry.example/times", Part: 2},
}

// A citation that names no source a part read is taken out, and the ones
// left are renumbered in the order they appear.
func TestCheckDropsACitationWithNoSource(t *testing.T) {
	report := "The ferry leaves at nine [3]. The museum opens at ten [1], and the café is famous [7].\n\nIt is closed on Mondays [3][9]."
	out, cited, dropped := Check(report, read)
	want := "The ferry leaves at nine [1]. The museum opens at ten [2], and the café is famous.\n\nIt is closed on Mondays [1]."
	if out != want {
		t.Errorf("the report reads\n%q\nwant\n%q", out, want)
	}
	if !reflect.DeepEqual(dropped, []int{7, 9}) {
		t.Errorf("dropped %v, want [7 9]", dropped)
	}
	if len(cited) != 2 || cited[0].N != 1 || cited[0].URL != "https://ferry.example/times" || cited[1].N != 2 || cited[1].URL != "https://museum.example/hours" {
		t.Errorf("the cited sources are %+v", cited)
	}
	// Every citation left maps to a source.
	for _, n := range Numbers(out) {
		if n < 1 || n > len(cited) {
			t.Errorf("[%d] names no cited source", n)
		}
	}
}

// With nothing read, every citation goes.
func TestCheckWithNoSourcesKeepsNoCitation(t *testing.T) {
	out, cited, dropped := Check("A claim [1], and another [2, 3].", nil)
	if out != "A claim, and another." || len(cited) != 0 || !reflect.DeepEqual(dropped, []int{1, 2, 3}) {
		t.Errorf("out %q, cited %v, dropped %v", out, cited, dropped)
	}
}

// Numbers in one pair of brackets are each checked, and written apart.
func TestAGroupOfNumbersIsCheckedOneByOne(t *testing.T) {
	out, cited, dropped := Check("Both say so [2, 8; 1].", read)
	if out != "Both say so [1][2]." || len(cited) != 2 || cited[0].URL != "https://visit.example/harbour" || !reflect.DeepEqual(dropped, []int{8}) {
		t.Errorf("out %q, cited %+v, dropped %v", out, cited, dropped)
	}
}

// A link's text, a reference's definition and a fenced block are not
// citations.
func TestWhatIsNotACitationIsLeftAlone(t *testing.T) {
	text := "See [9](https://example.org/nine) and the note [1].\n[9]: https://example.org/ref\n```\nrow[4] = list[9]\n```\nAfter the block [9]."
	out, cited, dropped := Check(text, read)
	want := "See [9](https://example.org/nine) and the note [1].\n[9]: https://example.org/ref\n```\nrow[4] = list[9]\n```\nAfter the block."
	if out != want || len(cited) != 1 || !reflect.DeepEqual(dropped, []int{9}) {
		t.Errorf("out\n%q\nwant\n%q\ncited %+v dropped %v", out, want, cited, dropped)
	}
}

// A part's own numbers become the research's, and one that names nothing
// the part read goes.
func TestRemapChangesAPartsNumbers(t *testing.T) {
	out, dropped := Remap("Open at ten [1]. Closed on Mondays [2]. Free on Sundays [4].", map[int]int{1: 5, 2: 6})
	if out != "Open at ten [5]. Closed on Mondays [6]. Free on Sundays." || !reflect.DeepEqual(dropped, []int{4}) {
		t.Errorf("out %q, dropped %v", out, dropped)
	}
}

// Two numbers side by side that became one source are written once.
func TestTwoNumbersForOneSourceAreWrittenOnce(t *testing.T) {
	out, dropped := Remap("Open at ten [1][3], and again [3, 1]. Later [1] and [3].", map[int]int{1: 5, 3: 5})
	if out != "Open at ten [5], and again [5]. Later [5] and [5]." || len(dropped) != 0 {
		t.Errorf("out %q, dropped %v", out, dropped)
	}
}

// Text with no citation comes back as it was.
func TestTextWithoutCitationsIsUnchanged(t *testing.T) {
	text := "# A title\n\nNothing is cited here, not even [this] or [a1].\n"
	out, cited, dropped := Check(text, read)
	if out != text || len(cited) != 0 || len(dropped) != 0 {
		t.Errorf("out %q, cited %v, dropped %v", out, cited, dropped)
	}
	if got := Numbers(strings.Repeat("[2] ", 3) + "[1]"); !reflect.DeepEqual(got, []int{2, 1}) {
		t.Errorf("Numbers gave %v", got)
	}
}
