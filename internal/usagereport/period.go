package usagereport

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The periods a report may be asked for by name. A period can also be one
// calendar month, or a range of days.
const (
	PeriodThisMonth  = "this_month"
	PeriodLastMonth  = "last_month"
	PeriodLast7Days  = "last_7_days"
	PeriodLast30Days = "last_30_days"
	// PeriodMonth is a calendar month that was asked for by its name.
	PeriodMonth = "month"
	// PeriodRange is a range of days that was asked for by its first and
	// last day.
	PeriodRange = "range"
)

// Presets returns the periods that are asked for by name, in the order a
// picker lists them. usage.report_period is one of them.
func Presets() []string {
	return []string{PeriodThisMonth, PeriodLastMonth, PeriodLast7Days, PeriodLast30Days}
}

// ErrPeriod is the error for a period that cannot be read. The route
// answers it with 400.
var ErrPeriod = errors.New("usage report")

// Period is the days a report covers, in UTC.
type Period struct {
	// Name is one of the presets, month or range.
	Name string `json:"name"`
	// From is the first day and To the last, both included, as 2026-11-01.
	From string `json:"from"`
	To   string `json:"to"`
	// Month is the calendar month the period is, as 2026-11, when it is
	// exactly one, and empty otherwise. The cap is about this month, or
	// about the month of now when it is empty.
	Month string `json:"month,omitempty"`

	start, end time.Time // the records read are those at or after start and before end
}

// Ask is what a report is asked for. The zero Ask is the default period
// with the default number of rows.
type Ask struct {
	// Period is a preset's name. Empty means the person's
	// usage.report_period, unless Month or From and To are given.
	Period string
	// Month is a calendar month, as 2026-11.
	Month string
	// From and To are the first and the last day of a range, both
	// included, as 2026-11-01.
	From, To string
	// Rows is the most rows a breakdown shows before the rest are added up
	// in one row. Zero means the person's usage.report_rows.
	Rows int
}

const dayLayout = "2006-01-02"

var monthShape = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

func bad(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrPeriod}, args...)...)
}

// monthPeriod is the calendar month that starts at first.
func monthPeriod(name string, first time.Time) Period {
	end := first.AddDate(0, 1, 0)
	return Period{
		Name: name, From: first.Format(dayLayout), To: end.AddDate(0, 0, -1).Format(dayLayout),
		Month: first.Format("2006-01"), start: first, end: end,
	}
}

// dayPeriod is the days from first to last, both included.
func dayPeriod(name string, first, last time.Time) Period {
	p := Period{Name: name, From: first.Format(dayLayout), To: last.Format(dayLayout), start: first, end: last.AddDate(0, 0, 1)}
	if first.Day() == 1 && p.end.Day() == 1 && p.end.Equal(first.AddDate(0, 1, 0)) {
		p.Month = first.Format("2006-01")
	}
	return p
}

// Resolve works out the days a report covers. preset is used when the ask
// names no period, and maxDays bounds a range. Days are calendar days in
// UTC, as the cap's month is (decision 0162).
func Resolve(a Ask, preset string, maxDays int, now time.Time) (Period, error) {
	given := 0
	for _, set := range []bool{a.Period != "", a.Month != "", a.From != "" || a.To != ""} {
		if set {
			given++
		}
	}
	if given > 1 {
		return Period{}, bad("ask for a named period, a month, or a range of days, and only one of them")
	}
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	switch {
	case a.Month != "":
		if !monthShape.MatchString(a.Month) {
			return Period{}, bad("%q is not a month such as 2026-11", a.Month)
		}
		first, err := time.Parse("2006-01", a.Month)
		if err != nil {
			return Period{}, bad("%q is not a month such as 2026-11", a.Month)
		}
		return monthPeriod(PeriodMonth, first), nil
	case a.From != "" || a.To != "":
		first, err1 := time.Parse(dayLayout, a.From)
		last, err2 := time.Parse(dayLayout, a.To)
		switch {
		case err1 != nil || err2 != nil:
			return Period{}, bad("a range is a first and a last day, such as 2026-11-01 and 2026-11-15")
		case last.Before(first):
			return Period{}, bad("the last day of a range comes before its first")
		case maxDays < 1:
			return Period{}, errors.New("usage report: the longest range must be 1 day or more")
		case int(last.Sub(first).Hours()/24)+1 > maxDays:
			return Period{}, bad("a range covers at most %d days", maxDays)
		}
		return dayPeriod(PeriodRange, first, last), nil
	}
	name := a.Period
	if name == "" {
		name = preset
	}
	switch name {
	case PeriodThisMonth:
		return monthPeriod(name, thisMonth), nil
	case PeriodLastMonth:
		return monthPeriod(name, thisMonth.AddDate(0, -1, 0)), nil
	case PeriodLast7Days:
		return dayPeriod(name, today.AddDate(0, 0, -6), today), nil
	case PeriodLast30Days:
		return dayPeriod(name, today.AddDate(0, 0, -29), today), nil
	}
	if a.Period == "" {
		// A stored value is checked against the options when it is set, so
		// this is a registry that changed under it.
		return Period{}, fmt.Errorf("usage report: the default period %q is not one of %s", preset, strings.Join(Presets(), ", "))
	}
	return Period{}, bad("%q is not a period: ask for %s, a month, or a range of days", name, strings.Join(Presets(), ", "))
}
