package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/compact"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Compaction (P5-02). Every line of chat here is invented.

// scriptedModel is a model in the ordinary tenant's registry that the
// fake provider plays, with the shipped recorded turns: the recorded
// summary among them.
var scriptedModel = models.Model{Name: "house-scripted", Label: "House Scripted", Adapter: models.AdapterFake, ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal}

// recordedSummary is a piece of the test tenant's recorded summary, in
// fixtures/test-tenant/turns/compaction.json.
const recordedSummary = "stands in for a real summary"

// lockedBuffer is a log a test can read while the runner writes to it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// grow appends n turns of about 500 tokens each under parent, and returns
// the ids of the messages, the person's and the reply of each turn.
func grow(t *testing.T, s *tenant.Store, chatID string, parent int64, from, n int) []int64 {
	t.Helper()
	var ids []int64
	for i := from; i < from+n; i++ {
		for _, role := range []seam.Role{seam.RoleUser, seam.RoleAssistant} {
			text := fmt.Sprintf("Turn %d from the %s. %s", i, role, strings.Repeat("harbour ", 90))
			msg, err := chat.Append(s, chatID, chat.NewMessage{ParentID: parent, Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}})
			if err != nil {
				t.Fatal(err)
			}
			parent = msg.ID
			ids = append(ids, msg.ID)
		}
	}
	return ids
}

// settle waits for the chat's summary job, if one runs, to end.
func (sv *served) settle(tenantID, chatID string) {
	sv.g.t.Helper()
	key := tenantID + "/" + chatID
	for deadline := time.Now().Add(20 * time.Second); sv.runner.compaction.busy(key); {
		if time.Now().After(deadline) {
			sv.g.t.Fatal("the chat's summary did not end in 20 seconds")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// chatCall is the request a turn's first chat call sent, from the audit
// log, and what that call was built from.
func chatCall(t *testing.T, s *tenant.Store, turnID, requestID string) (string, contextbuild.Record) {
	t.Helper()
	ctx := context.Background()
	calls, err := Contexts(ctx, s, turnID)
	if err != nil || len(calls) == 0 {
		t.Fatalf("the turn's stored contexts are %+v (%v)", calls, err)
	}
	entries, err := audit.List(ctx, s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Outcome == audit.OutcomeStarted && !strings.Contains(e.Sent, "running summary of a long chat") {
			return e.Sent, calls[0].Record
		}
	}
	t.Fatalf("the turn's chat call is not in the audit log: %d entries", len(entries))
	return "", contextbuild.Record{}
}

// The item's proof: a chat ten times its model's window, with two pinned
// messages, is answered turn after turn. Every call fits, holds both
// pinned messages, and once the first summary is made carries it in place
// of the oldest turns. The summary is extended, never made again, and its
// calls are recorded with the kind summary.
func TestAChatTenTimesTheWindowIsSummarisedAndItsPinsKept(t *testing.T) {
	g := newRig(t)
	sv := g.serve(nil)
	id := sv.newChat(g.ordinary.ID)
	g.addModels(scriptedModel)
	var ids []int64
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		ids = grow(t, s, id, 0, 1, 170) // about 85000 tokens, ten times the window
	})
	first, middle := ids[0], ids[2*80+1] // the first question, and the reply of turn 81
	sv.call(g.ordinary.ID, "PUT", fmt.Sprintf("chats/%s/pins/%d", id, first), "", http.StatusOK, nil)
	var pins PinsReply
	sv.call(g.ordinary.ID, "PUT", fmt.Sprintf("chats/%s/pins/%d", id, middle), "", http.StatusOK, &pins)
	if len(pins.Pinned) != 2 || pins.Pinned[0] != first || pins.Pinned[1] != middle {
		t.Fatalf("the pins are %+v", pins)
	}
	pinnedText := []string{"Turn 1 from the user. harbour", "Turn 81 from the assistant. harbour"}

	body, _ := json.Marshal(SendBody{Text: bakery})
	summarised := false
	for n := 1; n <= 6; n++ {
		events, turnID, requestID := sv.send(g.ordinary.ID, id, string(body))
		if end := last(t, events); end.Status != StatusDone {
			t.Fatalf("turn %d ended as %+v: %s", n, end, names(events))
		}
		sv.settle(g.ordinary.ID, id)
		g.with(g.ordinary.ID, func(s *tenant.Store) {
			sent, rec := chatCall(t, s, turnID, requestID)
			if rec.Over != 0 || rec.Tokens > rec.Limit || rec.Limit > rec.Room {
				t.Fatalf("turn %d: the call does not fit: %d tokens, limit %d, room %d", n, rec.Tokens, rec.Limit, rec.Room)
			}
			for _, p := range pinnedText {
				if !strings.Contains(sent, p) {
					t.Fatalf("turn %d: the pinned message %q is not in the call", n, p)
				}
			}
			sum := rec.Part(contextbuild.PartSummary)
			if n == 1 {
				if sum.State != contextbuild.StateEmpty || rec.Part(contextbuild.PartNote).State != contextbuild.StateKept {
					t.Fatalf("the first call had no summary to send, and is %+v with the note %+v", sum, rec.Part(contextbuild.PartNote))
				}
			} else {
				if sum.State != contextbuild.StateKept || sum.Covers == 0 || !strings.Contains(sent, recordedSummary) {
					t.Fatalf("turn %d: the summary was not sent in place of the oldest turns: %+v", n, sum)
				}
				summarised = true
			}
			if rec.Part(contextbuild.PartPinned).Items[0].InTurn {
				t.Fatalf("turn %d: the first message's turn was sent, in a chat ten times the window", n)
			}
		})
	}
	if !summarised {
		t.Fatal("no call carried a summary")
	}

	g.with(g.ordinary.ID, func(s *tenant.Store) {
		ctx := context.Background()
		sums, err := compact.List(ctx, s, id)
		if err != nil || len(sums) < 2 {
			t.Fatalf("the chat has %d summaries (%v)", len(sums), err)
		}
		// One chain: the first summary extends nothing, and every later one
		// extends the one before it and stands for more.
		byID := map[int64]compact.Summary{}
		starts := 0
		for _, sum := range sums {
			byID[sum.ID] = sum
			if sum.Extends == 0 {
				starts++
			}
			if sum.FirstID != ids[0] || sum.Model != scriptedModel.Name || sum.Sensitive || sum.Text == "" {
				t.Fatalf("a summary is %+v", sum)
			}
		}
		if starts != 1 {
			t.Fatalf("%d summaries were made from the start, not one", starts)
		}
		for _, sum := range sums {
			if prev, ok := byID[sum.Extends]; sum.Extends != 0 && (!ok || prev.ThroughID >= sum.ThroughID || prev.Turns >= sum.Turns) {
				t.Fatalf("summary %d does not extend the one before it: %+v after %+v", sum.ID, sum, prev)
			}
		}
		// Each summary call has a usage record of the kind summary, and an
		// audit entry before and after.
		records, err := usage.List(ctx, s, usage.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		kinds := 0
		for _, rec := range records {
			if rec.Kind == string(routing.KindSummary) {
				kinds++
				if rec.Outcome != usage.OutcomeOK || rec.Model != scriptedModel.Name {
					t.Fatalf("a summary call's usage record is %+v", rec)
				}
			}
		}
		if kinds < len(sums) {
			t.Fatalf("%d summaries were kept and %d summary calls have a usage record", len(sums), kinds)
		}
	})

	// The pins come off.
	sv.call(g.ordinary.ID, "DELETE", fmt.Sprintf("chats/%s/pins/%d", id, first), "", http.StatusOK, &pins)
	if len(pins.Pinned) != 1 || pins.Pinned[0] != middle {
		t.Fatalf("after one pin came off the pins are %+v", pins)
	}
	sv.call(g.ordinary.ID, "GET", "chats/"+id+"/pins", "", http.StatusOK, &pins)
	if len(pins.Pinned) != 1 {
		t.Fatalf("the pins read back as %+v", pins)
	}
	sv.call(g.ordinary.ID, "PUT", "chats/"+id+"/pins/abc", "", http.StatusNotFound, nil)
	sv.call(g.ordinary.ID, "PUT", "chats/"+id+"/pins/999999", "", http.StatusNotFound, nil)
	sv.call(g.test.ID, "GET", "chats/"+id+"/pins", "", http.StatusNotFound, nil)
}

// summaryFails answers a chat and fails a summary, as a summary model that
// cannot be reached does.
type summaryFails struct {
	answers
	summaries *atomic.Int32
}

func (p summaryFails) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	if len(req.System) > 0 && strings.Contains(req.System[0].Text, "running summary of a long chat") {
		p.summaries.Add(1)
		return down{&atomic.Int32{}}.Stream(ctx, req, emit)
	}
	return p.answers.Stream(ctx, req, emit)
}

// Summarising never stops a turn: a summary that fails leaves the chat
// answered with the oldest turns left out and the note, writes one line in
// the log, and is not tried again for every turn.
func TestASummaryThatFailsLeavesTheNote(t *testing.T) {
	g := newRig(t)
	var summaries atomic.Int32
	g.extra = adapters.Set{models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) {
		return summaryFails{answers{chat: "Three names follow."}, &summaries}, nil
	}}
	sv := g.serve(nil)
	log := &lockedBuffer{}
	sv.runner.o.Log = api.NewLog(log)
	id := sv.newChat(g.ordinary.ID)
	g.addModels(houseModel)
	g.with(g.ordinary.ID, func(s *tenant.Store) { grow(t, s, id, 0, 1, 60) })

	body, _ := json.Marshal(SendBody{Text: bakery})
	for n := 1; n <= 2; n++ {
		events, turnID, requestID := sv.send(g.ordinary.ID, id, string(body))
		if end := last(t, events); end.Status != StatusDone {
			t.Fatalf("turn %d ended as %+v: %s", n, end, names(events))
		}
		sv.settle(g.ordinary.ID, id)
		g.with(g.ordinary.ID, func(s *tenant.Store) {
			_, rec := chatCall(t, s, turnID, requestID)
			if rec.Part(contextbuild.PartNote).State != contextbuild.StateKept || rec.Part(contextbuild.PartSummary).State != contextbuild.StateEmpty {
				t.Fatalf("turn %d: the chat was not sent with the note and no summary: %+v", n, rec.Parts)
			}
		})
	}
	if got := summaries.Load(); got != 1 {
		t.Fatalf("the summary was tried %d times in two turns, not once", got)
	}
	if got := strings.Count(log.String(), "were not summarised"); got != 1 {
		t.Fatalf("the failure wrote %d lines in the log:\n%s", got, log.String())
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if sums, err := compact.List(context.Background(), s, id); err != nil || len(sums) != 0 {
			t.Fatalf("a failed summary was kept: %+v (%v)", sums, err)
		}
	})
}

// Fixed rule 3: the summary of a chat that was sent sensitive content is
// made only by a model that may read it, is kept as sensitive, and is
// given to no call that may reach a model that may not read it.
func TestAHeldChatIsSummarisedByNoHostedModel(t *testing.T) {
	g := newRig(t)
	var paidCalls atomic.Int32
	g.extra = adapters.Set{
		models.AdapterAnthropic: func(models.Model, string) (seam.Provider, error) {
			return counted{answers{chat: "A summary by the paid model."}, &paidCalls}, nil
		},
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) {
			return answers{chat: "The person asked a run of questions about the harbour."}, nil
		},
	}
	sv := g.serve(nil)
	id := sv.newChat(g.ordinary.ID)
	g.addModels(houseModel, paidModel)
	g.set("person", KeySummaryModel, paidModel.Name)

	// A first turn, and then the chat is held, as recall holds a chat
	// that was sent content from a sensitive space.
	body, _ := json.Marshal(SendBody{Text: bakery})
	_, turnID, _ := sv.send(g.ordinary.ID, id, string(body))
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if err := recall.Hold(context.Background(), s, turnID, id); err != nil {
			t.Fatal(err)
		}
		path, err := chat.Path(s, id)
		if err != nil {
			t.Fatal(err)
		}
		grow(t, s, id, path[len(path)-1].ID, 1, 60)
	})
	for n := 1; n <= 2; n++ {
		events, _, _ := sv.send(g.ordinary.ID, id, string(body))
		if end := last(t, events); end.Status != StatusDone {
			t.Fatalf("turn %d ended as %+v: %s", n, end, names(events))
		}
		sv.settle(g.ordinary.ID, id)
	}
	if got := paidCalls.Load(); got != 0 {
		t.Fatalf("the hosted summary model was called %d times for a held chat", got)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		ctx := context.Background()
		sums, err := compact.List(ctx, s, id)
		if err != nil || len(sums) == 0 {
			t.Fatalf("the held chat has no summary: %v", err)
		}
		for _, sum := range sums {
			if !sum.Sensitive || sum.Model != houseModel.Name {
				t.Fatalf("a held chat's summary is %+v", sum)
			}
		}
		// A call that may reach a model that may not read sensitive content
		// is not given it.
		messages, err := sv.runner.branch(ctx, s, id, lastID(t, s, id), houseModel.Name)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := openTurns(ctx, s, id)
		if err != nil || len(tr) != 0 {
			t.Fatalf("turns are still open: %v", err)
		}
		row := row{Turn: Turn{ID: "a-later-turn", ChatID: id, UserMessageID: lastID(t, s, id)}}
		if h := sv.runner.olderHistory(ctx, s, row, houseModel, messages, true); h.summary.ID == 0 {
			t.Fatal("a call whose every model may read sensitive content was not given the summary")
		}
		if h := sv.runner.olderHistory(ctx, s, row, houseModel, messages, false); h.summary.ID != 0 || h.text != "" {
			t.Fatal("a sensitive summary was given to a call that may reach a model that may not read it")
		}
	})
}

// lastID is the id of the last message of the chat's current branch.
func lastID(t *testing.T, s *tenant.Store, chatID string) int64 {
	t.Helper()
	path, err := chat.Path(s, chatID)
	if err != nil || len(path) == 0 {
		t.Fatalf("the chat has no branch: %v", err)
	}
	return path[len(path)-1].ID
}
