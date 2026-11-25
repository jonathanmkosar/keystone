package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// Every passphrase and file here is invented.
const passphrase = "an invented passphrase for a test"

var when = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

type testEntry struct {
	name string
	dir  bool
	data []byte
}

// sample is a few entries, one of them several chunks long even after
// compression, so the file has more than two chunks.
func sample(t *testing.T) []testEntry {
	t.Helper()
	big := make([]byte, 3*chunkSize+123)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	return []testEntry{
		{name: "manifest.json", data: []byte(`{"invented": true}`)},
		{name: "tenant", dir: true},
		{name: "tenant/keystone.db", data: big},
		{name: "tenant/files", dir: true},
		{name: "tenant/files/empty"},
		{name: "tenant/files/note.txt", data: []byte("an invented note")},
	}
}

func write(t *testing.T, entries []testEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := Create(&buf, passphrase, MinRounds)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.dir {
			err = w.Dir(e.name, when)
		} else {
			err = w.File(e.name, int64(len(e.data)), when, bytes.NewReader(e.data))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// readAll reads every entry and its bytes, and returns the first error.
func readAll(r *Reader) ([]testEntry, error) {
	var out []testEntry
	for {
		e, err := r.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		data, err := io.ReadAll(r)
		if err != nil {
			return out, err
		}
		if int64(len(data)) != e.Size {
			return out, errors.New("an entry's bytes are not its size")
		}
		out = append(out, testEntry{name: e.Name, dir: e.Dir, data: data})
	}
}

func TestARoundTrip(t *testing.T) {
	entries := sample(t)
	file := write(t, entries)
	if bytes.Contains(file, []byte("an invented note")) || bytes.Contains(file, []byte("manifest.json")) {
		t.Error("the file holds its contents in the clear")
	}
	if n := (len(file) - headerSize) / (chunkSize + 16); n < 3 {
		t.Fatalf("the sample makes %d chunks; the test wants more than two", n)
	}

	r, err := Open(bytes.NewReader(file), passphrase)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(entries) {
		t.Fatalf("read %d entries, wrote %d", len(got), len(entries))
	}
	for i, e := range entries {
		if got[i].name != e.name || got[i].dir != e.dir || !bytes.Equal(got[i].data, e.data) {
			t.Errorf("entry %d: read %q (folder %v, %d bytes), wrote %q (folder %v, %d bytes)",
				i, got[i].name, got[i].dir, len(got[i].data), e.name, e.dir, len(e.data))
		}
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("Next after the end gave %v", err)
	}

	// Reopen reads it again without the passphrase.
	again, err := r.Reopen(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := readAll(again); err != nil || len(got) != len(entries) {
		t.Errorf("Reopen read %d entries: %v", len(got), err)
	}
	// But not another file, even one made with the same passphrase.
	if _, err := r.Reopen(bytes.NewReader(write(t, entries))); !errors.Is(err, ErrDamaged) {
		t.Errorf("Reopen of another file gave %v", err)
	}
}

func TestTheKeepersMatch(t *testing.T) {
	var buf bytes.Buffer
	w, err := Create(&buf, passphrase, MinRounds)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{3}, 32)
	wrapped, err := w.Keeper().Wrap(key, "a context")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(bytes.NewReader(buf.Bytes()), passphrase)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Keeper().Unwrap(wrapped, "a context")
	if err != nil || !bytes.Equal(got, key) {
		t.Errorf("the reader's keeper did not unwrap what the writer's wrapped: %v", err)
	}
	// Another file's keeper, with the same passphrase, cannot: the salt
	// is new for each file.
	var other bytes.Buffer
	ow, err := Create(&other, passphrase, MinRounds)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ow.Keeper().Unwrap(wrapped, "a context"); err == nil {
		t.Error("another file's keeper unwrapped the key")
	}
}

func TestAWrongPassphrase(t *testing.T) {
	file := write(t, sample(t))
	for _, wrong := range []string{"", "an invented passphrase for a tesT", passphrase + " "} {
		if _, err := Open(bytes.NewReader(file), wrong); !errors.Is(err, ErrPassphrase) {
			t.Errorf("%q gave %v, want ErrPassphrase", wrong, err)
		}
	}
}

// check reads a changed copy of a file with the key already made, and
// wants it refused as damaged before the end.
func check(t *testing.T, opened *Reader, what string, changed []byte) {
	t.Helper()
	r, err := opened.Reopen(bytes.NewReader(changed))
	if err == nil {
		_, err = readAll(r)
	}
	if !errors.Is(err, ErrDamaged) {
		t.Errorf("%s: gave %v, want ErrDamaged", what, err)
	}
}

func TestAChangedOrCutFileIsRefused(t *testing.T) {
	file := write(t, sample(t))
	opened, err := Open(bytes.NewReader(file), passphrase)
	if err != nil {
		t.Fatal(err)
	}
	sealed := chunkSize + 16
	chunk := func(i int) []byte {
		start := headerSize + i*sealed
		end := min(start+sealed, len(file))
		return file[start:end]
	}
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	head := file[:headerSize]

	for _, at := range []int{headerSize, headerSize + 1, headerSize + sealed - 1, headerSize + sealed, len(file) / 2, len(file) - 17, len(file) - 1} {
		changed := append([]byte(nil), file...)
		changed[at] ^= 0x40
		check(t, opened, "a byte changed", changed)
	}
	check(t, opened, "cut at the edge of the first chunk", file[:headerSize+sealed])
	check(t, opened, "cut at the edge of the second chunk", file[:headerSize+2*sealed])
	check(t, opened, "cut inside a chunk", file[:headerSize+sealed+100])
	check(t, opened, "cut after the header", file[:headerSize])
	check(t, opened, "cut by one byte", file[:len(file)-1])
	check(t, opened, "a byte added", append(append([]byte(nil), file...), 0))
	check(t, opened, "a chunk added", append(append([]byte(nil), file...), chunk(1)...))
	check(t, opened, "the second chunk dropped", join(head, chunk(0), chunk(2), chunk(3)))
	check(t, opened, "two chunks swapped", join(head, chunk(1), chunk(0), chunk(2), chunk(3)))
	check(t, opened, "a chunk repeated", join(head, chunk(0), chunk(0), chunk(1), chunk(2), chunk(3)))
	check(t, opened, "the last chunk dropped", join(head, chunk(0), chunk(1), chunk(2)))

	// A changed header is refused: the check fails, or the chunks no
	// longer open.
	// (The high bytes of the rounds are left alone: a flip there names
	// millions of rounds, which is slow to refuse. TestNotABackup covers a
	// count out of bounds.)
	for _, at := range []int{4, 8, 9, 9 + saltSize, headerSize - 1} {
		changed := append([]byte(nil), file...)
		changed[at] ^= 0x01
		r, err := Open(bytes.NewReader(changed), passphrase)
		if err == nil {
			_, err = readAll(r)
		}
		if err == nil {
			t.Errorf("byte %d of the header changed: read", at)
		}
	}
}

func TestNotABackup(t *testing.T) {
	file := write(t, sample(t))
	newer := append([]byte(nil), file...)
	newer[3] = '2'
	cases := map[string]struct {
		raw  []byte
		want string
	}{
		"nothing":        {nil, "not a Keystone backup"},
		"another file":   {[]byte("an invented file that is long enough to be read as a header, and is not one"), "not a Keystone backup"},
		"a newer format": {newer, "newer Keystone"},
		"a cut header":   {file[:20], "damaged"},
	}
	for what, c := range cases {
		_, err := Open(bytes.NewReader(c.raw), passphrase)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: gave %v, want an error saying %q", what, err, c.want)
		}
	}
	tooMany := append([]byte(nil), file...)
	copy(tooMany[5:9], []byte{0xff, 0xff, 0xff, 0xff})
	if _, err := Open(bytes.NewReader(tooMany), passphrase); !errors.Is(err, ErrDamaged) {
		t.Errorf("a header naming too many rounds gave %v", err)
	}
}

func TestCreateRefuses(t *testing.T) {
	for what, c := range map[string]struct {
		passphrase string
		rounds     int
	}{
		"no passphrase":   {"", MinRounds},
		"too few rounds":  {passphrase, MinRounds - 1},
		"too many rounds": {passphrase, MaxRounds + 1},
	} {
		var buf bytes.Buffer
		if _, err := Create(&buf, c.passphrase, c.rounds); err == nil {
			t.Errorf("%s: a backup was started", what)
		}
		if buf.Len() != 0 {
			t.Errorf("%s: %d bytes were written", what, buf.Len())
		}
	}
}

func TestTheWriterRefusesBadEntries(t *testing.T) {
	bad := []string{"", ".", "/abs", "../up", "a/../b", "a/./b", "a//b", "a/", `a\b`, "c:x", "a\x01b", "a\x7fb", "\xff", strings.Repeat("a", maxNameLength+1)}
	for _, name := range bad {
		var buf bytes.Buffer
		w, err := Create(&buf, passphrase, MinRounds)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.File(name, 0, when, strings.NewReader("")); err == nil {
			t.Errorf("the file %q was taken", name)
		}
		if err := w.Dir(name, when); err == nil {
			t.Errorf("the folder %q was taken", name)
		}
	}

	var buf bytes.Buffer
	w, err := Create(&buf, passphrase, MinRounds)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.File("tenant/a", 1, when, strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	for _, again := range []string{"tenant/a", "Tenant/A"} {
		if err := w.File(again, 1, when, strings.NewReader("x")); err == nil {
			t.Errorf("%q was taken after tenant/a", again)
		}
	}
	if err := w.File("tenant/short", 10, when, strings.NewReader("abc")); err == nil {
		t.Error("a file shorter than its size was taken")
	}
	if err := w.Close(); err == nil {
		t.Error("a writer that failed closed as if whole")
	}
}

// craft seals a tar written by fill under the test passphrase, without the
// writer's checks, as someone who knew the passphrase could.
func craft(t *testing.T, fill func(tw *tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := Create(&buf, passphrase, MinRounds)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(w.gz)
	fill(tw)
	for _, step := range []func() error{tw.Close, w.gz.Close, w.chunks.Close} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func TestTheReaderRefusesBadEntries(t *testing.T) {
	file := func(name string) func(tw *tar.Writer) {
		return func(tw *tar.Writer) {
			tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Size: 1, Mode: 0o600})
			tw.Write([]byte("x"))
		}
	}
	cases := map[string]func(tw *tar.Writer){
		"a path that leaves": file("../outside"),
		"an absolute path":   file("/outside"),
		"a backslash":        file(`tenant\..\..\outside`),
		"a drive":            file("c:/outside"),
		"a link": func(tw *tar.Writer) {
			tw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: "tenant/link", Linkname: "/"})
		},
		"a hard link": func(tw *tar.Writer) {
			tw.WriteHeader(&tar.Header{Typeflag: tar.TypeLink, Name: "tenant/link", Linkname: "tenant/a"})
		},
		"a device":             func(tw *tar.Writer) { tw.WriteHeader(&tar.Header{Typeflag: tar.TypeChar, Name: "tenant/dev"}) },
		"a name twice":         func(tw *tar.Writer) { file("tenant/a")(tw); file("tenant/a")(tw) },
		"a name twice in case": func(tw *tar.Writer) { file("tenant/a")(tw); file("tenant/A")(tw) },
	}
	for what, fill := range cases {
		r, err := Open(bytes.NewReader(craft(t, fill)), passphrase)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := readAll(r); !errors.Is(err, ErrDamaged) {
			t.Errorf("%s: gave %v, want ErrDamaged", what, err)
		}
	}

	// Something after the compressed contents, inside the last chunk.
	var buf bytes.Buffer
	w, err := Create(&buf, passphrase, MinRounds)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(w.chunks)
	tw := tar.NewWriter(gz)
	file("tenant/a")(tw)
	tw.Close()
	gz.Close()
	w.chunks.Write([]byte("more"))
	w.chunks.Close()
	r, err := Open(bytes.NewReader(buf.Bytes()), passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readAll(r); !errors.Is(err, ErrDamaged) {
		t.Errorf("bytes after the contents gave %v, want ErrDamaged", err)
	}
}

func TestAnEmptyBackup(t *testing.T) {
	file := write(t, nil)
	r, err := Open(bytes.NewReader(file), passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := readAll(r); err != nil || len(got) != 0 {
		t.Errorf("an empty backup read %d entries: %v", len(got), err)
	}
}
