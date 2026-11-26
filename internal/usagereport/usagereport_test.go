package usagereport

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// Every person, chat, model and number here is invented.

// A request id of the generated records, and where its calls belong.
type origin struct {
	requestID string
	chatID    string // "" for the line of calls in no chat the person still has
	task      string // WorkTask, WorkSchedule or "" for the record's own kind
}

// world is a tenant with chats, turns, a background task, a scheduled
// task, a recap asked for through the API, a deleted chat, and usage
// records spread over them and over the end of October and the start of
// November 2026, in UTC.
type world struct {
	in      *tenant.Install
	s       *tenant.Store
	reg     []settings.Setting
	records []usage.Record
	origins map[string]origin // by request id
	titles  map[string]string // by chat id
}

var generatedModels = []struct {
	name  string
	class models.Class
	price int64 // millionths of a dollar for each thousand tokens
}{
	{"example-large", models.ClassHosted, 9000},
	{"example-small", models.ClassHosted, 700},
	{"house-trusted", models.ClassTrusted, 1500},
	{"house-small", models.ClassLocal, 0},
	{"house-large", models.ClassLocal, 0},
}

var generatedKinds = []string{"chat", "chat", "chat", "title", "summary", "memory", "embedding", "a-later-kind"}

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

func exec(t *testing.T, s *tenant.Store, q string, args ...any) {
	t.Helper()
	if _, err := s.DB().Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

const stampLayout = "2006-01-02T15:04:05.000Z"

func newChat(t *testing.T, w *world, title string) string {
	t.Helper()
	c, err := chat.Create(w.s)
	if err != nil {
		t.Fatal(err)
	}
	if title != "" {
		lim, err := chat.DefaultLimits()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := chat.Rename(w.s, lim, c.ID, title); err != nil {
			t.Fatal(err)
		}
	}
	w.titles[c.ID] = title
	return c.ID
}

// addTurn writes a turn of a chat sent under a request id, as the turn
// runner does.
func addTurn(t *testing.T, s *tenant.Store, chatID, requestID string) {
	t.Helper()
	exec(t, s, `INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES(?, ?, 'done', ?, 'person', '{}', ?)`,
		"turn-"+requestID+"-"+chatID, chatID, requestID, time.Now().UTC().Format(stampLayout))
}

func newWorld(t *testing.T, seed uint64, n int) *world {
	t.Helper()
	in := newInstall(t)
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	w := &world{in: in, s: newStore(t, in, "Report Person One"), reg: reg, origins: map[string]origin{}, titles: map[string]string{}}
	ctx := context.Background()

	// Five chats, one with no title, each with turns.
	var chats []string
	for _, title := range []string{"Harbour bakery names", "Raised bed plan", "", "Ferry timetable", "Kite shop accounts"} {
		chats = append(chats, newChat(t, w, title))
	}
	for i, c := range chats {
		for j := range 3 {
			id := fmt.Sprintf("req-chat-%d-%d", i, j)
			addTurn(t, w.s, c, id)
			w.origins[id] = origin{requestID: id, chatID: c}
		}
	}

	// A background task started in the first chat, working in a chat of its
	// own. Its note in the first chat is a turn under the same request id,
	// which must not pull its calls there.
	work := newChat(t, w, "Background: lighthouse walk times")
	exec(t, w.s, `INSERT INTO jobs(id, chat_id, work_chat_id, title, task, started_by, status, request_id, started_request, actor, budget_micros, max_rounds, created_at)
		VALUES('job-one', ?, ?, 'Lighthouse walk', 'Find the walk times.', 'person', 'done', 'req-task', 'req-chat-0-0', 'person', 1000000, 5, ?)`,
		chats[0], work, time.Now().UTC().Format(stampLayout))
	addTurn(t, w.s, work, "req-task")
	addTurn(t, w.s, chats[0], "req-task")
	w.origins["req-task"] = origin{requestID: "req-task", chatID: work, task: WorkTask}

	// A scheduled task: a schedule, its run, and the run's background task.
	runChat := newChat(t, w, "Morning tide brief, Wed 4 Nov 2026")
	stamp := time.Now().UTC().Format(stampLayout)
	exec(t, w.s, `INSERT INTO schedules(id, name, prompt, kind, created_at, updated_at) VALUES('sched-one', 'Morning tide brief', 'The tides today.', 'daily', ?, ?)`, stamp, stamp)
	exec(t, w.s, `INSERT INTO jobs(id, chat_id, work_chat_id, title, task, started_by, status, request_id, started_request, actor, budget_micros, max_rounds, created_at)
		VALUES('job-two', ?, ?, 'Morning tide brief', 'The tides today.', 'person', 'done', 'req-scheduled', 'req-run', 'schedule', 1000000, 5, ?)`,
		runChat, runChat, stamp)
	exec(t, w.s, `INSERT INTO schedule_runs(id, schedule_id, cause, due_at, state, request_id, actor, chat_id, job_id, created_at)
		VALUES('run-one', 'sched-one', 'schedule', ?, 'started', 'req-run', 'schedule', ?, 'job-two', ?)`, stamp, runChat, stamp)
	addTurn(t, w.s, runChat, "req-scheduled")
	w.origins["req-scheduled"] = origin{requestID: "req-scheduled", chatID: runChat, task: WorkSchedule}

	// A recap asked for through the recap route: no turn, only the API
	// call in the audit log names the chat.
	if _, err := audit.Append(ctx, w.s, audit.Entry{Kind: audit.KindAPI, RequestID: "req-recap", Actor: "person",
		Action: "POST /api/v1/chats/" + chats[1] + "/recap", Outcome: audit.OutcomeStarted}); err != nil {
		t.Fatal(err)
	}
	w.origins["req-recap"] = origin{requestID: "req-recap", chatID: chats[1]}

	// A chat that was deleted, with its turns: its calls stay in usage.
	gone := newChat(t, w, "A chat that was deleted")
	addTurn(t, w.s, gone, "req-gone")
	if _, err := audit.Append(ctx, w.s, audit.Entry{Kind: audit.KindAPI, RequestID: "req-gone", Actor: "person",
		Action: "POST /api/v1/chats/" + gone + "/turns", Outcome: audit.OutcomeStarted}); err != nil {
		t.Fatal(err)
	}
	if err := chat.Delete(w.s, gone); err != nil {
		t.Fatal(err)
	}
	w.origins["req-gone"] = origin{requestID: "req-gone"}

	// A call under an API call that names no chat, and one with no request
	// id at all.
	if _, err := audit.Append(ctx, w.s, audit.Entry{Kind: audit.KindAPI, RequestID: "req-elsewhere", Actor: "person",
		Action: "POST /api/v1/memory/forget", Outcome: audit.OutcomeStarted}); err != nil {
		t.Fatal(err)
	}
	w.origins["req-elsewhere"] = origin{requestID: "req-elsewhere"}
	w.origins[""] = origin{}

	// One model is in the registry with a label.
	if err := models.Add(ctx, w.s, models.Limits{MaxModels: 10}, models.Model{
		Name: "example-large", Label: "Example Large", Adapter: models.AdapterChatCompletions, Endpoint: "https://models.example/v1",
		ProviderModel: "example-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassHosted, Price: models.Price{Input: 2, Output: 10},
	}); err != nil {
		t.Fatal(err)
	}

	// The records, from 25 October to 6 November, with a few on each side
	// of midnight UTC on 1 November.
	ids := make([]string, 0, len(w.origins))
	for id := range w.origins {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	rng := rand.New(rand.NewPCG(seed, seed*7+1))
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	edges := []time.Time{
		time.Date(2026, 10, 31, 23, 59, 59, 999_000_000, time.UTC),
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 0, 0, 0, 1_000_000, time.UTC),
		time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 30, 23, 59, 59, 999_000_000, time.UTC),
		time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), // the first moment after a range that ends on 1 November
	}
	for i := range n {
		m := generatedModels[rng.IntN(len(generatedModels))]
		at := start.Add(time.Duration(rng.Int64N(int64(12 * 24 * time.Hour))))
		if i < len(edges) {
			at = edges[i]
		}
		r := usage.Record{
			At: at, RequestID: ids[rng.IntN(len(ids))], Kind: generatedKinds[rng.IntN(len(generatedKinds))],
			Model: m.name, Class: m.class, Outcome: usage.OutcomeOK,
			InputTokens: rng.IntN(6000), OutputTokens: rng.IntN(1500),
			CacheReadTokens: rng.IntN(20000), CacheWriteTokens: rng.IntN(3000), ThinkingTokens: 0,
		}
		r.CostMicros = int64(r.InputTokens+r.OutputTokens+r.CacheWriteTokens) * m.price / 1000
		r.CostMicros += int64(r.CacheReadTokens) * m.price / 10000
		if rng.IntN(12) == 0 { // a call the cap stopped: recorded, at no cost
			r.Outcome, r.CostMicros, r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens = usage.OutcomeRefused, 0, 0, 0, 0, 0
		}
		added, err := usage.Add(ctx, w.s, r)
		if err != nil {
			t.Fatal(err)
		}
		w.records = append(w.records, added)
	}
	return w
}

// expected sums the generated records in [from, to) by hand, without the
// package: the total, and the lines of each breakdown by their key.
type expected struct {
	total  Sum
	models map[string]Sum // model + class
	chats  map[string]Sum
	kinds  map[string]Sum
}

func (w *world) expect(from, to time.Time) expected {
	e := expected{models: map[string]Sum{}, chats: map[string]Sum{}, kinds: map[string]Sum{}}
	bump := func(m map[string]Sum, key string, r usage.Record) {
		s := m[key]
		s.add(r)
		m[key] = s
	}
	for _, r := range w.records {
		if r.At.Before(from) || !r.At.Before(to) {
			continue
		}
		o := w.origins[r.RequestID]
		e.total.add(r)
		bump(e.models, r.Model+"/"+string(r.Class), r)
		bump(e.chats, o.chatID, r)
		kind := r.Kind
		if o.task != "" {
			kind = o.task
		}
		bump(e.kinds, kind, r)
	}
	return e
}

// sumOf adds up a breakdown's lines.
func sumOf(rows []Row) Sum {
	var s Sum
	for _, r := range rows {
		s.merge(r.Sum)
	}
	return s
}

// sameSum compares two sums field by field and class by class.
func sameSum(t *testing.T, what string, got, want Sum) {
	t.Helper()
	if got != want {
		t.Errorf("%s:\n got %+v\nwant %+v", what, got, want)
	}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// The item's proof: the totals equal the sum of the usage records, and
// every breakdown's lines add up to the total, to the millionth of a
// dollar, cached tokens and the split by class included, for a month on
// each side of a month boundary in UTC and a range across it.
func TestTotalsEqualTheSumOfTheRecords(t *testing.T) {
	w := newWorld(t, 11, 900)
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		ask      Ask
		from, to time.Time
		month    string
	}{
		{Ask{Month: "2026-10"}, day(2026, 10, 1), day(2026, 11, 1), "2026-10"},
		{Ask{Month: "2026-11"}, day(2026, 11, 1), day(2026, 12, 1), "2026-11"},
		{Ask{}, day(2026, 11, 1), day(2026, 12, 1), "2026-11"}, // the default, this month
		{Ask{Period: PeriodLastMonth}, day(2026, 10, 1), day(2026, 11, 1), "2026-10"},
		{Ask{From: "2026-10-31", To: "2026-11-01"}, day(2026, 10, 31), day(2026, 11, 2), "2026-11"},
		{Ask{Period: PeriodLast7Days}, day(2026, 10, 31), day(2026, 11, 7), "2026-11"},
		{Ask{From: "2026-10-01", To: "2026-12-31"}, day(2026, 10, 1), day(2027, 1, 1), "2026-11"},
	}
	for _, c := range cases {
		name := fmt.Sprintf("%+v", c.ask)
		rep, err := Read(ctx, w.s, w.reg, c.ask, now)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := w.expect(c.from, c.to)
		if want.total.Calls == 0 {
			t.Fatalf("%s: the generated records left the period empty", name)
		}
		sameSum(t, name+": the total", rep.Total, want.total)

		// Each breakdown's lines add up to the total.
		for what, rows := range map[string][]Row{"models": rep.Models, "chats": rep.Chats, "kinds": rep.Kinds} {
			sameSum(t, name+": the lines by "+what, sumOf(rows), rep.Total)
		}

		// Each line is the records that belong to it, and nothing else.
		for _, r := range rep.Models {
			sameSum(t, name+": model "+r.Key, r.Sum, want.models[r.Key+"/"+string(r.Class)])
		}
		for _, r := range rep.Chats {
			sameSum(t, name+": chat "+r.Label, r.Sum, want.chats[r.Key])
		}
		for _, r := range rep.Kinds {
			sameSum(t, name+": kind "+r.Key, r.Sum, want.kinds[r.Key])
		}
		if len(rep.Models) != len(want.models) || len(rep.Chats) != len(want.chats) || len(rep.Kinds) != len(want.kinds) {
			t.Errorf("%s: %d, %d and %d lines, want %d, %d and %d", name, len(rep.Models), len(rep.Chats), len(rep.Kinds), len(want.models), len(want.chats), len(want.kinds))
		}

		// The cap's month, and its spend as the gate reads it.
		if rep.Cap.Month != c.month {
			t.Errorf("%s: the cap is about %s, want %s", name, rep.Cap.Month, c.month)
		}
		monthStart, _ := time.Parse("2006-01", c.month)
		inMonth := w.expect(monthStart, monthStart.AddDate(0, 1, 0)).total
		if rep.Cap.SpentMicros != inMonth.Capped() {
			t.Errorf("%s: the cap's spend is %d, and the month's calls to models that are not local cost %d", name, rep.Cap.SpentMicros, inMonth.Capped())
		}
		if rep.Period.Month == c.month && rep.Cap.SpentMicros != rep.Total.Capped() {
			t.Errorf("%s: the period is the cap's month, and the cap's spend %d is not the report's %d", name, rep.Cap.SpentMicros, rep.Total.Capped())
		}
	}
}

// The model, chat and kind lines are the right ones: a task's calls go
// under its work chat and not the chat its note was written in, a
// scheduled task's under its run's chat, a recap asked for through the
// API under its chat, and a deleted chat's calls under the line of calls
// in no chat the person still has.
func TestRecordsArePlaced(t *testing.T) {
	w := newWorld(t, 5, 400)
	rep, err := Read(context.Background(), w.s, w.reg, Ask{From: "2026-10-01", To: "2026-11-30", Rows: 100}, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, r := range rep.Chats {
		if r.Rest != 0 {
			t.Fatalf("with 100 rows a line was folded: %+v", r)
		}
		labels[r.Key] = r.Label
	}
	for id, title := range w.titles {
		if title == "A chat that was deleted" {
			if _, ok := labels[id]; ok {
				t.Error("a deleted chat has a line of its own")
			}
			continue
		}
		want := title
		if want == "" {
			want = labelChat
		}
		if got, ok := labels[id]; !ok || got != want {
			t.Errorf("chat %q is labelled %q (listed %v)", title, got, ok)
		}
	}
	if labels[""] != labelNoChat {
		t.Errorf("the line of calls in no chat is labelled %q", labels[""])
	}
	kinds := map[string]string{}
	for _, r := range rep.Kinds {
		kinds[r.Key] = r.Label
	}
	for kind, label := range map[string]string{WorkTask: "Background tasks", WorkSchedule: "Scheduled tasks", "chat": "Chat replies", "summary": "Summaries and recaps", "a-later-kind": "a-later-kind"} {
		if kinds[kind] != label {
			t.Errorf("kind %s is labelled %q, want %q", kind, kinds[kind], label)
		}
	}
	for _, r := range rep.Models {
		want := r.Key
		if r.Key == "example-large" {
			want = "Example Large"
		}
		if r.Label != want {
			t.Errorf("model %s is labelled %q, want %q", r.Key, r.Label, want)
		}
		if (r.Class == models.ClassLocal) != (r.CostMicros == 0) && r.Calls > 2 {
			t.Errorf("model %s (%s) cost %d over %d calls", r.Key, r.Class, r.CostMicros, r.Calls)
		}
	}
}

// A breakdown with more lines than it shows adds up the rest in one more
// line, so it still adds up to the total, and its lines are costliest
// first.
func TestTheRestAreAddedUp(t *testing.T) {
	w := newWorld(t, 23, 300)
	for _, rows := range []int{1, 2, 3, 4, 100} {
		rep, err := Read(context.Background(), w.s, w.reg, Ask{Month: "2026-11", Rows: rows}, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		all := w.expect(day(2026, 11, 1), day(2026, 12, 1))
		for what, b := range map[string]struct {
			rows []Row
			n    int
		}{"models": {rep.Models, len(all.models)}, "chats": {rep.Chats, len(all.chats)}, "kinds": {rep.Kinds, len(all.kinds)}} {
			sameSum(t, fmt.Sprintf("%d rows by %s", rows, what), sumOf(b.rows), rep.Total)
			wantLines := min(b.n, rows)
			if b.n > rows {
				wantLines++
				last := b.rows[len(b.rows)-1]
				if last.Rest != b.n-rows || last.Key != "" {
					t.Errorf("%d rows by %s: the last line adds up %d of %d lines", rows, what, last.Rest, b.n-rows)
				}
			}
			if len(b.rows) != wantLines {
				t.Errorf("%d rows by %s: %d lines, want %d", rows, what, len(b.rows), wantLines)
			}
			own := b.rows
			if b.n > rows {
				own = b.rows[:rows]
			}
			for i := 1; i < len(own); i++ {
				if own[i].CostMicros > own[i-1].CostMicros {
					t.Errorf("%d rows by %s: line %d cost more than the one above it", rows, what, i)
				}
			}
		}
		if rep.Rows != rows {
			t.Errorf("the report says it shows %d rows, want %d", rep.Rows, rows)
		}
	}
}

// The cap and the reserve are reported as they are set, with what is
// left and the state, and a cap that does not read is said to stop every
// paid call.
func TestTheCapAndTheReserve(t *testing.T) {
	in := newInstall(t)
	s := newStore(t, in, "Report Person Two")
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	add := func(kind string, class models.Class, cost int64) {
		t.Helper()
		if _, err := usage.Add(ctx, s, usage.Record{At: now.Add(-time.Hour), Kind: kind, Model: "example-" + string(class), Class: class, Outcome: usage.OutcomeOK, CostMicros: cost}); err != nil {
			t.Fatal(err)
		}
	}
	read := func() Report {
		t.Helper()
		rep, err := Read(ctx, s, reg, Ask{}, now)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}

	rep := read()
	if rep.Cap.State != CapOK || rep.Cap.LimitMicros != 100_000_000 || rep.Cap.WarnPercent != 80 || rep.Cap.WarnAtMicros != 80_000_000 || rep.Cap.LeftMicros != 100_000_000 || rep.Cap.Month != "2026-11" {
		t.Errorf("an empty month against the shipped cap: %+v", rep.Cap)
	}
	if rep.Reserve.LimitMicros != 2_000_000 || rep.Reserve.SpentMicros != 0 || rep.Reserve.LeftMicros != 2_000_000 {
		t.Errorf("the shipped reserve: %+v", rep.Reserve)
	}

	add("chat", models.ClassHosted, 60_000_000)
	add("chat", models.ClassLocal, 0)
	add("memory", models.ClassTrusted, 500_000)
	add("memory", models.ClassLocal, 0)
	add("chat", models.ClassTrusted, 19_500_000)
	rep = read()
	if rep.Cap.SpentMicros != 80_000_000 || rep.Cap.State != CapWarning || rep.Cap.LeftMicros != 20_000_000 {
		t.Errorf("at the warning threshold: %+v", rep.Cap)
	}
	if rep.Reserve.SpentMicros != 500_000 || rep.Reserve.LeftMicros != 1_500_000 {
		t.Errorf("the memory pass's spend of its reserve: %+v", rep.Reserve)
	}
	if rep.Total.Calls != 5 || rep.Total.Local.Calls != 2 || rep.Total.Local.CostMicros != 0 || rep.Total.Capped() != 80_000_000 {
		t.Errorf("the total and its split: %+v", rep.Total)
	}

	add("chat", models.ClassHosted, 20_000_000)
	if rep = read(); rep.Cap.State != CapReached || rep.Cap.LeftMicros != 0 {
		t.Errorf("at the cap: %+v", rep.Cap)
	}

	// The admin lowers the cap below the spend: nothing is left, never less.
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Admin, settingCap, "50"); err != nil {
		t.Fatal(err)
	}
	if rep = read(); rep.Cap.State != CapReached || rep.Cap.LeftMicros != 0 || rep.Cap.LimitMicros != 50_000_000 {
		t.Errorf("past a lowered cap: %+v", rep.Cap)
	}

	// A stored value that is a number and not a cap: the gate stops every
	// paid call, and the report says so instead of failing. (A value that
	// is not a number is not in force: tenantsettings falls back to the
	// default.)
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Admin, settingCap, "-5"); err != nil {
		t.Fatal(err)
	}
	if rep = read(); rep.Cap.State != CapUnread || rep.Cap.Problem == "" || rep.Cap.SpentMicros != 100_000_000 || rep.Cap.LeftMicros != 0 {
		t.Errorf("a cap that does not read: %+v", rep.Cap)
	}
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, memorySettingReserve, "-1"); err != nil {
		t.Fatal(err)
	}
	if rep = read(); rep.Reserve.Problem == "" || rep.Reserve.SpentMicros != 500_000 || rep.Reserve.LeftMicros != 0 {
		t.Errorf("a reserve that does not read: %+v", rep.Reserve)
	}
}

const memorySettingReserve = "knowledge.memory_reserve_usd"

// A report is one tenant's: another tenant's records never reach it.
func TestAReportIsOneTenants(t *testing.T) {
	w := newWorld(t, 3, 120)
	other := newStore(t, w.in, "Report Person Three")
	ctx := context.Background()
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	if _, err := usage.Add(ctx, other, usage.Record{At: at, RequestID: "req-chat-0-0", Kind: "chat", Model: "example-large", Class: models.ClassHosted, Outcome: usage.OutcomeOK, InputTokens: 10, CostMicros: 123}); err != nil {
		t.Fatal(err)
	}
	mine, err := Read(ctx, w.s, w.reg, Ask{Month: "2026-11"}, at)
	if err != nil {
		t.Fatal(err)
	}
	sameSum(t, "the first tenant's month", mine.Total, w.expect(day(2026, 11, 1), day(2026, 12, 1)).total)
	theirs, err := Read(ctx, other, w.reg, Ask{Month: "2026-11"}, at)
	if err != nil {
		t.Fatal(err)
	}
	if theirs.Total.Calls != 1 || theirs.Total.CostMicros != 123 || len(theirs.Chats) != 1 || theirs.Chats[0].Key != "" {
		t.Errorf("the other tenant's month holds %+v, chats %+v", theirs.Total, theirs.Chats)
	}
}

// The report reads only: reading it changes no row of the tenant's
// database.
func TestReadingChangesNothing(t *testing.T) {
	w := newWorld(t, 9, 60)
	count := func() string {
		var n, m int
		if err := w.s.DB().QueryRow("SELECT count(*), coalesce(max(id), 0) FROM usage_records").Scan(&n, &m); err != nil {
			t.Fatal(err)
		}
		var a int
		if err := w.s.DB().QueryRow("SELECT count(*) FROM audit_log").Scan(&a); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(n, m, a)
	}
	before := count()
	if _, err := Read(context.Background(), w.s, w.reg, Ask{}, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if after := count(); after != before {
		t.Errorf("reading the report changed the database: %s before, %s after", before, after)
	}
}

func TestChatInPath(t *testing.T) {
	for action, want := range map[string]string{
		"POST /api/v1/chats/abc123/turns":   "abc123",
		"POST /api/v1/chats/abc123/recap":   "abc123",
		"GET /api/v1/chats/abc123":          "abc123",
		"POST /api/v1/chats":                "",
		"POST /api/v1/chats/":               "",
		"POST /api/v1/memory/forget":        "",
		"POST /api/v1/projects/chats/x/y":   "",
		"POST /api/v2/chats/abc123/turns":   "",
		"post /api/v1/chats/abc123/turns":   "",
		"POST  /api/v1/chats/abc123/turns":  "",
		"POST /api/v1/chatsabc/turns":       "",
		"DELETE /api/v1/chats/abc-123_x.y/": "abc-123_x.y",
	} {
		if got := chatInPath(action); got != want {
			t.Errorf("chatInPath(%q) = %q, want %q", action, got, want)
		}
	}
}
