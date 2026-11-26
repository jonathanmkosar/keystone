package tools

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/tools/approval"
)

// A call that reaches other people (P12-02) runs only on the person's own
// answer: not without an approval, not under a standing one, and not as a
// tool that says it only reads. A call of the same tool that reaches nobody
// runs under the standing policy.
func TestACallThatReachesOthersRunsOnlyOnAnAnswer(t *testing.T) {
	s := newStore(t)
	r := New()
	var ran atomic.Int32
	invite := echo("add_event", false, &ran)
	invite.ReachesOthers = func(input json.RawMessage) bool {
		var in struct {
			Guests []string `json:"guests"`
		}
		return json.Unmarshal(input, &in) != nil || len(in.Guests) > 0
	}
	sneaky := echo("send_note", true, &ran) // says it only reads, and reaches people
	sneaky.ReachesOthers = func(json.RawMessage) bool { return true }
	for _, tool := range []Tool{invite, sneaky} {
		if err := r.Add(tool); err != nil {
			t.Fatal(err)
		}
	}
	guests := json.RawMessage(`{"guests":["someone@example.com"]}`)
	alone := json.RawMessage(`{"guests":[]}`)
	run := func(name string, input json.RawMessage, a approval.Approval) Result {
		return r.Run(context.Background(), Call{Store: s, RequestID: "req-reach", Actor: "person", ID: "call_1", Name: name, Input: input, Approval: a})
	}

	standing := approval.Decide(approval.Call{Tool: "add_event", Input: guests, Policy: approval.Always}).Approval
	for what, res := range map[string]Result{
		"no approval":        run("add_event", guests, approval.Approval{}),
		"a standing one":     run("add_event", guests, standing),
		"a read-only one":    run("send_note", alone, approval.Approval{}),
		"read-only standing": run("send_note", alone, approval.Decide(approval.Call{Tool: "send_note", Input: alone, Policy: approval.Always}).Approval),
	} {
		if res.Status != StatusRefused || res.Ran {
			t.Errorf("%s came to %+v", what, res)
		}
		if what == "a standing one" && !strings.Contains(text(res.Block), "approves it themselves") {
			t.Errorf("the model is told %q", text(res.Block))
		}
	}
	if ran.Load() != 0 {
		t.Fatalf("a call that reaches others ran %d times without an answer", ran.Load())
	}
	if res := run("add_event", guests, approval.Answered(approval.Answer{Tool: "add_event", Input: guests, Approved: true})); res.Status != StatusOK {
		t.Errorf("the answered call came to %+v", res)
	}
	if res := run("add_event", alone, approval.Decide(approval.Call{Tool: "add_event", Input: alone, Policy: approval.Always}).Approval); res.Status != StatusOK {
		t.Errorf("a call that reaches nobody, under the standing policy, came to %+v", res)
	}
	if ran.Load() != 2 {
		t.Errorf("%d calls ran, want 2", ran.Load())
	}
	for _, info := range r.List() {
		if !info.ReachesOthers {
			t.Errorf("%s is listed as reaching nobody", info.Name)
		}
	}
	if (Tool{}).Reaches(guests) {
		t.Error("a tool with no ReachesOthers reaches people")
	}
}
