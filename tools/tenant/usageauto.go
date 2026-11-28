package main

import (
	"fmt"
	"io"

	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/internal/usagereport"
)

// printAuto writes the report's Auto figures (P14-07) after the cap and the
// reserve: what the replies on Auto cost with the router's decisions,
// against the same tokens at the ladder's top step, and how often a
// stronger model was asked for. A period with nothing on Auto prints
// nothing.
func printAuto(out io.Writer, a usagereport.Auto) {
	if a.Turns == 0 && a.Replies.Calls == 0 && a.Decisions.Calls == 0 {
		return
	}
	fmt.Fprintf(out, "\nOn Auto: %d replies, whose %d calls cost %s, and %d decisions, which cost %s: %s in all.",
		a.Turns, a.Replies.Calls, usage.Dollars(a.Replies.CostMicros), a.Decisions.Calls, usage.Dollars(a.Decisions.CostMicros), usage.Dollars(a.CostMicros))
	t := a.TopStep
	switch {
	case t.Problem != "":
		fmt.Fprintf(out, " The replies are not priced at a top step: %s.\n", t.Problem)
	case a.SavedMicros >= 0:
		fmt.Fprintf(out, " The same tokens at the top step, %s (%s), would have cost %s: %s saved.\n", t.Label, t.Name, usage.Dollars(t.CostMicros), usage.Dollars(a.SavedMicros))
	default:
		fmt.Fprintf(out, " The same tokens at the top step, %s (%s), would have cost %s: Auto cost %s more.\n", t.Label, t.Name, usage.Dollars(t.CostMicros), usage.Dollars(-a.SavedMicros))
	}
	fmt.Fprintf(out, "A stronger model was asked for on %d of the %d replies; %d of them were a stronger model's rewrite.\n", a.StrongerAsked, a.Turns, a.Rewrites)
}
