package usagereport

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// The settings a report reads. The cap's two are the ones the turn reads
// before a paid call (internal/turn/route.go), and the reserve is the one
// the memory pass reads, so the report shows what the gates enforce.
const (
	SettingPeriod  = "usage.report_period"
	SettingRows    = "usage.report_rows"
	SettingMaxDays = "usage.report_max_days"

	settingCap  = "usage.monthly_cap_usd"
	settingWarn = "usage.cap_warn_percent"
)

// The states of the cap.
const (
	// CapOK: under the warning threshold.
	CapOK = "ok"
	// CapWarning: at or past the warning threshold, and under the cap.
	CapWarning = "warning"
	// CapReached: the month's spend has reached the cap, so a call to a
	// model that is not local is stopped.
	CapReached = "reached"
	// CapUnread: the cap's settings do not read, so every call to a model
	// that is not local is stopped.
	CapUnread = "unread"
)

// Cap is where a calendar month stands against the monthly cap, with the
// cap and its threshold as they are set now.
type Cap struct {
	// Month is the calendar month in UTC: the report's period when it is
	// one month, and the month of now otherwise.
	Month string `json:"month"`
	// SpentMicros is what the month's calls to models that are not local
	// cost, summed from the usage records as the gate sums them.
	SpentMicros  int64 `json:"spent_micros"`
	LimitMicros  int64 `json:"limit_micros"`
	WarnPercent  int   `json:"warn_percent"`
	WarnAtMicros int64 `json:"warn_at_micros"`
	// LeftMicros is what may still be spent this month, and never less
	// than nothing.
	LeftMicros int64 `json:"left_micros"`
	// State is ok, warning, reached or unread.
	State string `json:"state"`
	// Problem says why the cap does not read, when State is unread.
	Problem string `json:"problem,omitempty"`
}

// Reserve is what the memory pass has spent of its reserve in the cap's
// month. The reserve is a part of the cap (decision 0280).
type Reserve struct {
	Month       string `json:"month"`
	SpentMicros int64  `json:"spent_micros"`
	LimitMicros int64  `json:"limit_micros"`
	LeftMicros  int64  `json:"left_micros"`
	// Problem says why the reserve does not read: every memory call to a
	// model that is not local is then stopped.
	Problem string `json:"problem,omitempty"`
}

// Report is a tenant's usage for a period.
type Report struct {
	Period Period `json:"period"`
	Breakdowns
	Cap     Cap     `json:"cap"`
	Reserve Reserve `json:"memory_reserve"`
}

// Config is the report's own settings, as they stand for one tenant.
type Config struct {
	Period  string
	Rows    int
	MaxDays int
}

// ReadConfig reads the report's own settings for a tenant. A stored value
// that does not read is an error; a number of rows below one is taken as
// one.
func ReadConfig(ctx context.Context, s *tenant.Store, reg []settings.Setting) (Config, error) {
	var c Config
	var err error
	get := func(key string) string {
		if err != nil {
			return ""
		}
		var v string
		if v, err = tenantsettings.Get(ctx, s, reg, key); err != nil {
			err = fmt.Errorf("usage report: %s: %w", key, err)
		}
		return v
	}
	c.Period = get(SettingPeriod)
	rows, maxDays := get(SettingRows), get(SettingMaxDays)
	if err != nil {
		return Config{}, err
	}
	if c.Rows, err = strconv.Atoi(rows); err != nil {
		return Config{}, fmt.Errorf("usage report: %s is not a whole number", SettingRows)
	}
	if c.Rows < 1 {
		c.Rows = 1
	}
	if c.MaxDays, err = strconv.Atoi(maxDays); err != nil || c.MaxDays < 1 {
		return Config{}, fmt.Errorf("usage report: %s must be a whole number of days, 1 or more", SettingMaxDays)
	}
	return c, nil
}

// Read is a tenant's report for what was asked, at now: the one function
// behind GET /api/v1/usage and tools/tenant usage. An ask that cannot be
// read is an error that wraps ErrPeriod.
func Read(ctx context.Context, s *tenant.Store, reg []settings.Setting, a Ask, now time.Time) (Report, error) {
	c, err := ReadConfig(ctx, s, reg)
	if err != nil {
		return Report{}, err
	}
	p, err := Resolve(a, c.Period, c.MaxDays, now)
	if err != nil {
		return Report{}, err
	}
	rows := c.Rows
	if a.Rows > 0 {
		rows = a.Rows
	}
	recs, err := Records(ctx, s, p)
	if err != nil {
		return Report{}, err
	}
	b, err := Summarise(ctx, s, recs, rows)
	if err != nil {
		return Report{}, err
	}
	month := p.Month
	if month == "" {
		month = usage.MonthOf(now)
	}
	out := Report{Period: p, Breakdowns: b}
	if out.Cap, out.Reserve, err = Standing(ctx, s, reg, month); err != nil {
		return Report{}, err
	}
	return out, nil
}

// Standing is where a calendar month stands against the monthly cap and
// the memory reserve: the Cap and the Reserve of a report for that month,
// read the same way, without the period's records and breakdowns. A caller
// that wants only these, as Operations does for its limits and the home
// screen's notice, reads two sums through the month's index, where a whole
// report places every record of the month by its chat (P13-03).
func Standing(ctx context.Context, s *tenant.Store, reg []settings.Setting, month string) (Cap, Reserve, error) {
	c, err := readCap(ctx, s, reg, month)
	if err != nil {
		return Cap{}, Reserve{}, err
	}
	r, err := readReserve(ctx, s, reg, month)
	if err != nil {
		return Cap{}, Reserve{}, err
	}
	return c, r, nil
}

// readCap is the month against the cap. A cap whose settings do not read
// is reported, not an error: the gate stops every paid call then.
func readCap(ctx context.Context, s *tenant.Store, reg []settings.Setting, month string) (Cap, error) {
	spent, err := usage.Spent(ctx, s, month)
	if err != nil {
		return Cap{}, err
	}
	out := Cap{Month: month, SpentMicros: spent}
	limitText, err1 := tenantsettings.Get(ctx, s, reg, settingCap)
	warnText, err2 := tenantsettings.Get(ctx, s, reg, settingWarn)
	limit, err3 := strconv.ParseFloat(limitText, 64)
	warn, err4 := strconv.Atoi(warnText)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		out.State, out.Problem = CapUnread, "the monthly cap's settings do not read, so every call to a model that is not local is stopped"
		return out, nil
	}
	c, err := usage.NewCap(limit, warn)
	if err != nil {
		out.State, out.Problem = CapUnread, err.Error()
		return out, nil
	}
	st := usage.Judge(spent, 0, c)
	out.LimitMicros, out.WarnPercent, out.WarnAtMicros = c.LimitMicros, c.WarnPercent, st.WarnAtMicros
	out.LeftMicros = max(c.LimitMicros-spent, 0)
	switch {
	case spent >= c.LimitMicros:
		out.State = CapReached
	case st.State == usage.StateWarning:
		out.State = CapWarning
	default:
		out.State = CapOK
	}
	return out, nil
}

// readReserve is the memory pass's spend in the month against its
// reserve, read as the memory pass reads it.
func readReserve(ctx context.Context, s *tenant.Store, reg []settings.Setting, month string) (Reserve, error) {
	spent, err := usage.SpentOn(ctx, s, month, memory.Kind)
	if err != nil {
		return Reserve{}, err
	}
	out := Reserve{Month: month, SpentMicros: spent}
	text, err := tenantsettings.Get(ctx, s, reg, memory.SettingReserve)
	usd, perr := strconv.ParseFloat(text, 64)
	limit, rerr := memory.NewReserve(usd)
	if err := errors.Join(err, perr, rerr); err != nil {
		out.Problem = "the memory pass's reserve does not read, so every memory call to a model that is not local is stopped"
		return out, nil
	}
	out.LimitMicros, out.LeftMicros = limit, max(limit-spent, 0)
	return out, nil
}
