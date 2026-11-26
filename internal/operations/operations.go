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
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// The install's settings, read once when the server starts.
const (
	SettingSnapshotEvery = "operations.snapshot_every"
	SettingSnapshotKeep  = "operations.snapshot_keep"
)

// The settings read for each report, as they stand for the tenant. The
// rooms are install settings, read through the tenant's settings like the
// usage report's own install setting.
const (
	SettingDatabaseRoom    = "operations.database_room_mb"
	SettingFilesRoom       = "operations.files_room_mb"
	SettingArtifactsRoom   = "operations.artifacts_room_mb"
	SettingTrendWindow     = "operations.trend_window"
	SettingHealthWindow    = "operations.health_window"
	SettingJobsShown       = "operations.jobs_shown"
	SettingTracesShown     = "operations.traces_shown"
	SettingStuckAfter      = "operations.stuck_after"
	SettingModelErrorWatch = "operations.model_error_watch_percent"
	SettingWatchDisk       = "operations.watch_disk_percent"
	SettingWatchDatabase   = "operations.watch_database_percent"
	SettingWatchFiles      = "operations.watch_files_percent"
	SettingWatchArtifacts  = "operations.watch_artifacts_percent"
	SettingWatchReserve    = "operations.watch_reserve_percent"
	SettingWatchRunBudget  = "operations.watch_run_budget_percent"

	// settingCapWarn is the cap's own warning threshold, which is the cap's
	// watch level, so the two never disagree.
	settingCapWarn = "usage.cap_warn_percent"
)

// The bounds a setting is held to. A value outside them is taken as the
// nearest bound, so a report is never refused for a setting.
var (
	everyBounds  = [2]time.Duration{time.Minute, 24 * time.Hour}
	keepBounds   = [2]time.Duration{time.Hour, 366 * 24 * time.Hour}
	trendBounds  = [2]time.Duration{time.Hour, 90 * 24 * time.Hour}
	healthBounds = [2]time.Duration{5 * time.Minute, 30 * 24 * time.Hour}
	stuckBounds  = [2]time.Duration{time.Minute, 7 * 24 * time.Hour}
	shownBounds  = [2]int{1, 100}
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

// Settings are the settings one report reads, as they stand for a tenant.
type Settings struct {
	TrendWindow  time.Duration
	HealthWindow time.Duration
	StuckAfter   time.Duration
	JobsShown    int
	TracesShown  int
	// ModelErrorWatch is the share of a model's calls, in percent, that
	// may fail before its health reads as errors.
	ModelErrorWatch int
	// Watch is each limit's watch level, in percent, by its key; a run's
	// budget is under LimitRun.
	Watch map[string]int
	// Room is the room set for each folder, in bytes, by its key.
	Room map[string]int64
}

// ReadSettings reads a report's settings for a tenant. A stored value that
// does not read is an error; a value outside its bounds is taken as the
// nearest bound.
func ReadSettings(ctx context.Context, s *tenant.Store, reg []settings.Setting) (Settings, error) {
	out := Settings{Watch: map[string]int{}, Room: map[string]int64{}}
	var errs []error
	get := func(key string) string {
		v, err := tenantsettings.Get(ctx, s, reg, key)
		if err != nil {
			errs = append(errs, fmt.Errorf("operations: %s: %w", key, err))
		}
		return v
	}
	dur := func(key string, b [2]time.Duration) time.Duration {
		d, err := time.ParseDuration(get(key))
		if err != nil {
			errs = append(errs, fmt.Errorf("operations: %s is not a length of time", key))
		}
		return clampDuration(d, b)
	}
	num := func(key string, b [2]int) int {
		n, err := strconv.Atoi(get(key))
		if err != nil {
			errs = append(errs, fmt.Errorf("operations: %s is not a whole number", key))
		}
		return min(max(n, b[0]), b[1])
	}
	out.TrendWindow = dur(SettingTrendWindow, trendBounds)
	out.HealthWindow = dur(SettingHealthWindow, healthBounds)
	out.StuckAfter = dur(SettingStuckAfter, stuckBounds)
	out.JobsShown = num(SettingJobsShown, shownBounds)
	out.TracesShown = num(SettingTracesShown, shownBounds)
	out.ModelErrorWatch = num(SettingModelErrorWatch, [2]int{1, 100})
	for key, setting := range map[string]string{
		LimitDisk: SettingWatchDisk, LimitDatabase: SettingWatchDatabase, LimitFiles: SettingWatchFiles,
		LimitArtifacts: SettingWatchArtifacts, LimitReserve: SettingWatchReserve, LimitRun: SettingWatchRunBudget,
	} {
		out.Watch[key] = num(setting, [2]int{1, 100})
	}
	for key, setting := range map[string]string{LimitDatabase: SettingDatabaseRoom, LimitFiles: SettingFilesRoom, LimitArtifacts: SettingArtifactsRoom} {
		out.Room[key] = int64(num(setting, [2]int{1, 1 << 30})) << 20
	}
	if err := errors.Join(errs...); err != nil {
		return Settings{}, err
	}
	return out, nil
}

func clampDuration(d time.Duration, b [2]time.Duration) time.Duration {
	return min(max(d, b[0]), b[1])
}
