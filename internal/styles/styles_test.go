package styles

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every style and word here is invented.

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

const builtInList = `[
	{"id": "normal", "name": "Normal", "instructions": ""},
	{"id": "brief", "name": "Brief", "instructions": "Answer in two sentences at most.\r\n"},
	{"id": "plain-words", "name": "  Plain   words ", "instructions": "Use everyday words."}
]`

func options(t *testing.T) Options {
	t.Helper()
	b, err := ParseBuiltIn(builtInList)
	if err != nil {
		t.Fatal(err)
	}
	return Options{BuiltIn: b, MaxStyles: 3, NameMaxChars: 20, InstructionsMaxChars: 80}
}

func newChat(t *testing.T, s *tenant.Store) string {
	t.Helper()
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func TestCheckOptions(t *testing.T) {
	if err := CheckOptions(options(t)); err != nil {
		t.Fatalf("good options are refused: %v", err)
	}
	for key, change := range map[string]func(*Options){
		SettingMaxStyles:            func(o *Options) { o.MaxStyles = -1 },
		SettingNameMaxChars:         func(o *Options) { o.NameMaxChars = 0 },
		SettingInstructionsMaxChars: func(o *Options) { o.InstructionsMaxChars = 50001 },
	} {
		o := options(t)
		change(&o)
		if err := CheckOptions(o); !errors.Is(err, ErrOptions) || !strings.Contains(err.Error(), key) {
			t.Errorf("a bad %s gives %v", key, err)
		}
	}
}

func TestTheBuiltInStylesAreRead(t *testing.T) {
	b, err := ParseBuiltIn(builtInList)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 3 || b[0].ID != "normal" || b[0].Instructions != "" || !b[0].BuiltIn {
		t.Fatalf("the list reads as %+v", b)
	}
	if b[1].Instructions != "Answer in two sentences at most." || b[2].Name != "Plain words" {
		t.Errorf("the instructions and names are not tidied: %+v", b)
	}
	if b, err := ParseBuiltIn("  "); err != nil || len(b) != 0 {
		t.Errorf("an empty setting gives %v, %v", b, err)
	}
	for name, raw := range map[string]string{
		"not JSON":             `Normal: nothing`,
		"an unknown field":     `[{"id":"a","name":"A","instructions":"","colour":"teal"}]`,
		"an id in capitals":    `[{"id":"Normal","name":"Normal","instructions":""}]`,
		"an id with a space":   `[{"id":"a b","name":"A","instructions":""}]`,
		"an own style's shape": `[{"id":"0123456789abcdef","name":"A","instructions":""}]`,
		"no name":              `[{"id":"a","name":"  ","instructions":""}]`,
		"an id twice":          `[{"id":"a","name":"A","instructions":""},{"id":"a","name":"B","instructions":""}]`,
		"a name twice":         `[{"id":"a","name":"Same","instructions":""},{"id":"b","name":"same","instructions":""}]`,
	} {
		if _, err := ParseBuiltIn(raw); !errors.Is(err, ErrOptions) || !strings.Contains(err.Error(), SettingBuiltIn) {
			t.Errorf("%s gives %v", name, err)
		}
	}
}

func TestAPersonsOwnStyles(t *testing.T) {
	s, _ := stores(t)
	o := options(t)
	made, err := Create(bg, s, o, " Ship's\nlog ", "  Write like a ship's log: time, place, event.\r\nNo opinions.  ")
	if err != nil {
		t.Fatal(err)
	}
	if !IsOwnID(made.ID) || made.Name != "Ship's log" || made.Instructions != "Write like a ship's log: time, place, event.\nNo opinions." || made.BuiltIn || made.Created.IsZero() {
		t.Fatalf("the style was made as %+v", made)
	}
	if got, err := Get(bg, s, o, made.ID); err != nil || got != made {
		t.Errorf("reading it back gives %+v, %v", got, err)
	}
	if got, err := Get(bg, s, o, "brief"); err != nil || !got.BuiltIn || got.Name != "Brief" {
		t.Errorf("a built-in style reads as %+v, %v", got, err)
	}

	// Refusals.
	for name, c := range map[string]struct{ name, text string }{
		"a blank name":            {"  ", "Some words."},
		"a long name":             {strings.Repeat("n", 21), "Some words."},
		"no instructions":         {"Quiet", " \n "},
		"long instructions":       {"Quiet", strings.Repeat("w", 81)},
		"a built-in style's name": {"plain WORDS", "Some words."},
		"one of the person's own": {"SHIP'S LOG", "Some words."},
	} {
		if _, err := Create(bg, s, o, c.name, c.text); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s gives %v", name, err)
		}
	}
	if _, err := Create(bg, s, o, "garden", "Answer as a gardener would."); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(bg, s, o, "almanac", "Answer with dates first."); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(bg, s, o, "One too many", "Words."); !errors.Is(err, ErrFull) {
		t.Errorf("a style past the most gives %v", err)
	}
	if n, err := Count(bg, s); err != nil || n != 3 {
		t.Errorf("the person has %d styles (%v)", n, err)
	}

	// The list: the built-in ones in their order, then the person's own by
	// name, whatever the letter case.
	all, err := List(bg, s, o)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, st := range all {
		names = append(names, st.Name)
	}
	if got := strings.Join(names, ", "); got != "Normal, Brief, Plain words, almanac, garden, Ship's log" {
		t.Errorf("the list is %s", got)
	}

	// Changes.
	renamed := "Logbook"
	changed, err := Update(bg, s, o, made.ID, Change{Name: &renamed})
	if err != nil || changed.Name != "Logbook" || changed.Instructions != made.Instructions {
		t.Errorf("renaming gives %+v, %v", changed, err)
	}
	words := "Time, place, event, in one line."
	if changed, err = Update(bg, s, o, made.ID, Change{Instructions: &words}); err != nil || changed.Instructions != words || changed.Name != "Logbook" {
		t.Errorf("new instructions give %+v, %v", changed, err)
	}
	same := "LOGBOOK"
	if _, err := Update(bg, s, o, made.ID, Change{Name: &same}); err != nil {
		t.Errorf("renaming a style to its own name in other letters gives %v", err)
	}
	taken := "Almanac"
	if _, err := Update(bg, s, o, made.ID, Change{Name: &taken}); !errors.Is(err, ErrInvalid) {
		t.Errorf("renaming to another style's name gives %v", err)
	}
	if _, err := Update(bg, s, o, "brief", Change{Name: &renamed}); !errors.Is(err, ErrBuiltIn) {
		t.Errorf("changing a built-in style gives %v", err)
	}
	if _, err := Update(bg, s, o, "0123456789abcdef", Change{Name: &renamed}); !errors.Is(err, ErrNotFound) {
		t.Errorf("changing a style that is not there gives %v", err)
	}
	if err := Delete(bg, s, o, "normal"); !errors.Is(err, ErrBuiltIn) {
		t.Errorf("deleting a built-in style gives %v", err)
	}
	if err := Delete(bg, s, o, "nothere"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting a style that is not there gives %v", err)
	}
	if _, err := Get(bg, s, o, "nothere"); !errors.Is(err, ErrNotFound) {
		t.Errorf("reading a style that is not there gives %v", err)
	}
}

func TestTheStyleAChatUses(t *testing.T) {
	s, _ := stores(t)
	o := options(t)
	id := newChat(t, s)
	mine, err := Create(bg, s, o, "Ledger", "Answer as a table.")
	if err != nil {
		t.Fatal(err)
	}

	// No style of its own: the default, or none when the default names no
	// style.
	if st, source, err := Resolve(bg, s, o, id, "brief"); err != nil || source != SourceDefault || st.ID != "brief" {
		t.Errorf("with the default brief the chat uses %+v from %s (%v)", st, source, err)
	}
	if st, source, err := Resolve(bg, s, o, id, "gone"); err != nil || source != SourceNone || st.ID != "" {
		t.Errorf("with a default that is not there the chat uses %+v from %s (%v)", st, source, err)
	}

	// Its first turn keeps the default; a second keep changes nothing.
	if kept, err := Keep(bg, s, id, "brief"); err != nil || !kept {
		t.Fatalf("keeping the default gives %v, %v", kept, err)
	}
	if kept, err := Keep(bg, s, id, "plain-words"); err != nil || kept {
		t.Errorf("keeping another over it gives %v, %v", kept, err)
	}
	if st, source, err := Resolve(bg, s, o, id, "plain-words"); err != nil || source != SourceChat || st.ID != "brief" {
		t.Errorf("after the default changed the chat uses %+v from %s (%v)", st, source, err)
	}

	// Chosen: the person's own style.
	if st, err := Choose(bg, s, o, id, mine.ID); err != nil || st.ID != mine.ID {
		t.Fatalf("choosing gives %+v, %v", st, err)
	}
	if got, ok, err := Of(bg, s, id); err != nil || !ok || got != mine.ID {
		t.Errorf("the chat's own style is %q, %v (%v)", got, ok, err)
	}
	if _, err := Choose(bg, s, o, id, "nothere"); !errors.Is(err, ErrNotFound) {
		t.Errorf("choosing a style that is not there gives %v", err)
	}
	if _, err := Choose(bg, s, o, "nochat", "brief"); !errors.Is(err, ErrNotFound) {
		t.Errorf("choosing for a chat that is not there gives %v", err)
	}
	if _, err := Keep(bg, s, "nochat", "brief"); !errors.Is(err, ErrNotFound) {
		t.Errorf("keeping for a chat that is not there gives %v", err)
	}

	// Deleting the style leaves the chat with none of its own: it uses the
	// default again, and its next turn keeps that.
	if err := Delete(bg, s, o, mine.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Of(bg, s, id); err != nil || ok {
		t.Errorf("after its style was deleted the chat still has one (%v)", err)
	}
	if st, source, _ := Resolve(bg, s, o, id, "normal"); source != SourceDefault || st.ID != "normal" {
		t.Errorf("after its style was deleted the chat uses %+v from %s", st, source)
	}

	// A built-in style taken out of the setting counts as none of its own.
	if _, err := Choose(bg, s, o, id, "plain-words"); err != nil {
		t.Fatal(err)
	}
	fewer := o
	fewer.BuiltIn = o.BuiltIn[:2]
	if st, source, _ := Resolve(bg, s, fewer, id, "brief"); source != SourceDefault || st.ID != "brief" {
		t.Errorf("with its built-in style gone the chat uses %+v from %s", st, source)
	}

	// The row goes with the chat.
	if err := chat.Delete(s, id); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB().QueryRow("SELECT count(*) FROM chat_styles").Scan(&n); err != nil || n != 0 {
		t.Errorf("after the chat was deleted %d rows are left (%v)", n, err)
	}
}

// Fixed rule 1: a style and a chat of one tenant cannot be named from
// another tenant's store.
func TestAnotherTenantsStyleIsNotThere(t *testing.T) {
	one, two := stores(t)
	o := options(t)
	mine, err := Create(bg, one, o, "Ledger", "Answer as a table.")
	if err != nil {
		t.Fatal(err)
	}
	myChat, theirChat := newChat(t, one), newChat(t, two)
	if _, err := Get(bg, two, o, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant reads the style: %v", err)
	}
	name := "Taken"
	if _, err := Update(bg, two, o, mine.ID, Change{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant renames the style: %v", err)
	}
	if err := Delete(bg, two, o, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant deletes the style: %v", err)
	}
	if _, err := Choose(bg, two, o, theirChat, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant gives its chat the style: %v", err)
	}
	if _, err := Choose(bg, one, o, theirChat, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the owner gives another tenant's chat the style: %v", err)
	}
	if all, err := List(bg, two, o); err != nil || len(all) != len(o.BuiltIn) {
		t.Errorf("another tenant lists %+v (%v)", all, err)
	}
	// The other tenant may use the same name for a style of its own.
	if _, err := Create(bg, two, o, "Ledger", "Their own words."); err != nil {
		t.Errorf("another tenant cannot name a style as the first did: %v", err)
	}
	if got, err := Get(bg, one, o, mine.ID); err != nil || got.Name != "Ledger" || got.Instructions != "Answer as a table." {
		t.Errorf("after the other tenant's calls the style reads %+v (%v)", got, err)
	}
	if _, ok, _ := Of(bg, one, myChat); ok {
		t.Error("the owner's chat was given a style")
	}
}

func TestWhatACallCarries(t *testing.T) {
	st := Style{Name: "Brief", Instructions: "  Two sentences at most. "}
	if got := Text(`The person chose "{name}":`, st); got != "The person chose \"Brief\":\n\nTwo sentences at most." {
		t.Errorf("with a heading: %q", got)
	}
	if got := Text("  ", st); got != "Two sentences at most." {
		t.Errorf("with no heading: %q", got)
	}
	if got := Text(`The person chose "{name}":`, Style{Name: "Normal"}); got != "" {
		t.Errorf("a style with no instructions sends %q", got)
	}
	if got := Text("Anything", Style{}); got != "" {
		t.Errorf("no style sends %q", got)
	}
}

func TestReasonDropsThePrefix(t *testing.T) {
	_, err := cleanName("", 10)
	if got := Reason(err); got != "a style's name is 1 to 10 characters" {
		t.Errorf("the reason is %q", got)
	}
}
