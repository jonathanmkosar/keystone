package seam

import (
	"reflect"
	"strings"
	"testing"
)

// A request's cache points (P5-03) go through JSON and come back the same,
// and points outside the request are refused.
func TestCachePoints(t *testing.T) {
	said := func(role Role, text string) Message {
		return Message{Role: role, Blocks: []Block{{Type: BlockText, Text: text}}}
	}
	req := Request{
		System:   []Block{{Type: BlockText, Text: "Answer briefly."}, {Type: BlockText, Text: "Earlier: the walk was planned."}},
		Messages: []Message{said(RoleUser, "When is high water?"), said(RoleAssistant, "At six."), said(RoleUser, "And tomorrow?")},
		Cache:    &Cache{Start: 1, History: 2},
	}
	raw, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"cache":{"start":1,"history":2}`) {
		t.Fatalf("the points are not in the JSON: %s", raw)
	}
	back, err := DecodeRequest(raw)
	if err != nil || !reflect.DeepEqual(back, req) {
		t.Fatalf("the request did not come back the same: %v\n%+v", err, back)
	}
	// None is written when there are none.
	req.Cache = nil
	if raw, _ := EncodeRequest(req); strings.Contains(string(raw), "cache") {
		t.Fatalf("a request with no points wrote some: %s", raw)
	}
	// The whole of each is allowed, and nothing past it.
	for _, c := range []Cache{{Start: 0, History: 0}, {Start: 2, History: 3}} {
		req.Cache = &c
		if err := ValidateRequest(req); err != nil {
			t.Errorf("points %+v were refused: %v", c, err)
		}
	}
	for _, c := range []Cache{{Start: -1}, {Start: 3}, {History: -1}, {History: 4}} {
		req.Cache = &c
		if err := ValidateRequest(req); err == nil {
			t.Errorf("points %+v were accepted", c)
		}
	}
}
