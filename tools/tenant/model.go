package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/settings"
)

const modelUsage = "tenant model add <tenant-id> <file.json|->   (- reads the entry from standard input)\n" +
	"       tenant model list <tenant-id>\n" +
	"       tenant model remove <tenant-id> <name>"

var errModelUsage = errors.New("usage: " + modelUsage)

// maxModelInput is far more than an entry holds, so an entry that is too
// long is refused by the registry and not cut short here.
const maxModelInput = 1 << 20

// bom is the byte order mark PowerShell can put in front of what it pipes.
var bom = string(rune(0xFEFF))

// modelMain runs "tenant model ..." and exits. keeper is the --keeper
// flag: empty for the keeper the install already uses. The vault is opened
// only to say whether the secret an entry names is there.
func modelMain(dir, keeper string, args []string) {
	in, err := openInstall(dir, keeper)
	if err == nil {
		err = runModel(in, args, os.Stdin, os.Stdout)
	}
	if errors.Is(err, errModelUsage) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runModel adds, lists or removes the models in one tenant's registry. An
// entry names the secret that holds its key and never holds the key, so
// nothing here can print one. The vault is asked only for the names it
// holds.
func runModel(in *tenant.Install, args []string, stdin io.Reader, out io.Writer) error {
	if len(args) < 2 {
		return errModelUsage
	}
	command, id, rest := args[0], args[1], args[2:]
	wantArgs := map[string]int{"add": 1, "list": 0, "remove": 1}
	if n, ok := wantArgs[command]; !ok || len(rest) != n {
		return errModelUsage
	}
	s, err := in.Store(id)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx := context.Background()

	switch command {
	case "add":
		raw, err := readEntry(rest[0], stdin)
		if err != nil {
			return err
		}
		m, err := models.Decode(raw)
		if err != nil {
			return err
		}
		reg, err := settings.Registry()
		if err != nil {
			return err
		}
		lim, err := adapters.Limits(reg)
		if err != nil {
			return err
		}
		if err := models.Add(ctx, s, lim, m); err != nil {
			if errors.Is(err, models.ErrExists) {
				return fmt.Errorf("%w. To change it, remove it first: tenant model remove %s %s", err, id, m.Name)
			}
			return err
		}
		fmt.Fprintf(out, "Added model %s (%s) for tenant %s: the %s adapter, class %s\n", m.Name, m.Label, id, m.Adapter, m.Class)
		if m.Key != "" {
			if held, err := holdsSecret(s, m.Key); err != nil {
				return err
			} else if !held {
				fmt.Fprintf(out, "Its key is not in the vault yet. Store it before the model is used: tenant secret set %s %s\n", id, m.Key)
			}
		}
		if test, err := testtenant.IsTest(s); err == nil && test {
			fmt.Fprintln(out, "This is the test tenant. It always answers from its recorded turns and never reads its registry, so this model will not be used.")
		}
	case "list":
		entries, err := models.List(ctx, s)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tLABEL\tADAPTER\tCLASS\tWINDOW\tOUTPUT\tKEY\tENDPOINT")
		refused := 0
		for _, e := range entries {
			if e.Problem != "" {
				refused++
				fmt.Fprintf(w, "%s\t(refused)\t\t\t\t\t\t\n", e.Model.Name)
				continue
			}
			m := e.Model
			key := "none"
			if m.Key != "" {
				held, err := holdsSecret(s, m.Key)
				if err != nil {
					return err
				}
				key = m.Key
				if !held {
					key += " (not in the vault)"
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n", m.Name, m.Label, m.Adapter, m.Class, m.ContextWindow, m.OutputLimit, key, m.Endpoint)
		}
		w.Flush()
		for _, e := range entries {
			if e.Problem != "" {
				fmt.Fprintf(out, "\n%s is refused and will not be used: %s\n", e.Model.Name, e.Problem)
			}
		}
		fmt.Fprintf(out, "\n%d models", len(entries))
		if refused > 0 {
			fmt.Fprintf(out, ", %d refused", refused)
		}
		fmt.Fprintln(out, ". A key is named, never shown.")
	case "remove":
		if err := models.Remove(ctx, s, rest[0]); err != nil {
			if errors.Is(err, models.ErrUnknown) {
				return fmt.Errorf("tenant %s has no model named %s", id, rest[0])
			}
			return err
		}
		fmt.Fprintf(out, "Removed model %s for tenant %s. Its key, if it had one, is still in the vault.\n", rest[0], id)
	}
	return nil
}

// readEntry reads one entry from a file, or from standard input when the
// name is "-". A byte order mark, which PowerShell can add, is not part of
// the entry.
func readEntry(name string, stdin io.Reader) ([]byte, error) {
	var raw []byte
	var err error
	if name == "-" {
		raw, err = io.ReadAll(io.LimitReader(stdin, maxModelInput))
		if err != nil {
			return nil, errors.New("standard input could not be read")
		}
	} else {
		f, openErr := os.Open(name)
		if openErr != nil {
			return nil, fmt.Errorf("the entry file could not be read: %w", openErr)
		}
		defer f.Close()
		if raw, err = io.ReadAll(io.LimitReader(f, maxModelInput)); err != nil {
			return nil, fmt.Errorf("the entry file could not be read: %w", err)
		}
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(raw), bom))
	if text == "" {
		return nil, errors.New("the entry is empty: give one model entry as JSON. docs/runbooks/adding-a-model.md has examples")
	}
	return []byte(text), nil
}

// holdsSecret reports whether the tenant's vault has a secret of that
// name. The value is never read.
func holdsSecret(s *tenant.Store, name string) (bool, error) {
	names, err := s.Secrets().Names()
	if err != nil {
		return false, fmt.Errorf("the vault's names could not be read: %w", err)
	}
	return slices.Contains(names, name), nil
}
