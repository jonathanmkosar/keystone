package usagereport

import (
	"context"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// Standing is the report's cap and reserve for a month, field for field, at
// every state of the cap: Operations and the home screen read it in place
// of a whole report (P13-03), and the two must never disagree.
func TestStandingIsTheReportsCapAndReserve(t *testing.T) {
	in := newInstall(t)
	s := newStore(t, in, "Report Person Nine")
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	same := func(when string) {
		t.Helper()
		rep, err := Read(ctx, s, reg, Ask{Month: "2026-11"}, now)
		if err != nil {
			t.Fatal(err)
		}
		c, r, err := Standing(ctx, s, reg, "2026-11")
		if err != nil {
			t.Fatal(err)
		}
		if c != rep.Cap || r != rep.Reserve {
			t.Errorf("%s: Standing gives %+v and %+v, and the report %+v and %+v", when, c, r, rep.Cap, rep.Reserve)
		}
	}
	add := func(at time.Time, kind string, class models.Class, cost int64) {
		t.Helper()
		if _, err := usage.Add(ctx, s, usage.Record{At: at, Kind: kind, Model: "example-" + string(class), Class: class, Outcome: usage.OutcomeOK, CostMicros: cost}); err != nil {
			t.Fatal(err)
		}
	}
	same("an empty month")
	add(now.Add(-time.Hour), "chat", models.ClassHosted, 60_000_000)
	add(now.Add(-time.Hour), "chat", models.ClassLocal, 0)
	add(now.Add(-time.Hour), "memory", models.ClassTrusted, 500_000)
	add(now.AddDate(0, -1, 0), "chat", models.ClassHosted, 7_000_000) // another month's, which neither counts
	same("under the cap")
	if c, _, _ := Standing(ctx, s, reg, "2026-11"); c.SpentMicros != 60_500_000 || c.State != CapOK {
		t.Errorf("the month's spend: %+v", c)
	}
	add(now.Add(-time.Minute), "chat", models.ClassHosted, 25_000_000)
	same("past the warning")
	add(now.Add(-time.Minute), "chat", models.ClassHosted, 25_000_000)
	same("at the cap")
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Admin, settingCap, "-5"); err != nil {
		t.Fatal(err)
	}
	same("a cap that does not read")
	if c, _, _ := Standing(ctx, s, reg, "2026-11"); c.State != CapUnread || c.Problem == "" {
		t.Errorf("a cap that does not read: %+v", c)
	}
	if _, _, err := Standing(ctx, s, reg, "November"); err == nil {
		t.Error("a month that is not a month was read")
	}
}
