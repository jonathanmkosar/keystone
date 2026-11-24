package project

import (
	"strings"
	"testing"
)

const sample = "# P7-01. Tool loop\n\nStatus: todo\nLane: agents + interface\nDepends on: P4-03, P3-08\nOwner gate: Approval of the agent mockups.\nScope: A loop.\nProof: It loops.\n\n## Plan and result\n\nStatus: not this one\n"

func TestParseItem(t *testing.T) {
	it, err := ParseItem("P7-01-tool-loop.md", sample)
	if err != nil {
		t.Fatal(err)
	}
	if it.ID != "P7-01" || it.Title != "Tool loop" || it.Status != "todo" || it.Lane != "agents + interface" {
		t.Errorf("misread: %+v", it)
	}
	if len(it.Depends) != 2 || it.Depends[0] != "P4-03" || it.Depends[1] != "P3-08" {
		t.Errorf("dependencies misread: %v", it.Depends)
	}
	if !strings.HasPrefix(it.Gate, "Approval") {
		t.Errorf("gate misread: %q", it.Gate)
	}
	if _, err := ParseItem("x.md", "Tool loop\nStatus: todo\n"); err == nil {
		t.Error("a file with no heading was accepted")
	}
	if _, err := ParseItem("x.md", "# P1-01. Thing\n\nLane: core\n"); err == nil {
		t.Error("an item with no status was accepted")
	}
	crlf, err := ParseItem("x.md", strings.ReplaceAll(sample, "\n", "\r\n"))
	if err != nil || crlf.Status != "todo" {
		t.Errorf("Windows line endings were misread: %+v %v", crlf, err)
	}
}

func items() []Item {
	return []Item{
		{ID: "P0-01", Status: "done", Lane: "core"},
		{ID: "P0-02", Status: "todo", Lane: "core", Depends: []string{"P0-01"}},
		{ID: "P2-01", Status: "todo", Lane: "core", Depends: []string{"P0-02"}},
		{ID: "P1-02", Status: "provisional: waits for approval", Lane: "design"},
		{ID: "P1-03", Status: "doing (design)", Lane: "design"},
	}
}

func TestReady(t *testing.T) {
	got := Ready(items())
	if len(got) != 2 || got[0].ID != "P0-02" || got[1].ID != "P1-02" {
		t.Errorf("want P0-02 and P1-02 ready, got %+v", got)
	}
}

func TestCheckItems(t *testing.T) {
	if p := CheckItems(items()); len(p) != 0 {
		t.Errorf("a sound plan was faulted: %v", p)
	}
	missing := append(items(), Item{ID: "P9-01", Status: "todo", Lane: "chats", Depends: []string{"P8-99"}})
	if p := CheckItems(missing); len(p) != 1 || !strings.Contains(p[0], "does not exist") {
		t.Errorf("a missing dependency was not reported: %v", p)
	}
	loop := []Item{{ID: "P1-01", Status: "todo", Lane: "a", Depends: []string{"P1-02"}}, {ID: "P1-02", Status: "todo", Lane: "a", Depends: []string{"P1-01"}}}
	if p := CheckItems(loop); len(p) != 1 || !strings.Contains(p[0], "loop") {
		t.Errorf("a loop was not reported: %v", p)
	}
	odd := []Item{{ID: "P1-01", Status: "finished", Lane: "a"}}
	if p := CheckItems(odd); len(p) != 1 || !strings.Contains(p[0], "status") {
		t.Errorf("an unknown status was not reported: %v", p)
	}
}

func TestStateLine(t *testing.T) {
	state := "# State\n\nPhase: 1, design\nNext: P0-01\n"
	if got := StateLine(state, "Phase:"); got != "Phase: 1, design" {
		t.Errorf("got %q", got)
	}
	if got := StateLine(state, "Blocked:"); !strings.Contains(got, "missing") {
		t.Errorf("a missing line was not flagged: %q", got)
	}
}
