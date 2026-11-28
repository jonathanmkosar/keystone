package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/backup"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name here is invented.
func TestTheEncryptionCommand(t *testing.T) {
	dir := t.TempDir()
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	p, err := in.Create("Plain Person")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runEncryption(in, []string{"status"}, &out); err != nil || !strings.Contains(out.String(), p.ID+"  plain") {
		t.Fatalf("status before: %v\n%s", err, out.String())
	}
	in.EncryptAtRest(tenant.AtRest{On: true})
	r, err := in.SealAll(tenant.SealOptions{Backup: tenant.BackupOptions{Rounds: backup.MinRounds, MinPassphrase: 12}})
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runEncryption(in, []string{"status"}, &out); err != nil || !strings.Contains(out.String(), p.ID+"  encrypted") {
		t.Fatalf("status after: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := runEncryption(in, []string{"passphrase", r.Backups[0]}, &out); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(r.Backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := tenant.CheckBackup(f, strings.TrimSpace(out.String())); err != nil {
		t.Fatalf("the printed passphrase does not open the backup: %v", err)
	}
	if err := runEncryption(in, []string{"passphrase", filepath.Join(dir, "other.keystone-backup")}, &out); err == nil {
		t.Error("a file that is no backup gave a passphrase")
	}
	if err := runEncryption(in, nil, &out); !errors.Is(err, errEncryptionUsage) {
		t.Errorf("no arguments gave %v", err)
	}
}
