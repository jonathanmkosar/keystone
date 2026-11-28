package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// The admin sets a person's router policy from the command line until the
// admin's settings screen (P15-03): both settings are stored as the admin's.
func TestTheRouterPolicyIsSetAsTheAdmin(t *testing.T) {
	in, id := testInstall(t)
	var out bytes.Buffer
	for key, value := range map[string]string{"router.allowed_models": "house-small, house-medium", "router.may_switch_off": "false"} {
		if err := runSetting(in, []string{"set", id, key, value}, &out); err != nil {
			t.Fatalf("setting %s: %v", key, err)
		}
	}
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	all, err := tenantsettings.All(context.Background(), s, reg)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, v := range all {
		switch v.Setting.Key {
		case "router.allowed_models", "router.may_switch_off":
			seen++
			if !v.Overridden || v.SetBy != "admin" {
				t.Errorf("%s is %q, set by %q", v.Setting.Key, v.Value, v.SetBy)
			}
		}
	}
	if seen != 2 {
		t.Errorf("%d of the two settings are listed", seen)
	}
	// A person may not set either for themselves.
	if err := tenantsettings.Set(context.Background(), s, reg, tenantsettings.Person, "router.may_switch_off", "true"); err == nil {
		t.Error("a person set router.may_switch_off for themselves")
	}
}
