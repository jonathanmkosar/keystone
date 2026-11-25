package fake

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Every value here is invented test data.

func text(s string) seam.Block { return seam.Block{Type: seam.BlockText, Text: s} }

func call(id, name, input string) seam.Block {
	return seam.Block{Type: seam.BlockToolCall, ID: id, Name: name, Input: json.RawMessage(input)}
}

// ask is a request that holds one message from the person.
func ask(said string) seam.Request {
	return seam.Request{Messages: []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{text(said)}}}}
}

func reply(stop seam.StopReason, usage seam.Usage, blocks ...seam.Block) *seam.Response {
	return &seam.Response{Message: seam.Message{Role: seam.RoleAssistant, Blocks: blocks}, StopReason: stop, Usage: usage}
}

// answer adds a reply and a result for each of its tool calls to a
// request, as the tool loop does before it calls the model again.
func answer(req seam.Request, r seam.Response) seam.Request {
	results := seam.Message{Role: seam.RoleUser}
	for _, c := range seam.ToolCalls(r.Message) {
		results.Blocks = append(results.Blocks, seam.Block{Type: seam.BlockToolResult, ID: c.ID, Content: []seam.Block{text("an invented result")}})
	}
	req.Messages = append(req.Messages[:len(req.Messages):len(req.Messages)], r.Message, results)
	return req
}

func provider(t *testing.T, opts Options, scripts ...Script) *Provider {
	t.Helper()
	p, err := New(scripts, opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// play runs one call and returns every event it sent. Each event goes
// through JSON on the way, as it will on the way to a browser.
func play(t *testing.T, p *Provider, req seam.Request) ([]seam.Event, error) {
	t.Helper()
	var events []seam.Event
	err := p.Stream(context.Background(), req, func(e seam.Event) error {
		raw, err := seam.EncodeEvent(e)
		if err != nil {
			t.Fatalf("the fake sent an event the seam refuses: %v", err)
		}
		back, err := seam.DecodeEvent(raw)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, back)
		return nil
	})
	return events, err
}

func count(events []seam.Event, is func(seam.Event) bool) int {
	n := 0
	for _, e := range events {
		if is(e) {
			n++
		}
	}
	return n
}

var ferryAnswer = "The north ferry leaves at 09:00 and at 11:30 on Tuesday."

func TestTextStreamsInSeveralPieces(t *testing.T) {
	want := reply(seam.StopEnd, seam.Usage{InputTokens: 40, OutputTokens: 19}, text(ferryAnswer))
	p := provider(t, Options{DeltaSize: 10}, Script{Name: "ferry", When: When{UserText: "When does the ferry leave?"}, Steps: []Step{{Reply: want}}})

	req := ask("When does the ferry leave?")
	req.Model = "test-model"
	events, err := play(t, p, req)
	if err != nil {
		t.Fatal(err)
	}
	pieces := count(events, func(e seam.Event) bool { return e.Type == seam.EventBlockDelta })
	if wantPieces := (utf8.RuneCountInString(ferryAnswer) + 9) / 10; pieces != wantPieces {
		t.Errorf("the text came in %d pieces, want %d", pieces, wantPieces)
	}
	for _, e := range events {
		if e.Type == seam.EventBlockDelta && utf8.RuneCountInString(e.Text) > 10 {
			t.Errorf("a piece holds %q, more than 10 characters", e.Text)
		}
	}
	if events[0].Type != seam.EventStart || events[0].Model != "test-model" {
		t.Errorf("the stream opens with %+v, want a start that names the request's model", events[0])
	}

	// The stream folds back to the scripted reply, with the model filled in.
	got, err := seam.Collect(events)
	if err != nil {
		t.Fatal(err)
	}
	whole := *want
	whole.Model = "test-model"
	if !reflect.DeepEqual(got, whole) {
		t.Errorf("collected %+v, want %+v", got, whole)
	}
	// With no delta size, each block is one piece.
	events, _ = play(t, provider(t, Options{}, Script{Name: "ferry", Steps: []Step{{Reply: want}}}), req)
	if n := count(events, func(e seam.Event) bool { return e.Type == seam.EventBlockDelta }); n != 1 {
		t.Errorf("with no delta size the text came in %d pieces, want 1", n)
	}
}

func TestThinkingStreamsBeforeTheAnswer(t *testing.T) {
	want := reply(seam.StopEnd, seam.Usage{InputTokens: 30, OutputTokens: 50, ThinkingTokens: 20},
		seam.Block{Type: seam.BlockThinking, Text: "They asked about Tuesday, so the weekday timetable applies.", Signature: "sig-test-0001"},
		seam.Block{Type: seam.BlockThinking, Signature: "sig-test-0002"}, // withheld thinking
		text(ferryAnswer))
	p := provider(t, Options{DeltaSize: 8}, Script{Name: "thinks", Steps: []Step{{Reply: want}}})

	events, err := play(t, p, ask("When does the ferry leave?"))
	if err != nil {
		t.Fatal(err)
	}
	if events[1].Type != seam.EventBlockStart || events[1].Block.Type != seam.BlockThinking {
		t.Errorf("the first block is %+v, want thinking", events[1])
	}
	thinking := count(events, func(e seam.Event) bool { return e.Type == seam.EventBlockDelta && e.Index == 0 && e.Text != "" })
	if thinking < 2 {
		t.Errorf("the thinking came in %d pieces, want several", thinking)
	}
	// A signature is opaque, so it is never split.
	for _, e := range events {
		if e.Signature != "" && e.Signature != "sig-test-0001" && e.Signature != "sig-test-0002" {
			t.Errorf("a signature arrived as %q", e.Signature)
		}
	}
	got, err := seam.Collect(events)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, *want) {
		t.Errorf("collected %+v, want %+v", got, *want)
	}
	if got.Usage.ThinkingTokens != 20 {
		t.Errorf("thinking tokens are %d, want 20", got.Usage.ThinkingTokens)
	}
}

func toolScript() Script {
	return Script{
		Name: "ferry times",
		When: When{UserText: "When does the north ferry leave on Tuesday?"},
		Steps: []Step{
			{Reply: reply(seam.StopToolCall, seam.Usage{InputTokens: 240, OutputTokens: 38},
				text("Let me look at the timetable."),
				call("call_1", "lookup_timetable", `{"route":"north","day":"Tuesday"}`),
				call("call_2", "current_time", `{}`))},
			{Reply: reply(seam.StopEnd, seam.Usage{InputTokens: 310, OutputTokens: 19}, text(ferryAnswer))},
		},
	}
}

func TestAToolCallContinuesWhenItsResultComesBack(t *testing.T) {
	s := toolScript()
	p := provider(t, Options{DeltaSize: 7}, s)
	req := ask("When does the north ferry leave on Tuesday?")

	events, err := play(t, p, req)
	if err != nil {
		t.Fatal(err)
	}
	inputs := count(events, func(e seam.Event) bool { return e.Input != "" && e.Index == 1 })
	if inputs < 2 {
		t.Errorf("the tool input came in %d pieces, want several", inputs)
	}
	first, err := seam.Collect(events)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, *s.Steps[0].Reply) {
		t.Errorf("step 1 collected %+v, want %+v", first, *s.Steps[0].Reply)
	}
	if calls := seam.ToolCalls(first.Message); first.StopReason != seam.StopToolCall || len(calls) != 2 || calls[0].Name != "lookup_timetable" {
		t.Fatalf("step 1 is %+v, want a stop for two tool calls", first)
	}

	// The same request again gives the same step: nothing is remembered.
	again, err := seam.Call(context.Background(), p, req)
	if err != nil || !reflect.DeepEqual(again, first) {
		t.Errorf("asking again gave %+v (%v)", again, err)
	}

	// With the results in the request, the script moves on.
	second, err := seam.Call(context.Background(), p, answer(req, first))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, *s.Steps[1].Reply) {
		t.Errorf("step 2 collected %+v, want %+v", second, *s.Steps[1].Reply)
	}
	if got := first.Usage.Add(second.Usage); got.InputTokens != 550 || got.OutputTokens != 57 {
		t.Errorf("the two steps used %+v", got)
	}
}

func TestAnErrorEndsTheCallBeforeAnythingIsSent(t *testing.T) {
	failure := &seam.Error{Kind: seam.ErrOverloaded, Message: "the invented provider is busy", Retryable: true}
	p := provider(t, Options{},
		Script{Name: "busy", When: When{Model: "fake-busy"}, Steps: []Step{{Error: failure}}},
		Script{Name: "steady", Steps: []Step{{Reply: reply(seam.StopEnd, seam.Usage{}, text(ferryAnswer))}}},
	)
	req := ask("When does the ferry leave?")
	req.Model = "fake-busy"
	events, err := play(t, p, req)
	var got *seam.Error
	if !errors.As(err, &got) || *got != *failure {
		t.Fatalf("Stream returned %v, want the scripted error", err)
	}
	if len(events) != 1 || events[0].Type != seam.EventError || *events[0].Error != *failure {
		t.Errorf("the stream is %+v, want the error event alone", events)
	}
	// Call gives the same error.
	if _, err := seam.Call(context.Background(), p, req); !errors.As(err, &got) || got.Kind != seam.ErrOverloaded || !got.Retryable {
		t.Errorf("Call returned %v", err)
	}
	// A fallback to another model reaches a script that answers.
	req.Model = "fake-steady"
	if r, err := seam.Call(context.Background(), p, req); err != nil || seam.Text(r.Message) != ferryAnswer {
		t.Errorf("the other model gave %+v (%v)", r, err)
	}
}

func TestACutReplyEndsEarly(t *testing.T) {
	whole := reply(seam.StopToolCall, seam.Usage{InputTokens: 40, OutputTokens: 30},
		text("Let me look at the timetable."), // 29 characters
		call("call_1", "lookup_timetable", `{"route":"north"}`))
	cutAt := func(n int, failure *seam.Error) Script {
		return Script{Name: "cut", Steps: []Step{{Reply: whole, CutAfter: &n, Error: failure}}}
	}
	slow := &seam.Error{Kind: seam.ErrRateLimited, Message: "the invented provider asked for fewer calls", Retryable: true}

	for name, c := range map[string]struct {
		script   Script
		size     int
		wantText string
		wantKind seam.ErrorKind
	}{
		"inside the text":          {cutAt(10, nil), 0, "Let me loo", seam.ErrUnavailable},
		"inside a small piece":     {cutAt(10, nil), 4, "Let me loo", seam.ErrUnavailable},
		"at the end of a piece":    {cutAt(8, nil), 4, "Let me l", seam.ErrUnavailable},
		"before any text":          {cutAt(0, nil), 4, "", seam.ErrUnavailable},
		"inside the tool input":    {cutAt(35, nil), 4, "Let me look at the timetable.", seam.ErrUnavailable},
		"with an error of its own": {cutAt(10, slow), 4, "Let me loo", seam.ErrRateLimited},
	} {
		p := provider(t, Options{DeltaSize: c.size}, c.script)
		var col seam.Collector
		var events []seam.Event
		err := p.Stream(context.Background(), ask("When does the ferry leave?"), func(e seam.Event) error {
			events = append(events, e)
			return col.Add(e)
		})
		var got *seam.Error
		if !errors.As(err, &got) || got.Kind != c.wantKind || !got.Retryable {
			t.Errorf("%s: Stream returned %v, want a retryable %s error", name, err, c.wantKind)
			continue
		}
		if last := events[len(events)-1]; last.Type != seam.EventError {
			t.Errorf("%s: the last event is %s, want error", name, last.Type)
		}
		if n := count(events, func(e seam.Event) bool { return e.Type == seam.EventStop }); n != 0 {
			t.Errorf("%s: a cut stream sent a stop event", name)
		}
		if events[0].Type != seam.EventStart {
			t.Errorf("%s: the stream opens with %s, want start", name, events[0].Type)
		}
		// What arrived before the cut is kept, and the half-sent tool
		// call is not.
		partial := col.Partial()
		if seam.Text(partial) != c.wantText || len(seam.ToolCalls(partial)) != 0 {
			t.Errorf("%s: the partial reply is %+v, want the text %q and no tool call", name, partial, c.wantText)
		}
		if _, err := col.Response(); err == nil {
			t.Errorf("%s: a cut stream gave a response", name)
		}
	}
}

func TestInvalidScriptsAreRefused(t *testing.T) {
	good := reply(seam.StopEnd, seam.Usage{}, text(ferryAnswer)) // 56 characters
	asks := reply(seam.StopToolCall, seam.Usage{}, call("call_1", "current_time", `{}`))
	failure := &seam.Error{Kind: seam.ErrOverloaded, Message: "busy"}
	n := func(v int) *int { return &v }
	one := func(steps ...Step) []Script { return []Script{{Name: "s", Steps: steps}} }

	bad := map[string]struct {
		scripts []Script
		reason  string
	}{
		"no name":                   {[]Script{{Steps: []Step{{Reply: good}}}}, "has no name"},
		"no steps":                  {[]Script{{Name: "s"}}, "has no steps"},
		"an empty step":             {one(Step{}), "needs a reply or an error"},
		"empty text":                {one(Step{Reply: reply(seam.StopEnd, seam.Usage{}, seam.Block{Type: seam.BlockText})}), "a text block has no text"},
		"a reply from the user":     {one(Step{Reply: &seam.Response{Message: seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{text("x")}}, StopReason: seam.StopEnd}}), "assistant"},
		"an image in the reply":     {one(Step{Reply: reply(seam.StopEnd, seam.Usage{}, seam.Block{Type: seam.BlockImage, MediaType: "image/png", Ref: "9f2c41d0aa17"})}), "not a image block"},
		"no stop reason":            {one(Step{Reply: reply("", seam.Usage{}, text("x"))}), "stop reason"},
		"a tool stop with no call":  {one(Step{Reply: reply(seam.StopToolCall, seam.Usage{}, text("x"))}), "holds none"},
		"tool input not an object":  {one(Step{Reply: reply(seam.StopToolCall, seam.Usage{}, call("call_1", "t", `[1]`))}), "not a JSON object"},
		"more thinking than output": {one(Step{Reply: reply(seam.StopEnd, seam.Usage{OutputTokens: 5, ThinkingTokens: 6}, text("x"))}), "thinking tokens"},
		"an unknown error kind":     {one(Step{Error: &seam.Error{Kind: "weather", Message: "x"}}), "is not known"},
		"an error with no message":  {one(Step{Error: &seam.Error{Kind: seam.ErrOther}}), "needs a message"},
		"a reply and an error":      {one(Step{Reply: good, Error: failure}), "add cut_after"},
		"a cut with no reply":       {one(Step{Error: failure, CutAfter: n(3)}), "needs a reply to cut"},
		"a cut at the very end":     {one(Step{Reply: good, CutAfter: n(56)}), "less than"},
		"a negative cut":            {one(Step{Reply: good, CutAfter: n(-1)}), "at least 0"},
		"a step after the end":      {one(Step{Reply: good}, Step{Reply: good}), "step 2 can never be reached"},
		"a step after a failure":    {one(Step{Error: failure}, Step{Reply: good}), "step 2 can never be reached"},
		"a step after a cut":        {one(Step{Reply: asks, CutAfter: n(1)}, Step{Reply: good}), "step 2 can never be reached"},
		"one name twice":            {[]Script{{Name: "s", Steps: []Step{{Reply: good}}}, {Name: "s", When: When{Model: "m"}, Steps: []Step{{Reply: good}}}}, "two scripts are named"},
		"the same conditions twice": {[]Script{{Name: "a", When: When{UserText: "Hello"}, Steps: []Step{{Reply: good}}}, {Name: "b", When: When{UserText: " hello "}, Steps: []Step{{Reply: good}}}}, "answer the same requests"},
	}
	for name, c := range bad {
		_, err := New(c.scripts, Options{})
		if err == nil || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: want an error containing %q, got %v", name, c.reason, err)
		}
	}
	if _, err := New(one(Step{Reply: good}), Options{DeltaSize: -1}); err == nil {
		t.Error("a negative delta size was accepted")
	}
	// A script that ends by asking for a tool is allowed: it is how a
	// step limit is tested.
	if _, err := New(one(Step{Reply: asks}, Step{Reply: asks}), Options{}); err != nil {
		t.Errorf("a script that ends on a tool call was refused: %v", err)
	}
}

const goodFile = `{"test_data": true, "scripts": [{"name": "s", "when": {"user_text": "Hello"}, "steps": [
	{"reply": {"message": {"role": "assistant", "blocks": [{"type": "text", "text": "Hello."}]}, "stop_reason": "end", "usage": {"input_tokens": 3, "output_tokens": 2}}}
]}]}`

func TestDecodeReadsAFileAndRefusesABadOne(t *testing.T) {
	scripts, err := Decode([]byte(goodFile))
	if err != nil {
		t.Fatal(err)
	}
	want := []Script{{Name: "s", When: When{UserText: "Hello"}, Steps: []Step{{Reply: reply(seam.StopEnd, seam.Usage{InputTokens: 3, OutputTokens: 2}, text("Hello."))}}}}
	if !reflect.DeepEqual(scripts, want) {
		t.Errorf("decoded %+v, want %+v", scripts, want)
	}

	for name, c := range map[string]struct{ raw, reason string }{
		"not marked as test data":   {strings.Replace(goodFile, `"test_data": true, `, "", 1), "not marked as test data"},
		"marked as not test data":   {strings.Replace(goodFile, `"test_data": true`, `"test_data": false`, 1), "not marked as test data"},
		"no scripts":                {`{"test_data": true, "scripts": []}`, "holds no scripts"},
		"an unknown field":          {strings.Replace(goodFile, `"name": "s"`, `"name": "s", "mood": "calm"`, 1), "unknown field"},
		"an unknown block field":    {strings.Replace(goodFile, `"text": "Hello."`, `"text": "Hello.", "colour": "red"`, 1), "unknown field"},
		"an unknown condition":      {strings.Replace(goodFile, `"user_text"`, `"weather"`, 1), "unknown field"},
		"one name twice in a file": {strings.Replace(goodFile, `]}]}`, `]}, {"name": "s", "when": {}, "steps": [{"error": {"kind": "other", "message": "x"}}]}]}`, 1), "two scripts are named"},
		"two values":                {goodFile + goodFile, "more than one JSON value"},
		"not JSON":                  {"name: s", "invalid character"},
		"a script the seam refuses": {strings.Replace(goodFile, `"stop_reason": "end"`, `"stop_reason": "bored"`, 1), `script "s": step 1: reply: stop reason "bored" is not known`},
	} {
		if _, err := Decode([]byte(c.raw)); err == nil || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: want an error containing %q, got %v", name, c.reason, err)
		}
	}
}

// A tool call's input streams the same characters however the file lays it
// out.
func TestToolInputIsWrittenTheShortWay(t *testing.T) {
	raw := `{"test_data": true, "scripts": [{"name": "s", "when": {}, "steps": [{"reply": {"message": {"role": "assistant", "blocks": [
		{"type": "tool_call", "id": "call_1", "name": "lookup_timetable", "input": {
			"route": "north",
			"day":   "Tuesday"
		}}
	]}, "stop_reason": "tool_call", "usage": {"input_tokens": 3, "output_tokens": 2}}}]}]}`
	scripts, err := Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	got, err := seam.Call(context.Background(), provider(t, Options{DeltaSize: 3}, scripts...), ask("Anything."))
	if err != nil {
		t.Fatal(err)
	}
	if input := string(seam.ToolCalls(got.Message)[0].Input); input != `{"route":"north","day":"Tuesday"}` {
		t.Errorf("the input arrived as %s", input)
	}
}

func TestLoadReadsAFolderInOrderOfFileName(t *testing.T) {
	file := func(name string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte(strings.Replace(strings.Replace(goodFile, `"name": "s"`, `"name": "`+name+`"`, 1), "Hello", name, 1))}
	}
	scripts, err := Load(fstest.MapFS{"b.json": file("second"), "a.json": file("first"), "notes.md": {Data: []byte("not a script")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 2 || scripts[0].Name != "first" || scripts[1].Name != "second" {
		t.Errorf("loaded %+v, want first then second", scripts)
	}
	for name, c := range map[string]struct {
		fsys   fstest.MapFS
		reason string
	}{
		"no script files":          {fstest.MapFS{"notes.md": {Data: []byte("x")}}, "holds no script files"},
		"a bad file":               {fstest.MapFS{"a.json": file("first"), "b.json": {Data: []byte(`{"scripts": []}`)}}, "b.json: the file is not marked"},
		"one name in two files":    {fstest.MapFS{"a.json": file("first"), "b.json": file("first")}, "two scripts are named"},
		"one condition, two files": {fstest.MapFS{"a.json": file("first"), "b.json": {Data: []byte(strings.Replace(string(file("first").Data), `"name": "first"`, `"name": "other"`, 1))}}, "answer the same requests"},
	} {
		if _, err := Load(c.fsys); err == nil || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: want an error containing %q, got %v", name, c.reason, err)
		}
	}
}

func TestTheScriptWithTheMostConditionsWins(t *testing.T) {
	says := func(s string) []Step { return []Step{{Reply: reply(seam.StopEnd, seam.Usage{}, text(s))}} }
	p := provider(t, Options{},
		Script{Name: "anything", Steps: says("anything")},
		Script{Name: "about ferries", When: When{UserTextContains: "ferry"}, Steps: says("about ferries")},
		Script{Name: "also about ferries", When: When{UserTextContains: "the ferry"}, Steps: says("also about ferries")},
		Script{Name: "the exact question", When: When{UserText: "When does the ferry leave?", UserTextContains: "ferry"}, Steps: says("the exact question")},
		Script{Name: "a title", When: When{SystemContains: "short title"}, Steps: says("a title")},
		Script{Name: "the small model", When: When{Model: "fake-small", UserTextContains: "ferry", SystemContains: "short title"}, Steps: says("the small model")},
	)
	title := []seam.Block{text("Write a Short Title for this chat.")}
	for name, c := range map[string]struct {
		req  seam.Request
		want string
	}{
		"no condition holds but the empty one": {ask("Good morning."), "anything"},
		"a part of the text":                   {ask("Which FERRY is late?"), "about ferries"},
		"a tie goes to the earlier script":     {ask("Is the ferry late?"), "about ferries"},
		"the whole text, in any case":          {ask("  when does the ferry LEAVE?\n"), "the exact question"},
		"the system text":                      {seam.Request{System: title, Messages: ask("Good morning.").Messages}, "a title"},
		"the model, with two more conditions":  {seam.Request{Model: "fake-small", System: title, Messages: ask("When does the ferry leave?").Messages}, "the small model"},
		"another model":                        {seam.Request{Model: "fake-large", Messages: ask("When does the ferry leave?").Messages}, "the exact question"},
	} {
		got, err := seam.Call(context.Background(), p, c.req)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if seam.Text(got.Message) != c.want {
			t.Errorf("%s: answered by %q, want %q", name, seam.Text(got.Message), c.want)
		}
	}
}

// The person's last message picks the script, however long the chat is.
func TestTheLastMessageFromThePersonPicksTheScript(t *testing.T) {
	p := provider(t, Options{}, toolScript(),
		Script{Name: "thanks", When: When{UserText: "Thank you."}, Steps: []Step{{Reply: reply(seam.StopEnd, seam.Usage{}, text("You are welcome."))}}})
	ctx := context.Background()

	req := ask("When does the north ferry leave on Tuesday?")
	first, err := seam.Call(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	req = answer(req, first)
	second, err := seam.Call(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	req.Messages = append(req.Messages, second.Message, ask("Thank you.").Messages[0])
	third, err := seam.Call(ctx, p, req)
	if err != nil || seam.Text(third.Message) != "You are welcome." {
		t.Fatalf("the next message got %+v (%v)", third, err)
	}

	// The same question later in the same chat starts the script again,
	// and its tool calls get ids the chat has not used.
	req.Messages = append(req.Messages, third.Message, ask("When does the north ferry leave on Tuesday?").Messages[0])
	fourth, err := seam.Call(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	calls := seam.ToolCalls(fourth.Message)
	if len(calls) != 2 || calls[0].ID != "call_1-2" || calls[1].ID != "call_2-2" {
		t.Fatalf("the second use made the calls %+v, want ids call_1-2 and call_2-2", calls)
	}
	req = answer(req, fourth)
	if err := seam.ValidateRequest(req); err != nil {
		t.Fatalf("the chat is no longer a valid request: %v", err)
	}
	fifth, err := seam.Call(ctx, p, req)
	if err != nil || seam.Text(fifth.Message) != ferryAnswer {
		t.Errorf("the second use ended with %+v (%v)", fifth, err)
	}
}

func TestRequestsTheFakeRefuses(t *testing.T) {
	s := toolScript()
	p := provider(t, Options{}, s)
	asked := ask("When does the north ferry leave on Tuesday?")
	first := *s.Steps[0].Reply
	second := *s.Steps[1].Reply

	unanswered := asked
	unanswered.Messages = append(unanswered.Messages[:1:1], first.Message, ask("Never mind.").Messages[0])
	halfAnswered := answer(asked, first)
	halfAnswered.Messages[2].Blocks = halfAnswered.Messages[2].Blocks[:1]
	endsOnTheAssistant := asked
	endsOnTheAssistant.Messages = append(endsOnTheAssistant.Messages[:1:1], second.Message)
	onlyResults := seam.Request{Messages: answer(asked, first).Messages[1:]}
	// A script whose every step asks for a tool runs out of steps.
	loop := Script{Name: "loop", Steps: []Step{{Reply: reply(seam.StopToolCall, seam.Usage{}, call("call_1", "current_time", `{}`))}}}
	pastTheEnd := answer(ask("Go on."), *loop.Steps[0].Reply)

	for name, c := range map[string]struct {
		p      *Provider
		req    seam.Request
		reason string
	}{
		"no script matches":           {p, ask("Good morning."), `no script answers a request whose last user text is "good morning."`},
		"a request the seam refuses":  {p, seam.Request{}, "the request is not valid"},
		"a tool call left unanswered": {p, unanswered, "tool call call_1 has no result"},
		"one of two calls unanswered": {p, halfAnswered, "tool call call_2 has no result"},
		"the assistant spoke last":    {p, endsOnTheAssistant, "the last message is the assistant's"},
		"nothing from the person":     {p, onlyResults, "holds no message from the person"},
		"a step the script lacks":     {provider(t, Options{}, loop), pastTheEnd, `script "loop" has no step 2`},
	} {
		events, err := play(t, c.p, c.req)
		var got *seam.Error
		if !errors.As(err, &got) || got.Kind != seam.ErrInvalid || got.Retryable || !strings.Contains(got.Message, c.reason) {
			t.Errorf("%s: Stream returned %v, want an invalid error containing %q", name, err, c.reason)
		}
		if len(events) != 1 || events[0].Type != seam.EventError {
			t.Errorf("%s: the stream is %+v, want one error event", name, events)
		}
	}
}

func TestStreamStopsWhenToldTo(t *testing.T) {
	script := Script{Name: "ferry", Steps: []Step{{Reply: reply(seam.StopEnd, seam.Usage{}, text(ferryAnswer))}}}
	req := ask("When does the ferry leave?")

	// The turn is stopped after three events.
	ctx, cancel := context.WithCancel(context.Background())
	seen := 0
	err := provider(t, Options{DeltaSize: 5}, script).Stream(ctx, req, func(seam.Event) error {
		if seen++; seen == 3 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || seen != 3 {
		t.Errorf("after a cancel Stream returned %v and had sent %d events, want the context's error and 3", err, seen)
	}

	// The receiver gives up.
	gone := errors.New("the browser went away")
	seen = 0
	err = provider(t, Options{DeltaSize: 5}, script).Stream(context.Background(), req, func(seam.Event) error {
		if seen++; seen == 4 {
			return gone
		}
		return nil
	})
	if err != gone || seen != 4 {
		t.Errorf("after emit failed Stream returned %v and had sent %d events, want emit's error and 4", err, seen)
	}

	// A cancel during the wait before a piece ends the call at once.
	ctx, cancel = context.WithCancel(context.Background())
	seen = 0
	done := make(chan error, 1)
	slow := provider(t, Options{DeltaSize: 5, Delay: time.Hour}, script)
	go func() {
		done <- slow.Stream(ctx, req, func(e seam.Event) error {
			if seen++; e.Type == seam.EventBlockStart {
				cancel()
			}
			return nil
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || seen != 2 {
			t.Errorf("a cancel during the wait gave %v after %d events, want the context's error after 2", err, seen)
		}
	case <-time.After(10 * time.Second):
		t.Error("Stream went on waiting after its context was cancelled")
	}
}

func TestTheDelayComesBeforeEachPiece(t *testing.T) {
	script := Script{Name: "ferry", Steps: []Step{{Reply: reply(seam.StopEnd, seam.Usage{}, text("Nine."))}}}
	start := time.Now()
	events, err := play(t, provider(t, Options{DeltaSize: 2, Delay: 5 * time.Millisecond}, script), ask("When?"))
	if err != nil {
		t.Fatal(err)
	}
	pieces := count(events, func(e seam.Event) bool { return e.Type == seam.EventBlockDelta })
	if took := time.Since(start); pieces != 3 || took < 15*time.Millisecond {
		t.Errorf("%d pieces took %s, want 3 pieces and at least 15ms", pieces, took)
	}
}

func TestTheSameRequestGetsTheSameStream(t *testing.T) {
	p := provider(t, Options{DeltaSize: 6}, toolScript())
	req := ask("When does the north ferry leave on Tuesday?")
	first, err := play(t, p, req)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		again, err := play(t, p, req)
		if err != nil || !reflect.DeepEqual(again, first) {
			t.Fatalf("run %d differs from the first (%v)", i+2, err)
		}
	}
}

func TestDefaultOptionsComeFromTheSettings(t *testing.T) {
	opts, err := DefaultOptions()
	if err != nil {
		t.Fatal(err)
	}
	if opts.DeltaSize <= 0 || opts.Delay <= 0 {
		t.Errorf("the defaults are %+v, want a delta size and a delay", opts)
	}
	if _, err := New([]Script{toolScript()}, opts); err != nil {
		t.Errorf("the default options were refused: %v", err)
	}
}

// requestFor is the smallest request a script's conditions accept.
func requestFor(s Script) seam.Request {
	said := s.When.UserText
	if said == "" {
		said = s.When.UserTextContains
	}
	if said == "" {
		said = "Something no script was written for."
	}
	req := ask(said)
	req.Model = s.When.Model
	if s.When.SystemContains != "" {
		req.System = []seam.Block{text(s.When.SystemContains)}
	}
	return req
}

// Every script in fixtures/fake-provider loads, is reached by a request
// that meets its conditions, and plays each step to the end it scripts.
func TestTheExampleScriptsPlay(t *testing.T) {
	scripts, err := Load(os.DirFS("../../../fixtures/fake-provider"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) < 4 {
		t.Fatalf("the folder holds %d scripts, want the examples", len(scripts))
	}
	p := provider(t, Options{DeltaSize: 12}, scripts...)
	kinds := map[string]bool{}
	for _, s := range scripts {
		req := requestFor(s)
		for i, step := range s.Steps {
			var col seam.Collector
			err := p.Stream(context.Background(), req, col.Add)
			switch {
			case step.Reply != nil && step.CutAfter == nil:
				want := *step.Reply
				if want.Model == "" {
					want.Model = req.Model
				}
				got, respErr := col.Response()
				if err != nil || respErr != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("%s, step %d: collected %+v (%v, %v), want %+v", s.Name, i+1, got, err, respErr, want)
				}
				req = answer(req, got)
				kinds["reply"] = true
				for _, b := range got.Message.Blocks {
					kinds[string(b.Type)] = true
				}
			case step.Reply == nil:
				var got *seam.Error
				if !errors.As(err, &got) || *got != *step.Error {
					t.Errorf("%s, step %d: returned %v, want the scripted error", s.Name, i+1, err)
				}
				kinds["error"] = true
			default:
				var got *seam.Error
				if !errors.As(err, &got) || (step.Error != nil && *got != *step.Error) || (step.Error == nil && got.Kind != seam.ErrUnavailable) {
					t.Errorf("%s, step %d: returned %v, want the stream to fail", s.Name, i+1, err)
				}
				if n := utf8.RuneCountInString(seam.Text(col.Partial())); n != *step.CutAfter {
					t.Errorf("%s, step %d: %d characters arrived before the cut, want %d", s.Name, i+1, n, *step.CutAfter)
				}
				kinds["cut"] = true
			}
		}
	}
	for _, kind := range []string{"reply", "text", "thinking", "tool_call", "error", "cut"} {
		if !kinds[kind] {
			t.Errorf("no example script shows %s", kind)
		}
	}
}
