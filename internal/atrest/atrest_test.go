package atrest

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Every id, word and value here is invented.

const marker = "ZEBRAQUILLMARKER"

func testKeys(t *testing.T, tenant string) *Keys {
	t.Helper()
	root := make([]byte, 32)
	rand.Read(root)
	k, err := Derive(root, tenant)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestDerive(t *testing.T) {
	root := bytes.Repeat([]byte{7}, 32)
	a, err := Derive(root, "aaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Derive(root, "bbbbbbbbbbbbbbbb")
	again, _ := Derive(root, "aaaaaaaaaaaaaaaa")
	if a.Name("x") == b.Name("x") || a.Check() == b.Check() {
		t.Error("two tenants share a name or a check")
	}
	if a.Name("x") != again.Name("x") || a.Check() != again.Check() {
		t.Error("the same root and tenant gave other keys")
	}
	if a.Name("x") == a.Name("y") || len(a.Name("x")) != 32 {
		t.Errorf("names: %q, %q", a.Name("x"), a.Name("y"))
	}
	if s := fmt.Sprintf("%v %#v %s", a, a, a); strings.Contains(s, string(a.pages)) || !strings.Contains(s, "aaaaaaaaaaaaaaaa") {
		t.Errorf("printing the keys gave %q", s)
	}
	if _, err := Derive(root[:5], "aaaaaaaaaaaaaaaa"); err == nil {
		t.Error("a short root was taken")
	}
	if _, err := Derive(root, "../x"); err == nil {
		t.Error("a path was taken as a tenant id")
	}
}

func TestSealedFiles(t *testing.T) {
	k := testKeys(t, "aaaaaaaaaaaaaaaa")
	other := testKeys(t, "bbbbbbbbbbbbbbbb")
	for _, n := range []int{0, 1, 100, FileChunk - 1, FileChunk, FileChunk + 1, 3*FileChunk + 17} {
		plain := bytes.Repeat([]byte(marker), n/len(marker)+1)[:n]
		sealed, err := k.SealFile(plain)
		if err != nil {
			t.Fatal(err)
		}
		if n >= len(marker) && bytes.Contains(sealed, []byte(marker)) {
			t.Fatalf("%d bytes: the marker is in the sealed file", n)
		}
		got, err := k.OpenFile(sealed)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("%d bytes: %v", n, err)
		}
		if _, err := other.OpenFile(sealed); !errors.Is(err, ErrSealed) {
			t.Fatalf("%d bytes: another person's keys gave %v", n, err)
		}
		changed := bytes.Clone(sealed)
		changed[len(changed)-1] ^= 1
		if _, err := k.OpenFile(changed); !errors.Is(err, ErrSealed) {
			t.Fatalf("%d bytes: a changed bit gave %v", n, err)
		}
		if n > FileChunk {
			cut := sealed[:fileHeader+FileChunk+gcmTag]
			if _, err := k.OpenFile(cut); !errors.Is(err, ErrSealed) {
				t.Fatalf("%d bytes: a file cut at a chunk's edge gave %v", n, err)
			}
		}
	}
	if _, err := k.OpenFile([]byte("plain words")); !errors.Is(err, ErrSealed) {
		t.Errorf("a plain file gave %v", err)
	}
}

// openPage opens a page file in dir as the VFS would.
func openPage(t *testing.T, path string, k *Keys, lenient bool) *pageFile {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	p, err := openPages(f, k, lenient, true)
	if err != nil {
		f.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

// TestPagesAsAPlainFile writes, reads and truncates a page file at random
// and holds it to a plain buffer.
func TestPagesAsAPlainFile(t *testing.T) {
	k := testKeys(t, "aaaaaaaaaaaaaaaa")
	p := openPage(t, filepath.Join(t.TempDir(), "keystone.db-journal"), k, false)
	var want []byte
	r := mrand.New(mrand.NewPCG(1, 2))
	for step := 0; step < 600; step++ {
		switch r.IntN(10) {
		case 0:
			to := int64(r.IntN(6 * Block))
			if err := p.Truncate(to); err != nil {
				t.Fatalf("step %d: truncate to %d: %v", step, to, err)
			}
			if int(to) <= len(want) {
				want = want[:to]
			} else {
				want = append(want, make([]byte, int(to)-len(want))...)
			}
		default:
			off := int64(r.IntN(7 * Block))
			b := make([]byte, 1+r.IntN(2*Block))
			for i := range b {
				b[i] = byte(r.IntN(256))
			}
			if _, err := p.WriteAt(b, off); err != nil {
				t.Fatalf("step %d: write %d at %d: %v", step, len(b), off, err)
			}
			if end := int(off) + len(b); end > len(want) {
				want = append(want, make([]byte, end-len(want))...)
			}
			copy(want[off:], b)
		}
		n, err := p.Size()
		if err != nil || n != int64(len(want)) {
			t.Fatalf("step %d: size %d (%v), want %d", step, n, err, len(want))
		}
		got := make([]byte, len(want)+10)
		m, err := p.ReadAt(got, 0)
		if m != len(want) || !errors.Is(err, io.EOF) || !bytes.Equal(got[:m], want) {
			t.Fatalf("step %d: read %d of %d (%v), same %v", step, m, len(want), err, bytes.Equal(got[:m], want))
		}
	}
}

// TestTornBlocks: a slot that does not open is an error in a database and
// zeros in a journal.
func TestTornBlocks(t *testing.T) {
	k := testKeys(t, "aaaaaaaaaaaaaaaa")
	dir := t.TempDir()
	for _, lenient := range []bool{false, true} {
		path := filepath.Join(dir, fmt.Sprint(lenient))
		p := openPage(t, path, k, lenient)
		block := bytes.Repeat([]byte{9}, 3*Block)
		if _, err := p.WriteAt(block, 0); err != nil {
			t.Fatal(err)
		}
		// Tear the middle block, as a write cut short would.
		raw, _ := os.ReadFile(path)
		copy(raw[slotAt(1)+slotHead+100:], bytes.Repeat([]byte{0}, 300))
		os.WriteFile(path, raw, 0o600)
		got := make([]byte, 3*Block)
		_, err := p.ReadAt(got, 0)
		switch {
		case !lenient && !errors.Is(err, ErrSealed):
			t.Errorf("a torn block in a database gave %v", err)
		case lenient && err != nil:
			t.Errorf("a torn block in a journal gave %v", err)
		case lenient && (!bytes.Equal(got[:Block], block[:Block]) || !bytes.Equal(got[Block:2*Block], make([]byte, Block)) || !bytes.Equal(got[2*Block:], block[:Block])):
			t.Error("a torn block in a journal did not read as zeros between whole ones")
		}
	}
}

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn, err := DSN(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn+"&_txlock=immediate&_pragma=journal_mode(truncate)&_pragma=synchronous(full)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// TestADatabaseThroughTheVFS: SQLite works through the VFS, from several
// connections at once, and nothing it writes holds the words or a plain
// header; another person's keys do not open it.
func TestADatabaseThroughTheVFS(t *testing.T) {
	dir := t.TempDir()
	k := testKeys(t, "aaaaaaaaaaaaaaaa")
	if err := Use(dir, k); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, DatabaseFile)
	first := openDB(t, path)
	if _, err := first.Exec(`CREATE TABLE notes(body TEXT); CREATE VIRTUAL TABLE search USING fts5(body)`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for c := 0; c < 3; c++ {
		db := first
		if c > 0 {
			db = openDB(t, path)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				body := fmt.Sprintf("%s %d %d %s", marker, c, i, strings.Repeat("filler ", 200))
				if _, err := db.Exec("INSERT INTO notes VALUES(?); INSERT INTO search VALUES(?)", body, body); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	var n int
	if err := first.QueryRow("SELECT count(*) FROM search WHERE search MATCH ?", marker).Scan(&n); err != nil || n != 150 {
		t.Fatalf("found %d (%v)", n, err)
	}
	var check string
	if err := first.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity: %s %v", check, err)
	}
	// A transaction left open keeps its journal on disk to be read.
	tx, err := first.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE notes SET body = body || ?", marker); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	sawJournal := false
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if e.Name() == journalFile && len(raw) > pageHeader {
			sawJournal = true
		}
		if bytes.Contains(raw, []byte(marker)) || bytes.Contains(raw, []byte("SQLite format 3")) || bytes.Contains(raw, []byte("filler")) {
			t.Errorf("%s holds plain words", e.Name())
		}
		if !IsPageFile(raw) {
			t.Errorf("%s is not a page file", e.Name())
		}
	}
	if !sawJournal {
		t.Error("no journal was on disk during a transaction")
	}
	tx.Rollback()

	// The same folder under another person's keys does not open.
	Use(dir, testKeys(t, "aaaaaaaaaaaaaaaa"))
	defer Use(dir, k)
	stranger := openDB(t, path)
	if err := stranger.QueryRow("SELECT count(*) FROM notes").Scan(&n); err == nil {
		t.Error("the database opened under other keys")
	}
}

func TestTheVFSRefusesOtherFiles(t *testing.T) {
	dir := t.TempDir()
	if err := Use(dir, testKeys(t, "aaaaaaaaaaaaaaaa")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "other.db"), filepath.Join(t.TempDir(), DatabaseFile)} {
		db := openDB(t, path)
		if _, err := db.Exec("CREATE TABLE t(x)"); err == nil {
			t.Errorf("%s was opened", path)
		}
	}
}

func TestALockKeepsASecondProcessOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFile)
	os.WriteFile(path, nil, 0o600)
	// Another process, as far as the lock can tell: a handle of its own.
	other, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := tryLock(other); err != nil {
		t.Fatal(err)
	}
	SetLockWait(100 * time.Millisecond)
	defer SetLockWait(30 * time.Second)
	k := testKeys(t, "aaaaaaaaaaaaaaaa")
	h := holderOf(path)
	if _, err := h.take(path, k, os.O_RDWR); !errors.Is(err, ErrBusy) {
		t.Fatalf("a held database gave %v", err)
	}
	unlock(other)
	if _, err := h.take(path, k, os.O_RDWR); err != nil {
		t.Fatal(err)
	}
	if _, err := h.take(path, k, os.O_RDWR); err != nil { // the same process again: no wait
		t.Fatal(err)
	}
	if _, err := h.take(path, testKeys(t, "aaaaaaaaaaaaaaaa"), os.O_RDWR); !errors.Is(err, ErrSealed) {
		t.Fatalf("other keys on an open database gave %v", err)
	}
	if err := tryLock(other); !errors.Is(err, errLocked) {
		t.Fatalf("the process's hold let another in: %v", err)
	}
	h.give()
	if err := tryLock(other); !errors.Is(err, errLocked) {
		t.Fatal("the hold went with one of two closes")
	}
	h.give()
	if err := tryLock(other); err != nil {
		t.Fatalf("the hold stayed after the last close: %v", err)
	}
	unlock(other)
}
