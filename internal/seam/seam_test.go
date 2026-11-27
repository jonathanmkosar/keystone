package seam

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Every value here is invented test data.

var (
	textBlock     = Block{Type: BlockText, Text: "The ferry leaves at nine."}
	imageBlock    = Block{Type: BlockImage, MediaType: "image/png", Data: []byte{0x89, 'P', 'N', 'G', 0, 1, 2}}
	imageRefBlock = Block{Type: BlockImage, MediaType: "image/jpeg", Ref: "9f2c41d0aa17"}
	fileBlock     = Block{Type: BlockFile, MediaType: "application/pdf", Name: "timetable.pdf", Ref: "4be07c93d615"}
	fileDataBlock = Block{Type: BlockFile, MediaType: "text/plain", Name: "notes.txt", Data: []byte("two lines\nof notes")}
	callBlock     = Block{Type: BlockToolCall, ID: "call_1", Name: "lookup_timetable", Input: json.RawMessage(`{"route":"north","day":"Tuesday"}`)}
	resultBlock   = Block{Type: BlockToolResult, ID: "call_1", Content: []Block{{Type: BlockText, Text: "09:00, 11:30"}, imageRefBlock}}
	failedBlock   = Block{Type: BlockToolResult, ID: "call_1", IsError: true, Content: []Block{{Type: BlockText, Text: "no such route"}}}
	emptyResult   = Block{Type: BlockToolResult, ID: "call_1"}
	thinkingBlock = Block{Type: BlockThinking, Text: "They asked about Tuesday.", Signature: "sig-test-0001"}
	withheldBlock = Block{Type: BlockThinking, Signature: "sig-test-0002"}
)

var allBlocks = map[string]Block{
	"text": textBlock, "image": imageBlock, "image by ref": imageRefBlock,
	"file": fileBlock, "file with data": fileDataBlock,
	"tool call": callBlock, "tool result": resultBlock, "failed tool result": failedBlock,
	"empty tool result": emptyResult, "thinking": thinkingBlock, "withheld thinking": withheldBlock,
}

func TestEveryBlockTypeIsCovered(t *testing.T) {
	seen := map[BlockType]bool{}
	for _, b := range allBlocks {
		seen[b.Type] = true
	}
	for _, typ := range []BlockType{BlockText, BlockImage, BlockFile, BlockToolCall, BlockToolResult, BlockThinking} {
		if !seen[typ] {
			t.Errorf("no test block of type %s", typ)
		}
	}
}

func TestBlocksRoundTripThroughJSON(t *testing.T) {
	for name, b := range allBlocks {
		if err := ValidateBlock(b); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		role := RoleUser
		if b.Type == BlockToolCall || b.Type == BlockThinking {
			role = RoleAssistant
		}
		want := Message{Role: role, Blocks: []Block{b}}
		raw, err := EncodeMessage(want)
		if err != nil {
			t.Errorf("%s: encode: %v", name, err)
			continue
		}
		got, err := DecodeMessage(raw)
		if err != nil {
			t.Errorf("%s: decode: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: came back as %+v, want %+v", name, got, want)
		}
		again, _ := EncodeMessage(got)
		if string(again) != string(raw) {
			t.Errorf("%s: the second encoding differs:\n%s\n%s", name, raw, again)
		}
	}
}

func TestBlockJSONShape(t *testing.T) {
	raw, err := json.Marshal(callBlock)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"tool_call","name":"lookup_timetable","id":"call_1","input":{"route":"north","day":"Tuesday"}}`
	if string(raw) != want {
		t.Errorf("got  %s\nwant %s", raw, want)
	}
}

func TestInvalidBlocksAreRefused(t *testing.T) {
	bad := map[string]Block{
		"unknown type":            {Type: "audio", Text: "x"},
		"no type":                 {Text: "x"},
		"empty text":              {Type: BlockText},
		"text with an id":         {Type: BlockText, Text: "x", ID: "call_1"},
		"text with a signature":   {Type: BlockText, Text: "x", Signature: "s"},
		"empty thinking":          {Type: BlockThinking},
		"image with no media":     {Type: BlockImage, Data: []byte{1}},
		"image that is a pdf":     {Type: BlockImage, MediaType: "application/pdf", Data: []byte{1}},
		"image with nothing":      {Type: BlockImage, MediaType: "image/png"},
		"image with data and ref": {Type: BlockImage, MediaType: "image/png", Data: []byte{1}, Ref: "abc"},
		"image with a name":       {Type: BlockImage, MediaType: "image/png", Ref: "abc", Name: "a.png"},
		"file with text":          {Type: BlockFile, MediaType: "text/plain", Ref: "abc", Text: "x"},
		"call with no id":         {Type: BlockToolCall, Name: "t", Input: json.RawMessage(`{}`)},
		"call with no name":       {Type: BlockToolCall, ID: "c", Input: json.RawMessage(`{}`)},
		"call with no input":      {Type: BlockToolCall, ID: "c", Name: "t"},
		"call with array input":   {Type: BlockToolCall, ID: "c", Name: "t", Input: json.RawMessage(`[1]`)},
		"call with broken input":  {Type: BlockToolCall, ID: "c", Name: "t", Input: json.RawMessage(`{"a":`)},
		"call with content":       {Type: BlockToolCall, ID: "c", Name: "t", Input: json.RawMessage(`{}`), IsError: true},
		"result with no id":       {Type: BlockToolResult, Content: []Block{textBlock}},
		"result holding a call":   {Type: BlockToolResult, ID: "c", Content: []Block{callBlock}},
		"result holding bad text": {Type: BlockToolResult, ID: "c", Content: []Block{{Type: BlockText}}},
		"result with input":       {Type: BlockToolResult, ID: "c", Input: json.RawMessage(`{}`)},
	}
	for name, b := range bad {
		if err := ValidateBlock(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestInvalidMessagesAreRefused(t *testing.T) {
	bad := map[string]Message{
		"no role":                  {Blocks: []Block{textBlock}},
		"system role":              {Role: "system", Blocks: []Block{textBlock}},
		"no blocks":                {Role: RoleUser},
		"user tool call":           {Role: RoleUser, Blocks: []Block{callBlock}},
		"user thinking":            {Role: RoleUser, Blocks: []Block{thinkingBlock}},
		"assistant tool result":    {Role: RoleAssistant, Blocks: []Block{resultBlock}},
		"a bad block among others": {Role: RoleUser, Blocks: []Block{textBlock, {Type: BlockText}}},
	}
	for name, m := range bad {
		if err := ValidateMessage(m); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func fullRequest() Request {
	return Request{
		Model:  "test-model",
		System: []Block{{Type: BlockText, Text: "Answer in one sentence."}},
		Messages: []Message{
			{Role: RoleUser, Blocks: []Block{textBlock, imageBlock, fileBlock}},
			{Role: RoleAssistant, Blocks: []Block{thinkingBlock, {Type: BlockText, Text: "Let me look."}, callBlock}},
			{Role: RoleUser, Blocks: []Block{resultBlock}},
		},
		Tools: []Tool{{
			Name:        "lookup_timetable",
			Description: "Looks up ferry times.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"route":{"type":"string"}}}`),
		}},
		MaxOutputTokens: 1024,
		ThinkingTokens:  512,
	}
}

func TestRequestRoundTripsThroughJSON(t *testing.T) {
	for name, want := range map[string]Request{
		"full":    fullRequest(),
		"minimal": {Messages: []Message{{Role: RoleUser, Blocks: []Block{textBlock}}}},
	} {
		raw, err := EncodeRequest(want)
		if err != nil {
			t.Fatalf("%s: encode: %v", name, err)
		}
		got, err := DecodeRequest(raw)
		if err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: came back as %+v, want %+v", name, got, want)
		}
	}
}

func TestInvalidRequestsAreRefused(t *testing.T) {
	bad := map[string]func(*Request){
		"no messages":            func(r *Request) { r.Messages = nil },
		"negative output limit":  func(r *Request) { r.MaxOutputTokens = -1 },
		"negative thinking":      func(r *Request) { r.ThinkingTokens = -1 },
		"image in system":        func(r *Request) { r.System = []Block{imageBlock} },
		"tool with no name":      func(r *Request) { r.Tools[0].Name = "" },
		"tool with no schema":    func(r *Request) { r.Tools[0].InputSchema = nil },
		"tool offered twice":     func(r *Request) { r.Tools = append(r.Tools, r.Tools[0]) },
		"bad message":            func(r *Request) { r.Messages[0].Role = "" },
		"result with no call":    func(r *Request) { r.Messages = r.Messages[2:] },
		"result before its call": func(r *Request) { r.Messages[1], r.Messages[2] = r.Messages[2], r.Messages[1] },
		"result given twice":     func(r *Request) { r.Messages = append(r.Messages, r.Messages[2]) },
		"call id used twice":     func(r *Request) { r.Messages = append(r.Messages, r.Messages[1]) },
		"unknown effort":         func(r *Request) { r.Effort = "extreme" },
		"effort in capitals":     func(r *Request) { r.Effort = "HIGH" },
	}
	for name, breakIt := range bad {
		r := fullRequest()
		breakIt(&r)
		if err := ValidateRequest(r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Every effort goes through JSON and comes back, and a request with none
// has no effort field at all, so it is stored as it was before effort
// existed (P14-01).
func TestEffortRoundTripsAndIsLeftOutWhenEmpty(t *testing.T) {
	for _, e := range Efforts {
		r := fullRequest()
		r.Effort = e
		raw, err := EncodeRequest(r)
		if err != nil {
			t.Fatalf("%s: encode: %v", e, err)
		}
		got, err := DecodeRequest(raw)
		if err != nil || got.Effort != e {
			t.Errorf("%s: came back as %q (%v)", e, got.Effort, err)
		}
	}
	raw, err := EncodeRequest(fullRequest())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "effort") {
		t.Errorf("a request with no effort carries the field: %s", raw)
	}
	if Effort("").Known() {
		t.Error("an empty effort counts as a level")
	}
}

func TestDecodeRefusesWhatItDoesNotKnow(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown field":  `{"role":"user","blocks":[{"type":"text","text":"x","colour":"red"}]}`,
		"two values":     `{"role":"user","blocks":[{"type":"text","text":"x"}]} {"role":"user"}`,
		"not JSON":       `role: user`,
		"invalid inside": `{"role":"user","blocks":[{"type":"text"}]}`,
	} {
		if _, err := DecodeMessage([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestUsage(t *testing.T) {
	a := Usage{InputTokens: 100, OutputTokens: 40, CacheReadTokens: 900, CacheWriteTokens: 50, ThinkingTokens: 10}
	b := Usage{InputTokens: 7, OutputTokens: 3, Estimated: true}
	if got := a.Total(); got != 1090 {
		t.Errorf("total is %d, want 1090", got)
	}
	want := Usage{InputTokens: 107, OutputTokens: 43, CacheReadTokens: 900, CacheWriteTokens: 50, ThinkingTokens: 10, Estimated: true}
	if got := a.Add(b); got != want {
		t.Errorf("sum is %+v, want %+v", got, want)
	}
	for _, u := range []Usage{a, b, want, {}} {
		raw, err := json.Marshal(u)
		if err != nil {
			t.Fatal(err)
		}
		var got Usage
		if err := json.Unmarshal(raw, &got); err != nil || got != u {
			t.Errorf("%+v came back as %+v (%v)", u, got, err)
		}
	}
	// A zero count is still written, so a record always shows both sides.
	if raw, _ := json.Marshal(Usage{}); string(raw) != `{"input_tokens":0,"output_tokens":0}` {
		t.Errorf("empty usage is %s", raw)
	}
	for name, u := range map[string]Usage{
		"negative":                  {InputTokens: -1},
		"more thinking than output": {OutputTokens: 5, ThinkingTokens: 6},
	} {
		if err := ValidateUsage(u); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestErrorKeepsItsKindWhenWrapped(t *testing.T) {
	var target *Error
	err := error(&Error{Kind: ErrRateLimited, Message: "slow down", Retryable: true})
	if !errors.As(err, &target) || target.Kind != ErrRateLimited {
		t.Errorf("errors.As lost the kind: %v", err)
	}
	if !strings.Contains(err.Error(), "rate_limited") || !strings.Contains(err.Error(), "slow down") {
		t.Errorf("the message is %q", err.Error())
	}
}
