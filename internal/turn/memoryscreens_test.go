package turn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/personmap"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Every person, place and fact in these tests is invented.

// serveScreens starts a server as serve does, with the chat, memory,
// Memory screen, incognito and recap routes.
func (g *rig) serveScreens() *served {
	g.t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 50 * time.Millisecond
	provider, err := testtenant.Provider(g.fx, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	set, err := adapters.Fake(g.fx.Scripts, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	runner := NewRunner(Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Adapters: set})
	runner.Recover()
	routes := append(Routes(runner), MemoryRoutes(runner)...)
	routes = append(routes, MemoryScreenRoutes(runner)...)
	routes = append(routes, IncognitoRoutes(runner)...)
	routes = append(routes, RecapRoutes(runner)...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, routes)
	if err != nil {
		g.t.Fatal(err)
	}
	sv := &served{g: g, runner: runner, server: httptest.NewServer(h), tokens: map[string]string{}}
	g.t.Cleanup(sv.stop)
	for id, password := range map[string]string{g.test.ID: g.fx.Password, g.ordinary.ID: ordinaryPassword} {
		g.with(id, func(s *tenant.Store) {
			token, _, err := signin.SignIn(s, password, g.signIn)
			if err != nil {
				g.t.Fatal(err)
			}
			sv.tokens[id] = token
		})
	}
	return sv
}

// recalled returns the ids of the hits a turn's recall found.
func recalled(g *rig, tenantID, turnID string) []string {
	g.t.Helper()
	var ids []string
	g.with(tenantID, func(s *tenant.Store) {
		rec, ok, err := recall.Saved(context.Background(), s, turnID)
		if err != nil || !ok {
			g.t.Fatalf("turn %s has no recall record: %v, %v", turnID, ok, err)
		}
		for _, h := range rec.Hits {
			ids = append(ids, h.ID)
		}
	})
	return ids
}

// The item's proof in the test tenant with the built-in store: what the
// memory pass added is on the Memory screen, by space and with its chat; a
// forgotten fact is gone from the store and from recall, the rest stay,
// and a profile line that said it goes too; a correction replaces a fact.
func TestAForgottenFactIsGoneFromTheStoreAndFromRecall(t *testing.T) {
	g := newRig(t)
	g.setFor(g.test.ID, tenantsettings.Person, knowledge.SettingAdapter, builtin.Name)
	sv := g.serveScreens()
	forgotten := "The person keeps two beehives at the allotment."
	kept := "The person looks in on the beehives every Sunday morning."

	id := sv.newChat(g.test.ID)
	body, _ := json.Marshal(SendBody{Text: beesAsk})
	_, turnID, _ := sv.send(g.test.ID, id, string(body))
	p := waitPass(g, g.test.ID, turnID)
	if p.SourceID == "" {
		t.Fatalf("the memory pass added nothing: %+v", p)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		if _, err := personmap.SetProfile(context.Background(), s, "I live near the coast.\n- "+forgotten, 2000); err != nil {
			t.Fatal(err)
		}
	})

	var over MemoryOverview
	sv.call(g.test.ID, "GET", "memory", "", http.StatusOK, &over)
	if over.Store.Adapter != builtin.Name || !over.Store.CanRemove || !over.Store.CanAdd || over.Store.Note == "" || !strings.Contains(over.Profile, "coast") {
		t.Errorf("the overview's store and profile are %+v, %q", over.Store, over.Profile)
	}
	at := slices.IndexFunc(over.Spaces, func(sp MemorySpace) bool { return sp.Name == "memory" })
	if at < 0 || len(over.Spaces[at].Memories) != 1 || over.Spaces[at].Sensitive {
		t.Fatalf("the overview's spaces are %+v", over.Spaces)
	}
	m := over.Spaces[at].Memories[0]
	if m.SourceID != p.SourceID || m.ChatID != id || m.TurnID != turnID || m.ChatGone || strings.Join(m.Facts, "|") != forgotten+"|"+kept {
		t.Errorf("the memory is %+v", m)
	}
	// The store's pages are listed by space, with the space's label.
	g.with(g.test.ID, func(s *tenant.Store) {
		if _, err := builtin.WritePage(context.Background(), s, builtin.NewPage{Space: "health", Path: "sleep", Title: "Sleep notes", Body: "Sleep was poor in March."}); err != nil {
			t.Fatal(err)
		}
		if err := builtin.SetLabel(context.Background(), s, "health", knowledge.LabelSensitive); err != nil {
			t.Fatal(err)
		}
	})
	sv.call(g.test.ID, "GET", "memory", "", http.StatusOK, &over)
	if hs := slices.IndexFunc(over.Spaces, func(sp MemorySpace) bool { return sp.Name == "health" }); hs < 0 || !over.Spaces[hs].Sensitive ||
		len(over.Spaces[hs].Pages) != 1 || over.Spaces[hs].Pages[0].Title != "Sleep notes" {
		t.Errorf("the sensitive space is listed as %+v", over.Spaces)
	}

	// Before: a turn asking about the beehives recalls the source.
	g.setFor(g.test.ID, tenantsettings.Person, memory.SettingOn, "false")
	probe := func() []string {
		chatID := sv.newChat(g.test.ID)
		_, probeTurn, _ := sv.send(g.test.ID, chatID, string(body))
		return recalled(g, g.test.ID, probeTurn)
	}
	if hits := probe(); !slices.Contains(hits, p.SourceID) {
		t.Fatalf("before forgetting, recall did not find the memory source %s: %v, so the proof proves nothing", p.SourceID, hits)
	}

	// Another person, with a store of the same kind, cannot forget it.
	g.setFor(g.ordinary.ID, tenantsettings.Person, knowledge.SettingAdapter, builtin.Name)
	edit, _ := json.Marshal(MemoryEdit{SourceID: p.SourceID, Fact: forgotten})
	sv.call(g.ordinary.ID, "POST", "memory/forget", string(edit), http.StatusNotFound, nil)

	var ch MemoryChangeReply
	sv.call(g.test.ID, "POST", "memory/forget", string(edit), http.StatusOK, &ch)
	if !ch.Change.Removed || ch.Change.NewSourceID == "" || strings.Join(ch.Change.Facts, "|") != kept || !ch.ProfileChanged || ch.Note == "" {
		t.Errorf("forgetting answered %+v", ch)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		ks, err := knowledge.Open(context.Background(), builtin.Name, s)
		if err != nil {
			t.Fatal(err)
		}
		defer ks.Close()
		if _, err := ks.ReadSource(context.Background(), p.SourceID); err == nil {
			t.Error("the forgotten source is still in the store")
		}
		hits, err := ks.Search(context.Background(), knowledge.Query{Text: "allotment"})
		if err != nil || len(hits) != 0 {
			t.Errorf("a search for the forgotten fact's word finds %+v (%v)", hits, err)
		}
		prof, _ := personmap.ReadProfile(context.Background(), s)
		if strings.Contains(prof.Text, "allotment") || !strings.Contains(prof.Text, "coast") {
			t.Errorf("the profile reads %q", prof.Text)
		}
	})
	if hits := probe(); slices.Contains(hits, p.SourceID) {
		t.Errorf("after forgetting, recall still finds the forgotten source: %v", hits)
	}
	sv.call(g.test.ID, "GET", "memory", "", http.StatusOK, &over)
	at = slices.IndexFunc(over.Spaces, func(sp MemorySpace) bool { return sp.Name == "memory" })
	if at < 0 || len(over.Spaces[at].Memories) != 1 || strings.Join(over.Spaces[at].Memories[0].Facts, "|") != kept {
		t.Errorf("after forgetting the overview's spaces are %+v", over.Spaces)
	}
	if strings.Contains(over.Map, "allotment") {
		t.Errorf("the map says the forgotten fact: %q", over.Map)
	}

	// A correction, and what is refused.
	newID := ch.Change.NewSourceID
	edit, _ = json.Marshal(MemoryEdit{SourceID: newID, Fact: kept, Text: "The person looks in on the beehives every Saturday morning."})
	sv.call(g.test.ID, "POST", "memory/correct", string(edit), http.StatusOK, &ch)
	if len(ch.Change.Facts) != 1 || !strings.Contains(ch.Change.Facts[0], "Saturday") || ch.Change.SourceID != newID {
		t.Errorf("correcting answered %+v", ch)
	}
	for _, bad := range []struct {
		path string
		body MemoryEdit
		want int
	}{
		{"memory/forget", MemoryEdit{SourceID: newID, Fact: kept}, http.StatusNotFound},
		{"memory/forget", MemoryEdit{SourceID: ch.Change.NewSourceID, Fact: "Not a fact it holds."}, http.StatusNotFound},
		{"memory/forget", MemoryEdit{SourceID: ch.Change.NewSourceID, Fact: ch.Change.Facts[0], Text: "x"}, http.StatusBadRequest},
		{"memory/correct", MemoryEdit{SourceID: ch.Change.NewSourceID, Fact: ch.Change.Facts[0]}, http.StatusBadRequest},
		{"memory/correct", MemoryEdit{SourceID: ch.Change.NewSourceID, Fact: ch.Change.Facts[0], Text: strings.Repeat("long ", 100)}, http.StatusBadRequest},
		{"memory/forget", MemoryEdit{}, http.StatusBadRequest},
	} {
		raw, _ := json.Marshal(bad.body)
		sv.call(g.test.ID, "POST", bad.path, string(raw), bad.want, nil)
	}

	// With no store there is nothing to change.
	g.setFor(g.test.ID, tenantsettings.Person, knowledge.SettingAdapter, knowledge.None)
	sv.call(g.test.ID, "GET", "memory", "", http.StatusOK, &over)
	if over.Store.CanRemove || over.Store.CanAdd || !strings.Contains(over.Store.Note, "no knowledge store") {
		t.Errorf("with no store the overview says %+v", over.Store)
	}
}

// count counts the rows of a table a chat left.
func countRows(g *rig, tenantID, query string, args ...any) int {
	g.t.Helper()
	var n int
	g.with(tenantID, func(s *tenant.Store) {
		if err := s.DB().QueryRow(query, args...).Scan(&n); err != nil {
			g.t.Fatal(err)
		}
	})
	return n
}

// The item's proof for incognito, in the test tenant with the built-in
// store: an incognito chat runs no memory pass, gets no recap, and once it
// is closed keeps no message, turn or event; its calls stay in the audit
// log and its usage rows, which hold no words.
func TestAnIncognitoChatLeavesNothingBehind(t *testing.T) {
	g := newRig(t)
	g.setFor(g.test.ID, tenantsettings.Person, knowledge.SettingAdapter, builtin.Name)
	sv := g.serveScreens()

	var made ChatReply
	sv.call(g.test.ID, "POST", "chats/incognito", "", http.StatusCreated, &made)
	id := made.Chat.ID
	var mem MemoryReply
	sv.call(g.test.ID, "GET", "chats/"+id+"/memory", "", http.StatusOK, &mem)
	if !mem.Incognito {
		t.Fatalf("a chat made incognito is not: %+v", mem)
	}
	body, _ := json.Marshal(SendBody{Text: beesAsk})
	_, turnID, requestID := sv.send(g.test.ID, id, string(body))
	sv.call(g.test.ID, "POST", "chats/"+id+"/recap", "", http.StatusConflict, nil)
	sv.call(g.test.ID, "GET", "chats/"+id+"/memory", "", http.StatusOK, &mem)
	if len(mem.Passes) != 0 {
		t.Errorf("an incognito chat queued a memory pass: %+v", mem.Passes)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		if _, ok, err := recap.Get(context.Background(), s, id); err != nil || ok {
			t.Errorf("an incognito chat has a recap: %v, %v", ok, err)
		}
	})

	// Another person cannot close it, and an ordinary chat is not closed.
	sv.call(g.ordinary.ID, "POST", "chats/"+id+"/close", "", http.StatusNotFound, nil)
	plain := sv.newChat(g.test.ID)
	sv.call(g.test.ID, "POST", "chats/"+plain+"/close", "", http.StatusConflict, nil)
	sv.call(g.test.ID, "GET", "chats/"+plain, "", http.StatusOK, nil)

	var closed IncognitoReply
	sv.call(g.test.ID, "POST", "chats/"+id+"/close", "", http.StatusOK, &closed)
	if !closed.Deleted || closed.Waiting {
		t.Errorf("closing answered %+v", closed)
	}
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusNotFound, nil)
	for what, n := range map[string]int{
		"messages":       countRows(g, g.test.ID, "SELECT count(*) FROM messages WHERE chat_id = ?", id),
		"turns":          countRows(g, g.test.ID, "SELECT count(*) FROM turns WHERE chat_id = ?", id),
		"turn events":    countRows(g, g.test.ID, "SELECT count(*) FROM turn_events WHERE turn_id = ?", turnID),
		"memory passes":  countRows(g, g.test.ID, "SELECT count(*) FROM memory_passes WHERE chat_id = ?", id),
		"recaps":         countRows(g, g.test.ID, "SELECT count(*) FROM chat_recaps WHERE chat_id = ?", id),
		"message search": countRows(g, g.test.ID, "SELECT count(*) FROM message_search WHERE message_search MATCH 'allotment'"),
	} {
		if n != 0 {
			t.Errorf("a closed incognito chat left %d rows of %s", n, what)
		}
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		records, err := usage.List(context.Background(), s, usage.Filter{RequestID: requestID})
		if err != nil || len(records) == 0 {
			t.Errorf("the closed chat's call has no usage row, which keeps tokens and cost: %v", err)
		}
		entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil || len(entries) == 0 {
			t.Errorf("the closed chat's call is not in the audit log, as fixed rule 4 asks: %v", err)
		}
	})
}

// The sweep deletes an incognito chat that was left, or idle for longer
// than knowledge.incognito_keep, and keeps one a background task still
// works for until the task ends.
func TestTheSweepDeletesIdleAndLeftIncognitoChats(t *testing.T) {
	g := newRig(t)
	sv := g.serveScreens()
	make := func() string {
		var made ChatReply
		sv.call(g.test.ID, "POST", "chats/incognito", "", http.StatusCreated, &made)
		return made.Chat.ID
	}
	idle, busy := make(), make()
	g.with(g.test.ID, func(s *tenant.Store) {
		if _, err := s.DB().Exec(`INSERT INTO jobs(id, chat_id, work_chat_id, title, task, started_by, status, request_id, started_request, actor, budget_micros, max_rounds, created_at)
			VALUES('job-1', ?, 'work-1', 'Count the hives', 'Count them', 'person', 'running', 'req-job', 'req-start', 'person', 1000000, 3, '2026-11-25T09:00:00.000Z')`, busy); err != nil {
			t.Fatal(err)
		}
	})
	if n := sv.runner.SweepIncognitoNow(time.Now()); n != 0 {
		t.Errorf("the sweep deleted %d chats that are neither left nor idle", n)
	}
	var closed IncognitoReply
	sv.call(g.test.ID, "POST", "chats/"+busy+"/close", "", http.StatusOK, &closed)
	if closed.Deleted || !closed.Waiting {
		t.Errorf("closing a chat a task works for answered %+v", closed)
	}
	if n := sv.runner.SweepIncognitoNow(time.Now().Add(25 * time.Hour)); n != 1 {
		t.Errorf("a day on, the sweep deleted %d chats, want the idle one", n)
	}
	sv.call(g.test.ID, "GET", "chats/"+idle, "", http.StatusNotFound, nil)
	sv.call(g.test.ID, "GET", "chats/"+busy, "", http.StatusOK, nil)
	g.with(g.test.ID, func(s *tenant.Store) {
		if _, err := s.DB().Exec("UPDATE jobs SET status = 'done' WHERE id = 'job-1'"); err != nil {
			t.Fatal(err)
		}
	})
	if n := sv.runner.SweepIncognitoNow(time.Now()); n != 1 {
		t.Errorf("once its task ended, the sweep deleted %d chats, want the one that was left", n)
	}
	sv.call(g.test.ID, "GET", "chats/"+busy, "", http.StatusNotFound, nil)

	// The person's setting decides how long an idle chat is kept.
	g.setFor(g.test.ID, tenantsettings.Person, memory.SettingIncognitoKeep, "10m")
	short := make()
	if n := sv.runner.SweepIncognitoNow(time.Now().Add(11 * time.Minute)); n != 1 {
		t.Errorf("with %s at 10m, eleven minutes on the sweep deleted %d chats", memory.SettingIncognitoKeep, n)
	}
	sv.call(g.test.ID, "GET", "chats/"+short, "", http.StatusNotFound, nil)
}
