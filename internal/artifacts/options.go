package artifacts

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
)

// Options are the install's limits on artifacts, from
// settings/artifacts.json. The caller reads them and passes them in: this
// package reads no setting.
type Options struct {
	// MaxBytes is the largest one version's content may be
	// (artifacts.max_bytes).
	MaxBytes int `json:"max_bytes"`
	// VersionsKept is how many of an artifact's newest versions are kept.
	// Zero keeps every version (artifacts.versions_kept).
	VersionsKept int `json:"versions_kept"`
	// TitleMaxChars is the most characters a title may have
	// (artifacts.title_max_chars).
	TitleMaxChars int `json:"title_max_chars"`
	// Sandbox are the permissions an artifact's frame is given, each one
	// of SandboxAllowed (artifacts.sandbox). Same-origin, top navigation
	// and popups are never among them.
	Sandbox []string `json:"sandbox"`

	// Runtime is where the sandbox's renderers are found: the built web
	// interface, with sandbox/react.js and sandbox/mermaid.js. It is not a
	// setting. Nil means the files compiled into the binary.
	Runtime fs.FS `json:"-"`
}

// The settings ReadOptions reads.
const (
	SettingMaxBytes      = "artifacts.max_bytes"
	SettingVersionsKept  = "artifacts.versions_kept"
	SettingTitleMaxChars = "artifacts.title_max_chars"
	SettingSandbox       = "artifacts.sandbox"
)

// SandboxAllowed are the only permissions an artifact's frame may be
// given. Anything else is refused, whatever a setting says: allow-same-origin
// would give an artifact's script the person's session, and top navigation
// and popups would let it lead the page or a new window away.
var SandboxAllowed = []string{"allow-scripts", "allow-forms", "allow-modals", "allow-downloads", "allow-pointer-lock"}

// ReadOptions reads the options from wherever setting values are kept.
func ReadOptions(get func(key string) (string, bool)) (Options, error) {
	v := map[string]string{}
	for _, key := range []string{SettingMaxBytes, SettingVersionsKept, SettingTitleMaxChars, SettingSandbox} {
		s, ok := get(key)
		if !ok {
			return Options{}, fmt.Errorf("the %s setting is missing", key)
		}
		v[key] = s
	}
	var o Options
	var err error
	for key, into := range map[string]*int{SettingMaxBytes: &o.MaxBytes, SettingVersionsKept: &o.VersionsKept, SettingTitleMaxChars: &o.TitleMaxChars} {
		if *into, err = strconv.Atoi(strings.TrimSpace(v[key])); err != nil {
			return Options{}, fmt.Errorf("%s: %w", key, err)
		}
	}
	o.Sandbox = strings.Fields(strings.ToLower(v[SettingSandbox]))
	return o, o.Check()
}

// Check refuses options no artifact could be kept or shown under.
func (o Options) Check() error {
	switch {
	case o.MaxBytes < 1:
		return errors.New(SettingMaxBytes + " must be 1 or more")
	case o.VersionsKept < 0:
		return errors.New(SettingVersionsKept + " must be 0, for every version, or more")
	case o.TitleMaxChars < 1:
		return errors.New(SettingTitleMaxChars + " must be 1 or more")
	}
	for _, p := range o.Sandbox {
		if !slices.Contains(SandboxAllowed, p) {
			return fmt.Errorf("%s: %q is not a permission an artifact's frame may have; it may have %s", SettingSandbox, p, strings.Join(SandboxAllowed, ", "))
		}
	}
	return nil
}

// SandboxTokens is the frame's sandbox attribute: the permissions the
// options name that are in SandboxAllowed, and nothing else. Check refuses
// options with any other; this drops one that got past it.
func (o Options) SandboxTokens() string {
	var out []string
	for _, p := range o.Sandbox {
		if slices.Contains(SandboxAllowed, p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}
