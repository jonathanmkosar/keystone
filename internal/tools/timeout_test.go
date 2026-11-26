package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A tool's own timeout replaces the call's when it is longer, and never
// when it is shorter (P8-02); and the tool is told which call it answers.
func TestAToolsOwnLongerTimeoutAndTheCallsID(t *testing.T) {
	s := newStore(t)
	var seen string
	slow := func(wait time.Duration) func(context.Context, Input) (string, error) {
		return func(ctx context.Context, in Input) (string, error) {
			seen = in.CallID
			select {
			case <-time.After(wait):
				return "waited", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	reg := New()
	for _, tl := range []Tool{
		{Name: "patient", Description: "Waits longer than a call may.", InputSchema: json.RawMessage(`{"type":"object"}`), ReadOnly: true,
			Run: slow(150 * time.Millisecond), Timeout: time.Second},
		{Name: "hasty", Description: "Has a shorter limit of its own.", InputSchema: json.RawMessage(`{"type":"object"}`), ReadOnly: true,
			Run: slow(150 * time.Millisecond), Timeout: time.Millisecond},
	} {
		if err := reg.Add(tl); err != nil {
			t.Fatal(err)
		}
	}
	call := func(name string) Result {
		return reg.Run(context.Background(), Call{Store: s, RequestID: "req-timeout", Actor: "person", ID: "call-" + name, Name: name, Timeout: 50 * time.Millisecond})
	}
	if res := call("patient"); res.Status != StatusOK || seen != "call-patient" {
		t.Fatalf("a tool with a longer limit of its own came back %s, and was told the call %q", res.Status, seen)
	}
	res := call("hasty")
	if res.Status != StatusError || !strings.Contains(res.Block.Content[0].Text, "longer than 50ms") {
		t.Fatalf("a tool with a shorter limit of its own came back %s: %+v", res.Status, res.Block)
	}
}
