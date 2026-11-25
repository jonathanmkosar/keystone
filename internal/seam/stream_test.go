package seam

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// Every value here is invented test data.

var testUsage = Usage{InputTokens: 120, OutputTokens: 60, CacheReadTokens: 800, ThinkingTokens: 20}

// allEvents holds at least one event of every type and every kind of delta.
var allEvents = map[string]Event{
	"start":             {Type: EventStart, Model: "test-model"},
	"start, no model":   {Type: EventStart},
	"text opens":        {Type: EventBlockStart, Index: 0, Block: &Block{Type: BlockText}},
	"thinking opens":    {Type: EventBlockStart, Index: 1, Block: &Block{Type: BlockThinking}},
	"tool call opens":   {Type: EventBlockStart, Index: 2, Block: &Block{Type: BlockToolCall, ID: "call_1", Name: "lookup_timetable"}},
	"text delta":        {Type: EventBlockDelta, Index: 0, Text: "The ferry "},
	"input delta":       {Type: EventBlockDelta, Index: 2, Input: `{"route":`},
	"signature delta":   {Type: EventBlockDelta, Index: 1, Signature: "sig-test-0001"},
	"block stop":        {Type: EventBlockStop, Index: 2},
	"block stop, first": {Type: EventBlockStop},
	"stop":              {Type: EventStop, StopReason: StopEnd, Usage: &testUsage},
	"stop for tools":    {Type: EventStop, StopReason: StopToolCall, Usage: &Usage{Estimated: true}},
	"stop at the limit": {Type: EventStop, StopReason: StopMaxTokens, Usage: &testUsage},
	"stop, refused":     {Type: EventStop, StopReason: StopRefused, Usage: &testUsage},
	"error":             {Type: EventError, Error: &Error{Kind: ErrOverloaded, Message: "try again shortly", Retryable: true}},
	"error, final":      {Type: EventError, Error: &Error{Kind: ErrAuth, Message: "the key was refused"}},
}

func TestEveryEventTypeIsCovered(t *testing.T) {
	seen := map[EventType]bool{}
	for _, e := range allEvents {
		seen[e.Type] = true
	}
	for _, typ := range []EventType{EventStart, EventBlockStart, EventBlockDelta, EventBlockStop, EventStop, EventError} {
		if !seen[typ] {
			t.Errorf("no test event of type %s", typ)
		}
	}
}

func TestEventsRoundTripThroughJSON(t *testing.T) {
	for name, want := range allEvents {
		raw, err := EncodeEvent(want)
		if err != nil {
			t.Errorf("%s: encode: %v", name, err)
			continue
		}
		got, err := DecodeEvent(raw)
		if err != nil {
			t.Errorf("%s: decode: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: came back as %s", name, raw)
		}
	}
}

func TestEveryErrorKindRoundTrips(t *testing.T) {
	for _, kind := range errorKinds {
		want := Event{Type: EventError, Error: &Error{Kind: kind, Message: "invented failure"}}
		raw, err := EncodeEvent(want)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		got, err := DecodeEvent(raw)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: came back as %s (%v)", kind, raw, err)
		}
	}
}

func TestInvalidEventsAreRefused(t *testing.T) {
	bad := map[string]Event{
		"unknown type":              {Type: "ping"},
		"start with text":           {Type: EventStart, Text: "x"},
		"start with an index":       {Type: EventStart, Index: 1},
		"block start, no block":     {Type: EventBlockStart},
		"block start with content":  {Type: EventBlockStart, Block: &Block{Type: BlockText, Text: "x"}},
		"block start, image":        {Type: EventBlockStart, Block: &Block{Type: BlockImage, MediaType: "image/png", Ref: "abc"}},
		"block start, tool result":  {Type: EventBlockStart, Block: &Block{Type: BlockToolResult, ID: "call_1"}},
		"tool call opens unnamed":   {Type: EventBlockStart, Block: &Block{Type: BlockToolCall, ID: "call_1"}},
		"tool call opens with args": {Type: EventBlockStart, Block: &Block{Type: BlockToolCall, ID: "call_1", Name: "t", Input: json.RawMessage(`{}`)}},
		"negative index":            {Type: EventBlockStop, Index: -1},
		"empty delta":               {Type: EventBlockDelta},
		"delta with a block":        {Type: EventBlockDelta, Text: "x", Block: &Block{Type: BlockText}},
		"block stop with text":      {Type: EventBlockStop, Text: "x"},
		"stop, no reason":           {Type: EventStop, Usage: &testUsage},
		"stop, unknown reason":      {Type: EventStop, StopReason: "bored", Usage: &testUsage},
		"stop, no usage":            {Type: EventStop, StopReason: StopEnd},
		"stop, bad usage":           {Type: EventStop, StopReason: StopEnd, Usage: &Usage{OutputTokens: -1}},
		"stop with an error":        {Type: EventStop, StopReason: StopEnd, Usage: &testUsage, Error: &Error{Kind: ErrOther, Message: "x"}},
		"error, no error":           {Type: EventError},
		"error, unknown kind":       {Type: EventError, Error: &Error{Kind: "weather", Message: "x"}},
		"error, no message":         {Type: EventError, Error: &Error{Kind: ErrOther}},
		"error with usage":          {Type: EventError, Error: &Error{Kind: ErrOther, Message: "x"}, Usage: &testUsage},
	}
	for name, e := range bad {
		if err := ValidateEvent(e); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func fullResponse() Response {
	return Response{
		Model: "test-model",
		Message: Message{Role: RoleAssistant, Blocks: []Block{
			thinkingBlock,
			withheldBlock,
			{Type: BlockText, Text: "Let me look that up."},
			callBlock,
			{Type: BlockToolCall, ID: "call_2", Name: "current_time", Input: json.RawMessage(`{}`)},
		}},
		StopReason: StopToolCall,
		Usage:      testUsage,
	}
}

// A whole reply becomes a stream, the stream goes through JSON one event at
// a time, and collecting it gives the same reply.
func TestResponseRoundTripsThroughTheStream(t *testing.T) {
	for name, want := range map[string]Response{
		"full":  fullResponse(),
		"text":  {Message: Message{Role: RoleAssistant, Blocks: []Block{textBlock}}, StopReason: StopEnd},
		"empty": {Message: Message{Role: RoleAssistant}, StopReason: StopRefused},
	} {
		events, err := Events(want)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var c Collector
		for i, e := range events {
			raw, err := EncodeEvent(e)
			if err != nil {
				t.Fatalf("%s: event %d: encode: %v", name, i, err)
			}
			back, err := DecodeEvent(raw)
			if err != nil {
				t.Fatalf("%s: event %d: decode: %v", name, i, err)
			}
			if err := c.Add(back); err != nil {
				t.Fatalf("%s: event %d: %v", name, i, err)
			}
		}
		got, err := c.Response()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: collected %+v, want %+v", name, got, want)
		}
		raw, err := EncodeResponse(got)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if again, err := DecodeResponse(raw); err != nil || !reflect.DeepEqual(again, want) {
			t.Errorf("%s: the response came back from JSON as %s (%v)", name, raw, err)
		}
	}
}

// A real provider sends many small deltas, and tool input split anywhere.
func TestCollectJoinsSmallDeltas(t *testing.T) {
	events := []Event{
		{Type: EventStart, Model: "test-model"},
		{Type: EventBlockStart, Index: 0, Block: &Block{Type: BlockThinking}},
		{Type: EventBlockDelta, Index: 0, Text: "They asked "},
		{Type: EventBlockDelta, Index: 0, Text: "about Tuesday."},
		{Type: EventBlockDelta, Index: 0, Signature: "sig-test-"},
		{Type: EventBlockDelta, Index: 0, Signature: "0001"},
		{Type: EventBlockStop, Index: 0},
		{Type: EventBlockStart, Index: 1, Block: &Block{Type: BlockText}}, // opened and left empty
		{Type: EventBlockStop, Index: 1},
		{Type: EventBlockStart, Index: 2, Block: &Block{Type: BlockToolCall, ID: "call_1", Name: "lookup_timetable"}},
		{Type: EventBlockDelta, Index: 2, Input: `{"route":"no`},
		{Type: EventBlockDelta, Index: 2, Input: `rth","day":"Tuesday"}`},
		{Type: EventBlockStop, Index: 2},
		{Type: EventBlockStart, Index: 3, Block: &Block{Type: BlockToolCall, ID: "call_2", Name: "current_time"}},
		{Type: EventBlockStop, Index: 3}, // no input at all
		{Type: EventStop, StopReason: StopToolCall, Usage: &testUsage},
	}
	got, err := Collect(events)
	if err != nil {
		t.Fatal(err)
	}
	want := Response{
		Model: "test-model",
		Message: Message{Role: RoleAssistant, Blocks: []Block{
			thinkingBlock,
			callBlock,
			{Type: BlockToolCall, ID: "call_2", Name: "current_time", Input: json.RawMessage(`{}`)},
		}},
		StopReason: StopToolCall,
		Usage:      testUsage,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collected %+v, want %+v", got, want)
	}
	if calls := ToolCalls(got.Message); len(calls) != 2 || calls[1].ID != "call_2" {
		t.Errorf("tool calls are %+v", calls)
	}
}

func TestCollectRefusesStreamsOutOfOrder(t *testing.T) {
	start := Event{Type: EventStart}
	open := Event{Type: EventBlockStart, Index: 0, Block: &Block{Type: BlockText}}
	delta := Event{Type: EventBlockDelta, Index: 0, Text: "x"}
	closeIt := Event{Type: EventBlockStop, Index: 0}
	stop := Event{Type: EventStop, StopReason: StopEnd, Usage: &Usage{}}
	openCall := Event{Type: EventBlockStart, Index: 0, Block: &Block{Type: BlockToolCall, ID: "c", Name: "t"}}
	openThinking := Event{Type: EventBlockStart, Index: 0, Block: &Block{Type: BlockThinking}}

	bad := map[string][]Event{
		"nothing":                   {},
		"no start":                  {open, delta, closeIt, stop},
		"two starts":                {start, start, stop},
		"no stop":                   {start, open, delta, closeIt},
		"delta with nothing open":   {start, delta, stop},
		"delta after the close":     {start, open, delta, closeIt, delta, stop},
		"delta for another block":   {start, open, {Type: EventBlockDelta, Index: 1, Text: "x"}, closeIt, stop},
		"close with nothing open":   {start, closeIt, stop},
		"open inside an open block": {start, open, {Type: EventBlockStart, Index: 1, Block: &Block{Type: BlockText}}, stop},
		"a skipped index":           {start, {Type: EventBlockStart, Index: 1, Block: &Block{Type: BlockText}}, stop},
		"an index used twice":       {start, open, delta, closeIt, open, delta, closeIt, stop},
		"stop inside a block":       {start, open, delta, stop},
		"event after the stop":      {start, stop, open},
		"text into a tool call":     {start, openCall, delta, closeIt, stop},
		"input into text":           {start, open, {Type: EventBlockDelta, Index: 0, Input: "{}"}, closeIt, stop},
		"signature into text":       {start, open, {Type: EventBlockDelta, Index: 0, Signature: "s"}, closeIt, stop},
		"input into thinking":       {start, openThinking, {Type: EventBlockDelta, Index: 0, Input: "{}"}, closeIt, stop},
		"broken tool input":         {start, openCall, {Type: EventBlockDelta, Index: 0, Input: `{"a":`}, closeIt, {Type: EventStop, StopReason: StopToolCall, Usage: &Usage{}}},
		"tool stop with no call":    {start, open, delta, closeIt, {Type: EventStop, StopReason: StopToolCall, Usage: &Usage{}}},
		"a malformed event":         {start, {Type: EventBlockDelta}, stop},
	}
	for name, events := range bad {
		if _, err := Collect(events); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAnErrorEventEndsTheStream(t *testing.T) {
	failure := &Error{Kind: ErrOverloaded, Message: "try again shortly", Retryable: true}
	var c Collector
	for _, e := range []Event{
		{Type: EventStart},
		{Type: EventBlockStart, Index: 0, Block: &Block{Type: BlockText}},
		{Type: EventBlockDelta, Index: 0, Text: "The ferry "},
	} {
		if err := c.Add(e); err != nil {
			t.Fatal(err)
		}
	}
	err := c.Add(Event{Type: EventError, Error: failure})
	var got *Error
	if !errors.As(err, &got) || *got != *failure {
		t.Fatalf("the error came back as %v", err)
	}
	if _, err := c.Response(); err == nil {
		t.Error("a failed stream gave a response")
	}
	if err := c.Add(Event{Type: EventBlockStop, Index: 0}); err == nil {
		t.Error("the collector took an event after the error")
	}
	// What arrived before the failure is kept.
	if text := Text(c.Partial()); text != "The ferry " {
		t.Errorf("the partial text is %q", text)
	}

	_, err = Collect([]Event{{Type: EventStart}, {Type: EventError, Error: failure}})
	if !errors.As(err, &got) || got.Kind != ErrOverloaded {
		t.Errorf("Collect returned %v", err)
	}
}

func TestPartialLeavesOutAnUnfinishedToolCall(t *testing.T) {
	var c Collector
	for _, e := range []Event{
		{Type: EventStart},
		{Type: EventBlockStart, Index: 0, Block: &Block{Type: BlockText}},
		{Type: EventBlockDelta, Index: 0, Text: "Let me look."},
		{Type: EventBlockStop, Index: 0},
		{Type: EventBlockStart, Index: 1, Block: &Block{Type: BlockToolCall, ID: "call_1", Name: "lookup_timetable"}},
		{Type: EventBlockDelta, Index: 1, Input: `{"route":`},
	} {
		if err := c.Add(e); err != nil {
			t.Fatal(err)
		}
	}
	got := c.Partial()
	want := Message{Role: RoleAssistant, Blocks: []Block{{Type: BlockText, Text: "Let me look."}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("partial is %+v, want %+v", got, want)
	}
	if _, err := c.Response(); err == nil {
		t.Error("an unfinished stream gave a response")
	}
}

func TestInvalidResponsesAreRefused(t *testing.T) {
	bad := map[string]func(*Response){
		"user role":           func(r *Response) { r.Message.Role = RoleUser },
		"no stop reason":      func(r *Response) { r.StopReason = "" },
		"bad usage":           func(r *Response) { r.Usage.InputTokens = -1 },
		"an image":            func(r *Response) { r.Message.Blocks = append(r.Message.Blocks, imageBlock) },
		"a bad block":         func(r *Response) { r.Message.Blocks[0] = Block{Type: BlockText} },
		"tool stop, no calls": func(r *Response) { r.Message.Blocks = r.Message.Blocks[:3] },
	}
	for name, breakIt := range bad {
		r := fullResponse()
		breakIt(&r)
		if err := ValidateResponse(r); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := Events(r); err == nil {
			t.Errorf("%s: Events accepted it", name)
		}
	}
}
