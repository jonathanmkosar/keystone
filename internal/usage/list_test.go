package usage

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/models"
)

// A month's records and a request's records are found by their index, and
// never by reading every record a person has (P13-03, the load test: the
// Operations report lists a request's records once for each trace).
func TestListFindsAMonthAndARequestByTheirIndex(t *testing.T) {
	one, _ := twoStores(t)
	ctx := context.Background()
	for _, r := range []Record{
		{At: at("2026-10-31 23:59"), RequestID: "req-1", Kind: "chat", Model: "hosted-large", Class: models.ClassHosted, Outcome: OutcomeOK, CostMicros: 1},
		{At: at("2026-11-01 00:00"), RequestID: "req-1", Kind: "title", Model: "house-small", Class: models.ClassLocal, Outcome: OutcomeOK, CostMicros: 2},
		{At: at("2026-11-02 09:00"), RequestID: "req-2", Kind: "chat", Model: "hosted-large", Class: models.ClassHosted, Outcome: OutcomeOK, CostMicros: 4},
		{At: at("2026-11-03 09:00"), Kind: "chat", Model: "hosted-large", Class: models.ClassHosted, Outcome: OutcomeOK, CostMicros: 8},
	} {
		if _, err := Add(ctx, one, r); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name  string
		f     Filter
		want  []int64 // the costs, oldest first
		index string  // the index the query must use, "" for none
	}{
		{"every record", Filter{}, []int64{1, 2, 4, 8}, ""},
		{"a month", Filter{Month: "2026-11"}, []int64{2, 4, 8}, "usage_records_month"},
		{"a request", Filter{RequestID: "req-1"}, []int64{1, 2}, "usage_records_request"},
		{"a request in a month", Filter{Month: "2026-11", RequestID: "req-1"}, []int64{2}, "usage_records_"},
		{"the newest two of a month", Filter{Month: "2026-11", Limit: 2}, []int64{4, 8}, "usage_records_month"},
		{"a request no record has", Filter{RequestID: "req-9"}, nil, "usage_records_request"},
	} {
		got, err := List(ctx, one, c.f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var costs []int64
		for _, r := range got {
			costs = append(costs, r.CostMicros)
		}
		if len(costs) != len(c.want) {
			t.Errorf("%s: got the records costing %v, want %v", c.name, costs, c.want)
			continue
		}
		for i := range costs {
			if costs[i] != c.want[i] {
				t.Errorf("%s: got the records costing %v, want %v", c.name, costs, c.want)
				break
			}
		}

		// How the database reads it.
		var args []any
		if c.f.Month != "" {
			args = append(args, c.f.Month)
		}
		if c.f.RequestID != "" {
			args = append(args, c.f.RequestID)
		}
		rows, err := one.DB().Query("EXPLAIN QUERY PLAN "+listColumns+listWhere(c.f)+" ORDER BY id DESC", args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		how := strings.Join(plan, "; ")
		if c.index != "" && (!strings.Contains(how, "USING INDEX "+c.index) || strings.Contains(how, "SCAN usage_records")) {
			t.Errorf("%s: the query reads every record, or misses its index %s: %s", c.name, c.index, how)
		}
	}
}
