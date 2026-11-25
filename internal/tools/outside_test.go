package tools

import (
	"testing"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

func TestTheOutsideMark(t *testing.T) {
	result := func(text string, isError bool) seam.Block {
		return seam.Block{Type: seam.BlockToolResult, ID: "c1", IsError: isError, Content: []seam.Block{{Type: seam.BlockText, Text: text}}}
	}
	marked := MarkOutside("Web search results for \"harbour\".")
	cases := []struct {
		name string
		b    seam.Block
		want bool
	}{
		{"a marked result", result(marked, false), true},
		{"a result with no mark", result("3 matches for \"harbour\"", false), false},
		{"a marked result that is an error", result(marked, true), false},
		{"the mark later in the text", result("Chat one: "+OutsideMark, false), false},
		{"a text block with the mark", seam.Block{Type: seam.BlockText, Text: marked}, false},
		{"a result with no text", seam.Block{Type: seam.BlockToolResult, ID: "c1"}, false},
	}
	for _, c := range cases {
		if got := IsOutside(c.b); got != c.want {
			t.Errorf("%s: IsOutside is %v, want %v", c.name, got, c.want)
		}
	}
	inside := []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{result("3 matches", false)}}}
	if ReadOutside(inside) {
		t.Error("messages with no marked result have read outside content")
	}
	if !ReadOutside(append(inside, seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{result(marked, false)}})) {
		t.Error("messages with a marked result have not read outside content")
	}
}
