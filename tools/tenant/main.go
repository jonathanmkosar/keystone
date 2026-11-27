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
//
//	go run ./tools/tenant model add <tenant-id> entry.json
//	go run ./tools/tenant model list <tenant-id>
//	go run ./tools/tenant model remove <tenant-id> <name>
//
// model manages one tenant's model registry. An entry names the secret that
// holds its key and never holds the key. See model.go and
// docs/runbooks/adding-a-model.md.
//
//	Get-Clipboard | go run ./tools/tenant backup <tenant-id> <file>
//	Get-Clipboard | go run ./tools/tenant --data other/folder restore [--replace] [--check] <file>
//
// backup writes one tenant to one file sealed under a passphrase, read
// from standard input; restore brings it into a data folder, with its keys
// under that install's master key. See backup.go and
// docs/runbooks/backup-and-restore.md.
//
//	go run ./tools/tenant connector add <tenant-id> entry.json
//	go run ./tools/tenant connector list <tenant-id>
//	go run ./tools/tenant connector check <tenant-id> <name>
//	go run ./tools/tenant connector remove <tenant-id> <name>
//
// connector manages one tenant's connectors. A connector that runs a
// command on this machine is added here and never through the API. See
// connector.go and docs/architecture/connectors.md.
//
//	go run ./tools/tenant import-copy <workspace.sqlite> <new file>
//	go run ./tools/tenant import --check <tenant-id> <copy of workspace.sqlite>
//	go run ./tools/tenant import <tenant-id> <copy of workspace.sqlite>
//
// import brings one person's chats and routines from New Keystone into one
// tenant, and import-copy makes the whole copy it reads. See importcmd.go
// and docs/runbooks/import-new-keystone.md.
//
//	go run ./tools/tenant admin <tenant-id> on|off [--yes]
//
// admin gives a tenant the admin mark, or takes it away. It is the only way
// to: no route can. Taking it from the last admin needs --yes. See admin.go
// and docs/architecture/admin.md.
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
		fmt.Fprintln(os.Stderr, "       "+testUsage)
		fmt.Fprintln(os.Stderr, "       "+modelUsage)
		fmt.Fprintln(os.Stderr, "       "+usageUsage)
		fmt.Fprintln(os.Stderr, "       "+settingUsage)
		fmt.Fprintln(os.Stderr, "       "+backupUsage)
		fmt.Fprintln(os.Stderr, "       "+connectorUsage)
		fmt.Fprintln(os.Stderr, "       "+importUsage)
		fmt.Fprintln(os.Stderr, "       "+adminUsage)
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
		fmt.Fprintln(w, "ID\tNAME\tCREATED\tADMIN")
		for _, t := range in.Tenants() {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Name, t.Created.Format("2006-01-02 15:04"), map[bool]string{true: "yes", false: "no"}[t.Admin])
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
	case "test":
		testMain(in)
	case "model":
		modelMain(dir, *keeper, args[1:])
	case "usage":
		usageMain(in, args[1:])
	case "setting":
		settingMain(in, args[1:])
	case "backup":
		backupMain(dir, *keeper, args[1:])
	case "restore":
		restoreMain(dir, *keeper, args[1:])
	case "connector":
		connectorMain(dir, *keeper, args[1:]) // P7-04: connector.go
	case "import":
		importMain(in, args[1:]) // P12-03: importcmd.go
	case "import-copy":
		importCopyMain(args[1:])
	case "admin":
		adminMain(in, args[1:]) // P15-01: admin.go
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
