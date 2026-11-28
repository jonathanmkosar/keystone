package atrest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// A sealed file (decision 0661) is a header and then chunks:
//
//	magic "KSFILE01" (8) | chunk size, uint32 (4) | salt (16)
//	chunk: AES-256-GCM of up to chunk size bytes, and its 16-byte tag
//
// The file's key is derived from the person's files key and the salt, so
// every file has a key of its own and a chunk's nonce can be its number,
// with a flag on the last chunk so that a file cut short at a chunk's edge
// is refused. The header and the tenant id are the additional data of
// every chunk. An empty file is one empty last chunk.

const (
	fileMagic  = "KSFILE01"
	fileHeader = 8 + 4 + 16
	// FileChunk is the size of a sealed file's chunks. It is written in
	// each file's header, so a reader follows the file and not this value.
	FileChunk     = 64 << 10
	maxFileChunk  = 16 << 20
	gcmTag        = 16
	gcmNonceBytes = 12
)

// SealFile returns plain sealed as a file of this person's.
func (k *Keys) SealFile(plain []byte) ([]byte, error) {
	head := make([]byte, fileHeader)
	copy(head, fileMagic)
	binary.BigEndian.PutUint32(head[8:], FileChunk)
	if _, err := rand.Read(head[12:]); err != nil {
		return nil, err
	}
	aead, err := k.fileAEAD(head[12:])
	if err != nil {
		return nil, err
	}
	ad := append(append([]byte{}, head...), k.tenant...)
	n := (len(plain) + FileChunk - 1) / FileChunk
	if n == 0 {
		n = 1
	}
	out := make([]byte, 0, fileHeader+len(plain)+n*gcmTag)
	out = append(out, head...)
	for i := 0; i < n; i++ {
		end := min((i+1)*FileChunk, len(plain))
		out = aead.Seal(out, chunkNonce(uint64(i), i == n-1), plain[i*FileChunk:end], ad)
	}
	return out, nil
}

// OpenFile returns the plain bytes of a file SealFile made with these keys.
// Anything else, another person's file, a file cut short or a single
// changed bit, is an error that wraps ErrSealed.
func (k *Keys) OpenFile(sealed []byte) ([]byte, error) {
	if !IsSealedFile(sealed) {
		return nil, fmt.Errorf("not a sealed file: %w", ErrSealed)
	}
	chunk := int(binary.BigEndian.Uint32(sealed[8:]))
	if chunk < 1 || chunk > maxFileChunk {
		return nil, fmt.Errorf("a sealed file's chunks are not of a size Keystone writes: %w", ErrSealed)
	}
	aead, err := k.fileAEAD(sealed[12:fileHeader])
	if err != nil {
		return nil, err
	}
	ad := append(append([]byte{}, sealed[:fileHeader]...), k.tenant...)
	rest := sealed[fileHeader:]
	out := make([]byte, 0, len(rest))
	for i := uint64(0); ; i++ {
		size := min(chunk+gcmTag, len(rest))
		if size < gcmTag {
			return nil, fmt.Errorf("a sealed file is cut short: %w", ErrSealed)
		}
		last := size == len(rest)
		var err error
		if out, err = aead.Open(out, chunkNonce(i, last), rest[:size], ad); err != nil {
			return nil, fmt.Errorf("chunk %d: %w", i, ErrSealed)
		}
		rest = rest[size:]
		if last {
			return out, nil
		}
	}
}

// SealedSize is the size on disk of a file of n bytes, sealed.
func SealedSize(n int) int64 {
	chunks := max((n+FileChunk-1)/FileChunk, 1)
	return int64(fileHeader + n + chunks*gcmTag)
}

// IsSealedFile reports whether b starts as a sealed file does. It says
// nothing about whose it is.
func IsSealedFile(b []byte) bool {
	return len(b) >= fileHeader && bytes.Equal(b[:8], []byte(fileMagic))
}

func (k *Keys) fileAEAD(salt []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, k.files, salt, "keystone sealed file v1 for tenant "+k.tenant, keySize)
	if err != nil {
		return nil, err
	}
	return newGCM(key)
}

func chunkNonce(i uint64, last bool) []byte {
	n := make([]byte, gcmNonceBytes)
	binary.BigEndian.PutUint64(n, i)
	if last {
		n[11] = 1
	}
	return n
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
