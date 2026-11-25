package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

const secretUsage = "tenant secret set <tenant-id> <name>     (the value is read from standard input)\n" +
	"       tenant secret list <tenant-id>\n" +
	"       tenant secret delete <tenant-id> <name>"

var errSecretUsage = errors.New("usage: " + secretUsage)

// maxSecretInput is more than any value the vault takes, so a value that
// is too long is refused by the vault and not cut short here.
const maxSecretInput = 1 << 20

// secretMain runs "tenant secret ..." and exits. keeper is the --keeper
// flag: empty for the keeper the install already uses.
func secretMain(dir, keeper string, args []string) {
	in, err := openInstall(dir, keeper)
	if err == nil {
		err = runSecret(in, args, os.Stdin, isTerminal(os.Stdin), os.Stdout)
	}
	if errors.Is(err, errSecretUsage) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func openInstall(dir, keeper string) (*tenant.Install, error) {
	if keeper == "" {
		return tenant.Open(dir)
	}
	k, err := secrets.Named(keeper)
	if err != nil {
		return nil, err
	}
	return tenant.OpenWithKeeper(dir, k)
}

// runSecret stores, lists or deletes one tenant's secrets. It never prints
// a value, and it takes one only from standard input: an argument would
// stay in the shell's history and show in the list of running programs.
func runSecret(in *tenant.Install, args []string, stdin io.Reader, typed bool, out io.Writer) error {
	if len(args) < 2 {
		return errSecretUsage
	}
	command, id, rest := args[0], args[1], args[2:]
	wantNames := map[string]int{"set": 1, "list": 0, "delete": 1}
	if n, ok := wantNames[command]; !ok || len(rest) != n {
		return errSecretUsage
	}
	s, err := in.Store(id)
	if err != nil {
		return err
	}
	defer s.Close()
	vault := s.Secrets()

	switch command {
	case "set":
		name := rest[0]
		if typed {
			return errors.New("the value is read from standard input, and standard input is the keyboard, which would show it as it is typed. Pipe it in, for example: Get-Clipboard | go run ./tools/tenant secret set <tenant-id> <name>")
		}
		raw, err := io.ReadAll(io.LimitReader(stdin, maxSecretInput))
		if err != nil {
			return errors.New("standard input could not be read")
		}
		// A shell adds a line ending to what it pipes, and PowerShell can
		// add a byte order mark in front. Neither is part of the value.
		value := strings.TrimRight(strings.TrimPrefix(string(raw), "\ufeff"), "\r\n")
		if value == "" {
			return errors.New("standard input was empty, so nothing was stored")
		}
		if err := vault.Set(name, value); err != nil {
			return err
		}
		fmt.Fprintf(out, "Stored %s for tenant %s (%d bytes)\n", name, id, len(value))
	case "list":
		names, err := vault.Names()
		if err != nil {
			return err
		}
		for _, name := range names {
			fmt.Fprintln(out, name)
		}
		fmt.Fprintf(out, "\n%d secrets, values not shown\n", len(names))
	case "delete":
		name := rest[0]
		names, err := vault.Names()
		if err != nil {
			return err
		}
		if !slices.Contains(names, name) {
			return fmt.Errorf("tenant %s has no secret named %s", id, name)
		}
		if err := vault.Delete(name); err != nil {
			return err
		}
		fmt.Fprintf(out, "Deleted %s for tenant %s\n", name, id)
	}
	return nil
}

// isTerminal reports whether f is a keyboard or another device, not a pipe
// or a file.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
