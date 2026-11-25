package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

func TestSettingSetClearList(t *testing.T) {
	in, id := testInstall(t)
	var out bytes.Buffer
	run := func(args ...string) error { return runSetting(in, args, &out) }
	inForce := func(key string) string {
		t.Helper()
		reg, err := settings.Registry()
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(id)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		v, err := tenantsettings.Get(context.Background(), s, reg, key)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	// A tenant-admin setting and a tenant setting are each stored as the
	// role that may store it.
	if err := run("set", id, "usage.monthly_cap_usd", "25"); err != nil {
		t.Fatal(err)
	}
	if err := run("set", id, "routing.fallback_models", "house-small"); err != nil {
		t.Fatal(err)
	}
	if inForce("usage.monthly_cap_usd") != "25" || inForce("routing.fallback_models") != "house-small" {
		t.Errorf("the values in force are %q and %q", inForce("usage.monthly_cap_usd"), inForce("routing.fallback_models"))
	}
	out.Reset()
	if err := run("list", id); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "usage.monthly_cap_usd") || !strings.Contains(out.String(), "admin") || !strings.Contains(out.String(), "2 settings") {
		t.Errorf("list printed:\n%s", out.String())
	}
	if err := run("clear", id, "usage.monthly_cap_usd"); err != nil {
		t.Fatal(err)
	}
	if inForce("usage.monthly_cap_usd") != "100" {
		t.Errorf("after clear the cap is %q", inForce("usage.monthly_cap_usd"))
	}

	// What may not be set for one tenant is refused.
	if err := run("set", id, "usage.monthly_cap_usd", "a lot"); !errors.Is(err, tenantsettings.ErrBadValue) {
		t.Errorf("a cap that is not a number gave %v", err)
	}
	if err := run("set", id, "server.address", "127.0.0.1:1"); !errors.Is(err, tenantsettings.ErrNotAllowed) {
		t.Errorf("an install setting gave %v", err)
	}
	if err := run("set", id, "rules.sensitive_content", "false"); !errors.Is(err, tenantsettings.ErrNotAllowed) {
		t.Errorf("a fixed rule gave %v", err)
	}
	if err := run("set", id, "no.such_setting", "1"); !errors.Is(err, tenantsettings.ErrUnknown) {
		t.Errorf("an unknown setting gave %v", err)
	}
	for _, args := range [][]string{{}, {"set", id, "usage.monthly_cap_usd"}, {"list", id, "more"}, {"show", id}} {
		if err := run(args...); !errors.Is(err, errSettingUsage) {
			t.Errorf("%v gave %v, want the usage", args, err)
		}
	}
}
