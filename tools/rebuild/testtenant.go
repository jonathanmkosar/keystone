package main

import (
	"fmt"
	"path/filepath"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// testTenant loads the test tenant into the data folder: an invented
// person with a known invented password, a few chats, and the fake provider
// for a model. Run again, it finds the same tenant and changes nothing.
// The test_tenant.load setting switches the step off.
//
// The use cases are not run here, against a tenant someone may have been
// using: verify runs them in a throwaway data folder.
func testTenant(root string) (string, error) {
	reg, err := settings.Registry()
	if err != nil {
		return "", err
	}
	load, ok := settings.Lookup(reg, "test_tenant.load")
	if !ok {
		return "", fmt.Errorf("the test_tenant.load setting is missing from the registry")
	}
	if load.Default != "true" {
		return "not loaded: test_tenant.load is off", nil
	}
	data, ok := settings.Lookup(reg, "data.dir")
	if !ok {
		return "", fmt.Errorf("the data.dir setting is missing from the registry")
	}
	dir := data.Default
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	in, err := tenant.Open(dir)
	if err != nil {
		return "", err
	}
	fx, err := testtenant.Shipped()
	if err != nil {
		return "", err
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		return "", err
	}
	res, err := testtenant.Load(in, fx, o)
	if err != nil {
		return "", err
	}
	did := "found as it was"
	switch {
	case res.Created:
		did = "made"
	case res.PasswordSet || res.ChatsMade > 0:
		did = "brought back to its fixtures"
	}
	return fmt.Sprintf("%s (%s), %s, %d chats; the password is in fixtures/test-tenant/tenant.json",
		res.Tenant.Name, res.Tenant.ID, did, len(fx.Chats)), nil
}
