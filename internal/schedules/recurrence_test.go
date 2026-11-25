package schedules

import (
	"testing"
	"time"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func at(t *testing.T, loc *time.Location, text string) time.Time {
	t.Helper()
	got, err := time.ParseInLocation("2006-01-02 15:04", text, loc)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func utc(t *testing.T, text string) time.Time {
	t.Helper()
	got, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The next due time is right across a change of the clocks: the run stays at
// its time of day, so the hours between two runs are 23 or 25.
func TestTheNextDueTimeIsRightAcrossADaylightSavingChange(t *testing.T) {
	ny := zone(t, "America/New_York")
	lisbon := zone(t, "Europe/Lisbon")
	daily := func(clock string) Recurrence { return Recurrence{Kind: KindDaily, Time: clock} }
	for _, c := range []struct {
		name  string
		r     Recurrence
		loc   *time.Location
		after time.Time
		want  string // UTC
	}{
		// New York goes back from EDT (UTC-4) to EST (UTC-5) at 02:00 on 1 November 2026.
		{"daily at 07:00 the day before the clocks go back", daily("07:00"), ny, utc(t, "2026-10-30T12:00:00Z"), "2026-10-31T11:00:00Z"},
		{"daily at 07:00 the day the clocks go back", daily("07:00"), ny, utc(t, "2026-10-31T11:00:00Z"), "2026-11-01T12:00:00Z"},
		// Lisbon goes back from WEST (UTC+1) to WET (UTC+0) at 02:00 on 25 October 2026.
		{"daily at 07:00 in Lisbon across the change", daily("07:00"), lisbon, utc(t, "2026-10-24T06:00:00Z"), "2026-10-25T07:00:00Z"},
		// And forward at 01:00 on 28 March 2027.
		{"daily at 07:00 in Lisbon across the spring change", daily("07:00"), lisbon, utc(t, "2027-03-27T07:00:00Z"), "2027-03-28T06:00:00Z"},
		// A time that happens twice runs once, at the first: 01:30 EDT.
		{"daily at 01:30 on the day it happens twice", daily("01:30"), ny, utc(t, "2026-10-31T12:00:00Z"), "2026-11-01T05:30:00Z"},
		{"and the next day once more", daily("01:30"), ny, utc(t, "2026-11-01T05:30:00Z"), "2026-11-02T06:30:00Z"},
		// New York goes forward from 02:00 EST to 03:00 EDT on 14 March 2027:
		// 02:30 does not happen, and runs at 03:30 EDT.
		{"daily at 02:30 on the day it is skipped", daily("02:30"), ny, utc(t, "2027-03-13T12:00:00Z"), "2027-03-14T07:30:00Z"},
		{"and at 02:30 the day after", daily("02:30"), ny, utc(t, "2027-03-14T07:30:00Z"), "2027-03-15T06:30:00Z"},
		// Weekdays and weekly keep to the day on the local calendar.
		{"weekdays skip Saturday and Sunday", Recurrence{Kind: KindWeekdays, Time: "07:00"}, ny, utc(t, "2026-11-27T13:00:00Z"), "2026-11-30T12:00:00Z"},
		{"weekly on Sunday across the change", Recurrence{Kind: KindWeekly, Time: "18:00", Days: []string{"sun"}}, ny, utc(t, "2026-10-26T12:00:00Z"), "2026-11-01T23:00:00Z"},
		{"weekly on two days, the nearer first", Recurrence{Kind: KindWeekly, Time: "09:15", Days: []string{"thu", "mon"}}, lisbon, utc(t, "2026-11-24T10:00:00Z"), "2026-11-26T09:15:00Z"},
		{"a time later today runs today", daily("21:00"), lisbon, utc(t, "2026-11-25T08:00:00Z"), "2026-11-25T21:00:00Z"},
		{"a time that is now runs tomorrow", daily("08:00"), lisbon, utc(t, "2026-11-25T08:00:00Z"), "2026-11-26T08:00:00Z"},
		{"once, still to come", Recurrence{Kind: KindOnce, Time: "09:00", Date: "2026-12-01"}, lisbon, utc(t, "2026-11-25T08:00:00Z"), "2026-12-01T09:00:00Z"},
		{"once, in the skipped hour", Recurrence{Kind: KindOnce, Time: "02:10", Date: "2027-03-14"}, ny, utc(t, "2027-03-01T00:00:00Z"), "2027-03-14T07:10:00Z"},
	} {
		got := Next(c.r, c.after, c.loc)
		if !got.Equal(utc(t, c.want)) {
			t.Errorf("%s: next after %s is %s, want %s", c.name, c.after.In(c.loc), got.In(c.loc), utc(t, c.want).In(c.loc))
		}
		if got.Location() != c.loc {
			t.Errorf("%s: the due time is in %s, want %s", c.name, got.Location(), c.loc)
		}
	}
}

// An hourly schedule runs every real hour: twice in the hour the clocks
// repeat, and not in the hour they skip.
func TestHourlyRunsEveryRealHour(t *testing.T) {
	ny := zone(t, "America/New_York")
	r := Recurrence{Kind: KindHourly, Minute: 15}
	walk := func(from time.Time, n int) []string {
		var out []string
		at := from
		for i := 0; i < n; i++ {
			at = Next(r, at, ny)
			out = append(out, at.UTC().Format("15:04"))
		}
		return out
	}
	// Going back: 00:15 EDT, 01:15 EDT, 01:15 EST, 02:15 EST.
	got := walk(utc(t, "2026-11-01T04:00:00Z"), 4)
	want := []string{"04:15", "05:15", "06:15", "07:15"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hourly across the clocks going back ran at %v UTC, want %v", got, want)
		}
	}
	// Going forward: 01:15 EST, then 03:15 EDT, an hour later in real time.
	got = walk(utc(t, "2027-03-14T06:00:00Z"), 2)
	if got[0] != "06:15" || got[1] != "07:15" {
		t.Fatalf("hourly across the clocks going forward ran at %v UTC", got)
	}
	if local := Next(r, utc(t, "2027-03-14T06:15:00Z"), ny).Format("15:04"); local != "03:15" {
		t.Fatalf("the hour after 01:15 EST is %s on the clock, want 03:15", local)
	}
	// On the hour, and in a zone half an hour off UTC.
	if got := Next(Recurrence{Kind: KindHourly}, utc(t, "2026-11-25T10:20:00Z"), zone(t, "Asia/Kolkata")); !got.Equal(utc(t, "2026-11-25T10:30:00Z")) {
		t.Fatalf("on the hour in Kolkata after 15:50 is %s, want 16:00", got)
	}
}

func TestAOneTimeScheduleThatHasRunIsNeverDueAgain(t *testing.T) {
	lisbon := zone(t, "Europe/Lisbon")
	r := Recurrence{Kind: KindOnce, Time: "09:00", Date: "2026-12-01"}
	if got := Next(r, at(t, lisbon, "2026-12-01 09:00"), lisbon); !got.IsZero() {
		t.Fatalf("a one-time schedule is due again at %s", got)
	}
	if got := Next(Recurrence{Kind: "fortnightly", Time: "09:00"}, time.Now(), lisbon); !got.IsZero() {
		t.Fatalf("a recurrence that does not read is due at %s", got)
	}
}

// After a stop, the latest due time is found, with how many passed.
func TestTheLatestDueTimeCountsTheOnesMissed(t *testing.T) {
	lisbon := zone(t, "Europe/Lisbon")
	ru, err := readRule(Recurrence{Kind: KindDaily, Time: "07:00"})
	if err != nil {
		t.Fatal(err)
	}
	first := at(t, lisbon, "2026-11-25 07:00")
	last, n := ru.latest(first, at(t, lisbon, "2026-11-27 08:12"), lisbon)
	if !last.Equal(at(t, lisbon, "2026-11-27 07:00")) || n != 3 {
		t.Fatalf("over three missed mornings the latest is %s and %d were counted", last, n)
	}
	last, n = ru.latest(first, first, lisbon)
	if !last.Equal(first) || n != 1 {
		t.Fatalf("a due time that has just come is %s, counted %d", last, n)
	}
	// An hourly schedule over a long stop still finds the hour just gone.
	hourly, err := readRule(Recurrence{Kind: KindHourly, Minute: 5})
	if err != nil {
		t.Fatal(err)
	}
	last, n = hourly.latest(at(t, lisbon, "2025-01-01 00:05"), at(t, lisbon, "2026-11-25 10:30"), lisbon)
	if !last.Equal(at(t, lisbon, "2026-11-25 10:05")) || n < 10000 {
		t.Fatalf("an hourly schedule stopped for a year and more has its latest at %s, counted %d", last, n)
	}
}

func TestARecurrenceIsReadAndSaidInWords(t *testing.T) {
	for _, c := range []struct {
		r    Recurrence
		says string
	}{
		{Recurrence{Kind: KindHourly}, "Every hour, on the hour"},
		{Recurrence{Kind: KindHourly, Minute: 15}, "Every hour at 15 past"},
		{Recurrence{Kind: KindDaily, Time: "7:05"}, "Every day at 07:05"},
		{Recurrence{Kind: KindWeekdays, Time: "07:00"}, "Weekdays at 07:00"},
		{Recurrence{Kind: KindWeekly, Time: "18:30", Days: []string{"thu", "Mon"}}, "Mondays and Thursdays at 18:30"},
		{Recurrence{Kind: KindWeekly, Time: "18:30", Days: []string{"sat", "mon", "wed"}}, "Mondays, Wednesdays and Saturdays at 18:30"},
		{Recurrence{Kind: KindOnce, Time: "09:00", Date: "2026-12-01"}, "Once, on Tue 1 Dec 2026 at 09:00"},
	} {
		if got := Says(c.r); got != c.says {
			t.Errorf("%+v says %q, want %q", c.r, got, c.says)
		}
	}
	for _, bad := range []Recurrence{
		{Kind: "fortnightly", Time: "07:00"},
		{Kind: KindDaily},
		{Kind: KindDaily, Time: "24:00"},
		{Kind: KindDaily, Time: "07:5"},
		{Kind: KindHourly, Minute: 60},
		{Kind: KindWeekly, Time: "07:00"},
		{Kind: KindWeekly, Time: "07:00", Days: []string{"someday"}},
		{Kind: KindOnce, Time: "07:00", Date: "1 December"},
	} {
		if _, err := readRule(bad); err == nil {
			t.Errorf("%+v was read", bad)
		}
	}
	// Read back as it is kept, every field in its one form.
	ru, err := readRule(Recurrence{Kind: KindWeekly, Time: "7:00", Days: []string{"FRI", "mon", "fri"}, Date: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	got := ru.recurrence()
	if got.Time != "07:00" || len(got.Days) != 2 || got.Days[0] != "mon" || got.Days[1] != "fri" || got.Date != "" {
		t.Fatalf("a weekly recurrence reads back as %+v", got)
	}
}
