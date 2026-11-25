package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

const settingUsage = "tenant setting set <tenant-id> <key> <value>\n" +
	"       tenant setting clear <tenant-id> <key>\n" +
	"       tenant setting list <tenant-id>   (the values this tenant has of its own)"

var errSettingUsage = errors.New("usage: " + settingUsage)

// settingMain runs "tenant setting ..." and exits.
func settingMain(in *tenant.Install, args []string) {
	err := runSetting(in, args, os.Stdout)
	if errors.Is(err, errSettingUsage) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runSetting stores, clears or lists one tenant's own values for settings.
// Whoever runs this command has the data folder, so it stores a value as
// the role the setting's scope asks for: the person for a tenant setting,
// the admin for a tenant-admin one. An install setting has one value for
// everyone and is refused, and so is a fixed rule.
func runSetting(in *tenant.Install, args []string, out io.Writer) error {
	if len(args) < 2 {
		return errSettingUsage
	}
	command, id, rest := args[0], args[1], args[2:]
	wantArgs := map[string]int{"set": 2, "clear": 1, "list": 0}
	if n, ok := wantArgs[command]; !ok || len(rest) != n {
		return errSettingUsage
	}
	reg, err := settings.Registry()
	if err != nil {
		return err
	}
	s, err := in.Store(id)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx := context.Background()

	role := func(key string) tenantsettings.Role {
		if st, ok := settings.Lookup(reg, key); ok && st.Scope == settings.ScopeTenantAdmin {
			return tenantsettings.Admin
		}
		return tenantsettings.Person
	}
	switch command {
	case "set":
		key, value := rest[0], rest[1]
		if err := tenantsettings.Set(ctx, s, reg, role(key), key, value); err != nil {
			return err
		}
		fmt.Fprintf(out, "Set %s for tenant %s\n", key, id)
	case "clear":
		key := rest[0]
		if err := tenantsettings.Clear(ctx, s, reg, role(key), key); err != nil {
			return err
		}
		st, _ := settings.Lookup(reg, key)
		fmt.Fprintf(out, "Cleared %s for tenant %s. It is back to its default, %q\n", key, id, st.Default)
	case "list":
		all, err := tenantsettings.All(ctx, s, reg)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\tVALUE\tDEFAULT\tSET BY")
		n := 0
		for _, v := range all {
			if v.Overridden {
				n++
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", v.Setting.Key, v.Value, v.Setting.Default, v.SetBy)
			}
		}
		w.Flush()
		fmt.Fprintf(out, "\n%d settings of this tenant's own. Every other setting has its default.\n", n)
	}
	return nil
}
