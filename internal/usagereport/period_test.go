package usagereport

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/settings"
)

func TestResolve(t *testing.T) {
	now := time.Date(2026, 11, 6, 23, 30, 0, 0, time.FixedZone("west", -8*3600)) // 7 November in UTC
	for _, c := range []struct {
		ask            Ask
		preset         string
		name, from, to string
		month          string
		start, end     time.Time
	}{
		{Ask{}, PeriodThisMonth, PeriodThisMonth, "2026-11-01", "2026-11-30", "2026-11", day(2026, 11, 1), day(2026, 12, 1)},
		{Ask{}, PeriodLast30Days, PeriodLast30Days, "2026-10-09", "2026-11-07", "", day(2026, 10, 9), day(2026, 11, 8)},
		{Ask{Period: PeriodLastMonth}, PeriodThisMonth, PeriodLastMonth, "2026-10-01", "2026-10-31", "2026-10", day(2026, 10, 1), day(2026, 11, 1)},
		{Ask{Period: PeriodLast7Days}, PeriodThisMonth, PeriodLast7Days, "2026-11-01", "2026-11-07", "", day(2026, 11, 1), day(2026, 11, 8)},
		{Ask{Month: "2026-02"}, PeriodThisMonth, PeriodMonth, "2026-02-01", "2026-02-28", "2026-02", day(2026, 2, 1), day(2026, 3, 1)},
		{Ask{Month: "2028-02"}, PeriodThisMonth, PeriodMonth, "2028-02-01", "2028-02-29", "2028-02", day(2028, 2, 1), day(2028, 3, 1)},
		{Ask{Month: "2026-12"}, PeriodThisMonth, PeriodMonth, "2026-12-01", "2026-12-31", "2026-12", day(2026, 12, 1), day(2027, 1, 1)},
		{Ask{From: "2026-10-31", To: "2026-11-01"}, PeriodThisMonth, PeriodRange, "2026-10-31", "2026-11-01", "", day(2026, 10, 31), day(2026, 11, 2)},
		{Ask{From: "2026-11-05", To: "2026-11-05"}, PeriodThisMonth, PeriodRange, "2026-11-05", "2026-11-05", "", day(2026, 11, 5), day(2026, 11, 6)},
		// A range that is exactly a calendar month is that month, for the cap.
		{Ask{From: "2026-09-01", To: "2026-09-30"}, PeriodThisMonth, PeriodRange, "2026-09-01", "2026-09-30", "2026-09", day(2026, 9, 1), day(2026, 10, 1)},
		{Ask{From: "2026-09-01", To: "2026-09-29"}, PeriodThisMonth, PeriodRange, "2026-09-01", "2026-09-29", "", day(2026, 9, 1), day(2026, 9, 30)},
		// The longest range allowed.
		{Ask{From: "2026-01-01", To: "2026-02-09"}, PeriodThisMonth, PeriodRange, "2026-01-01", "2026-02-09", "", day(2026, 1, 1), day(2026, 2, 10)},
	} {
		p, err := Resolve(c.ask, c.preset, 40, now)
		if err != nil {
			t.Errorf("%+v: %v", c.ask, err)
			continue
		}
		if p.Name != c.name || p.From != c.from || p.To != c.to || p.Month != c.month || !p.start.Equal(c.start) || !p.end.Equal(c.end) {
			t.Errorf("%+v with %s: %+v (%s to %s), want %s %s to %s month %q (%s to %s)", c.ask, c.preset, p, p.start, p.end, c.name, c.from, c.to, c.month, c.start, c.end)
		}
	}

	for _, ask := range []Ask{
		{Period: "last_year"},
		{Period: "this month"},
		{Month: "2026-13"},
		{Month: "2026-1"},
		{Month: "November"},
		{From: "2026-11-01"},
		{To: "2026-11-01"},
		{From: "2026-11-02", To: "2026-11-01"},
		{From: "2026-11-01", To: "2026-12-11"}, // 41 days, one more than allowed
		{From: "0001-01-01", To: "9999-12-31"},
		{From: "2026-11-31", To: "2026-12-01"},
		{Month: "2026-11", From: "2026-11-01", To: "2026-11-02"},
		{Period: PeriodThisMonth, Month: "2026-11"},
		{Period: PeriodThisMonth, From: "2026-11-01", To: "2026-11-02"},
	} {
		if _, err := Resolve(ask, PeriodThisMonth, 40, now); !errors.Is(err, ErrPeriod) {
			t.Errorf("%+v gave %v, want a period that cannot be read", ask, err)
		}
	}
	// A default that is not a preset is the registry's fault, not the ask's.
	if _, err := Resolve(Ask{}, "every_day", 40, now); err == nil || errors.Is(err, ErrPeriod) {
		t.Errorf("a broken default gave %v", err)
	}
}

// The presets are the options of usage.report_period, and its default is
// one of them.
func TestThePresetsAreTheSettingsOptions(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := settings.Lookup(reg, SettingPeriod)
	if !ok || !slices.Equal(st.Options, Presets()) || !slices.Contains(Presets(), st.Default) {
		t.Errorf("%s has options %v and default %q, and the presets are %v", SettingPeriod, st.Options, st.Default, Presets())
	}
	for _, key := range []string{SettingRows, SettingMaxDays, settingCap, settingWarn, memorySettingReserve} {
		if _, ok := settings.Lookup(reg, key); !ok {
			t.Errorf("the report reads %s, which is not in the registry", key)
		}
	}
}
