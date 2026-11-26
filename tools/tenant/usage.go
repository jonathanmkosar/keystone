package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/internal/usagereport"
	"github.com/jonathanmkosar/keystone/settings"
)

const usageUsage = "tenant usage <tenant-id> [<month, such as 2026-11> | <first day> <last day> | this_month | last_month | last_7_days | last_30_days]   (the tenant's usage.report_period unless given)"

var errUsageUsage = errors.New("usage: " + usageUsage)

// usageMain runs "tenant usage ..." and exits.
func usageMain(in *tenant.Install, args []string) {
	err := runUsage(in, args, time.Now(), os.Stdout)
	if errors.Is(err, errUsageUsage) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runUsage prints one tenant's usage report for a period: by model, by
// kind of work and by chat, and where the month stands against the cap and
// the memory pass's reserve. It is the report GET /api/v1/usage gives,
// from the same function (P11-02).
func runUsage(in *tenant.Install, args []string, now time.Time, out io.Writer) error {
	if len(args) < 1 || len(args) > 3 {
		return errUsageUsage
	}
	var ask usagereport.Ask
	switch {
	case len(args) == 3:
		ask.From, ask.To = args[1], args[2]
	case len(args) == 2 && slices.Contains(usagereport.Presets(), args[1]):
		ask.Period = args[1]
	case len(args) == 2:
		ask.Month = args[1]
	}
	s, err := in.Store(args[0])
	if err != nil {
		return err
	}
	defer s.Close()
	reg, err := settings.Registry()
	if err != nil {
		return err
	}
	rep, err := usagereport.Read(context.Background(), s, reg, ask, now)
	if err != nil {
		return err
	}
	printReport(out, rep)
	return nil
}

// printReport writes a report as tables a person reads in a terminal.
func printReport(out io.Writer, rep usagereport.Report) {
	p := rep.Period
	month := ""
	if p.Month != "" {
		month = " (" + p.Month + ")"
	}
	fmt.Fprintf(out, "%s to %s%s, in UTC\n\n", p.From, p.To, month)

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', tabwriter.AlignRight)
	head := "\tCALLS\tINPUT\tOUTPUT\tCACHE READ\tCACHE WRITE\tCOST\t"
	line := func(name string, s usagereport.Sum) {
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%s\t\n", name, s.Calls, s.InputTokens, s.OutputTokens, s.CacheReadTokens, s.CacheWriteTokens, usage.Dollars(s.CostMicros))
	}
	fmt.Fprintln(w, "MODEL\tCLASS"+head)
	for _, r := range rep.Models {
		name := r.Key
		if r.Rest > 0 {
			name = r.Label
		}
		line(name+"\t"+string(r.Class), r.Sum)
	}
	line("total\t", rep.Total)
	w.Flush() // each table is aligned on its own
	fmt.Fprintln(out)
	fmt.Fprintln(w, "KIND OF WORK\t"+head)
	for _, r := range rep.Kinds {
		line(r.Label+"\t", r.Sum)
	}
	w.Flush()
	fmt.Fprintln(out)
	fmt.Fprintln(w, "CHAT\tID"+head)
	for _, r := range rep.Chats {
		line(r.Label+"\t"+r.Key, r.Sum)
	}
	w.Flush()

	fmt.Fprintf(out, "\nOf the total, %s on hosted models and %s on trusted ones; local models took %d of the calls, at no cost.\n",
		usage.Dollars(rep.Total.Hosted.CostMicros), usage.Dollars(rep.Total.Trusted.CostMicros), rep.Total.Local.Calls)

	c := rep.Cap
	if c.State == usagereport.CapUnread {
		fmt.Fprintf(out, "%s: %s spent on models that are not local. The cap does not read: %s.\n", c.Month, usage.Dollars(c.SpentMicros), c.Problem)
	} else {
		fmt.Fprintf(out, "%s: %s spent on models that are not local, of a monthly cap of %s, with %s left.", c.Month, usage.Dollars(c.SpentMicros), usage.Dollars(c.LimitMicros), usage.Dollars(c.LeftMicros))
		switch c.State {
		case usagereport.CapReached:
			fmt.Fprintln(out, " The cap is reached: calls to those models are stopped.")
		case usagereport.CapWarning:
			fmt.Fprintf(out, " That is past the warning threshold of %s.\n", usage.Dollars(c.WarnAtMicros))
		default:
			fmt.Fprintf(out, " The warning starts at %s.\n", usage.Dollars(c.WarnAtMicros))
		}
	}
	m := rep.Reserve
	if m.Problem != "" {
		fmt.Fprintf(out, "The memory pass spent %s in %s, and its reserve does not read: %s.\n", usage.Dollars(m.SpentMicros), m.Month, m.Problem)
		return
	}
	fmt.Fprintf(out, "The memory pass spent %s of its %s reserve in %s, a part of the cap.\n", usage.Dollars(m.SpentMicros), usage.Dollars(m.LimitMicros), m.Month)
}
