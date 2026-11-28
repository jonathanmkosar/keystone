package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// encryptAtStart applies the encryption.* settings to the install (P15-06):
// it tells the tenant package how to keep people's data on disk, and with
// encryption at rest on it seals every person who is still plain, one at a
// time, each after a backup, before the server serves anyone (decision
// 0663). A person who cannot be sealed is left plain and as they were, and
// is named in the log; the server still starts, and tries again at its
// next start. Nothing of a person's but their id is written to the log.
func encryptAtStart(in *tenant.Install, dataDir string, log io.Writer) error {
	reg, err := settings.Registry()
	if err != nil {
		return err
	}
	get := func(key string) (string, error) { return setting(reg, key) }
	on, err := get("encryption.at_rest")
	if err != nil {
		return err
	}
	wait, err := get("encryption.lock_wait")
	if err != nil {
		return err
	}
	folder, err := get("encryption.backup_folder")
	if err != nil {
		return err
	}
	var o tenant.AtRest
	if o.On, err = strconv.ParseBool(on); err != nil {
		return fmt.Errorf("encryption.at_rest: %w", err)
	}
	if o.LockWait, err = time.ParseDuration(wait); err != nil {
		return fmt.Errorf("encryption.lock_wait: %w", err)
	}
	in.EncryptAtRest(o)
	if !o.On {
		return nil
	}
	var so tenant.SealOptions
	for key, to := range map[string]*int{"backup.passphrase_rounds": &so.Backup.Rounds, "backup.passphrase_min_length": &so.Backup.MinPassphrase} {
		v, err := get(key)
		if err != nil {
			return err
		}
		if *to, err = strconv.Atoi(v); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	if !filepath.IsAbs(folder) {
		folder = filepath.Join(dataDir, folder)
	}
	so.BackupFolder = folder
	so.Progress = func(line string) { fmt.Fprintln(log, line) }
	r, err := in.SealAll(so)
	if len(r.Sealed) > 0 {
		fmt.Fprintf(log, "encryption at rest: %d people sealed now, %d before; the backups made first are in %s\n", len(r.Sealed), r.Already, folder)
	}
	if err != nil {
		fmt.Fprintf(log, "encryption at rest: stopped: %v. The people not sealed stay plain and as they were; the next start tries again.\n", err)
	}
	return nil
}
