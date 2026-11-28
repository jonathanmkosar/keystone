package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/operations"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/internal/usagereport"
	"github.com/jonathanmkosar/keystone/settings"
)

// The standing check "operations" (P11-03). The item's proof: each limit
// shows its share used and its trend; a crossed watch level shows on the
// home screen.
//
// On the real server, put together as internal/app puts it, with the
// shipped settings, on a throwaway data folder:
//
//  1. The server records every person's limits when it starts: the test
//     tenant has a snapshot of each soon after.
//  2. The test tenant sends a message, so it has a real turn. A week of
//     hourly snapshots is generated behind where each limit stands, and
//     the admin lowers its monthly cap to a cent, of which nine tenths are
//     spent.
//  3. GET /api/v1/operations: every limit (the cap, the memory reserve,
//     the database, the files and the artifacts; not the disk) has a share
//     that is its used of its limit, a watch level, and a trend that knows
//     its rate from more than a day of points ending at the reading now;
//     the cap is GET /api/v1/usage's; the turn is a trace whose tokens and
//     cost are its usage records'; no key is in the reply.
//  4. GET /api/v1/operations/watch, which the home screen reads, has the
//     cap crossed.
//  5. A second tenant's report and watch hold nothing of the test
//     tenant's: not its chat, not its trace, not its spend.
//
// The two routes are walked with every other by the tenant isolation,
// calls recorded and no keys in output checks (routes.go). The home
// screen's notice is seen in a browser by hand (docs/architecture/web.md).

func init() {
	componentDocs["internal/operations"] = "docs/architecture/operations.md"
	networkAllowed["internal/operations"] = "the Operations routes' handlers, served by the server; it opens no connection"
}

// operationsPassword is invented, and guards a folder that is removed.
const operationsPassword = "an invented phrase for the operations check"

func checkOperations(string) error {
	problems, err := operationsProblems()
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

func operationsProblems() (problems []string, err error) {
	dir, err := os.MkdirTemp("", "keystone-operations-")
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
	other, err := in.Create("Operations Person Two")
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
		return signin.SetPassword(s, operationsPassword, so)
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
	otherToken, rec := w.signIn(other.ID, operationsPassword)
	if otherToken == "" {
		return nil, fmt.Errorf("the second tenant cannot sign in through the API: status %d", rec.Code)
	}
	ctx := context.Background()
	s, err := in.Store(loaded.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}

	// 1. The snapshot taken when the server started.
	started := false
	for range 100 {
		if _, ok, err := operations.Newest(ctx, s); err != nil {
			return nil, err
		} else if ok {
			started = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !started {
		problems = append(problems, "the server took no snapshot of the test tenant's limits when it started")
	}

	// 2. A real turn, a week of snapshots, and a lowered cap.
	rec = w.request("POST", api.Prefix+"chats", token, "", nil)
	var made turn.ChatReply
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil || made.Chat.ID == "" {
		return nil, fmt.Errorf("a chat cannot be made through the API: status %d", rec.Code)
	}
	body, _ := json.Marshal(turn.SendBody{Text: "Suggest three names for a small bakery by the harbour."})
	rec = w.request("POST", api.Prefix+"chats/"+made.Chat.ID+"/turns", token, string(body), nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "event: "+turn.EventEnd+"\n") {
		return nil, fmt.Errorf("a turn did not stream to its end: status %d", rec.Code)
	}
	turnRequest := rec.Header().Get("X-Request-Id")
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Admin, "usage.monthly_cap_usd", "0.01"); err != nil {
		return nil, err
	}
	if _, err := usage.Add(ctx, s, usage.Record{Kind: "chat", Model: "verify-hosted", Class: models.ClassHosted, Outcome: usage.OutcomeOK, InputTokens: 10, CostMicros: 9000}); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	current, err := operations.Limits(ctx, s, reg, now)
	if err != nil {
		return nil, err
	}
	var snaps []operations.Snapshot
	// generated counts the snapshots each limit's trend should read: the
	// cap's and the reserve's reach back no further than the month.
	generated := map[string]int{}
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, l := range current {
		if strings.HasPrefix(l.Key, operations.LimitRun+":") || l.Problem != "" {
			continue
		}
		for h := 167; h >= 1; h-- {
			at := now.Add(-time.Duration(h) * time.Hour)
			used := max(l.Used-l.Used*int64(h)/400, 0) // a steady climb to where it stands
			snaps = append(snaps, operations.Snapshot{At: at, Key: l.Key, Used: used, Of: l.Of})
			if (l.Key != operations.LimitCap && l.Key != operations.LimitReserve) || !at.Before(monthStart) {
				generated[l.Key]++
			}
		}
	}
	if err := operations.Keep(ctx, s, snaps); err != nil {
		return nil, err
	}

	// 3. The report.
	var rep operations.Report
	rec = w.request("GET", api.Prefix+"operations", token, "", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rep) != nil {
		return nil, fmt.Errorf("GET /api/v1/operations: status %d", rec.Code)
	}
	byKey := map[string]operations.Limit{}
	for _, l := range rep.Limits {
		byKey[l.Key] = l
	}
	// The disk is the install's, shown only in the admin account (P15-02,
	// decision 0701): no person's report has it.
	if _, ok := byKey[operations.LimitDisk]; ok {
		problems = append(problems, "GET /api/v1/operations has the disk, which only the admin account shows (decision 0701)")
	}
	for _, key := range []string{operations.LimitCap, operations.LimitReserve, operations.LimitDatabase, operations.LimitFiles, operations.LimitArtifacts} {
		l, ok := byKey[key]
		switch {
		case !ok:
			problems = append(problems, "GET /api/v1/operations has no limit "+key)
			continue
		case l.State == operations.StateUnknown:
			problems = append(problems, fmt.Sprintf("the limit %s does not read: %s", key, l.Problem))
			continue
		case l.Of <= 0:
			problems = append(problems, fmt.Sprintf("the limit %s has a limit of %d", key, l.Of))
			continue
		}
		if want := int(float64(l.Used) * 100 / float64(l.Of)); l.Percent != want {
			problems = append(problems, fmt.Sprintf("the limit %s shows %d%% used, and %d of %d is %d%%", key, l.Percent, l.Used, l.Of, want))
		}
		if l.Watch < 1 || l.Watch > 100 {
			problems = append(problems, fmt.Sprintf("the limit %s has a watch level of %d", key, l.Watch))
		}
		n := len(l.Trend.Points)
		if n < generated[key]+1 || (generated[key] > 0 && !l.Trend.Known) {
			problems = append(problems, fmt.Sprintf("the limit %s has a trend of %d points, known %v, from %d snapshots and the reading now", key, n, l.Trend.Known, generated[key]))
			continue
		}
		if span := l.Trend.Points[n-1].At.Sub(l.Trend.Points[0].At); generated[key] >= 24 && span < 24*time.Hour {
			problems = append(problems, fmt.Sprintf("the limit %s has a trend over %s, from snapshots over a week", key, span))
		}
		if last := l.Trend.Points[n-1]; last.Used != l.Used {
			problems = append(problems, fmt.Sprintf("the limit %s's trend ends at %d, not the reading now, %d", key, last.Used, l.Used))
		}
	}
	var report usagereport.Report
	rec = w.request("GET", api.Prefix+"usage?period=this_month", token, "", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &report) != nil {
		return nil, fmt.Errorf("GET /api/v1/usage: status %d", rec.Code)
	}
	if capLimit := byKey[operations.LimitCap]; capLimit.Used != report.Cap.SpentMicros || capLimit.Of != report.Cap.LimitMicros || capLimit.Of != 10000 {
		problems = append(problems, fmt.Sprintf("the cap on Operations is %d of %d, and the usage report's is %d of %d", capLimit.Used, capLimit.Of, report.Cap.SpentMicros, report.Cap.LimitMicros))
	}
	var traced *operations.Trace
	for i := range rep.Traces {
		if rep.Traces[i].RequestID == turnRequest {
			traced = &rep.Traces[i]
		}
	}
	if traced == nil {
		problems = append(problems, "the test tenant's turn is not among the traces")
	} else {
		recs, err := usage.List(ctx, s, usage.Filter{RequestID: turnRequest})
		if err != nil {
			return nil, err
		}
		var want usage.Total
		for _, r := range recs {
			want.Calls++
			want.InputTokens += int64(r.InputTokens)
			want.OutputTokens += int64(r.OutputTokens)
			want.CacheReadTokens += int64(r.CacheReadTokens)
			want.CacheWriteTokens += int64(r.CacheWriteTokens)
			want.CostMicros += r.CostMicros
		}
		if traced.Usage != want || want.Calls == 0 {
			problems = append(problems, fmt.Sprintf("the turn's trace holds %+v, and its usage records sum to %+v", traced.Usage, want))
		}
		if traced.ChatID != made.Chat.ID || traced.TurnID == "" || traced.Status != "done" {
			problems = append(problems, fmt.Sprintf("the turn's trace opens the inspector on %q, turn %q, status %q", traced.ChatID, traced.TurnID, traced.Status))
		}
	}

	// 4. The watch route the home screen reads.
	var watched operations.WatchReply
	rec = w.request("GET", api.Prefix+"operations/watch", token, "", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &watched) != nil {
		return nil, fmt.Errorf("GET /api/v1/operations/watch: status %d", rec.Code)
	}
	capCrossed := false
	for _, l := range watched.Crossed {
		capCrossed = capCrossed || (l.Key == operations.LimitCap && l.Crossed && l.Percent >= 90)
	}
	if !capCrossed {
		problems = append(problems, fmt.Sprintf("nine tenths of the cap spent is not on the watch route the home screen reads: %d crossed", len(watched.Crossed)))
	}
	if rep.Crossed < 1 {
		problems = append(problems, "the report counts no crossed limit with the cap crossed")
	}

	// 5. The second tenant.
	var theirs operations.Report
	rec = w.request("GET", api.Prefix+"operations", otherToken, "", nil)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &theirs) != nil {
		return nil, fmt.Errorf("the second tenant's GET /api/v1/operations: status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), made.Chat.ID) || strings.Contains(rec.Body.String(), turnRequest) || len(theirs.Traces) != 0 {
		problems = append(problems, "the second tenant's report holds the test tenant's chat or trace")
	}
	for _, l := range theirs.Limits {
		if l.Key == operations.LimitCap && (l.Used != 0 || l.Of == 10000) {
			problems = append(problems, fmt.Sprintf("the second tenant's cap is %d of %d, the test tenant's", l.Used, l.Of))
		}
	}
	rec = w.request("GET", api.Prefix+"operations/watch", otherToken, "", nil)
	if strings.Contains(rec.Body.String(), `"key":"cap"`) {
		problems = append(problems, "the second tenant's watch route has the test tenant's cap crossed")
	}
	return problems, nil
}
