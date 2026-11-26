package scope

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestOnlyALocalOrTrustedModelMayServe(t *testing.T) {
	for class, want := range map[string]bool{"local": true, "trusted": true, "hosted": false, "": false, "Local": false, "anything": false} {
		if got := MayServe(class); got != want {
			t.Errorf("MayServe(%q) = %v, want %v", class, got, want)
		}
	}
}

func TestAnIDReadsAsANumberOrAText(t *testing.T) {
	var w Write
	if err := json.Unmarshal([]byte(`{"page_id": 12, "base_rev": "3", "sources": [7, "s4"]}`), &w); err != nil {
		t.Fatal(err)
	}
	if w.PageID != "12" || w.BaseRev != "3" || len(w.Sources) != 2 || w.Sources[0] != "7" || w.Sources[1] != "s4" || w.New() {
		t.Errorf("read %+v", w)
	}
	if err := json.Unmarshal([]byte(`{"page_id": true}`), &w); err == nil {
		t.Error("a page id that is neither a number nor a text was read")
	}
}

var compile = Actions{Write: true, Delete: true}

func page(path string, sources ...ID) Write {
	return Write{Space: "memory", Path: path, Title: "A page", Body: "A fact [[source:7]].", Sources: sources}
}

func TestAnAnswerThatFitsIsLetThrough(t *testing.T) {
	st := Store{Sources: map[ID]bool{"7": false, "9": true}, Spaces: map[string]bool{"health": true}}
	p := Plan{Batches: []Batch{{Note: "File", Writes: []Write{page("garden", "7"), {Space: "health", Path: "checkup", Title: "Checkup", Body: "x", Sources: []ID{"9"}}},
		Deletes: []Delete{{PageID: "3", BaseRev: "2"}}}}}
	if err := Check(compile, Limits{Batches: 2, NewPages: 2}, p, st); err != nil {
		t.Fatal(err)
	}
	if err := Check(Actions{Write: true, Raise: true, Close: true}, Limits{Batches: 1, NewPages: 1}, Plan{Issues: []Issue{
		{Raise: &Raise{PageID: "3", Title: "Wrong date"}}, {Note: &IssueNote{Issue: "5", Text: "The sources agree."}},
		{Close: &IssueClose{Issue: "5", Reason: "not_a_problem"}}, {Close: &IssueClose{Issue: "6", Reason: "fixed", Changeset: "41"}},
	}}, st); err != nil {
		t.Fatal(err)
	}
}

// Every way an answer is refused, each by its reason.
func TestAnAnswerIsRefusedWhole(t *testing.T) {
	st := Store{Sources: map[ID]bool{"7": false, "9": true}, Spaces: map[string]bool{"health": true}}
	lim := Limits{Batches: 2, NewPages: 2}
	cases := []struct {
		name string
		may  Actions
		p    Plan
		want string
	}{
		{"too many batches", compile, Plan{Batches: []Batch{{Writes: []Write{page("a", "7")}}, {Writes: []Write{page("b", "7")}}, {Writes: []Write{page("c", "7")}}}}, "3 batches"},
		{"too many new pages", compile, Plan{Batches: []Batch{{Writes: []Write{page("a", "7"), page("b", "7"), page("c", "7")}}}}, "3 new pages"},
		{"an empty batch", compile, Plan{Batches: []Batch{{Note: "nothing"}}}, "writes nothing"},
		{"a write without the grant", Actions{Close: true}, Plan{Batches: []Batch{{Writes: []Write{page("a", "7")}}}}, "does not let it"},
		{"a delete without the grant", Actions{Write: true}, Plan{Batches: []Batch{{Deletes: []Delete{{PageID: "3", BaseRev: "1"}}}}}, "deletes pages"},
		{"a page with no path", compile, Plan{Batches: []Batch{{Writes: []Write{{Space: "memory", Title: "T", Body: "x"}}}}}, "no space, path or title"},
		{"a page with no text", compile, Plan{Batches: []Batch{{Writes: []Write{{Space: "memory", Path: "a", Title: "T"}}}}}, "no text"},
		{"a change with no revision", compile, Plan{Batches: []Batch{{Writes: []Write{{PageID: "4", Space: "memory", Path: "a", Title: "T", Body: "x"}}}}}, "without the revision"},
		{"a source it may not cite", compile, Plan{Batches: []Batch{{Writes: []Write{page("a", "8")}}}}, "may not cite"},
		{"a sensitive source on a normal page", compile, Plan{Batches: []Batch{{Writes: []Write{page("a", "9")}}}}, "sensitive space"},
		{"a delete with no revision", compile, Plan{Batches: []Batch{{Deletes: []Delete{{PageID: "3"}}}}}, "without its id"},
		{"a ref named twice", compile, Plan{Batches: []Batch{{Writes: []Write{{Ref: "x", Space: "m", Path: "a", Title: "T", Body: "b"}, {Ref: "x", Space: "m", Path: "b", Title: "T", Body: "b"}}}}}, "twice"},
		{"a link to no ref", compile, Plan{Batches: []Batch{{Writes: []Write{{Space: "m", Path: "a", Title: "T", Body: "See [[new:nowhere]]."}}}}}, "[[new:nowhere]]"},
		{"a raise without the grant", compile, Plan{Issues: []Issue{{Raise: &Raise{PageID: "3", Title: "x"}}}}, "raises an issue"},
		{"a close without the grant", Actions{Raise: true}, Plan{Issues: []Issue{{Close: &IssueClose{Issue: "3", Reason: "fixed", Changeset: "1"}}}}, "closes an issue"},
		{"a note without the grant", compile, Plan{Issues: []Issue{{Note: &IssueNote{Issue: "3", Text: "x"}}}}, "notes an issue"},
		{"two changes in one", Actions{Raise: true, Close: true}, Plan{Issues: []Issue{{Note: &IssueNote{Issue: "3", Text: "x"}, Close: &IssueClose{Issue: "3", Reason: "duplicate"}}}}, "one of raise"},
		{"a reason that is none", Actions{Close: true}, Plan{Issues: []Issue{{Close: &IssueClose{Issue: "3", Reason: "gone"}}}}, "reason"},
		{"fixed with no change", Actions{Close: true}, Plan{Issues: []Issue{{Close: &IssueClose{Issue: "3", Reason: "fixed"}}}}, "without the change"},
		{"urgent with no reason", Actions{Raise: true}, Plan{Issues: []Issue{{Raise: &Raise{PageID: "3", Title: "x", Urgent: true}}}}, "urgent"},
	}
	for _, c := range cases {
		err := Check(c.may, lim, c.p, st)
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want a refusal with %q", c.name, err, c.want)
		}
	}
}

func TestNewPagesAreLinkedByTheirIDs(t *testing.T) {
	body := "See [[new:hives]] and [[new:garden|the garden]], and [[page:3]]."
	if got := NewLinks(body); len(got) != 2 || got[0] != "hives" || got[1] != "garden" {
		t.Errorf("NewLinks = %v", got)
	}
	out, done := Resolve(body, map[string]string{"hives": "12"})
	if done || out != "See [[page:12]] and the garden, and [[page:3]]." {
		t.Errorf("Resolve with one id = %q, %v", out, done)
	}
	out, done = Resolve(body, map[string]string{"hives": "12", "garden": "13"})
	if !done || out != "See [[page:12]] and [[page:13|the garden]], and [[page:3]]." {
		t.Errorf("Resolve with both = %q, %v", out, done)
	}
}
