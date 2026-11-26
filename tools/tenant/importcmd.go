package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jonathanmkosar/keystone/internal/importer"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// import brings one person's chats and routines from New Keystone, the
// owner's earlier app, into one tenant (P12-03), and import-copy makes the
// whole copy of New Keystone's database that import reads. See
// docs/runbooks/import-new-keystone.md and docs/architecture/import.md.
//
// Neither prints a title, a name or a word of a message: counts, model
// names, table names and dates only.

const importUsage = "tenant import [--check] [--workspace <name>] <tenant-id> <copy of workspace.sqlite>\n" +
	"       tenant import-copy <workspace.sqlite> <new file>   (a whole copy of a database that may be in use)"

var errImportUsage = errors.New("usage: " + importUsage)

// importMain runs "tenant import ..." and exits.
func importMain(in *tenant.Install, args []string) {
	exitOnImport(runImport(in, args, os.Stdout))
}

// importCopyMain runs "tenant import-copy ..." and exits.
func importCopyMain(args []string) {
	exitOnImport(runImportCopy(args, os.Stdout))
}

func exitOnImport(err error) {
	if errors.Is(err, errImportUsage) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runImport brings a copy of New Keystone's database into one tenant, or
// with --check says what it would bring and writes nothing.
func runImport(in *tenant.Install, args []string, out io.Writer) error {
	fl := flag.NewFlagSet("import", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	check := fl.Bool("check", false, "say what would be imported and write nothing")
	workspace := fl.String("workspace", "", "the workspace of the source to read, when it holds several")
	if err := fl.Parse(args); err != nil || fl.NArg() != 2 {
		return errImportUsage
	}
	id, file := fl.Arg(0), fl.Arg(1)
	src, err := importer.OpenSource(file)
	if err != nil {
		return err
	}
	defer src.Close()
	s, err := in.Store(id)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx := context.Background()
	if *check {
		report, err := importer.Check(ctx, s, src, *workspace, time.Now())
		if err != nil {
			return err
		}
		report.Write(out)
		return nil
	}
	report, err := importer.Run(ctx, s, src, *workspace, time.Now())
	if err != nil {
		if report.Chats > 0 || report.Schedules > 0 {
			fmt.Fprintf(out, "The import stopped early. %d chats and %d schedules were written whole and are kept; run it again to bring the rest.\n", report.Chats, report.Schedules)
		}
		return err
	}
	report.Write(out)
	if report.Schedules > 0 {
		fmt.Fprintln(out, "Every schedule is paused. Switch one on from the Scheduled screen when it should run here.")
	}
	return nil
}

// runImportCopy makes a whole copy of a New Keystone database, which may
// be in use, for import to read.
func runImportCopy(args []string, out io.Writer) error {
	if len(args) != 2 {
		return errImportUsage
	}
	if err := importer.Copy(args[0], args[1]); err != nil {
		return err
	}
	fmt.Fprintf(out, "Copied to %s: one whole file, read back and sound. The database it was copied from was only read.\n", args[1])
	return nil
}
