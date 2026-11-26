package artifacts

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/approval"
)

// resultText is the text a tool's result block holds.
func resultText(r tools.Result) string {
	var b strings.Builder
	for _, c := range r.Block.Content {
		if c.Type == seam.BlockText {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// The words the chat screen reads to draw the artifact's card.
var cardWords = regexp.MustCompile(`\(artifact ([0-9a-f]{16}), version (\d+)`)

func TestTheToolsMakeChangeAndReadThroughTheRegistryAndAreRecorded(t *testing.T) {
	_, s, other := stores(t)
	ctx := context.Background()
	reg := tools.New()
	if err := Add(reg, testOptions()); err != nil {
		t.Fatal(err)
	}
	chatID := newChat(t, s)
	run := func(store interface{}, name string, args any, chat string) tools.Result {
		t.Helper()
		raw, _ := json.Marshal(args)
		st := s
		if store != nil {
			st = other
		}
		return reg.Run(ctx, tools.Call{Store: st, RequestID: "req-tool", Actor: "person", ChatID: chat, ID: "call-" + name, Name: name, Input: raw})
	}

	made := run(nil, CreateName, map[string]string{"type": "react", "title": "Tide clock", "content": "export default function App() { return <p>high water</p> }"}, chatID)
	m := cardWords.FindStringSubmatch(resultText(made))
	if made.Status != tools.StatusOK || m == nil || m[2] != "1" {
		t.Fatalf("create_artifact gave %s: %q", made.Status, resultText(made))
	}
	id := m[1]
	if a, err := Get(ctx, s, id); err != nil || a.Type != KindReact || a.ChatID != chatID {
		t.Fatalf("the made artifact reads %+v (%v)", a, err)
	}
	if v, _, err := Content(ctx, s, id, 1); err != nil || v.RequestID != "" && v.RequestID != "req-tool" {
		t.Errorf("the version's request is %q (%v)", v.RequestID, err)
	}

	changed := run(nil, UpdateName, map[string]string{"artifact_id": id, "old_text": "high water", "new_text": "low water"}, chatID)
	if m := cardWords.FindStringSubmatch(resultText(changed)); changed.Status != tools.StatusOK || m == nil || m[2] != "2" {
		t.Fatalf("update_artifact gave %s: %q", changed.Status, resultText(changed))
	}
	read := run(nil, ReadName, map[string]any{"artifact_id": id}, chatID)
	if !strings.Contains(resultText(read), "low water") || !strings.Contains(resultText(read), "version 2 of 2") {
		t.Errorf("read_artifact gave %q", resultText(read))
	}
	old := run(nil, ReadName, map[string]any{"artifact_id": id, "version": 1}, chatID)
	if !strings.Contains(resultText(old), "high water") {
		t.Errorf("read_artifact of version 1 gave %q", resultText(old))
	}
	listed := run(nil, ReadName, map[string]any{}, chatID)
	if !strings.Contains(resultText(listed), "artifact "+id) {
		t.Errorf("read_artifact with no id gave %q", resultText(listed))
	}

	// A refusal is an error result the model can act on, and keeps nothing.
	bad := run(nil, CreateName, map[string]string{"type": "spreadsheet", "title": "Sums", "content": "1"}, chatID)
	if bad.Status != tools.StatusError || !strings.Contains(resultText(bad), "not an artifact's type") {
		t.Errorf("a bad type gave %s: %q", bad.Status, resultText(bad))
	}
	missing := run(nil, UpdateName, map[string]string{"artifact_id": id, "old_text": "midnight", "new_text": "x"}, chatID)
	if missing.Status != tools.StatusError || !strings.Contains(resultText(missing), "does not appear") {
		t.Errorf("old text that is not there gave %s: %q", missing.Status, resultText(missing))
	}

	// Another chat of the same person, and another person, reach nothing.
	elsewhere := newChat(t, s)
	for _, r := range []tools.Result{
		run(nil, UpdateName, map[string]string{"artifact_id": id, "content": "x"}, elsewhere),
		run(nil, ReadName, map[string]any{"artifact_id": id}, elsewhere),
		run(other, ReadName, map[string]any{"artifact_id": id}, chatID),
	} {
		if r.Status != tools.StatusError || strings.Contains(resultText(r), "water") {
			t.Errorf("a call from elsewhere gave %s: %q", r.Status, resultText(r))
		}
	}

	// Fixed rule 4: every call is in the audit log before and after.
	entries, err := audit.List(ctx, s, audit.Filter{RequestID: "req-tool", Kind: audit.KindTool})
	if err != nil {
		t.Fatal(err)
	}
	started, ended := 0, 0
	for _, e := range entries {
		if e.Outcome == audit.OutcomeStarted {
			started++
		} else {
			ended++
		}
	}
	if started != 9 || ended != 9 {
		t.Errorf("the audit log holds %d starts and %d ends of the nine calls in this person's log", started, ended)
	}
}

// Decision 0420: the tools change nothing outside Keystone, so the
// approval rule runs them without asking, after outside content too,
// unless the person sets one to ask or never.
func TestTheToolsRunWithoutAskingUnlessThePersonSetsOtherwise(t *testing.T) {
	for _, tool := range Tools(testOptions()) {
		if !tool.ReadOnly || !tool.Inside || tool.ReachesOthers != nil {
			t.Errorf("%s: read only %v, inside %v", tool.Name, tool.ReadOnly, tool.Inside)
		}
		for _, outside := range []bool{false, true} {
			if d := approval.Decide(approval.Call{Tool: tool.Name, Input: []byte(`{}`), ReadOnly: tool.ReadOnly, Outside: outside}); d.Verdict != approval.VerdictRun {
				t.Errorf("%s, outside %v: %s, want run", tool.Name, outside, d.Verdict)
			}
		}
		if d := approval.Decide(approval.Call{Tool: tool.Name, ReadOnly: tool.ReadOnly, Policy: approval.Ask}); d.Verdict != approval.VerdictAsk {
			t.Errorf("%s set to ask: %s", tool.Name, d.Verdict)
		}
		if d := approval.Decide(approval.Call{Tool: tool.Name, ReadOnly: tool.ReadOnly, Policy: approval.Never}); d.Verdict != approval.VerdictRefuse {
			t.Errorf("%s set to never: %s", tool.Name, d.Verdict)
		}
	}
	if err := Add(tools.New(), Options{}); err == nil {
		t.Error("the tools were added with options no artifact could be kept under")
	}
}
