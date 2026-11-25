package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

const usageUsage = "tenant usage <tenant-id> [<month, such as 2026-11>]   (this month unless given)"

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

// runUsage prints one tenant's model calls for a calendar month, by model,
// and where the month stands against the tenant's cap.
func runUsage(in *tenant.Install, args []string, now time.Time, out io.Writer) error {
	if len(args) < 1 || len(args) > 2 {
		return errUsageUsage
	}
	month := usage.MonthOf(now)
	if len(args) == 2 {
		month = args[1]
	}
	s, err := in.Store(args[0])
	if err != nil {
		return err
	}
	defer s.Close()
	ctx := context.Background()
	sum, err := usage.Month(ctx, s, month)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "MODEL\tCLASS\tCALLS\tINPUT\tOUTPUT\tCACHE READ\tCACHE WRITE\tCOST\t")
	for _, m := range sum.Models {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%s\t\n", m.Model, m.Class, m.Calls, m.InputTokens, m.OutputTokens, m.CacheReadTokens, m.CacheWriteTokens, usage.Dollars(m.CostMicros))
	}
	fmt.Fprintf(w, "total\t\t%d\t%d\t%d\t%d\t%d\t%s\t\n", sum.Calls, sum.InputTokens, sum.OutputTokens, sum.CacheReadTokens, sum.CacheWriteTokens, usage.Dollars(sum.CostMicros))
	w.Flush()

	reg, err := settings.Registry()
	if err != nil {
		return err
	}
	limitText, err := tenantsettings.Get(ctx, s, reg, "usage.monthly_cap_usd")
	if err != nil {
		return err
	}
	warnText, err := tenantsettings.Get(ctx, s, reg, "usage.cap_warn_percent")
	if err != nil {
		return err
	}
	limit, err1 := strconv.ParseFloat(limitText, 64)
	warn, err2 := strconv.Atoi(warnText)
	c, err3 := usage.NewCap(limit, warn)
	if err := errors.Join(err1, err2, err3); err != nil {
		return fmt.Errorf("the tenant's cap does not read, so every call to a model that is not local is stopped: %w", err)
	}
	st := usage.Judge(sum.CappedMicros, 0, c)
	fmt.Fprintf(out, "\n%s: %s spent on models that are not local, of a monthly cap of %s.", month, usage.Dollars(st.SpentMicros), usage.Dollars(st.LimitMicros))
	switch {
	case st.SpentMicros >= st.LimitMicros:
		fmt.Fprintln(out, " The cap is reached: calls to those models are stopped.")
	case st.State == usage.StateWarning:
		fmt.Fprintf(out, " That is past the warning threshold of %s.\n", usage.Dollars(st.WarnAtMicros))
	default:
		fmt.Fprintln(out)
	}
	return nil
}
