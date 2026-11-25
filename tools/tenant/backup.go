package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// backup and restore move one tenant to a file and back, under a
// passphrase read from standard input (docs/runbooks/backup-and-restore.md).

const backupUsage = "tenant backup <tenant-id> <file>   (the passphrase is read from standard input)\n" +
	"       tenant restore [--replace] [--check] <file>   (the passphrase is read from standard input)"

var errBackupUsage = errors.New("usage: " + backupUsage)

// backupMain runs "tenant backup ..." and exits.
func backupMain(dir, keeper string, args []string) {
	in, err := openInstall(dir, keeper)
	var o tenant.BackupOptions
	if err == nil {
		o, err = backupOptions()
	}
	if err == nil {
		err = runBackup(in, o, args, os.Stdin, isTerminal(os.Stdin), os.Stderr, os.Stdout)
	}
	exitOn(err)
}

// restoreMain runs "tenant restore ..." and exits. The install is opened
// with the keeper it already uses, or the one secrets.keeper names for a
// new install, which then wraps the restored tenant's data key.
func restoreMain(dir, keeper string, args []string) {
	in, err := openInstall(dir, keeper)
	if err == nil {
		err = runRestore(in, args, os.Stdin, isTerminal(os.Stdin), os.Stderr, os.Stdout)
	}
	exitOn(err)
}

func exitOn(err error) {
	if errors.Is(err, errBackupUsage) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// backupOptions reads the backup settings. The tenant package reads no
// setting, so the command reads them and hands them over.
func backupOptions() (tenant.BackupOptions, error) {
	reg, err := settings.Registry()
	if err != nil {
		return tenant.BackupOptions{}, err
	}
	read := func(key string) (int, error) {
		s, ok := settings.Lookup(reg, key)
		if !ok {
			return 0, fmt.Errorf("the %s setting is missing from the registry", key)
		}
		n, err := strconv.Atoi(s.Default)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", key, err)
		}
		return n, nil
	}
	var o tenant.BackupOptions
	if o.Rounds, err = read("backup.passphrase_rounds"); err != nil {
		return o, err
	}
	o.MinPassphrase, err = read("backup.passphrase_min_length")
	return o, err
}

// runBackup writes one tenant to a new file. The file is written beside
// its name and renamed into place only when it is whole and has been read
// back with the passphrase, so a backup that fails leaves no file.
func runBackup(in *tenant.Install, o tenant.BackupOptions, args []string, stdin io.Reader, typed bool, prompt, out io.Writer) error {
	if len(args) != 2 {
		return errBackupUsage
	}
	id, file := args[0], args[1]
	if _, err := os.Lstat(file); err == nil {
		return fmt.Errorf("%s is already there, and a backup never replaces a file: give it a new name", file)
	}
	passphrase, err := readPassphrase(stdin, typed, true, prompt)
	if err != nil {
		return err
	}
	partial := file + ".partial"
	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			f.Close()
			os.Remove(partial) // ours alone: O_EXCL above made it
		}
	}()
	info, err := in.Backup(id, f, passphrase, o)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := checkFile(partial, passphrase); err != nil {
		return fmt.Errorf("the backup was written and does not read back, so it was removed: %w", err)
	}
	if err := os.Rename(partial, file); err != nil {
		return err
	}
	done = true
	fmt.Fprintf(out, "Backed up tenant %s (%s) to %s: %d files, %s before compression, %d secrets.\n",
		info.Tenant.ID, info.Tenant.Name, file, info.Files, size(info.Bytes), info.Secrets)
	fmt.Fprintln(out, "It was read back with the passphrase and is whole. Keep the passphrase: without it the backup cannot be opened.")
	return nil
}

func checkFile(path, passphrase string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = tenant.CheckBackup(f, passphrase)
	return err
}

// runRestore brings the tenant in a backup into the install, or with
// --check only reads the backup through and says what it holds.
func runRestore(in *tenant.Install, args []string, stdin io.Reader, typed bool, prompt, out io.Writer) error {
	fl := flag.NewFlagSet("restore", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	replace := fl.Bool("replace", false, "put the backup in the place of the tenant with its id")
	check := fl.Bool("check", false, "read the backup through and write nothing")
	if err := fl.Parse(args); err != nil || fl.NArg() != 1 || (*replace && *check) {
		return errBackupUsage
	}
	file := fl.Arg(0)
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	passphrase, err := readPassphrase(stdin, typed, false, prompt)
	if err != nil {
		return err
	}
	if *check {
		info, err := tenant.CheckBackup(f, passphrase)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s opens and is whole: tenant %s (%s), made %s, %d files, %s before compression, %d secrets. Nothing was written.\n",
			file, info.Tenant.ID, info.Tenant.Name, info.MadeAt.Format(time.RFC3339), info.Files, size(info.Bytes), info.Secrets)
		return nil
	}
	r, err := in.Restore(f, passphrase, tenant.RestoreOptions{Replace: *replace, Ready: signin.SignOutAll})
	if errors.Is(err, tenant.ErrTenantExists) {
		return fmt.Errorf("%w. To put the backup in its place, run restore --replace", err)
	}
	if err != nil {
		return err
	}
	verb := "Restored"
	if r.Replaced {
		verb = "Restored, in the place of the tenant that was there,"
	}
	fmt.Fprintf(out, "%s tenant %s (%s) from a backup made %s: %d files, %d secrets, now under this install's master key.\n",
		verb, r.Tenant.ID, r.Tenant.Name, r.MadeAt.Format(time.RFC3339), r.Files, r.Secrets)
	fmt.Fprintln(out, "Every session it had is ended: sign in again with the password it had when the backup was made.")
	if r.Leftover != "" {
		fmt.Fprintf(out, "The replaced tenant's old folder could not be removed: %s. Remove it once nothing has it open.\n", r.Leftover)
	}
	return nil
}

// readPassphrase reads the passphrase from standard input, one line, and
// never from an argument, so it is not left in the shell's history. Only
// the line ending, and a byte order mark PowerShell can put in front, are
// taken off: spaces are part of it. Typed at the keyboard it shows on the
// screen, and a backup asks for it twice.
func readPassphrase(stdin io.Reader, typed, twice bool, prompt io.Writer) (string, error) {
	r := bufio.NewReader(io.LimitReader(stdin, 1<<16))
	line := func() (string, error) {
		s, err := r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", errors.New("standard input could not be read")
		}
		return strings.TrimRight(strings.TrimPrefix(s, bom), "\r\n"), nil
	}
	if typed {
		fmt.Fprintln(prompt, "Type the backup's passphrase and press Enter. It shows on the screen as you type.")
	}
	p, err := line()
	if err != nil {
		return "", err
	}
	if p == "" {
		return "", errors.New("no passphrase was given on standard input")
	}
	if typed && twice {
		fmt.Fprintln(prompt, "Type it again.")
		again, err := line()
		if err != nil {
			return "", err
		}
		if again != p {
			return "", errors.New("the two passphrases differ, so nothing was written")
		}
	}
	return p, nil
}

// size writes a byte count for a person to read.
func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
