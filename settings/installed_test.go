package settings

import "testing"

// The install's own values (P15-03) are the one overlay on the registry.
func TestInstallValuesAreTheDefaultsInForce(t *testing.T) {
	t.Cleanup(func() { UseInstalled(nil) })
	shipped, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	get := func(reg []Setting, key string) Setting {
		t.Helper()
		st, ok := Lookup(reg, key)
		if !ok {
			t.Fatalf("%s is not in the registry", key)
		}
		return st
	}
	const (
		install      = "server.page_limit" // install, int
		person       = "appearance.theme"  // tenant, options
		forOnePerson = "share.max_links"   // tenant-admin
	)
	if get(shipped, install).Scope != ScopeInstall || get(shipped, person).Scope != ScopeTenant || get(shipped, forOnePerson).Scope != ScopeTenantAdmin {
		t.Fatal("the test's keys no longer have the scopes it needs")
	}
	before := get(shipped, install)

	UseInstalled(map[string]string{install: "77", person: "dark", forOnePerson: "3", "rules.approval": "false", "no.such_key": "1"})
	reg, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if got := get(reg, install); got.Default != "77" || Shipped(got) != before.Default {
		t.Errorf("an install value gave %q, shipped %q", got.Default, Shipped(got))
	}
	if got := get(reg, person); got.Default != "dark" {
		t.Errorf("a person's default gave %q", got.Default)
	}
	if got := get(reg, forOnePerson); got.Default != get(shipped, forOnePerson).Default || DefaultFor(got) != got.Default {
		t.Errorf("a tenant-admin setting took an install value: %q", got.Default)
	}
	// A registry read before the change answers the value in force now.
	if got := DefaultFor(before); got != "77" {
		t.Errorf("DefaultFor on an older registry gave %q", got)
	}
	// A value that does not fit is passed over.
	UseInstalled(map[string]string{install: "many", person: "purple"})
	if reg, _ = Registry(); get(reg, install).Default != before.Default || get(reg, person).Default != get(shipped, person).Default {
		t.Error("a value that does not fit the setting was used")
	}
	// Taken away, even a registry read while it was set falls back.
	UseInstalled(map[string]string{install: "77"})
	held, _ := Registry()
	UseInstalled(nil)
	if got := DefaultFor(get(held, install)); got != before.Default {
		t.Errorf("after the install value was taken away, DefaultFor gave %q", got)
	}
	if len(Installed()) != 0 {
		t.Errorf("Installed after clearing is %v", Installed())
	}
}

func TestOnlyAnInstallSettingIsMarkedRestart(t *testing.T) {
	s := Setting{Key: "test.restart", Type: "int", Default: "1", Scope: ScopeTenant, Description: "A test setting.", Restart: true}
	if err := Validate(s); err == nil {
		t.Error("a person's setting marked restart was accepted")
	}
	s.Scope = ScopeInstall
	if err := Validate(s); err != nil {
		t.Error(err)
	}
}
