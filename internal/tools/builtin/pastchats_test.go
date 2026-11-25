package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

// Every chat, name and fact in these tests is invented.

// recapOf gives a chat a recap, as the job after its turns would.
func (g *rig) recapOf(s *tenant.Store, chatID string, r recap.Recap) {
	g.t.Helper()
	r.ChatID = chatID
	if r.Model == "" {
		r.Model = "house"
	}
	if _, err := recap.Save(context.Background(), s, r); err != nil {
		g.t.Fatal(err)
	}
}

// hold marks a chat as recall marks one that was sent content from a
// sensitive space.
func (g *rig) hold(s *tenant.Store, chatID string) {
	g.t.Helper()
	if _, err := s.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES(?, ?, 'done', 'req-1', 'person', '{}', '2026-11-25T10:00:00.000Z')", "turn-"+chatID, chatID); err != nil {
		g.t.Fatal(err)
	}
	if err := recall.Hold(context.Background(), s, "turn-"+chatID, chatID); err != nil {
		g.t.Fatal(err)
	}
}

// runOn runs one of a list of tools, as a turn runs the ones it made for
// its call.
func runOn(t *testing.T, list []tools.Tool, s *tenant.Store, chatID, name, input string) (string, tools.Status) {
	t.Helper()
	reg := tools.New()
	for _, tl := range list {
		if err := reg.Add(tl); err != nil {
			t.Fatal(err)
		}
	}
	res := reg.Run(context.Background(), tools.Call{Store: s, RequestID: "req-1", Actor: "person", ChatID: chatID, ID: "call_1", Name: name, Input: json.RawMessage(input)})
	return seam.Text(seam.Message{Blocks: res.Block.Content}), res.Status
}

// The search finds a chat by its recap and gives the recap, ranks a chat
// that matches in more places first, and keeps to the limits.
func TestSearchChatsGivesEachChatsRecap(t *testing.T) {
	g := newRig(t, 40)
	walk := g.chat(g.one, "Planning the lighthouse walk", "What should I bring to the lighthouse?", "A windproof jacket and water.", "And the ferry back?", "The last ferry is at six.")
	ferry := g.chat(g.one, "North ferry times", "When does the north ferry leave on Tuesday?", "At 09:00 and at 11:30.")
	g.chat(g.one, "A name for the bakery", "Suggest names for a bakery.", "Tide and Crumb.")
	here := g.chat(g.one, "Today", "What did we settle?")
	g.recapOf(g.one, ferry, recap.Recap{Summary: "The north ferry's sailings on Tuesday.", Topics: []string{"timetables", "the quay"}, Decisions: []string{"Take the 09:00 sailing."}})

	// A word that is only in the recap.
	text, status := g.run(g.one, here, SearchChats, `{"query":"timetables"}`)
	if status != tools.StatusOK || !strings.HasPrefix(text, "1 match for") {
		t.Fatalf("the search by a topic came back %s: %q", status, text)
	}
	for _, want := range []string{`"North ferry times"`, ferry, "Summary: The north ferry's sailings on Tuesday.", "Topics: timetables; the quay", "Decisions:\n- Take the 09:00 sailing."} {
		if !strings.Contains(text, want) {
			t.Errorf("the search does not hold %q:\n%s", want, text)
		}
	}

	// A chat matching in its title, its recap and a message comes before
	// one matching in messages alone.
	text, _ = g.run(g.one, here, SearchChats, `{"query":"ferry"}`)
	if !strings.HasPrefix(text, "2 matches for") || strings.Index(text, ferry) > strings.Index(text, walk) {
		t.Fatalf("the search for a word in a title, a recap and messages gave:\n%s", text)
	}
	// Any of the words: both chats, each with at most two passages.
	text, _ = g.run(g.one, here, SearchChats, `{"query":"ferry lighthouse"}`)
	if !strings.HasPrefix(text, "2 matches for") || !strings.Contains(text, ferry) || !strings.Contains(text, walk) {
		t.Fatalf("the search for any of the words gave:\n%s", text)
	}
	if strings.Contains(text, "bakery") || strings.Contains(text, here) {
		t.Errorf("a chat that does not match, or the chat the turn is in, was given:\n%s", text)
	}
	if n := strings.Count(text, "\n- The "); n > 4 {
		t.Errorf("%d passages were given, want at most two for each chat:\n%s", n, text)
	}
	if text, _ := g.run(g.one, here, SearchChats, `{"query":"ferry lighthouse","limit":1}`); !strings.HasPrefix(text, "1 match for") {
		t.Errorf("a limit of one gave:\n%s", text)
	}
	for _, input := range []string{`{"query":"ferry","limit":0}`, `{"query":"ferry","limit":"two"}`, `{"query":"ferry","limit":1.5}`} {
		if text, status := g.run(g.one, here, SearchChats, input); status != tools.StatusError || !strings.Contains(text, `"limit"`) {
			t.Errorf("search_chats with %s came back %s: %q", input, status, text)
		}
	}

	// read_chat gives the recap too.
	if text, _ := g.run(g.one, here, ReadChat, `{"chat_id":"`+ferry+`"}`); !strings.Contains(text, "Summary: The north ferry's sailings on Tuesday.") || !strings.Contains(text, "The reply: At 09:00 and at 11:30.") {
		t.Errorf("read_chat did not give the recap and the messages:\n%s", text)
	}
}

// Fixed rule 3: a chat that was sent sensitive content, or whose recap
// was made while it was, is left out for a call that may reach a model
// that may not read such content, and only counted. For a call whose every
// model may, it is given, and the chat the call is in is held first.
func TestAHeldChatReachesOnlyACallThatMayReadIt(t *testing.T) {
	g := newRig(t, 40)
	held := g.chat(g.one, "Sleep notes", "What helped with the insomnia?", "Melatonin, in March.")
	marked := g.chat(g.one, "The clinic letter", "What did the letter say about sleep?", "A review in May.")
	plain := g.chat(g.one, "Sleep and the garden", "Does lavender help with sleep?", "Some find it does.")
	here := g.chat(g.one, "Today", "Have we talked about sleep?")
	g.hold(g.one, held)
	g.recapOf(g.one, held, recap.Recap{Summary: "Melatonin helped with the insomnia.", Sensitive: true})
	g.recapOf(g.one, marked, recap.Recap{Summary: "The clinic wrote about a sleep review.", Sensitive: true})

	// The registry's tools are made for a call that may not read it.
	text, status := g.run(g.one, here, SearchChats, `{"query":"sleep insomnia"}`)
	if status != tools.StatusOK || !strings.Contains(text, plain) || !strings.Contains(text, "2 more chats matched and are left out") {
		t.Fatalf("the search came back %s:\n%s", status, text)
	}
	for _, not := range []string{held, marked, "Melatonin", "Sleep notes", "clinic", "review"} {
		if strings.Contains(text, not) {
			t.Errorf("the search gave %q to a call that may not read it:\n%s", not, text)
		}
	}
	if text, _ := g.run(g.one, here, SearchChats, `{"query":"insomnia"}`); !strings.HasPrefix(text, `No other chat matches "insomnia" that this call may read. 1 chat matched and is left out`) || strings.Contains(text, held) {
		t.Errorf("a search that found only a held chat gave:\n%s", text)
	}
	for _, id := range []string{held, marked} {
		text, status := g.run(g.one, here, ReadChat, `{"chat_id":"`+id+`"}`)
		if status != tools.StatusError || !strings.Contains(text, "holds content from a sensitive space") || strings.Contains(text, "Melatonin") || strings.Contains(text, "Sleep notes") || strings.Contains(text, "letter") {
			t.Errorf("read_chat of a held chat came back %s: %q", status, text)
		}
	}

	// Made for a call whose every model may read it, the tools give it, and
	// hold the chat the call is in first.
	o := Options{Limits: limits, ReadChatMessages: 40, PastChats: PastChats{MaxResults: 5, Passages: 2}}
	if _, err := ChatTools(o, true, nil); err == nil {
		t.Fatal("tools that may give sensitive content were made with no way to hold the chat")
	}
	var holds []string
	list, err := ChatTools(o, true, func(_ context.Context, in tools.Input) error {
		holds = append(holds, in.ChatID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	text, status = runOn(t, list, g.one, here, SearchChats, `{"query":"sleep insomnia"}`)
	if status != tools.StatusOK || !strings.Contains(text, held) || !strings.Contains(text, "Melatonin helped") || strings.Contains(text, "left out") || len(holds) != 1 || holds[0] != here {
		t.Fatalf("the search for a call that may read it came back %s with holds %v:\n%s", status, holds, text)
	}
	if text, _ := runOn(t, list, g.one, here, SearchChats, `{"query":"lavender"}`); !strings.Contains(text, plain) || len(holds) != 1 {
		t.Errorf("a search that gave nothing sensitive held the chat: %v\n%s", holds, text)
	}
	if text, status := runOn(t, list, g.one, here, ReadChat, `{"chat_id":"`+held+`"}`); status != tools.StatusOK || !strings.Contains(text, "Melatonin, in March.") || len(holds) != 2 {
		t.Errorf("read_chat for a call that may read it came back %s with holds %v: %q", status, holds, text)
	}

	// A chat that cannot be held is not given anything sensitive.
	list, _ = ChatTools(o, true, func(context.Context, tools.Input) error { return errors.New("the database is locked") })
	for name, input := range map[string]string{SearchChats: `{"query":"insomnia"}`, ReadChat: `{"chat_id":"` + held + `"}`} {
		if text, status := runOn(t, list, g.one, here, name, input); status != tools.StatusError || strings.Contains(text, "Melatonin") || !strings.Contains(text, "could not be marked") {
			t.Errorf("%s with a hold that fails came back %s: %q", name, status, text)
		}
	}
}

func TestThePastChatSearchsLimits(t *testing.T) {
	p, err := DefaultPastChats()
	if err != nil || p.MaxResults < 1 {
		t.Fatalf("the shipped limits are %+v: %v", p, err)
	}
	for _, bad := range []PastChats{{MaxResults: 0, Passages: 1}, {MaxResults: 51, Passages: 1}, {MaxResults: 5, Passages: -1}, {MaxResults: 5, Passages: 21}} {
		if bad.Check() == nil {
			t.Errorf("%+v was taken", bad)
		}
		if _, err := ChatTools(Options{Limits: limits, ReadChatMessages: 4, PastChats: bad}, false, nil); err == nil {
			t.Errorf("tools were made with %+v", bad)
		}
	}
}
