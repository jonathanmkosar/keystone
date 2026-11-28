package settings

import (
	"maps"
	"sync"
)

// The install's own values (P15-03). The admin sets an install setting for
// the whole install, and a default for each person's own settings, from the
// browser; internal/installsettings keeps them in keystone.json and hands
// them here at start and on each change. They are the one overlay on the
// registry: Registry gives each as its setting's Default, and DefaultFor
// answers it for code that read the registry before the change.
//
// Only an install setting or a person's own (tenant) setting takes an
// install value. A value that does not pass CheckValue is passed over, so a
// registry change that makes a stored value wrong falls back to the shipped
// default, as a tenant's own value does.
//
// The overlay is the process's: one server serves one install. A process
// that never calls UseInstalled, such as verify or a test, sees the lane
// files' defaults.

var installed struct {
	sync.RWMutex
	values map[string]string
}

// UseInstalled sets the install's own values, by key, replacing the ones
// set before. Nil or empty clears them.
func UseInstalled(values map[string]string) {
	installed.Lock()
	defer installed.Unlock()
	installed.values = maps.Clone(values)
}

// Installed returns a copy of the install's own values as UseInstalled last
// set them.
func Installed() map[string]string {
	installed.RLock()
	defer installed.RUnlock()
	out := maps.Clone(installed.values)
	if out == nil {
		out = map[string]string{}
	}
	return out
}

// TakesInstallValue reports whether a setting can have an install value: an
// install setting, or a person's own setting, whose install value is the
// default a person starts with. A tenant-admin setting cannot.
func TakesInstallValue(s Setting) bool {
	return s.Scope == ScopeInstall || s.Scope == ScopeTenant
}

// installedValue returns the install's value for a setting, if it has a
// usable one.
func installedValue(s Setting) (string, bool) {
	if !TakesInstallValue(s) || IsFixedRule(s.Key) {
		return "", false
	}
	installed.RLock()
	v, ok := installed.values[s.Key]
	installed.RUnlock()
	if !ok || CheckValue(s, v) != nil {
		return "", false
	}
	return v, true
}

// Shipped returns a setting's default as its lane file has it, whatever the
// install has set.
func Shipped(s Setting) string {
	if s.installed {
		return s.shipped
	}
	return s.Default
}

// DefaultFor returns the default in force for a setting now: the install's
// own value when it has one, and the shipped default otherwise. It holds
// for a Setting read from the registry before the install's values last
// changed, which code that keeps the registry for its life has.
func DefaultFor(s Setting) string {
	if v, ok := installedValue(s); ok {
		return v
	}
	return Shipped(s)
}

// withInstalled gives each setting with an install value that value as its
// Default.
func withInstalled(reg []Setting) []Setting {
	installed.RLock()
	empty := len(installed.values) == 0
	installed.RUnlock()
	if empty {
		return reg
	}
	for i, s := range reg {
		if v, ok := installedValue(s); ok {
			reg[i].shipped, reg[i].installed = s.Default, true
			reg[i].Default = v
		}
	}
	return reg
}
