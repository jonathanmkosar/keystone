//go:build !windows && !darwin && !linux

package secrets

// System returns the operating system's keeper. Keystone knows none for
// this system.
func System() (Keeper, error) { return nil, ErrNoKeeper }
