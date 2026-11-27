package unattended

import (
	"context"
	"errors"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// A run's budget can be changed once it is registered, and the registry
// reads the new one; a negative budget and a run that is not registered are
// refused.
func TestARunsBudgetCanBeChanged(t *testing.T) {
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	who, err := in.Create("Ordinary Person")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(who.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := Register(ctx, s.DB(), Run{Kind: KindTask, ID: "one", RequestID: "req-budget", Title: "Research: tides", BudgetMicros: 3_000_000}); err != nil {
		t.Fatal(err)
	}
	if err := SetBudget(ctx, s.DB(), "req-budget", 900_000); err != nil {
		t.Fatal(err)
	}
	run, ok, err := ForRequest(ctx, s.DB(), "req-budget")
	if err != nil || !ok || run.BudgetMicros != 900_000 || run.Title != "Research: tides" {
		t.Fatalf("the run reads %+v (%v, %v)", run, ok, err)
	}
	if err := SetBudget(ctx, s.DB(), "req-budget", -1); !errors.Is(err, ErrInvalid) {
		t.Errorf("a negative budget gave %v", err)
	}
	if err := SetBudget(ctx, s.DB(), "req-nobody", 1); !errors.Is(err, ErrInvalid) {
		t.Errorf("a run that is not registered gave %v", err)
	}
}
