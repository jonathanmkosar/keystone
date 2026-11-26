package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/importer/importertest"
)

// The import from New Keystone (P12-03): the record of what came, the
// importer, and the package that makes its test sources and holds its
// proof.
func init() {
	componentDocs["internal/imported"] = "docs/architecture/import.md"
	componentDocs["internal/importer"] = "docs/architecture/import.md"
	componentDocs["internal/importer/importertest"] = "docs/architecture/import.md"
	networkAllowed["internal/importer/importertest"] = "the import's proof calls the API's handlers in memory, for its tests and verify; it opens no connection, and is not in the server Keystone ships"
}

// importerMayNotImport are the parts of Keystone that call a model or a
// knowledge store, or run a turn. The importer brings words into the chat
// record and calls nothing, so it names none of them.
var importerMayNotImport = []string{
	"github.com/jonathanmkosar/keystone/internal/adapters",
	"github.com/jonathanmkosar/keystone/internal/routing",
	"github.com/jonathanmkosar/keystone/internal/models",
	"github.com/jonathanmkosar/keystone/internal/knowledge",
	"github.com/jonathanmkosar/keystone/internal/turn",
	"github.com/jonathanmkosar/keystone/internal/jobs",
	"github.com/jonathanmkosar/keystone/internal/embedding",
}

// checkImport is the standing check of the import from New Keystone. On a
// source generated in New Keystone's schema with invented words, and the
// real server on a throwaway folder (importertest.Proof): the dry run
// writes nothing and counts what the import then brings; imported chats
// open through the API in order, with their original times and the models
// New Keystone named, wear their mark, and are found by the chat search
// and by search_chats; the morning brief is a paused schedule that does
// not run while paused and runs, with the fake provider, once switched on;
// a second import adds nothing; the import made no model call and is in
// the audit log; and another tenant holds and reaches none of it. It also
// reads the importer's own imports: it names no part that calls a model or
// a knowledge store, and the offline check holds it to no network.
func checkImport(root string) error {
	var problems []string
	named, err := importerImports(filepath.Join(root, "internal", "importer"))
	if err != nil {
		return err
	}
	for _, path := range named {
		for _, refused := range importerMayNotImport {
			if path == refused || strings.HasPrefix(path, refused+"/") {
				problems = append(problems, "internal/importer imports "+path+", a part that calls a model, a knowledge store or runs a turn: an import calls none")
			}
		}
	}
	dir, err := os.MkdirTemp("", "keystone-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	proved, err := importertest.Proof(dir)
	if err != nil {
		return err
	}
	problems = append(problems, proved...)
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// importerImports lists what the Go files of one folder import, tests left
// out.
func importerImports(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		for _, im := range f.Imports {
			path, err := strconv.Unquote(im.Path.Value)
			if err != nil {
				return nil, err
			}
			seen[path] = true
		}
	}
	var out []string
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out, nil
}
