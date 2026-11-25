package turn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/knowledge/waymarks"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// Every page and name in these tests is invented.

// kept is a provider that keeps the requests it is sent, and fails when
// it is down.
type kept struct {
	mu   *sync.Mutex
	got  *[]string
	down bool
}

func (p kept) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	raw, _ := seam.EncodeRequest(req)
	p.mu.Lock()
	*p.got = append(*p.got, string(raw))
	p.mu.Unlock()
	if p.down {
		return down{calls: new(atomic.Int32)}.Stream(ctx, req, emit)
	}
	return answers{chat: "A reply.", title: "A title"}.Stream(ctx, req, emit)
}

// seedKnowledge gives the ordinary tenant the built-in store with a garden
// page and a sensitive health page.
func seedKnowledge(g *rig) {
	g.t.Helper()
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		ctx := context.Background()
		for _, p := range []builtin.NewPage{
			{Space: "garden", Path: "plants/runner-beans", Title: "Runner beans", Kind: "plant", Body: "Runner beans go in the north bed after the last frost."},
			{Space: "health", Path: "notes/sleep", Title: "Sleep notes", Kind: "note", Body: "Melatonin helped with the insomnia in March."},
		} {
			if _, err := builtin.WritePage(ctx, s, p); err != nil {
				g.t.Fatal(err)
			}
		}
		if err := builtin.SetLabel(ctx, s, "health", knowledge.LabelSensitive); err != nil {
			g.t.Fatal(err)
		}
	})
	g.set(tenantsettings.Person, knowledge.SettingAdapter, builtin.Name)
}

// sentFor returns what each model call of a request was sent, as the audit
// log holds it.
func sentFor(g *rig, requestID string) string {
	g.t.Helper()
	var b strings.Builder
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil {
			g.t.Fatal(err)
		}
		for _, e := range entries {
			if e.Outcome == audit.OutcomeStarted {
				b.WriteString(e.Sent + "\n")
			}
		}
	})
	return b.String()
}

// A message about a page in the store brings it into the call, under the
// search-first rule, and the turn keeps a record of what was found. A
// message about nothing brings nothing, and the none adapter searches
// nothing.
func TestRecallFillsTheCall(t *testing.T) {
	g := newRig(t)
	g.addModels(houseModel)
	var mu sync.Mutex
	var got []string
	g.extra = adapters.Set{models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) {
		return kept{mu: &mu, got: &got}, nil
	}}
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	sv := g.serve(nil)

	// With none, nothing is searched, recorded or told.
	body, _ := json.Marshal(SendBody{Text: "When do the runner beans go in?"})
	events, turnID, requestID := sv.send(g.ordinary.ID, sv.newChat(g.ordinary.ID), string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if _, ok, err := recall.Saved(context.Background(), s, turnID); ok || err != nil {
			t.Errorf("with no store a recall was recorded (%v)", err)
		}
	})
	if call := sentFor(g, requestID); strings.Contains(call, "knowledge store") {
		t.Errorf("with no store the call speaks of one:\n%s", call)
	}

	seedKnowledge(g)
	events, turnID, requestID = sv.send(g.ordinary.ID, sv.newChat(g.ordinary.ID), string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	call := sentFor(g, requestID)
	if !strings.Contains(call, "north bed after the last frost") || !strings.Contains(call, `Page \"Runner beans\" in the space \"garden\"`) {
		t.Errorf("the page was not recalled into the call:\n%s", call)
	}
	if !strings.Contains(call, "This person keeps a knowledge store of their own.") {
		t.Errorf("the search-first rule is not in the call:\n%s", call)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		ctx := context.Background()
		rec, ok, err := recall.Saved(ctx, s, turnID)
		if err != nil || !ok || rec.Adapter != builtin.Name || rec.Kept != 1 || rec.Sensitive || rec.Query != "runner beans go" {
			t.Errorf("the turn's recall record is %+v (%v, %v)", rec, ok, err)
		}
		calls, err := Contexts(ctx, s, turnID)
		if err != nil || len(calls) != 1 {
			t.Fatalf("the turn's contexts are %+v (%v)", calls, err)
		}
		if part := calls[0].Record.Part(contextbuild.PartRecall); part.State != contextbuild.StateKept || part.Kept != 1 {
			t.Errorf("the recall part of the call is %+v", part)
		}
	})

	nothing, _ := json.Marshal(SendBody{Text: "Can you write a short poem about the sea?"})
	_, turnID, requestID = sv.send(g.ordinary.ID, sv.newChat(g.ordinary.ID), string(nothing))
	if call := sentFor(g, requestID); strings.Contains(call, "From the person's knowledge store") {
		t.Errorf("a message about nothing in the store recalled something:\n%s", call)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if rec, ok, err := recall.Saved(context.Background(), s, turnID); err != nil || !ok || rec.Found != 0 {
			t.Errorf("the record of a message about nothing: %+v (%v, %v)", rec, ok, err)
		}
	})
}

// A local model that recalled sensitive content does not fall back to a
// hosted one, and a chat that was sent sensitive content is not sent to a
// hosted model afterwards: with nothing else to go to, the turn ends with
// the code sensitive.
func TestSensitiveRecallHoldsTheRoute(t *testing.T) {
	g := newRig(t)
	var mu sync.Mutex
	var paid, house []string
	g.extra = adapters.Set{
		models.AdapterAnthropic:       func(models.Model, string) (seam.Provider, error) { return kept{mu: &mu, got: &paid}, nil },
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return kept{mu: &mu, got: &house, down: true}, nil },
	}
	g.addModels(paidModel, houseModel)
	g.set(tenantsettings.Person, KeyFallbackModels, paidModel.Name)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	seedKnowledge(g)
	sv := g.serve(nil)

	id := sv.newChat(g.ordinary.ID)
	body, _ := json.Marshal(SendBody{Text: "What helped with the insomnia?", Model: houseModel.Name})
	events, turnID, _ := sv.send(g.ordinary.ID, id, string(body))
	if end := last(t, events); end.Status != StatusFailed || end.Error == nil || end.Error.Code != string(seam.ErrUnavailable) {
		t.Errorf("the turn ended as %+v", end)
	}
	mu.Lock()
	if len(paid) != 0 || len(house) != 1 || !strings.Contains(house[0], "Melatonin") {
		t.Errorf("the hosted model was called %d times and the local one %d times", len(paid), len(house))
	}
	mu.Unlock()
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		held, err := recall.Held(context.Background(), s, id)
		if err != nil || !held {
			t.Errorf("the chat is not held (%v)", err)
		}
		if rec, ok, _ := recall.Saved(context.Background(), s, turnID); !ok || !rec.Sensitive {
			t.Errorf("the record does not say a sensitive item was kept: %+v", rec)
		}
	})

	body, _ = json.Marshal(SendBody{Text: "Say it again.", Model: paidModel.Name})
	events, _, _ = sv.send(g.ordinary.ID, id, string(body))
	if end := last(t, events); end.Status != StatusFailed || end.Error == nil || end.Error.Code != CodeSensitive {
		t.Errorf("a held chat on a hosted model ended as %+v", end)
	}
	mu.Lock()
	if len(paid) != 0 {
		t.Errorf("the hosted model was sent a held chat %d times", len(paid))
	}
	mu.Unlock()

	// A plain question in a new chat may fall back to the hosted model,
	// and its call is built for it: no sensitive space in the map.
	body, _ = json.Marshal(SendBody{Text: "When do the runner beans go in?", Model: houseModel.Name})
	events, _, _ = sv.send(g.ordinary.ID, sv.newChat(g.ordinary.ID), string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Errorf("a plain question ended as %+v", end)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paid) != 1 || !strings.Contains(paid[0], "north bed") || strings.Contains(paid[0], "health") || strings.Contains(paid[0], "Sleep notes") {
		t.Errorf("the hosted fallback was sent %d calls: %v", len(paid), paid)
	}
}

// never is a Waymarks that never answers.
func never(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return srv.URL
}

// A store that is slow, or that does not open, never stops a chat: the
// turn is answered with nothing recalled, inside the recall timeout.
func TestASlowOrMissingStoreDoesNotStopAChat(t *testing.T) {
	g := newRig(t)
	g.addModels(houseModel)
	var mu sync.Mutex
	var got []string
	g.extra = adapters.Set{models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) {
		return kept{mu: &mu, got: &got}, nil
	}}
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	g.set(tenantsettings.Person, knowledge.SettingAdapter, waymarks.Name)
	g.set(tenantsettings.Person, recall.SettingTimeout, "300ms")
	sv := g.serve(nil)
	body, _ := json.Marshal(SendBody{Text: "When do the runner beans go in?"})

	// No address and no token: the store does not open.
	events, turnID, _ := sv.send(g.ordinary.ID, sv.newChat(g.ordinary.ID), string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Errorf("with a store that does not open the turn ended as %+v", end)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if rec, ok, err := recall.Saved(context.Background(), s, turnID); err != nil || !ok || rec.Note != noteNotOpened {
			t.Errorf("the record of a store that did not open: %+v (%v, %v)", rec, ok, err)
		}
		if err := s.Secrets().Set(waymarks.SecretReader, "an-invented-token"); err != nil {
			t.Fatal(err)
		}
	})

	// A store that never answers. Recall gives up at its own timeout, well
	// before the adapter's own of five seconds.
	g.set(tenantsettings.Person, waymarks.SettingAddress, never(t))
	r := NewRunner(Options{Install: g.install})
	defer r.Close()
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		began := time.Now()
		tr := r.searchKnowledge(context.Background(), s, row{input: SendBody{Text: "When do the runner beans go in?"}})
		if took := time.Since(began); took > 2*time.Second || !tr.open || tr.note != noteNoAnswer {
			t.Errorf("a search of a store that never answers took %s and gave %+v", took, tr)
		}
	})

	// A whole turn on it is answered. The map waits for the adapter's own
	// timeout, so that is made short too.
	g.set(tenantsettings.Person, waymarks.SettingTimeout, "300ms")
	events, turnID, _ = sv.send(g.ordinary.ID, sv.newChat(g.ordinary.ID), string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Errorf("with a store that never answers the turn ended as %+v", end)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		if rec, ok, err := recall.Saved(context.Background(), s, turnID); err != nil || !ok || rec.Note != noteNoAnswer {
			t.Errorf("the record of a store that never answers: %+v (%v, %v)", rec, ok, err)
		}
	})
}

// The answer for a call is the least of its route's, and the lookups are
// filtered by it.
func TestTheLeastOfTheRouteReads(t *testing.T) {
	trusted := models.Model{Name: "trusted", Class: models.ClassTrusted}
	link := func(m models.Model) routing.Link { return routing.Link{Model: m, Provider: answers{}} }
	both, err := routing.NewChain(false, []routing.Link{link(houseModel), link(paidModel)}, routing.Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	safe, err := routing.NewChain(true, []routing.Link{link(houseModel), link(trusted)}, routing.Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		p    seam.Provider
		m    models.Model
		want bool
	}{
		"a local model alone":            {answers{}, houseModel, true},
		"a hosted model alone":           {answers{}, paidModel, false},
		"a local model before a hosted":  {both, houseModel, false},
		"a local model before a trusted": {safe, houseModel, true},
	} {
		if got := readsSensitive(c.p, c.m); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}

	g := newRig(t)
	seedKnowledge(g)
	r := NewRunner(Options{Install: g.install})
	defer r.Close()
	if err := r.setup(); err != nil {
		t.Fatal(err)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		ctx := context.Background()
		ks, err := knowledge.Open(ctx, builtin.Name, s)
		if err != nil {
			t.Fatal(err)
		}
		defer ks.Close()
		for _, c := range []struct {
			p    seam.Provider
			want bool
		}{{both, false}, {safe, true}} {
			list, err := r.knowledgeLookups(ctx, s, c.p, houseModel)
			if err != nil || len(list) != 3 {
				t.Fatalf("the lookups: %d (%v)", len(list), err)
			}
			res, err := list[0].Run(ctx, s, ks, json.RawMessage(`{"query":"melatonin"}`))
			if err != nil {
				t.Fatal(err)
			}
			if res.Sensitive != c.want || (res.Withheld > 0) == c.want {
				t.Errorf("a search for a route that may read sensitive content %v: %+v", c.want, res)
			}
		}
	})
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := settings.Lookup(reg, recall.SettingLookupResults); !ok {
		t.Error("the lookup settings are not in the registry")
	}
}
