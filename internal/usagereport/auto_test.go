package usagereport

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// Every person, chat, model and number here is invented.

// autoVerdict is one row of router_verdicts as the test writes it.
type autoVerdict struct {
	turnID, requestID, why string
	asked                  bool
	at                     time.Time
}

// autoWorld is a tenant with a ladder, turns on Auto and off, their
// verdicts, and usage records under them from 25 October to 6 November
// 2026, in UTC.
type autoWorld struct {
	s        *tenant.Store
	reg      []settings.Setting
	records  []usage.Record
	verdicts []autoVerdict
	routed   map[string]bool // request ids with a verdict
}

// The registry of the Auto world: a local, a middle and a top step, a
// decision model and an embedding model.
var autoModels = []models.Model{
	{Name: "house-small", Label: "House small", Adapter: models.AdapterChatCompletions, Endpoint: "http://192.168.1.20:11434/v1",
		ProviderModel: "house-small-1", ContextWindow: 32768, OutputLimit: 4096, Class: models.ClassLocal},
	{Name: "example-middle", Label: "Example Middle", Adapter: models.AdapterChatCompletions, Endpoint: "https://models.example/v1",
		ProviderModel: "example-middle-1", ContextWindow: 200000, OutputLimit: 8192, Class: models.ClassTrusted, Price: models.Price{Input: 1, Output: 5}},
	{Name: "example-top", Label: "Example Top", Adapter: models.AdapterChatCompletions, Endpoint: "https://models.example/v1",
		ProviderModel: "example-top-1", ContextWindow: 200000, OutputLimit: 8192, Class: models.ClassHosted,
		Price: models.Price{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}},
	{Name: "house-decide", Label: "House decisions", Adapter: models.AdapterSystemone, Endpoint: "http://192.168.1.20:11434",
		ProviderModel: "house-decide-1", ContextWindow: 8192, OutputLimit: 16, Class: models.ClassLocal, Capabilities: []models.Capability{models.CapDecision}},
	{Name: "example-embed", Label: "Example Embed", Adapter: models.AdapterChatCompletions, Endpoint: "https://models.example/v1",
		ProviderModel: "example-embed-1", ContextWindow: 8192, OutputLimit: 1, Class: models.ClassHosted,
		Capabilities: []models.Capability{models.CapEmbedding}, Price: models.Price{Input: 0.1}},
}

// topByHand is a call's tokens at example-top's price, worked out here
// and not by models.Price.
func topByHand(r usage.Record) int64 {
	if r.Outcome == usage.OutcomeRefused {
		return 0
	}
	return int64(math.Round(float64(r.InputTokens)*3 + float64(r.OutputTokens)*15 + float64(r.CacheReadTokens)*0.3 + float64(r.CacheWriteTokens)*3.75))
}

func setFor(t *testing.T, s *tenant.Store, reg []settings.Setting, key, value string) {
	t.Helper()
	if err := tenantsettings.Set(context.Background(), s, reg, tenantsettings.Admin, key, value); err != nil {
		t.Fatal(err)
	}
}

func newAutoWorld(t *testing.T, seed uint64) *autoWorld {
	t.Helper()
	in := newInstall(t)
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	w := &autoWorld{s: newStore(t, in, "Auto Person One"), reg: reg, routed: map[string]bool{}}
	ctx := context.Background()
	for _, m := range autoModels {
		if err := models.Add(ctx, w.s, models.Limits{MaxModels: 10}, m); err != nil {
			t.Fatal(err)
		}
	}
	// The top step is example-top: the embedding model above it takes no
	// chat calls, and the last name is in no registry.
	setFor(t, w.s, reg, router.KeyLadder, "house-small, example-middle<=100000, example-top, example-embed, gone-model")

	c, err := chat.Create(w.s)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(seed, seed*3+1))
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	// The first moments are on each side of midnight UTC on 1 November,
	// so a turn's verdict and its calls fall in different months.
	edges := []time.Time{
		time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC),
		time.Date(2026, 10, 31, 23, 59, 59, 900_000_000, time.UTC),
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 23, 59, 59, 999_000_000, time.UTC),
	}
	chatModels := []models.Model{autoModels[0], autoModels[1], autoModels[2],
		{Name: "left-model", Class: models.ClassHosted}} // a model that has left the registry
	add := func(r usage.Record) {
		t.Helper()
		added, err := usage.Add(ctx, w.s, r)
		if err != nil {
			t.Fatal(err)
		}
		w.records = append(w.records, added)
	}
	for i := range 90 {
		requestID := fmt.Sprintf("req-%03d", i)
		turnID := fmt.Sprintf("turn-%03d", i)
		at := start.Add(time.Duration(rng.Int64N(int64(12 * 24 * time.Hour)))).Truncate(time.Millisecond)
		if i < len(edges) {
			at = edges[i]
		}
		onAuto := i%4 != 3
		exec(t, w.s, `INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at, auto) VALUES(?, ?, 'done', ?, 'person', '{}', ?, ?)`,
			turnID, c.ID, requestID, at.Format(stampLayout), onAuto)
		if onAuto {
			v := autoVerdict{turnID: turnID, requestID: requestID, at: at, asked: rng.IntN(6) == 0}
			if rng.IntN(8) == 0 {
				v.why = router.OnlyStronger
			}
			w.verdicts = append(w.verdicts, v)
			if i == 0 || rng.IntN(7) == 0 { // routed again when its local step did not answer; the first across midnight
				again := v
				again.at, again.why, again.asked = at.Add(2*time.Second), "local_unavailable", false
				w.verdicts = append(w.verdicts, again)
			}
			w.routed[requestID] = true
			add(usage.Record{At: at, RequestID: requestID, Kind: "decision", Model: "house-decide", Class: models.ClassLocal,
				Outcome: usage.OutcomeOK, InputTokens: 300 + rng.IntN(200), OutputTokens: 2})
		}
		// The turn's calls: its tool steps, a title, sometimes a summary.
		for k := range 1 + rng.IntN(3) {
			m := chatModels[rng.IntN(len(chatModels))]
			r := usage.Record{At: at.Add(time.Duration(k+1) * 150 * time.Millisecond), RequestID: requestID, Kind: "chat",
				Model: m.Name, Class: m.Class, Outcome: usage.OutcomeOK,
				InputTokens: rng.IntN(9000), OutputTokens: rng.IntN(2000), CacheReadTokens: rng.IntN(20000), CacheWriteTokens: rng.IntN(3000)}
			r.CostMicros = m.Price.CostMicros(r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens)
			if m.Name == "left-model" {
				r.CostMicros = int64(r.InputTokens + r.OutputTokens)
			}
			switch rng.IntN(10) {
			case 0: // stopped by the cap: recorded, never sent
				r.Outcome, r.CostMicros, r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens = usage.OutcomeRefused, 0, 0, 0, 0, 0
			case 1: // refused with an estimate kept: still never sent
				r.Outcome, r.CostMicros = usage.OutcomeRefused, 0
			}
			add(r)
		}
		add(usage.Record{At: at.Add(time.Second), RequestID: requestID, Kind: "title", Model: "example-middle", Class: models.ClassTrusted,
			Outcome: usage.OutcomeOK, InputTokens: 400, OutputTokens: 12, CostMicros: 460})
	}
	// A chat call under no request id is not Auto's.
	add(usage.Record{At: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC), Kind: "chat", Model: "example-top", Class: models.ClassHosted,
		Outcome: usage.OutcomeOK, InputTokens: 100, OutputTokens: 100, CostMicros: 1800})

	for _, v := range w.verdicts {
		exec(t, w.s, `INSERT INTO router_verdicts(id, turn_id, request_id, shown, step, effort, rules_only, rules_only_why, stronger_asked, created_at)
			VALUES(?, ?, ?, '{}', 'example-middle', 'medium', ?, ?, ?, ?)`,
			fmt.Sprintf("verdict-%s-%d", v.turnID, v.at.UnixMilli()), v.turnID, v.requestID, v.why != "", v.why, v.asked, v.at.Format(stampLayout))
	}
	return w
}

// expect is the Auto figures of the days from from to before to, summed
// here from the records and the verdicts the world wrote.
func (w *autoWorld) expect(from, to time.Time) Auto {
	var out Auto
	out.TopStep = TopStep{Name: "example-top", Label: "Example Top", Class: models.ClassHosted}
	for _, r := range w.records {
		if r.At.Before(from) || !r.At.Before(to) || !w.routed[r.RequestID] {
			continue
		}
		switch r.Kind {
		case "chat":
			out.Replies.add(r)
			out.TopStep.CostMicros += topByHand(r)
		case "decision":
			out.Decisions.add(r)
		}
	}
	out.CostMicros = out.Replies.CostMicros + out.Decisions.CostMicros
	out.SavedMicros = out.TopStep.CostMicros - out.CostMicros
	type turn struct {
		first          time.Time
		asked, rewrite bool
	}
	turns := map[string]*turn{}
	for _, v := range w.verdicts {
		tn, ok := turns[v.turnID]
		if !ok {
			tn = &turn{first: v.at}
			turns[v.turnID] = tn
		}
		if v.at.Before(tn.first) {
			tn.first = v.at
		}
		tn.asked = tn.asked || v.asked
		tn.rewrite = tn.rewrite || v.why == router.OnlyStronger
	}
	for _, tn := range turns {
		if tn.first.Before(from) || !tn.first.Before(to) {
			continue
		}
		out.Turns++
		if tn.asked {
			out.StrongerAsked++
		}
		if tn.rewrite {
			out.Rewrites++
		}
	}
	return out
}

func sameAuto(t *testing.T, what string, got, want Auto) {
	t.Helper()
	sameSum(t, what+" replies", got.Replies, want.Replies)
	sameSum(t, what+" decisions", got.Decisions, want.Decisions)
	got.Replies, got.Decisions, want.Replies, want.Decisions = Sum{}, Sum{}, Sum{}, Sum{}
	if got != want {
		t.Errorf("%s: the Auto figures are\n%+v\nwant\n%+v", what, got, want)
	}
}

// The item's proof on generated data: every Auto figure is the usage
// records and the verdicts summed by hand, for a month on each side of
// the boundary, a range across it, and this month.
func TestAutoIsTheRecordsAndTheVerdicts(t *testing.T) {
	w := newAutoWorld(t, 7)
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name     string
		ask      Ask
		from, to time.Time
	}{
		{"October", Ask{Month: "2026-10"}, day(2026, 10, 1), day(2026, 11, 1)},
		{"November", Ask{Month: "2026-11"}, day(2026, 11, 1), day(2026, 12, 1)},
		{"this month", Ask{Period: "this_month"}, day(2026, 11, 1), day(2026, 12, 1)},
		{"across the boundary", Ask{From: "2026-10-31", To: "2026-11-01"}, day(2026, 10, 31), day(2026, 11, 2)},
		{"the last 7 days", Ask{Period: "last_7_days"}, day(2026, 10, 31), day(2026, 11, 7)},
	} {
		rep, err := Read(ctx, w.s, w.reg, c.ask, now)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		want := w.expect(c.from, c.to)
		sameAuto(t, c.name, rep.Auto, want)
		if want.Turns == 0 || want.Replies.Calls == 0 || want.Decisions.Calls == 0 {
			t.Errorf("%s: the world has nothing on Auto to sum: %+v", c.name, want)
		}
		// The replies and decisions are a part of the report's total.
		if rep.Auto.Replies.Calls+rep.Auto.Decisions.Calls > rep.Total.Calls || rep.Auto.CostMicros > rep.Total.CostMicros {
			t.Errorf("%s: Auto %+v is more than the total %+v", c.name, rep.Auto, rep.Total.Total)
		}
	}
	// Across the boundary, the month of the first verdict decides a
	// turn's month, while each call is in its own.
	oct, _ := Read(ctx, w.s, w.reg, Ask{Month: "2026-10"}, now)
	nov, _ := Read(ctx, w.s, w.reg, Ask{Month: "2026-11"}, now)
	all := w.expect(day(2026, 10, 1), day(2026, 12, 1))
	if oct.Auto.Turns+nov.Auto.Turns != all.Turns || oct.Auto.Replies.Calls+nov.Auto.Replies.Calls != all.Replies.Calls {
		t.Errorf("October %d turns and %d replies and November %d and %d do not add up to %d and %d",
			oct.Auto.Turns, oct.Auto.Replies.Calls, nov.Auto.Turns, nov.Auto.Replies.Calls, all.Turns, all.Replies.Calls)
	}
	if all.Rewrites == 0 || all.StrongerAsked == 0 {
		t.Errorf("the world has no rewrite or no stronger model asked for: %+v", all)
	}
}

// The top step is the highest chat model of the ladder in the registry,
// as the ladder stands now; each ladder with none says why.
func TestTheTopStep(t *testing.T) {
	w := newAutoWorld(t, 11)
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ladder, name, problem string
	}{
		{"house-small, example-middle, example-top", "example-top", ""},
		{"example-top, example-middle", "example-middle", ""},
		{"house-small", "house-small", ""},
		{"house-small, house-decide", "house-small", ""},
		{"", "", "no ladder is set"},
		{"gone-model, other-gone", "", "no step of the ladder is a chat model in your registry"},
		{"house-decide, example-embed", "", "no step of the ladder is a chat model in your registry"},
	} {
		setFor(t, w.s, w.reg, router.KeyLadder, c.ladder)
		rep, err := Read(ctx, w.s, w.reg, Ask{Month: "2026-11"}, now)
		if err != nil {
			t.Fatalf("%q: %v", c.ladder, err)
		}
		a := rep.Auto
		if a.TopStep.Name != c.name || !strings.Contains(a.TopStep.Problem, c.problem) || (c.problem == "") != (a.TopStep.Problem == "") {
			t.Errorf("ladder %q: the top step is %+v, want %q with the problem %q", c.ladder, a.TopStep, c.name, c.problem)
		}
		if c.problem != "" && (a.TopStep.CostMicros != 0 || a.SavedMicros != 0) {
			t.Errorf("ladder %q: a top step with a problem priced the replies at %d, saving %d", c.ladder, a.TopStep.CostMicros, a.SavedMicros)
		}
		// What Auto cost does not rest on the ladder.
		if want := w.expect(day(2026, 11, 1), day(2026, 12, 1)); a.CostMicros != want.CostMicros || a.Turns != want.Turns {
			t.Errorf("ladder %q: Auto cost %d over %d turns, want %d over %d", c.ladder, a.CostMicros, a.Turns, want.CostMicros, want.Turns)
		}
	}
	// A ladder that does not read is a problem, not a failed report.
	if err := tenantsettings.Set(ctx, w.s, w.reg, tenantsettings.Admin, router.KeyLadder, "house-small<=0"); err == nil {
		rep, err := Read(ctx, w.s, w.reg, Ask{Month: "2026-11"}, now)
		if err != nil || !strings.Contains(rep.Auto.TopStep.Problem, "the ladder does not read") {
			t.Errorf("a ladder that does not read gave %+v, %v", rep.Auto.TopStep, err)
		}
	}
}

// A tenant with nothing on Auto has zero figures and the top step of its
// ladder; one with no ladder says so.
func TestNothingOnAuto(t *testing.T) {
	in := newInstall(t)
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	s := newStore(t, in, "Auto Person Two")
	rep, err := Read(context.Background(), s, reg, Ask{}, time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if a := rep.Auto; a.Turns != 0 || a.Replies.Calls != 0 || a.CostMicros != 0 || !strings.Contains(a.TopStep.Problem, "no ladder") {
		t.Errorf("nothing on Auto gave %+v", a)
	}
}
