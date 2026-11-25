package usage

import (
	"context"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every name and number here is invented.
func twoStores(t *testing.T) (one, two *tenant.Store) {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	open := func(name string) *tenant.Store {
		made, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	return open("Test Person One"), open("Test Person Two")
}

func at(day string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", day)
	if err != nil {
		panic(err)
	}
	return t
}

func TestARecordForEachCallSumsByMonth(t *testing.T) {
	one, two := twoStores(t)
	ctx := context.Background()
	add := func(s *tenant.Store, r Record) Record {
		t.Helper()
		got, err := Add(ctx, s, r)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	add(one, Record{At: at("2026-10-31 23:59"), Kind: "chat", Model: "hosted-large", Class: models.ClassHosted, Outcome: OutcomeOK, InputTokens: 1000, OutputTokens: 100, CostMicros: 4500})
	first := add(one, Record{At: at("2026-11-01 00:00"), RequestID: "req-1", Kind: "chat", Model: "hosted-large", Class: models.ClassHosted, Outcome: OutcomeOK,
		InputTokens: 2000, OutputTokens: 300, CacheReadTokens: 50, CacheWriteTokens: 20, ThinkingTokens: 100, Estimated: true, CostMicros: 10500, DurationMS: 1200})
	add(one, Record{At: at("2026-11-02 09:00"), RequestID: "req-1", Kind: "title", Model: "house-small", Class: models.ClassLocal, Outcome: OutcomeOK, InputTokens: 300, OutputTokens: 10, CostMicros: 700})
	add(one, Record{At: at("2026-11-03 09:00"), Kind: "chat", Model: "hosted-large", Class: models.ClassHosted, Outcome: OutcomeError, DurationMS: 30})
	add(one, Record{At: at("2026-11-04 09:00"), Kind: "chat", Model: "trusted-large", Class: models.ClassTrusted, Outcome: OutcomeStopped, CostMicros: 2000})
	add(two, Record{At: at("2026-11-02 09:00"), Kind: "chat", Model: "hosted-large", Class: models.ClassHosted, Outcome: OutcomeOK, CostMicros: 999})

	if first.ID == 0 || MonthOf(first.At) != "2026-11" {
		t.Errorf("the record was written as %+v", first)
	}
	sum, err := Month(ctx, one, "2026-11")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Calls != 4 || sum.CostMicros != 13200 || sum.CappedMicros != 12500 || sum.InputTokens != 2300 || sum.OutputTokens != 310 ||
		sum.CacheReadTokens != 50 || sum.CacheWriteTokens != 20 {
		t.Errorf("November sums to %+v", sum)
	}
	if len(sum.Models) != 3 || sum.Models[0].Model != "hosted-large" || sum.Models[0].Calls != 2 || sum.Models[0].CostMicros != 10500 ||
		sum.Models[1].Model != "house-small" || sum.Models[1].Class != models.ClassLocal {
		t.Errorf("November by model is %+v", sum.Models)
	}
	if spent, err := Spent(ctx, one, "2026-11"); err != nil || spent != 12500 {
		t.Errorf("November's spend on models that are not local is %d (%v)", spent, err)
	}
	if spent, _ := Spent(ctx, one, "2026-10"); spent != 4500 {
		t.Errorf("October's spend is %d", spent)
	}
	if sum, err := Month(ctx, one, "2026-12"); err != nil || sum.Calls != 0 || sum.Models == nil {
		t.Errorf("an empty month is %+v (%v)", sum, err)
	}
	// Another tenant's calls are its own.
	if spent, _ := Spent(ctx, two, "2026-11"); spent != 999 {
		t.Errorf("the other tenant's spend is %d", spent)
	}

	got, err := List(ctx, one, Filter{RequestID: "req-1"})
	if err != nil || len(got) != 2 || got[0].ID != first.ID || got[0] != first || got[1].Kind != "title" {
		t.Errorf("the records of one request are %+v (%v)", got, err)
	}
	if got, _ := List(ctx, one, Filter{Month: "2026-11", Limit: 2}); len(got) != 2 || got[1].Model != "trusted-large" {
		t.Errorf("the newest two of November are %+v", got)
	}
	if all, _ := List(ctx, one, Filter{}); len(all) != 5 {
		t.Errorf("the tenant has %d records", len(all))
	}
	for _, bad := range []string{"", "2026", "2026-13", "2026-1", "2026-11-01", "x' OR 1=1 --"} {
		if _, err := Month(ctx, one, bad); err == nil {
			t.Errorf("%q was read as a month", bad)
		}
		if _, err := Spent(ctx, one, bad); err == nil {
			t.Errorf("%q was read as a month", bad)
		}
	}
}

func TestRecordsAreCheckedAndNeverChanged(t *testing.T) {
	one, _ := twoStores(t)
	ctx := context.Background()
	good := Record{Kind: "chat", Model: "hosted-large", Class: models.ClassHosted, Outcome: OutcomeOK, CostMicros: 1}
	for name, change := range map[string]func(*Record){
		"no kind":         func(r *Record) { r.Kind = " " },
		"no model":        func(r *Record) { r.Model = "" },
		"an odd class":    func(r *Record) { r.Class = "elsewhere" },
		"an odd outcome":  func(r *Record) { r.Outcome = "started" },
		"a negative cost": func(r *Record) { r.CostMicros = -1 },
		"negative tokens": func(r *Record) { r.OutputTokens = -1 },
		"a negative time": func(r *Record) { r.DurationMS = -1 },
	} {
		r := good
		change(&r)
		if _, err := Add(ctx, one, r); err == nil {
			t.Errorf("a record with %s was written", name)
		}
	}
	// With no time given, the record is written now.
	written, err := Add(ctx, one, good)
	if err != nil || time.Since(written.At) > time.Minute {
		t.Fatalf("the record was written as %+v (%v)", written, err)
	}
	for _, q := range []string{
		"UPDATE usage_records SET cost_micros = 0",
		"DELETE FROM usage_records",
		"INSERT OR REPLACE INTO usage_records(id, at, month, kind, model, class, outcome) VALUES(1, '2026-11-01T00:00:00.000Z', '2026-11', 'chat', 'm', 'local', 'ok')",
		"INSERT INTO usage_records(at, month, kind, model, class, outcome) VALUES('2026-11-01T00:00:00.000Z', '2026-10', 'chat', 'm', 'local', 'ok')",
	} {
		if _, err := one.DB().Exec(q); err == nil {
			t.Errorf("the table took: %s", q)
		}
	}
	if spent, _ := Spent(ctx, one, MonthOf(written.At)); spent != 1 {
		t.Errorf("after the refused changes the spend is %d", spent)
	}
}

func TestTheCapWarnsAndStops(t *testing.T) {
	c, err := NewCap(100, 80)
	if err != nil || c.LimitMicros != 100_000_000 {
		t.Fatalf("the cap is %+v (%v)", c, err)
	}
	for _, x := range []struct {
		spent, next int64
		want        State
	}{
		{0, 0, StateOK},
		{0, 79_999_999, StateOK},
		{79_000_000, 1_000_000, StateWarning}, // reaches the threshold
		{90_000_000, 10_000_000, StateWarning},
		{90_000_000, 10_000_001, StateStopped}, // would pass the cap
		{100_000_000, 0, StateWarning},         // a call that costs nothing still runs
		{100_000_000, 1, StateStopped},
		{250_000_000, 5, StateStopped},
	} {
		st := Judge(x.spent, x.next, c)
		if st.State != x.want || st.WarnAtMicros != 80_000_000 || st.LimitMicros != c.LimitMicros {
			t.Errorf("with %d spent and %d to come the cap says %+v, want %s", x.spent, x.next, st, x.want)
		}
	}
	// A cap of nothing stops every call that costs anything.
	zero, _ := NewCap(0, 80)
	if Judge(0, 1, zero).State != StateStopped || Judge(0, 0, zero).State == StateStopped {
		t.Error("a cap of zero does not stop exactly the calls that cost something")
	}
	for _, bad := range []struct {
		usd  float64
		warn int
	}{{-1, 80}, {100, -1}, {100, 101}, {1e12, 80}} {
		if _, err := NewCap(bad.usd, bad.warn); err == nil {
			t.Errorf("a cap of %v with a threshold of %d was accepted", bad.usd, bad.warn)
		}
	}
	if Dollars(12_345_678) != "$12.35" {
		t.Errorf("Dollars gives %s", Dollars(12_345_678))
	}
}

func TestAllowCountsTheMonthItIsAskedIn(t *testing.T) {
	one, two := twoStores(t)
	ctx := context.Background()
	c, _ := NewCap(1, 50) // one dollar
	for _, r := range []Record{
		{At: at("2026-11-10 10:00"), Kind: "chat", Model: "hosted-large", Class: models.ClassHosted, Outcome: OutcomeOK, CostMicros: 600_000},
		{At: at("2026-11-11 10:00"), Kind: "chat", Model: "house-small", Class: models.ClassLocal, Outcome: OutcomeOK, CostMicros: 5_000_000},
	} {
		if _, err := Add(ctx, one, r); err != nil {
			t.Fatal(err)
		}
	}
	st, err := Allow(ctx, one, c, at("2026-11-20 10:00"), 300_000)
	if err != nil || st.State != StateWarning || st.SpentMicros != 600_000 || st.Month != "2026-11" || st.NextMicros != 300_000 {
		t.Errorf("under the cap, Allow says %+v (%v)", st, err)
	}
	if st, _ := Allow(ctx, one, c, at("2026-11-20 10:00"), 400_001); st.State != StateStopped {
		t.Errorf("over the cap, Allow says %+v", st)
	}
	// The next month starts from nothing, and so does another tenant.
	if st, _ := Allow(ctx, one, c, at("2026-12-01 00:00"), 400_001); st.State != StateOK || st.SpentMicros != 0 {
		t.Errorf("in a new month, Allow says %+v", st)
	}
	if st, _ := Allow(ctx, two, c, at("2026-11-20 10:00"), 400_001); st.State != StateOK {
		t.Errorf("for another tenant, Allow says %+v", st)
	}
	if _, err := Allow(ctx, one, c, at("2026-11-20 10:00"), -1); err == nil {
		t.Error("a negative cost was asked about")
	}
	if !Counts(models.ClassHosted) || !Counts(models.ClassTrusted) || Counts(models.ClassLocal) {
		t.Error("the cap does not count exactly the models that are not local")
	}
}
