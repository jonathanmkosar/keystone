package main

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/project"
)

// networkAllowed lists the folders whose Go code may import a network
// package, and why. Code in every other folder must import none. An item
// that builds a part which talks to the network adds its folder here, with
// the reason.
var networkAllowed = map[string]string{
	"cmd/keystone":   "the server listens for its clients",
	"internal/audit": "wraps the server's handlers to record each call; it opens no connection",
	"tools/rebuild":  "fetches the page from the binary it has just started, on this machine",
	"internal/api":   "the API's handlers, served by the server; it opens no connection",
	"internal/app":   "builds the API's handler from the settings; it opens no connection",
	"tools/verify":   "calls the API's handlers in memory for the route walks; it opens no connection",

	"internal/adapters/responses": "calls the model at the registry entry's endpoint; its tests call a server on this machine",

	"internal/knowledge/waymarks": "calls the tenant's own Waymarks instance, at the address in that tenant's settings; its tests call a fake one on this machine",

	"internal/adapters/anthropic": "calls the endpoint a registry entry names, over HTTP; its tests call a server on this machine",
}

// deadProxy is an address on this machine where nothing listens. The tests
// are run with it as their proxy, so a call that leaves the machine through
// the standard HTTP client fails at once. Calls to this machine skip the
// proxy and still work.
const deadProxy = "http://127.0.0.1:9"

// checkOffline is the standing check that Keystone can be checked with no
// network and no keys. It proves two things today: only the folders in
// networkAllowed import a network package, and the tests pass with every
// key taken out of the environment and no way out to the network.
func checkOffline(root string) error {
	problems, err := networkImports(root, networkAllowed)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = root
	cmd.Env = offlineEnv(os.Environ())
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go test ./... failed with no keys and no network:\n%s", strings.TrimSpace(out.String()))
	}
	return nil
}

// reachesNetwork reports whether an import path is a package that can open
// a connection. net/url and net/netip only read addresses.
func reachesNetwork(path string) bool {
	switch {
	case path == "net/url", path == "net/netip":
		return false
	case path == "net", strings.HasPrefix(path, "net/"):
		return true
	case path == "crypto/tls":
		return true
	case path == "golang.org/x/net", strings.HasPrefix(path, "golang.org/x/net/"):
		return true
	}
	return false
}

// networkImports reads the imports of every Go file under root, tests
// included. It reports each file outside the allowed folders that imports a
// network package, and each allowed folder that no longer needs to be one.
func networkImports(root string, allowed map[string]string) ([]string, error) {
	var problems []string
	used := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if project.Skip(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		file, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		folder := "."
		if i := strings.LastIndex(rel, "/"); i >= 0 {
			folder = rel[:i]
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil || !reachesNetwork(path) {
				continue
			}
			if _, ok := allowed[folder]; ok {
				used[folder] = true
				continue
			}
			problems = append(problems, fmt.Sprintf("%s imports %s, and %s is not a folder that may use the network (the list is in tools/verify/offline.go)", rel, path, folder))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for folder := range allowed {
		if !used[folder] {
			problems = append(problems, folder+" is listed as a folder that may use the network and imports no network package: take it off the list in tools/verify/offline.go")
		}
	}
	sort.Strings(problems)
	return problems, nil
}

// offlineEnv returns env with nothing in it that could reach a provider:
// every variable named like a key, a token, a secret or a password is
// dropped, the Go module proxy is switched off, and the HTTP proxy points
// at deadProxy.
func offlineEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		name = strings.ToUpper(name)
		switch name {
		case "GOPROXY", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		if namedLikeASecret(name) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GOPROXY=off", "HTTP_PROXY="+deadProxy, "HTTPS_PROXY="+deadProxy)
}

// namedLikeASecret reports whether an upper-case variable name is one that
// holds a key, such as a provider's API key.
func namedLikeASecret(name string) bool {
	for _, end := range []string{"_KEY", "_TOKEN", "_SECRET", "_PASSWORD"} {
		if strings.HasSuffix(name, end) {
			return true
		}
	}
	return false
}
