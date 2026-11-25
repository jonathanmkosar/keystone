package inspector

import (
	"testing"

	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// rec is a usage record with only what Calls reads, and numbers that make
// each one's share of a sum plain.
func rec(id int64, kind, model string, outcome usage.Outcome, input int) usage.Record {
	return usage.Record{ID: id, Kind: kind, Model: model, Outcome: outcome, InputTokens: input, OutputTokens: input / 10, ThinkingTokens: 1, CostMicros: int64(input)}
}

func built(step int, model string) Context {
	return Context{Step: step, Record: contextbuild.Record{Model: model, Tokens: step * 100}}
}

// A turn of three chat calls: the first fell back from a hosted model to a
// local one, the second answered, and the third was stopped. A title call
// and two summary calls were made beside them, one of them after a
// summary model that did not answer. Each call gets its own records, the
// chat calls their steps, and the totals are every record's.
func TestCallsJoinTheRecordsToTheSteps(t *testing.T) {
	contexts := []Context{built(2, "paid-large"), built(1, "paid-large"), built(3, "paid-large")}
	records := []usage.Record{
		rec(10, "chat", "paid-large", usage.OutcomeError, 0),
		rec(11, "chat", "house-small", usage.OutcomeOK, 1000),
		rec(12, "summary", "summary-model", usage.OutcomeError, 0),
		rec(13, "chat", "house-small", usage.OutcomeOK, 1200),
		rec(14, "summary", "house-small", usage.OutcomeOK, 3000),
		rec(15, "summary", "house-small", usage.OutcomeOK, 2000),
		rec(16, "chat", "house-small", usage.OutcomeStopped, 1300),
		rec(17, "title", "house-small", usage.OutcomeOK, 50),
	}
	calls, total := Calls(contexts, records)
	type want struct {
		step  int
		kind  string
		model string
		ids   []int64
	}
	wants := []want{
		{1, "chat", "house-small", []int64{10, 11}},
		{2, "chat", "house-small", []int64{13}},
		{3, "chat", "house-small", []int64{16}},
		{0, "summary", "house-small", []int64{12, 14}},
		{0, "summary", "house-small", []int64{15}},
		{0, "title", "house-small", []int64{17}},
	}
	if len(calls) != len(wants) {
		t.Fatalf("%d calls, want %d: %+v", len(calls), len(wants), calls)
	}
	var sum Totals
	for i, w := range wants {
		c := calls[i]
		var ids []int64
		for _, r := range c.Usage {
			ids = append(ids, r.ID)
		}
		if c.Step != w.step || c.Kind != w.kind || c.Model != w.model || len(ids) != len(w.ids) {
			t.Fatalf("call %d is step %d, %s on %s with records %v; want %+v", i, c.Step, c.Kind, c.Model, ids, w)
		}
		for j := range ids {
			if ids[j] != w.ids[j] {
				t.Fatalf("call %d has records %v, want %v", i, ids, w.ids)
			}
		}
		if (c.Context != nil) != (w.kind == "chat") || (c.Context != nil && c.Context.Tokens != w.step*100) {
			t.Errorf("call %d has the context %+v", i, c.Context)
		}
		if c.Total != Sum(c.Usage) {
			t.Errorf("call %d totals %+v, and its records %+v", i, c.Total, Sum(c.Usage))
		}
		sum.Calls += c.Total.Calls
		sum.InputTokens += c.Total.InputTokens
		sum.OutputTokens += c.Total.OutputTokens
		sum.ThinkingTokens += c.Total.ThinkingTokens
		sum.CostMicros += c.Total.CostMicros
	}
	if total != Sum(records) || total.Calls != 8 || total.InputTokens != 8550 || total.CostMicros != 8550 || total.ThinkingTokens != 8 {
		t.Errorf("the totals are %+v", total)
	}
	if sum != total {
		t.Errorf("the calls add up to %+v, and the totals are %+v", sum, total)
	}
}

// A last call that failed on every model of its route is one call. A step
// whose call never ended has its context and no record, and a record with
// no context still gets a step.
func TestCallsWithAFailureAndAMissingRecord(t *testing.T) {
	failed := []usage.Record{
		rec(1, "chat", "house-small", usage.OutcomeOK, 100),
		rec(2, "chat", "paid-large", usage.OutcomeRefused, 0),
		rec(3, "chat", "house-small", usage.OutcomeError, 0),
	}
	calls, total := Calls([]Context{built(1, "house-small"), built(2, "paid-large")}, failed)
	if len(calls) != 2 || len(calls[0].Usage) != 1 || len(calls[1].Usage) != 2 || calls[1].Model != "house-small" || total.Calls != 3 {
		t.Fatalf("a failed last call: %+v, %+v", calls, total)
	}

	// The server stopped during the second call: it has a context and no
	// record.
	calls, _ = Calls([]Context{built(1, "house-small"), built(2, "house-small")}, failed[:1])
	if len(calls) != 2 || calls[1].Step != 2 || len(calls[1].Usage) != 0 || calls[1].Model != "house-small" || calls[1].Total.Calls != 0 {
		t.Fatalf("a call that never ended: %+v", calls)
	}

	// A record whose context was not kept.
	calls, _ = Calls([]Context{built(1, "house-small")}, append(failed[:1:1], rec(4, "chat", "house-small", usage.OutcomeOK, 50)))
	if len(calls) != 2 || calls[1].Step != 2 || calls[1].Context != nil || len(calls[1].Usage) != 1 {
		t.Fatalf("a record with no context: %+v", calls)
	}

	// Nothing at all.
	calls, total = Calls(nil, nil)
	if len(calls) != 0 || total != (Totals{}) {
		t.Fatalf("no calls: %+v, %+v", calls, total)
	}
}

// LeaveOut marks the items and pins the skips name, and nothing else.
func TestLeaveOut(t *testing.T) {
	items := []contextbuild.Item{
		{Label: "garden: Runner beans (page 7)", Ref: "page:7", Text: "a"},
		{Label: "garden: Sweet peas (source 7)", Ref: "source:7", Text: "b"},
		{Label: "no name", Text: "c"},
	}
	pins := []contextbuild.Pin{
		{Message: 0, Item: contextbuild.Item{Label: "pinned message 7", Ref: "message:7", Text: "d"}},
		{Message: 2, Item: contextbuild.Item{Label: "pinned message 9", Ref: "message:9", Text: "e"}},
	}
	skips := []Skip{{Kind: KindPage, Ref: "7"}, {Kind: KindMessage, Ref: "7"}, {Kind: KindPage, Ref: "99"}}
	gotItems, gotPins := LeaveOut(items, pins, skips)
	if !gotItems[0].Removed || gotItems[1].Removed || gotItems[2].Removed {
		t.Errorf("the items are %+v", gotItems)
	}
	if !gotPins[0].Removed || gotPins[1].Removed {
		t.Errorf("the pins are %+v", gotPins)
	}
	if items[0].Removed || pins[0].Removed {
		t.Error("LeaveOut changed what it was given")
	}
	// A page's skip never removes a pinned message of the same id, and a
	// message's skip never removes a recalled item.
	gotItems, gotPins = LeaveOut([]contextbuild.Item{{Ref: "message:7", Text: "x"}}, []contextbuild.Pin{{Item: contextbuild.Item{Ref: "page:7", Text: "y"}}}, skips)
	if gotItems[0].Removed || gotPins[0].Removed {
		t.Errorf("a skip crossed kinds: %+v %+v", gotItems, gotPins)
	}
}
