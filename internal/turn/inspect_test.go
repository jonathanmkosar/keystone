package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/inspector"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// The turn inspector (P5-04). Every page, message and name here is
// invented.

// serveInspector starts a server as serveTools does, with the inspector's
// routes beside the others.
func (g *rig) serveInspector(provider seam.Provider, reg *tools.Registry) *served {
	g.t.Helper()
	sv := g.serveTools(provider, reg, nil)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute},
		append(append(Routes(sv.runner), ApprovalRoutes(sv.runner)...), InspectorRoutes(sv.runner)...))
	if err != nil {
		g.t.Fatal(err)
	}
	sv.server.Close()
	sv.server = httptest.NewServer(h)
	return sv
}

// inspectTurn reads one turn through the inspector's route.
func (sv *served) inspectTurn(tenantID, chatID, turnID string) InspectReply {
	sv.g.t.Helper()
	var got InspectReply
	sv.call(tenantID, "GET", "chats/"+chatID+"/turns/"+turnID+"/inspect", "", http.StatusOK, &got)
	return got
}

// totalsAreTheRecords is the item's first proof for one turn: the
// inspector's totals equal the sum of the turn's usage records, every
// record is in exactly one call, and each call's total is its records'.
func totalsAreTheRecords(t *testing.T, g *rig, tenantID, requestID string, got InspectReply) {
	t.Helper()
	var records []usage.Record
	g.with(tenantID, func(s *tenant.Store) {
		var err error
		if records, err = usage.List(context.Background(), s, usage.Filter{RequestID: requestID}); err != nil {
			t.Fatal(err)
		}
	})
	if len(records) == 0 {
		t.Fatal("the turn has no usage records")
	}
	var want inspector.Totals
	for _, r := range records {
		want.Calls++
		want.InputTokens += r.InputTokens
		want.OutputTokens += r.OutputTokens
		want.CacheReadTokens += r.CacheReadTokens
		want.CacheWriteTokens += r.CacheWriteTokens
		want.ThinkingTokens += r.ThinkingTokens
		want.CostMicros += r.CostMicros
		want.Estimated = want.Estimated || r.Estimated
	}
	if got.Totals != want {
		t.Errorf("the inspector's totals are %+v, and the usage records add up to %+v", got.Totals, want)
	}
	seen := map[int64]int{}
	var sum inspector.Totals
	for _, c := range got.Calls {
		for _, r := range c.Usage {
			seen[r.ID]++
		}
		if c.Total != inspector.Sum(c.Usage) {
			t.Errorf("call %d (%s) totals %+v, and its records %+v", c.Step, c.Kind, c.Total, inspector.Sum(c.Usage))
		}
		sum.Calls += c.Total.Calls
		sum.InputTokens += c.Total.InputTokens
		sum.OutputTokens += c.Total.OutputTokens
		sum.CacheReadTokens += c.Total.CacheReadTokens
		sum.CacheWriteTokens += c.Total.CacheWriteTokens
		sum.ThinkingTokens += c.Total.ThinkingTokens
		sum.CostMicros += c.Total.CostMicros
		sum.Estimated = sum.Estimated || c.Total.Estimated
	}
	for _, r := range records {
		if seen[r.ID] != 1 {
			t.Errorf("usage record %d (%s, %s) is in %d calls", r.ID, r.Kind, r.Model, seen[r.ID])
		}
	}
	if sum != want {
		t.Errorf("the calls add up to %+v, and the usage records to %+v", sum, want)
	}
}

// The item's first proof: the inspector's totals equal the usage records.
// A turn whose hosted model did not answer and whose local fallback did,
// with a title written by the same route, and a paid turn that cost
// something: each call has its parts and its records, and the totals are
// every record's.
func TestTheInspectorsTotalsAreTheUsageRecords(t *testing.T) {
	g := newRig(t)
	var paidCalls atomic.Int32
	g.extra = adapters.Set{
		models.AdapterAnthropic: func(models.Model, string) (seam.Provider, error) { return down{&paidCalls}, nil },
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) {
			return answers{chat: "Three names follow.", title: "Names for a bakery"}, nil
		},
	}
	g.addModels(paidModel, houseModel)
	g.set(tenantsettings.Person, KeyFallbackModels, houseModel.Name)
	sv := g.serveInspector(nil, nil)

	id := sv.newChat(g.ordinary.ID)
	body, _ := json.Marshal(SendBody{Text: bakery, Model: paidModel.Name})
	events, turnID, requestID := sv.send(g.ordinary.ID, id, string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	got := sv.inspectTurn(g.ordinary.ID, id, turnID)
	totalsAreTheRecords(t, g, g.ordinary.ID, requestID, got)
	if got.Turn.ID != turnID || len(got.Calls) < 2 {
		t.Fatalf("the inspection is of turn %s with %d calls: %+v", got.Turn.ID, len(got.Calls), got.Calls)
	}
	first := got.Calls[0]
	if first.Step != 1 || first.Kind != "chat" || first.Model != houseModel.Name || len(first.Usage) != 2 ||
		first.Usage[0].Model != paidModel.Name || first.Usage[0].Outcome != usage.OutcomeError || first.Usage[1].Outcome != usage.OutcomeOK {
		t.Fatalf("the chat call is %+v", first)
	}
	if first.Context == nil || first.Context.Part(contextbuild.PartRules).State != contextbuild.StateKept ||
		first.Context.Part(contextbuild.PartCurrent).Kept != 1 || len(first.Previews) != 1 || first.Previews[0] != bakery {
		t.Fatalf("the chat call's context is %+v with previews %q", first.Context, first.Previews)
	}
	if title := got.Calls[len(got.Calls)-1]; title.Kind != "title" || title.Step != 0 || title.Context != nil || len(title.Usage) == 0 {
		t.Errorf("the title call is %+v", title)
	}
	if got.Recall != nil || len(got.Summaries) != 0 || len(got.Pinned) != 0 || len(got.Skips) != 0 {
		t.Errorf("a turn with no store, summary, pin or skip shows %+v", got)
	}

	// A call that costs something: the totals carry its cost.
	g.extra[models.AdapterAnthropic] = func(models.Model, string) (seam.Provider, error) {
		return answers{chat: "From the paid model.", title: "A title"}, nil
	}
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	sv.stop()
	sv = g.serveInspector(nil, nil)
	events, turnID, requestID = sv.send(g.ordinary.ID, id, string(body))
	if end := last(t, events); end.Status != StatusDone || end.CostMicros == 0 {
		t.Fatalf("the paid turn ended as %+v", end)
	}
	got = sv.inspectTurn(g.ordinary.ID, id, turnID)
	totalsAreTheRecords(t, g, g.ordinary.ID, requestID, got)
	if got.Totals.CostMicros != got.Turn.CostMicros || got.Totals.InputTokens != got.Turn.Usage.InputTokens || len(got.Calls) != 1 {
		t.Errorf("the paid turn's totals %+v, and the turn says %d micros and %+v", got.Totals, got.Turn.CostMicros, got.Turn.Usage)
	}
	// Its earlier turn is listed, with its first words.
	if c := got.Calls[0]; len(c.Previews) != 2 || c.Previews[0] != bakery || c.Context.Turns[0].Part != contextbuild.PartRecent || !c.Context.Turns[0].Kept {
		t.Errorf("the second turn's call lists %+v with previews %q", c.Context.Turns, c.Previews)
	}

	// Another person's session reaches none of it.
	sv.call(g.test.ID, "GET", "chats/"+id+"/turns/"+turnID+"/inspect", "", http.StatusNotFound, nil)
	sv.call(g.ordinary.ID, "GET", "chats/"+id+"/turns/0000000000000000/inspect", "", http.StatusNotFound, nil)
}

// A turn of several tool steps, on the test tenant: a call for each step,
// each with what it was built from and its own record.
func TestTheInspectorShowsEachStep(t *testing.T) {
	g := newRig(t)
	sv := g.serveInspector(nil, nil)
	id := sv.newChat(g.test.ID)
	body, _ := json.Marshal(SendBody{Text: severalSteps})
	events, turnID, requestID := sv.send(g.test.ID, id, string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	got := sv.inspectTurn(g.test.ID, id, turnID)
	totalsAreTheRecords(t, g, g.test.ID, requestID, got)
	var chats []inspector.Call
	for _, c := range got.Calls {
		if c.Kind == "chat" {
			chats = append(chats, c)
		}
	}
	if len(chats) != 3 {
		t.Fatalf("%d chat calls, want 3: %+v", len(chats), got.Calls)
	}
	var input int
	for i, c := range chats {
		if c.Step != i+1 || c.Context == nil || len(c.Usage) != 1 || c.Usage[0].Outcome != usage.OutcomeOK {
			t.Errorf("chat call %d is %+v", i+1, c)
		}
		if c.Context != nil && c.Context.Part(contextbuild.PartTools).Kept != 3 {
			t.Errorf("chat call %d offered %+v", i+1, c.Context.Part(contextbuild.PartTools))
		}
		input += c.Total.InputTokens
	}
	if input != got.Turn.Usage.InputTokens {
		t.Errorf("the chat calls took %d input tokens, and the turn says %d", input, got.Turn.Usage.InputTokens)
	}
}

// The item's second proof, for a recalled page: taken out through the
// routes, it is absent from the next turn's call in the audit log, and back
// in the call of the turn after.
func TestARemovedRecallItemIsAbsentFromTheNextCall(t *testing.T) {
	g := newRig(t)
	g.addModels(houseModel)
	var mu sync.Mutex
	var got []string
	g.extra = adapters.Set{models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) {
		return kept{mu: &mu, got: &got}, nil
	}}
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	seedKnowledge(g)
	sv := g.serveInspector(nil, nil)
	id := sv.newChat(g.ordinary.ID)
	body, _ := json.Marshal(SendBody{Text: "When do the runner beans go in?"})
	const page = "north bed after the last frost"

	send := func(n int) (string, string) {
		t.Helper()
		events, turnID, requestID := sv.send(g.ordinary.ID, id, string(body))
		if end := last(t, events); end.Status != StatusDone {
			t.Fatalf("turn %d ended as %+v", n, end)
		}
		return turnID, requestID
	}
	turnID, requestID := send(1)
	if !strings.Contains(sentFor(g, requestID), page) {
		t.Fatal("the page was not recalled into the first call")
	}
	first := sv.inspectTurn(g.ordinary.ID, id, turnID)
	if first.Recall == nil || first.Recall.Kept != 1 || len(first.Recall.Hits) == 0 {
		t.Fatalf("the first turn's recall is %+v", first.Recall)
	}
	part := first.Calls[0].Context.Part(contextbuild.PartRecall)
	if len(part.Items) != 1 || !part.Items[0].Kept {
		t.Fatalf("the first call's recall part is %+v", part)
	}
	item := part.Items[0]
	var hit = first.Recall.Hits[0]
	if item.Ref != inspector.Ref(hit.Type, hit.ID) || hit.Type != inspector.KindPage || !strings.Contains(item.Label, "Runner beans") {
		t.Fatalf("the recalled item %+v does not name its hit %+v", item, hit)
	}

	var skips SkipsReply
	sv.call(g.ordinary.ID, "PUT", "chats/"+id+"/skips/page/"+hit.ID, "", http.StatusOK, &skips)
	sv.call(g.ordinary.ID, "PUT", "chats/"+id+"/skips/page/"+hit.ID, "", http.StatusOK, &skips)
	if len(skips.Skips) != 1 || skips.Skips[0].Label != item.Label || skips.Skips[0].Kind != inspector.KindPage {
		t.Fatalf("after taking the page out twice the skips are %+v", skips)
	}

	turnID, requestID = send(2)
	if call := sentFor(g, requestID); strings.Contains(call, page) {
		t.Fatalf("the page taken out of the next turn was sent:\n%s", call)
	}
	second := sv.inspectTurn(g.ordinary.ID, id, turnID)
	part = second.Calls[0].Context.Part(contextbuild.PartRecall)
	if len(part.Items) != 1 || !part.Items[0].Removed || part.Items[0].Kept || part.Removed != 1 || len(second.Skips) != 0 {
		t.Fatalf("the second call's recall part is %+v, with %d skips waiting", part, len(second.Skips))
	}

	// Exactly once: the turn after has it again.
	_, requestID = send(3)
	if !strings.Contains(sentFor(g, requestID), page) {
		t.Fatal("the page was left out of a second turn")
	}

	// Put back before the turn, it is sent.
	sv.call(g.ordinary.ID, "PUT", "chats/"+id+"/skips/page/"+hit.ID, "", http.StatusOK, nil)
	sv.call(g.ordinary.ID, "DELETE", "chats/"+id+"/skips/page/"+hit.ID, "", http.StatusOK, &skips)
	sv.call(g.ordinary.ID, "GET", "chats/"+id+"/skips", "", http.StatusOK, &skips)
	if len(skips.Skips) != 0 {
		t.Fatalf("after putting the page back the skips are %+v", skips)
	}
	_, requestID = send(4)
	if !strings.Contains(sentFor(g, requestID), page) {
		t.Fatal("a page put back was left out")
	}

	// What cannot be taken out, and whose chat it is.
	sv.call(g.ordinary.ID, "PUT", "chats/"+id+"/skips/page/999999", "", http.StatusNotFound, nil)
	sv.call(g.ordinary.ID, "PUT", "chats/"+id+"/skips/turn/7", "", http.StatusBadRequest, nil)
	sv.call(g.ordinary.ID, "PUT", "chats/"+id+"/skips/message/abc", "", http.StatusBadRequest, nil)
	sv.call(g.test.ID, "PUT", "chats/"+id+"/skips/page/"+hit.ID, "", http.StatusNotFound, nil)
	sv.call(g.test.ID, "GET", "chats/"+id+"/skips", "", http.StatusNotFound, nil)
	sv.call(g.test.ID, "DELETE", "chats/"+id+"/skips/page/"+hit.ID, "", http.StatusNotFound, nil)
}

// The item's second proof, for a pinned message sent on its own because its
// turn is left out: taken out, it is absent from the next turn's call in
// the audit log, and back in the call after. A message that is not pinned
// cannot be taken out.
func TestARemovedPinIsAbsentFromTheNextCall(t *testing.T) {
	g := newRig(t)
	g.addModels(scriptedModel)
	g.set(tenantsettings.Person, KeyCompact, "false")
	sv := g.serveInspector(nil, nil)
	id := sv.newChat(g.ordinary.ID)
	var ids []int64
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		ids = grow(t, s, id, 0, 1, 30) // about 15000 tokens, twice the window
	})
	first := ids[0]
	sv.call(g.ordinary.ID, "PUT", fmt.Sprintf("chats/%s/pins/%d", id, first), "", http.StatusOK, nil)
	const pinned = "Turn 1 from the user. harbour"

	body, _ := json.Marshal(SendBody{Text: bakery})
	send := func(n int) (string, string) {
		t.Helper()
		events, turnID, requestID := sv.send(g.ordinary.ID, id, string(body))
		if end := last(t, events); end.Status != StatusDone {
			t.Fatalf("turn %d ended as %+v", n, end)
		}
		return turnID, requestID
	}
	turnID, requestID := send(1)
	if !strings.Contains(sentFor(g, requestID), pinned) {
		t.Fatal("the pinned message was not in the first call")
	}
	got := sv.inspectTurn(g.ordinary.ID, id, turnID)
	part := got.Calls[0].Context.Part(contextbuild.PartPinned)
	if len(part.Items) != 1 || !part.Items[0].Kept || part.Items[0].InTurn || part.Items[0].Ref != messageRef(first) {
		t.Fatalf("the first call's pinned part is %+v: the pin was not sent on its own", part)
	}
	if len(got.Pinned) != 1 || got.Pinned[0] != first {
		t.Fatalf("the inspection's pins are %v", got.Pinned)
	}
	if recent := got.Calls[0].Context.Part(contextbuild.PartRecent); recent.Dropped == 0 || !strings.HasPrefix(got.Calls[0].Previews[0], "Turn 1 from the user.") {
		t.Fatalf("the earlier turns %+v, with previews beginning %q", recent, got.Calls[0].Previews[0])
	}

	var skips SkipsReply
	sv.call(g.ordinary.ID, "PUT", fmt.Sprintf("chats/%s/skips/message/%d", id, first), "", http.StatusOK, &skips)
	if len(skips.Skips) != 1 || !strings.HasPrefix(skips.Skips[0].Label, fmt.Sprintf("pinned message %d: Turn 1 from the user.", first)) {
		t.Fatalf("after taking the pin out the skips are %+v", skips)
	}
	turnID, requestID = send(2)
	if call := sentFor(g, requestID); strings.Contains(call, pinned) {
		t.Fatal("the pinned message taken out of the next turn was sent")
	}
	got = sv.inspectTurn(g.ordinary.ID, id, turnID)
	part = got.Calls[0].Context.Part(contextbuild.PartPinned)
	if len(part.Items) != 1 || !part.Items[0].Removed || part.Items[0].Kept || part.State != contextbuild.StateDropped {
		t.Fatalf("the second call's pinned part is %+v", part)
	}

	_, requestID = send(3)
	if !strings.Contains(sentFor(g, requestID), pinned) {
		t.Fatal("the pinned message was left out of a second turn")
	}

	// A message that is not pinned, or not in the chat, cannot be taken out.
	sv.call(g.ordinary.ID, "PUT", fmt.Sprintf("chats/%s/skips/message/%d", id, ids[1]), "", http.StatusBadRequest, nil)
	sv.call(g.ordinary.ID, "PUT", fmt.Sprintf("chats/%s/skips/message/%d", id, 999999), "", http.StatusNotFound, nil)
}
