package installsettings

import (
	"context"
	"errors"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// The keys the tests use, each with the scope it needs.
const (
	liveKey    = "usage.report_max_days" // install, read when used
	restartKey = "server.max_body_bytes" // install, read at start
	personKey  = "appearance.theme"      // a person's own, with options
	adminKey   = "share.max_links"       // tenant-admin
)

func open(t *testing.T) *tenant.Install {
	t.Helper()
	t.Cleanup(func() { settings.UseInstalled(nil) })
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	if err := Load(in); err != nil {
		t.Fatal(err)
	}
	return in
}

func shipped(t *testing.T, key string) settings.Setting {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := settings.Lookup(reg, key)
	if !ok {
		t.Fatalf("%s is not in the registry", key)
	}
	return st
}

func TestTheTestsKeysHaveTheirScopes(t *testing.T) {
	if st := shipped(t, liveKey); st.Scope != settings.ScopeInstall || st.Restart {
		t.Errorf("%s is %s, restart %v", liveKey, st.Scope, st.Restart)
	}
	if st := shipped(t, restartKey); st.Scope != settings.ScopeInstall || !st.Restart {
		t.Errorf("%s is %s, restart %v", restartKey, st.Scope, st.Restart)
	}
	if shipped(t, personKey).Scope != settings.ScopeTenant || shipped(t, adminKey).Scope != settings.ScopeTenantAdmin {
		t.Error("the person's key or the admin's key has another scope")
	}
}

func TestAnInstallValueTakesEffectAtOnceAndIsKept(t *testing.T) {
	in := open(t)
	def := shipped(t, liveKey).Default
	c, err := Set(in, liveKey, "41")
	if err != nil {
		t.Fatal(err)
	}
	if c.Old != def || c.New != "41" || c.Restart || c.Scope != settings.ScopeInstall {
		t.Errorf("the change is %+v", c)
	}
	if got := shipped(t, liveKey).Default; got != "41" {
		t.Errorf("the registry gives %q after the change", got)
	}
	v, err := Get(in, liveKey)
	if err != nil || !v.IsStored || v.Set != "41" || v.InForce != "41" || v.Pending || v.Shipped != def {
		t.Errorf("the value is %+v, %v", v, err)
	}
	// Read again at the next start.
	settings.UseInstalled(nil)
	if err := Load(in); err != nil {
		t.Fatal(err)
	}
	if got := shipped(t, liveKey).Default; got != "41" {
		t.Errorf("after a start the registry gives %q", got)
	}
	if c, err = Clear(in, liveKey); err != nil || c.Old != "41" || c.New != def {
		t.Errorf("clearing gave %+v, %v", c, err)
	}
	if got := shipped(t, liveKey).Default; got != def {
		t.Errorf("after clearing the registry gives %q", got)
	}
}

func TestARestartSettingWaitsForTheNextStart(t *testing.T) {
	in := open(t)
	def := shipped(t, restartKey).Default
	c, err := Set(in, restartKey, "12345")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Restart || c.New != "12345" {
		t.Errorf("the change is %+v", c)
	}
	v, _ := Get(in, restartKey)
	if v.Set != "12345" || v.InForce != def || !v.Pending {
		t.Errorf("before a restart the value is %+v", v)
	}
	if got := shipped(t, restartKey).Default; got != def {
		t.Errorf("a restart setting took effect at once: %q", got)
	}
	// Another change to a setting that is read when used keeps the
	// restart setting where it started.
	if _, err := Set(in, liveKey, "33"); err != nil {
		t.Fatal(err)
	}
	if got := shipped(t, restartKey).Default; got != def {
		t.Errorf("a later change put the restart setting in force: %q", got)
	}
	// The next start.
	if err := Load(in); err != nil {
		t.Fatal(err)
	}
	if v, _ = Get(in, restartKey); v.InForce != "12345" || v.Pending {
		t.Errorf("after the start the value is %+v", v)
	}
}

func TestWhatCannotBeSetIsRefused(t *testing.T) {
	in := open(t)
	for key, want := range map[string]error{
		adminKey:         ErrNotAllowed,
		"rules.approval": ErrNotAllowed,
		"data.dir":       ErrNotAllowed,
		"no.such_key":    ErrUnknown,
	} {
		if _, err := Set(in, key, "1"); !errors.Is(err, want) {
			t.Errorf("setting %s gave %v, want %v", key, err, want)
		}
	}
	if _, err := Set(in, liveKey, "many"); !errors.Is(err, ErrBadValue) {
		t.Errorf("a value that is not a number gave %v", err)
	}
	if _, err := Set(in, personKey, "purple"); !errors.Is(err, ErrBadValue) {
		t.Errorf("a value that is not one of the options gave %v", err)
	}
	list, err := List(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range list {
		if v.Setting.Scope == settings.ScopeTenantAdmin || v.Setting.Key == "data.dir" || settings.IsFixedRule(v.Setting.Key) {
			t.Errorf("%s is listed as something the install sets", v.Setting.Key)
		}
	}
	if values, _ := in.InstallValues(tenant.SettingsField); len(values) != 0 {
		t.Errorf("refused changes reached keystone.json: %v", values)
	}
}

// A person's default: a new person starts with it, a person who has not
// chosen follows it, and a person's own choice stays theirs.
func TestAPersonsDefaultIsTheirsUntilTheyChoose(t *testing.T) {
	in := open(t)
	ctx := context.Background()
	chooser, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.Registry() // kept from before the change, as a server keeps it
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(chooser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, personKey, "light"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	if _, err := Set(in, personKey, "dark"); err != nil {
		t.Fatal(err)
	}
	if values, _ := in.InstallValues(tenant.PersonDefaultsField); values[personKey] != "dark" {
		t.Errorf("person_defaults holds %v", values)
	}
	newcomer, err := in.Create("Test Person Two")
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{chooser.ID: "light", newcomer.ID: "dark"} {
		s, err := in.Store(id)
		if err != nil {
			t.Fatal(err)
		}
		got, err := tenantsettings.Get(ctx, s, reg, personKey)
		all, _ := tenantsettings.All(ctx, s, reg)
		s.Close()
		if err != nil || got != want {
			t.Errorf("%s has %q, want %q (%v)", id, got, want, err)
		}
		for _, v := range all {
			if v.Setting.Key == personKey && (v.Value != want || v.Overridden != (id == chooser.ID)) {
				t.Errorf("%s's settings list %s as %+v", id, personKey, v)
			}
		}
	}
	// The admin cannot set a person's own setting for them.
	s, _ = in.Store(newcomer.ID)
	defer s.Close()
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Admin, personKey, "light"); !errors.Is(err, tenantsettings.ErrNotAllowed) {
		t.Errorf("the admin set a person's own setting: %v", err)
	}
}
