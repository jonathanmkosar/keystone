package contextbuild

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The order of a call for caching (P5-03). Every question, answer, page and
// tool here is invented.

// grown is a chat's branch when the person sends their nth message: the
// n-1 turns before it, each a question and its answer, and the nth question
// with no reply yet. The branch of message n+1 begins with the branch of
// message n, as a real chat's does.
func grown(n int) []seam.Message {
	var out []seam.Message
	for i := 1; i < n; i++ {
		out = append(out,
			say(seam.RoleUser, fmt.Sprintf("Question %d about the harbour. %s", i, filler(300))),
			say(seam.RoleAssistant, fmt.Sprintf("Answer %d about the harbour. %s", i, filler(300))))
	}
	return append(out, say(seam.RoleUser, fmt.Sprintf("Question %d about the harbour. %s", n, filler(300))))
}

func tool(name string) seam.Tool {
	return seam.Tool{Name: name, Description: "Reads the " + name + ".", InputSchema: json.RawMessage(`{"type":"object"}`)}
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The item's second proof: across the turns of a chat, with new recall on
// every one, the tools, the system text and every earlier turn are sent in
// the same bytes as on the call before, and what changes, recall and the
// current message, comes after the history.
func TestTheStartAndTheHistoryStayTheSameFromTurnToTurn(t *testing.T) {
	o, b := shipped(t)
	input := func(n int) Input {
		return Input{
			Model: model(200000, 4000), Budget: b, Options: o, Messages: grown(n),
			// The tool loop and the lookups come in whatever order they are
			// registered in.
			Tools:     []seam.Tool{tool("timetable"), tool("date_time"), tool("knowledge_search")},
			Profile:   "The person keeps a bakery by the north pier.",
			PersonMap: "The bakery, the ferry, the lighthouse walk.",
			Summary:   "Earlier: the person asked about the ferry and the bakery's opening hours.",
			Recall: []Item{
				{Label: "harbour: Tide table (page 4)", Ref: "page:4", Text: fmt.Sprintf("From the person's knowledge store, for question %d: high water is at six.", n)},
				{Label: "harbour: Moorings (page 9)", Ref: "page:9", Text: fmt.Sprintf("From the person's knowledge store, for question %d: the visitors' moorings are on the east wall.", n)},
			},
		}
	}

	var prev seam.Request
	var prevRec Record
	for n := 1; n <= 6; n++ {
		in := input(n)
		req, rec := build(t, in)
		fits(t, in, req, rec)

		// The tools by name, and the record of where the points are.
		var names []string
		for _, tl := range req.Tools {
			names = append(names, tl.Name)
		}
		if want := []string{"date_time", "knowledge_search", "timetable"}; !reflect.DeepEqual(names, want) {
			t.Fatalf("call %d offers the tools in the order %q, not by name", n, names)
		}
		if req.Cache == nil || !reflect.DeepEqual(rec.Cache, req.Cache) {
			t.Fatalf("call %d: the record's points %+v are not the request's %+v", n, rec.Cache, req.Cache)
		}
		if req.Cache.Start != 3 || req.Cache.History != 2*(n-1) {
			t.Fatalf("call %d: the points are %+v, not the three blocks of the standing start and %d earlier messages", n, *req.Cache, 2*(n-1))
		}
		want := []Part{PartTools, PartRules, PartProfile, PartPersonMap, PartSummary, PartRecent, PartRecall, PartCurrent}
		if n == 1 {
			want = slices.DeleteFunc(slices.Clone(want), func(p Part) bool { return p == PartRecent })
		}
		if !reflect.DeepEqual(rec.Order, want) {
			t.Fatalf("call %d: the record's order is %q, not %q", n, rec.Order, want)
		}

		// Recall is in the current message, before the person's words, and
		// nowhere else.
		cur := req.Messages[req.Cache.History]
		if len(cur.Blocks) != 3 || cur.Blocks[0].Text != in.Recall[0].Text || cur.Blocks[1].Text != in.Recall[1].Text ||
			!reflect.DeepEqual(cur.Blocks[2], in.Messages[len(in.Messages)-1].Blocks[0]) {
			t.Fatalf("call %d: the current message is %+v", n, cur.Blocks)
		}
		if strings.Contains(asJSON(t, req.System), "knowledge store, for question") ||
			strings.Contains(asJSON(t, req.Messages[:req.Cache.History]), "knowledge store, for question") {
			t.Fatalf("call %d: recall is in the system text or the history", n)
		}
		if !reflect.DeepEqual(in.Messages, grown(n)) {
			t.Fatalf("call %d: building the call changed the branch it was given", n)
		}

		if n > 1 {
			// What the call before sent is sent again, byte for byte: the
			// tools, the whole system text, and its history.
			if asJSON(t, req.Tools) != asJSON(t, prev.Tools) {
				t.Fatalf("call %d: the tools changed", n)
			}
			if asJSON(t, req.System) != asJSON(t, prev.System) {
				t.Fatalf("call %d: the system text changed:\n%s\n%s", n, asJSON(t, prev.System), asJSON(t, req.System))
			}
			h := prev.Cache.History
			if asJSON(t, req.Messages[:h]) != asJSON(t, prev.Messages[:h]) {
				t.Fatalf("call %d: the history the call before sent is not sent again in the same bytes", n)
			}
			// The person's message of the call before is now history, and
			// goes as it was written, with no recall in it.
			if asJSON(t, req.Messages[h]) != asJSON(t, seam.Message{Role: seam.RoleUser, Blocks: prev.Messages[h].Blocks[2:]}) {
				t.Fatalf("call %d: the message before is not history as the person wrote it: %+v", n, req.Messages[h])
			}
			if prevRec.Cache.Start != rec.Cache.Start {
				t.Fatalf("call %d: the standing start moved", n)
			}
		}
		prev, prevRec = req, rec
	}
}

// A turn of tool steps: each step of the turn sends the same history, and
// the same first message of the turn with its recall, so the next step can
// read what the step before wrote.
func TestEveryStepOfATurnSendsTheSameStart(t *testing.T) {
	o, b := shipped(t)
	branch := grown(4)
	in := Input{
		Model: model(200000, 4000), Budget: b, Options: o, Messages: branch,
		Tools:  []seam.Tool{tool("timetable")},
		Recall: []Item{{Label: "harbour: Tide table (page 4)", Text: "From the person's knowledge store: high water is at six."}},
	}
	one, _ := build(t, in)
	in.Messages = append(slices.Clone(branch),
		seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockToolCall, ID: "call-1", Name: "timetable", Input: json.RawMessage(`{"pier":"north"}`)}}},
		seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockToolResult, ID: "call-1", Content: []seam.Block{{Type: seam.BlockText, Text: "Seven, and then every hour."}}}}},
	)
	two, rec := build(t, in)
	if *two.Cache != *one.Cache || rec.Part(PartCurrent).Kept != 1 {
		t.Fatalf("the second step's points are %+v, and the first's %+v", *two.Cache, *one.Cache)
	}
	upTo := one.Cache.History + 1 // the history and the turn's first message, recall and all
	if asJSON(t, two.Messages[:upTo]) != asJSON(t, one.Messages[:upTo]) || asJSON(t, two.System) != asJSON(t, one.System) {
		t.Fatal("the second step does not send what the first sent, in the same bytes")
	}
	if len(two.Messages) != upTo+2 {
		t.Fatalf("the second step sends %d messages, not %d", len(two.Messages), upTo+2)
	}
}

// When turns are left out the history mark still ends at the current turn,
// and the note goes after the standing start, where it does not move it.
func TestThePointsWhenTurnsAreLeftOut(t *testing.T) {
	o, b := shipped(t)
	in := Input{Model: model(3000, 500), Budget: b, Options: o, Messages: chat(60, 300), Profile: "The person keeps a bakery."}
	req, rec := build(t, in)
	fits(t, in, req, rec)
	if _, dropped := keptTurns(rec); dropped == 0 {
		t.Fatal("the chat was meant to lose turns")
	}
	if req.Cache.Start != 2 || req.System[len(req.System)-1].Text != o.DroppedNote || len(req.System) != 3 {
		t.Fatalf("the points are %+v and the system blocks %+v", *req.Cache, req.System)
	}
	last := rec.Turns[len(rec.Turns)-1]
	if got := len(req.Messages) - last.Messages; req.Cache.History != got {
		t.Fatalf("the history mark is at %d messages, and the current turn starts at %d", req.Cache.History, got)
	}
	// A call with nothing but the current message has no history, and no
	// standing start when the rules are empty.
	o.SystemText = ""
	req, _ = build(t, Input{Model: model(4000, 500), Budget: b, Options: o, Messages: grown(1)})
	if *req.Cache != (seam.Cache{}) {
		t.Fatalf("a first message with no rules has the points %+v", *req.Cache)
	}
}

// A tool added to a set moves no other: the tools before it in the order
// keep their place and their bytes.
func TestANewToolMovesNoOther(t *testing.T) {
	before := ByName([]seam.Tool{tool("web_search"), tool("date_time")})
	after := ByName([]seam.Tool{tool("web_search"), tool("mcp_calendar_list"), tool("date_time")})
	if len(after) != 3 || after[0].Name != "date_time" || after[1].Name != "mcp_calendar_list" || after[2].Name != "web_search" ||
		!reflect.DeepEqual(before[0], after[0]) {
		t.Fatalf("the tools are %+v and then %+v", before, after)
	}
	given := []seam.Tool{tool("b"), tool("a")}
	if ByName(given); given[0].Name != "b" {
		t.Fatal("sorting the tools changed the caller's slice")
	}
	if ByName(nil) != nil {
		t.Fatal("no tools came back as some")
	}
}
