package uploads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

var bg = context.Background()

// testOptions are the shipped kinds, with a small size limit.
func testOptions() Options {
	return Options{
		MaxBytes:       64 << 10,
		ImageTypes:     []string{"image/png", "image/jpeg", "image/gif", "image/webp"},
		TextExtensions: []string{".txt", ".md", ".csv", ".json", ".html", ".go"},
		DocumentTypes:  []string{"application/pdf"},
	}
}

// pngOf is a small invented picture: a square of one colour.
func pngOf(t *testing.T, side int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for x := 0; x < side; x++ {
		for y := 0; y < side; y++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// pdfOf is the start of an invented PDF: enough for its bytes to be known.
func pdfOf(words string) []byte {
	return []byte("%PDF-1.4\n% invented test document: " + words + "\n%%EOF\n")
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// stores returns two tenants' stores in one install.
func stores(t *testing.T) (*tenant.Install, *tenant.Store, *tenant.Store) {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	var out []*tenant.Store
	for _, name := range []string{"Test Person One", "Test Person Two"} {
		made, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		out = append(out, s)
	}
	return in, out[0], out[1]
}

func newChat(t *testing.T, s *tenant.Store) string {
	t.Helper()
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func add(t *testing.T, s *tenant.Store, chatID, name string, data []byte) Upload {
	t.Helper()
	up, _, err := Add(bg, s, testOptions(), chatID, name, data)
	if err != nil {
		t.Fatalf("adding %s: %v", name, err)
	}
	return up
}

func TestAFileIsKeptUnderItsHashAndTiedToItsChat(t *testing.T) {
	_, s, _ := stores(t)
	chatID := newChat(t, s)
	picture := pngOf(t, 8, color.RGBA{200, 120, 40, 255})

	up, added, err := Add(bg, s, testOptions(), chatID, `C:\Users\someone\Pictures\harbour.png`, picture)
	if err != nil || !added {
		t.Fatalf("Add = %+v, %v, %v", up, added, err)
	}
	if up.ID != hashOf(picture) || up.Kind != KindImage || up.MediaType != "image/png" || up.Size != int64(len(picture)) || up.Name != "harbour.png" {
		t.Errorf("the upload reads %+v", up)
	}
	stored, err := s.Files().ReadFile(up.ID)
	if err != nil || !bytes.Equal(stored, picture) {
		t.Fatalf("the files folder holds %d bytes under the hash: %v", len(stored), err)
	}
	if names, err := chat.Files(s, chatID); err != nil || len(names) != 1 || names[0] != up.ID {
		t.Errorf("the chat holds the files %v: %v", names, err)
	}
	// Nothing but the file is left in the folder: no part written on the way.
	entries, _ := fs.ReadDir(s.Files().FS(), ".")
	if len(entries) != 1 {
		t.Errorf("the files folder holds %d entries, want the one file", len(entries))
	}

	again, added, err := Add(bg, s, testOptions(), chatID, "another-name.png", picture)
	if err != nil || added || again.ID != up.ID || again.Name != "harbour.png" {
		t.Errorf("the same file again gave %+v, added %v, %v: it is the same upload", again, added, err)
	}
	if list, err := List(bg, s, chatID); err != nil || len(list) != 1 {
		t.Errorf("the chat lists %d uploads: %v", len(list), err)
	}
}

// A project's knowledge file of the same bytes is the same file in the files
// folder, so removing the upload keeps it (P9-01).
func TestRemovingAnUploadKeepsAProjectsFileOfTheSameBytes(t *testing.T) {
	_, s, _ := stores(t)
	chatID := newChat(t, s)
	notes := []byte("Invented notes for a test project.\n")
	up := add(t, s, chatID, "notes.txt", notes)
	const now = "2026-11-25T00:00:00Z"
	if _, err := s.DB().Exec(`INSERT INTO projects(id, name, created_at, updated_at) VALUES('p1', 'Test project', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO project_files(project_id, hash, name, media_type, size, added_at) VALUES('p1', ?, 'notes.txt', 'text/plain', ?, ?)`, up.ID, len(notes), now); err != nil {
		t.Fatal(err)
	}
	if err := Remove(bg, s, chatID, up.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Files().Stat(up.ID); err != nil {
		t.Errorf("the project's file went with the upload: %v", err)
	}
}

func TestAnImageOrADocumentIsKnownByItsBytes(t *testing.T) {
	_, s, _ := stores(t)
	chatID := newChat(t, s)
	picture := pngOf(t, 4, color.RGBA{10, 20, 30, 255})
	cases := []struct {
		name  string
		data  []byte
		kind  Kind
		media string
	}{
		{"named-like-text.txt", picture, KindImage, "image/png"},
		{"timetable.bin", pdfOf("a timetable"), KindDocument, "application/pdf"},
		{"notes.md", []byte("# Invented notes\n\nTwo lines.\r\n"), KindText, "text/markdown"},
		{"prices.csv", []byte("loaf,price\nrye,4\n"), KindText, "text/csv"},
		{"page.HTML", []byte("<p>An invented page</p>"), KindText, "text/html"},
		{"main.go", []byte("package main\n"), KindText, "text/plain"},
	}
	for _, c := range cases {
		up, _, err := Add(bg, s, testOptions(), chatID, c.name, c.data)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if up.Kind != c.kind || up.MediaType != c.media {
			t.Errorf("%s is a %s of type %s, want a %s of type %s", c.name, up.Kind, up.MediaType, c.kind, c.media)
		}
	}
	refused := map[string][]byte{
		"empty.txt":          {},
		"picture.png":        []byte("this is text with the name of a picture"),
		"no-ending":          []byte("text with no ending"),
		"binary.txt":         {'a', 0, 'b'},
		"latin.txt":          {'c', 'a', 'f', 0xe9},
		"drawing.svg":        []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"too-large.txt":      bytes.Repeat([]byte("x"), int(testOptions().MaxBytes)+1),
		"program.exe":        {'M', 'Z', 0x90, 0},
		"../../escape.png":   []byte("not a picture"),
		"spreadsheet.xlsx":   {'P', 'K', 3, 4},
		"archive.zip":        {'P', 'K', 3, 4},
		"unknown-image.tiff": {'I', 'I', '*', 0},
	}
	for name, data := range refused {
		if up, _, err := Add(bg, s, testOptions(), chatID, name, data); !errors.Is(err, ErrRefused) {
			t.Errorf("%s was not refused: %+v, %v", name, up, err)
		}
	}
	entries, _ := fs.ReadDir(s.Files().FS(), ".")
	if len(entries) != len(cases) {
		t.Errorf("the files folder holds %d files, want the %d taken", len(entries), len(cases))
	}
}

func TestTheTextOfAFileIsRead(t *testing.T) {
	_, s, _ := stores(t)
	chatID := newChat(t, s)
	notes := add(t, s, chatID, "notes.txt", append([]byte{0xEF, 0xBB, 0xBF}, []byte("First line\r\nSecond line\r\n")...))
	if notes.Text() != "First line\nSecond line\n" || notes.Chars != len("First line\nSecond line\n") {
		t.Errorf("the text read is %q (%d characters)", notes.Text(), notes.Chars)
	}
	picture := add(t, s, chatID, "a.png", pngOf(t, 2, color.RGBA{1, 2, 3, 255}))
	if picture.Text() != "" || picture.Chars != 0 {
		t.Errorf("an image has the text %q", picture.Text())
	}
}

func TestHTMLIsReadAsItsWords(t *testing.T) {
	page := `<!DOCTYPE html><html><head><title>Harbour bakery</title>
<style>p { color: red }</style><script>var secret = "not words";</script></head>
<body><!-- a comment --><h1>Opening hours</h1>
<p>Open &amp; warm, 7&nbsp;to&nbsp;3. Prices: 1 &lt; 2 and 3 < 4.</p>
<ul><li>Rye</li><li>Seeded <b>spelt</b></li></ul>
<table><tr><th>Loaf</th><th>Price</th></tr><tr><td>Rye</td><td>4</td></tr></table>
<p>Line<br>break</p><noscript>Turn on scripts</noscript><svg><text>drawn</text></svg></body></html>`
	got := Extract("text/html", []byte(page))
	want := "Harbour bakery\n\nOpening hours\n\nOpen & warm, 7 to 3. Prices: 1 < 2 and 3 < 4.\n\n- Rye\n- Seeded spelt\n\nLoaf | Price\nRye | 4\n\nLine\nbreak"
	if got != want {
		t.Errorf("the words of the page are\n%q\nwant\n%q", got, want)
	}
	for _, gone := range []string{"color: red", "not words", "a comment", "Turn on scripts", "drawn"} {
		if strings.Contains(got, gone) {
			t.Errorf("the words hold %q", gone)
		}
	}
	if got := Extract("text/html", []byte("<p>No end to this <b")); got != "No end to this <b" {
		t.Errorf("a tag with no end reads as %q", got)
	}
	if got := Extract("text/plain", []byte("<p>kept as it is</p>")); got != "<p>kept as it is</p>" {
		t.Errorf("plain text that looks like markup reads as %q", got)
	}
}

func TestAnUploadIsRemovedOnlyWhileNoMessageHoldsIt(t *testing.T) {
	_, s, _ := stores(t)
	one, two := newChat(t, s), newChat(t, s)
	shared := pngOf(t, 3, color.RGBA{9, 9, 9, 255})
	a := add(t, s, one, "shared.png", shared)
	add(t, s, two, "shared.png", shared)
	lonely := add(t, s, one, "lonely.txt", []byte("only in chat one"))

	// Removed from one chat, the shared file stays for the other.
	if err := Remove(bg, s, one, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Files().Stat(a.ID); err != nil {
		t.Errorf("the file another chat holds was removed: %v", err)
	}
	if _, err := Get(bg, s, one, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the removed upload is still there: %v", err)
	}

	// A message names the other upload: it stays.
	if _, err := chat.Append(s, one, chat.NewMessage{Role: seam.RoleUser, Blocks: []seam.Block{
		{Type: seam.BlockText, Text: "What does it say?"}, BlockOf(lonely),
	}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(bg, s, one, lonely.ID); !got.InUse {
		t.Error("an upload a message names is not marked in use")
	}
	if err := Remove(bg, s, one, lonely.ID); !errors.Is(err, ErrInUse) {
		t.Errorf("removing an upload a message names was not refused: %v", err)
	}

	// Deleting a chat removes the files no other chat holds.
	if err := chat.Delete(s, one); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Files().Stat(lonely.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the deleted chat's own file is still there: %v", err)
	}
	if _, err := s.Files().Stat(a.ID); err != nil {
		t.Errorf("the file the other chat holds went with the deleted chat: %v", err)
	}
	if err := Remove(bg, s, two, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Files().Stat(a.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the file no chat holds is still there: %v", err)
	}
}

func TestOneTenantNeverReadsAnothersUploads(t *testing.T) {
	_, a, b := stores(t)
	mine := newChat(t, a)
	up := add(t, a, mine, "private.txt", []byte("tenant one's invented notes"))
	if _, err := Get(bg, b, mine, up.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("tenant two found tenant one's upload: %v", err)
	}
	if _, err := Read(b, up.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("tenant two read tenant one's file: %v", err)
	}
	for _, name := range []string{"../" + up.ID, "..", "/etc/passwd", up.ID + "/..", strings.ToUpper(up.ID)} {
		if _, err := Read(a, name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Read(%q) was not refused: %v", name, err)
		}
	}
	// Tenant two's own chat cannot be given tenant one's file by naming it.
	theirs := newChat(t, b)
	if _, err := Blocks(bg, b, theirs, []seam.Block{{Type: seam.BlockFile, MediaType: "text/plain", Name: "private.txt", Ref: up.ID}}); !errors.Is(err, ErrRefused) {
		t.Errorf("a message naming another tenant's upload was not refused: %v", err)
	}
}

func TestBlocksAreWrittenAsTheirUploadSays(t *testing.T) {
	_, s, _ := stores(t)
	chatID := newChat(t, s)
	picture := add(t, s, chatID, "harbour.png", pngOf(t, 2, color.RGBA{5, 5, 5, 255}))
	notes := add(t, s, chatID, "notes.txt", []byte("invented"))
	got, err := Blocks(bg, s, chatID, []seam.Block{
		{Type: seam.BlockText, Text: "Look"},
		{Type: seam.BlockFile, MediaType: "application/octet-stream", Name: "lie.exe", Ref: picture.ID},
		{Type: seam.BlockImage, MediaType: "image/gif", Ref: notes.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []seam.Block{
		{Type: seam.BlockText, Text: "Look"},
		{Type: seam.BlockImage, MediaType: "image/png", Ref: picture.ID},
		{Type: seam.BlockFile, MediaType: "text/plain", Name: "notes.txt", Ref: notes.ID},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d blocks", len(got))
	}
	for i := range want {
		if got[i].Type != want[i].Type || got[i].MediaType != want[i].MediaType || got[i].Name != want[i].Name || got[i].Ref != want[i].Ref {
			t.Errorf("block %d is %+v, want %+v", i, got[i], want[i])
		}
	}
	if _, err := Blocks(bg, s, chatID, []seam.Block{{Type: seam.BlockImage, MediaType: "image/png", Ref: hashOf([]byte("never uploaded"))}}); !errors.Is(err, ErrRefused) {
		t.Errorf("a ref that names no upload was not refused: %v", err)
	}
}

func TestSizesReadForAPerson(t *testing.T) {
	for n, want := range map[int64]string{512: "512 bytes", 34_400: "34 kB", 5_242_880: "5.2 MB", 2_000_000: "2 MB"} {
		if got := Size(n); got != want {
			t.Errorf("Size(%d) = %q, want %q", n, got, want)
		}
	}
}
