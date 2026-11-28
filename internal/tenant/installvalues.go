package tenant

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
)

// The install's own setting values in keystone.json (P15-03): "settings",
// the value of each install setting the admin changed, and
// "person_defaults", the default each person's own setting starts from.
// Both are objects of key to value as text, as the registry writes a
// default. This package reads no setting: it keeps the two objects, and
// internal/installsettings says what they mean.
//
// Each read takes keystone.json from disk, so a value changed by another
// process is seen, and each write reads the file again first, changes only
// its own object, and keeps every other field and every key it does not
// know.

// The two fields of keystone.json the install's values are kept in.
const (
	SettingsField       = "settings"
	PersonDefaultsField = "person_defaults"
)

// InstallValues returns one of the two objects, SettingsField or
// PersonDefaultsField, as keystone.json holds it now. A missing object is
// empty.
func (in *Install) InstallValues(field string) (map[string]string, error) {
	if err := valuesField(field); err != nil {
		return nil, err
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.readValues(field)
}

// ChangeInstallValues changes one of the two objects: change is given the
// object as keystone.json holds it now and changes it in place. The file is
// written only when change returns nil; the rest of it is kept as it is on
// disk.
func (in *Install) ChangeInstallValues(field string, change func(map[string]string) error) error {
	if err := valuesField(field); err != nil {
		return err
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	values, err := in.readValues(field)
	if err != nil {
		return err
	}
	if err := change(values); err != nil {
		return err
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	had, kept := in.other[field]
	in.other[field] = raw
	if err := in.save(); err != nil {
		if kept {
			in.other[field] = had
		} else {
			delete(in.other, field)
		}
		return err
	}
	return nil
}

func valuesField(field string) error {
	if field != SettingsField && field != PersonDefaultsField {
		return fmt.Errorf("tenant: %q is not a field of the install's setting values", field)
	}
	return nil
}

// readValues reads one object from keystone.json on disk, and keeps what it
// read in in.other, so a save writes it back. The caller holds in.mu.
func (in *Install) readValues(field string) (map[string]string, error) {
	values := map[string]string{}
	raw, err := os.ReadFile(filepath.Join(in.dir, registryFile))
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("%s: %w", registryFile, err)
	}
	// Fields another program wrote since the install was opened are kept
	// by the next save. The tenant list and the version are this package's.
	for k, v := range all {
		if k != "tenants" && k != "version" {
			in.other[k] = v
		}
	}
	v, ok := all[field]
	if !ok || string(v) == "null" {
		delete(in.other, field)
		return values, nil
	}
	if err := json.Unmarshal(v, &values); err != nil {
		return nil, fmt.Errorf("%s: %s: %w", registryFile, field, err)
	}
	in.other[field] = v
	return maps.Clone(values), nil
}
