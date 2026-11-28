package app

import (
	"testing"

	"github.com/jonathanmkosar/keystone/settings"
)

// Every install setting the server reads into its configuration at start
// takes effect only after a restart, so the registry must say so (P15-03):
// the admin's settings screen tells the admin which changes wait.
func TestEveryInstallSettingReadAtStartIsMarkedRestart(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	read := map[string]bool{}
	if _, err := ReadConfig(func(key string) (string, bool) {
		read[key] = true
		st, ok := settings.Lookup(reg, key)
		return st.Default, ok
	}); err != nil {
		t.Fatal(err)
	}
	if len(read) == 0 {
		t.Fatal("the configuration read no setting, so the test proves nothing")
	}
	for key := range read {
		st, ok := settings.Lookup(reg, key)
		if ok && st.Scope == settings.ScopeInstall && !st.Restart {
			t.Errorf(`%s is read once, at start, and is not marked "restart": true in settings/%s.json`, key, st.Lane)
		}
	}
}
