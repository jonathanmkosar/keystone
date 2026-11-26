package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// The "memory screens" check (P6-07): a fact the person forgets is gone
// from their knowledge store and from recall, and an incognito chat leaves
// nothing behind.
//
// It runs the real server, with the shipped settings, on a throwaway data
// folder: the test tenant, with the knowledge of
// fixtures/test-tenant/knowledge/knowledge.json in its built-in store, the
// messages and facts of fixtures/test-tenant/knowledge/memory.json, and a
// question of fixtures/test-tenant/past-chats.json. The test tenant's
// recorded turns answer every model call. The check proves:
//
//  1. A fact the memory pass added is on the Memory screen with its chat.
//     A turn asking about it recalls its source. Forgotten through the
//     Memory screen's route, its source is gone from the store, no source
//     the store finds holds the fact, the next turn's recall does not find
//     it, and the source's other fact is kept.
//  2. Another person cannot forget it.
//  3. An incognito chat queues no memory pass, puts no source in the store
//     and is refused a recap; past-chat search from another chat leaves it
//     out while it is open; and once the person leaves it, its messages,
//     turns, events and recap are gone, and its model calls stay in the
//     audit log, as fixed rule 4 asks.
func checkMemoryScreens(root string) error {
	mf, err := readMemoryFixture(root)
	if err != nil {
		return err
	}
	pf, err := readPastChatsFixture(root)
	if err != nil {
		return err
	}
	if len(mf.Chats[0].Facts) < 2 || len(pf.Questions) == 0 {
		return errors.New("the memory fixture's first chat needs two facts, and the past chats fixture a question")
	}
	g, err := openKnowledgeRig(root, "keystone-memory-screens-", adapters.Set{})
	if err != nil {
		return err
	}
	defer g.done()
	problems, err := memoryScreensProblems(g, mf, pf)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

// sourcesHolding returns the ids of the sources a tenant's store finds for
// the words that hold the text, through the store as Keystone opens it.
func (g *knowledgeRig) sourcesHolding(tenantID, text string) ([]string, error) {
	var out []string
	err := g.with(tenantID, func(s *tenant.Store) error {
		ctx := context.Background()
		ks, err := knowledge.Open(ctx, builtin.Name, s)
		if err != nil {
			return err
		}
		defer ks.Close()
		hits, err := ks.Search(ctx, knowledge.Query{Text: text, Limit: 100})
		if err != nil {
			return err
		}
		for _, h := range hits {
			if h.Type != knowledge.HitSource {
				continue
			}
			src, err := ks.ReadSource(ctx, h.ID)
			if err != nil {
				return err
			}
			if strings.Contains(src.Text, text) {
				out = append(out, src.ID)
			}
		}
		return nil
	})
	return out, err
}

// count counts rows of a tenant's database.
func (g *knowledgeRig) count(tenantID, query string, args ...any) (int, error) {
	var n int
	err := g.with(tenantID, func(s *tenant.Store) error {
		return s.DB().QueryRow(query, args...).Scan(&n)
	})
	return n, err
}

func memoryScreensProblems(g *knowledgeRig, mf memoryFixture, pf pastChatsFixture) ([]string, error) {
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	token, rec := g.w.signIn(g.test.ID, g.testPassword)
	if token == "" {
		return nil, fmt.Errorf("the test tenant cannot sign in through the API: status %d", rec.Code)
	}
	if err := g.use(g.test.ID, builtin.Name); err != nil {
		return nil, err
	}
	forgotten, kept := mf.Chats[0].Facts[0], mf.Chats[0].Facts[1]

	// 1. A fact the memory pass added, then forgotten.
	sent, err := g.send(token, "", mf.Chats[0].Ask, "")
	if err != nil {
		return nil, err
	}
	p, err := g.waitPass(g.test.ID, sent.turnID)
	if err != nil {
		return nil, err
	}
	if p.SourceID == "" || !slices.Contains(p.Facts, forgotten) {
		return nil, fmt.Errorf("the memory pass of %q added %+v, so there is nothing to forget", mf.Chats[0].Ask, p)
	}
	var over turn.MemoryOverview
	if rec := g.w.request("GET", api.Prefix+"memory", token, "", nil); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &over) != nil {
		return nil, fmt.Errorf("the Memory screen's route answered %d", rec.Code)
	}
	listed := false
	for _, sp := range over.Spaces {
		for _, m := range sp.Memories {
			listed = listed || (m.SourceID == p.SourceID && m.ChatID == sent.chatID && slices.Contains(m.Facts, forgotten))
		}
	}
	if !listed || !over.Store.CanRemove {
		say("the Memory screen does not list the memory pass's source %s with its chat, or says the built-in store cannot remove: %+v", p.SourceID, over.Store)
	}
	if err := g.set(g.test.ID, memory.SettingOn, "false"); err != nil { // the probes add nothing
		return nil, err
	}
	probe := func() ([]string, error) {
		t, err := g.send(token, "", mf.Chats[0].Ask, "")
		if err != nil {
			return nil, err
		}
		r, ok, err := g.record(g.test.ID, t.turnID)
		if err != nil || !ok {
			return nil, fmt.Errorf("a probe turn has no recall record: %v", err)
		}
		var ids []string
		for _, h := range r.Hits {
			ids = append(ids, h.ID)
		}
		return ids, nil
	}
	before, err := probe()
	if err != nil {
		return nil, err
	}
	if !slices.Contains(before, p.SourceID) {
		return nil, fmt.Errorf("recall did not find the memory pass's source %s before it was forgotten (it found %v), so forgetting it would prove nothing", p.SourceID, before)
	}

	// 2. Another person, with a built-in store of their own, cannot forget it.
	other, err := g.in.Create("Memory Screens Other")
	if err != nil {
		return nil, err
	}
	err = g.with(other.ID, func(s *tenant.Store) error {
		o, err := signin.Defaults()
		if err != nil {
			return err
		}
		return signin.SetPassword(s, memoryPassword, o)
	})
	if err != nil {
		return nil, err
	}
	if err := g.use(other.ID, builtin.Name); err != nil {
		return nil, err
	}
	otherToken, _ := g.w.signIn(other.ID, memoryPassword)
	edit, _ := json.Marshal(turn.MemoryEdit{SourceID: p.SourceID, Fact: forgotten})
	if rec := g.w.request("POST", api.Prefix+"memory/forget", otherToken, string(edit), nil); rec.Code != http.StatusNotFound {
		say("another person's forget of the test tenant's source answered %d, want 404", rec.Code)
	}

	var ch turn.MemoryChangeReply
	if rec := g.w.request("POST", api.Prefix+"memory/forget", token, string(edit), nil); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &ch) != nil {
		return nil, fmt.Errorf("forgetting a fact answered %d: %s", rec.Code, rec.Body.String())
	}
	if !ch.Change.Removed || ch.Change.NewSourceID == "" || !slices.Equal(ch.Change.Facts, []string{kept}) {
		say("forgetting %q answered %+v", forgotten, ch)
	}
	// Read through the built-in store's own handle, past Keystone's list of
	// withdrawn sources: the built-in store removes, so the source is gone
	// from the store itself.
	err = g.with(g.test.ID, func(s *tenant.Store) error {
		ks, err := knowledge.Open(context.Background(), builtin.Name, s)
		if err != nil {
			return err
		}
		defer ks.Close()
		if _, err := knowledge.Unwrap(ks).ReadSource(context.Background(), p.SourceID); !errors.Is(err, knowledge.ErrNotFound) {
			say("the forgotten fact's source %s is still in the built-in store: %v", p.SourceID, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if holding, err := g.sourcesHolding(g.test.ID, forgotten); err != nil {
		return nil, err
	} else if len(holding) > 0 {
		say("the store still finds the forgotten fact in the sources %v", holding)
	}
	if holding, err := g.sourcesHolding(g.test.ID, kept); err != nil {
		return nil, err
	} else if !slices.Contains(holding, ch.Change.NewSourceID) {
		say("the fact that was kept is not in its new source %s: %v", ch.Change.NewSourceID, holding)
	}
	after, err := probe()
	if err != nil {
		return nil, err
	}
	if slices.Contains(after, p.SourceID) {
		say("after it was forgotten, recall still found the source %s: %v", p.SourceID, after)
	}
	if err := g.set(g.test.ID, memory.SettingOn, "true"); err != nil {
		return nil, err
	}

	// 3. An incognito chat.
	made := g.w.request("POST", api.Prefix+"chats/incognito", token, "", nil)
	var chatReply turn.ChatReply
	if made.Code != http.StatusCreated || json.Unmarshal(made.Body.Bytes(), &chatReply) != nil {
		return nil, fmt.Errorf("an incognito chat cannot be made: status %d", made.Code)
	}
	quiet := chatReply.Chat.ID
	first, err := g.send(token, quiet, mf.Chats[1].Ask, "")
	if err != nil {
		return nil, err
	}
	if _, err := g.send(token, quiet, "A note to keep to myself: the lighthouse walk at dawn by the hidden cove.", ""); err != nil {
		return nil, err
	}
	if rec := g.w.request("POST", api.Prefix+"chats/"+quiet+"/recap", token, "", nil); rec.Code != http.StatusConflict {
		say("an incognito chat's recap answered %d, want 409", rec.Code)
	}
	asked, err := g.send(token, "", pf.Questions[0].Ask, "")
	if err != nil {
		return nil, err
	}
	result, called, _ := searched(asked.stream)
	switch {
	case !called:
		say("%q was not answered through search_chats, so the incognito chat's absence proves nothing", pf.Questions[0].Ask)
	case !strings.Contains(result, pf.Questions[0].Chat):
		say("search_chats lost the chat %q: %q", pf.Questions[0].Chat, result)
	case strings.Contains(result, quiet) || strings.Contains(result, "hidden cove"):
		say("search_chats found the incognito chat: %q", result)
	}
	if n, err := g.count(g.test.ID, "SELECT count(*) FROM memory_passes WHERE chat_id = ?", quiet); err != nil {
		return nil, err
	} else if n != 0 {
		say("an incognito chat queued %d memory passes", n)
	}
	for _, f := range mf.Chats[1].Facts {
		holding, err := g.sourcesHolding(g.test.ID, f)
		if err != nil {
			return nil, err
		}
		if len(holding) > 0 {
			say("the incognito chat's fact %q is in the store, in %v", f, holding)
		}
	}

	var closed turn.IncognitoReply
	if rec := g.w.request("POST", api.Prefix+"chats/"+quiet+"/close", token, "", nil); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &closed) != nil || !closed.Deleted {
		say("leaving the incognito chat answered %d: %s", rec.Code, rec.Body.String())
	}
	if rec := g.w.request("GET", api.Prefix+"chats/"+quiet, token, "", nil); rec.Code != http.StatusNotFound {
		say("the incognito chat still opens after it was left: %d", rec.Code)
	}
	for what, query := range map[string]string{
		"messages":    "SELECT count(*) FROM messages WHERE chat_id = ?",
		"turns":       "SELECT count(*) FROM turns WHERE chat_id = ?",
		"turn events": "SELECT count(*) FROM turn_events WHERE turn_id IN (SELECT id FROM turns WHERE chat_id = ?)",
		"recaps":      "SELECT count(*) FROM chat_recaps WHERE chat_id = ?",
		"summaries":   "SELECT count(*) FROM chat_summaries WHERE chat_id = ?",
	} {
		if n, err := g.count(g.test.ID, query, quiet); err != nil {
			return nil, err
		} else if n != 0 {
			say("the incognito chat left %d rows of %s", n, what)
		}
	}
	if n, err := g.count(g.test.ID, "SELECT count(*) FROM message_search WHERE message_search MATCH 'cove'"); err != nil {
		return nil, err
	} else if n != 0 {
		say("the incognito chat's words are still in the message search index")
	}
	err = g.with(g.test.ID, func(s *tenant.Store) error {
		entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: first.requestID, Kind: audit.KindModel})
		if err == nil && len(entries) == 0 {
			say("the incognito chat's model call is not in the audit log, as fixed rule 4 asks")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return problems, nil
}
