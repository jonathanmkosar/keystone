// Command keystone is the Keystone binary: the server and its command line.
// So far it can serve the embedded web page and print the settings registry
// and its version.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/jonathanmkosar/keystone/settings"
)

// version is set at build time by the release build. A plain build says dev.
var version = "dev"

const usage = `Keystone: your own agent workspace.

Usage:
  keystone serve [--address <host:port>] [--data <folder>]
                                               serve the API and the web interface
  keystone settings [--json] [--lane <lane>]   print every setting, its default and who may change it
  keystone openapi [--out <file>]              print the API's OpenAPI document
  keystone version                             print the version
  keystone help                                print this help

As a client of a running server (each takes --server <address>):
  keystone people                              list the people who can sign in
  keystone signin <person-id>                  sign in; the password is read from standard input
  keystone whoami                              say who is signed in
  keystone signout                             sign out
  keystone api <METHOD> <path> [<json> | -]    make any API call and print the answer, a stream as it arrives
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run carries out one command and returns the exit code.
func run(args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return 0
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:], out, errw)
	case "settings":
		return runSettings(args[1:], out, errw)
	case "openapi":
		return runOpenAPI(args[1:], out, errw)
	case "people", "signin", "signout", "whoami", "api":
		return runClient(args[0], args[1:], os.Stdin, out, errw)
	case "version":
		fmt.Fprintln(out, "keystone", version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return 0
	}
	fmt.Fprintf(errw, "keystone: %q is not a command\n\n%s", args[0], usage)
	return 2
}

func runSettings(args []string, out, errw io.Writer) int {
	fl := flag.NewFlagSet("settings", flag.ContinueOnError)
	fl.SetOutput(errw)
	asJSON := fl.Bool("json", false, "print the registry as JSON")
	lane := fl.String("lane", "", "print one lane's settings only")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	reg, err := settings.Registry()
	if err != nil {
		fmt.Fprintln(errw, "keystone: the settings registry is broken:", err)
		return 1
	}
	if *lane != "" {
		kept := reg[:0:0]
		for _, s := range reg {
			if s.Lane == *lane {
				kept = append(kept, s)
			}
		}
		reg = kept
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(reg); err != nil {
			fmt.Fprintln(errw, "keystone:", err)
			return 1
		}
		return 0
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tLANE\tTYPE\tWHO MAY CHANGE IT\tDEFAULT\tDESCRIPTION")
	for _, s := range reg {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Key, s.Lane, s.Type, who(s.Scope), s.Default, s.Description)
	}
	w.Flush()
	fmt.Fprintf(out, "\n%d settings\n", len(reg))
	return 0
}

func who(s settings.Scope) string {
	switch s {
	case settings.ScopeInstall:
		return "admin, for the install"
	case settings.ScopeTenantAdmin:
		return "admin, for each person"
	case settings.ScopeTenant:
		return "each person"
	}
	return string(s)
}
