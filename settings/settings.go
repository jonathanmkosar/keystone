// Package settings is the registry of every value an owner or a person may change.
//
// Each lane keeps its settings in its own JSON file in this folder, so lanes
// working at the same time do not edit the same file. The files are compiled
// into the binary and merged into one registry when it is read.
package settings

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed *.json
var files embed.FS

// Scope says who may change a setting.
type Scope string

const (
	// ScopeInstall is one value for the whole install, set by the admin.
	ScopeInstall Scope = "install"
	// ScopeTenantAdmin is one value per tenant, set by the admin.
	ScopeTenantAdmin Scope = "tenant-admin"
	// ScopeTenant is one value per tenant, set by that person.
	ScopeTenant Scope = "tenant"
)

// Setting is one entry in the registry. Default is kept as text and read
// according to Type.
type Setting struct {
	Key         string   `json:"key"`
	Lane        string   `json:"lane"`
	Type        string   `json:"type"`
	Default     string   `json:"default"`
	Options     []string `json:"options,omitempty"`
	Scope       Scope    `json:"scope"`
	Description string   `json:"description"`
}

type laneFile struct {
	Lane     string    `json:"lane"`
	Settings []Setting `json:"settings"`
}

var (
	keyShape = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	types    = []string{"bool", "int", "float", "string", "duration"}
	scopes   = []Scope{ScopeInstall, ScopeTenantAdmin, ScopeTenant}
)

// Registry returns every setting compiled into the binary, sorted by key.
func Registry() ([]Setting, error) {
	return Read(files)
}

// Read merges every lane file in fsys into one registry, sorted by key.
// It refuses a file whose name and lane disagree, any setting that fails
// Validate, and a key that appears twice.
func Read(fsys fs.FS) ([]Setting, error) {
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var all []Setting
	seen := map[string]string{}
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		var lf laneFile
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&lf); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if want := strings.TrimSuffix(path.Base(name), ".json"); lf.Lane != want {
			return nil, fmt.Errorf("%s: lane is %q, and the file name says %q", name, lf.Lane, want)
		}
		for _, s := range lf.Settings {
			if s.Lane != "" && s.Lane != lf.Lane {
				return nil, fmt.Errorf("%s: %s names lane %q inside the %q file", name, s.Key, s.Lane, lf.Lane)
			}
			s.Lane = lf.Lane
			if err := Validate(s); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if other, dup := seen[s.Key]; dup {
				return nil, fmt.Errorf("%s: %s is already defined in %s", name, s.Key, other)
			}
			seen[s.Key] = name
			all = append(all, s)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Key < all[j].Key })
	return all, nil
}

// Validate checks one setting: the shape of its key, its type, that its
// default reads as that type, its options, its scope and its description.
func Validate(s Setting) error {
	if !keyShape.MatchString(s.Key) {
		return fmt.Errorf("setting %q: a key is lower-case words joined by dots, such as server.address", s.Key)
	}
	if !slices.Contains(types, s.Type) {
		return fmt.Errorf("%s: type %q is not one of %s", s.Key, s.Type, strings.Join(types, ", "))
	}
	if err := ParseAs(s.Type, s.Default); err != nil {
		return fmt.Errorf("%s: default %q is not a %s", s.Key, s.Default, s.Type)
	}
	if len(s.Options) > 0 {
		if s.Type != "string" {
			return fmt.Errorf("%s: only a string setting can list options", s.Key)
		}
		if !slices.Contains(s.Options, s.Default) {
			return fmt.Errorf("%s: default %q is not one of its options", s.Key, s.Default)
		}
	}
	if !slices.Contains(scopes, s.Scope) {
		return fmt.Errorf("%s: scope %q is not install, tenant-admin or tenant", s.Key, s.Scope)
	}
	d := strings.TrimSpace(s.Description)
	if d == "" || !strings.HasSuffix(d, ".") {
		return fmt.Errorf("%s: a description is a full sentence ending in a full stop", s.Key)
	}
	return nil
}

// ParseAs reports whether value reads as the given setting type.
func ParseAs(typ, value string) error {
	var err error
	switch typ {
	case "bool":
		_, err = strconv.ParseBool(value)
	case "int":
		_, err = strconv.Atoi(value)
	case "float":
		_, err = strconv.ParseFloat(value, 64)
	case "duration":
		_, err = time.ParseDuration(value)
	case "string":
	default:
		err = fmt.Errorf("unknown type %q", typ)
	}
	return err
}

// Lookup finds a setting by key.
func Lookup(reg []Setting, key string) (Setting, bool) {
	i := sort.Search(len(reg), func(i int) bool { return reg[i].Key >= key })
	if i < len(reg) && reg[i].Key == key {
		return reg[i], true
	}
	return Setting{}, false
}

// TenantMayOverride reports whether a person may change this setting for
// their own tenant.
func TenantMayOverride(s Setting) bool {
	return s.Scope == ScopeTenant
}

// CheckValue reports whether value is acceptable for the setting: it must
// read as the setting's type and, where options are listed, be one of them.
func CheckValue(s Setting, value string) error {
	if err := ParseAs(s.Type, value); err != nil {
		return fmt.Errorf("%s: %q is not a %s", s.Key, value, s.Type)
	}
	if len(s.Options) > 0 && !slices.Contains(s.Options, value) {
		return fmt.Errorf("%s: %q is not one of %s", s.Key, value, strings.Join(s.Options, ", "))
	}
	return nil
}
