package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/backup"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/projects"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/uploads"
)

// The standing check "encryption at rest" (P15-06). The item's proof, on
// the packages that really write a person's data: a chat and its message,
// an upload, an artifact, a project file and a page of the built-in
// knowledge store are written for a sealed person, and then
//
//   - every file in the person's folder, read straight from disk, holds no
//     marker word and no plain SQLite header, and no name on disk is the
//     upload's hash, the chat's id or the artifact's id;
//   - the install opened under another master key does not open;
//   - the person's database and files put in another person's folder do
//     not open there;
//   - an install written plain, as an older Keystone left it, sealed the
//     way the server seals it at its first start, reads back every word
//     and passes the same scan.
//
// A start cut short at each step is proven by internal/tenant's tests,
// which the tests check runs.
func init() {
	componentDocs["internal/atrest"] = "docs/architecture/encryption.md"
}

const encryptionMarker = "OCELOTLANTERNMARKER"

func checkEncryption(string) error {
	cfg, err := app.Defaults()
	if err != nil {
		return err
	}
	ctx := context.Background()
	work, err := os.MkdirTemp("", "keystone-encryption-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	// A sealed install, as the server makes people.
	keeper := secrets.NewMemoryKeeper()
	dir := filepath.Join(work, "sealed")
	in, err := tenant.OpenWithKeeper(dir, keeper)
	if err != nil {
		return err
	}
	in.EncryptAtRest(tenant.AtRest{On: true})
	one, err := in.Create("Encryption Check One")
	if err != nil {
		return err
	}
	two, err := in.Create("Encryption Check Two")
	if err != nil {
		return err
	}
	names, err := writeEverything(ctx, in, one.ID, cfg)
	if err != nil {
		return err
	}
	if err := readEverything(ctx, in, one.ID, names, cfg); err != nil {
		return err
	}
	var problems []string
	problems = append(problems, scanSealed(filepath.Join(dir, "tenants", one.ID), names)...)

	if _, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper()); err == nil {
		problems = append(problems, "the sealed install opened under another master key")
	}

	// The first person's database and files in the second person's folder,
	// under the second person's own secrets file and seal.json.
	from, to := filepath.Join(dir, "tenants", one.ID), filepath.Join(dir, "tenants", two.ID)
	for _, sub := range []string{"files", "artifacts"} {
		if err := os.RemoveAll(filepath.Join(to, sub)); err != nil {
			return err
		}
		if err := copyTree(filepath.Join(from, sub), filepath.Join(to, sub)); err != nil {
			return err
		}
	}
	if err := copyTree(filepath.Join(from, "keystone.db"), filepath.Join(to, "keystone.db")); err != nil {
		return err
	}
	if _, err := tenant.OpenWithKeeper(dir, keeper); err == nil {
		problems = append(problems, "a person's sealed database and files opened in another person's folder")
	}

	// A plain install, sealed as the server does at its first start.
	plainDir := filepath.Join(work, "plain")
	plain, err := tenant.OpenWithKeeper(plainDir, keeper)
	if err != nil {
		return err
	}
	old, err := plain.Create("Encryption Check Plain")
	if err != nil {
		return err
	}
	oldNames, err := writeEverything(ctx, plain, old.ID, cfg)
	if err != nil {
		return err
	}
	plain.EncryptAtRest(tenant.AtRest{On: true})
	r, err := plain.SealAll(tenant.SealOptions{Backup: tenant.BackupOptions{Rounds: backup.MinRounds, MinPassphrase: 12},
		BackupFolder: filepath.Join(work, "backups")})
	if err != nil {
		return fmt.Errorf("sealing a plain install: %w", err)
	}
	if len(r.Sealed) != 1 || len(r.Backups) != 1 {
		problems = append(problems, fmt.Sprintf("sealing a plain install of one person sealed %d and wrote %d backups", len(r.Sealed), len(r.Backups)))
	}
	if err := readEverything(ctx, plain, old.ID, oldNames, cfg); err != nil {
		problems = append(problems, "after sealing a plain install: "+err.Error())
	}
	problems = append(problems, scanSealed(filepath.Join(plainDir, "tenants", old.ID), oldNames)...)

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// written names what writeEverything wrote, to read it back and to look
// for on disk.
type written struct {
	chatID, uploadID, artifactID, projectFile string
}

func writeEverything(ctx context.Context, in *tenant.Install, id string, cfg app.Config) (written, error) {
	var w written
	s, err := in.Store(id)
	if err != nil {
		return w, err
	}
	defer s.Close()
	c, err := chat.Create(s)
	if err != nil {
		return w, err
	}
	w.chatID = c.ID
	if _, err := chat.Append(s, c.ID, chat.NewMessage{Role: seam.RoleUser,
		Blocks: []seam.Block{{Type: seam.BlockText, Text: encryptionMarker + " said in a message"}}}); err != nil {
		return w, err
	}
	up, _, err := uploads.Add(ctx, s, cfg.Uploads, c.ID, "notes.txt", []byte(encryptionMarker+" written in an upload\n"))
	if err != nil {
		return w, err
	}
	w.uploadID = up.ID
	a, _, err := artifacts.Create(ctx, s, cfg.Artifacts, c.ID,
		artifacts.Draft{Type: artifacts.KindMarkdown, Title: "Check", Content: "# " + encryptionMarker + " in an artifact\n"}, "encryption-check")
	if err != nil {
		return w, err
	}
	w.artifactID = a.ID
	data := []byte(encryptionMarker + " in a project file\n")
	sum := sha256.Sum256(data)
	w.projectFile = hex.EncodeToString(sum[:])
	if err := (projects.Folder{}).Keep(s, w.projectFile, data); err != nil {
		return w, err
	}
	if _, err := builtin.WritePage(ctx, s, builtin.NewPage{Space: "notes", Path: "check", Title: "Check",
		Body: encryptionMarker + " in a page of what Keystone knows"}); err != nil {
		return w, err
	}
	return w, nil
}

func readEverything(ctx context.Context, in *tenant.Install, id string, w written, cfg app.Config) error {
	s, err := in.Store(id)
	if err != nil {
		return err
	}
	defer s.Close()
	var n int
	if err := s.DB().QueryRow("SELECT count(*) FROM messages WHERE instr(content, ?) > 0", encryptionMarker).Scan(&n); err != nil || n != 1 {
		return fmt.Errorf("the message does not read back (%d, %v)", n, err)
	}
	if raw, err := uploads.Read(s, w.uploadID); err != nil || !bytes.Contains(raw, []byte(encryptionMarker)) {
		return fmt.Errorf("the upload does not read back: %v", err)
	}
	if _, content, err := artifacts.Content(ctx, s, w.artifactID, 1); err != nil || !strings.Contains(content, encryptionMarker) {
		return fmt.Errorf("the artifact does not read back: %v", err)
	}
	if raw, err := s.FilesFolder().ReadFile(w.projectFile); err != nil || !bytes.Contains(raw, []byte(encryptionMarker)) {
		return fmt.Errorf("the project file does not read back: %v", err)
	}
	if !s.Sealed() {
		return errors.New("the person is not sealed")
	}
	return nil
}

// scanSealed reads every file in a person's folder from disk.
func scanSealed(folder string, w written) []string {
	var problems []string
	files := 0
	err := filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, name := range []string{w.chatID, w.uploadID, w.artifactID, w.projectFile} {
			if d.Name() == name {
				problems = append(problems, p+" has a plain name on disk")
			}
		}
		if d.IsDir() {
			return nil
		}
		files++
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte(encryptionMarker)) {
			problems = append(problems, p+" holds the marker word")
		}
		if bytes.Contains(raw, []byte("SQLite format 3")) {
			problems = append(problems, p+" holds a plain database header")
		}
		return nil
	})
	if err != nil {
		problems = append(problems, err.Error())
	}
	if files < 6 {
		problems = append(problems, fmt.Sprintf("%s held %d files, fewer than were written", folder, files))
	}
	return problems
}

// copyTree copies a file, or a folder and everything in it.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, raw, 0o600)
	})
}
