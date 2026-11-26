package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Every model and number here is invented.
func TestUsagePrintsAMonthAndTheCap(t *testing.T) {
	in, id := testInstall(t)
	november := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []usage.Record{
		{At: november, Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, InputTokens: 1000, OutputTokens: 200, CostMicros: 85_000_000},
		{At: november, Kind: "title", Model: "house-small", Class: models.ClassLocal, Outcome: usage.OutcomeOK, InputTokens: 300, OutputTokens: 10},
	} {
		if _, err := usage.Add(context.Background(), s, r); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	var out bytes.Buffer
	if err := runUsage(in, []string{id}, november, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"example-large", "house-small", "$85.00", "2026-11: $85.00 spent", "cap of $100.00", "past the warning threshold of $80.00"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage did not print %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := runUsage(in, []string{id, "2026-10"}, november, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "2026-10: $0.00 spent") || strings.Contains(out.String(), "example-large") {
		t.Errorf("an empty month printed:\n%s", out.String())
	}
	if err := runUsage(in, []string{id, "last month"}, november, &out); err == nil {
		t.Error("a month that is not one gave no error")
	}
	if err := runUsage(in, nil, november, &out); !errors.Is(err, errUsageUsage) {
		t.Errorf("no arguments gave %v", err)
	}
	if err := runUsage(in, []string{"0000000000000000"}, november, &out); err == nil {
		t.Error("usage was printed for a tenant that does not exist")
	}

	// P11-02: the report by kind of work and by chat, for a range of days
	// or a named period, from the function the API uses.
	out.Reset()
	if err := runUsage(in, []string{id, "2026-11-01", "2026-11-30"}, november, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2026-11-01 to 2026-11-30 (2026-11), in UTC", "KIND OF WORK", "Chat replies", "Chat titles", "Not in a chat you still have", "$85.00 on hosted models", "local models took 1 of the calls", "The memory pass spent $0.00 of its $2.00 reserve"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage for a range did not print %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := runUsage(in, []string{id, "last_month"}, november, &out); err != nil || !strings.Contains(out.String(), "2026-10-01 to 2026-10-31 (2026-10)") {
		t.Errorf("usage for last month gave %v:\n%s", err, out.String())
	}
	if err := runUsage(in, []string{id, "2026-11-30", "2026-11-01"}, november, &out); err == nil {
		t.Error("a range that ends before it starts gave no error")
	}
	if err := runUsage(in, []string{id, "2026-11-01", "2026-11-30", "more"}, november, &out); !errors.Is(err, errUsageUsage) {
		t.Errorf("four arguments gave %v", err)
	}
}
