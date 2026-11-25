package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The "no keys in output" check. It stores an invented value as a tenant's
// secret, and then looks for that value in everything Keystone printed,
// returned or wrote while it held it. The only place the value may come
// back is the call that asks for it by name.

// keyOutput is one thing Keystone printed, returned or wrote.
type keyOutput struct {
	where string
	data  []byte
}

// keySurfaces are the places a key could leak to. Each one runs a part of
// Keystone in a folder of its own, with value stored as a secret, and
// returns everything that part printed, returned or wrote.
//
// An item that adds a place a key could reach adds a line here: the logs,
// the audit log, the API's responses.
var keySurfaces = []struct {
	name string
	run  func(root, dir, value string) ([]keyOutput, error)
}{
	{"the secrets package", keysInThePackage},
	{"the tenant command", keysInTheCommand},
	{"the model registry", keysInTheRegistry},
	{"the API", keysInTheAPI}, // in routes.go: every route's answers, the server's log and the audit log
	{"the Anthropic adapter", keysInTheAnthropicAdapter},
	{"the settings routes", keysInTheSettingsRoutes}, // in settingsroutes.go: a value stored through the API, and the routes that could hand it back
	{"web search", keysInWebSearch},                  // in websearch.go: a search provider that repeats the key in its refusals and results
	{"backup and restore", keysInBackups},            // in backup.go: the backup file, a restore under another master key, and what both print
	{"connectors", keysInConnectors},                 // in connectors.go: servers that repeat a connector's token in results, errors, a refusal and a command's standard error
}

const keyCheckName = "provider.test"

func checkNoKeys(root string) error {
	dir, err := os.MkdirTemp("", "keystone-nokeys-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var tail [8]byte
	if _, err := rand.Read(tail[:]); err != nil {
		return err
	}
	value := "an invented value " + hex.EncodeToString(tail[:]) + ", not a real key"

	var problems []string
	for i, surface := range keySurfaces {
		outputs, err := surface.run(root, filepath.Join(dir, fmt.Sprint(i)), value)
		if err != nil {
			outputs = append(outputs, keyOutput{"the error", []byte(err.Error())})
			problems = append(problems, surface.name+": "+strings.ReplaceAll(err.Error(), value, "(the stored value)"))
		}
		for _, o := range outputs {
			if holdsKey(o.data, value) {
				problems = append(problems, surface.name+": "+o.where+" holds the stored value")
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// holdsKey reports whether data holds value as it is, or as hex or base64.
func holdsKey(data []byte, value string) bool {
	for _, form := range []string{
		value,
		hex.EncodeToString([]byte(value)),
		base64.StdEncoding.EncodeToString([]byte(value)),
	} {
		if bytes.Contains(data, []byte(form)) {
			return true
		}
	}
	return false
}

// folderFiles returns every file under dir, so a caller can look in the
// registry, the databases and the secrets files.
func folderFiles(dir string) ([]keyOutput, error) {
	var out []keyOutput
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, keyOutput{"the file " + filepath.ToSlash(rel), raw})
		return nil
	})
	return out, err
}

// keysInThePackage uses internal/secrets through a tenant's store handle,
// with the master key held in memory. It returns what the package's calls
// gave back, other than the value asked for by name, and every file in the
// data folder.
func keysInThePackage(_, dir, value string) ([]keyOutput, error) {
	data := filepath.Join(dir, "data")
	in, err := tenant.OpenWithKeeper(data, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	var stores [2]*tenant.Store
	for i, name := range []string{"Test Person One", "Test Person Two"} {
		made, err := in.Create(name)
		if err != nil {
			return nil, err
		}
		if stores[i], err = in.Store(made.ID); err != nil {
			return nil, err
		}
		defer stores[i].Close()
	}
	one, two := stores[0].Secrets(), stores[1].Secrets()

	if err := one.Set(keyCheckName, value); err != nil {
		return nil, err
	}
	// The check is worth nothing unless the value really is stored.
	if got, ok, err := one.Get(keyCheckName); err != nil || !ok || got != value {
		return nil, fmt.Errorf("a value was stored and did not come back: found %v, %v", ok, err)
	}

	var out []keyOutput
	// say notes each value the way a log line or an error page might
	// print it.
	say := func(where string, values ...any) {
		for _, v := range values {
			out = append(out, keyOutput{where, []byte(fmt.Sprintf("%v %+v %#v", v, v, v))})
		}
	}
	names, err := one.Names()
	say("the list of names", names, err)
	say("the vault, printed", one)
	say("the error for a bad name", one.Set("Not A Name", value))
	say("the error for a value that is not text", one.Set(keyCheckName, value+"\xff"))
	say("the error for a value that is too long", one.Set(keyCheckName, value+strings.Repeat("x", 1<<20)))
	_, found, err := two.Get(keyCheckName)
	say("what another tenant found", found, err)

	// Tenant one's file in tenant two's folder, as a mistaken restore.
	raw, err := os.ReadFile(filepath.Join(data, "tenants", stores[0].TenantID(), "secrets.bin"))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(data, "tenants", stores[1].TenantID(), "secrets.bin"), raw, 0o600); err != nil {
		return nil, err
	}
	names, err = two.Names()
	if err == nil {
		return out, fmt.Errorf("tenant one's secrets file was read in tenant two's folder")
	}
	say("the error for another tenant's file", names, err)
	say("the error for a write over another tenant's file", two.Set(keyCheckName, value))

	files, err := folderFiles(data)
	return append(out, files...), err
}

var tenantIDShape = regexp.MustCompile(`[a-f0-9]{16}`)

// keysInTheCommand builds the tenant command and runs it as the operator
// would. It returns everything the command printed and every file it
// wrote. The master key is kept in a file, with the user's profile pointed
// at a folder of this check's own, so the check touches no key store and
// leaves nothing behind.
func keysInTheCommand(root, dir, value string) ([]keyOutput, error) {
	profile, data := filepath.Join(dir, "profile"), filepath.Join(dir, "data")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, err
	}
	bin := filepath.Join(dir, "tenant")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := project.Run(root, "go", "build", "-o", bin, "./tools/tenant"); err != nil {
		return nil, fmt.Errorf("go build ./tools/tenant failed:\n%s", out)
	}

	var out []keyOutput
	// run runs the command with stdin piped in. works says whether the
	// command is expected to succeed.
	run := func(what string, works bool, stdin string, args ...string) (string, error) {
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

	added, err := run("add", true, "", "add", "Test Person One")
	if err != nil {
		return out, err
	}
	id := tenantIDShape.FindString(added)
	if id == "" {
		return out, fmt.Errorf("add did not print the new tenant's id")
	}
	steps := []struct {
		what  string
		works bool
		stdin string
		args  []string
	}{
		{"secret set", true, value + "\n", []string{"secret", "set", id, keyCheckName}},
		{"a second secret set", true, value + "\n", []string{"secret", "set", id, "connector.test"}},
		{"secret set with a bad name", false, value + "\n", []string{"secret", "set", id, "Not A Name"}},
		{"secret set with the value as an argument", false, "", []string{"secret", "set", id, keyCheckName, value}},
		{"secret set for a tenant that is not there", false, value + "\n", []string{"secret", "set", "0000000000000000", keyCheckName}},
		{"secret delete", true, "", []string{"secret", "delete", id, "connector.test"}},
		{"list", true, "", []string{"list"}},
	}
	for _, s := range steps {
		if _, err := run(s.what, s.works, s.stdin, s.args...); err != nil {
			return out, err
		}
	}
	// The check is worth nothing unless the value really is stored.
	listed, err := run("secret list", true, "", "secret", "list", id)
	if err != nil {
		return out, err
	}
	if !strings.Contains(listed, keyCheckName) || strings.Contains(listed, "connector.test") {
		return out, fmt.Errorf("secret list did not show the one secret that is stored")
	}

	for _, folder := range []string{data, profile} {
		files, err := folderFiles(folder)
		if err != nil {
			return out, err
		}
		out = append(out, files...)
	}
	return out, nil
}
