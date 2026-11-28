package adminview

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/figures"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// ErrPeriod is a period that cannot be read: a day that is not a date, an
// end before its start, or more days than usage.report_max_days.
var ErrPeriod = errors.New("adminview: the period cannot be read")

// Period is the days a usage read covers, from the first moment of From to
// the end of To, in UTC.
type Period struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// The settings a period reads, the usage report's own.
const settingMaxDays = "usage.report_max_days"

// ResolvePeriod reads a period from two dates as 2026-11-01, either empty:
// an empty start is the first of the month of now, and an empty end is
// today. It returns the period and its first moment and the moment after
// its last day.
func ResolvePeriod(from, to string, maxDays int, now time.Time) (Period, time.Time, time.Time, error) {
	today := now.UTC().Truncate(24 * time.Hour)
	start := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := today
	var err error
	if from != "" {
		if start, err = time.Parse("2006-01-02", from); err != nil {
			return Period{}, time.Time{}, time.Time{}, fmt.Errorf("%w: from is a date such as 2026-11-01", ErrPeriod)
		}
	}
	if to != "" {
		if end, err = time.Parse("2006-01-02", to); err != nil {
			return Period{}, time.Time{}, time.Time{}, fmt.Errorf("%w: to is a date such as 2026-11-30", ErrPeriod)
		}
	}
	switch {
	case end.Before(start):
		return Period{}, time.Time{}, time.Time{}, fmt.Errorf("%w: it ends before it starts", ErrPeriod)
	case maxDays > 0 && int(end.Sub(start).Hours()/24)+1 > maxDays:
		return Period{}, time.Time{}, time.Time{}, fmt.Errorf("%w: it is longer than %d days", ErrPeriod, maxDays)
	}
	return Period{From: start.Format("2006-01-02"), To: end.Format("2006-01-02")}, start, end.AddDate(0, 0, 1), nil
}

// PersonUsage is one person's model calls over a period, their failures
// by kind, and their storage over the period: numbers, never words.
type PersonUsage struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Period Period `json:"period"`
	figures.Use
	// Failures counts what failed by kind: model_error, model_refused,
	// task_failed and schedule_start_failed. None carries its words.
	Failures map[string]int `json:"failures"`
	// Storage is their database, files and artifacts at each snapshot of
	// the period, oldest first.
	Storage []figures.StoragePoint `json:"storage"`
}

// usageRead is what a usage read reads, as the person's log records it.
var usageRead = []string{"usage", "failures", "storage"}

// maxDays reads usage.report_max_days as the registry ships it or the
// person has it.
func maxDays(reg []settings.Setting) int {
	if st, ok := settings.Lookup(reg, settingMaxDays); ok {
		var n int
		fmt.Sscanf(st.Default, "%d", &n)
		return n
	}
	return 0
}

// Usage reads one person's usage over a period given as two dates, either
// empty (this month to today).
func (v *View) Usage(ctx context.Context, a Asker, id, from, to string) (PersonUsage, error) {
	acc, err := v.check(a)
	if err != nil {
		return PersonUsage{}, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return PersonUsage{}, err
	}
	p, start, end, err := ResolvePeriod(from, to, maxDays(reg), v.now())
	if err != nil {
		return PersonUsage{}, err
	}
	for _, t := range v.in.Tenants() {
		if t.ID != id {
			continue
		}
		out := PersonUsage{ID: t.ID, Name: t.Name, Period: p}
		err := v.with(ctx, a, id, Touch{By: acc.Name, Read: usageRead}, func(s *tenant.Store) (err error) {
			if out.Use, err = figures.Usage(ctx, s, start, end); err != nil {
				return err
			}
			if out.Failures, err = figures.Failures(ctx, s, out.Use, start, end); err != nil {
				return err
			}
			out.Storage, err = figures.Storage(ctx, s, start, end)
			return err
		})
		return out, err
	}
	return PersonUsage{}, fmt.Errorf("%w: %s", ErrNoPerson, id)
}

// Install is the whole install this month: everyone's calls and spend, by
// model and by day, the sum of each person's; failures by kind; the disk;
// the people; the version; and the sampler's state. The install settings
// that were changed come with P15-03.
type Install struct {
	At     time.Time `json:"at"`
	Period Period    `json:"period"`
	figures.Use
	Failures map[string]int `json:"failures"`
	// CapMicros is the sum of every person's monthly cap that reads.
	CapMicros int64 `json:"cap_micros"`
	// Disk is the volume that holds the data folder.
	Disk   figures.Disk  `json:"disk"`
	People InstallPeople `json:"people"`
	// Version is the server's build.
	Version string  `json:"version"`
	Sampler Sampler `json:"sampler"`
}

// InstallPeople counts the people on the install.
type InstallPeople struct {
	Total    int `json:"total"`
	Enabled  int `json:"enabled"`
	Disabled int `json:"disabled"`
	// Waiting is how many have not set a password yet.
	Waiting int `json:"waiting"`
}

// Sampler is the state of Operations' sampler, which records every
// person's limits: how often, and the newest snapshot across people.
type Sampler struct {
	Every  string    `json:"every"`
	Newest time.Time `json:"newest,omitzero"`
}

// installRead is what the install's read reads of each person.
var installRead = []string{"usage", "failures", "spend", "enabled", "has_password", "snapshots"}

// version is the build of the binary, from what Go records in it.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	v := info.Main.Version
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 12 {
			v = strings.TrimSpace(v + " " + s.Value[:12])
		}
	}
	if v == "" {
		v = "unknown"
	}
	return v
}

// Install reads the whole install for this month. Each person's log gets
// the read.
func (v *View) Install(ctx context.Context, a Asker) (Install, error) {
	acc, err := v.check(a)
	if err != nil {
		return Install{}, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return Install{}, err
	}
	now := v.now()
	p, start, end, err := ResolvePeriod("", "", 0, now)
	if err != nil {
		return Install{}, err
	}
	out := Install{At: now.UTC(), Period: p, Use: figures.Use{Days: []figures.Day{}, Models: []figures.ModelUse{}}, Failures: map[string]int{},
		Version: version(), Sampler: Sampler{Every: v.o.SampleEvery.String()}}
	watch := 90
	if st, ok := settings.Lookup(reg, figures.SettingWatchDisk); ok {
		fmt.Sscanf(st.Default, "%d", &watch)
	}
	out.Disk = figures.ReadDisk(v.in.Dir(), watch)
	for _, t := range v.in.Tenants() {
		err := v.with(ctx, a, t.ID, Touch{By: acc.Name, Read: installRead}, func(s *tenant.Store) error {
			use, err := figures.Usage(ctx, s, start, end)
			if err != nil {
				return err
			}
			fails, err := figures.Failures(ctx, s, use, start, end)
			if err != nil {
				return err
			}
			c, _, err := figures.Standing(ctx, s, reg, monthOf(now))
			if err != nil {
				return err
			}
			_, off, err := s.Disabled(ctx)
			if err != nil {
				return err
			}
			has, err := signin.HasPassword(s)
			if err != nil {
				return err
			}
			newest, ok, err := figures.Newest(ctx, s)
			if err != nil {
				return err
			}
			out.Use.Merge(use)
			for k, n := range fails {
				out.Failures[k] += n
			}
			if c.State != figures.CapUnread {
				out.CapMicros += c.LimitMicros
			}
			out.People.Total++
			if off {
				out.People.Disabled++
			} else {
				out.People.Enabled++
			}
			if !has {
				out.People.Waiting++
			}
			if ok && newest.After(out.Sampler.Newest) {
				out.Sampler.Newest = newest
			}
			return nil
		})
		if err != nil {
			return Install{}, err
		}
	}
	return out, nil
}
