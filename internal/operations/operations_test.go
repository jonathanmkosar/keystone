package operations

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/internal/usagereport"
	"github.com/jonathanmkosar/keystone/settings"
)

// Every person, chat, model, key and number here is invented.

// now is the moment the tests stand at: well into a month, so the cap's
// trend has days to look back over.
var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

const stampLayout = "2006-01-02T15:04:05.000Z"

// fakeKey has the shape of a provider key, so audit.Scrub removes it. It
// is invented and opens nothing.
const fakeKey = "sk-ant-" + "invented0000000000000000000000operations"

func newInstall(t *testing.T) *tenant.Install {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func newStore(t *testing.T, in *tenant.Install, name string) *tenant.Store {
	t.Helper()
	made, err := in.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func registry(t *testing.T) []settings.Setting {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// withDefault is the registry with one setting's default changed, as an
// install that set it would read it.
func withDefault(t *testing.T, reg []settings.Setting, key, value string) []settings.Setting {
	t.Helper()
	out := slices.Clone(reg)
	for i := range out {
		if out[i].Key == key {
			out[i].Default = value
			return out
		}
	}
	t.Fatalf("no setting %s", key)
	return nil
}

func exec(t *testing.T, s *tenant.Store, q string, args ...any) {
	t.Helper()
	if _, err := s.DB().Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func setAs(t *testing.T, s *tenant.Store, reg []settings.Setting, role tenantsettings.Role, key, value string) {
	t.Helper()
	if err := tenantsettings.Set(context.Background(), s, reg, role, key, value); err != nil {
		t.Fatal(err)
	}
}

func newChat(t *testing.T, s *tenant.Store, title string) string {
	t.Helper()
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	if title != "" {
		lim, err := chat.DefaultLimits()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := chat.Rename(s, lim, c.ID, title); err != nil {
			t.Fatal(err)
		}
	}
	return c.ID
}

// addTurn writes a turn as the turn runner does, with a status and a
// stored error.
func addTurn(t *testing.T, s *tenant.Store, id, chatID, requestID, status, failure string, at time.Time) {
	t.Helper()
	ended := ""
	if status != "queued" && status != "running" {
		ended = at.Add(time.Minute).UTC().Format(stampLayout)
	}
	exec(t, s, `INSERT INTO turns(id, chat_id, status, request_id, actor, input, error, created_at, started_at, ended_at) VALUES(?, ?, ?, ?, 'person', '{}', ?, ?, ?, ?)`,
		id, chatID, status, requestID, failure, at.UTC().Format(stampLayout), at.UTC().Format(stampLayout), ended)
}

func addRecord(t *testing.T, s *tenant.Store, r usage.Record) {
	t.Helper()
	if _, err := usage.Add(context.Background(), s, r); err != nil {
		t.Fatal(err)
	}
}

func appendAudit(t *testing.T, s *tenant.Store, e audit.Entry) {
	t.Helper()
	if _, err := audit.Append(context.Background(), s, e); err != nil {
		t.Fatal(err)
	}
}

func find(t *testing.T, all []Limit, key string) Limit {
	t.Helper()
	for _, l := range all {
		if l.Key == key {
			return l
		}
	}
	t.Fatalf("no limit %s among %d", key, len(all))
	return Limit{}
}

// The item's proof, first half: each limit shows its share used and a
// trend. A week of hourly snapshots is generated for every limit, growing
// at a known rate up to where it stands now; each limit then has its
// share, its watch level, a trend that knows the rate, and a date it would
// fill at that rate, or the cap's spend at the end of the month.
func TestEachLimitHasItsShareAndTrend(t *testing.T) {
	ctx := context.Background()
	in := newInstall(t)
	s := newStore(t, in, "Operations Person One")
	reg := registry(t)

	// This month's spend, $4.80 of the $100 cap, and the memory pass's
	// $0.40 of its $2 reserve.
	addRecord(t, s, usage.Record{At: now.Add(-48 * time.Hour), Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, InputTokens: 900, CostMicros: 4_400_000})
	addRecord(t, s, usage.Record{At: now.Add(-24 * time.Hour), Kind: "memory", Model: "example-small", Class: models.ClassTrusted, Outcome: usage.OutcomeOK, InputTokens: 300, CostMicros: 400_000})
	// A file in the person's folder, and one in the artifacts folder.
	if err := s.Files().WriteFile("a1b2c3", []byte(strings.Repeat("harbour ", 4096)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Artifacts().WriteFile("v1.html", []byte(strings.Repeat("<p>kite</p>", 512)), 0o600); err != nil {
		t.Fatal(err)
	}

	// Where each stands now, and a rate for each, in its unit a day.
	cfg, err := ReadSettings(ctx, s, reg)
	if err != nil {
		t.Fatal(err)
	}
	current, err := measure(ctx, s, reg, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	rates := map[string]int64{
		LimitCap: 600_000, LimitReserve: 50_000, LimitDisk: 200 << 20, LimitDatabase: 3 << 20, LimitFiles: 40 << 20, LimitArtifacts: 1 << 20,
	}
	var snaps []Snapshot
	for _, r := range current {
		if r.Problem != "" {
			t.Fatalf("%s does not read: %s", r.Key, r.Problem)
		}
		rate := rates[r.Key]
		for h := 168; h >= 1; h-- {
			at := now.Add(-time.Duration(h) * time.Hour)
			used := r.Used - rate*int64(h)/24
			if r.Key == LimitCap || r.Key == LimitReserve {
				if at.Month() != now.Month() {
					continue
				}
				used = max(used, 0)
			}
			snaps = append(snaps, Snapshot{At: at, Key: r.Key, Used: used, Of: r.Of})
		}
	}
	// A snapshot from before the trend window is not read.
	snaps = append(snaps, Snapshot{At: now.Add(-30 * 24 * time.Hour), Key: LimitDatabase, Used: 1, Of: 1})
	if err := Keep(ctx, s, snaps); err != nil {
		t.Fatal(err)
	}

	all, err := Limits(ctx, s, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{LimitCap, LimitReserve, LimitDisk, LimitDatabase, LimitFiles, LimitArtifacts} {
		l := find(t, all, key)
		if l.State == StateUnknown || l.Of <= 0 {
			t.Errorf("%s: state %s of %d (%s)", key, l.State, l.Of, l.Problem)
			continue
		}
		if want := int(float64(l.Used) * 100 / float64(l.Of)); l.Percent != want {
			t.Errorf("%s: %d%% used, and %d of %d is %d%%", key, l.Percent, l.Used, l.Of, want)
		}
		if l.Watch < 1 || l.Watch > 100 {
			t.Errorf("%s: watch level %d", key, l.Watch)
		}
		if !l.Trend.Known || len(l.Trend.Points) < 24 {
			t.Errorf("%s: a trend of %d points, known %v", key, len(l.Trend.Points), l.Trend.Known)
			continue
		}
		last := l.Trend.Points[len(l.Trend.Points)-1]
		if !last.At.Equal(now) || last.Used != l.Used {
			t.Errorf("%s: the trend's last point is %v %d, not the reading now", key, last.At, last.Used)
		}
		if l.Trend.Points[0].At.Before(now.Add(-cfg.TrendWindow)) {
			t.Errorf("%s: the trend reaches back to %v, past its window", key, l.Trend.Points[0].At)
		}
		rate := float64(rates[key])
		tolerance := 0.02
		if key == LimitDisk {
			tolerance = 0.1 // the volume is shared with everything else on this machine
		}
		if got := float64(l.Trend.PerDay); math.Abs(got-rate) > rate*tolerance {
			t.Errorf("%s: grows %d a day, and the snapshots grow %d", key, l.Trend.PerDay, rates[key])
		}
		switch key {
		case LimitCap, LimitReserve:
			if l.Trend.Resets == nil || !l.Trend.Resets.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) || l.Trend.AtReset == nil {
				t.Errorf("%s: resets %v, at reset %v", key, l.Trend.Resets, l.Trend.AtReset)
			} else if want := l.Used + int64(rate*10.5); math.Abs(float64(*l.Trend.AtReset-want)) > rate*0.3 {
				t.Errorf("%s: %d by the end of the month, and the rate says about %d", key, *l.Trend.AtReset, want)
			}
		}
		if l.Trend.FullAt == nil {
			if key != LimitDisk && float64(l.Of-l.Used)/rate < 100*365 {
				t.Errorf("%s: no fill date at %d a day with %d of %d used", key, l.Trend.PerDay, l.Used, l.Of)
			}
		} else if want := now.Add(time.Duration(float64(l.Of-l.Used) / float64(l.Trend.PerDay) * 24 * float64(time.Hour))); l.Trend.FullAt.Sub(want).Abs() > time.Hour {
			t.Errorf("%s: fills at %v, and the rate says %v", key, l.Trend.FullAt, want)
		}
	}
	if c := find(t, all, LimitCap); c.Used != 4_800_000 || c.Of != 100_000_000 || c.Percent != 4 || c.Watch != 80 || c.State != StateOK || c.Crossed {
		t.Errorf("the cap: %+v", c)
	}
	if r := find(t, all, LimitReserve); r.Used != 400_000 || r.Of != 2_000_000 || r.Percent != 20 {
		t.Errorf("the reserve: %+v", r)
	}
	if f := find(t, all, LimitFiles); f.Used != int64(len("harbour "))*4096 || f.Scope != ScopeTenant {
		t.Errorf("the files: %+v", f)
	}
	if d := find(t, all, LimitDisk); d.Scope != ScopeInstall || d.Unit != UnitBytes {
		t.Errorf("the disk: %+v", d)
	}
}

// The cap and the reserve are the usage report's own, read by the same
// function, so the two never disagree.
func TestTheCapIsTheUsageReports(t *testing.T) {
	ctx := context.Background()
	in := newInstall(t)
	s := newStore(t, in, "Operations Person Two")
	reg := registry(t)
	setAs(t, s, reg, tenantsettings.Admin, "usage.monthly_cap_usd", "5")
	addRecord(t, s, usage.Record{At: now.Add(-time.Hour), Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 4_100_000})
	addRecord(t, s, usage.Record{At: now.Add(-time.Hour), Kind: "chat", Model: "house-small", Class: models.ClassLocal, Outcome: usage.OutcomeOK, OutputTokens: 400})
	addRecord(t, s, usage.Record{At: now.AddDate(0, -1, 0), Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 9_000_000})
	rep, err := usagereport.Read(ctx, s, reg, usagereport.Ask{Month: "2026-11"}, now)
	if err != nil {
		t.Fatal(err)
	}
	all, err := Limits(ctx, s, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	c := find(t, all, LimitCap)
	if c.Used != rep.Cap.SpentMicros || c.Of != rep.Cap.LimitMicros || c.Watch != rep.Cap.WarnPercent {
		t.Errorf("the cap is %d of %d at %d%%, and the usage report says %d of %d at %d%%", c.Used, c.Of, c.Watch, rep.Cap.SpentMicros, rep.Cap.LimitMicros, rep.Cap.WarnPercent)
	}
	if c.Used != 4_100_000 || c.Percent != 82 || c.State != StateWatch || !c.Crossed {
		t.Errorf("the cap at 82%%: %+v", c)
	}
	// Past the cap it is full; a cap that does not read is crossed too.
	addRecord(t, s, usage.Record{At: now.Add(-time.Minute), Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 1_000_000})
	all, _ = Limits(ctx, s, reg, now)
	if c := find(t, all, LimitCap); c.State != StateFull || !c.Crossed || c.Percent != 102 {
		t.Errorf("the cap past its limit: %+v", c)
	}
	setAs(t, s, reg, tenantsettings.Admin, "usage.monthly_cap_usd", "-5")
	all, _ = Limits(ctx, s, reg, now)
	if c := find(t, all, LimitCap); c.State != StateUnknown || !c.Crossed || c.Problem == "" {
		t.Errorf("a cap that does not read: %+v", c)
	}
}

// The item's proof, second half, in the package: a crossed watch level is
// in Watch, and one that is not is not.
func TestACrossedWatchLevelIsWatched(t *testing.T) {
	ctx := context.Background()
	in := newInstall(t)
	s := newStore(t, in, "Operations Person Three")
	reg := withDefault(t, registry(t), SettingFilesRoom, "1") // a megabyte of room for files

	w, err := Watch(ctx, s, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range w.Crossed {
		if l.Key != LimitDisk { // this machine's own disk may be full
			t.Errorf("an empty tenant has a crossed limit: %+v", l)
		}
	}
	if err := s.Files().WriteFile("big", make([]byte, 900<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err = Watch(ctx, s, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	var files *Limit
	for i := range w.Crossed {
		if w.Crossed[i].Key == LimitFiles {
			files = &w.Crossed[i]
		}
	}
	if files == nil || files.Percent != 87 || files.State != StateWatch {
		t.Fatalf("900 KB of a megabyte is not watched: %+v", w.Crossed)
	}
	// The person raises the watch level past it, and it is not.
	setAs(t, s, reg, tenantsettings.Person, SettingWatchFiles, "90")
	w, _ = Watch(ctx, s, reg, now)
	for _, l := range w.Crossed {
		if l.Key == LimitFiles {
			t.Errorf("87%% is watched at a level of 90: %+v", l)
		}
	}
	// Past the room it is full whatever the level.
	if err := s.Files().WriteFile("bigger", make([]byte, 200<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	w, _ = Watch(ctx, s, reg, now)
	found := false
	for _, l := range w.Crossed {
		found = found || (l.Key == LimitFiles && l.State == StateFull)
	}
	if !found {
		t.Errorf("1.1 MB of a megabyte is not full: %+v", w.Crossed)
	}
}

// A trend fits a line, says when it fills and what it reaches by its
// reset, and says nothing from one point.
func TestTrend(t *testing.T) {
	var points []Point
	for d := 0; d <= 4; d++ {
		points = append(points, Point{At: now.AddDate(0, 0, d-4), Used: 100 + int64(d)*10, Of: 1000})
	}
	tr := trend(points, 140, 1000, now, nil)
	if !tr.Known || tr.PerDay != 10 || tr.FullAt == nil || !tr.FullAt.Equal(now.Add(86*24*time.Hour)) {
		t.Errorf("a line of 10 a day: %+v", tr)
	}
	reset := now.AddDate(0, 0, 10)
	tr = trend(points, 140, 1000, now, &reset)
	if tr.AtReset == nil || *tr.AtReset != 240 {
		t.Errorf("at its reset in ten days: %v", tr.AtReset)
	}
	if tr := trend(points[:1], 100, 1000, now, nil); tr.Known || tr.FullAt != nil {
		t.Errorf("one point: %+v", tr)
	}
	flat := []Point{{At: now.Add(-time.Hour), Used: 5, Of: 10}, {At: now, Used: 5, Of: 10}}
	if tr := trend(flat, 5, 10, now, nil); !tr.Known || tr.PerDay != 0 || tr.FullAt != nil {
		t.Errorf("flat: %+v", tr)
	}
	falling := []Point{{At: now.Add(-24 * time.Hour), Used: 9, Of: 10}, {At: now, Used: 5, Of: 10}}
	if tr := trend(falling, 5, 10, now, nil); tr.PerDay != -4 || tr.FullAt != nil {
		t.Errorf("falling: %+v", tr)
	}
	if tr := trend(nil, 0, 0, now, nil); tr.Points == nil {
		t.Error("no points is not an empty list")
	}
}

// The sampler records each limit, takes no second snapshot within its
// interval, and removes the ones past their keeping.
func TestSampleDue(t *testing.T) {
	ctx := context.Background()
	in := newInstall(t)
	one := newStore(t, in, "Operations Person Four")
	two := newStore(t, in, "Operations Person Five")
	reg := registry(t)
	c := Config{SnapshotEvery: time.Hour, SnapshotKeep: 48 * time.Hour}
	if err := Keep(ctx, one, []Snapshot{{At: now.Add(-72 * time.Hour), Key: LimitDatabase, Used: 7, Of: 9}}); err != nil {
		t.Fatal(err)
	}
	if n := SampleDue(ctx, in, reg, c, now, nil); n != 2 {
		t.Fatalf("sampled %d tenants, not 2", n)
	}
	for _, s := range []*tenant.Store{one, two} {
		for _, key := range []string{LimitCap, LimitReserve, LimitDisk, LimitDatabase, LimitFiles, LimitArtifacts} {
			got, err := Snapshots(ctx, s, key, now.Add(-100*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || !got[0].At.Equal(now) {
				t.Errorf("%s: %s has %d snapshots: %+v", s.TenantID(), key, len(got), got)
			}
		}
	}
	if n := SampleDue(ctx, in, reg, c, now.Add(59*time.Minute), nil); n != 0 {
		t.Errorf("sampled %d tenants within the hour", n)
	}
	if n := SampleDue(ctx, in, reg, c, now.Add(time.Hour), nil); n != 2 {
		t.Errorf("sampled %d tenants an hour later", n)
	}
	got, _ := Snapshots(ctx, one, LimitDatabase, time.Time{})
	if len(got) != 2 {
		t.Errorf("the database's snapshots after two samples and a removal: %+v", got)
	}
}

// Model health counts each model's calls in the window by how they ended,
// with the median time of the calls that were sent and the last error in
// words that hold no key.
func TestModelHealth(t *testing.T) {
	ctx := context.Background()
	in := newInstall(t)
	s := newStore(t, in, "Operations Person Six")
	reg := registry(t)
	for _, m := range []models.Model{
		{Name: "example-large", Label: "Example Large", Adapter: models.AdapterChatCompletions, Endpoint: "https://models.example/v1/path",
			ProviderModel: "example-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassHosted, Price: models.Price{Input: 2, Output: 10}},
		{Name: "house-small", Label: "House Small", Adapter: models.AdapterChatCompletions, Endpoint: "https://gateway.home.arpa/v1",
			ProviderModel: "small", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal},
		{Name: "idle-model", Label: "Idle Model", Adapter: models.AdapterChatCompletions, Endpoint: "https://idle.example/v1",
			ProviderModel: "idle", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassHosted},
	} {
		if err := models.Add(ctx, s, models.Limits{MaxModels: 10}, m); err != nil {
			t.Fatal(err)
		}
	}
	rec := func(model string, class models.Class, outcome usage.Outcome, ms int64, ago time.Duration, rid string) {
		addRecord(t, s, usage.Record{At: now.Add(-ago), RequestID: rid, Kind: "chat", Model: model, Class: class, Outcome: outcome, DurationMS: ms, CostMicros: 10})
	}
	// example-large: 8 ok and 2 errors in the window, one ok outside it.
	for i := range 8 {
		rec("example-large", models.ClassHosted, usage.OutcomeOK, int64(100*(i+1)), time.Duration(i+1)*time.Hour, "")
	}
	rec("example-large", models.ClassHosted, usage.OutcomeError, 50, 3*time.Hour, "req-old-error")
	rec("example-large", models.ClassHosted, usage.OutcomeError, 70, 2*time.Hour, "req-new-error")
	rec("example-large", models.ClassHosted, usage.OutcomeRefused, 0, 2*time.Hour, "")
	rec("example-large", models.ClassHosted, usage.OutcomeOK, 99999, 30*time.Hour, "")
	appendAudit(t, s, audit.Entry{Kind: audit.KindModel, RequestID: "req-new-error", Actor: "person", Action: "example-large", Outcome: audit.OutcomeError,
		Received: `{"kind":"auth","message":"the provider refused the key ` + fakeKey + `"}`})
	// house-small: every call failed. gone-model: left the registry.
	rec("house-small", models.ClassLocal, usage.OutcomeError, 3000, time.Hour, "")
	rec("house-small", models.ClassLocal, usage.OutcomeError, 1000, 2*time.Hour, "")
	rec("gone-model", models.ClassHosted, usage.OutcomeRefused, 0, time.Hour, "")

	cfg, err := ReadSettings(ctx, s, reg)
	if err != nil {
		t.Fatal(err)
	}
	part, err := readHealth(ctx, s, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ModelHealth{}
	for _, m := range part.Models {
		by[m.Name] = m
	}
	large := by["example-large"]
	if large.Calls != 11 || large.OK != 8 || large.Errors != 2 || large.Refusals != 1 || large.ErrorPercent != 20 || large.State != HealthErrors {
		t.Errorf("example-large: %+v", large)
	}
	if large.MedianMS != 350 { // 50 70 100 200 300 | 400 500 600 700 800
		t.Errorf("example-large's median is %d", large.MedianMS)
	}
	if large.CostMicros != 110 || !large.InRegistry || large.Label != "Example Large" {
		t.Errorf("example-large: %+v", large)
	}
	if large.Where != "chat_completions · models.example" && !strings.HasSuffix(large.Where, "· models.example") {
		t.Errorf("example-large is at %q", large.Where)
	}
	if strings.Contains(large.Where, "path") || strings.Contains(large.Where, "https") {
		t.Errorf("where names more than the host: %q", large.Where)
	}
	if e := large.LastError; e == nil || e.Kind != "auth" || !e.At.Equal(now.Add(-2*time.Hour)) || strings.Contains(e.Message, fakeKey) || !strings.Contains(e.Message, "refused the key") {
		t.Errorf("example-large's last error: %+v", large.LastError)
	}
	if h := by["house-small"]; h.State != HealthFailing || h.MedianMS != 2000 || h.LastError == nil || h.LastError.Kind != "other" {
		t.Errorf("house-small: %+v", h)
	}
	if i := by["idle-model"]; i.State != HealthIdle || i.Calls != 0 || i.Label != "Idle Model" {
		t.Errorf("idle-model: %+v", i)
	}
	if g := by["gone-model"]; g.State != HealthRefused || g.InRegistry || g.Calls != 1 {
		t.Errorf("gone-model: %+v", g)
	}
	raw, _ := json.Marshal(part)
	if strings.Contains(string(raw), fakeKey) {
		t.Error("model health holds the key")
	}
}

// A task's line, as tests write it.
type taskRow struct {
	id, chat, work, title, status, requestID string
	paused, waiting                          bool
	budget, created                          string
	ended                                    string
	failure                                  string
	turnID                                   string
}

func addTask(t *testing.T, s *tenant.Store, r taskRow) {
	t.Helper()
	exec(t, s, `INSERT INTO jobs(id, chat_id, work_chat_id, title, task, started_by, status, request_id, started_request, actor, budget_micros, max_rounds,
			paused, waiting, error, turn_id, created_at, ended_at)
		VALUES(?, ?, ?, ?, 'Invented work.', 'person', ?, ?, 'req-start', 'person', ?, 5, ?, ?, ?, ?, ?, ?)`,
		r.id, r.chat, r.work, r.title, r.status, r.requestID, r.budget, r.paused, r.waiting, r.failure, r.turnID, r.created, r.ended)
	exec(t, s, `INSERT INTO unattended_runs(request_id, kind, ref_id, title, budget_micros, created_at) VALUES(?, 'task', ?, ?, ?, ?)`,
		r.requestID, r.id, r.title, r.budget, r.created)
}

// Background tasks by state: the newest, the stuck (open, not paused or
// waiting, with an old round) and the failed within the window; and an
// open task's budget is a limit, with its spend's trend.
func TestJobsAndRunBudgets(t *testing.T) {
	ctx := context.Background()
	in := newInstall(t)
	s := newStore(t, in, "Operations Person Seven")
	reg := registry(t)
	from := newChat(t, s, "Harbour plans")
	stamp := func(d time.Duration) string { return now.Add(-d).UTC().Format(stampLayout) }
	works := map[string]string{}
	for _, id := range []string{"running", "stuck", "paused", "waiting", "done", "failed", "oldfail"} {
		works[id] = newChat(t, s, "Background task: "+id)
	}
	// The stuck task's round began two hours ago; the running one's a
	// minute ago.
	addTurn(t, s, "turn-stuck", works["stuck"], "req-stuck", "running", "", now.Add(-2*time.Hour))
	addTurn(t, s, "turn-running", works["running"], "req-running", "running", "", now.Add(-time.Minute))
	for _, r := range []taskRow{
		{id: "running", status: "running", turnID: "turn-running", created: stamp(3 * time.Hour)},
		{id: "stuck", status: "running", turnID: "turn-stuck", created: stamp(3 * time.Hour)},
		{id: "paused", status: "running", paused: true, created: stamp(5 * time.Hour)},
		{id: "waiting", status: "running", waiting: true, created: stamp(6 * time.Hour)},
		{id: "done", status: "done", created: stamp(7 * time.Hour), ended: stamp(6 * time.Hour)},
		{id: "failed", status: "failed", created: stamp(8 * time.Hour), ended: stamp(7 * time.Hour), failure: `{"code":"auth","message":"refused ` + fakeKey + `"}`},
		{id: "oldfail", status: "failed", created: stamp(80 * time.Hour), ended: stamp(79 * time.Hour), failure: `{"code":"failed","message":"old"}`},
	} {
		r.chat, r.work, r.title, r.requestID, r.budget = from, works[r.id], "Task "+r.id, "req-"+r.id, "1000000"
		addTask(t, s, r)
	}
	// The running task has spent 85 cents of its dollar, in two calls.
	addRecord(t, s, usage.Record{At: now.Add(-50 * time.Minute), RequestID: "req-running", Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 350_000})
	addRecord(t, s, usage.Record{At: now.Add(-10 * time.Minute), RequestID: "req-running", Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, CostMicros: 500_000})
	addRecord(t, s, usage.Record{At: now.Add(-9 * time.Minute), RequestID: "req-running", Kind: "chat", Model: "house-small", Class: models.ClassLocal, Outcome: usage.OutcomeOK})

	cfg, err := ReadSettings(ctx, s, reg)
	if err != nil {
		t.Fatal(err)
	}
	part, err := readJobs(ctx, s, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"running": 2, "paused": 1, "waiting": 1, "done": 1, "failed": 2}
	if part.Counts["running"] != 3 || part.Counts["paused"] != 1 || part.Counts["waiting"] != 1 || part.Counts["done"] != 1 || part.Counts["failed"] != 2 || part.Open != 4 {
		t.Errorf("counts %v, open %d; want about %v", part.Counts, part.Open, want)
	}
	if len(part.Newest) != 7 || part.Newest[0].ID != "oldfail" {
		t.Errorf("the newest: %d, first %s", len(part.Newest), part.Newest[0].ID)
	}
	if len(part.Stuck) != 1 || part.Stuck[0].ID != "stuck" {
		t.Errorf("stuck: %+v", part.Stuck)
	}
	// A task's spend is its usage records', not the column on its row.
	for _, l := range part.Newest {
		if l.ID == "running" && (l.SpentMicros != 850_000 || l.BudgetMicros != 1_000_000) {
			t.Errorf("the running task spent %d of %d, and its records say 850000", l.SpentMicros, l.BudgetMicros)
		}
	}
	if part.FailedCount != 1 || len(part.Failed) != 1 || part.Failed[0].ID != "failed" || strings.Contains(part.Failed[0].Error, fakeKey) || !strings.Contains(part.Failed[0].Error, "refused") {
		t.Errorf("failed in the window: %d %+v", part.FailedCount, part.Failed)
	}

	all, err := Limits(ctx, s, reg, now)
	if err != nil {
		t.Fatal(err)
	}
	var runs []Limit
	for _, l := range all {
		if strings.HasPrefix(l.Key, LimitRun+":") {
			runs = append(runs, l)
		}
	}
	if len(runs) != 4 {
		t.Fatalf("%d run budgets, not one for each of the 4 open tasks", len(runs))
	}
	r := find(t, all, LimitRun+":req-running")
	if r.Used != 850_000 || r.Of != 1_000_000 || r.Percent != 85 || r.State != StateWatch || !r.Crossed || r.ChatID != works["running"] || r.RunID != "running" {
		t.Errorf("the running task's budget: %+v", r)
	}
	if len(r.Trend.Points) != 3 || r.Trend.Points[1].Used != 850_000 || !r.Trend.Known || r.Trend.FullAt == nil {
		t.Errorf("the running task's trend: %+v", r.Trend)
	}
	if p := find(t, all, LimitRun+":req-paused"); p.Used != 0 || p.Crossed {
		t.Errorf("the paused task's budget: %+v", p)
	}
}

// A trace is everything a request id set off: its turns, its calls and
// their tokens and cost as its usage records hold them, its tool steps,
// its outcome, and where the inspector opens. A run's trace is its task's.
func TestTraces(t *testing.T) {
	ctx := context.Background()
	in := newInstall(t)
	s := newStore(t, in, "Operations Person Eight")
	reg := registry(t)
	c1 := newChat(t, s, "Kite shop accounts")
	c2 := newChat(t, s, "")
	work := newChat(t, s, "Background task: tide tables")

	addTurn(t, s, "t1", c1, "req-one", "done", "", now.Add(-50*time.Minute))
	addTurn(t, s, "t2", c2, "req-two", "failed", `{"code":"auth","message":"the provider refused `+fakeKey+`"}`, now.Add(-40*time.Minute))
	addTurn(t, s, "t3a", work, "req-task", "done", "", now.Add(-30*time.Minute))
	addTurn(t, s, "t3b", work, "req-task", "running", "", now.Add(-20*time.Minute))
	addTurn(t, s, "t3note", c1, "req-task", "done", "", now.Add(-25*time.Minute))
	addTask(t, s, taskRow{id: "tide", chat: c1, work: work, title: "Tide tables", status: "running", requestID: "req-task", budget: "1000000",
		created: now.Add(-31 * time.Minute).UTC().Format(stampLayout), turnID: "t3b"})

	for i, r := range []usage.Record{
		{RequestID: "req-one", Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, InputTokens: 1200, OutputTokens: 300, CacheReadTokens: 900, CostMicros: 7000},
		{RequestID: "req-one", Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, InputTokens: 1500, OutputTokens: 200, CacheWriteTokens: 40, CostMicros: 8000},
		{RequestID: "req-one", Kind: "title", Model: "house-small", Class: models.ClassLocal, Outcome: usage.OutcomeOK, InputTokens: 100, OutputTokens: 9},
		{RequestID: "req-two", Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeError},
		{RequestID: "req-two", Kind: "chat", Model: "house-small", Class: models.ClassLocal, Outcome: usage.OutcomeRefused},
		{RequestID: "req-task", Kind: "chat", Model: "house-small", Class: models.ClassLocal, Outcome: usage.OutcomeOK, InputTokens: 50, OutputTokens: 5},
	} {
		r.At = now.Add(-time.Duration(45-i) * time.Minute)
		addRecord(t, s, r)
	}
	for _, e := range []audit.Entry{
		{Kind: audit.KindTool, RequestID: "req-one", Actor: "person", Action: "search_chats", Outcome: audit.OutcomeStarted},
		{Kind: audit.KindTool, RequestID: "req-one", Actor: "person", Action: "search_chats", Outcome: audit.OutcomeOK, DurationMS: 12},
		{Kind: audit.KindTool, RequestID: "req-one", Actor: "person", Action: "web_fetch", Outcome: audit.OutcomeStarted},
		{Kind: audit.KindTool, RequestID: "req-one", Actor: "person", Action: "web_fetch", Outcome: audit.OutcomeError, DurationMS: 40},
		{Kind: audit.KindTool, RequestID: "req-task", Actor: "person", Action: "current_time", Outcome: audit.OutcomeOK, DurationMS: 1},
	} {
		appendAudit(t, s, e)
	}
	// Another person's turn is no trace of this one's.
	other := newStore(t, in, "Operations Person Nine")
	oc := newChat(t, other, "Another person's chat")
	addTurn(t, other, "o1", oc, "req-other", "done", "", now)

	cfg, err := ReadSettings(ctx, s, reg)
	if err != nil {
		t.Fatal(err)
	}
	traces, err := readTraces(ctx, s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 3 || traces[0].RequestID != "req-task" || traces[1].RequestID != "req-two" || traces[2].RequestID != "req-one" {
		var ids []string
		for _, tr := range traces {
			ids = append(ids, tr.RequestID)
		}
		t.Fatalf("traces %v, not the task, then two, then one", ids)
	}
	task, two, one := traces[0], traces[1], traces[2]
	if task.Kind != TraceTask || task.Title != "Tide tables" || task.Turns != 3 || task.Status != "running" || task.ChatID != work || task.TurnID != "t3b" || task.RunID != "tide" || task.Ended != nil {
		t.Errorf("the task's trace: %+v", task)
	}
	if len(task.Tools) != 1 || task.Tools[0].Name != "current_time" || task.Calls != 1 {
		t.Errorf("the task's steps: %+v", task)
	}
	if two.Status != "failed" || two.Title != "Untitled chat" || two.Errors != 1 || two.Refusals != 1 || strings.Contains(two.Error, fakeKey) || !strings.Contains(two.Error, "refused") {
		t.Errorf("the failed turn's trace: %+v", two)
	}
	if one.Kind != TraceChat || one.Title != "Kite shop accounts" || one.ChatID != c1 || one.TurnID != "t1" || one.Status != "done" || one.Ended == nil {
		t.Errorf("the chat's trace: %+v", one)
	}
	if !slices.Equal(one.Models, []string{"example-large", "house-small"}) || one.Calls != 3 {
		t.Errorf("the chat's models and calls: %v %d", one.Models, one.Calls)
	}
	if len(one.Tools) != 2 || one.Tools[0] != (ToolStep{Name: "search_chats", Outcome: "ok", DurationMS: 12}) || one.Tools[1].Outcome != "error" {
		t.Errorf("the chat's tools: %+v", one.Tools)
	}
	// Its tokens and cost are its usage records', summed.
	recs, err := usage.List(ctx, s, usage.Filter{RequestID: "req-one"})
	if err != nil {
		t.Fatal(err)
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
	if one.Usage != want || want.CostMicros != 15000 {
		t.Errorf("the chat's usage is %+v, and its records sum to %+v", one.Usage, want)
	}
	raw, _ := json.Marshal(traces)
	if strings.Contains(string(raw), fakeKey) || strings.Contains(string(raw), "Another person's chat") {
		t.Error("the traces hold a key, or another person's chat")
	}

	// The traces shown are as many as the setting says.
	setAs(t, s, reg, tenantsettings.Person, SettingTracesShown, "2")
	cfg, _ = ReadSettings(ctx, s, reg)
	if traces, _ := readTraces(ctx, s, cfg); len(traces) != 2 {
		t.Errorf("%d traces with two asked for", len(traces))
	}
}

// Settings outside their bounds are taken as the nearest bound.
func TestSettingsAreHeldToTheirBounds(t *testing.T) {
	ctx := context.Background()
	in := newInstall(t)
	s := newStore(t, in, "Operations Person Ten")
	reg := registry(t)
	setAs(t, s, reg, tenantsettings.Person, SettingTracesShown, "1000")
	setAs(t, s, reg, tenantsettings.Person, SettingWatchDisk, "0")
	setAs(t, s, reg, tenantsettings.Person, SettingHealthWindow, "1s")
	cfg, err := ReadSettings(ctx, s, reg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TracesShown != 100 || cfg.Watch[LimitDisk] != 1 || cfg.HealthWindow != 5*time.Minute || cfg.Room[LimitDatabase] != 2048<<20 {
		t.Errorf("%+v", cfg)
	}
	c, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.SnapshotEvery != time.Hour || c.SnapshotKeep != 840*time.Hour {
		t.Errorf("the install's defaults: %+v", c)
	}
}
