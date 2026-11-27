package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

const adminUsage = "tenant admin <tenant-id> on|off [--yes]   (the admin mark; --yes takes it from the last admin)"

var (
	errAdminUsage = errors.New("usage: " + adminUsage)
	// errLastAdmin is taking the mark from the install's last admin
	// without --yes.
	errLastAdmin = errors.New("the last admin keeps the mark unless --yes is given")
)

// adminMain runs "tenant admin ..." and exits.
func adminMain(in *tenant.Install, args []string) {
	err := runAdmin(in, args, os.Stdout)
	switch {
	case errors.Is(err, errAdminUsage):
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runAdmin gives a tenant the admin mark, or takes it away (P15-01,
// decision 0640). It is the only caller of tenant.Install.SetAdmin: no
// route can change a mark. Taking the mark from the last admin is refused,
// with a warning, unless --yes is given.
//
// A running server reads the mark from keystone.json at each admin call, so
// the change counts at once, with no restart (decision 0641).
func runAdmin(in *tenant.Install, args []string, out io.Writer) error {
	yes := false
	var rest []string
	for _, a := range args {
		if a == "--yes" || a == "-yes" {
			yes = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) != 2 || (rest[1] != "on" && rest[1] != "off") {
		return errAdminUsage
	}
	id, on := rest[0], rest[1] == "on"
	marks, err := in.Marks()
	if err != nil {
		return err
	}
	last := !on && marks[id] && len(marks) == 1
	if last && !yes {
		return fmt.Errorf("tenant %s is the install's last admin. Without one, no one can open the admin area until the command line marks another. Run the command again with --yes to take the mark all the same: %w", id, errLastAdmin)
	}
	t, err := in.SetAdmin(id, on)
	if err != nil {
		return err
	}
	if on {
		fmt.Fprintf(out, "Tenant %s (%s) is an admin. The admin area asks for their password again before it opens. A running server sees the mark at once.\n", t.ID, t.Name)
		return nil
	}
	fmt.Fprintf(out, "Tenant %s (%s) is not an admin. A running server refuses them the admin area from their next call.\n", t.ID, t.Name)
	if last {
		fmt.Fprintln(out, "Warning: the install has no admin now.")
	}
	return nil
}
