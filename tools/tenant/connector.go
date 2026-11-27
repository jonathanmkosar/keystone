package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/jonathanmkosar/keystone/internal/connectors"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// The connector commands (P7-04). A connector that runs a command on this
// machine is added here and nowhere else: the API refuses one, so nobody
// signed in through the browser can make Keystone run a program. Whoever
// runs this command already has a shell on the machine.

const connectorUsage = "tenant connector add <tenant-id> <file.json|->   (- reads the entry from standard input)\n" +
	"       tenant connector list <tenant-id>\n" +
	"       tenant connector check <tenant-id> <name>\n" +
	"       tenant connector remove <tenant-id> <name>\n" +
	"       tenant connector google <tenant-id> <account address> <scope> [<scope> ...]   (the refresh token is read from standard input)"

var errConnectorUsage = errors.New("usage: " + connectorUsage)

// connectorEntry is an entry as the file gives it: enabled and outside are
// true when it leaves them out.
type connectorEntry struct {
	connectors.Connector
	Enabled *bool `json:"enabled"`
	Outside *bool `json:"outside"`
}

// connectorMain runs "tenant connector ..." and exits.
func connectorMain(dir, keeper string, args []string) {
	in, err := openInstall(dir, keeper)
	if err == nil {
		err = runConnector(in, args, os.Stdin, os.Stdout)
	}
	if errors.Is(err, errConnectorUsage) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// connectorOptions reads the connectors' settings as shipped.
func connectorOptions() (connectors.Options, error) {
	reg, err := settings.Registry()
	if err != nil {
		return connectors.Options{}, err
	}
	return connectors.ReadOptions(func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	})
}

// runConnector adds, lists, checks or removes one tenant's connectors. An
// entry names the secret that holds its token and never holds the token,
// so nothing here can print one.
func runConnector(in *tenant.Install, args []string, stdin io.Reader, out io.Writer) error {
	if len(args) < 2 {
		return errConnectorUsage
	}
	command, id, rest := args[0], args[1], args[2:]
	wantArgs := map[string]int{"add": 1, "list": 0, "check": 1, "remove": 1}
	if n, ok := wantArgs[command]; command == "google" && len(rest) >= 2 {
		// P12-04: a Google sign-in made elsewhere on this machine, taken over.
	} else if !ok || len(rest) != n {
		return errConnectorUsage
	}
	o, err := connectorOptions()
	if err != nil {
		return err
	}
	s, err := in.Store(id)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx := context.Background()

	switch command {
	case "google":
		// The token comes on standard input, so it is in no command line,
		// and nothing here prints it.
		raw, err := io.ReadAll(io.LimitReader(stdin, 64<<10))
		if err != nil {
			return err
		}
		covered, err := connectors.AdoptGoogle(ctx, s, rest[0], rest[1:], string(raw), o.MaxTools)
		if err != nil {
			return err
		}
		names := make([]string, len(covered))
		for i, svc := range covered {
			names[i] = svc.Label()
		}
		fmt.Fprintf(out, "Took over the Google sign-in of %s for %s. Its token is in the vault as %s. Check it with: tenant connector check %s %s\n",
			rest[0], strings.Join(names, " and "), connectors.GoogleSecret, id, covered[0])
		return nil
	case "add":
		raw, err := readEntry(rest[0], stdin)
		if err != nil {
			return err
		}
		var e connectorEntry
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil {
			return errors.New("the entry is not a connector as JSON: docs/architecture/connectors.md has an example")
		}
		c := e.Connector
		c.Enabled, c.Outside = e.Enabled == nil || *e.Enabled, e.Outside == nil || *e.Outside
		k, err := connectors.Add(ctx, s, c, connectors.ByCommandLine, o.Max)
		if err != nil {
			if errors.Is(err, connectors.ErrExists) {
				return fmt.Errorf("%w. To change it, remove it first: tenant connector remove %s %s", err, id, c.Name)
			}
			return err
		}
		fmt.Fprintf(out, "Added connector %s for tenant %s, over %s.\n", k.Name, id, k.Transport)
		if k.Secret != "" {
			if held, err := holdsSecret(s, k.Secret); err != nil {
				return err
			} else if !held {
				fmt.Fprintf(out, "Its token is not in the vault yet. Store it, then check the connector: tenant secret set %s %s\n", id, k.Secret)
				return nil
			}
		}
		return printCheck(ctx, o, s, id, k.Name, out)
	case "check":
		return printCheck(ctx, o, s, id, rest[0], out)
	case "list":
		kept, err := connectors.List(ctx, s)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tTRANSPORT\tON\tOUTSIDE\tTOOLS\tTOKEN\tADDED BY\tWHERE")
		for _, k := range kept {
			token := "none"
			if k.Secret != "" {
				held, err := holdsSecret(s, k.Secret)
				if err != nil {
					return err
				}
				token = k.Secret
				if !held {
					token += " (not in the vault)"
				}
			}
			where := k.URL
			if k.Transport == connectors.TransportStdio {
				where = strings.TrimSpace(k.Command + " " + strings.Join(k.Args, " "))
			}
			fmt.Fprintf(w, "%s\t%s\t%v\t%v\t%d\t%s\t%s\t%s\n", k.Name, k.Transport, k.Enabled, k.Outside, len(k.Tools), token, k.AddedBy, where)
		}
		w.Flush()
		for _, k := range kept {
			if k.Problem != "" {
				fmt.Fprintf(out, "\n%s could not be reached when last tried: %s\n", k.Name, k.Problem)
			}
		}
		fmt.Fprintf(out, "\n%d connectors. A token is named, never shown.\n", len(kept))
	case "remove":
		if err := connectors.Remove(ctx, s, rest[0]); err != nil {
			if errors.Is(err, connectors.ErrNotFound) {
				return fmt.Errorf("tenant %s has no connector named %s", id, rest[0])
			}
			return err
		}
		fmt.Fprintf(out, "Removed connector %s for tenant %s, its switches in every chat and the policies for its tools. Its token, if it had one, is still in the vault.\n", rest[0], id)
	}
	return nil
}

// printCheck reaches a connector and says what it listed.
func printCheck(ctx context.Context, o connectors.Options, s *tenant.Store, id, name string, out io.Writer) error {
	k, err := o.Check(ctx, s, name)
	if err != nil {
		if errors.Is(err, connectors.ErrNotFound) {
			return fmt.Errorf("tenant %s has no connector named %s", id, name)
		}
		return err
	}
	if k.Problem != "" {
		fmt.Fprintf(out, "%s could not be reached: %s\n", name, k.Problem)
		return nil
	}
	fmt.Fprintf(out, "%s listed %d tools:\n", name, len(k.Tools))
	for _, t := range k.Tools {
		acts := "changes something, so it asks"
		if t.ReadOnly {
			acts = "only reads"
		}
		fmt.Fprintf(out, "  %s (%s)\n", t.Name, acts)
	}
	return nil
}
