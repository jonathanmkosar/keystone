package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/internal/usagereport"
	"github.com/jonathanmkosar/keystone/settings"
)

// The standing check "usage report" (P11-02). The plan's words for phase
// 11: spend matches the usage records. The item's proof: totals equal the
// sum of usage records.
//
// On the real server, put together as internal/app puts it, with the
// shipped settings, on a throwaway data folder:
//
//  1. The test tenant sends messages in two chats, so its real turns leave
//     usage records under their request ids. Records are then added for
//     invented models of each class and several kinds of work, at times
//     on both sides of midnight UTC on 1 November 2026, under those
//     request ids and under none.
//  2. For a month on each side of the boundary, a range of days across
//     it, this month, and with one row a breakdown: GET /api/v1/usage is
//     compared with sums taken straight from the usage_records table. The
//     total, and its split by class, equal the table's; the lines of each
//     breakdown, by model, by chat and by kind of work, add up to the
//     total, field by field and class by class; the cap's spend is the
//     table's cost on models that are not local in the cap's month; a
//     chat line names a chat of the tenant's, by its title.
//  3. A second tenant's report holds its own one call and nothing of the
//     test tenant's.
//
// The route is walked with every other route by the tenant isolation,
// calls recorded and no keys in output checks (routes.go).

func init() {
	componentDocs["internal/usagereport"] = "docs/architecture/usage-report.md"
	networkAllowed["internal/usagereport"] = "the usage report's route handler, served by the server; it opens no connection"
}

// usageReportPassword is invented, and guards a folder that is removed.
const usageReportPassword = "an invented phrase for the usage report check"

func checkUsageReport(string) error {
	problems, err := usageReportProblems()
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

func usageReportProblems() (problems []string, err error) {
	dir, err := os.MkdirTemp("", "keystone-usage-report-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	fx, err := testtenant.Shipped()
	if err != nil {
		return nil, err
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		return nil, err
	}
	res, err := testtenant.Load(in, fx, o)
	if err != nil {
		return nil, err
	}
	loaded := res.Tenant
	other, err := in.Create("Usage Person Two")
	if err != nil {
		return nil, err
	}
	if err := func() error {
		s, err := in.Store(other.ID)
		if err != nil {
			return err
		}
		defer s.Close()
		so, err := signin.Defaults()
		if err != nil {
			return err
		}
		return signin.SetPassword(s, usageReportPassword, so)
	}(); err != nil {
		return nil, err
	}

	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	token, rec := w.signIn(loaded.ID, fx.Password)
	if token == "" {
		return nil, fmt.Errorf("the test tenant cannot sign in through the API: status %d", rec.Code)
	}
	otherToken, rec := w.signIn(other.ID, usageReportPassword)
	if otherToken == "" {
		return nil, fmt.Errorf("the second tenant cannot sign in through the API: status %d", rec.Code)
	}

	// 1. Real turns in two chats, then generated records under their
	// request ids.
	var requests []string
	for _, text := range []string{"Suggest three names for a small bakery by the harbour.", "What should I plant in a raised bed in spring?"} {
		rec := w.request("POST", api.Prefix+"chats", token, "", nil)
		var made turn.ChatReply
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil || made.Chat.ID == "" {
			return nil, fmt.Errorf("a chat cannot be made through the API: status %d", rec.Code)
		}
		body, _ := json.Marshal(turn.SendBody{Text: text})
		rec = w.request("POST", api.Prefix+"chats/"+made.Chat.ID+"/turns", token, string(body), nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "event: "+turn.EventEnd+"\n") {
			return nil, fmt.Errorf("a turn did not stream to its end: status %d", rec.Code)
		}
		requests = append(requests, rec.Header().Get("X-Request-Id"))
	}
	requests = append(requests, "") // records under no request id

	ctx := context.Background()
	s, err := in.Store(loaded.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	kinds := []string{"chat", "title", "summary", "memory", "embedding"}
	invented := []struct {
		name  string
		class models.Class
	}{{"verify-hosted", models.ClassHosted}, {"verify-trusted", models.ClassTrusted}, {"verify-local", models.ClassLocal}}
	start := time.Date(2026, 10, 30, 22, 0, 0, 0, time.UTC)
	for i := range 240 {
		m := invented[i%len(invented)]
		at := start.Add(time.Duration(i) * 17 * time.Minute) // 30 October 22:00 to 2 November about 18:00
		if i == 0 {
			at = time.Date(2026, 10, 31, 23, 59, 59, 999_000_000, time.UTC)
		}
		if i == 1 {
			at = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		}
		r := usage.Record{
			At: at, RequestID: requests[i%len(requests)], Kind: kinds[i%len(kinds)], Model: m.name, Class: m.class, Outcome: usage.OutcomeOK,
			InputTokens: 100 + i*7, OutputTokens: 20 + i, CacheReadTokens: (i % 4) * 900, CacheWriteTokens: (i % 3) * 300,
		}
		if m.class != models.ClassLocal {
			r.CostMicros = int64(r.InputTokens*3 + r.OutputTokens*15 + r.CacheReadTokens/3 + r.CacheWriteTokens*4)
		}
		if _, err := usage.Add(ctx, s, r); err != nil {
			return nil, err
		}
	}

	// 2. Each report against the table.
	now := time.Now().UTC()
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, q := range []struct {
		query    string
		from, to time.Time
		capMonth string
	}{
		{"?month=2026-10", usageDay(2026, 10, 1), usageDay(2026, 11, 1), "2026-10"},
		{"?month=2026-11", usageDay(2026, 11, 1), usageDay(2026, 12, 1), "2026-11"},
		{"?from=2026-10-31&to=2026-11-01", usageDay(2026, 10, 31), usageDay(2026, 11, 2), usage.MonthOf(now)},
		{"?month=2026-11&rows=1", usageDay(2026, 11, 1), usageDay(2026, 12, 1), "2026-11"},
		{"?period=this_month", thisMonth, thisMonth.AddDate(0, 1, 0), usage.MonthOf(now)},
	} {
		var rep usagereport.Report
		rec := w.request("GET", api.Prefix+"usage"+q.query, token, "", nil)
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rep) != nil {
			return nil, fmt.Errorf("GET /api/v1/usage%s: status %d", q.query, rec.Code)
		}
		p, err := usageAgainstTable(ctx, s, rep, q.query, q.from, q.to, q.capMonth)
		if err != nil {
			return nil, err
		}
		problems = append(problems, p...)
	}

	// 3. The second tenant sees only its own.
	otherStore, err := in.Store(other.ID)
	if err != nil {
		return nil, err
	}
	defer otherStore.Close()
	if _, err := usage.Add(ctx, otherStore, usage.Record{At: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), Kind: "chat", Model: "verify-hosted", Class: models.ClassHosted, Outcome: usage.OutcomeOK, InputTokens: 5, CostMicros: 77}); err != nil {
		return nil, err
	}
	var theirs usagereport.Report
	rec = w.request("GET", api.Prefix+"usage?from=2026-10-01&to=2026-11-30", otherToken, "", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &theirs) != nil {
		return nil, fmt.Errorf("the second tenant's GET /api/v1/usage: status %d", rec.Code)
	}
	if theirs.Total.Calls != 1 || theirs.Total.CostMicros != 77 || len(theirs.Models) != 1 {
		problems = append(problems, fmt.Sprintf("the second tenant's report holds %d calls costing %d, not its own one call", theirs.Total.Calls, theirs.Total.CostMicros))
	}
	return problems, nil
}

func usageDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// tableSum is the usage_records table summed straight, by class, for the
// rows at or after from and before to.
func usageTableSum(ctx context.Context, s *tenant.Store, from, to time.Time) (usagereport.Sum, error) {
	var out usagereport.Sum
	rows, err := s.DB().QueryContext(ctx, `SELECT class, count(*), coalesce(sum(input_tokens), 0), coalesce(sum(output_tokens), 0),
			coalesce(sum(cache_read_tokens), 0), coalesce(sum(cache_write_tokens), 0), coalesce(sum(cost_micros), 0)
		FROM usage_records WHERE at >= ? AND at < ? GROUP BY class`,
		from.Format("2006-01-02T15:04:05.000Z"), to.Format("2006-01-02T15:04:05.000Z"))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var class string
		var t usage.Total
		if err := rows.Scan(&class, &t.Calls, &t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheWriteTokens, &t.CostMicros); err != nil {
			return out, err
		}
		switch models.Class(class) {
		case models.ClassLocal:
			out.Local = t
		case models.ClassHosted:
			out.Hosted = t
		case models.ClassTrusted:
			out.Trusted = t
		}
		out.Calls += t.Calls
		out.InputTokens += t.InputTokens
		out.OutputTokens += t.OutputTokens
		out.CacheReadTokens += t.CacheReadTokens
		out.CacheWriteTokens += t.CacheWriteTokens
		out.CostMicros += t.CostMicros
	}
	return out, rows.Err()
}

// lineSum adds up a breakdown's lines.
func usageLineSum(rows []usagereport.Row) usagereport.Sum {
	var out usagereport.Sum
	add := func(a *usage.Total, b usage.Total) {
		a.Calls += b.Calls
		a.InputTokens += b.InputTokens
		a.OutputTokens += b.OutputTokens
		a.CacheReadTokens += b.CacheReadTokens
		a.CacheWriteTokens += b.CacheWriteTokens
		a.CostMicros += b.CostMicros
	}
	for _, r := range rows {
		add(&out.Total, r.Total)
		add(&out.Local, r.Local)
		add(&out.Hosted, r.Hosted)
		add(&out.Trusted, r.Trusted)
	}
	return out
}

// reportAgainstTable lists what is wrong with one report, against the
// table it was summed from.
func usageAgainstTable(ctx context.Context, s *tenant.Store, rep usagereport.Report, query string, from, to time.Time, capMonth string) ([]string, error) {
	var problems []string
	want, err := usageTableSum(ctx, s, from, to)
	if err != nil {
		return nil, err
	}
	if want.Calls == 0 {
		problems = append(problems, fmt.Sprintf("GET /api/v1/usage%s: the table holds no record for the period, so there is nothing to compare", query))
	}
	if rep.Total != want {
		problems = append(problems, fmt.Sprintf("GET /api/v1/usage%s: the total is %+v, and the usage records sum to %+v", query, rep.Total, want))
	}
	for what, rows := range map[string][]usagereport.Row{"model": rep.Models, "chat": rep.Chats, "kind of work": rep.Kinds} {
		if got := usageLineSum(rows); got != rep.Total {
			problems = append(problems, fmt.Sprintf("GET /api/v1/usage%s: the lines by %s add up to %+v, not the total %+v", query, what, got, rep.Total))
		}
		if len(rows) > rep.Rows+1 {
			problems = append(problems, fmt.Sprintf("GET /api/v1/usage%s: %d lines by %s, with %d rows asked for", query, len(rows), what, rep.Rows))
		}
	}
	monthStart, err := time.Parse("2006-01", capMonth)
	if err != nil {
		return nil, err
	}
	inMonth, err := usageTableSum(ctx, s, monthStart, monthStart.AddDate(0, 1, 0))
	if err != nil {
		return nil, err
	}
	if rep.Cap.Month != capMonth || rep.Cap.SpentMicros != inMonth.Capped() {
		problems = append(problems, fmt.Sprintf("GET /api/v1/usage%s: the cap is about %s with %d spent, and the table's %s cost %d on models that are not local", query, rep.Cap.Month, rep.Cap.SpentMicros, capMonth, inMonth.Capped()))
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	if st, ok := settings.Lookup(reg, "usage.monthly_cap_usd"); !ok || rep.Cap.LimitMicros != usageCapMicros(st.Default) {
		problems = append(problems, fmt.Sprintf("GET /api/v1/usage%s: the cap is %d millionths, not the setting's", query, rep.Cap.LimitMicros))
	}
	for _, r := range rep.Chats {
		if r.Key == "" {
			continue
		}
		ch, err := chat.Get(s, r.Key)
		if err != nil {
			problems = append(problems, fmt.Sprintf("GET /api/v1/usage%s: a chat line names %s, which is not a chat of the tenant's", query, r.Key))
			continue
		}
		if ch.Title != "" && r.Label != ch.Title {
			problems = append(problems, fmt.Sprintf("GET /api/v1/usage%s: chat %s is labelled %q and titled %q", query, r.Key, r.Label, ch.Title))
		}
	}
	return problems, nil
}

// usageCapMicros is a cap in US dollars, as the setting holds it, in
// millionths of a dollar, or -1 when it does not read.
func usageCapMicros(usd string) int64 {
	f, err := strconv.ParseFloat(usd, 64)
	if err != nil {
		return -1
	}
	c, err := usage.NewCap(f, 0)
	if err != nil {
		return -1
	}
	return c.LimitMicros
}
