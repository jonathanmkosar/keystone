package figures

import "golang.org/x/sys/windows"

// Volume returns the size of the volume that holds dir, and the bytes on it
// this process may still write, from Windows.
func Volume(dir string) (total, free uint64, err error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0, err
	}
	var avail, all, allFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &all, &allFree); err != nil {
		return 0, 0, err
	}
	return all, avail, nil
}
