// Command tenant adds and lists the tenants of a data folder.
//
//	go run ./tools/tenant add "Test Person One"
//	go run ./tools/tenant list
//	go run ./tools/tenant --data some/folder list
//
// The data folder is the data.dir setting unless --data names another.
// A relative folder is read from the repo root.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

func main() {
	fl := flag.NewFlagSet("tenant", flag.ExitOnError)
	data := fl.String("data", "", "the data folder; the data.dir setting unless given")
	fl.Parse(os.Args[1:])
	args := fl.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tenant [--data <folder>] add <name> | list")
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
