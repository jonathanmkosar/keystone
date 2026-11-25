package usage

import (
	"context"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/models"
)

// What one kind of task spent is summed apart: only that kind, only models
// that are not local, only that month, and only that tenant.
func TestSpentOnSumsOneKind(t *testing.T) {
	one, two := twoStores(t)
	ctx := context.Background()
	for _, x := range []struct {
		s    int
		r    Record
		into bool
	}{
		{1, Record{At: at("2026-11-02 09:00"), Kind: "memory", Model: "hosted-small", Class: models.ClassHosted, Outcome: OutcomeOK, CostMicros: 300}, true},
		{1, Record{At: at("2026-11-03 09:00"), Kind: "memory", Model: "trusted-small", Class: models.ClassTrusted, Outcome: OutcomeOK, CostMicros: 200}, true},
		{1, Record{At: at("2026-11-03 10:00"), Kind: "memory", Model: "house-small", Class: models.ClassLocal, Outcome: OutcomeOK, CostMicros: 900}, false},
		{1, Record{At: at("2026-11-03 11:00"), Kind: "chat", Model: "hosted-small", Class: models.ClassHosted, Outcome: OutcomeOK, CostMicros: 5000}, false},
		{1, Record{At: at("2026-10-31 23:59"), Kind: "memory", Model: "hosted-small", Class: models.ClassHosted, Outcome: OutcomeOK, CostMicros: 70}, false},
		{2, Record{At: at("2026-11-03 09:00"), Kind: "memory", Model: "hosted-small", Class: models.ClassHosted, Outcome: OutcomeOK, CostMicros: 40}, false},
	} {
		s := one
		if x.s == 2 {
			s = two
		}
		if _, err := Add(ctx, s, x.r); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := SpentOn(ctx, one, "2026-11", "memory"); err != nil || got != 500 {
		t.Errorf("November's memory spend on models that are not local is %d (%v), want 500", got, err)
	}
	if got, _ := SpentOn(ctx, one, "2026-11", "summary"); got != 0 {
		t.Errorf("a kind with no records spent %d", got)
	}
	if got, _ := SpentOn(ctx, two, "2026-11", "memory"); got != 40 {
		t.Errorf("the second tenant's memory spend is %d, want its own 40", got)
	}
	if _, err := SpentOn(ctx, one, "November", "memory"); err == nil {
		t.Error("a month that is not one was taken")
	}
}
