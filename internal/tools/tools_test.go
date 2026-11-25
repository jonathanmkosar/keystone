package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools/approval"
)

var anyObject = json.RawMessage(`{"type":"object","properties":{}}`)

func newStore(t *testing.T) *tenant.Store {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Test Person One")
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

func echo(name string, readOnly bool, ran *atomic.Int32) Tool {
	return Tool{
		Name: name, Description: "Gives back what it was sent.", InputSchema: anyObject, ReadOnly: readOnly,
		Run: func(_ context.Context, in Input) (string, error) {
			if ran != nil {
				ran.Add(1)
			}
			return "sent " + string(in.Args), nil
		},
	}
}

func text(b seam.Block) string {
	return seam.Text(seam.Message{Blocks: b.Content})
}

func entries(t *testing.T, s *tenant.Store, requestID string) []audit.Entry {
	t.Helper()
	got, err := audit.List(context.Background(), s, audit.Filter{Kind: audit.KindTool, RequestID: requestID})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAddRefusesWhatIsNotATool(t *testing.T) {
	r := New()
	if err := r.Add(echo("echo", true, nil)); err != nil {
		t.Fatal(err)
	}
	bad := map[string]Tool{
		"a name taken":            echo("echo", true, nil),
		"a name with spaces":      echo("two words", true, nil),
		"a capital":               echo("Echo", true, nil),
		"no name":                 echo("", true, nil),
		"no description":          {Name: "quiet", InputSchema: anyObject, Run: echo("x", true, nil).Run},
		"no schema":               {Name: "loose", Description: "No schema.", Run: echo("x", true, nil).Run},
		"a schema that is a list": {Name: "listed", Description: "A list.", InputSchema: json.RawMessage(`[]`), Run: echo("x", true, nil).Run},
		"no function":             {Name: "idle", Description: "Does nothing.", InputSchema: anyObject},
	}
	for what, tool := range bad {
		if err := r.Add(tool); err == nil {
			t.Errorf("%s was registered", what)
		}
	}
	if err := (*Registry)(nil).Add(echo("echo", true, nil)); err == nil {
		t.Error("a tool was added to no registry")
	}
	if got := r.List(); len(got) != 1 || got[0].Name != "echo" || !got[0].ReadOnly {
		t.Errorf("the registry lists %+v", got)
	}
}

func TestDefinitionsAreInOrderOfName(t *testing.T) {
	r := New()
	for _, name := range []string{"zebra", "apple", "mango"} {
		if err := r.Add(echo(name, true, nil)); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for _, d := range r.Definitions() {
		names = append(names, d.Name)
		if d.Description == "" || string(d.InputSchema) != string(anyObject) {
			t.Errorf("%s is offered as %+v", d.Name, d)
		}
	}
	if strings.Join(names, " ") != "apple mango zebra" {
		t.Errorf("the tools are offered as %v", names)
	}
	if (*Registry)(nil).Definitions() != nil || (*Registry)(nil).List() != nil {
		t.Error("no registry offers tools")
	}
}

func TestAToolRunsAndIsRecorded(t *testing.T) {
	s := newStore(t)
	r := New()
	var ran atomic.Int32
	if err := r.Add(echo("echo", true, &ran)); err != nil {
		t.Fatal(err)
	}
	res := r.Run(context.Background(), Call{Store: s, RequestID: "req-1", Actor: "person", ID: "call_1", Name: "echo", Input: json.RawMessage(`{"word":"harbour"}`)})
	if res.Status != StatusOK || !res.Ran || ran.Load() != 1 {
		t.Fatalf("the call came to %+v after %d runs", res, ran.Load())
	}
	if b := res.Block; b.Type != seam.BlockToolResult || b.ID != "call_1" || b.IsError || text(b) != `sent {"word":"harbour"}` {
		t.Errorf("the result is %+v", b)
	}
	if err := seam.ValidateBlock(res.Block); err != nil {
		t.Errorf("the result is not a block a provider takes: %v", err)
	}
	got := entries(t, s, "req-1")
	if len(got) != 2 {
		t.Fatalf("the call left %d audit entries, want one before and one after", len(got))
	}
	if e := got[0]; e.Outcome != audit.OutcomeStarted || e.Action != "echo" || e.Sent != `{"word":"harbour"}` || e.Actor != "person" {
		t.Errorf("the entry before the call is %+v", e)
	}
	if e := got[1]; e.Outcome != audit.OutcomeOK || e.Action != "echo" || !strings.Contains(e.Received, "harbour") || !strings.Contains(e.Received, `"status":"ok"`) {
		t.Errorf("the entry after the call is %+v", e)
	}
}

// The second fixed rule, where a tool is run: one that changes something
// is refused, its function is never called, and the refusal is recorded.
func TestAToolThatChangesSomethingIsRefused(t *testing.T) {
	s := newStore(t)
	r := New()
	var ran atomic.Int32
	if err := r.Add(echo("send_letter", false, &ran)); err != nil {
		t.Fatal(err)
	}
	res := r.Run(context.Background(), Call{Store: s, RequestID: "req-2", Actor: "person", ID: "call_1", Name: "send_letter", Input: json.RawMessage(`{}`)})
	if res.Status != StatusRefused || res.Ran || ran.Load() != 0 {
		t.Fatalf("a tool that changes something came to %+v after %d runs", res, ran.Load())
	}
	if !res.Block.IsError || !strings.Contains(text(res.Block), "runs only with a person's approval") {
		t.Errorf("the model is told %q", text(res.Block))
	}
	got := entries(t, s, "req-2")
	if len(got) != 2 || got[0].Outcome != audit.OutcomeStarted || got[1].Outcome != audit.OutcomeRefused {
		t.Errorf("the refusal is recorded as %+v", got)
	}
}

// An approval runs the one call it covers (P7-02): the same tool with
// other input, or another tool, is refused and never runs.
func TestAnApprovalRunsOnlyItsOwnCall(t *testing.T) {
	s := newStore(t)
	r := New()
	var ran atomic.Int32
	for _, name := range []string{"send_letter", "send_parcel"} {
		if err := r.Add(echo(name, false, &ran)); err != nil {
			t.Fatal(err)
		}
	}
	input := json.RawMessage(`{"to":"The harbour master"}`)
	ok := approval.Answered(approval.Answer{Tool: "send_letter", Input: input, Approved: true})
	res := r.Run(context.Background(), Call{Store: s, RequestID: "req-3", Actor: "person", ID: "call_1", Name: "send_letter", Input: json.RawMessage(`{ "to": "The harbour master" }`), Approval: ok})
	if res.Status != StatusOK || ran.Load() != 1 {
		t.Fatalf("the approved call came to %+v after %d runs", res, ran.Load())
	}
	for name, c := range map[string]Call{
		"other input":  {Name: "send_letter", Input: json.RawMessage(`{"to":"Someone else"}`)},
		"another tool": {Name: "send_parcel", Input: input},
	} {
		c.Store, c.RequestID, c.Actor, c.ID, c.Approval = s, "req-4", "person", "call_2", ok
		if res := r.Run(context.Background(), c); res.Status != StatusRefused || !strings.Contains(text(res.Block), "another call") {
			t.Errorf("%s came to %+v", name, res)
		}
	}
	if ran.Load() != 1 {
		t.Errorf("an approval ran %d calls", ran.Load())
	}
	// The standing policy covers its call the same way.
	standing := approval.Decide(approval.Call{Tool: "send_parcel", Input: input, Policy: approval.Always}).Approval
	if res := r.Run(context.Background(), Call{Store: s, RequestID: "req-5", Actor: "person", ID: "call_3", Name: "send_parcel", Input: input, Approval: standing}); res.Status != StatusOK {
		t.Errorf("a call under the standing policy came to %+v", res)
	}
}

func TestASummarySaysWhatACallWillDo(t *testing.T) {
	plain := Tool{Name: "send_letter"}
	if got := Summary(plain, json.RawMessage(`{"to":"The harbour master","urgent":true,"copies":2,"extra":{"a":1}}`)); got != "Use send_letter with copies 2, to “The harbour master”, urgent true." {
		t.Errorf("the summary is %q", got)
	}
	if got := Summary(plain, json.RawMessage(`{}`)); got != "Use send_letter." {
		t.Errorf("with no input the summary is %q", got)
	}
	own := Tool{Name: "send_letter", Describe: func(json.RawMessage) string { return "Send a letter to the harbour master." }}
	if got := Summary(own, nil); got != "Send a letter to the harbour master." {
		t.Errorf("a tool's own words are %q", got)
	}
}

func TestWhatGoesWrongBecomesAnErrorResult(t *testing.T) {
	s := newStore(t)
	r := New()
	add := func(name string, run func(context.Context, Input) (string, error)) {
		t.Helper()
		if err := r.Add(Tool{Name: name, Description: "A tool for a test.", InputSchema: anyObject, ReadOnly: true, Run: run}); err != nil {
			t.Fatal(err)
		}
	}
	add("fails", func(context.Context, Input) (string, error) { return "", errors.New("the tide table is missing") })
	add("panics", func(context.Context, Input) (string, error) { panic("a slip on the quay") })
	add("slow", func(ctx context.Context, _ Input) (string, error) { <-ctx.Done(); return "", ctx.Err() })
	add("deaf", func(context.Context, Input) (string, error) { time.Sleep(time.Second); return "late", nil })
	add("silent", func(context.Context, Input) (string, error) { return "  ", nil })
	add("long", func(context.Context, Input) (string, error) { return strings.Repeat("é", 500), nil })

	cases := []struct {
		name, input string
		timeout     time.Duration
		status      Status
		ran         bool
		says        string
		outcome     audit.Outcome
	}{
		{"fails", `{}`, 0, StatusError, true, "The tide table is missing", audit.OutcomeError},
		{"panics", `{}`, 0, StatusError, true, "broke while it ran", audit.OutcomeError},
		{"slow", `{}`, 30 * time.Millisecond, StatusError, true, "longer than 30ms", audit.OutcomeError},
		{"deaf", `{}`, 30 * time.Millisecond, StatusError, true, "longer than 30ms", audit.OutcomeError},
		{"missing", `{}`, 0, StatusError, false, `no tool named "missing"`, audit.OutcomeError},
		{"fails", `["a list"]`, 0, StatusError, false, "not a JSON object", audit.OutcomeError},
		{"silent", `{}`, 0, StatusOK, true, "returned nothing", audit.OutcomeOK},
		{"long", `{}`, 0, StatusOK, true, "The result was cut here. It had 500 characters, and 100 are kept.", audit.OutcomeOK},
	}
	for i, c := range cases {
		id := "req-" + c.name + string(rune('a'+i))
		res := r.Run(context.Background(), Call{Store: s, RequestID: id, Actor: "person", ID: "call_1", Name: c.name, Input: json.RawMessage(c.input), Timeout: c.timeout, MaxResultChars: 100})
		if res.Status != c.status || res.Ran != c.ran || res.Block.IsError != (c.status != StatusOK) || !strings.Contains(text(res.Block), c.says) {
			t.Errorf("%s with %s came to %s, ran %v: %q", c.name, c.input, res.Status, res.Ran, text(res.Block))
		}
		if err := seam.ValidateBlock(res.Block); err != nil {
			t.Errorf("%s: the result is not a block a provider takes: %v", c.name, err)
		}
		if got := entries(t, s, id); len(got) != 2 || got[1].Outcome != c.outcome {
			t.Errorf("%s is recorded as %+v", c.name, got)
		}
		if c.name == "panics" && (!strings.Contains(res.Log, "a slip on the quay") || strings.Contains(text(res.Block), "quay")) {
			t.Errorf("a panic's words belong in the log and not in the result: log %q, result %q", res.Log, text(res.Block))
		}
	}
	if n := len([]rune(text(r.Run(context.Background(), Call{Store: s, RequestID: "req-whole", Actor: "person", ID: "c", Name: "long"}).Block))); n != 500 {
		t.Errorf("with no limit a result of 500 characters came back as %d", n)
	}
}

// Stop: the turn's context ends while the tool runs. Run returns at once,
// even for a tool that does not watch its context.
func TestStoppingTheTurnStopsTheTool(t *testing.T) {
	s := newStore(t)
	for _, watches := range []bool{true, false} {
		r := New()
		started, release := make(chan struct{}), make(chan struct{})
		sawCancel := make(chan struct{})
		err := r.Add(Tool{Name: "wait", Description: "Waits.", InputSchema: anyObject, ReadOnly: true, Run: func(ctx context.Context, _ Input) (string, error) {
			close(started)
			if watches {
				<-ctx.Done()
				close(sawCancel)
				return "", ctx.Err()
			}
			<-release
			return "done", nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-started
			cancel()
		}()
		id := "req-stop"
		if watches {
			id = "req-stop-watched"
		}
		done := make(chan Result, 1)
		go func() {
			done <- r.Run(ctx, Call{Store: s, RequestID: id, Actor: "person", ID: "call_1", Name: "wait", Timeout: time.Minute})
		}()
		select {
		case res := <-done:
			if res.Status != StatusStopped || !res.Block.IsError || !strings.Contains(text(res.Block), "stopped") {
				t.Errorf("a stopped tool came to %+v", res)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return when its context ended")
		}
		if watches {
			select {
			case <-sawCancel:
			case <-time.After(5 * time.Second):
				t.Error("the tool's own context was not cancelled")
			}
		}
		close(release)
		if got := entries(t, s, id); len(got) != 2 || got[1].Outcome != audit.OutcomeOK || !strings.Contains(got[1].Received, `"status":"stopped"`) {
			t.Errorf("a stopped tool is recorded as %+v", got)
		}
	}
	// A call that arrives after the stop does not run at all.
	r := New()
	var ran atomic.Int32
	if err := r.Add(echo("echo", true, &ran)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res := r.Run(ctx, Call{Store: s, RequestID: "req-late", Actor: "person", ID: "c", Name: "echo"}); res.Status != StatusStopped || res.Ran || ran.Load() != 0 {
		t.Errorf("a call after the stop came to %+v", res)
	}
}

// A call that cannot be recorded is not run.
func TestACallThatCannotBeRecordedIsNotRun(t *testing.T) {
	s := newStore(t)
	r := New()
	var ran atomic.Int32
	if err := r.Add(echo("echo", true, &ran)); err != nil {
		t.Fatal(err)
	}
	// No request id: the audit log refuses the entry.
	res := r.Run(context.Background(), Call{Store: s, Actor: "person", ID: "call_1", Name: "echo"})
	if res.Status != StatusError || res.Ran || ran.Load() != 0 || !strings.Contains(text(res.Block), "could not be recorded") {
		t.Errorf("an unrecorded call came to %+v after %d runs", res, ran.Load())
	}
}

func TestResultsForCallsThatWereNotMade(t *testing.T) {
	s := newStore(t)
	var ran atomic.Int32
	r := New()
	if err := r.Add(echo("echo", true, &ran)); err != nil {
		t.Fatal(err)
	}
	// A nil registry declines too: the call is still recorded.
	for i, reg := range []*Registry{r, nil} {
		id := "req-declined-" + string(rune('a'+i))
		res := reg.Decline(Call{Store: s, RequestID: id, Actor: "person", ID: "call_1", Name: "echo", Input: json.RawMessage(`{"a":1}`)}, "Tools are switched off.")
		if err := seam.ValidateBlock(res.Block); err != nil || !res.Block.IsError || res.Block.ID != "call_1" || res.Ran || res.Status != StatusRefused || text(res.Block) != "Tools are switched off." {
			t.Errorf("%+v: %v", res, err)
		}
		if got := entries(t, s, id); len(got) != 2 || got[0].Sent != `{"a":1}` || got[1].Outcome != audit.OutcomeRefused {
			t.Errorf("a declined call is recorded as %+v", got)
		}
	}
	if ran.Load() != 0 {
		t.Error("a declined call ran")
	}
}
