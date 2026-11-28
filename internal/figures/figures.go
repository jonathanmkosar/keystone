// Package figures is a person's numbers, read one way for everyone who
// shows them (P15-02): the person's own Operations page, and the admin
// account, which sees numbers and never content (decision 0644).
//
// It holds the part of Operations that is only numbers: the report's
// settings and their bounds, the limits' keys, the measure of the monthly
// cap, the memory reserve, the database, the files and the artifacts, the
// judge of a limit's state, the size of the volume a folder is on, the
// usage records of a window and a model's health. internal/operations builds
// its report on these functions, so the page and the admin never read a
// limit two ways. What Operations reads through packages that hold a
// person's words (open tasks, failed and stuck tasks, schedules), figures
// counts from the same rows with narrow queries, and the tests hold each
// count to Operations' own.
//
// It imports no package that holds a person's content, so internal/adminview
// may use it: the admin isolation check holds that. It enforces no fixed
// rule, so it may read settings. docs/architecture/figures.md describes it.
package figures

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

// The limits, by key. A run's budget is LimitRun, a colon, and the run's
// request id.
const (
	LimitCap       = "cap"
	LimitReserve   = "memory_reserve"
	LimitDisk      = "disk"
	LimitDatabase  = "database"
	LimitFiles     = "files"
	LimitArtifacts = "artifacts"
	LimitRun       = "run"
)

// The units a limit is counted in.
const (
	// UnitMicros is millionths of a US dollar.
	UnitMicros = "usd_micros"
	// UnitBytes is bytes.
	UnitBytes = "bytes"
)

// The scopes of a limit.
const (
	// ScopeTenant is the person's own: their spend, their folder, their runs.
	ScopeTenant = "tenant"
	// ScopeInstall is the same for every person on the install: the volume
	// that holds the data folder. Since P15-02 it is shown only in the admin
	// account (decision 0701).
	ScopeInstall = "install"
)

// The states of a limit.
const (
	// StateOK is under its watch level.
	StateOK = "ok"
	// StateWatch is at or past its watch level, and under the limit.
	StateWatch = "watch"
	// StateFull is at or past the limit.
	StateFull = "full"
	// StateUnknown is a limit that could not be read.
	StateUnknown = "unknown"
)

// The settings a person's figures read, as they stand for that person. The
// rooms are install settings, read through the person's settings like the
// usage report's own install setting.
const (
	SettingDatabaseRoom    = "operations.database_room_mb"
	SettingFilesRoom       = "operations.files_room_mb"
	SettingArtifactsRoom   = "operations.artifacts_room_mb"
	SettingTrendWindow     = "operations.trend_window"
	SettingTrendMinSpan    = "operations.trend_min_span"
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

	// SettingCap and SettingCapWarn are the monthly cap and its warning
	// threshold, which is the cap's watch level, so the two never disagree.
	// They are the ones the turn reads before a paid call.
	SettingCap     = "usage.monthly_cap_usd"
	SettingCapWarn = "usage.cap_warn_percent"
	// SettingReserve is the memory pass's reserve, and KindMemory the kind
	// its calls are recorded under: internal/knowledge/memory's own, which
	// figures may not import. A test holds them equal.
	SettingReserve = "knowledge.memory_reserve_usd"
	KindMemory     = "memory"
)

// The bounds a setting is held to. A value outside them is taken as the
// nearest bound, so a report is never refused for a setting.
var (
	trendBounds  = [2]time.Duration{time.Hour, 90 * 24 * time.Hour}
	spanBounds   = [2]time.Duration{0, 30 * 24 * time.Hour}
	healthBounds = [2]time.Duration{5 * time.Minute, 30 * 24 * time.Hour}
	stuckBounds  = [2]time.Duration{time.Minute, 7 * 24 * time.Hour}
	shownBounds  = [2]int{1, 100}
)

// Settings are the settings a person's figures read, as they stand for
// that person.
type Settings struct {
	TrendWindow time.Duration
	// TrendMinSpan is how long a limit's snapshots must span before its
	// trend is told; a run's budget is told from its own calls.
	TrendMinSpan time.Duration
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

// ReadSettings reads a person's settings for their figures. A stored value
// that does not read is an error; a value outside its bounds is taken as
// the nearest bound.
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
		return ClampDuration(d, b)
	}
	num := func(key string, b [2]int) int {
		n, err := strconv.Atoi(get(key))
		if err != nil {
			errs = append(errs, fmt.Errorf("operations: %s is not a whole number", key))
		}
		return min(max(n, b[0]), b[1])
	}
	out.TrendWindow = dur(SettingTrendWindow, trendBounds)
	out.TrendMinSpan = dur(SettingTrendMinSpan, spanBounds)
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

// ClampDuration holds a length of time to its bounds.
func ClampDuration(d time.Duration, b [2]time.Duration) time.Duration {
	return min(max(d, b[0]), b[1])
}

// Judge is a limit's share, state and whether its watch level is crossed,
// from what is used, the limit, the watch level and why it could not be
// read. Percent is rounded down and may pass 100; a limit of nothing that
// holds something is 100, and a cap of nothing is full, since it stops
// every paid call. A monthly cap that does not read is crossed, as if it
// were full.
func Judge(key string, used, of int64, watch int, problem string) (percent int, state string, crossed bool) {
	switch {
	case problem != "":
		return 0, StateUnknown, key == LimitCap
	case of <= 0:
		if used > 0 || key == LimitCap {
			percent = 100
		}
	default:
		percent = int(min(float64(used)*100/float64(of), 1e6))
	}
	switch {
	case percent >= 100 || (of > 0 && used >= of):
		state = StateFull
	case percent >= watch:
		state = StateWatch
	default:
		state = StateOK
	}
	return percent, state, state != StateOK
}
