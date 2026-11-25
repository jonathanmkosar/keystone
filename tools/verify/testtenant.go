package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
)

// checkTestTenant is the standing check "test tenant": every use case in
// the test tenant produces its expected result.
//
// It reads the fixtures from the repo, loads the test tenant into a
// throwaway data folder beside an ordinary tenant, with the shipped
// settings, and proves:
//
//   - every JSON file under fixtures/ says it is test data;
//   - the fixtures load, and a second load changes nothing;
//   - the person signs in with the fixture's password and with no other;
//   - the test tenant is the one tenant marked as test data;
//   - every use case gives its expected result.
//
// An item that adds a use case adds an entry under
// fixtures/test-tenant/cases, and it is run here with no change.
func checkTestTenant(root string) error {
	problems, err := unmarkedFixtures(filepath.Join(root, "fixtures"))
	if err != nil {
		return err
	}
	fx, err := testtenant.Read(os.DirFS(filepath.Join(root, "fixtures", "test-tenant")))
	if err != nil {
		return fmt.Errorf("the fixtures do not load: %w\n%s", err, strings.Join(problems, "\n"))
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "keystone-test-tenant-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.Open(dir)
	if err != nil {
		return err
	}
	person, err := in.Create("Ordinary Person")
	if err != nil {
		return err
	}

	first, err := testtenant.Load(in, fx, o)
	if err != nil {
		return fmt.Errorf("the test tenant does not load: %w", err)
	}
	if !first.Created || first.ChatsMade != len(fx.Chats) {
		problems = append(problems, fmt.Sprintf("the first load made %d of %d chats, and made the tenant: %v", first.ChatsMade, len(fx.Chats), first.Created))
	}
	second, err := testtenant.Load(in, fx, o)
	if err != nil {
		return fmt.Errorf("the test tenant does not load a second time: %w", err)
	}
	if second.Created || second.PasswordSet || second.ChatsMade != 0 || second.Tenant != first.Tenant {
		problems = append(problems, "a second load changed the test tenant")
	}
	if n := len(in.Tenants()); n != 2 {
		problems = append(problems, fmt.Sprintf("two loads beside one person left %d tenants, and 2 were expected", n))
	}

	s, err := in.Store(first.Tenant.ID)
	if err != nil {
		return err
	}
	defer s.Close()
	chats, err := chat.List(s, chat.Filter{})
	if err != nil {
		return err
	}
	if len(chats) != len(fx.Chats) {
		problems = append(problems, fmt.Sprintf("the test tenant has %d chats after two loads, and %d were expected", len(chats), len(fx.Chats)))
	}

	// Sign-in: the wrong password first, so the count of wrong tries is
	// seen to clear.
	if _, _, err := signin.SignIn(s, fx.Password+" no", o.SignIn); !errors.Is(err, signin.ErrRefused) {
		problems = append(problems, fmt.Sprintf("a wrong password for the test tenant gave %v, and a refusal was expected", err))
	}
	token, _, err := signin.SignIn(s, fx.Password, o.SignIn)
	if err != nil {
		problems = append(problems, fmt.Sprintf("the fixture's password does not sign in to the test tenant: %v", err))
	} else if got, _, err := signin.Resolve(in, token); err != nil {
		problems = append(problems, fmt.Sprintf("the test tenant's session does not resolve: %v", err))
	} else {
		if got.TenantID() != first.Tenant.ID {
			problems = append(problems, "the test tenant's session resolved to another tenant")
		}
		got.Close()
	}

	// The mark.
	if found, ok, err := testtenant.Find(in); err != nil {
		return err
	} else if !ok || found.ID != first.Tenant.ID {
		problems = append(problems, "Find did not return the test tenant")
	}
	ps, err := in.Store(person.ID)
	if err != nil {
		return err
	}
	defer ps.Close()
	if is, err := testtenant.IsTest(ps); err != nil {
		return err
	} else if is {
		problems = append(problems, "an ordinary tenant reads as the test tenant")
	}
	if has, err := signin.HasPassword(ps); err != nil {
		return err
	} else if has {
		problems = append(problems, "loading the test tenant gave an ordinary tenant a password")
	}

	// The use cases.
	failed, err := testtenant.RunCases(s, fx, o.Chat)
	if err != nil {
		return err
	}
	if len(fx.Cases) == 0 {
		problems = append(problems, "the test tenant has no use cases")
	}
	problems = append(problems, failed...)

	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// unmarkedFixtures lists every JSON file under the fixtures folder that
// does not say it is test data.
func unmarkedFixtures(dir string) ([]string, error) {
	var problems []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || string(bytes.TrimSpace(fields["test_data"])) != "true" {
			rel, _ := filepath.Rel(filepath.Dir(dir), p)
			problems = append(problems, filepath.ToSlash(rel)+` is not marked as test data: it needs "test_data": true`)
		}
		return nil
	})
	return problems, err
}
