package projects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every project, file and word here is invented.

var bg = context.Background()

func stores(t *testing.T) (one, two *tenant.Store) {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	open := func(name string) *tenant.Store {
		made, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	return open("Person One"), open("Person Two")
}

func options() Options {
	return Options{
		MaxProjects: 3, NameMaxChars: 40, InstructionsMaxChars: 200, MaxFiles: 2, FileMaxBytes: 1000,
		TextExtensions: Extensions(".txt, md,.CSV"), MemorySpace: "project-{slug}",
	}
}

func newChat(t *testing.T, s *tenant.Store) string {
	t.Helper()
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func hashOf(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func onDisk(s *tenant.Store, hash string) bool {
	_, err := s.Files().Stat(hash)
	return err == nil
}

func TestCheckOptions(t *testing.T) {
	if err := CheckOptions(options()); err != nil {
		t.Fatalf("good options are refused: %v", err)
	}
	for key, change := range map[string]func(*Options){
		SettingMaxProjects:          func(o *Options) { o.MaxProjects = 0 },
		SettingNameMaxChars:         func(o *Options) { o.NameMaxChars = 501 },
		SettingInstructionsMaxChars: func(o *Options) { o.InstructionsMaxChars = -1 },
		SettingMaxFiles:             func(o *Options) { o.MaxFiles = 501 },
		SettingFileMaxBytes:         func(o *Options) { o.FileMaxBytes = 0 },
		SettingTextExtensions:       func(o *Options) { o.TextExtensions = []string{"txt"} },
	} {
		o := options()
		change(&o)
		if err := CheckOptions(o); !errors.Is(err, ErrOptions) || !strings.Contains(err.Error(), key) {
			t.Errorf("a bad %s gave %v", key, err)
		}
	}
	if got := Extensions(" .TXT,md,, .csv "); !slices.Equal(got, []string{".txt", ".md", ".csv"}) {
		t.Errorf("Extensions read %v", got)
	}
}

func TestAProjectIsMadeChangedAndListed(t *testing.T) {
	s, _ := stores(t)
	o := options()
	now = func() time.Time { return time.Date(2026, 11, 25, 21, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = time.Now })
	p, err := Create(bg, s, o, "  Harbour\tmuseum \n guide ", "  Answer in short paragraphs.\r\nCite the guide.  ")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Harbour museum guide" || p.Instructions != "Answer in short paragraphs.\nCite the guide." || p.MemorySpace != "project-harbour-museum-guide" || p.Archived || p.ID == "" {
		t.Fatalf("the project was made as %+v", p)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("n", 41)} {
		if _, err := Create(bg, s, o, bad, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("the name %q gave %v", bad, err)
		}
	}
	if _, err := Create(bg, s, o, "Long", strings.Repeat("i", 201)); !errors.Is(err, ErrInvalid) {
		t.Errorf("instructions that are too long gave %v", err)
	}

	now = func() time.Time { return time.Date(2026, 11, 25, 22, 0, 0, 0, time.UTC) }
	second, err := Create(bg, s, o, "Garden plans", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(bg, s, o, "Third", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(bg, s, o, "Fourth", ""); !errors.Is(err, ErrFull) {
		t.Errorf("a project past the most gave %v", err)
	}

	name, text, yes, space := "Harbour guide", "Be brief.", true, "  harbour  notes "
	now = func() time.Time { return time.Date(2026, 11, 25, 23, 0, 0, 0, time.UTC) }
	changed, err := Update(bg, s, o, p.ID, Change{Name: &name, Instructions: &text, Archived: &yes, MemorySpace: &space})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Name != name || changed.Instructions != text || !changed.Archived || changed.MemorySpace != "harbour notes" || !changed.Changed.After(changed.Created) {
		t.Fatalf("the change gave %+v", changed)
	}
	list, err := List(bg, s)
	if err != nil || len(list) != 3 || list[0].ID != p.ID {
		t.Fatalf("the list is %+v (%v), want the project changed last first", list, err)
	}
	no := false
	if back, err := Update(bg, s, o, p.ID, Change{Archived: &no}); err != nil || back.Archived {
		t.Errorf("bringing it back gave %+v (%v)", back, err)
	}
	empty := ""
	if c, err := Update(bg, s, o, second.ID, Change{MemorySpace: &empty}); err != nil || c.MemorySpace != "" {
		t.Errorf("clearing the memory space gave %+v (%v)", c, err)
	}
	if _, err := Update(bg, s, o, "nothere", Change{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Errorf("changing no project gave %v", err)
	}
	if _, err := Update(bg, s, o, p.ID, Change{Name: &empty}); !errors.Is(err, ErrInvalid) {
		t.Errorf("an empty name gave %v", err)
	}
}

func TestSlugsAndSpaces(t *testing.T) {
	for name, want := range map[string]string{
		"Harbour Museum Guide":  "harbour-museum-guide",
		"  Q4 -- plans!! ":      "q4-plans",
		"Café menus":            "café-menus",
		"!!!":                   "",
		strings.Repeat("a", 70): strings.Repeat("a", 60),
	} {
		if got := Slug(name); got != want {
			t.Errorf("Slug(%q) = %q, want %q", name, got, want)
		}
	}
	if got := SpaceFor("project-{slug}", "!!!", "0123abcd"); got != "project-0123abcd" {
		t.Errorf("a name with no letters gave the space %q", got)
	}
	if got := SpaceFor(" ", "Harbour", "x"); got != "" {
		t.Errorf("an empty pattern gave the space %q", got)
	}
	if got := SpaceFor("work", "Harbour", "x"); got != "work" {
		t.Errorf("a pattern with no slug gave %q", got)
	}
}

func TestFilesAreKeptByTheirHash(t *testing.T) {
	s, _ := stores(t)
	o := options()
	p, err := Create(bg, s, o, "Harbour museum guide", "")
	if err != nil {
		t.Fatal(err)
	}
	notes := "\xEF\xBB\xBFOpening hours\r\nTuesday to Sunday, ten to five.\r\n"
	f, added, err := AddFile(bg, s, o, p.ID, `C:\folder\opening-hours.md`, []byte(notes))
	if err != nil || !added {
		t.Fatalf("adding a file gave %+v %v (%v)", f, added, err)
	}
	if f.ID != hashOf(notes) || f.Name != "opening-hours.md" || f.MediaType != "text/markdown" || f.Size != int64(len(notes)) ||
		f.Text() != "Opening hours\nTuesday to Sunday, ten to five.\n" || f.Chars != len(f.Text()) || f.Sensitive {
		t.Fatalf("the file was kept as %+v with %q", f, f.Text())
	}
	if !onDisk(s, f.ID) {
		t.Fatal("the file is not in the files folder under its hash")
	}
	if again, added, err := AddFile(bg, s, o, p.ID, "copy.md", []byte(notes)); err != nil || added || again.Name != "opening-hours.md" {
		t.Errorf("the same file again gave %+v %v (%v)", again, added, err)
	}

	for name, data := range map[string]string{
		"photo.png":  "\x89PNG\r\n\x1a\nbytes",
		"noending":   "words",
		"empty.txt":  "",
		"blank.txt":  "   \n  ",
		"binary.txt": "a\x00b",
		"latin.txt":  "caf\xe9",
		"big.txt":    strings.Repeat("x", 1001),
	} {
		if _, _, err := AddFile(bg, s, o, p.ID, name, []byte(data)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s gave %v", name, err)
		}
	}
	if _, _, err := AddFile(bg, s, o, p.ID, "prices.csv", []byte("item,price\ntea,2\n")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AddFile(bg, s, o, p.ID, "third.txt", []byte("one too many")); !errors.Is(err, ErrFull) {
		t.Errorf("a file past the most gave %v", err)
	}
	if onDisk(s, hashOf("one too many")) {
		t.Error("a refused file was left in the files folder")
	}
	if _, _, err := AddFile(bg, s, o, "nothere", "a.txt", []byte("words")); !errors.Is(err, ErrNotFound) {
		t.Errorf("a file for no project gave %v", err)
	}

	files, err := Files(bg, s, p.ID)
	if err != nil || len(files) != 2 || files[0].ID != f.ID || files[1].MediaType != "text/csv" {
		t.Fatalf("the files are %+v (%v)", files, err)
	}
	marked, err := MarkFile(bg, s, p.ID, f.ID, true)
	if err != nil || !marked.Sensitive {
		t.Fatalf("marking the file gave %+v (%v)", marked, err)
	}
	if yes, err := Sensitive(bg, s, p.ID); err != nil || !yes {
		t.Errorf("the project with a sensitive file reads as %v (%v)", yes, err)
	}
	if _, err := MarkFile(bg, s, p.ID, hashOf("none"), true); !errors.Is(err, ErrNotFound) {
		t.Errorf("marking no file gave %v", err)
	}
	if _, err := GetFile(bg, s, p.ID, "../keystone.db"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a name that is not a hash gave %v", err)
	}

	// A file another project holds, or a chat holds, stays on disk when one
	// project lets it go.
	other, err := Create(bg, s, o, "Second guide", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := AddFile(bg, s, o, other.ID, "same.md", []byte(notes)); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveFile(bg, s, o, p.ID, f.ID); err != nil || !removed {
		t.Fatalf("removing the file gave %v (%v)", removed, err)
	}
	if !onDisk(s, f.ID) {
		t.Error("a file another project holds was removed from disk")
	}
	held := newChat(t, s)
	if err := chat.AddFile(s, held, f.ID); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveFile(bg, s, o, other.ID, f.ID); err != nil || !removed {
		t.Fatalf("removing the other copy gave %v (%v)", removed, err)
	}
	if !onDisk(s, f.ID) {
		t.Error("a file a chat holds was removed from disk")
	}
	if removed, err := RemoveFile(bg, s, o, other.ID, f.ID); err != nil || removed {
		t.Errorf("removing it twice gave %v (%v)", removed, err)
	}
	// Once nothing holds it, it goes.
	if err := chat.Delete(s, held); err != nil {
		t.Fatal(err)
	}
	if onDisk(s, f.ID) {
		t.Error("a file nothing holds is still on disk")
	}
}

// chat.Delete removes the files no other chat holds; it must keep the ones
// a project holds.
func TestDeletingAChatKeepsAFileAProjectHolds(t *testing.T) {
	s, _ := stores(t)
	o := options()
	p, err := Create(bg, s, o, "Harbour museum guide", "")
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := AddFile(bg, s, o, p.ID, "notes.txt", []byte("Shared words."))
	if err != nil {
		t.Fatal(err)
	}
	c := newChat(t, s)
	if err := chat.AddFile(s, c, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := chat.Delete(s, c); err != nil {
		t.Fatal(err)
	}
	if !onDisk(s, f.ID) {
		t.Fatal("deleting a chat removed a file a project holds")
	}
}

func TestChatsJoinAndLeaveAProject(t *testing.T) {
	s, theirs := stores(t)
	o := options()
	p, err := Create(bg, s, o, "Harbour museum guide", "Be brief.")
	if err != nil {
		t.Fatal(err)
	}
	q, err := Create(bg, s, o, "Garden plans", "")
	if err != nil {
		t.Fatal(err)
	}
	a, b := newChat(t, s), newChat(t, s)
	if _, ok, err := Of(bg, s, a); ok || err != nil {
		t.Fatalf("a new chat is in a project (%v)", err)
	}
	for _, c := range []string{a, b} {
		if moved, err := Move(bg, s, p.ID, c); err != nil || !moved {
			t.Fatalf("moving a chat in gave %v (%v)", moved, err)
		}
	}
	if moved, err := Move(bg, s, p.ID, a); err != nil || moved {
		t.Errorf("moving it in again gave %v (%v)", moved, err)
	}
	if got, ok, err := Of(bg, s, a); !ok || err != nil || got.ID != p.ID || got.Instructions != "Be brief." {
		t.Fatalf("the chat's project is %+v %v (%v)", got, ok, err)
	}
	ids, err := Chats(bg, s, p.ID)
	if err != nil || len(ids) != 2 {
		t.Fatalf("the project's chats are %v (%v)", ids, err)
	}
	files, chats, err := Counts(bg, s)
	if err != nil || chats[p.ID] != 2 || files[p.ID] != 0 {
		t.Errorf("the counts are %v %v (%v)", files, chats, err)
	}
	// Into another project: out of the first.
	if moved, err := Move(bg, s, q.ID, a); err != nil || !moved {
		t.Fatalf("moving it to another project gave %v (%v)", moved, err)
	}
	if got, _, _ := Of(bg, s, a); got.ID != q.ID {
		t.Errorf("after the move the chat is in %q", got.ID)
	}
	if left, err := Leave(bg, s, p.ID, a); err != nil || left {
		t.Errorf("leaving a project it is not in gave %v (%v)", left, err)
	}
	if left, err := Leave(bg, s, q.ID, a); err != nil || !left {
		t.Errorf("leaving its project gave %v (%v)", left, err)
	}
	if _, ok, _ := Of(bg, s, a); ok {
		t.Error("the chat is still in a project")
	}
	// A chat that is deleted leaves the project.
	if err := chat.Delete(s, b); err != nil {
		t.Fatal(err)
	}
	if ids, _ := Chats(bg, s, p.ID); len(ids) != 0 {
		t.Errorf("a deleted chat is still in the project: %v", ids)
	}

	// Fixed rule 1: another tenant's chat and project cannot be named.
	other := newChat(t, theirs)
	if _, err := Move(bg, s, p.ID, other); !errors.Is(err, ErrNotFound) {
		t.Errorf("moving another tenant's chat in gave %v", err)
	}
	if _, err := Move(bg, theirs, p.ID, other); !errors.Is(err, ErrNotFound) {
		t.Errorf("moving a chat into another tenant's project gave %v", err)
	}
	if _, err := Get(bg, theirs, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant read the project: %v", err)
	}
	if _, err := Chats(bg, theirs, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant listed the project's chats: %v", err)
	}
	if _, _, err := AddFile(bg, theirs, o, p.ID, "a.txt", []byte("words")); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant added a file: %v", err)
	}
	if err := Delete(bg, theirs, o, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant deleted the project: %v", err)
	}
	if list, _ := List(bg, theirs); len(list) != 0 {
		t.Errorf("another tenant lists %d projects", len(list))
	}
}

func TestDeletingAProjectKeepsItsChats(t *testing.T) {
	s, _ := stores(t)
	o := options()
	p, err := Create(bg, s, o, "Harbour museum guide", "")
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := AddFile(bg, s, o, p.ID, "notes.txt", []byte("Words of the guide."))
	if err != nil {
		t.Fatal(err)
	}
	c := newChat(t, s)
	if _, err := Move(bg, s, p.ID, c); err != nil {
		t.Fatal(err)
	}
	if err := Delete(bg, s, o, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Get(s, c); err != nil {
		t.Fatalf("the project's chat went with it: %v", err)
	}
	if _, ok, _ := Of(bg, s, c); ok {
		t.Error("the chat is still in a project")
	}
	if onDisk(s, f.ID) {
		t.Error("the project's file is still on disk")
	}
	if err := Delete(bg, s, o, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting it twice gave %v", err)
	}
	var n int
	if err := s.DB().QueryRow("SELECT count(*) FROM project_files").Scan(&n); err != nil || n != 0 {
		t.Errorf("%d file rows are left (%v)", n, err)
	}
}

func TestWhatACallCarries(t *testing.T) {
	w := Words{
		InstructionsHeading: `In the project "{name}":`,
		FileHeading:         `File {name} of "{project}":`,
		CutNote:             "[{chars} more of {name} left out.]",
		FileMaxChars:        100,
	}
	if err := CheckWords(w); err != nil {
		t.Fatal(err)
	}
	if err := CheckWords(Words{FileMaxChars: 99}); !errors.Is(err, ErrOptions) {
		t.Errorf("a file size of 99 gave %v", err)
	}
	p := Project{Name: "Harbour guide", Instructions: "Be brief."}
	if got := Instructions(w, p); got != "In the project \"Harbour guide\":\n\nBe brief." {
		t.Errorf("the instructions read %q", got)
	}
	if got := Instructions(w, Project{Name: "Empty", Instructions: "  "}); got != "" {
		t.Errorf("a project with no instructions gives %q", got)
	}
	if got := Instructions(Words{}, p); got != "Be brief." {
		t.Errorf("with no heading the instructions read %q", got)
	}
	f := File{Name: "hours.md", text: "Tuesday to Sunday."}
	label, text := FileItem(w, p, f)
	if label != `project "Harbour guide": hours.md` || text != "File hours.md of \"Harbour guide\":\nTuesday to Sunday." {
		t.Errorf("the file reads %q, %q", label, text)
	}
	long := File{Name: "long.txt", text: strings.Repeat("é", 130)}
	if _, text := FileItem(w, p, long); !strings.HasSuffix(text, strings.Repeat("é", 100)+"\n[30 more of long.txt left out.]") {
		t.Errorf("a long file reads %q", text)
	}
	secret := File{Sensitive: true}
	if MayShow(secret, false) || !MayShow(secret, true) || !MayShow(File{}, false) {
		t.Error("MayShow does not keep a sensitive file to a call whose models may all read it")
	}
	if one, two := LeftOut(1), LeftOut(2); !strings.Contains(one, "1 knowledge file of this chat's project is left out") || !strings.Contains(two, "2 knowledge files") {
		t.Errorf("the left-out lines read %q and %q", one, two)
	}
}

// Recall in a project chat searches the project's space first.
func TestPreferPutsTheProjectsSpaceFirst(t *testing.T) {
	s, _ := stores(t)
	for _, pg := range []builtin.NewPage{
		{Space: "home", Path: "lighthouse-a", Title: "Lighthouse visit", Body: "The lighthouse visit is on Saturday. Lighthouse lighthouse."},
		{Space: "home", Path: "lighthouse-b", Title: "Lighthouse history", Body: "The lighthouse was built long ago. Lighthouse lighthouse lighthouse."},
		{Space: "project-harbour", Path: "lighthouse-c", Title: "Lighthouse in the guide", Body: "The guide mentions the lighthouse once."},
	} {
		if _, err := builtin.WritePage(bg, s, pg); err != nil {
			t.Fatal(err)
		}
	}
	ks, err := knowledge.Open(bg, builtin.Name, s)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()
	plain, err := ks.Search(bg, knowledge.Query{Text: "lighthouse", Limit: 10})
	if err != nil || len(plain) != 3 {
		t.Fatalf("the plain search found %+v (%v)", plain, err)
	}
	pref := Prefer(ks, "project-harbour")
	hits, err := pref.Search(bg, knowledge.Query{Text: "lighthouse", Limit: 10})
	if err != nil || len(hits) != 3 || hits[0].Space != "project-harbour" {
		t.Fatalf("the preferring search found %+v (%v)", hits, err)
	}
	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.ID] {
			t.Errorf("hit %s is given twice", h.ID)
		}
		seen[h.ID] = true
		if h.Label != knowledge.LabelNormal {
			t.Errorf("hit %s lost its label: %q", h.ID, h.Label)
		}
	}
	if hits, _ := pref.Search(bg, knowledge.Query{Text: "lighthouse", Limit: 2}); len(hits) != 2 || hits[0].Space != "project-harbour" {
		t.Errorf("with a limit of two the search found %+v", hits)
	}
	if hits, _ := pref.Search(bg, knowledge.Query{Text: "lighthouse", Space: "home", Limit: 10}); len(hits) != 2 {
		t.Errorf("a search of one space found %+v", hits)
	}
	if Prefer(ks, " ") != ks {
		t.Error("an empty space wrapped the store")
	}
}
