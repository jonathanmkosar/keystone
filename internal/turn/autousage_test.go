package turn

import (
	"context"
	"io"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usagereport"
	"github.com/jonathanmkosar/keystone/settings"
)

// Auto in usage (P14-07), the item's proof on the test tenant: its
// recorded ladder answers messages on Auto and off, a stronger model is
// asked for twice, and the usage report's Auto figures equal sums taken
// straight from its usage_records and router_verdicts tables.
func TestAutoInUsageIsTheTestTenantsRecordsAndVerdicts(t *testing.T) {
	g := newRig(t)
	g.recordedLadder()
	ctx := context.Background()
	// The top step's price, for the report only: an entry in the test
	// tenant's registry under the large step's name. The test tenant's
	// turns are opened from its recorded models and not from the registry
	// (decision 0695), so the turns are as they always are.
	price := models.Price{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}
	g.with(g.test.ID, func(s *tenant.Store) {
		if err := models.Add(ctx, s, models.Limits{MaxModels: 10}, models.Model{
			Name: TestLargeModel, Label: "Recorded turns, large, priced", Adapter: models.AdapterChatCompletions,
			Endpoint: "https://models.example/v1", ProviderModel: "example-large", ContextWindow: 200000, OutputLimit: 8192,
			Class: models.ClassHosted, Price: price,
		}); err != nil {
			t.Fatal(err)
		}
	})
	sv := g.serveAuto()
	id := sv.newChat(g.test.ID)

	_, _, easy := sv.say(g.test.ID, id, autoEasy, nil)
	sv.say(g.test.ID, id, autoPlan, nil)
	sv.say(g.test.ID, id, autoHard, nil)
	stronger := func(turnID string) Turn {
		t.Helper()
		resp := sv.request(g.test.ID, "POST", "chats/"+id+"/turns/"+turnID+"/stronger", "", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("stronger answered %d: %s", resp.StatusCode, raw)
		}
		return last(t, readEvents(t, resp.Body, nil))
	}
	middle := stronger(easy.ID)
	stronger(middle.ID)
	// A turn on the model picked is not on Auto, and its calls are not
	// counted.
	if _, route, _ := sv.say(g.test.ID, id, "Thank you.", func(b *SendBody) { b.Model = TestModelName }); route.Auto {
		t.Fatalf("a turn on the model picked was routed %+v", route)
	}

	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		now := time.Now()
		rep, err := usagereport.Read(ctx, s, reg, usagereport.Ask{Period: "this_month"}, now)
		if err != nil {
			t.Fatal(err)
		}
		a := rep.Auto
		db := s.DB()
		one := func(q string, into ...any) {
			t.Helper()
			if err := db.QueryRowContext(ctx, q).Scan(into...); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		var turns, asked, rewrites int
		one("SELECT count(DISTINCT turn_id) FROM router_verdicts", &turns)
		one("SELECT count(DISTINCT turn_id) FROM router_verdicts WHERE stronger_asked = 1", &asked)
		one("SELECT count(DISTINCT turn_id) FROM router_verdicts WHERE rules_only_why = '"+router.OnlyStronger+"'", &rewrites)
		// Three messages and two rewrites; the first two were asked to be
		// written again.
		if a.Turns != turns || a.StrongerAsked != asked || a.Rewrites != rewrites || turns != 5 || asked != 2 || rewrites != 2 {
			t.Errorf("the report counts %d turns, %d asked, %d rewrites; the table %d, %d, %d", a.Turns, a.StrongerAsked, a.Rewrites, turns, asked, rewrites)
		}
		sum := func(kind string) (calls int, in, out, cost int64) {
			one(`SELECT count(*), coalesce(sum(input_tokens), 0), coalesce(sum(output_tokens), 0), coalesce(sum(cost_micros), 0)
				FROM usage_records WHERE kind = '`+kind+`' AND request_id IN (SELECT request_id FROM router_verdicts)`, &calls, &in, &out, &cost)
			return
		}
		calls, in, out, cost := sum("chat")
		if a.Replies.Calls != calls || a.Replies.InputTokens != in || a.Replies.OutputTokens != out || a.Replies.CostMicros != cost || calls < 5 {
			t.Errorf("the replies are %+v; the table has %d calls, %d in, %d out, %d micros", a.Replies.Total, calls, in, out, cost)
		}
		dCalls, dIn, dOut, dCost := sum("decision")
		if a.Decisions.Calls != dCalls || a.Decisions.InputTokens != dIn || a.Decisions.OutputTokens != dOut || a.Decisions.CostMicros != dCost || dCalls < 3 {
			t.Errorf("the decisions are %+v; the table has %d calls, %d in, %d out, %d micros", a.Decisions.Total, dCalls, dIn, dOut, dCost)
		}
		if a.CostMicros != cost+dCost {
			t.Errorf("Auto cost %d, want %d", a.CostMicros, cost+dCost)
		}
		// The top step's cost, call by call from the table.
		rows, err := db.QueryContext(ctx, `SELECT outcome, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens FROM usage_records
			WHERE kind = 'chat' AND request_id IN (SELECT request_id FROM router_verdicts)`)
		if err != nil {
			t.Fatal(err)
		}
		var top int64
		for rows.Next() {
			var outcome string
			var i, o, cr, cw float64
			if err := rows.Scan(&outcome, &i, &o, &cr, &cw); err != nil {
				t.Fatal(err)
			}
			if outcome != "refused" {
				top += int64(math.Round(i*3 + o*15 + cr*0.3 + cw*3.75))
			}
		}
		rows.Close()
		if a.TopStep.Name != TestLargeModel || a.TopStep.Problem != "" || a.TopStep.CostMicros != top || top == 0 || a.SavedMicros != top-a.CostMicros {
			t.Errorf("the top step is %+v, saving %d; from the table it costs %d", a.TopStep, a.SavedMicros, top)
		}
		// Every reply's call on Auto is a part of the report's chat replies.
		var chatCalls int
		for _, k := range rep.Kinds {
			if k.Key == "chat" {
				chatCalls = k.Calls
			}
		}
		if chatCalls <= a.Replies.Calls {
			t.Errorf("the chat replies are %d calls and those on Auto %d: the turn off Auto is missing", chatCalls, a.Replies.Calls)
		}
	})
}
