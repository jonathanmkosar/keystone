package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/internal/usagereport"
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
	// that holds the data folder. It says nothing of any other person.
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
	// StateUnknown is a limit that could not be read. Problem says why.
	StateUnknown = "unknown"
)

// Limit is one place the install may run out of room, as it stands now.
type Limit struct {
	// Key is cap, memory_reserve, disk, database, files, artifacts, or run
	// and a run's request id after a colon.
	Key string `json:"key"`
	// Label names it for a person, and Note says what it counts.
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
	// Scope is tenant or install, and Unit usd_micros or bytes.
	Scope string `json:"scope"`
	Unit  string `json:"unit"`
	// Used is what is used, and Of the limit, in Unit.
	Used int64 `json:"used"`
	Of   int64 `json:"of"`
	// Percent is Used of Of, in whole percent, rounded down; it may pass
	// 100. A limit of nothing that holds something is 100.
	Percent int `json:"percent"`
	// Watch is the watch level, in percent of Of.
	Watch int `json:"watch"`
	// State is ok, watch, full or unknown, and Crossed says the watch level
	// is reached: the state is watch or full, or the monthly cap does not
	// read, which stops every paid call.
	State   string `json:"state"`
	Crossed bool   `json:"crossed"`
	Problem string `json:"problem,omitempty"`
	// Trend is how it has moved: from the snapshots, or for a run's budget
	// from its usage records.
	Trend Trend `json:"trend"`
	// ChatID and RunID name a run's chat and its task, for a run's budget.
	ChatID string `json:"chat_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`
}

// reading is a limit as measured, before its trend and its state.
type reading struct {
	Limit
	// since is the earliest time its trend may reach back to: the start of
	// the month for the cap and the reserve.
	since time.Time
	// resets is when the limit starts again, for the cap and the reserve.
	resets *time.Time
}

// measure reads where every limit but the runs' stands now: the ones the
// sampler records.
func measure(ctx context.Context, s *tenant.Store, reg []settings.Setting, cfg Settings, now time.Time) ([]reading, error) {
	var out []reading
	month := usage.MonthOf(now)
	monthStart, err := time.Parse("2006-01", month)
	if err != nil {
		return nil, err
	}
	next := monthStart.AddDate(0, 1, 0)

	// The month against the cap, and the memory reserve, as the usage
	// report reads them: the same function, so the two never disagree.
	// Standing is the report's cap and reserve without its breakdowns,
	// which place every record of the month and are not shown here
	// (P13-03: the home screen reads this each time it is drawn).
	var rep usagereport.Report
	rep.Cap, rep.Reserve, err = usagereport.Standing(ctx, s, reg, month)
	if err != nil {
		return nil, err
	}
	c := reading{since: monthStart, resets: &next}
	c.Limit = Limit{Key: LimitCap, Label: "Monthly cap", Scope: ScopeTenant, Unit: UnitMicros,
		Note: "What calls to hosted and trusted models cost this month, a calendar month in UTC.",
		Used: rep.Cap.SpentMicros, Of: rep.Cap.LimitMicros, Watch: rep.Cap.WarnPercent}
	if rep.Cap.State == usagereport.CapUnread {
		c.Problem = rep.Cap.Problem
		if w, err := tenantsettings.Get(ctx, s, reg, settingCapWarn); err == nil {
			c.Watch, _ = strconv.Atoi(w)
		}
	}
	out = append(out, c)
	r := reading{since: monthStart, resets: &next}
	r.Limit = Limit{Key: LimitReserve, Label: "Memory reserve", Scope: ScopeTenant, Unit: UnitMicros,
		Note: "What the memory pass spent this month on models that are not local, a part of the monthly cap.",
		Used: rep.Reserve.SpentMicros, Of: rep.Reserve.LimitMicros, Watch: cfg.Watch[LimitReserve], Problem: rep.Reserve.Problem}
	out = append(out, r)

	// The tenant's own folder, and the volume it is on.
	dbFile, err := databaseFile(ctx, s.DB())
	if err != nil {
		return nil, err
	}
	d := reading{}
	d.Limit = Limit{Key: LimitDisk, Label: "Disk", Scope: ScopeInstall, Unit: UnitBytes, Watch: cfg.Watch[LimitDisk],
		Note: "The volume that holds Keystone's data folder, with everything else on it. The same for every person on this install."}
	if dbFile == "" {
		d.Problem = "the database is not a file, so its volume is not known"
	} else if total, free, err := volume(filepath.Dir(dbFile)); err != nil {
		d.Problem = "the volume's size could not be read: " + err.Error()
	} else {
		d.Used, d.Of = int64(total-min(free, total)), int64(total)
	}
	out = append(out, d)

	db := reading{}
	db.Limit = Limit{Key: LimitDatabase, Label: "Your database", Scope: ScopeTenant, Unit: UnitBytes,
		Note: "keystone.db and its write-ahead log: your chats, settings, usage and audit log, against the size it is planned to grow to.",
		Of:   cfg.Room[LimitDatabase], Watch: cfg.Watch[LimitDatabase]}
	if db.Used, err = databaseSize(ctx, s.DB(), dbFile); err != nil {
		db.Problem = "the database's size could not be read: " + err.Error()
	}
	out = append(out, db)

	for _, f := range []struct {
		key, label, note string
		root             *os.Root
	}{
		{LimitFiles, "Your files", "Uploads and project files, kept by their hash.", s.Files()},
		{LimitArtifacts, "Your artifacts", "Artifact versions.", s.Artifacts()},
	} {
		x := reading{}
		x.Limit = Limit{Key: f.key, Label: f.label, Note: f.note, Scope: ScopeTenant, Unit: UnitBytes, Of: cfg.Room[f.key], Watch: cfg.Watch[f.key]}
		if f.root == nil {
			x.Problem = "the folder is not open"
		} else if x.Used, err = folderSize(f.root); err != nil {
			x.Problem = "the folder's size could not be read: " + err.Error()
		}
		out = append(out, x)
	}
	return out, nil
}

// Limits returns where every limit stands for a tenant now, each with its
// share used, its watch level and its trend: the monthly cap, the memory
// reserve, the disk, the person's database, files and artifacts, and the
// budget of each background task and scheduled run under way.
func Limits(ctx context.Context, s *tenant.Store, reg []settings.Setting, now time.Time) ([]Limit, error) {
	cfg, err := ReadSettings(ctx, s, reg)
	if err != nil {
		return nil, err
	}
	return limits(ctx, s, reg, cfg, now)
}

func limits(ctx context.Context, s *tenant.Store, reg []settings.Setting, cfg Settings, now time.Time) ([]Limit, error) {
	readings, err := measure(ctx, s, reg, cfg, now)
	if err != nil {
		return nil, err
	}
	out := make([]Limit, 0, len(readings))
	for _, r := range readings {
		since := now.Add(-cfg.TrendWindow)
		if r.since.After(since) {
			since = r.since
		}
		points, err := Snapshots(ctx, s, r.Key, since)
		if err != nil {
			return nil, err
		}
		l := r.Limit
		if l.Problem == "" {
			points = append(points, Point{At: now.UTC(), Used: l.Used, Of: l.Of})
		}
		l.Trend = trend(points, l.Used, l.Of, now, r.resets, cfg.TrendMinSpan)
		judge(&l)
		out = append(out, l)
	}
	runs, err := runLimits(ctx, s, cfg, now)
	if err != nil {
		return nil, err
	}
	return append(out, runs...), nil
}

// runLimits is the budget of each background task and scheduled run under
// way, against what it has spent, with its trend from its usage records.
func runLimits(ctx context.Context, s *tenant.Store, cfg Settings, now time.Time) ([]Limit, error) {
	open, err := jobs.List(ctx, s, jobs.Filter{Open: true})
	if err != nil {
		return nil, err
	}
	var out []Limit
	for i := len(open) - 1; i >= 0; i-- { // oldest first
		t := open[i]
		recs, err := usage.List(ctx, s, usage.Filter{RequestID: t.RequestID})
		if err != nil {
			return nil, err
		}
		var spent int64
		var points []Point
		for _, rec := range recs {
			if usage.Counts(rec.Class) {
				spent += rec.CostMicros
				points = append(points, Point{At: rec.At, Used: spent, Of: t.BudgetMicros})
			}
		}
		points = append(points, Point{At: now.UTC(), Used: spent, Of: t.BudgetMicros})
		what := "Background task"
		if t.Scheduled {
			what = "Scheduled task"
		}
		l := Limit{
			Key: LimitRun + ":" + t.RequestID, Label: fmt.Sprintf("%s “%s”", what, t.Title), Scope: ScopeTenant, Unit: UnitMicros,
			Note: "What this run has spent on models that are not local, against its own budget.",
			Used: spent, Of: t.BudgetMicros, Watch: cfg.Watch[LimitRun], ChatID: t.WorkChatID, RunID: t.ID,
		}
		l.Trend = trend(points, l.Used, l.Of, now, nil, 0) // a run is short: its own calls tell its rate
		judge(&l)
		out = append(out, l)
	}
	return out, nil
}

// judge sets a limit's share, state and whether its watch level is
// crossed.
func judge(l *Limit) {
	switch {
	case l.Problem != "":
		l.State, l.Percent = StateUnknown, 0
		// A monthly cap that does not read stops every paid call: the
		// person is told as if it were full.
		l.Crossed = l.Key == LimitCap
		return
	case l.Of <= 0:
		l.Percent = 0
		if l.Used > 0 || l.Key == LimitCap {
			// A cap of nothing stops every call that costs anything.
			l.Percent = 100
		}
	default:
		l.Percent = int(min(float64(l.Used)*100/float64(l.Of), 1e6))
	}
	switch {
	case l.Percent >= 100 || (l.Of > 0 && l.Used >= l.Of):
		l.State = StateFull
	case l.Percent >= l.Watch:
		l.State = StateWatch
	default:
		l.State = StateOK
	}
	l.Crossed = l.State != StateOK
}

// databaseFile is the path of the tenant's database file, or "" when it is
// not a file.
func databaseFile(ctx context.Context, db *sql.DB) (string, error) {
	var file string
	err := db.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&file)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return file, err
}

// databaseSize is the size of the database file and its write-ahead log,
// or the size SQLite counts in pages when it is not a file.
func databaseSize(ctx context.Context, db *sql.DB, file string) (int64, error) {
	if file == "" {
		var pages, size int64
		if err := db.QueryRowContext(ctx, "SELECT c.page_count, z.page_size FROM pragma_page_count() c, pragma_page_size() z").Scan(&pages, &size); err != nil {
			return 0, err
		}
		return pages * size, nil
	}
	info, err := os.Stat(file)
	if err != nil {
		return 0, err
	}
	n := info.Size()
	if wal, err := os.Stat(file + "-wal"); err == nil {
		n += wal.Size()
	}
	return n, nil
}

// folderSize adds up the size of every file under a folder.
func folderSize(root *os.Root) (int64, error) {
	var n int64
	err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // removed while it was walked
			}
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			n += info.Size()
		}
		return nil
	})
	return n, err
}
