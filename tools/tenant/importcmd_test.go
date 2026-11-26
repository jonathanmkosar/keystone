package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/importer"
	"github.com/jonathanmkosar/keystone/internal/importer/importertest"
)

// The source here is generated, with an invented person and invented words.
func TestImportChecksThenImportsAndPrintsNoWords(t *testing.T) {
	in, id := testInstall(t)
	dir := t.TempDir()

	// New Keystone's database, in use: its rows are in the log beside it.
	live := filepath.Join(dir, "workspace.sqlite")
	db, err := importertest.Open(live, importertest.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := importertest.Write(db, importertest.Standard()); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runImport(in, []string{"--check", id, live}, &out); !errors.Is(err, importer.ErrSource) || !strings.Contains(err.Error(), "import-copy") {
		t.Fatalf("the database in use was read: %v", err)
	}
	whole := filepath.Join(dir, "copy.sqlite")
	if err := runImportCopy([]string{live, whole}, &out); err != nil || !strings.Contains(out.String(), "one whole file") {
		t.Fatalf("import-copy: %v\n%s", err, out.String())
	}
	if err := runImportCopy([]string{live, whole}, &out); err == nil {
		t.Error("import-copy replaced a file")
	}

	chatsOf := func() int {
		s, err := in.Store(id)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		list, err := chat.List(s, chat.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	out.Reset()
	if err := runImport(in, []string{"--check", id, whole}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Would import:") || !strings.Contains(out.String(), "Nothing was written") || chatsOf() != 0 {
		t.Errorf("the check wrote %d chats and printed:\n%s", chatsOf(), out.String())
	}
	if err := runImport(in, []string{id, whole}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Imported:") || !strings.Contains(out.String(), "Every schedule is paused") || chatsOf() != 5 {
		t.Errorf("the import wrote %d chats and printed:\n%s", chatsOf(), out.String())
	}
	if err := runImport(in, []string{id, whole}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Nothing was imported") || chatsOf() != 5 {
		t.Errorf("a second import wrote %d chats and printed:\n%s", chatsOf()-5, out.String())
	}
	for _, word := range []string{importertest.BeanWord, importertest.PintleWord, importertest.BriefName, importertest.WeeklyName, importertest.Workspace, importertest.BuildSummary} {
		if strings.Contains(out.String(), word) {
			t.Errorf("the command printed %q, a word of the source", word)
		}
	}

	for _, args := range [][]string{nil, {id}, {"--check", id}, {id, whole, "more"}, {"--sideways", id, whole}} {
		if err := runImport(in, args, &out); !errors.Is(err, errImportUsage) {
			t.Errorf("import %v gave %v", args, err)
		}
	}
	if err := runImportCopy([]string{live}, &out); !errors.Is(err, errImportUsage) {
		t.Errorf("import-copy with one argument gave %v", err)
	}
	if err := runImport(in, []string{"0000000000000000", whole}, &out); err == nil {
		t.Error("a tenant that does not exist was imported into")
	}
	if err := runImport(in, []string{"--workspace", "Nobody", id, whole}, &out); !errors.Is(err, importer.ErrWorkspace) {
		t.Errorf("a workspace that is not there gave %v", err)
	}
}
