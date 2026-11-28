//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package atrest

import (
	"errors"
	"os"
	"syscall"
)

// On Unix the lock is flock on a descriptor of the process's own. SQLite's
// own locks are fcntl locks, which flock does not touch, and a sealed
// database is only ever opened through this VFS.
func tryLock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errLocked
	}
	return err
}

func unlock(f *os.File) { syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
