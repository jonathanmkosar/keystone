// Package inspector puts together what the turn inspector shows of one
// turn, and keeps what a person takes out of a chat's next turn.
//
// A turn's model calls leave two kinds of record. The context builder's
// record of what each chat call was built from is kept by the turn, one
// for each step. The usage record of each model call carries the request
// id of the API call that sent the turn, its kind and its numbers, and no
// step. Calls joins the two and sums the usage records.
//
// A skip is one recalled page or source, or one pinned message, that the
// person took out of the chat's next turn from the inspector. The turn
// claims the chat's waiting skips when its first call is built, and every
// call of that turn leaves them out; no later turn does. LeaveOut marks
// them on what the context builder is given.
//
// The package reads no setting and opens no connection.
// docs/architecture/inspector.md describes it.
package inspector

import (
	"cmp"
	"slices"

	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Context is what one chat call of a turn was built from: the turn's step
// and the context builder's record.
type Context struct {
	Step   int
	Record contextbuild.Record
}

// Totals is the sum of some usage records.
type Totals struct {
	// Calls counts the records: a model call each, and a model of a route
	// that did not answer before the next one did counts as one.
	Calls            int   `json:"calls"`
	InputTokens      int   `json:"input_tokens"`
	OutputTokens     int   `json:"output_tokens"`
	CacheReadTokens  int   `json:"cache_read_tokens"`
	CacheWriteTokens int   `json:"cache_write_tokens"`
	ThinkingTokens   int   `json:"thinking_tokens"`
	CostMicros       int64 `json:"cost_micros"`
	// Estimated says a count in it was estimated, not given by the
	// provider.
	Estimated bool `json:"estimated"`
}

// Add counts one usage record in.
func (t *Totals) Add(r usage.Record) {
	t.Calls++
	t.InputTokens += r.InputTokens
	t.OutputTokens += r.OutputTokens
	t.CacheReadTokens += r.CacheReadTokens
	t.CacheWriteTokens += r.CacheWriteTokens
	t.ThinkingTokens += r.ThinkingTokens
	t.CostMicros += r.CostMicros
	t.Estimated = t.Estimated || r.Estimated
}

// Sum adds up records.
func Sum(records []usage.Record) Totals {
	var t Totals
	for _, r := range records {
		t.Add(r)
	}
	return t
}

// Call is one model call of a turn, as the inspector shows it.
type Call struct {
	// Step counts the turn's chat calls from 1. It is zero on a call of
	// another kind.
	Step int `json:"step,omitempty"`
	// Kind is the kind of task: chat, title, summary, or another kind
	// routing knows.
	Kind string `json:"kind"`
	// Model is the model the call ended on: the one that answered, or the
	// last that failed. For a call with no usage record, the model it was
	// built for.
	Model string `json:"model,omitempty"`
	// Context is what a chat call was built from. Absent on a call the
	// context builder did not build, and on one whose record was not kept.
	Context *contextbuild.Record `json:"context,omitempty"`
	// Previews are the first words of the message that starts each turn of
	// Context.Turns, in the same order. The caller fills them.
	Previews []string `json:"previews,omitempty"`
	// Usage is the call's usage records, oldest first: one, and one more
	// before it for each model of the route that did not answer. Empty for
	// a call that never ended, which the server stopping can leave.
	Usage []usage.Record `json:"usage"`
	// Total sums Usage.
	Total Totals `json:"total"`
}

// Calls puts a turn's model calls together, from what each chat call was
// built from and the usage records of the turn, and sums every record.
//
// A usage record names no step, so a call's records are found by their
// order, kind by kind: a call's run of records ends at the one whose
// outcome is ok or stopped, or at the kind's last record. The records
// before it in the run are the models of the route that did not answer,
// each tried before the next. This holds for chat calls because a turn
// whose chat call did not end ok or stopped ends there, so only its last
// call can end any other way. The chat calls' runs are matched to the
// steps in order.
//
// The chat calls come first, by step, then every other call in the order
// it was made. records are a turn's usage records, oldest first, as
// usage.List returns them.
func Calls(contexts []Context, records []usage.Record) ([]Call, Totals) {
	records = slices.Clone(records)
	slices.SortStableFunc(records, func(a, b usage.Record) int { return cmp.Compare(a.ID, b.ID) })
	contexts = slices.Clone(contexts)
	slices.SortStableFunc(contexts, func(a, b Context) int { return cmp.Compare(a.Step, b.Step) })

	runs := map[string][][]usage.Record{}
	open := map[string][]usage.Record{}
	var kinds []string // in the order each kind was first seen
	for _, r := range records {
		if _, seen := runs[r.Kind]; !seen {
			kinds = append(kinds, r.Kind)
			runs[r.Kind] = nil
		}
		open[r.Kind] = append(open[r.Kind], r)
		if r.Outcome == usage.OutcomeOK || r.Outcome == usage.OutcomeStopped {
			runs[r.Kind] = append(runs[r.Kind], open[r.Kind])
			open[r.Kind] = nil
		}
	}
	for _, kind := range kinds {
		if rest := open[kind]; len(rest) > 0 {
			runs[kind] = append(runs[kind], rest)
		}
	}

	chat := string(routing.KindChat)
	var calls []Call
	step := 0
	for i := 0; i < max(len(contexts), len(runs[chat])); i++ {
		c := Call{Kind: chat, Usage: []usage.Record{}}
		if i < len(contexts) {
			rec := contexts[i].Record
			c.Step, c.Context, c.Model = contexts[i].Step, &rec, rec.Model
		} else {
			c.Step = step + 1 // a call whose record was not kept
		}
		step = c.Step
		if i < len(runs[chat]) {
			c.Usage = runs[chat][i]
			c.Model = c.Usage[len(c.Usage)-1].Model
		}
		c.Total = Sum(c.Usage)
		calls = append(calls, c)
	}

	var others []Call
	for _, kind := range kinds {
		if kind == chat {
			continue
		}
		for _, run := range runs[kind] {
			others = append(others, Call{Kind: kind, Model: run[len(run)-1].Model, Usage: run, Total: Sum(run)})
		}
	}
	slices.SortStableFunc(others, func(a, b Call) int { return cmp.Compare(a.Usage[0].ID, b.Usage[0].ID) })
	return append(calls, others...), Sum(records)
}
