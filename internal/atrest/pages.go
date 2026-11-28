package atrest

import (
	"bytes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"modernc.org/sqlite/vfs"
)

// A page file is how the database and its journal are kept on disk
// (decision 0660): SQLite reads and writes it through the VFS as a plain
// file, and on disk it is a header and then one slot for each block of
// Block bytes.
//
//	header (64): magic "KSPAGE01" (8) | block size, uint32 (4) | salt (16)
//	             | check (32) | zero (4)
//	slot:        nonce (24) | length, uint32 (4) | AES-256-GCM of length
//	             bytes | tag (16) | room to the next slot, if any
//
// The file's key comes from the person's pages key and the salt. Each slot
// is sealed under a key of its own, derived from the file's key and the
// first half of its random nonce, so no key ever sees enough nonces for
// two to meet, however often a page is written. The additional data is
// the tenant id, the slot's number and its length, so a slot moved to
// another place, or to another person's file, does not open.
//
// A slot whose length is short of Block holds the start of its block, and
// the rest reads as zeros. Only the last slot gives the file its size;
// a write past the end never rewrites the slot before it, so a write cut
// short damages only the blocks it was writing: the promise the VFS makes
// SQLite, with a sector of Block bytes.
//
// A slot that does not open is an error in the database. In the journal
// it reads as zeros: a journal's blocks are written before the database
// is touched and are only read back after a crash, when SQLite treats a
// torn sector as garbage and its own checksums find the end of what was
// written whole.

const (
	pageMagic  = "KSPAGE01"
	pageHeader = 64
	// Block is the size of the blocks a page file is sealed in, and the
	// sector size the VFS tells SQLite. It matches SQLite's default page.
	Block     = 4096
	slotNonce = 24
	slotHead  = slotNonce + 4
	slotSize  = slotHead + Block + gcmTag
	pageCheck = "keystone page file check v1"
)

// pageFile is one database or journal, open. The VFS keeps one for each
// file and shares it between the connections of the process (vfs.go), so
// it keeps the file's layout in memory: every write of the process goes
// through it, and the hold keeps every other process out.
type pageFile struct {
	mu      sync.Mutex
	f       *os.File
	tenant  string
	key     []byte // this file's key
	salt    []byte // the salt it comes from
	lenient bool   // a journal: a slot that does not open reads as zeros
	known   bool   // n and last are the file's
	n       int64  // slots
	last    int    // the length of the last
}

// openPages makes f, a file just opened, a page file of k's. A file too
// short to hold a header is empty: a header is written to it if it can be
// written. Anything else must be a page file of k's.
func openPages(f *os.File, k *Keys, lenient, writable bool) (*pageFile, error) {
	p := &pageFile{f: f, tenant: k.tenant, lenient: lenient}
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	head := make([]byte, pageHeader)
	p.salt = head[12:28]
	if fi.Size() < pageHeader {
		if !writable {
			return nil, fmt.Errorf("%s: an empty page file cannot be written: %w", f.Name(), os.ErrPermission)
		}
		copy(head, pageMagic)
		binary.BigEndian.PutUint32(head[8:], Block)
		if _, err := rand.Read(head[12:28]); err != nil {
			return nil, err
		}
		if p.key, err = pageKey(k, head[12:28]); err != nil {
			return nil, err
		}
		copy(head[28:60], mac(p.key, pageCheck))
		if err := f.Truncate(0); err != nil {
			return nil, err
		}
		if _, err := f.WriteAt(head, 0); err != nil {
			return nil, err
		}
		p.known = true
		return p, nil
	}
	if _, err := f.ReadAt(head, 0); err != nil {
		return nil, err
	}
	if !bytes.Equal(head[:8], []byte(pageMagic)) {
		return nil, fmt.Errorf("%s is not a sealed page file: %w", f.Name(), ErrSealed)
	}
	if binary.BigEndian.Uint32(head[8:]) != Block {
		return nil, fmt.Errorf("%s has blocks of a size this Keystone does not write: %w", f.Name(), ErrSealed)
	}
	if p.key, err = pageKey(k, head[12:28]); err != nil {
		return nil, err
	}
	if !hmac.Equal(head[28:60], mac(p.key, pageCheck)) {
		return nil, fmt.Errorf("%s: %w", f.Name(), ErrSealed)
	}
	return p, nil
}

// IsPageFile reports whether b starts as a page file does: a database or
// journal sealed by this package. It says nothing about whose it is.
func IsPageFile(b []byte) bool {
	return len(b) >= 8 && bytes.Equal(b[:8], []byte(pageMagic))
}

func pageKey(k *Keys, salt []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, k.pages, salt, "keystone page file v1 for tenant "+k.tenant, keySize)
}

func slotAt(i int64) int64 { return pageHeader + i*slotSize }

// layout returns how many slots the file has and the length of the last.
func (p *pageFile) layout() (n int64, last int, err error) {
	if p.known {
		return p.n, p.last, nil
	}
	if n, last, err = p.layoutOnDisk(); err == nil {
		p.n, p.last, p.known = n, last, true
	}
	return n, last, err
}

// layoutOnDisk reads the layout from the file.
func (p *pageFile) layoutOnDisk() (n int64, last int, err error) {
	fi, err := p.f.Stat()
	if err != nil {
		return 0, 0, err
	}
	body := fi.Size() - pageHeader
	if body <= 0 {
		return 0, 0, nil
	}
	n = (body + slotSize - 1) / slotSize
	var b [4]byte
	got, err := p.f.ReadAt(b[:], slotAt(n-1)+slotNonce)
	if got < len(b) {
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, 0, err
		}
		// Torn before its length was written: the block is lost, and
		// reads as a block that does not open.
		return n, Block, nil
	}
	return n, int(min(binary.BigEndian.Uint32(b[:]), Block)), nil
}

func size(n int64, last int) int64 {
	if n == 0 {
		return 0
	}
	return (n-1)*Block + int64(last)
}

func (p *pageFile) aead(nonce []byte) (cipher.AEAD, error) {
	return newGCM(mac(p.key, string(nonce[:12])))
}

func (p *pageFile) ad(i int64, n int) []byte {
	ad := append([]byte("keystone page v1\x00"), p.tenant...)
	ad = binary.BigEndian.AppendUint64(ad, uint64(i))
	return binary.BigEndian.AppendUint32(ad, uint32(n))
}

// readSlot returns the bytes slot i holds, at most Block of them. A slot
// that does not open is an error, or, in a journal, as many zeros as it
// claims to hold.
func (p *pageFile) readSlot(i int64) ([]byte, error) {
	buf := make([]byte, slotSize)
	got, err := p.f.ReadAt(buf, slotAt(i))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	buf = buf[:got]
	n := Block
	if got >= slotHead {
		n = int(min(binary.BigEndian.Uint32(buf[slotNonce:]), Block))
	}
	if got >= slotHead+n+gcmTag {
		aead, err := p.aead(buf[:slotNonce])
		if err != nil {
			return nil, err
		}
		plain, err := aead.Open(nil, buf[12:slotNonce], buf[slotHead:slotHead+n+gcmTag], p.ad(i, n))
		if err == nil {
			return plain, nil
		}
	}
	if p.lenient {
		return make([]byte, n), nil
	}
	return nil, fmt.Errorf("%s, block %d: %w", p.f.Name(), i, ErrSealed)
}

// writeSlot seals plain, at most Block bytes, into slot i.
func (p *pageFile) writeSlot(i int64, plain []byte) error {
	buf := make([]byte, slotHead, slotHead+len(plain)+gcmTag)
	if _, err := rand.Read(buf[:slotNonce]); err != nil {
		return err
	}
	binary.BigEndian.PutUint32(buf[slotNonce:], uint32(len(plain)))
	aead, err := p.aead(buf[:slotNonce])
	if err != nil {
		return err
	}
	buf = aead.Seal(buf, buf[12:slotNonce], plain, p.ad(i, len(plain)))
	if _, err = p.f.WriteAt(buf, slotAt(i)); err != nil {
		p.known = false
		return err
	}
	if p.known {
		switch {
		case i >= p.n:
			p.n, p.last = i+1, len(plain)
		case i == p.n-1:
			p.last = len(plain)
		}
	}
	return nil
}

// ReadAt reads as from a plain file.
func (p *pageFile) ReadAt(b []byte, off int64) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, last, err := p.layout()
	if err != nil {
		return 0, err
	}
	end := min(off+int64(len(b)), size(n, last))
	pos := off
	for pos < end {
		i, in := pos/Block, pos%Block
		plain, err := p.readSlot(i)
		if err != nil {
			return int(pos - off), err
		}
		count := min(Block-in, end-pos)
		dst := b[pos-off : pos-off+count]
		clear(dst)
		if in < int64(len(plain)) {
			copy(dst, plain[in:])
		}
		pos += count
	}
	if read := int(max(pos-off, 0)); read < len(b) {
		return read, io.EOF
	}
	return len(b), nil
}

// WriteAt writes as to a plain file. Only the slots the bytes fall in are
// written, and any whole block between the end and off as zeros.
func (p *pageFile) WriteAt(b []byte, off int64) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writeAt(b, off)
}

func (p *pageFile) writeAt(b []byte, off int64) (int, error) {
	n, _, err := p.layout()
	if err != nil {
		return 0, err
	}
	end := off + int64(len(b))
	first, final := off/Block, (end-1)/Block
	for i := n; i < first; i++ {
		if err := p.writeSlot(i, make([]byte, Block)); err != nil {
			return 0, err
		}
	}
	for i := first; i <= final; i++ {
		start := i * Block
		lo, hi := max(off, start)-start, min(end, start+Block)-start
		var cur []byte
		switch {
		case lo == 0 && hi == Block:
			cur = b[start-off : start-off+Block]
		case i < n:
			if cur, err = p.readSlot(i); err != nil {
				return int(max(start-off, 0)), err
			}
			if int64(len(cur)) < hi {
				cur = append(cur, make([]byte, hi-int64(len(cur)))...)
			}
			copy(cur[lo:hi], b[start+lo-off:start+hi-off])
		default:
			cur = make([]byte, hi)
			copy(cur[lo:hi], b[start+lo-off:start+hi-off])
		}
		if err := p.writeSlot(i, cur); err != nil {
			return int(max(start-off, 0)), err
		}
	}
	return len(b), nil
}

// Truncate sets the size as of a plain file. The layout is read from the
// file again afterwards.
func (p *pageFile) Truncate(to int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer func() { p.known = false }()
	n, last, err := p.layout()
	if err != nil {
		return err
	}
	have := size(n, last)
	switch {
	case to == have:
		return nil
	case to > have:
		_, err := p.writeAt([]byte{0}, to-1)
		return err
	case to <= 0:
		return p.f.Truncate(pageHeader)
	}
	j, rem := to/Block, to%Block
	if rem == 0 {
		// Slot j-1 becomes the last, and must hold its whole block.
		if err := p.f.Truncate(slotAt(j)); err != nil {
			return err
		}
		cur, err := p.readSlot(j - 1)
		if err != nil {
			return err
		}
		if len(cur) < Block {
			if err := p.writeSlot(j-1, append(cur, make([]byte, Block-len(cur))...)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := p.f.Truncate(slotAt(j + 1)); err != nil {
		return err
	}
	cur, err := p.readSlot(j)
	if err != nil {
		return err
	}
	if int64(len(cur)) < rem {
		cur = append(cur, make([]byte, rem-int64(len(cur)))...)
	}
	if err := p.writeSlot(j, cur[:rem]); err != nil {
		return err
	}
	return p.f.Truncate(slotAt(j) + slotHead + rem + gcmTag)
}

// Sync makes what was written durable.
func (p *pageFile) Sync(vfs.SyncFlag) error { return p.f.Sync() }

// Size is the size as of a plain file.
func (p *pageFile) Size() (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, last, err := p.layout()
	return size(n, last), err
}

// Close closes the file.
func (p *pageFile) Close() error { return p.f.Close() }

// sameKeys refuses keys that are not the ones the file was opened with: a
// folder given other keys while the process has its database open.
func (p *pageFile) sameKeys(k *Keys) error {
	key, err := pageKey(k, p.salt)
	if err != nil {
		return err
	}
	if k.tenant != p.tenant || !hmac.Equal(key, p.key) {
		return fmt.Errorf("%s: %w", p.f.Name(), ErrSealed)
	}
	return nil
}

// handle is what the VFS gives SQLite for one open of a file: the file the
// process shares, and what to do when SQLite closes it.
type handle struct {
	*pageFile
	once  sync.Once
	close func()
}

func (h *handle) Close() error {
	h.once.Do(h.close)
	return nil
}
