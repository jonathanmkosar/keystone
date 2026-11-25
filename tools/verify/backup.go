package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/backup"
	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The backup surface of "no keys in output", added with P13-04. A backup
// carries a tenant's secrets to another install, so the stored value is
// looked for in the backup file, in everything a backup and a restore
// print or return, and in both data folders. The restore must also bring
// the value back under the second install's own master key, or the check
// proves nothing.

// backupPassphrase is invented. It is no secret: it seals invented data in
// a throwaway folder.
const backupPassphrase = "an invented passphrase for the verify check"

func keysInBackups(root, dir, value string) ([]keyOutput, error) {
	out, err := keysInABackupByThePackage(filepath.Join(dir, "package"), value)
	if err != nil {
		return out, err
	}
	more, err := keysInABackupByTheCommand(root, filepath.Join(dir, "command"), value)
	return append(out, more...), err
}

// keysInABackupByThePackage backs a tenant up through internal/tenant and
// restores it into a second data folder whose master key is another
// keeper's, both held in memory.
func keysInABackupByThePackage(dir, value string) ([]keyOutput, error) {
	var out []keyOutput
	say := func(where string, values ...any) {
		for _, v := range values {
			out = append(out, keyOutput{where, []byte(fmt.Sprintf("%v %+v %#v", v, v, v))})
		}
	}
	one, err := tenant.OpenWithKeeper(filepath.Join(dir, "one"), secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	made, err := one.Create("Test Person One")
	if err != nil {
		return nil, err
	}
	s, err := one.Store(made.ID)
	if err != nil {
		return nil, err
	}
	err = s.Secrets().Set(keyCheckName, value)
	s.Close()
	if err != nil {
		return nil, err
	}
	var file bytes.Buffer
	info, err := one.Backup(made.ID, &file, backupPassphrase, tenant.BackupOptions{Rounds: backup.MinRounds, MinPassphrase: 12})
	if err != nil {
		return out, err
	}
	out = append(out, keyOutput{"the backup file", file.Bytes()})
	say("what the backup returned", info)

	two, err := tenant.OpenWithKeeper(filepath.Join(dir, "two"), secrets.NewMemoryKeeper())
	if err != nil {
		return out, err
	}
	changed := append([]byte(nil), file.Bytes()...)
	changed[len(changed)-3] ^= 1
	_, err = two.Restore(bytes.NewReader(file.Bytes()), "another invented passphrase", tenant.RestoreOptions{})
	say("the error for a wrong passphrase", err)
	_, err = two.Restore(bytes.NewReader(changed), backupPassphrase, tenant.RestoreOptions{})
	say("the error for a changed file", err)
	restored, err := two.Restore(bytes.NewReader(file.Bytes()), backupPassphrase, tenant.RestoreOptions{})
	if err != nil {
		return out, err
	}
	say("what the restore returned", restored)
	_, err = two.Restore(bytes.NewReader(file.Bytes()), backupPassphrase, tenant.RestoreOptions{})
	say("the error for a restore over the tenant", err)

	r, err := two.Store(made.ID)
	if err != nil {
		return out, err
	}
	defer r.Close()
	if got, ok, err := r.Secrets().Get(keyCheckName); err != nil || !ok || got != value {
		return out, fmt.Errorf("the restored tenant did not give the stored value back under the second install's keeper: found %v, %v", ok, err)
	}
	for _, folder := range []string{"one", "two"} {
		files, err := folderFiles(filepath.Join(dir, folder))
		if err != nil {
			return out, err
		}
		out = append(out, files...)
	}
	return out, nil
}

// keysInABackupByTheCommand runs the tenant command as the operator would:
// a secret stored and a backup made in one data folder with one key file,
// then restored into another data folder with a key file of its own. The
// second install must list the secret, and the first key file must not
// open it there.
func keysInABackupByTheCommand(root, dir, value string) ([]keyOutput, error) {
	bin := filepath.Join(dir, "tenant")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := project.Run(root, "go", "build", "-o", bin, "./tools/tenant"); err != nil {
		return nil, fmt.Errorf("go build ./tools/tenant failed:\n%s", out)
	}
	var out []keyOutput
	run := func(what, machine string, works bool, stdin string, args ...string) (string, error) {
		profile, data := filepath.Join(dir, "profile-"+machine), filepath.Join(dir, "data-"+machine)
		if err := os.MkdirAll(profile, 0o700); err != nil {
			return "", err
		}
		cmd := exec.Command(bin, append([]string{"--data", data, "--keeper", "file"}, args...)...)
		cmd.Env = append(os.Environ(), "AppData="+profile, "XDG_CONFIG_HOME="+profile, "HOME="+profile)
		cmd.Stdin = strings.NewReader(stdin)
		printed, err := cmd.CombinedOutput()
		out = append(out, keyOutput{"what " + what + " printed", printed})
		if works && err != nil {
			return "", fmt.Errorf("%s failed: %v\n%s", what, err, printed)
		}
		if !works && err == nil {
			return "", fmt.Errorf("%s was not refused", what)
		}
		return string(printed), nil
	}

	added, err := run("add", "one", true, "", "add", "Test Person One")
	if err != nil {
		return out, err
	}
	id := tenantIDShape.FindString(added)
	if id == "" {
		return out, fmt.Errorf("add did not print the new tenant's id")
	}
	file := filepath.Join(dir, "tenant.backup")
	pass := backupPassphrase + "\n"
	steps := []struct {
		what, machine string
		works         bool
		stdin         string
		args          []string
	}{
		{"secret set", "one", true, value + "\n", []string{"secret", "set", id, keyCheckName}},
		{"backup", "one", true, pass, []string{"backup", id, file}},
		{"a backup over the file", "one", false, pass, []string{"backup", id, file}},
		{"restore --check", "two", true, pass, []string{"restore", "--check", file}},
		{"restore with a wrong passphrase", "two", false, "another invented passphrase\n", []string{"restore", file}},
		{"restore", "two", true, pass, []string{"restore", file}},
		{"a restore over the tenant", "two", false, pass, []string{"restore", file}},
	}
	for _, s := range steps {
		if _, err := run(s.what, s.machine, s.works, s.stdin, s.args...); err != nil {
			return out, err
		}
	}
	listed, err := run("secret list after the restore", "two", true, "", "secret", "list", id)
	if err != nil {
		return out, err
	}
	if !strings.Contains(listed, keyCheckName) {
		return out, fmt.Errorf("the restored tenant's secret list does not show the stored secret")
	}
	// The first machine's key file must not open the restored secrets: the
	// data key travelled re-wrapped, not with the old master key.
	cross := exec.Command(bin, "--data", filepath.Join(dir, "data-two"), "--keeper", "file", "secret", "list", id)
	profileOne := filepath.Join(dir, "profile-one")
	cross.Env = append(os.Environ(), "AppData="+profileOne, "XDG_CONFIG_HOME="+profileOne, "HOME="+profileOne)
	printed, err := cross.CombinedOutput()
	out = append(out, keyOutput{"what secret list with the first key file printed", printed})
	if err == nil {
		return out, fmt.Errorf("the first install's key file opened the restored secrets")
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		return out, err
	}
	out = append(out, keyOutput{"the backup file the command wrote", raw})
	for _, folder := range []string{"data-one", "data-two", "profile-one", "profile-two"} {
		files, err := folderFiles(filepath.Join(dir, folder))
		if err != nil {
			return out, err
		}
		out = append(out, files...)
	}
	return out, nil
}
