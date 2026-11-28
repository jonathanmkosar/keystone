package adminview

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/figures"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/operations"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/internal/usagereport"
	"github.com/jonathanmkosar/keystone/settings"
)

// The proof's numbers are read on these invented people, at a fixed
// moment, with words in their chats and tasks that no admin reply may hold.
var proofNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

const (
	markerTitle = "Lighthouse keeper's secret ledger"
	markerError = "the ferry token was refused"
	stamp       = "2006-01-02T15:04:05.000Z"
)

func exec(t *testing.T, s *tenant.Store, q string, args ...any) {
	t.Helper()
	if _, err := s.DB().Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// fill gives a person spend, failures, tasks, schedules, files and
// snapshots, scaled by k so two people differ.
func fill(t *testing.T, s *tenant.Store, reg []settings.Setting, k int64) {
	t.Helper()
	ctx := context.Background()
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Admin, "usage.monthly_cap_usd", "10"); err != nil {
		t.Fatal(err)
	}
	add := func(r usage.Record) {
		if _, err := usage.Add(ctx, s, r); err != nil {
			t.Fatal(err)
		}
	}
	add(usage.Record{At: proofNow.Add(-50 * time.Hour), Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, InputTokens: 900, OutputTokens: 200, CostMicros: 3_000_000 * k})
	add(usage.Record{At: proofNow.Add(-2 * time.Hour), Kind: "memory", Model: "example-small", Class: models.ClassTrusted, Outcome: usage.OutcomeOK, InputTokens: 100, CostMicros: 400_000 * k})
	add(usage.Record{At: proofNow.Add(-time.Hour), Kind: "chat", Model: "house-small", Class: models.ClassLocal, Outcome: usage.OutcomeError, DurationMS: 30})
	add(usage.Record{At: proofNow.Add(-30 * time.Minute), Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeRefused})
	add(usage.Record{At: proofNow.Add(-24 * time.Hour * 40), Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 9_000_000})
	// An open task that has spent 90 cents of its dollar, in this month.
	add(usage.Record{At: proofNow.Add(-10 * time.Minute), RequestID: "req-open", Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 900_000})

	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	lim, err := chat.DefaultLimits()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Rename(s, lim, c.ID, markerTitle); err != nil {
		t.Fatal(err)
	}
	at := func(d time.Duration) string { return proofNow.Add(-d).UTC().Format(stamp) }
	exec(t, s, `INSERT INTO turns(id, chat_id, status, request_id, actor, input, error, created_at, started_at, ended_at) VALUES('turn-stuck', ?, 'running', 'req-stuck', 'person', '{}', '', ?, ?, '')`,
		c.ID, at(3*time.Hour), at(3*time.Hour))
	for _, r := range []struct{ id, status, req, turn, created, ended, failure string }{
		{"open", "running", "req-open", "", at(10 * time.Minute), "", ""},
		{"stuck", "running", "req-stuck", "turn-stuck", at(4 * time.Hour), "", ""},
		{"failed", "failed", "req-failed", "", at(5 * time.Hour), at(4 * time.Hour), `{"code":"failed","message":"` + markerError + `"}`},
		{"oldfail", "failed", "req-oldfail", "", at(90 * time.Hour), at(89 * time.Hour), `{"code":"failed","message":"old"}`},
	} {
		exec(t, s, `INSERT INTO jobs(id, chat_id, work_chat_id, title, task, started_by, status, request_id, started_request, actor, budget_micros, max_rounds, error, turn_id, created_at, ended_at)
			VALUES(?, ?, ?, ?, 'Invented work.', 'person', ?, ?, 'req-start', 'person', 1000000, 5, ?, ?, ?, ?)`,
			r.id, c.ID, c.ID, markerTitle+" "+r.id, r.status, r.req, r.failure, r.turn, r.created, r.ended)
		exec(t, s, `INSERT INTO unattended_runs(request_id, kind, ref_id, title, budget_micros, created_at) VALUES(?, 'task', ?, ?, 1000000, ?)`, r.req, r.id, markerTitle, r.created)
	}
	for i := int64(0); i < k; i++ {
		exec(t, s, `INSERT INTO schedules(id, name, prompt, kind, hour, minute, created_at, updated_at) VALUES(?, ?, ?, 'daily', 7, 0, ?, ?)`,
			"sched-"+string(rune('a'+i)), markerTitle, "Write the "+markerTitle, at(time.Hour), at(time.Hour))
	}
	if err := s.FilesFolder().WriteFile("a1b2c3", []byte(strings.Repeat("harbour ", 1024*int(k))), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := operations.Keep(ctx, s, []operations.Snapshot{{At: proofNow.Add(-time.Hour), Key: "database", Used: 100 * k, Of: 1000}, {At: proofNow.Add(-time.Hour), Key: "files", Used: 200 * k, Of: 1000}}); err != nil {
		t.Fatal(err)
	}
}

func proofInstall(t *testing.T) (*tenant.Install, adminaccount.Account, []tenant.Tenant, []settings.Setting) {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	var people []tenant.Tenant
	for i, name := range []string{"Proof Person One", "Proof Person Two"} {
		p, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		fill(t, s, reg, int64(i+1))
		s.Close()
		people = append(people, p)
	}
	db, err := adminaccount.Open(in.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	admin, err := db.AddAccount("Proof Admin")
	if err != nil {
		t.Fatal(err)
	}
	return in, admin, people, reg
}

// The item's proof: the admin's numbers for each person equal that
// person's own Operations and Usage figures, and the install's equal their
// sum. No word of a person's reaches a reply.
func TestTheNumbersAreEachPersonsOwnAndTheInstallsAreTheirSum(t *testing.T) {
	ctx := context.Background()
	in, admin, people, reg := proofInstall(t)
	v := NewWith(in, Options{Now: func() time.Time { return proofNow }, SampleEvery: time.Hour, SetupCodeFor: time.Hour})
	ask := func(action string) Asker {
		return Asker{AdminID: admin.ID, RequestID: "0123456789abcdef", Action: action}
	}

	list, err := v.People(ctx, ask("GET /api/v1/admin/people"))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := v.Install(ctx, ask("GET /api/v1/admin/install"))
	if err != nil {
		t.Fatal(err)
	}
	var sum figures.Use
	sumFailures := map[string]int{}
	scheduler := schedules.New(schedules.Options{})
	for i, p := range people {
		s, err := in.Store(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		ops, err := operations.Read(ctx, s, reg, operations.Sources{Schedules: scheduler.List}, proofNow)
		if err != nil {
			t.Fatal(err)
		}
		watch, err := operations.Watch(ctx, s, reg, proofNow)
		if err != nil {
			t.Fatal(err)
		}
		rep, err := usagereport.Read(ctx, s, reg, usagereport.Ask{Month: "2026-11"}, proofNow)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
		got := list[i]
		if got.ID != p.ID {
			t.Fatalf("row %d is %s, want %s", i, got.ID, p.ID)
		}
		limit := func(key string) operations.Limit {
			for _, l := range ops.Limits {
				if l.Key == key {
					return l
				}
			}
			t.Fatalf("Operations has no limit %s", key)
			return operations.Limit{}
		}
		// Spend: the usage report's cap, and Operations' cap limit.
		if got.Spend.SpentMicros != rep.Cap.SpentMicros || got.Spend.LimitMicros != rep.Cap.LimitMicros || got.Spend.State != rep.Cap.State || got.Spend.WarnPercent != rep.Cap.WarnPercent {
			t.Errorf("%s: spend %+v, and the usage report's cap is %+v", p.Name, got.Spend, rep.Cap)
		}
		if c := limit(operations.LimitCap); c.Used != got.Spend.SpentMicros || c.Of != got.Spend.LimitMicros {
			t.Errorf("%s: spend %d of %d, and Operations' cap is %d of %d", p.Name, got.Spend.SpentMicros, got.Spend.LimitMicros, c.Used, c.Of)
		}
		// Storage: Operations' three limits.
		// The database is the one figure that moves under the read: the
		// admin's read is written in the person's log before it is made, so
		// it grows by those entries between the two reads.
		if d := limit(operations.LimitDatabase).Used - got.Storage.Database; d < -64<<10 || d > 64<<10 || got.Storage.Files != limit(operations.LimitFiles).Used || got.Storage.Artifacts != limit(operations.LimitArtifacts).Used {
			t.Errorf("%s: storage %+v, and Operations reads %d, %d, %d", p.Name, got.Storage, limit(operations.LimitDatabase).Used, limit(operations.LimitFiles).Used, limit(operations.LimitArtifacts).Used)
		}
		if got.Storage.Files == 0 {
			t.Errorf("%s: no files counted", p.Name)
		}
		// Tasks failed and stuck, schedules, model health by state.
		if got.Jobs.Failed != ops.Jobs.FailedCount || got.Jobs.Stuck != len(ops.Jobs.Stuck) || got.Jobs.Failed != 1 || got.Jobs.Stuck != 1 {
			t.Errorf("%s: jobs %+v, and Operations has %d failed and %d stuck", p.Name, got.Jobs, ops.Jobs.FailedCount, len(ops.Jobs.Stuck))
		}
		if got.Schedules != len(ops.Schedules.Schedules) || got.Schedules != i+1 {
			t.Errorf("%s: %d schedules, and Operations lists %d", p.Name, got.Schedules, len(ops.Schedules.Schedules))
		}
		states := map[string]int{}
		for _, m := range ops.Models.Models {
			states[m.State]++
		}
		if len(states) != len(got.Models) {
			t.Errorf("%s: models %v, and Operations' are %v", p.Name, got.Models, states)
		}
		for k, n := range states {
			if got.Models[k] != n {
				t.Errorf("%s: models %v, and Operations' are %v", p.Name, got.Models, states)
			}
		}
		// The limits crossed: Operations' watch route's.
		var crossed []string
		for _, l := range watch.Crossed {
			crossed = append(crossed, l.Key)
		}
		if strings.Join(crossed, ",") != strings.Join(got.Crossed, ",") || len(got.Crossed) == 0 {
			t.Errorf("%s: crossed %v, and Operations' watch is %v", p.Name, got.Crossed, crossed)
		}

		// One person's usage: the usage report's, model by model.
		use, err := v.Usage(ctx, ask("GET /api/v1/admin/people/"+p.ID+"/usage"), p.ID, "2026-11-01", "2026-11-20")
		if err != nil {
			t.Fatal(err)
		}
		if use.Total.Total != rep.Total.Total || use.Total.CappedMicros != rep.Total.Capped() {
			t.Errorf("%s: the period's total %+v, and the usage report's %+v", p.Name, use.Total, rep.Total)
		}
		for _, row := range rep.Models {
			found := false
			for _, m := range use.Models {
				if m.Model == row.Key && m.Class == row.Class {
					found = true
					if m.Total.Total != row.Total {
						t.Errorf("%s: %s %+v, and the usage report's %+v", p.Name, row.Key, m.Total.Total, row.Total)
					}
				}
			}
			if !found {
				t.Errorf("%s: %s/%s is not in the admin's models", p.Name, row.Key, row.Class)
			}
		}
		var days usage.Total
		for _, d := range use.Days {
			days.Calls += d.Calls
			days.CostMicros += d.CostMicros
		}
		if days.Calls != use.Total.Calls || days.CostMicros != use.Total.CostMicros {
			t.Errorf("%s: the days add up to %+v, not the total %+v", p.Name, days, use.Total.Total)
		}
		if use.Failures[figures.FailModelError] != 1 || use.Failures[figures.FailModelRefused] != 1 || use.Failures[figures.FailTask] != 2 {
			t.Errorf("%s: failures %v", p.Name, use.Failures)
		}
		if len(use.Storage) != 1 || use.Storage[0].Database != 100*int64(i+1) || use.Storage[0].Files != 200*int64(i+1) {
			t.Errorf("%s: storage over time %+v", p.Name, use.Storage)
		}
		sum.Merge(use.Use)
		for k, n := range use.Failures {
			sumFailures[k] += n
		}
	}

	// The install: the sum of each person's, for this month to today.
	if inst.Total != sum.Total {
		t.Errorf("the install's total %+v, and the people's sum %+v", inst.Total, sum.Total)
	}
	if len(inst.Models) != len(sum.Models) || len(inst.Days) != len(sum.Days) {
		t.Errorf("the install has %d models and %d days, and the people's sum %d and %d", len(inst.Models), len(inst.Days), len(sum.Models), len(sum.Days))
	}
	for i := range inst.Models {
		if i < len(sum.Models) && inst.Models[i] != sum.Models[i] {
			t.Errorf("the install's model %+v, and the sum's %+v", inst.Models[i], sum.Models[i])
		}
	}
	for k, n := range sumFailures {
		if inst.Failures[k] != n {
			t.Errorf("the install's failures %v, and the people's %v", inst.Failures, sumFailures)
		}
	}
	if inst.People.Total != 2 || inst.People.Enabled != 2 || inst.People.Waiting != 2 || inst.CapMicros != 20_000_000 {
		t.Errorf("the install's people %+v, caps %d", inst.People, inst.CapMicros)
	}
	if inst.Disk.Of <= 0 && inst.Disk.Problem == "" {
		t.Errorf("the disk: %+v", inst.Disk)
	}
	if !inst.Sampler.Newest.Equal(proofNow.Add(-time.Hour)) || inst.Sampler.Every != "1h0m0s" {
		t.Errorf("the sampler: %+v", inst.Sampler)
	}

	// No word of a person's in any reply.
	for what, x := range map[string]any{"people": list, "install": inst} {
		raw, _ := json.Marshal(x)
		for _, word := range []string{markerTitle, markerError, "Invented work", "Lighthouse"} {
			if strings.Contains(string(raw), word) {
				t.Errorf("the %s reply holds %q", what, word)
			}
		}
	}
}

// figures.Standing is the usage report's own cap and reserve, in each
// state, and its memory kind and reserve setting are the memory pass's.
func TestStandingIsTheUsageReports(t *testing.T) {
	ctx := context.Background()
	in, _, people, reg := proofInstall(t)
	s, err := in.Store(people[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, cap := range []string{"10", "3.5", "0", "-1"} {
		if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Admin, "usage.monthly_cap_usd", cap); err != nil && cap != "-1" {
			t.Fatal(err)
		}
		c, r, err := figures.Standing(ctx, s, reg, "2026-11")
		if err != nil {
			t.Fatal(err)
		}
		uc, ur, err := usagereport.Standing(ctx, s, reg, "2026-11")
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(c)
		b, _ := json.Marshal(uc)
		if string(a) != string(b) {
			t.Errorf("cap %s: figures says %s, the usage report %s", cap, a, b)
		}
		a, _ = json.Marshal(r)
		b, _ = json.Marshal(ur)
		if string(a) != string(b) {
			t.Errorf("cap %s: the reserve: figures says %s, the usage report %s", cap, a, b)
		}
	}
}
