package uploads

import (
	"bytes"
	"image/color"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

var testWords = Words{
	TextMaxChars:     40,
	FileHeading:      "File {name}:",
	FileMarker:       "[file {name} attached]",
	TextCutNote:      "[{chars} more characters of {name} left out]",
	ImageWithheld:    "[image {name} not shown: no vision]",
	DocumentWithheld: "[document {name} not shown: no files]",
}

func text(s string) seam.Block { return seam.Block{Type: seam.BlockText, Text: s} }

// imagesIn counts the image blocks of messages, tool results included.
func imagesIn(messages []seam.Message) int {
	return seam.Images(seam.Request{Messages: messages})
}

func TestAnImageGoesOnlyToARouteThatSees(t *testing.T) {
	_, s, _ := stores(t)
	chatID := newChat(t, s)
	earlier := add(t, s, chatID, "earlier.png", pngOf(t, 4, color.RGBA{255, 0, 0, 255}))
	now := add(t, s, chatID, "now.png", pngOf(t, 4, color.RGBA{0, 0, 255, 255}))
	branch := []seam.Message{
		{Role: seam.RoleUser, Blocks: []seam.Block{text("What is this?"), BlockOf(earlier)}},
		{Role: seam.RoleAssistant, Blocks: []seam.Block{text("A red square.")}},
		{Role: seam.RoleUser, Blocks: []seam.Block{BlockOf(now), text("And this one?")}},
	}
	src := ChatSource(bg, s, chatID)

	seen := Prepare(branch, 2, Route{Sees: true}, testWords, src)
	if n := imagesIn(seen.Messages); n != 2 {
		t.Fatalf("a route that sees was sent %d images, want 2", n)
	}
	got := seen.Messages[2].Blocks[0]
	stored, _ := Read(s, now.ID)
	if got.Type != seam.BlockImage || got.Ref != "" || got.MediaType != "image/png" || !bytes.Equal(got.Data, stored) {
		t.Errorf("the image went as %s %q ref %q with %d bytes, want its bytes", got.Type, got.MediaType, got.Ref, len(got.Data))
	}
	if err := seam.ValidateRequest(seam.Request{Messages: seen.Messages}); err != nil {
		t.Errorf("the readied branch is not a valid request: %v", err)
	}
	if len(seen.Call.Items) != 2 || seen.Call.Items[0].Current || !seen.Call.Items[1].Current || seen.Call.Items[1].Sent != SentImage {
		t.Errorf("the call's record is %+v", seen.Call)
	}
	// The branch it was given is not changed.
	if branch[2].Blocks[0].Ref != now.ID || len(branch[2].Blocks[0].Data) != 0 {
		t.Error("Prepare changed the branch it was given")
	}

	blind := Prepare(branch, 2, Route{Sees: false, Files: true}, testWords, src)
	if n := imagesIn(blind.Messages); n != 0 {
		t.Fatalf("a route with a model that cannot see was sent %d images", n)
	}
	if got := blind.Messages[2].Blocks[0]; got.Type != seam.BlockText || got.Text != "[image now.png not shown: no vision]" {
		t.Errorf("in place of the image: %+v", got)
	}
	withheld := blind.Call.Withheld()
	if len(withheld) != 2 || withheld[1].ID != now.ID || withheld[1].Why != WhyVision || !withheld[1].Current || withheld[0].Current {
		t.Errorf("withheld: %+v", withheld)
	}
	for i, m := range blind.Messages {
		if len(m.Blocks) != len(branch[i].Blocks) {
			t.Errorf("message %d has %d blocks, want %d: an index into the branch must still hold", i, len(m.Blocks), len(branch[i].Blocks))
		}
	}
}

func TestAnImageAnywhereIsWithheldFromARouteThatCannotSee(t *testing.T) {
	_, s, _ := stores(t)
	chatID := newChat(t, s)
	inline := seam.Block{Type: seam.BlockImage, MediaType: "image/png", Data: pngOf(t, 2, color.RGBA{1, 1, 1, 255})}
	branch := []seam.Message{
		{Role: seam.RoleUser, Blocks: []seam.Block{text("Draw me something"), inline}},
		{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockToolCall, ID: "call_1", Name: "draw", Input: []byte(`{}`)}}},
		{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockToolResult, ID: "call_1", Content: []seam.Block{text("drawn"), inline}}}},
	}
	out := Prepare(branch, 0, Route{}, testWords, ChatSource(bg, s, chatID))
	if n := imagesIn(out.Messages); n != 0 {
		t.Fatalf("%d images in the message and in a tool's result reached a route that cannot see", n)
	}
	if err := seam.ValidateRequest(seam.Request{Messages: out.Messages}); err != nil {
		t.Errorf("not a valid request: %v", err)
	}
	if len(out.Call.Withheld()) != 2 {
		t.Errorf("withheld: %+v", out.Call.Items)
	}
	// A route that sees gets them as they were.
	if n := imagesIn(Prepare(branch, 0, Route{Sees: true}, testWords, ChatSource(bg, s, chatID)).Messages); n != 2 {
		t.Errorf("a route that sees got %d of the 2 images", n)
	}
}

func TestATextFilesTextIsAnAttachment(t *testing.T) {
	_, s, _ := stores(t)
	chatID := newChat(t, s)
	older := add(t, s, chatID, "older.txt", []byte("The older invented notes."))
	long := add(t, s, chatID, "long.md", []byte(strings.Repeat("ab", 30)))
	branch := []seam.Message{
		{Role: seam.RoleUser, Blocks: []seam.Block{text("Read this"), BlockOf(older)}},
		{Role: seam.RoleAssistant, Blocks: []seam.Block{text("Read.")}},
		{Role: seam.RoleUser, Blocks: []seam.Block{text("And these"), BlockOf(long), BlockOf(older)}},
	}
	out := Prepare(branch, 2, Route{}, testWords, ChatSource(bg, s, chatID))
	if len(out.Attached) != 2 {
		t.Fatalf("%d attachments, want 2: each file once", len(out.Attached))
	}
	// The newest first; the older file, named again last, is newest.
	if out.Attached[0].Ref != RefOf(older.ID) || out.Attached[1].Ref != RefOf(long.ID) {
		t.Errorf("the attachments are in the order %s, %s", out.Attached[0].Ref, out.Attached[1].Ref)
	}
	if out.Attached[0].Text != "File older.txt:\n\nThe older invented notes." || out.Attached[0].Label != "file: older.txt" {
		t.Errorf("the older file's attachment is %+v", out.Attached[0])
	}
	cut := out.Attached[1].Text
	if !strings.HasPrefix(cut, "File long.md:\n\n"+strings.Repeat("ab", 20)) || !strings.HasSuffix(cut, "[20 more characters of long.md left out]") {
		t.Errorf("the long file's attachment is %q", cut)
	}
	if got := out.Messages[2].Blocks[1]; got.Type != seam.BlockText || got.Text != "[file long.md attached]" {
		t.Errorf("in the message, in place of the file: %+v", got)
	}
	var given *Given
	for i := range out.Call.Items {
		if out.Call.Items[i].ID == long.ID {
			given = &out.Call.Items[i]
		}
	}
	if given == nil || given.Sent != SentText || given.Chars != 40 || !given.Cut || !given.Current {
		t.Errorf("the long file's record is %+v", given)
	}
	// A text file is never sent as a file: its text is the attachment.
	for _, m := range out.Messages {
		for _, b := range m.Blocks {
			if b.Type == seam.BlockFile {
				t.Errorf("a file block was sent: %+v", b)
			}
		}
	}
}

func TestADocumentGoesWholeOnlyToARouteThatTakesFiles(t *testing.T) {
	_, s, _ := stores(t)
	chatID := newChat(t, s)
	doc := add(t, s, chatID, "timetable.pdf", pdfOf("ferry times"))
	branch := []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{text("Summarise"), BlockOf(doc)}}}
	src := ChatSource(bg, s, chatID)

	taken := Prepare(branch, 0, Route{Files: true}, testWords, src)
	if b := taken.Messages[0].Blocks[1]; b.Type != seam.BlockFile || b.Name != "timetable.pdf" || b.MediaType != "application/pdf" || b.Ref != "" || !bytes.Equal(b.Data, pdfOf("ferry times")) {
		t.Errorf("to a route that takes files the document went as %+v", b)
	}
	refused := Prepare(branch, 0, Route{Sees: true}, testWords, src)
	if b := refused.Messages[0].Blocks[1]; b.Type != seam.BlockText || b.Text != "[document timetable.pdf not shown: no files]" {
		t.Errorf("to a route that takes no files the document went as %+v", b)
	}
	if w := refused.Call.Withheld(); len(w) != 1 || w[0].Why != WhyFiles {
		t.Errorf("withheld: %+v", w)
	}
}

func TestWhatIsNoLongerStoredIsWithheld(t *testing.T) {
	_, s, _ := stores(t)
	chatID := newChat(t, s)
	gone := hashOf([]byte("an invented file that was never stored"))
	branch := []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{
		{Type: seam.BlockImage, MediaType: "image/png", Ref: gone},
		{Type: seam.BlockFile, MediaType: "text/plain", Name: "gone.txt", Ref: gone},
	}}}
	out := Prepare(branch, 0, Route{Sees: true, Files: true}, testWords, ChatSource(bg, s, chatID))
	if n := imagesIn(out.Messages); n != 0 {
		t.Errorf("%d images went for a file that is not stored", n)
	}
	for _, g := range out.Call.Items {
		if g.Sent != SentWithheld || g.Why != WhyMissing {
			t.Errorf("a file that is not stored is recorded as %+v", g)
		}
	}
	if !Holds(branch) || Holds([]seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{text("plain")}}}) {
		t.Error("Holds is wrong")
	}
}
