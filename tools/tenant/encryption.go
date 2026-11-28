package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Encryption at rest (P15-06, docs/architecture/encryption.md). The server
// seals people when it starts; this command only reports, and reads the
// passphrase of a backup the server wrote before sealing someone, so it can
// be restored with tenant restore:
//
//	go run ./tools/tenant encryption passphrase <file> | go run ./tools/tenant restore --replace <file>
const encryptionUsage = "tenant encryption status | passphrase <backup file>   (the passphrase of a backup made before sealing, from beside it)"

var errEncryptionUsage = errors.New("usage: " + encryptionUsage)

func encryptionMain(in *tenant.Install, args []string) {
	if err := runEncryption(in, args, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errEncryptionUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func runEncryption(in *tenant.Install, args []string, out io.Writer) error {
	switch {
	case len(args) == 1 && args[0] == "status":
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tON DISK")
		plain := 0
		for _, t := range in.Tenants() {
			sealed, err := in.Sealed(t.ID)
			if err != nil {
				return err
			}
			state := "encrypted"
			if !sealed {
				state = "plain"
				plain++
			}
			fmt.Fprintf(w, "%s\t%s\n", t.ID, state)
		}
		w.Flush()
		if plain > 0 {
			fmt.Fprintf(out, "\n%d plain: the server encrypts them at its next start while encryption.at_rest is on.\n", plain)
		}
		return nil
	case len(args) == 2 && args[0] == "passphrase":
		pass, err := in.BackupPassphrase(args[1])
		if err != nil {
			return err
		}
		fmt.Fprintln(out, pass)
		return nil
	}
	return errEncryptionUsage
}
