//go:build windows

package secrets

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// dpapiKeeper wraps with the Windows data protection API. The master key
// belongs to the Windows account Keystone runs as. Windows holds it, and
// Keystone never sees it.
type dpapiKeeper struct{}

// System returns the operating system's keeper. On Windows that is DPAPI.
func System() (Keeper, error) { return dpapiKeeper{}, nil }

func (dpapiKeeper) Name() string { return "dpapi" }

// Wrap protects key for this Windows account. The context goes in as
// DPAPI's extra entropy, so the same context is needed to unwrap.
func (dpapiKeeper) Wrap(key []byte, context string) ([]byte, error) {
	if len(key) == 0 {
		return nil, errors.New("there is no key to wrap")
	}
	var out windows.DataBlob
	err := windows.CryptProtectData(blob(key), nil, blob([]byte(context)), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return nil, fmt.Errorf("DPAPI: %w", err)
	}
	return take(out), nil
}

func (dpapiKeeper) Unwrap(wrapped []byte, context string) ([]byte, error) {
	if len(wrapped) == 0 {
		return nil, errors.New("there is no wrapped key")
	}
	var out windows.DataBlob
	err := windows.CryptUnprotectData(blob(wrapped), nil, blob([]byte(context)), 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return nil, fmt.Errorf("DPAPI: %w", err)
	}
	return take(out), nil
}

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

// take copies what Windows returned, then wipes and frees Windows' copy.
func take(out windows.DataBlob) []byte {
	if out.Data == nil {
		return nil
	}
	theirs := unsafe.Slice(out.Data, out.Size)
	ours := append([]byte{}, theirs...)
	clear(theirs)
	windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return ours
}
