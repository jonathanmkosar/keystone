package figures

import (
	"context"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// figures may not import the memory pass, so it names the pass's kind and
// reserve setting itself; they are the pass's own.
func TestTheMemoryNamesAreThePasss(t *testing.T) {
	if KindMemory != memory.Kind || SettingReserve != memory.SettingReserve {
		t.Errorf("figures names the memory pass %q and %q, and the pass is %q and %q", KindMemory, SettingReserve, memory.Kind, memory.SettingReserve)
	}
	for _, usd := range []float64{0, 2, 2.345678, 1e9} {
		want, err := memory.NewReserve(usd)
		if err != nil {
			t.Fatal(err)
		}
		if got := int64(usd*1e6 + 0.5); got != want {
			t.Errorf("$%v is %d micros to the pass", usd, want)
		}
	}
}

func TestJudge(t *testing.T) {
	for _, c := range []struct {
		key      string
		used, of int64
		watch    int
		problem  string
		percent  int
		state    string
		crossed  bool
	}{
		{LimitFiles, 10, 100, 80, "", 10, StateOK, false},
		{LimitFiles, 85, 100, 80, "", 85, StateWatch, true},
		{LimitFiles, 100, 100, 80, "", 100, StateFull, true},
		{LimitFiles, 0, 0, 80, "", 0, StateOK, false},
		{LimitFiles, 1, 0, 80, "", 100, StateFull, true},
		{LimitCap, 0, 0, 80, "", 100, StateFull, true},
		{LimitCap, 0, 0, 80, "unread", 0, StateUnknown, true},
		{LimitFiles, 0, 0, 80, "unread", 0, StateUnknown, false},
	} {
		p, s, x := Judge(c.key, c.used, c.of, c.watch, c.problem)
		if p != c.percent || s != c.state || x != c.crossed {
			t.Errorf("%s %d of %d at %d (%q): %d %s %v", c.key, c.used, c.of, c.watch, c.problem, p, s, x)
		}
	}
}

func TestHealthState(t *testing.T) {
	for _, c := range []struct {
		calls, refusals, errs, watch int
		want                         string
	}{
		{0, 0, 0, 10, HealthIdle},
		{2, 2, 0, 10, HealthRefused},
		{3, 1, 2, 10, HealthFailing},
		{10, 0, 1, 10, HealthErrors},
		{20, 0, 1, 10, HealthGood},
	} {
		if got, _ := HealthState(c.calls, c.refusals, c.errs, c.watch); got != c.want {
			t.Errorf("%+v: %s", c, got)
		}
	}
}

// A period's use adds up three ways, and two people's merge into the sum.
func TestUsageAddsUp(t *testing.T) {
	ctx := context.Background()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	var uses []Use
	for i, name := range []string{"Figures Person One", "Figures Person Two"} {
		p, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range []usage.Record{
			{At: now.Add(-49 * time.Hour), Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: int64(1000 * (i + 1))},
			{At: now.Add(-time.Hour), Kind: "chat", Model: "house-small", Class: models.ClassLocal, Outcome: usage.OutcomeError},
			{At: now.AddDate(0, -1, 0), Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 7},
		} {
			if _, err := usage.Add(ctx, s, r); err != nil {
				t.Fatal(err)
			}
		}
		u, err := Usage(ctx, s, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), now)
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if u.Total.Calls != 2 || u.Total.Errors != 1 || u.Total.CappedMicros != int64(1000*(i+1)) || len(u.Days) != 2 || len(u.Models) != 2 {
			t.Errorf("%s: %+v", name, u)
		}
		uses = append(uses, u)
	}
	var sum Use
	for _, u := range uses {
		sum.Merge(u)
	}
	if sum.Total.Calls != 4 || sum.Total.CostMicros != 3000 || len(sum.Days) != 2 || len(sum.Models) != 2 || sum.Models[0].CostMicros != 3000 {
		t.Errorf("the sum: %+v", sum)
	}
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := settings.Lookup(reg, SettingWatchDisk); !ok {
		t.Error("the disk's watch level is not a setting")
	}
}

func TestReadDisk(t *testing.T) {
	d := ReadDisk(t.TempDir(), 90)
	if d.Problem == "" && (d.Of <= 0 || d.Used < 0 || d.Used > d.Of || d.Watch != 90) {
		t.Errorf("the disk: %+v", d)
	}
}
