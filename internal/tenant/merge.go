package tenant

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// mergeFromDisk adds to the registry this process holds every person that
// keystone.json on disk lists and it does not, before it is saved (P15-02).
// A person added from the command line while the server runs is then kept
// when the server saves, as when an admin adds a person, instead of being
// dropped from the list. A listed person whose folder is not there is left
// out, as a half-made one. The caller holds in.mu, or is the only user of
// the install.
func (in *Install) mergeFromDisk() error {
	raw, err := os.ReadFile(filepath.Join(in.dir, registryFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var disk struct {
		Tenants []Tenant `json:"tenants"`
	}
	if err := json.Unmarshal(raw, &disk); err != nil {
		return fmt.Errorf("%s: %w", registryFile, err)
	}
	have := map[string]bool{}
	for _, t := range in.tenants {
		have[t.ID] = true
	}
	for _, t := range disk.Tenants {
		if have[t.ID] || !idShape.MatchString(t.ID) {
			continue
		}
		if info, err := os.Stat(filepath.Join(in.dir, "tenants", t.ID)); err != nil || !info.IsDir() {
			continue
		}
		in.tenants = append(in.tenants, t)
		have[t.ID] = true
	}
	return nil
}
