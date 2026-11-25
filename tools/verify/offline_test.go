package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// tree writes a small invented source tree and returns its root.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestOnlyListedFoldersMayImportTheNetwork(t *testing.T) {
	const quiet = "package a\n\nimport (\n\t\"fmt\"\n\t\"net/url\"\n\t\"net/netip\"\n)\n"
	const dials = "package a\n\nimport \"net/http\"\n"
	allowed := map[string]string{"internal/adapters/remote": "it calls a provider"}

	root := tree(t, map[string]string{
		"internal/chat/chat.go":                 quiet,
		"internal/adapters/remote/call.go":      dials,
		"internal/adapters/remote/call_test.go": "package a\n\nimport \"net/http/httptest\"\n",
		"data/scratch/old.go":                   dials, // a skipped folder
		"notes/readme.md":                       "import \"net/http\"",
	})
	problems, err := networkImports(root, allowed)
	if err != nil || len(problems) != 0 {
		t.Fatalf("a clean tree gave %v (%v)", problems, err)
	}

	for name, c := range map[string]struct {
		file, body, want string
	}{
		"the HTTP client":         {"internal/chat/fetch.go", dials, "internal/chat/fetch.go imports net/http"},
		"a raw connection":        {"internal/chat/dial.go", "package a\n\nimport \"net\"\n", "internal/chat/dial.go imports net,"},
		"TLS":                     {"internal/chat/tls.go", "package a\n\nimport \"crypto/tls\"\n", "imports crypto/tls"},
		"a test file":             {"internal/chat/chat_test.go", "package a\n\nimport \"net/http/httptest\"\n", "internal/chat/chat_test.go imports net/http/httptest"},
		"a renamed import":        {"internal/chat/alias.go", "package a\n\nimport web \"net/http\"\n", "imports net/http"},
		"the extended net module": {"internal/chat/x.go", "package a\n\nimport \"golang.org/x/net/http2\"\n", "imports golang.org/x/net/http2"},
		"a file at the root":      {"main.go", dials, "main.go imports net/http, and . is not"},
	} {
		root := tree(t, map[string]string{"internal/adapters/remote/call.go": dials, c.file: c.body})
		problems, err := networkImports(root, allowed)
		if err != nil || len(problems) != 1 || !strings.Contains(problems[0], c.want) {
			t.Errorf("%s: got %v (%v), want one problem containing %q", name, problems, err, c.want)
		}
	}

	// A folder that no longer uses the network must come off the list.
	root = tree(t, map[string]string{"internal/adapters/remote/call.go": quiet})
	problems, err = networkImports(root, allowed)
	if err != nil || len(problems) != 1 || !strings.Contains(problems[0], "take it off the list") {
		t.Errorf("a stale entry gave %v (%v)", problems, err)
	}
}

func TestOfflineEnvDropsKeysAndClosesTheWayOut(t *testing.T) {
	// Invented names and values.
	got := offlineEnv([]string{
		"PATH=/usr/bin",
		"KEYSTONE_DATA=/tmp/data",
		"EXAMPLE_API_KEY=invented-value",
		"Example_Token=invented-value",
		"DB_PASSWORD=invented-value",
		"SIGNING_SECRET=invented-value",
		"https_proxy=http://proxy.invalid:8080",
		"NO_PROXY=*",
		"GOPROXY=https://proxy.invalid",
		"=C:=C:\\work",
	})
	want := []string{
		"PATH=/usr/bin",
		"KEYSTONE_DATA=/tmp/data",
		"=C:=C:\\work",
		"GOPROXY=off",
		"HTTP_PROXY=" + deadProxy,
		"HTTPS_PROXY=" + deadProxy,
	}
	if !slices.Equal(got, want) {
		t.Errorf("the environment is\n%v\nwant\n%v", got, want)
	}
}
