//go:build linux || darwin || freebsd

package figures

import "syscall"

// Volume returns the size of the volume that holds dir, and the bytes on it
// an ordinary process may still write, from statfs.
func Volume(dir string) (total, free uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	size := uint64(st.Bsize)
	return uint64(st.Blocks) * size, uint64(st.Bavail) * size, nil
}
