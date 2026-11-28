// Command admin makes and removes the admin accounts of a data folder
// (P15-01, decision 0650). An admin is a separate account, kept in
// data/admin/admin.db, that shows only admin controls; it has no chats, no
// files and no models. This command is the only way to make or remove one:
// no route can.
//
//	go run ./tools/admin add "Admin Name"
//	go run ./tools/admin password <account-id>
//	go run ./tools/admin list
//	go run ./tools/admin remove <account-id> [--yes]
//	go run ./tools/admin --data some/folder list
//
// password gives an account a password, or replaces it, and ends every
// session the account had. The password is read from standard input, one
// line, and never from an argument. remove ends the account's sessions at
// once; removing the last account is refused, with a warning, unless --yes
// is given. Sign in at /admin/sign-in.
//
// The data folder is the data.dir setting unless --data names another. A
// relative folder is read from the repo root.
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

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/settings"
)

const usage = `usage: admin [--data <folder>] add <name>
       admin [--data <folder>] password <account-id>   (the password is read from standard input)
       admin [--data <folder>] list
       admin [--data <folder>] remove <account-id> [--yes]   (--yes removes the last account)`

var (
	errUsage = errors.New(usage)
	// errLastAccount is removing the install's last admin account without
	// --yes.
	errLastAccount = errors.New("that is the last admin account: with none, nobody can sign in at /admin/sign-in until one is added again. Add --yes to remove it anyway")
)

func main() {
	fl := flag.NewFlagSet("admin", flag.ExitOnError)
	data := fl.String("data", "", "the data folder; the data.dir setting unless given")
	fl.Parse(os.Args[1:])
	dir, err := dataDir(*data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if fl.NArg() >= 2 && fl.Arg(0) == "password" {
		if st, err := os.Stdin.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			fmt.Fprintf(os.Stderr, "Type the new password for admin account %s and press Enter. It shows on the screen as you type.\n", fl.Arg(1))
		}
	}
	err = run(dir, fl.Args(), os.Stdin, os.Stdout)
	switch {
	case errors.Is(err, errUsage):
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run runs one command on the admin database of the data folder dir.
func run(dir string, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}
	db, err := adminaccount.Open(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	switch args[0] {
	case "add":
		if len(args) < 2 {
			return errUsage
		}
		a, err := db.AddAccount(strings.Join(args[1:], " "))
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Added admin account %s (%s). Give it a password with: go run ./tools/admin password %s\n", a.ID, a.Name, a.ID)
	case "password":
		if len(args) != 2 {
			return errUsage
		}
		o, err := signin.Defaults()
		if err != nil {
			return err
		}
		password, err := readLine(in)
		if err != nil {
			return err
		}
		if err := db.SetPassword(args[1], password, o); err != nil {
			return err
		}
		fmt.Fprintf(out, "Password set for admin account %s. Every session it had is ended.\n", args[1])
	case "list":
		if len(args) != 1 {
			return errUsage
		}
		list, err := db.Accounts()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tADDED\tPASSWORD\tLAST SIGN-IN")
		for _, a := range list {
			has, err := db.HasPassword(a.ID)
			if err != nil {
				return err
			}
			last := "never"
			if !a.LastSignIn.IsZero() {
				last = a.LastSignIn.Format("2006-01-02 15:04")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.ID, a.Name, a.Added.Format("2006-01-02 15:04"), map[bool]string{true: "yes", false: "no"}[has], last)
		}
		w.Flush()
		fmt.Fprintf(out, "\n%d admin accounts\n", len(list))
	case "remove":
		var id string
		yes := false
		for _, a := range args[1:] {
			if a == "--yes" {
				yes = true
			} else if id == "" {
				id = a
			} else {
				return errUsage
			}
		}
		if id == "" {
			return errUsage
		}
		a, err := db.Account(id)
		if err != nil {
			return err
		}
		list, err := db.Accounts()
		if err != nil {
			return err
		}
		if len(list) == 1 && !yes {
			return errLastAccount
		}
		if err := db.RemoveAccount(id); err != nil {
			return err
		}
		fmt.Fprintf(out, "Removed admin account %s (%s). Every session it had is ended; its entries in the admin audit log stay.\n", a.ID, a.Name)
	default:
		return fmt.Errorf("admin: %q is not a command\n%w", args[0], errUsage)
	}
	return nil
}

// readLine reads the password: one line, with only the line ending taken
// off, so spaces are part of it. It comes from standard input and never
// from an argument, so it is not left in the shell's history.
func readLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(r, 1<<16)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	password := strings.TrimRight(strings.TrimPrefix(line, string(rune(0xFEFF))), "\r\n")
	if password == "" {
		return "", errors.New("no password was given on standard input")
	}
	return password, nil
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
