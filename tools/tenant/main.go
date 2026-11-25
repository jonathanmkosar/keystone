// Command tenant adds and lists the tenants of a data folder.
//
//	go run ./tools/tenant add "Test Person One"
//	go run ./tools/tenant list
//	go run ./tools/tenant --data some/folder list
//	go run ./tools/tenant password <tenant-id>
//
// password gives a tenant a password, or replaces it. The password is read
// from standard input, one line, and never from an argument.
//
//	Get-Clipboard | go run ./tools/tenant secret set <tenant-id> provider.name
//	go run ./tools/tenant secret list <tenant-id>
//	go run ./tools/tenant secret delete <tenant-id> provider.name
//
// The data folder is the data.dir setting unless --data names another.
// A relative folder is read from the repo root.
//
// secret set reads the value from standard input, never from an argument,
// and no command prints a value. See secret.go.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

func main() {
	fl := flag.NewFlagSet("tenant", flag.ExitOnError)
	data := fl.String("data", "", "the data folder; the data.dir setting unless given")
	keeper := fl.String("keeper", "", "for secret: where the install master key is kept, system or file; the keeper the install already uses unless given")
	fl.Parse(os.Args[1:])
	args := fl.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tenant [--data <folder>] add <name> | list")
		fmt.Fprintln(os.Stderr, "       tenant [--data <folder>] password <tenant-id>   (the password is read from standard input)")
		fmt.Fprintln(os.Stderr, "       "+secretUsage)
		os.Exit(2)
	}
	dir, err := dataDir(*data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	in, err := tenant.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: tenant add <name>")
			os.Exit(2)
		}
		t, err := in.Create(strings.Join(args[1:], " "))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("Added tenant %s (%s)\n", t.ID, t.Name)
	case "list":
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tCREATED")
		for _, t := range in.Tenants() {
			fmt.Fprintf(w, "%s\t%s\t%s\n", t.ID, t.Name, t.Created.Format("2006-01-02 15:04"))
		}
		w.Flush()
		fmt.Printf("\n%d tenants\n", len(in.Tenants()))
	case "password":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: tenant password <tenant-id>   (the password is read from standard input)")
			os.Exit(2)
		}
		if st, err := os.Stdin.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			fmt.Fprintf(os.Stderr, "Type the new password for tenant %s and press Enter. It shows on the screen as you type.\n", args[1])
		}
		if err := setPassword(in, args[1], os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("Password set for tenant %s. Every session it had is ended.\n", args[1])
	case "secret":
		secretMain(dir, *keeper, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "tenant: %q is not a command\n", args[0])
		os.Exit(2)
	}
}

// dataDir resolves the data folder: the flag, or the data.dir setting's
// default, read from the repo root when relative.
func dataDir(flagValue string) (string, error) {
	dir := flagValue
	if dir == "" {
		reg, err := settings.Registry()
		if err != nil {
			return "", err
		}
		s, ok := settings.Lookup(reg, "data.dir")
		if !ok {
			return "", fmt.Errorf("the data.dir setting is missing from the registry")
		}
		dir = s.Default
	}
	if filepath.IsAbs(dir) {
		return dir, nil
	}
	root, err := project.Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, dir), nil
}

// setPassword reads one line from r and makes it the tenant's password,
// under the shipped sign-in settings. The password comes from standard
// input and never from an argument, so it is not left in the shell's
// history or shown in a list of running commands. Only the line ending is
// taken off: spaces are part of the password.
func setPassword(in *tenant.Install, id string, r io.Reader) error {
	s, err := in.Store(id)
	if err != nil {
		return err
	}
	defer s.Close()
	o, err := signin.Defaults()
	if err != nil {
		return err
	}
	line, err := bufio.NewReader(io.LimitReader(r, 1<<16)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	password := strings.TrimRight(strings.TrimPrefix(line, "\ufeff"), "\r\n")
	if password == "" {
		return errors.New("no password was given on standard input")
	}
	return signin.SetPassword(s, password, o)
}
