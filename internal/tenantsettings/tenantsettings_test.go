package tenantsettings

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

var ctx = context.Background()

// twoStores makes an install with two tenants and returns the install and
// an open handle for each. Every name here is invented.
func twoStores(t *testing.T) (in *tenant.Install, a, b *tenant.Store) {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"Test Person One", "Test Person Two"} {
		tn, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(tn.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		if i == 0 {
			a = s
		} else {
			b = s
		}
	}
	return in, a, b
}

// registry reads a made-up registry through the real reader, so it is
// checked and sorted like the real one. It has a setting of each scope.
func registry(t *testing.T, more ...string) []settings.Setting {
	t.Helper()
	body := `{"key":"appearance.theme","type":"string","default":"dark","options":["dark","light","system"],"scope":"tenant","description":"The colour theme."},
		{"key":"chat.step_limit","type":"int","default":"20","scope":"tenant","description":"The most steps in one turn."},
		{"key":"spend.monthly_cap","type":"float","default":"100","scope":"tenant-admin","description":"The monthly cap on hosted spend."},
		{"key":"server.address","type":"string","default":"127.0.0.1:7900","scope":"install","description":"The address Keystone listens on."}`
	for _, m := range more {
		body += "," + m
	}
	reg, err := settings.Read(fstest.MapFS{"core.json": &fstest.MapFile{Data: []byte(`{"lane":"core","settings":[` + body + `]}`)}})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func get(t *testing.T, s *tenant.Store, reg []settings.Setting, key string) string {
	t.Helper()
	v, err := Get(ctx, s, reg, key)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func stored(t *testing.T, s *tenant.Store) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow("SELECT count(*) FROM tenant_settings").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAPersonOverridesTheirOwnSetting(t *testing.T) {
	in, a, b := twoStores(t)
	reg := registry(t)

	if got := get(t, a, reg, "appearance.theme"); got != "dark" {
		t.Fatalf("with nothing stored the theme is %q, want the default", got)
	}
	if err := Set(ctx, a, reg, Person, "appearance.theme", "light"); err != nil {
		t.Fatal(err)
	}
	if got := get(t, a, reg, "appearance.theme"); got != "light" {
		t.Errorf("after the override the theme is %q, want light", got)
	}
	if got := get(t, b, reg, "appearance.theme"); got != "dark" {
		t.Errorf("tenant two's theme is %q: tenant one's override reached it", got)
	}

	// Setting it again replaces the value.
	if err := Set(ctx, a, reg, Person, "appearance.theme", "system"); err != nil {
		t.Fatal(err)
	}
	if got := get(t, a, reg, "appearance.theme"); got != "system" || stored(t, a) != 1 {
		t.Errorf("after a second override the theme is %q in %d rows", got, stored(t, a))
	}

	// The admin sets a tenant-admin value for this tenant alone.
	if err := Set(ctx, a, reg, Admin, "spend.monthly_cap", "250.5"); err != nil {
		t.Fatal(err)
	}
	if got := get(t, a, reg, "spend.monthly_cap"); got != "250.5" {
		t.Errorf("the cap is %q, want 250.5", got)
	}
	if got := get(t, b, reg, "spend.monthly_cap"); got != "100" {
		t.Errorf("tenant two's cap is %q, want the default", got)
	}

	// All lists every setting in the registry, in key order.
	all, err := All(ctx, a, reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(reg) {
		t.Fatalf("All returned %d settings, want %d", len(all), len(reg))
	}
	want := map[string]Value{
		"appearance.theme":  {Value: "system", Overridden: true, SetBy: Person},
		"chat.step_limit":   {Value: "20"},
		"spend.monthly_cap": {Value: "250.5", Overridden: true, SetBy: Admin},
		"server.address":    {Value: "127.0.0.1:7900"},
	}
	for i, v := range all {
		w := want[v.Setting.Key]
		if v.Setting.Key != reg[i].Key || v.Value != w.Value || v.Overridden != w.Overridden || v.SetBy != w.SetBy || v.UpdatedAt.IsZero() == w.Overridden {
			t.Errorf("%s stands as %+v", v.Setting.Key, v)
		}
	}

	// The values are in the database: they are there after a reopen.
	id := a.TenantID()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got := get(t, again, reg, "appearance.theme"); got != "system" {
		t.Errorf("after a reopen the theme is %q, want system", got)
	}

	// Clearing goes back to the default.
	if err := Clear(ctx, again, reg, Person, "appearance.theme"); err != nil {
		t.Fatal(err)
	}
	if got := get(t, again, reg, "appearance.theme"); got != "dark" {
		t.Errorf("after clearing the theme is %q, want the default", got)
	}
	if err := Clear(ctx, again, reg, Person, "appearance.theme"); err != nil {
		t.Errorf("clearing a setting with no stored value failed: %v", err)
	}
}

func TestTheRealRegistryTakesAnOverride(t *testing.T) {
	_, a, _ := twoStores(t)
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	if err := Set(ctx, a, reg, Person, "appearance.theme", "light"); err != nil {
		t.Fatal(err)
	}
	if got := get(t, a, reg, "appearance.theme"); got != "light" {
		t.Errorf("the theme is %q, want light", got)
	}
	if err := Set(ctx, a, reg, Person, "server.address", "0.0.0.0:80"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("a person set the install's address: %v", err)
	}
}

func TestScopeDecidesWhoMayChange(t *testing.T) {
	_, a, _ := twoStores(t)
	reg := registry(t)
	cases := []struct {
		by      Role
		key     string
		value   string
		allowed bool
	}{
		{Person, "appearance.theme", "light", true},
		{Person, "chat.step_limit", "30", true},
		{Admin, "spend.monthly_cap", "50", true},
		{Person, "spend.monthly_cap", "100000", false}, // a person must not raise their own cap
		{Admin, "appearance.theme", "light", false},    // decision 0001: the person's own
		{Person, "server.address", "0.0.0.0:80", false},
		{Admin, "server.address", "0.0.0.0:80", false}, // one value for the install, never one per tenant
		{"", "appearance.theme", "light", false},
		{"agent", "appearance.theme", "light", false},
	}
	for _, c := range cases {
		before := stored(t, a)
		err := Set(ctx, a, reg, c.by, c.key, c.value)
		switch {
		case c.allowed && err != nil:
			t.Errorf("%s setting %s: refused: %v", c.by, c.key, err)
		case !c.allowed && !errors.Is(err, ErrNotAllowed):
			t.Errorf("%s setting %s: want ErrNotAllowed, got %v", c.by, c.key, err)
		case !c.allowed && stored(t, a) != before:
			t.Errorf("%s setting %s: refused, and a row was stored", c.by, c.key)
		}
		st, _ := settings.Lookup(reg, c.key)
		if got := MayChange(st, c.by) == nil; got != c.allowed {
			t.Errorf("MayChange(%s, %s) is %v, want %v", c.key, c.by, got, c.allowed)
		}
		if err := Clear(ctx, a, reg, c.by, c.key); (err == nil) != c.allowed {
			t.Errorf("%s clearing %s: allowed is %v, want %v", c.by, c.key, err == nil, c.allowed)
		}
	}
	// A person may override exactly the settings the registry says they may.
	for _, st := range reg {
		if (MayChange(st, Person) == nil) != settings.TenantMayOverride(st) {
			t.Errorf("%s: MayChange and settings.TenantMayOverride disagree", st.Key)
		}
	}
}

func TestValuesAndKeysAreChecked(t *testing.T) {
	_, a, _ := twoStores(t)
	reg := registry(t)
	bad := []struct{ key, value string }{
		{"appearance.theme", "purple"}, // not one of the options
		{"appearance.theme", ""},
		{"chat.step_limit", "many"}, // not an int
		{"chat.step_limit", "2.5"},
	}
	for _, c := range bad {
		if err := Set(ctx, a, reg, Person, c.key, c.value); !errors.Is(err, ErrBadValue) {
			t.Errorf("%s = %q: want ErrBadValue, got %v", c.key, c.value, err)
		}
	}
	if err := Set(ctx, a, reg, Admin, "spend.monthly_cap", "lots"); !errors.Is(err, ErrBadValue) {
		t.Errorf("a cap of lots: want ErrBadValue, got %v", err)
	}
	if err := Set(ctx, a, reg, Person, "no.such_setting", "1"); !errors.Is(err, ErrUnknown) {
		t.Errorf("an unknown key: want ErrUnknown, got %v", err)
	}
	if _, err := Get(ctx, a, reg, "no.such_setting"); !errors.Is(err, ErrUnknown) {
		t.Errorf("reading an unknown key: want ErrUnknown, got %v", err)
	}
	if err := Clear(ctx, a, reg, Person, "no.such_setting"); !errors.Is(err, ErrUnknown) {
		t.Errorf("clearing an unknown key: want ErrUnknown, got %v", err)
	}
	if n := stored(t, a); n != 0 {
		t.Errorf("%d rows were stored by refused calls", n)
	}
}

func TestAFixedRuleCannotBeOverridden(t *testing.T) {
	_, a, _ := twoStores(t)
	reg := registry(t)
	rules := settings.FixedRules()
	if len(rules) != 4 {
		t.Fatalf("there are %d fixed rules, want 4", len(rules))
	}
	// Even a registry that lists a rule as a setting, which the real reader
	// refuses, does not open a way in.
	forged := append([]settings.Setting(nil), reg...)
	for _, r := range rules {
		forged = append(forged, settings.Setting{Key: r.Key, Type: "bool", Default: "true", Scope: settings.ScopeTenant, Description: "Switches a rule."})
	}
	// A row to try to rename into a rule.
	if err := Set(ctx, a, reg, Person, "appearance.theme", "light"); err != nil {
		t.Fatal(err)
	}

	for _, r := range rules {
		for _, by := range []Role{Person, Admin} {
			for name, rg := range map[string][]settings.Setting{"the registry": reg, "a forged registry": forged} {
				for _, value := range []string{"false", "off", "0", ""} {
					if err := Set(ctx, a, rg, by, r.Key, value); !errors.Is(err, ErrNotAllowed) {
						t.Errorf("%s set %s to %q with %s: want ErrNotAllowed, got %v", by, r.Key, value, name, err)
					}
				}
				if err := Clear(ctx, a, rg, by, r.Key); !errors.Is(err, ErrNotAllowed) {
					t.Errorf("%s cleared %s with %s: want ErrNotAllowed, got %v", by, r.Key, name, err)
				}
				if _, err := Get(ctx, a, rg, r.Key); !errors.Is(err, ErrNotAllowed) {
					t.Errorf("%s read as a setting with %s: want ErrNotAllowed, got %v", r.Key, name, err)
				}
			}
			if err := MayChange(settings.Setting{Key: r.Key, Scope: settings.ScopeTenant}, by); !errors.Is(err, ErrNotAllowed) {
				t.Errorf("MayChange allows %s to change %s", by, r.Key)
			}
		}
		// The database refuses the row from any code.
		if _, err := a.DB().Exec("INSERT INTO tenant_settings(key, value, set_by, updated_at) VALUES(?, 'false', 'admin', '2026-11-24T00:00:00Z')", r.Key); err == nil {
			t.Errorf("the database stored a value for %s", r.Key)
		}
		if _, err := a.DB().Exec("UPDATE tenant_settings SET key = ? WHERE key = 'appearance.theme'", r.Key); err == nil {
			t.Errorf("the database renamed a row to %s", r.Key)
		}
	}
	var n int
	if err := a.DB().QueryRow("SELECT count(*) FROM tenant_settings WHERE key GLOB 'rules.*'").Scan(&n); err != nil || n != 0 {
		t.Errorf("%d rows name a fixed rule (%v)", n, err)
	}
	if n := stored(t, a); n != 1 {
		t.Errorf("the table holds %d rows, want the theme alone", n)
	}
}

func TestAStoredValueThatNoLongerFitsFallsBackToTheDefault(t *testing.T) {
	_, a, _ := twoStores(t)
	mode := func(options, scope string) []settings.Setting {
		return registry(t, `{"key":"chat.mode","type":"string","default":"plain","options":[`+options+`],"scope":"`+scope+`","description":"A made-up setting."}`)
	}
	first := mode(`"plain","rich"`, "tenant")
	if err := Set(ctx, a, first, Person, "chat.mode", "rich"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		reg  []settings.Setting
		want string
	}{
		{"unchanged", first, "rich"},
		{"the option was dropped", mode(`"plain","fancy"`, "tenant"), "plain"},
		{"the setting became the admin's", mode(`"plain","rich"`, "tenant-admin"), "plain"},
		{"the setting became the install's", mode(`"plain","rich"`, "install"), "plain"},
		{"changed back", first, "rich"},
	}
	for _, c := range cases {
		if got := get(t, a, c.reg, "chat.mode"); got != c.want {
			t.Errorf("%s: the value in force is %q, want %q", c.name, got, c.want)
		}
		all, err := All(ctx, a, c.reg)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range all {
			if v.Setting.Key == "chat.mode" && (v.Value != c.want || v.Overridden != (c.want == "rich")) {
				t.Errorf("%s: All says %+v", c.name, v)
			}
		}
	}
}
