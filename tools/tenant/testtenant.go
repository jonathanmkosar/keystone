package main

import (
	"fmt"
	"os"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
)

const testUsage = "tenant [--data <folder>] test   (loads the test tenant, or brings it back to its fixtures)"

// testMain loads the test tenant into the data folder, as rebuild does, and
// says which tenant it is. It can be run twice. It does not read the
// test_tenant.load setting: someone who runs this command has asked for it.
func testMain(in *tenant.Install) {
	res, err := loadTestTenant(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch {
	case res.Created:
		fmt.Printf("Made the test tenant %s (%s) with %d chats.\n", res.Tenant.ID, res.Tenant.Name, res.ChatsMade)
	case res.PasswordSet || res.ChatsMade > 0:
		fmt.Printf("The test tenant %s (%s) is back to its fixtures.\n", res.Tenant.ID, res.Tenant.Name)
	default:
		fmt.Printf("The test tenant %s (%s) was already loaded. Nothing changed.\n", res.Tenant.ID, res.Tenant.Name)
	}
	fmt.Println("Its password is in fixtures/test-tenant/tenant.json. It is invented, published, and guards nothing.")
}

// loadTestTenant loads the shipped fixtures under the shipped settings.
func loadTestTenant(in *tenant.Install) (testtenant.Result, error) {
	fx, err := testtenant.Shipped()
	if err != nil {
		return testtenant.Result{}, err
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		return testtenant.Result{}, err
	}
	return testtenant.Load(in, fx, o)
}
