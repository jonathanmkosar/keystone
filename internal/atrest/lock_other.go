//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package atrest

import "os"

// Elsewhere there is no lock between processes: one Keystone process at a
// time may have a sealed database open (docs/architecture/encryption.md).
func tryLock(*os.File) error { return nil }

func unlock(*os.File) {}
