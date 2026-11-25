package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/backup"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name, value, password and passphrase here is invented.
const (
	testPassphrase = "an invented passphrase for a test"
	testPassword   = "an invented password"
)

var testBackupOptions = tenant.BackupOptions{Rounds: backup.MinRounds, MinPassphrase: 12}

// say appends a message to a chat and returns its id.
func say(t *testing.T, s *tenant.Store, chatID string, parent int64, role seam.Role, text string) int64 {
	t.Helper()
	m, err := chat.Append(s, chatID, chat.NewMessage{ParentID: parent, Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}})
	if err != nil {
		t.Fatal(err)
	}
	return m.ID
}

// The item's proof: a tenant with chats, a file and a stored secret is
// backed up, restored into a fresh data folder whose master key is another
// keeper's, as on another machine, and there it signs in, its chats read
// back and its key works.
func TestABackupRestoredOnAnotherMachine(t *testing.T) {
	src, id := testInstall(t)
	o, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	lim, err := chat.DefaultLimits()
	if err != nil {
		t.Fatal(err)
	}
	s, err := src.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := signin.SetPassword(s, testPassword, o); err != nil {
		t.Fatal(err)
	}
	oldToken, _, err := signin.SignIn(s, testPassword, o)
	if err != nil {
		t.Fatal(err)
	}
	first, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Rename(s, lim, first.ID, "Harbour timetable"); err != nil {
		t.Fatal(err)
	}
	q := say(t, s, first.ID, 0, seam.RoleUser, "When does the ferry leave the harbour?")
	say(t, s, first.ID, q, seam.RoleAssistant, "The invented ferry leaves at nine.")
	second, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	say(t, s, second.ID, 0, seam.RoleUser, "A second invented chat.")
	if err := s.Files().WriteFile("chart.txt", []byte("an invented chart"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := chat.AddFile(s, first.ID, "chart.txt"); err != nil {
		t.Fatal(err)
	}
	if err := s.Secrets().Set("provider.test", testValue); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// The backup, with the passphrase piped in as PowerShell pipes it.
	file := filepath.Join(t.TempDir(), "tenant.backup")
	var out bytes.Buffer
	if err := runBackup(src, testBackupOptions, []string{id, file}, strings.NewReader("\ufeff"+testPassphrase+"\r\n"), false, io.Discard, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 secrets") || !strings.Contains(out.String(), "read back") {
		t.Errorf("backup printed:\n%s", out.String())
	}
	if _, err := os.Stat(file + ".partial"); err == nil {
		t.Error("the partial file is still there")
	}

	// Another machine: a data folder of its own and another keeper.
	dst, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	if err := runRestore(dst, []string{"--check", file}, strings.NewReader(testPassphrase+"\n"), false, io.Discard, &out); err != nil {
		t.Fatal(err)
	}
	if len(dst.Tenants()) != 0 {
		t.Fatal("restore --check added a tenant")
	}
	if err := runRestore(dst, []string{file}, strings.NewReader(testPassphrase+"\n"), false, io.Discard, &out); err != nil {
		t.Fatal(err)
	}

	// It signs in, and the session it had before is gone.
	if _, _, err := signin.Resolve(dst, oldToken); err == nil {
		t.Error("a session from before the backup still works after the restore")
	}
	token, _, err := signin.SignIn(mustStore(t, dst, id), testPassword, o)
	if err != nil {
		t.Fatalf("the restored tenant cannot sign in: %v", err)
	}
	r, sess, err := signin.Resolve(dst, token)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if sess.TenantID != id || r.TenantID() != id {
		t.Fatalf("the session resolved to tenant %s", sess.TenantID)
	}

	// Its chats read back: the list, the messages, search and the file.
	chats, err := chat.List(r, chat.Filter{})
	if err != nil || len(chats) != 2 {
		t.Fatalf("the restored tenant has %d chats: %v", len(chats), err)
	}
	path, err := chat.Path(r, first.ID)
	if err != nil || len(path) != 2 || path[1].Blocks[0].Text != "The invented ferry leaves at nine." {
		t.Errorf("the first chat came back as %+v, %v", path, err)
	}
	if got, err := chat.Get(r, first.ID); err != nil || got.Title != "Harbour timetable" {
		t.Errorf("the first chat's title is %q, %v", got.Title, err)
	}
	if hits, err := chat.Search(r, lim, "ferry"); err != nil || len(hits) == 0 {
		t.Errorf("search found %d hits: %v", len(hits), err)
	}
	if names, err := chat.Files(r, first.ID); err != nil || len(names) != 1 {
		t.Errorf("the first chat's files are %v, %v", names, err)
	}
	if raw, err := r.Files().ReadFile("chart.txt"); err != nil || string(raw) != "an invented chart" {
		t.Errorf("the file came back as %q, %v", raw, err)
	}

	// Its key works, under the new install's master key.
	if value, ok, err := r.Secrets().Get("provider.test"); err != nil || !ok || value != testValue {
		t.Errorf("the restored key does not open: %v, %v", ok, err)
	}

	// Nothing printed carries the key or the passphrase.
	for _, secret := range []string{testValue, testPassphrase} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("a command printed %q:\n%s", secret, out.String())
		}
	}
}

func mustStore(t *testing.T, in *tenant.Install, id string) *tenant.Store {
	t.Helper()
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestBackupCommandRefusals(t *testing.T) {
	in, id := testInstall(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "tenant.backup")
	run := func(stdin string, typed bool, args ...string) error {
		return runBackup(in, testBackupOptions, args, strings.NewReader(stdin), typed, io.Discard, io.Discard)
	}
	if err := run(testPassphrase, false, id); !errors.Is(err, errBackupUsage) {
		t.Errorf("no file gave %v", err)
	}
	if err := run(testPassphrase, false, id, file, testPassphrase); !errors.Is(err, errBackupUsage) {
		t.Errorf("the passphrase as an argument gave %v", err)
	}
	refused := map[string]struct {
		stdin string
		typed bool
	}{
		"no passphrase":            {"", false},
		"only a line ending":       {"\r\n", false},
		"a short passphrase":       {"too short\n", false},
		"two typed that differ":    {testPassphrase + "\n" + testPassphrase + "!\n", true},
		"typed once with no again": {testPassphrase + "\n", true},
	}
	for what, c := range refused {
		if err := run(c.stdin, c.typed, id, file); err == nil {
			t.Errorf("%s: a backup was made", what)
		}
	}
	if err := run(testPassphrase+"\n", false, "0000000000000000", file); err == nil {
		t.Error("a backup of a tenant that is not there was made")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("refused backups left %v", entries)
	}

	// Typed twice the same, it is taken.
	if err := run(testPassphrase+"\n"+testPassphrase+"\n", true, id, file); err != nil {
		t.Fatal(err)
	}
	// A file that is there is never replaced.
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(testPassphrase+"\n", false, id, file); err == nil || !strings.Contains(err.Error(), "already there") {
		t.Errorf("a backup over a file gave %v", err)
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
		t.Error("the file that was there changed")
	}
}

func TestRestoreCommandRefusals(t *testing.T) {
	in, id := testInstall(t)
	file := filepath.Join(t.TempDir(), "tenant.backup")
	if err := runBackup(in, testBackupOptions, []string{id, file}, strings.NewReader(testPassphrase+"\n"), false, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	run := func(stdin string, args ...string) error {
		return runRestore(in, args, strings.NewReader(stdin), false, io.Discard, io.Discard)
	}
	for what, args := range map[string][]string{
		"no file":                    {},
		"two files":                  {file, file},
		"an unknown flag":            {"--force", file},
		"check and replace together": {"--check", "--replace", file},
	} {
		if err := run(testPassphrase+"\n", args...); !errors.Is(err, errBackupUsage) {
			t.Errorf("%s: gave %v", what, err)
		}
	}
	// Over the tenant it came from, the refusal says how to replace it.
	err := run(testPassphrase+"\n", file)
	if !errors.Is(err, tenant.ErrTenantExists) || !strings.Contains(err.Error(), "--replace") {
		t.Errorf("a restore over the tenant gave %v", err)
	}
	if err := run("another invented passphrase\n", "--replace", file); !errors.Is(err, backup.ErrPassphrase) {
		t.Errorf("a wrong passphrase gave %v", err)
	}
	if err := run("", file); err == nil {
		t.Error("a restore with no passphrase ran")
	}
	if err := run(testPassphrase+"\n", filepath.Join(t.TempDir(), "not-there.backup")); err == nil {
		t.Error("a restore of a file that is not there ran")
	}
	var out bytes.Buffer
	if err := runRestore(in, []string{"--replace", file}, strings.NewReader(testPassphrase+"\n"), false, io.Discard, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "in the place of") {
		t.Errorf("a replace printed:\n%s", out.String())
	}
}

func TestBackupOptionsFromTheRegistry(t *testing.T) {
	o, err := backupOptions()
	if err != nil {
		t.Fatal(err)
	}
	if o.Rounds < backup.MinRounds || o.Rounds > backup.MaxRounds || o.MinPassphrase < 1 {
		t.Errorf("the shipped backup settings are %+v", o)
	}
}
