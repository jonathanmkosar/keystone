package loadtest

import "github.com/jonathanmkosar/keystone/internal/atrest"

// onDisk is the size a file of n bytes takes in a person's folder: sealed,
// when encryption at rest is on (P15-06), so the test's own count of the
// files and artifacts is what the Monitor reads from the disk.
func onDisk(who *person, n int) int64 {
	if who.store != nil && who.store.Sealed() {
		return atrest.SealedSize(n)
	}
	return int64(n)
}
