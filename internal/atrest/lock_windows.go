package atrest

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// On Windows the lock is one byte far past the end of the database, taken
// with LockFileEx on a handle of the process's own. SQLite never reads or
// writes there, and a lock there stops nothing but another such lock.
var lockAt = windows.Overlapped{OffsetHigh: 0x40000000}

func tryLock(f *os.File) error {
	ol := lockAt
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return errLocked
	}
	return err
}

func unlock(f *os.File) {
	ol := lockAt
	windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
