package figures

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// The states of the monthly cap, as the usage report names them.
const (
	CapOK      = "ok"
	CapWarning = "warning"
	CapReached = "reached"
	// CapUnread: the cap's settings do not read, so every call to a model
	// that is not local is stopped.
	CapUnread = "unread"
)

// Cap is where a calendar month stands against the monthly cap. It is the
// usage report's own Cap, field for field, and a test holds the two equal.
type Cap struct {
	Month        string `json:"month"`
	SpentMicros  int64  `json:"spent_micros"`
	LimitMicros  int64  `json:"limit_micros"`
	WarnPercent  int    `json:"warn_percent"`
	WarnAtMicros int64  `json:"warn_at_micros"`
	LeftMicros   int64  `json:"left_micros"`
	State        string `json:"state"`
	Problem      string `json:"problem,omitempty"`
}

// Reserve is what the memory pass spent of its reserve in the month.
type Reserve struct {
	Month       string `json:"month"`
	SpentMicros int64  `json:"spent_micros"`
	LimitMicros int64  `json:"limit_micros"`
	LeftMicros  int64  `json:"left_micros"`
	Problem     string `json:"problem,omitempty"`
}

// Standing is where a calendar month stands against the monthly cap and
// the memory reserve, summed from the usage records as the gates sum them.
// A cap or a reserve whose settings do not read is reported, not an error.
func Standing(ctx context.Context, s *tenant.Store, reg []settings.Setting, month string) (Cap, Reserve, error) {
	spent, err := usage.Spent(ctx, s, month)
	if err != nil {
		return Cap{}, Reserve{}, err
	}
	c := Cap{Month: month, SpentMicros: spent}
	limitText, err1 := tenantsettings.Get(ctx, s, reg, SettingCap)
	warnText, err2 := tenantsettings.Get(ctx, s, reg, SettingCapWarn)
	limit, err3 := strconv.ParseFloat(limitText, 64)
	warn, err4 := strconv.Atoi(warnText)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		c.State, c.Problem = CapUnread, "the monthly cap's settings do not read, so every call to a model that is not local is stopped"
	} else if uc, err := usage.NewCap(limit, warn); err != nil {
		c.State, c.Problem = CapUnread, err.Error()
	} else {
		st := usage.Judge(spent, 0, uc)
		c.LimitMicros, c.WarnPercent, c.WarnAtMicros = uc.LimitMicros, uc.WarnPercent, st.WarnAtMicros
		c.LeftMicros = max(uc.LimitMicros-spent, 0)
		switch {
		case spent >= uc.LimitMicros:
			c.State = CapReached
		case st.State == usage.StateWarning:
			c.State = CapWarning
		default:
			c.State = CapOK
		}
	}

	memSpent, err := usage.SpentOn(ctx, s, month, KindMemory)
	if err != nil {
		return Cap{}, Reserve{}, err
	}
	r := Reserve{Month: month, SpentMicros: memSpent}
	text, err := tenantsettings.Get(ctx, s, reg, SettingReserve)
	usd, perr := strconv.ParseFloat(text, 64)
	if err := errors.Join(err, perr); err != nil || math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0 || usd > 1e9 {
		r.Problem = "the memory pass's reserve does not read, so every memory call to a model that is not local is stopped"
	} else {
		r.LimitMicros = int64(math.Round(usd * 1e6))
		r.LeftMicros = max(r.LimitMicros-memSpent, 0)
	}
	return c, r, nil
}

// Reading is one limit as measured now, before a trend: what it counts,
// what is used of what, its watch level, and why it could not be read.
type Reading struct {
	Key   string
	Label string
	Note  string
	Scope string
	Unit  string
	Used  int64
	Of    int64
	Watch int
	// Problem says why it could not be read.
	Problem string
	// Since is the earliest time its trend may reach back to: the start of
	// the month for the cap and the reserve. Zero for the others.
	Since time.Time
	// Resets is when it starts again, for the cap and the reserve.
	Resets *time.Time
}

// Measure reads where each of a person's own limits stands now: the
// monthly cap, the memory reserve, their database, files and artifacts.
// Since P15-02 the disk is not one of them: it is the install's, and only
// the admin account shows it (decision 0701).
func Measure(ctx context.Context, s *tenant.Store, reg []settings.Setting, cfg Settings, now time.Time) ([]Reading, error) {
	month := usage.MonthOf(now)
	monthStart, err := time.Parse("2006-01", month)
	if err != nil {
		return nil, err
	}
	next := monthStart.AddDate(0, 1, 0)
	c, r, err := Standing(ctx, s, reg, month)
	if err != nil {
		return nil, err
	}
	capR := Reading{Key: LimitCap, Label: "Monthly cap", Scope: ScopeTenant, Unit: UnitMicros,
		Note: "What calls to hosted and trusted models cost this month, a calendar month in UTC.",
		Used: c.SpentMicros, Of: c.LimitMicros, Watch: c.WarnPercent, Since: monthStart, Resets: &next}
	if c.State == CapUnread {
		capR.Problem = c.Problem
		if w, err := tenantsettings.Get(ctx, s, reg, SettingCapWarn); err == nil {
			capR.Watch, _ = strconv.Atoi(w)
		}
	}
	out := []Reading{capR, {Key: LimitReserve, Label: "Memory reserve", Scope: ScopeTenant, Unit: UnitMicros,
		Note: "What the memory pass spent this month on models that are not local, a part of the monthly cap.",
		Used: r.SpentMicros, Of: r.LimitMicros, Watch: cfg.Watch[LimitReserve], Problem: r.Problem, Since: monthStart, Resets: &next}}

	dbFile, err := DatabaseFile(ctx, s.DB())
	if err != nil {
		return nil, err
	}
	db := Reading{Key: LimitDatabase, Label: "Your database", Scope: ScopeTenant, Unit: UnitBytes,
		Note: "keystone.db and its write-ahead log: your chats, settings, usage and audit log, against the size it is planned to grow to.",
		Of:   cfg.Room[LimitDatabase], Watch: cfg.Watch[LimitDatabase]}
	if db.Used, err = DatabaseSize(ctx, s.DB(), dbFile); err != nil {
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
		x := Reading{Key: f.key, Label: f.label, Note: f.note, Scope: ScopeTenant, Unit: UnitBytes, Of: cfg.Room[f.key], Watch: cfg.Watch[f.key]}
		if f.root == nil {
			x.Problem = "the folder is not open"
		} else if x.Used, err = FolderSize(f.root); err != nil {
			x.Problem = "the folder's size could not be read: " + err.Error()
		}
		out = append(out, x)
	}
	return out, nil
}

// DatabaseFile is the path of the person's database file, or "" when it is
// not a file.
func DatabaseFile(ctx context.Context, db *sql.DB) (string, error) {
	var file string
	err := db.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&file)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return file, err
}

// DatabaseSize is the size of the database file and its write-ahead log,
// or the size SQLite counts in pages when it is not a file.
func DatabaseSize(ctx context.Context, db *sql.DB, file string) (int64, error) {
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

// FolderSize adds up the size of every file under a folder, as it is on
// disk: for a sealed person, the sealed sizes.
func FolderSize(root *os.Root) (int64, error) {
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
