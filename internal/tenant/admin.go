package tenant

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The admin mark (P15-01). An admin is a person with a mark in the
// registry, not a second kind of account (decision 0640). The mark is read
// from keystone.json on disk each time it is asked about, so a change made
// from the command line counts in a running server at once (decision 0641).

// diskMarks reads the admin marks from keystone.json as it is on disk now.
// A registry that is not written yet has no marks.
func (in *Install) diskMarks() (map[string]bool, error) {
	raw, err := os.ReadFile(filepath.Join(in.dir, registryFile))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	var file struct {
		Tenants []Tenant `json:"tenants"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", registryFile, err)
	}
	marks := map[string]bool{}
	for _, t := range file.Tenants {
		if t.Admin {
			marks[t.ID] = true
		}
	}
	return marks, nil
}

// Marks returns the ids of the tenants that carry the admin mark, read from
// keystone.json now. An id that is not in the map has no mark.
func (in *Install) Marks() (map[string]bool, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.diskMarks()
}

// IsAdmin reports whether the tenant carries the admin mark, read from
// keystone.json now. An id that is not in the registry has none. The API
// shell asks it at every call to an admin route.
func (in *Install) IsAdmin(id string) (bool, error) {
	marks, err := in.Marks()
	if err != nil {
		return false, err
	}
	return marks[id], nil
}

// SetAdmin gives a tenant the admin mark, or takes it away, and saves the
// registry. It is the only code that changes a mark, and the command line
// (go run ./tools/tenant admin) is the only code that calls it: no route
// can. The admin isolation check in verify holds both.
func (in *Install) SetAdmin(id string, on bool) (Tenant, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	i := -1
	for k, t := range in.tenants {
		if t.ID == id {
			i = k
		}
	}
	if i < 0 {
		return Tenant{}, fmt.Errorf("there is no tenant %s", id)
	}
	marks, err := in.diskMarks()
	if err != nil {
		return Tenant{}, err
	}
	before := make([]bool, len(in.tenants))
	for k := range in.tenants {
		before[k] = in.tenants[k].Admin
		in.tenants[k].Admin = marks[in.tenants[k].ID]
	}
	in.tenants[i].Admin = on
	if err := in.write(); err != nil {
		for k := range in.tenants {
			in.tenants[k].Admin = before[k]
		}
		return Tenant{}, err
	}
	return in.tenants[i], nil
}
