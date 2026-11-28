package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/internal/usagereport"
)

// Every model and number here is invented.

func TestUsagePrintsAuto(t *testing.T) {
	var out bytes.Buffer
	printAuto(&out, usagereport.Auto{})
	if out.Len() != 0 {
		t.Errorf("nothing on Auto printed %q", out.String())
	}

	a := usagereport.Auto{Turns: 12, StrongerAsked: 2, Rewrites: 2, CostMicros: 3_200_000, SavedMicros: 9_600_000,
		TopStep: usagereport.TopStep{Name: "example-large", Label: "Example Large", CostMicros: 12_800_000}}
	a.Replies.Total = usage.Total{Calls: 17, CostMicros: 3_200_000}
	a.Decisions.Total = usage.Total{Calls: 10}
	printAuto(&out, a)
	for _, want := range []string{"On Auto: 12 replies, whose 17 calls cost $3.20, and 10 decisions", "$3.20 in all",
		"Example Large (example-large), would have cost $12.80: $9.60 saved", "asked for on 2 of the 12 replies; 2 of them"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("Auto did not print %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	a.SavedMicros = -400_000
	printAuto(&out, a)
	if !strings.Contains(out.String(), "Auto cost $0.40 more") {
		t.Errorf("Auto costing more printed:\n%s", out.String())
	}
	out.Reset()
	a.TopStep = usagereport.TopStep{Problem: "no ladder is set, so there is no top step to price the replies at"}
	printAuto(&out, a)
	if !strings.Contains(out.String(), "not priced at a top step: no ladder is set") {
		t.Errorf("a top step with a problem printed:\n%s", out.String())
	}
}
