package schedules

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The kinds of recurrence.
const (
	// KindHourly runs every hour, at Minute past.
	KindHourly = "hourly"
	// KindDaily runs every day at Time.
	KindDaily = "daily"
	// KindWeekdays runs Monday to Friday at Time.
	KindWeekdays = "weekdays"
	// KindWeekly runs on each of Days at Time.
	KindWeekly = "weekly"
	// KindOnce runs once, on Date at Time.
	KindOnce = "once"
)

// dayNames are the names a weekly recurrence gives its days, Monday first,
// and their Go weekdays.
var dayNames = []struct {
	name string
	day  time.Weekday
}{
	{"mon", time.Monday}, {"tue", time.Tuesday}, {"wed", time.Wednesday}, {"thu", time.Thursday},
	{"fri", time.Friday}, {"sat", time.Saturday}, {"sun", time.Sunday},
}

// Recurrence says when a schedule is due, in the person's time zone.
type Recurrence struct {
	// Kind is hourly, daily, weekdays, weekly or once.
	Kind string `json:"kind"`
	// Time is the time of day, HH:MM on a 24-hour clock, for every kind but
	// hourly.
	Time string `json:"time,omitempty"`
	// Minute is the minute past each hour, 0 to 59, for hourly.
	Minute int `json:"minute,omitempty"`
	// Days are the days of a weekly recurrence: mon, tue, wed, thu, fri,
	// sat, sun.
	Days []string `json:"days,omitempty"`
	// Date is the day of a one-time recurrence, YYYY-MM-DD.
	Date string `json:"date,omitempty"`
}

// rule is a recurrence read: what Next works from.
type rule struct {
	kind         string
	hour, minute int
	days         []time.Weekday // weekly only, in the order of dayNames
	date         time.Time      // once only: the day, at midnight UTC
}

var errRecurrence = errors.New("the recurrence is refused")

// readRule checks a recurrence and reads it. The fields a kind does not use
// are ignored.
func readRule(r Recurrence) (rule, error) {
	bad := func(format string, args ...any) (rule, error) {
		return rule{}, fmt.Errorf("%w: %s", errRecurrence, fmt.Sprintf(format, args...))
	}
	ru := rule{kind: strings.TrimSpace(r.Kind)}
	switch ru.kind {
	case KindHourly:
		if r.Minute < 0 || r.Minute > 59 {
			return bad("an hourly schedule runs at a minute past the hour, 0 to 59")
		}
		ru.minute = r.Minute
		return ru, nil
	case KindDaily, KindWeekdays, KindWeekly, KindOnce:
	default:
		return bad("kind is hourly, daily, weekdays, weekly or once")
	}
	var err error
	if ru.hour, ru.minute, err = clock(r.Time); err != nil {
		return bad("time is the time of day as HH:MM, from 00:00 to 23:59")
	}
	switch ru.kind {
	case KindWeekly:
		want := map[string]bool{}
		for _, d := range r.Days {
			want[strings.ToLower(strings.TrimSpace(d))] = true
		}
		for _, d := range dayNames {
			if want[d.name] {
				ru.days = append(ru.days, d.day)
				delete(want, d.name)
			}
		}
		if len(want) > 0 || len(ru.days) == 0 {
			return bad("a weekly schedule names one or more days, as mon, tue, wed, thu, fri, sat and sun")
		}
	case KindOnce:
		d, err := time.Parse("2006-01-02", strings.TrimSpace(r.Date))
		if err != nil {
			return bad("date is the day of a one-time schedule, as YYYY-MM-DD")
		}
		ru.date = d
	}
	return ru, nil
}

// clock reads HH:MM.
func clock(s string) (hour, minute int, err error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || len(m) != 2 || len(h) < 1 || len(h) > 2 {
		return 0, 0, errors.New("not HH:MM")
	}
	if hour, err = strconv.Atoi(h); err != nil || hour < 0 || hour > 23 {
		return 0, 0, errors.New("not an hour")
	}
	if minute, err = strconv.Atoi(m); err != nil || minute < 0 || minute > 59 {
		return 0, 0, errors.New("not a minute")
	}
	return hour, minute, nil
}

// recurrence is the rule as the API shows it, every field in its one form.
func (ru rule) recurrence() Recurrence {
	r := Recurrence{Kind: ru.kind}
	if ru.kind == KindHourly {
		r.Minute = ru.minute
		return r
	}
	r.Time = fmt.Sprintf("%02d:%02d", ru.hour, ru.minute)
	for _, d := range ru.days {
		r.Days = append(r.Days, nameOf(d))
	}
	if ru.kind == KindOnce {
		r.Date = ru.date.Format("2006-01-02")
	}
	return r
}

func nameOf(d time.Weekday) string {
	for _, n := range dayNames {
		if n.day == d {
			return n.name
		}
	}
	return ""
}

// Next returns the first time after `after` that a recurrence is due, in
// loc, or the zero time when it never is again: a one-time recurrence
// whose time has passed. A recurrence that does not read is never due.
//
// Times of day are wall-clock times in loc, so a daily run at 07:00 stays at
// 07:00 across a change of the clocks, and the hours between two runs that
// day are 23 or 25. On a day when the clocks go forward, a time that does
// not happen runs as long after the change as it was after the start of the
// gap: 02:30 runs at 03:30 when 02:00 becomes 03:00. On a day when they go
// back, a time that happens twice runs once, at the first. An hourly
// recurrence runs every real hour: twice in the hour that is repeated, and
// not in the hour that is skipped.
func Next(r Recurrence, after time.Time, loc *time.Location) time.Time {
	ru, err := readRule(r)
	if err != nil {
		return time.Time{}
	}
	return ru.next(after, loc)
}

func (ru rule) next(after time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	switch ru.kind {
	case KindHourly:
		return nextHourly(after, ru.minute, loc)
	case KindOnce:
		at := wallTime(ru.date.Year(), ru.date.Month(), ru.date.Day(), ru.hour, ru.minute, loc)
		if at.After(after) {
			return at
		}
		return time.Time{}
	}
	start := after.In(loc)
	// Eight days reach every day of the week once more; a run due later on
	// the day `after` falls on is tried first.
	for i := 0; i <= 8; i++ {
		day := time.Date(start.Year(), start.Month(), start.Day()+i, 12, 0, 0, 0, loc)
		if !ru.on(day.Weekday()) {
			continue
		}
		at := wallTime(day.Year(), day.Month(), day.Day(), ru.hour, ru.minute, loc)
		if at.After(after) {
			return at
		}
	}
	return time.Time{}
}

// on reports whether a daily, weekdays or weekly rule runs on a weekday.
func (ru rule) on(d time.Weekday) bool {
	switch ru.kind {
	case KindWeekdays:
		return d != time.Saturday && d != time.Sunday
	case KindWeekly:
		return slices.Contains(ru.days, d)
	}
	return true
}

// nextHourly is the first instant after `after` at `minute` past an hour on
// loc's clock. It counts real hours, so an hour the clocks repeat runs
// twice, and an hour they skip does not run.
func nextHourly(after time.Time, minute int, loc *time.Location) time.Time {
	for i := 0; i < 4; i++ {
		t := after.Add(time.Duration(i) * time.Hour).In(loc)
		// The start of t's hour on loc's clock, then minute past it.
		top := t.Add(-time.Duration(t.Minute())*time.Minute - time.Duration(t.Second())*time.Second - time.Duration(t.Nanosecond()))
		if at := top.Add(time.Duration(minute) * time.Minute); at.After(after) {
			return at
		}
	}
	return time.Time{}
}

// wallTime is the instant a wall-clock time happens on a day in loc. When it
// happens twice, as when the clocks go back, it is the first. When it does
// not happen, as when they go forward, it is as long after the change as the
// time was after the start of the gap.
func wallTime(year int, month time.Month, day, hour, minute int, loc *time.Location) time.Time {
	naive := time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
	// The offsets in force a day either side: a change of the clocks near
	// the time is between them.
	_, before := naive.Add(-26 * time.Hour).In(loc).Zone()
	_, after := naive.Add(26 * time.Hour).In(loc).Zone()
	var found time.Time
	for _, off := range []int{before, after} {
		at := naive.Add(-time.Duration(off) * time.Second).In(loc)
		if at.Year() == year && at.Month() == month && at.Day() == day && at.Hour() == hour && at.Minute() == minute {
			if found.IsZero() || at.Before(found) {
				found = at
			}
		}
	}
	if !found.IsZero() {
		return found
	}
	// Skipped: read with the offset from before the change.
	return naive.Add(-time.Duration(before) * time.Second).In(loc)
}

// latest returns the last due time at or before now, counting from first,
// a due time that has come, and how many due times have come from first to
// it, first included. A run at now stands for all of them.
func (ru rule) latest(first, now time.Time, loc *time.Location) (time.Time, int) {
	at, count := first, 1
	// An hourly schedule over a long stop counts many; past this many the
	// count stops growing, and the due time is found from now instead.
	const most = 10000
	for count < most {
		n := ru.next(at, loc)
		if n.IsZero() || n.After(now) {
			return at, count
		}
		at, count = n, count+1
	}
	// Step back to the due time just before now.
	back := now.Add(-8 * 24 * time.Hour)
	if back.Before(at) {
		back = at
	}
	for {
		n := ru.next(back, loc)
		if n.IsZero() || n.After(now) {
			return back, count
		}
		back = n
	}
}

// Says puts a recurrence in words, as the screen shows it: "Weekdays at
// 07:00", "Mondays and Thursdays at 18:30", "Every hour at 15 past".
func Says(r Recurrence) string {
	ru, err := readRule(r)
	if err != nil {
		return ""
	}
	at := fmt.Sprintf("%02d:%02d", ru.hour, ru.minute)
	switch ru.kind {
	case KindHourly:
		if ru.minute == 0 {
			return "Every hour, on the hour"
		}
		return fmt.Sprintf("Every hour at %d past", ru.minute)
	case KindDaily:
		return "Every day at " + at
	case KindWeekdays:
		return "Weekdays at " + at
	case KindOnce:
		return fmt.Sprintf("Once, on %s at %s", ru.date.Format("Mon 2 Jan 2006"), at)
	}
	if len(ru.days) == 7 {
		return "Every day at " + at
	}
	var names []string
	for _, d := range ru.days {
		names = append(names, d.String()+"s")
	}
	list := names[0]
	if len(names) > 1 {
		list = strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
	return list + " at " + at
}
