package loadtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
)

// The generator's proof: the fast path leaves, row for row, what the real
// handler leaves for the same chats.
func TestTheFastPathLeavesWhatARealTurnLeaves(t *testing.T) {
	p, err := SmallProfile()
	if err != nil {
		t.Fatal(err)
	}
	p.Tenants = 1
	// A sample of chats on both models, one long enough that a later call
	// carries several earlier turns.
	pl := newPlanner(p, time.Now())
	var plans []chatPlan
	for i, turns := range []int{1, 3, 2, 6, 1, 4} {
		m := []models.Model{hostedModel, localModel}[i%2]
		plans = append(plans, pl.chat(0, i, 6, turns, &m))
	}
	cfgTitle := ""
	{
		g, err := openRig(p, t.TempDir(), nil)
		if err != nil {
			t.Fatal(err)
		}
		cfgTitle = g.cfg.Turns.TitlePrompt
		g.close()
	}
	g, err := openRig(p, t.TempDir(), sampleScripts(plans, cfgTitle))
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	realOne, fastOne := g.people[0], g.people[1]

	first, err := g.sendSample(realOne, plans)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tpl, err := g.templateFrom(ctx, realOne.store, first)
	if err != nil {
		t.Fatal(err)
	}
	count := &tally{}
	wrote, err := newFast(tpl, fastOne.store, count).write(ctx, plans)
	if err != nil {
		t.Fatal(err)
	}
	if len(wrote) != len(plans) || count.chats != len(plans) || count.turns != 17 || count.messages != 34 {
		t.Fatalf("the fast path wrote %d chats, and counted %d chats, %d turns and %d messages", len(wrote), count.chats, count.turns, count.messages)
	}

	diffs, err := disagreements(realOne.store, fastOne.store)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) > 0 {
		t.Fatalf("the fast path does not leave what the real handler leaves:\n%s", strings.Join(diffs, "\n"))
	}

	// The comparison is not empty-handed: it saw the tables a turn writes.
	rows, err := canonicalRows(fastOne.store)
	if err != nil {
		t.Fatal(err)
	}
	for table, least := range map[string]int{
		"chats": 6, "messages": 34, "message_search": 34, "chat_title_search": 6, "turns": 17, "turn_messages": 34,
		"turn_contexts": 17, "turn_events": 17 * 8, "chat_styles": 6, "usage_records": 17 + 3, "audit_log": 6*2 + 17*4 + 3*2,
	} {
		if len(rows[table]) < least {
			t.Errorf("the comparison saw %d rows of %s, want at least %d", len(rows[table]), table, least)
		}
	}
	// And it would see a difference: a changed word in one message.
	if _, err := fastOne.store.DB().Exec("UPDATE turn_events SET data = replace(data, 'step', 'stop') WHERE name = 'start' AND turn_id = (SELECT id FROM turns ORDER BY seq LIMIT 1)"); err != nil {
		t.Fatal(err)
	}
	if diffs, err := disagreements(realOne.store, fastOne.store); err != nil || len(diffs) != 1 || !strings.HasPrefix(diffs[0], "turn_events:") {
		t.Fatalf("a changed event was not seen as one difference in turn_events: %v, %v", diffs, err)
	}
}
