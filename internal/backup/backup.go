// Package backup is the file a tenant's backup is written to: one file,
// sealed under a key made from a passphrase the person gives (decision
// 0260).
//
// It knows nothing about tenants. internal/tenant decides what goes in and
// where it comes back out. This package seals the entries, checks them on
// the way back, and refuses a name that could leave the folder it is
// restored into.
//
// The file is a header and then chunks:
//
//	magic    4 bytes, "KSB1": the digit is the format's version
//	kdf      1 byte: 1 is PBKDF2-SHA-256
//	rounds   4 bytes, big-endian: how many rounds the passphrase went through
//	salt     32 random bytes, new for each file
//	check    32 bytes made from the key, to tell a wrong passphrase from a damaged file
//	chunks   the contents, 64 KiB at a time, each sealed with AES-256-GCM
//
// A chunk's nonce is its number and a flag that marks the last chunk, and
// every chunk is bound to the header. A chunk that is changed, moved,
// dropped or repeated, a file that is cut short or has anything after its
// last chunk, and a header that is changed, are all refused.
//
// The contents are a gzip-compressed tar of the entries: plain files and
// folders, with slash-separated relative names.
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/secrets"
)

const (
	// MinRounds and MaxRounds bound the rounds of PBKDF2-SHA-256 a file
	// may name. Fewer would make a stolen backup quick to guess; more would
	// let a file keep a restore busy for minutes.
	MinRounds = 100_000
	MaxRounds = 50_000_000

	kdfPBKDF2SHA256 = 1
	keySize         = 32
	saltSize        = 32
	checkSize       = 32
	headerSize      = 4 + 1 + 4 + saltSize + checkSize
	chunkSize       = 64 << 10
	maxNameLength   = 1024
)

var magic = []byte("KSB1")

// ErrPassphrase is returned when the passphrase does not open a backup.
var ErrPassphrase = errors.New("the passphrase does not open this backup, or the start of the file was changed")

// ErrDamaged is returned when a backup was changed or cut short after it
// was written. Every error about the contents wraps it.
var ErrDamaged = errors.New("the backup is damaged: it was changed or cut short after it was written")

// keys are what a passphrase becomes for one file.
type keys struct {
	stream  []byte // seals the chunks
	secrets []byte // wraps a tenant's data key inside the file
	check   []byte // stored in the header
}

func derive(passphrase string, salt []byte, rounds int) (keys, error) {
	master, err := pbkdf2.Key(sha256.New, passphrase, salt, rounds, keySize)
	if err != nil {
		return keys{}, err
	}
	var k keys
	for _, part := range []struct {
		into *[]byte
		info string
	}{
		{&k.stream, "keystone backup v1 contents"},
		{&k.secrets, "keystone backup v1 data keys"},
		{&k.check, "keystone backup v1 passphrase check"},
	} {
		if *part.into, err = hkdf.Expand(sha256.New, master, part.info, keySize); err != nil {
			return keys{}, err
		}
	}
	return k, nil
}

// Entry is one file or folder in a backup.
type Entry struct {
	Name    string // slash-separated and relative, such as files/ab12
	Dir     bool
	Size    int64 // 0 for a folder
	ModTime time.Time
}

// Writer writes a backup. Nothing it writes can be read back until Close
// has written the last chunk.
type Writer struct {
	chunks *chunkWriter
	gz     *gzip.Writer
	tw     *tar.Writer
	keeper secrets.Keeper
	names  map[string]bool
	err    error
}

// Create writes the header to w and returns a Writer for the entries. The
// key is made from passphrase with the given rounds of PBKDF2-SHA-256, and
// the file records the count.
func Create(w io.Writer, passphrase string, rounds int) (*Writer, error) {
	if passphrase == "" {
		return nil, errors.New("a backup needs a passphrase")
	}
	if rounds < MinRounds || rounds > MaxRounds {
		return nil, fmt.Errorf("a backup's passphrase goes through %d to %d rounds, not %d", MinRounds, MaxRounds, rounds)
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	k, err := derive(passphrase, salt, rounds)
	if err != nil {
		return nil, err
	}
	header := make([]byte, 0, headerSize)
	header = append(header, magic...)
	header = append(header, kdfPBKDF2SHA256)
	header = binary.BigEndian.AppendUint32(header, uint32(rounds))
	header = append(header, salt...)
	header = append(header, k.check...)
	if _, err := w.Write(header); err != nil {
		return nil, err
	}
	keeper, err := secrets.KeyKeeper("backup", k.secrets)
	if err != nil {
		return nil, err
	}
	cw, err := newChunkWriter(w, k.stream, header)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewWriterLevel(cw, gzip.DefaultCompression)
	if err != nil {
		return nil, err
	}
	return &Writer{chunks: cw, gz: gz, tw: tar.NewWriter(gz), keeper: keeper, names: map[string]bool{}}, nil
}

// Keeper wraps a data key under this file's own key, for a secrets file
// that goes into it. The Reader of the same file, opened with the same
// passphrase, has the keeper that unwraps it.
func (w *Writer) Keeper() secrets.Keeper { return w.keeper }

// Dir adds a folder.
func (w *Writer) Dir(name string, mod time.Time) error {
	if err := w.add(name); err != nil {
		return err
	}
	return w.fail(w.tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: 0o700, ModTime: mod}))
}

// File adds a file of size bytes, read from r. r must give at least size
// bytes; only size are read.
func (w *Writer) File(name string, size int64, mod time.Time, r io.Reader) error {
	if err := w.add(name); err != nil {
		return err
	}
	if size < 0 {
		return w.fail(fmt.Errorf("%s: a size cannot be negative", name))
	}
	if err := w.tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Size: size, Mode: 0o600, ModTime: mod}); err != nil {
		return w.fail(err)
	}
	if _, err := io.CopyN(w.tw, r, size); err != nil {
		if errors.Is(err, io.EOF) {
			err = errors.New("it became shorter while it was read")
		}
		return w.fail(fmt.Errorf("%s: %w", name, err))
	}
	return nil
}

// add checks a name before an entry is written. A name the reader would
// refuse is refused here, so a backup that was written can be restored.
func (w *Writer) add(name string) error {
	if w.err != nil {
		return w.err
	}
	if err := CheckName(name); err != nil {
		return err
	}
	if w.names[strings.ToLower(name)] {
		return fmt.Errorf("%q is in the backup already", name)
	}
	w.names[strings.ToLower(name)] = true
	return nil
}

func (w *Writer) fail(err error) error {
	if err != nil && w.err == nil {
		w.err = err
	}
	return err
}

// Close finishes the entries, the compression and the last chunk. It does
// not close the writer given to Create.
func (w *Writer) Close() error {
	if w.err != nil {
		return w.err
	}
	for _, step := range []func() error{w.tw.Close, w.gz.Close, w.chunks.Close} {
		if err := step(); err != nil {
			return w.fail(err)
		}
	}
	w.err = errors.New("the backup is closed")
	return nil
}

// Reader reads a backup's entries and checks every byte on the way. An
// entry's bytes are read with Read until io.EOF, then Next moves on.
type Reader struct {
	header []byte
	keys   keys
	chunks *chunkReader
	gz     *gzip.Reader
	tr     *tar.Reader
	keeper secrets.Keeper
	names  map[string]bool
	done   bool
}

// Open reads the header from r and makes the key from passphrase. A wrong
// passphrase is ErrPassphrase. The contents are checked as they are read:
// only when Next has returned io.EOF has every chunk been checked.
func Open(r io.Reader, passphrase string) (*Reader, error) {
	header, err := readHeader(r)
	if err != nil {
		return nil, err
	}
	rounds := int(binary.BigEndian.Uint32(header[5:9]))
	salt, check := header[9:9+saltSize], header[9+saltSize:]
	k, err := derive(passphrase, salt, rounds)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(k.check, check) != 1 {
		return nil, ErrPassphrase
	}
	keeper, err := secrets.KeyKeeper("backup", k.secrets)
	if err != nil {
		return nil, err
	}
	rd := &Reader{header: header, keys: k, keeper: keeper}
	return rd, rd.start(r)
}

// Reopen reads the same backup again from the start, given in src, with
// the key already made, so the passphrase does not go through its rounds
// twice. A file whose header is not the one read before is refused.
func (r *Reader) Reopen(src io.Reader) (*Reader, error) {
	header, err := readHeader(src)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(header, r.header) {
		return nil, fmt.Errorf("%w: its header is not the one read before", ErrDamaged)
	}
	rd := &Reader{header: header, keys: r.keys, keeper: r.keeper}
	return rd, rd.start(src)
}

func readHeader(r io.Reader) ([]byte, error) {
	header := make([]byte, headerSize)
	if n, err := io.ReadFull(r, header); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			if n >= len(magic) && bytes.Equal(header[:len(magic)], magic) {
				return nil, fmt.Errorf("%w: it ends inside its header", ErrDamaged)
			}
			return nil, errors.New("this is not a Keystone backup")
		}
		return nil, err
	}
	switch {
	case bytes.Equal(header[:len(magic)], magic):
	case bytes.Equal(header[:3], magic[:3]) && header[3] > magic[3]:
		return nil, errors.New("this backup was made by a newer Keystone: use a newer binary to restore it")
	default:
		return nil, errors.New("this is not a Keystone backup")
	}
	if header[4] != kdfPBKDF2SHA256 {
		return nil, errors.New("this backup names a way of turning its passphrase into a key that this binary does not know: use a newer binary")
	}
	if rounds := binary.BigEndian.Uint32(header[5:9]); rounds < MinRounds || rounds > MaxRounds {
		return nil, fmt.Errorf("%w: it says its passphrase went through %d rounds, outside %d to %d", ErrDamaged, rounds, MinRounds, MaxRounds)
	}
	return header, nil
}

func (r *Reader) start(src io.Reader) error {
	cr, err := newChunkReader(src, r.keys.stream, r.header)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(cr)
	if err != nil {
		return damaged(err)
	}
	gz.Multistream(false)
	r.chunks, r.gz, r.tr, r.names = cr, gz, tar.NewReader(gz), map[string]bool{}
	return nil
}

// Keeper unwraps a data key that was wrapped under this file's key: the
// Writer's Keeper, for the same passphrase.
func (r *Reader) Keeper() secrets.Keeper { return r.keeper }

// Next moves to the next entry. At the end it reads the rest of the file
// and returns io.EOF only when every chunk has been checked, the last one
// is there, and nothing follows it.
func (r *Reader) Next() (Entry, error) {
	if r.done {
		return Entry{}, io.EOF
	}
	h, err := r.tr.Next()
	if errors.Is(err, io.EOF) {
		if _, err := io.Copy(io.Discard, r.gz); err != nil {
			return Entry{}, damaged(err)
		}
		var one [1]byte
		if n, err := r.chunks.Read(one[:]); n != 0 || !errors.Is(err, io.EOF) {
			if err != nil && !errors.Is(err, io.EOF) {
				return Entry{}, damaged(err)
			}
			return Entry{}, fmt.Errorf("%w: something follows the end of its contents", ErrDamaged)
		}
		r.done = true
		return Entry{}, io.EOF
	}
	if err != nil {
		return Entry{}, damaged(err)
	}
	e := Entry{Name: h.Name, Size: h.Size, ModTime: h.ModTime}
	switch h.Typeflag {
	case tar.TypeReg:
	case tar.TypeDir:
		e.Dir, e.Name = true, strings.TrimSuffix(h.Name, "/")
		if h.Size != 0 {
			return Entry{}, fmt.Errorf("%w: the folder %q has a size", ErrDamaged, e.Name)
		}
	default:
		return Entry{}, fmt.Errorf("%w: %q is neither a file nor a folder", ErrDamaged, h.Name)
	}
	if err := CheckName(e.Name); err != nil {
		return Entry{}, fmt.Errorf("%w: %v", ErrDamaged, err)
	}
	if e.Size < 0 {
		return Entry{}, fmt.Errorf("%w: %q has a negative size", ErrDamaged, e.Name)
	}
	// Two names that differ only in case are one file on Windows and macOS.
	key := strings.ToLower(e.Name)
	if r.names[key] {
		return Entry{}, fmt.Errorf("%w: %q is in it twice", ErrDamaged, e.Name)
	}
	r.names[key] = true
	return e, nil
}

// Read reads the current entry's bytes. It returns io.EOF at the end of
// the entry.
func (r *Reader) Read(p []byte) (int, error) {
	n, err := r.tr.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = damaged(err)
	}
	return n, err
}

// damaged wraps an error from the layers under the entries in ErrDamaged,
// unless it already is one.
func damaged(err error) error {
	if errors.Is(err, ErrDamaged) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrDamaged, err)
}

// CheckName refuses a name that is not a plain relative path inside the
// folder a backup is restored into: an absolute path, a ".." or "." part,
// an empty part, a backslash, a colon, a control character, a name that is
// not UTF-8, and a name this system reserves, such as NUL on Windows.
func CheckName(name string) error {
	ok := name != "" && name != "." && len(name) <= maxNameLength &&
		utf8.ValidString(name) && fs.ValidPath(name) &&
		!strings.ContainsAny(name, `\:`) && filepath.IsLocal(filepath.FromSlash(name))
	for _, c := range name {
		if c < 0x20 || c == 0x7f {
			ok = false
		}
	}
	if !ok {
		return fmt.Errorf("%q is not a name a backup may hold: use a relative path of plain parts joined by /", name)
	}
	return nil
}

// The chunks. Each holds up to chunkSize bytes of the contents, sealed
// with AES-256-GCM under the file's own key and bound to the header. The
// nonce is the chunk's number in the first eleven bytes and, in the last
// byte, 1 for the last chunk and 0 for any other.

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonce(counter uint64, last bool) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[3:11], counter)
	if last {
		n[11] = 1
	}
	return n
}

type chunkWriter struct {
	dst     io.Writer
	aead    cipher.AEAD
	header  []byte
	counter uint64
	buf     []byte
	out     []byte
	err     error
}

func newChunkWriter(dst io.Writer, key, header []byte) (*chunkWriter, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return &chunkWriter{dst: dst, aead: aead, header: header,
		buf: make([]byte, 0, chunkSize), out: make([]byte, 0, chunkSize+aead.Overhead())}, nil
}

// Write holds a full chunk back until more comes, so the last chunk is
// only sealed by Close.
func (c *chunkWriter) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	total := 0
	for len(p) > 0 {
		if len(c.buf) == chunkSize {
			if err := c.flush(false); err != nil {
				return total, err
			}
		}
		n := copy(c.buf[len(c.buf):chunkSize], p)
		c.buf, p, total = c.buf[:len(c.buf)+n], p[n:], total+n
	}
	return total, nil
}

func (c *chunkWriter) flush(last bool) error {
	sealed := c.aead.Seal(c.out[:0], nonce(c.counter, last), c.buf, c.header)
	if _, err := c.dst.Write(sealed); err != nil {
		c.err = err
		return err
	}
	c.counter++
	c.buf = c.buf[:0]
	return nil
}

func (c *chunkWriter) Close() error {
	if c.err != nil {
		return c.err
	}
	if err := c.flush(true); err != nil {
		return err
	}
	c.err = errors.New("the backup is closed")
	return nil
}

type chunkReader struct {
	src     io.Reader
	aead    cipher.AEAD
	header  []byte
	counter uint64
	in      []byte // sealed bytes read and not yet opened: at most one chunk and one byte more
	out     []byte
	buf     []byte // opened bytes not yet read
	last    bool   // the last chunk has been opened
	srcEOF  bool
	err     error
}

func newChunkReader(src io.Reader, key, header []byte) (*chunkReader, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return &chunkReader{src: src, aead: aead, header: header,
		in: make([]byte, 0, chunkSize+aead.Overhead()+1), out: make([]byte, 0, chunkSize)}, nil
}

// next opens the next chunk. It reads one byte past a full chunk to learn
// whether that chunk is the last, so a file cut at a chunk's edge is
// still refused: its last chunk was sealed as one that is not.
func (c *chunkReader) next() error {
	want := chunkSize + c.aead.Overhead() + 1
	for len(c.in) < want && !c.srcEOF {
		n, err := c.src.Read(c.in[len(c.in):want])
		c.in = c.in[:len(c.in)+n]
		if errors.Is(err, io.EOF) {
			c.srcEOF = true
		} else if err != nil {
			return err
		}
	}
	last := len(c.in) < want
	size := len(c.in)
	if !last {
		size = want - 1
	}
	if size < c.aead.Overhead() {
		return fmt.Errorf("%w: it ends before its last chunk", ErrDamaged)
	}
	plain, err := c.aead.Open(c.out[:0], nonce(c.counter, last), c.in[:size], c.header)
	if err != nil {
		if last {
			return fmt.Errorf("%w: a chunk does not open, or the file ends before its last chunk", ErrDamaged)
		}
		return fmt.Errorf("%w: a chunk does not open", ErrDamaged)
	}
	rest := copy(c.in, c.in[size:])
	c.in = c.in[:rest]
	c.counter++
	c.buf, c.last = plain, last
	return nil
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	for len(c.buf) == 0 {
		if c.last {
			return 0, io.EOF
		}
		if err := c.next(); err != nil {
			c.err = err
			return 0, err
		}
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

// ReadByte lets gzip read without a buffer of its own, so nothing past the
// end of the compressed contents is taken from the file unseen.
func (c *chunkReader) ReadByte() (byte, error) {
	var b [1]byte
	for {
		n, err := c.Read(b[:])
		if n == 1 {
			return b[0], nil
		}
		if err != nil {
			return 0, err
		}
	}
}
