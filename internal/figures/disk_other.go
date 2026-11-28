//go:build !windows && !linux && !darwin && !freebsd

package figures

import "errors"

// Volume is not known on this system: the disk limit says so.
func Volume(string) (total, free uint64, err error) {
	return 0, 0, errors.New("this system's free space is not read by Keystone")
}
