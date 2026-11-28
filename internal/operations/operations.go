// Package operations is Operations and Monitor (P11-03): what is running
// for a person, how it is doing, what went wrong, and where the install is
// running out of room.
//
// It reads what other parts keep and changes none of it: background tasks
// and their runs (internal/jobs), schedules (internal/schedules), the usage
// records (internal/usage) and the audit log (internal/audit) for model
// health and recent traces, the usage report (internal/usagereport) for the
// month against the cap, and the tenant's own folder and its volume for
// room. Its one table, operations_snapshots, holds where each limit stood
// at each snapshot, so a limit has a trend.
//
// A report is one tenant's: every function takes that tenant's store and
// reads nothing else of the install but the free space of the volume that
// holds the data folder, which is the same for every person (fixed rule
// 1). Every text shown from the audit log or a turn's error goes through
// audit.Scrub, so no key reaches a reply. The package enforces no fixed
// rule, so it may read settings. docs/architecture/operations.md describes
// it.
package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/figures"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// The install's settings, read once when the server starts.
const (
	SettingSnapshotEvery = "operations.snapshot_every"
	SettingSnapshotKeep  = "operations.snapshot_keep"
)

// The settings read for each report, as they stand for the tenant. They
// are internal/figures' own since P15-02 (decision 0700): the person's page
// and the admin read them one way.
const (
	SettingDatabaseRoom    = figures.SettingDatabaseRoom
	SettingFilesRoom       = figures.SettingFilesRoom
	SettingArtifactsRoom   = figures.SettingArtifactsRoom
	SettingTrendWindow     = figures.SettingTrendWindow
	SettingTrendMinSpan    = figures.SettingTrendMinSpan
	SettingHealthWindow    = figures.SettingHealthWindow
	SettingJobsShown       = figures.SettingJobsShown
	SettingTracesShown     = figures.SettingTracesShown
	SettingStuckAfter      = figures.SettingStuckAfter
	SettingModelErrorWatch = figures.SettingModelErrorWatch
	SettingWatchDisk       = figures.SettingWatchDisk
	SettingWatchDatabase   = figures.SettingWatchDatabase
	SettingWatchFiles      = figures.SettingWatchFiles
	SettingWatchArtifacts  = figures.SettingWatchArtifacts
	SettingWatchReserve    = figures.SettingWatchReserve
	SettingWatchRunBudget  = figures.SettingWatchRunBudget
)

// The bounds of the sampler's install settings. A value outside them is
// taken as the nearest bound.
var (
	everyBounds = [2]time.Duration{time.Minute, 24 * time.Hour}
	keepBounds  = [2]time.Duration{time.Hour, 366 * 24 * time.Hour}
)

// Config is the install's settings for the sampler, which records every
// tenant's limits.
type Config struct {
	// SnapshotEvery is how often each tenant's limits are recorded.
	SnapshotEvery time.Duration
	// SnapshotKeep is how long a snapshot is kept.
	SnapshotKeep time.Duration
}

// ReadConfig reads the install's settings from wherever they are kept. get
// returns the value for a setting's key.
func ReadConfig(get func(key string) (string, bool)) (Config, error) {
	var c Config
	for _, x := range []struct {
		key    string
		into   *time.Duration
		bounds [2]time.Duration
	}{{SettingSnapshotEvery, &c.SnapshotEvery, everyBounds}, {SettingSnapshotKeep, &c.SnapshotKeep, keepBounds}} {
		v, ok := get(x.key)
		if !ok {
			return Config{}, fmt.Errorf("the %s setting is missing", x.key)
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", x.key, err)
		}
		*x.into = clampDuration(d, x.bounds)
	}
	return c, nil
}

// DefaultConfig is the install's settings as the registry ships them.
func DefaultConfig() (Config, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Config{}, err
	}
	return ReadConfig(func(key string) (string, bool) {
		st, ok := settings.Lookup(reg, key)
		return st.Default, ok
	})
}

// Settings are the settings one report reads, as they stand for a tenant:
// internal/figures' own.
type Settings = figures.Settings

// ReadSettings reads a report's settings for a tenant. A stored value that
// does not read is an error; a value outside its bounds is taken as the
// nearest bound.
func ReadSettings(ctx context.Context, s *tenant.Store, reg []settings.Setting) (Settings, error) {
	return figures.ReadSettings(ctx, s, reg)
}

func clampDuration(d time.Duration, b [2]time.Duration) time.Duration {
	return figures.ClampDuration(d, b)
}
