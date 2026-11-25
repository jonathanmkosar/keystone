package recap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Every chat, name and fact in these tests is invented.

var options = Options{
	Prompt:          "Write the summary, topics and decisions of one chat. At most {words} words, {topics} topics and {decisions} decisions.",
	SummaryMaxWords: 40, MaxTopics: 3, MaxDecisions: 2, ItemMaxChars: 60,
	MessageMaxChars: 200, InputMaxChars: 2000, MaxOutputTokens: 500,
}

func store(t *testing.T) *tenant.Store {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newChat(t *testing.T, s *tenant.Store) string {
	t.Helper()
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func text(role seam.Role, s string) seam.Message {
	return seam.Message{Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: s}}}
}

func TestCheckOptionsNamesEachBadSetting(t *testing.T) {
	if err := CheckOptions(options); err != nil {
		t.Fatalf("good options were refused: %v", err)
	}
	for key, change := range map[string]func(*Options){
		SettingPrompt:          func(o *Options) { o.Prompt = " " },
		SettingSummaryMaxWords: func(o *Options) { o.SummaryMaxWords = 5 },
		SettingMaxTopics:       func(o *Options) { o.MaxTopics = -1 },
		SettingMaxDecisions:    func(o *Options) { o.MaxDecisions = 51 },
		SettingItemMaxChars:    func(o *Options) { o.ItemMaxChars = 10 },
		SettingMessageMaxChars: func(o *Options) { o.MessageMaxChars = 50 },
		SettingInputMaxChars:   func(o *Options) { o.InputMaxChars = 150 },
		SettingMaxOutputTokens: func(o *Options) { o.MaxOutputTokens = 10 },
	} {
		o := options
		change(&o)
		if err := CheckOptions(o); !errors.Is(err, ErrOptions) || !strings.Contains(err.Error(), key) {
			t.Errorf("a bad %s gave %v", key, err)
		}
	}
}

func TestDue(t *testing.T) {
	for _, c := range []struct {
		have      Recap
		ok        bool
		done, at  int
		want      bool
		described string
	}{
		{Recap{}, false, 0, 3, false, "a chat with no finished turn"},
		{Recap{}, false, 1, 3, true, "a chat with no recap and one finished turn"},
		{Recap{Turns: 1}, true, 3, 3, false, "two turns since, with three asked for"},
		{Recap{Turns: 1}, true, 4, 3, true, "three turns since"},
		{Recap{Turns: 4}, true, 5, 0, true, "a threshold below one counts as one"},
		{Recap{Turns: 5}, true, 5, 1, false, "no turn since"},
	} {
		if got := Due(c.have, c.ok, c.done, c.at); got != c.want {
			t.Errorf("%s: due is %v", c.described, got)
		}
	}
}

func TestTheRequestHoldsTheChat(t *testing.T) {
	messages := []seam.Message{
		text(seam.RoleUser, "Shall we walk to the old lighthouse on Saturday?"),
		{Role: seam.RoleAssistant, Blocks: []seam.Block{
			{Type: seam.BlockThinking, Text: "a private thought", Signature: "sig"},
			{Type: seam.BlockText, Text: "Let me look at the tides."},
			{Type: seam.BlockToolCall, ID: "c1", Name: "tide_table", Input: json.RawMessage(`{"day":"saturday"}`)},
		}},
		{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockToolResult, ID: "c1", Content: []seam.Block{{Type: seam.BlockText, Text: "Low water at noon."}}}}},
		text(seam.RoleAssistant, "Go at noon, at low water."),
		text(seam.RoleUser, "Then we go at noon."),
	}
	req, err := Request(Ask{Options: options, Model: "house", Title: "The lighthouse walk", Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	system, body := seam.Text(seam.Message{Blocks: req.System}), seam.Text(req.Messages[0])
	if !strings.Contains(system, "At most 40 words, 3 topics and 2 decisions.") || req.MaxOutputTokens != 500 || req.Model != "house" {
		t.Errorf("the prompt or the limits are wrong: %q, %d", system, req.MaxOutputTokens)
	}
	for _, want := range []string{
		"The chat's title: The lighthouse walk",
		"There is no recap yet.",
		"The person: Shall we walk to the old lighthouse on Saturday?",
		`The assistant: Let me look at the tides. [The assistant used the tool tide_table with {"day":"saturday"}.]`,
		"[The tool tide_table answered: Low water at noon.]",
		"The person: Then we go at noon.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the request does not hold %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "private thought") {
		t.Errorf("thinking was sent:\n%s", body)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != seam.RoleUser {
		t.Errorf("the request is %d messages", len(req.Messages))
	}

	// The recap so far, and only the messages after it.
	prev := Recap{Summary: "They planned a walk.", Topics: []string{"the lighthouse"}, Decisions: []string{"Walk on Saturday."}}
	req, err = Request(Ask{Options: options, Previous: &prev, Messages: messages[4:]})
	if err != nil {
		t.Fatal(err)
	}
	body = seam.Text(req.Messages[0])
	for _, want := range []string{"The chat has no title yet.", "The recap so far:\nSummary: They planned a walk.\nTopics: the lighthouse\nDecisions:\n- Walk on Saturday.", "The messages that follow it", "The person: Then we go at noon."} {
		if !strings.Contains(body, want) {
			t.Errorf("the request does not hold %q:\n%s", want, body)
		}
	}
	if _, err := Request(Ask{Options: options}); !errors.Is(err, ErrNothing) {
		t.Errorf("a request with no messages gave %v", err)
	}
}

func TestALongChatKeepsItsFirstMessageAndTheNewest(t *testing.T) {
	var messages []seam.Message
	for i := 0; i < 40; i++ {
		role := seam.RoleUser
		if i%2 == 1 {
			role = seam.RoleAssistant
		}
		messages = append(messages, text(role, strings.Repeat("harbour ", 10)+string(rune('a'+i%26))+"-mark-"+strings.Repeat("x", i)))
	}
	o := options
	o.InputMaxChars = 900
	req, err := Request(Ask{Options: o, Messages: messages})
	if err != nil {
		t.Fatal(err)
	}
	body := seam.Text(req.Messages[0])
	if !strings.Contains(body, "a-mark-\n") || !strings.Contains(body, "messages here are left out.") || !strings.HasSuffix(body, "n-mark-"+strings.Repeat("x", 39)) {
		t.Errorf("a long chat did not keep its first and newest messages:\n%s", body)
	}

	// A window that holds fewer takes fewer, and one that holds none of a
	// message cuts it.
	limit := len(body) / 2
	fits := func(r seam.Request) bool { return len(seam.Text(r.Messages[0])) <= limit }
	small, err := Request(Ask{Options: o, Messages: messages, Fits: fits})
	if err != nil || len(seam.Text(small.Messages[0])) > limit {
		t.Fatalf("a small window was not kept to: %v", err)
	}
	one := []seam.Message{text(seam.RoleUser, strings.Repeat("long ", 39))}
	cut, err := Request(Ask{Options: o, Messages: one, Fits: func(r seam.Request) bool { return len(seam.Text(r.Messages[0])) <= 150 }})
	if err != nil || !strings.Contains(seam.Text(cut.Messages[0]), "[The rest of this message is cut.]") {
		t.Fatalf("a message too large on its own was not cut: %v", err)
	}
	if _, err := Request(Ask{Options: o, Messages: one, Fits: func(seam.Request) bool { return false }}); !errors.Is(err, ErrNoRoom) {
		t.Errorf("no room gave %v", err)
	}
}

func reply(s string) seam.Response {
	return seam.Response{Message: text(seam.RoleAssistant, s), StopReason: seam.StopEnd}
}

func TestTheAnswerIsRead(t *testing.T) {
	got, err := Answer(reply("Here it is:\n```json\n"+`{"summary": "They  planned a walk\nto the lighthouse.", "topics": ["the lighthouse", {"text": "tides"}, "The Lighthouse", " ", "a topic far too long to be kept as one, since it goes on and on and on", "boots", "maps"], "decisions": ["Walk on Saturday.", "Go at noon.", "Bring water."]}`+"\n```"), options)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "They planned a walk to the lighthouse." || strings.Join(got.Topics, "|") != "the lighthouse|tides|boots" || strings.Join(got.Decisions, "|") != "Walk on Saturday.|Go at noon." || got.Dropped != 5 {
		t.Errorf("the answer read as %+v", got)
	}
	if got, err := Answer(reply(`{"summary": "Nothing was decided."}`), options); err != nil || len(got.Topics) != 0 || got.Decisions == nil {
		t.Errorf("an answer with no lists read as %+v, %v", got, err)
	}
	o := options
	o.SummaryMaxWords = 10
	if got, _ := Answer(reply(`{"summary": "one two three four five six seven eight nine ten eleven twelve"}`), o); got.Summary != "one two three four five six seven eight nine ten …" {
		t.Errorf("a long summary was not cut: %q", got.Summary)
	}
	for _, bad := range []string{"No recap.", `{"topics": []}`, `{"summary": "  "}`, `{"summary": "x", "topics": [3]}`, `{"summary": `} {
		if _, err := Answer(reply(bad), options); !errors.Is(err, ErrAnswer) {
			t.Errorf("%q gave %v", bad, err)
		}
	}
}

func TestRecapsAreKeptSearchedAndGoWithTheirChat(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	walk, ferry, bare := newChat(t, s), newChat(t, s), newChat(t, s)

	if _, ok, err := Get(ctx, s, walk); err != nil || ok {
		t.Fatalf("a chat with no recap has one: %v %v", ok, err)
	}
	if _, err := Save(ctx, s, Recap{ChatID: walk, Summary: " ", Model: "house"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a blank summary was kept: %v", err)
	}
	first, err := Save(ctx, s, Recap{ChatID: walk, Summary: "A walk to the old lighthouse.", Topics: []string{"cliff path"}, Decisions: []string{"Leave at dawn."}, ThroughID: 4, Turns: 1, Model: "house", Sensitive: true, RequestID: "req-1"})
	if err != nil || first.Made.IsZero() || !first.Sensitive || first.Turns != 1 {
		t.Fatalf("the recap was kept as %+v: %v", first, err)
	}
	// A newer recap takes its place, and the sensitive mark stays.
	second, err := Save(ctx, s, Recap{ChatID: walk, Summary: "A walk to the old lighthouse, at noon.", Decisions: []string{"Leave at noon."}, ThroughID: 8, Turns: 3, Model: "house"})
	if err != nil || !second.Sensitive || second.Summary != "A walk to the old lighthouse, at noon." || len(second.Topics) != 0 || second.Turns != 3 {
		t.Fatalf("the newer recap is %+v: %v", second, err)
	}
	if _, err := Save(ctx, s, Recap{ChatID: ferry, Summary: "The north ferry's times on Tuesday.", Topics: []string{"timetables"}, Model: "house"}); err != nil {
		t.Fatal(err)
	}

	sums, err := Summaries(ctx, s, []string{walk, ferry, bare})
	if err != nil || len(sums) != 2 || sums[ferry] != "The north ferry's times on Tuesday." {
		t.Fatalf("the summaries are %v: %v", sums, err)
	}
	for query, want := range map[string]string{
		"lighthouse":     walk,  // the summary
		"timetab":        ferry, // a topic, by the start of a word
		"noon":           walk,  // a decision
		"ferry or boats": ferry,
		"dawn":           "", // in a recap that was replaced
		"!!":             "", // no word at all
	} {
		hits, err := Search(ctx, s, query, 10)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case want == "" && len(hits) != 0:
			t.Errorf("%q found %v", query, hits)
		case want != "" && (len(hits) != 1 || hits[0].ChatID != want || hits[0].Score >= 0):
			t.Errorf("%q found %v, want %s", query, hits, want)
		}
	}

	// The recap and its search row go with the chat.
	if err := chat.Delete(s, walk); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.DB().QueryRow("SELECT count(*) FROM chat_recap_search").Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("%d search rows are left, want the ferry's: %v", rows, err)
	}
	if _, ok, _ := Get(ctx, s, walk); ok {
		t.Error("a deleted chat's recap is still there")
	}
	if hits, _ := Search(ctx, s, "lighthouse", 10); len(hits) != 0 {
		t.Errorf("a deleted chat is still found: %v", hits)
	}
}

func TestSensitiveAndMayShow(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	plain, held, marked := newChat(t, s), newChat(t, s), newChat(t, s)
	// A chat is held as recall holds it: a turn of it was sent sensitive
	// content.
	if _, err := s.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES('turn-1', ?, 'done', 'req-1', 'person', '{}', '2026-11-25T10:00:00.000Z')", held); err != nil {
		t.Fatal(err)
	}
	if err := recall.Hold(ctx, s, "turn-1", held); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(ctx, s, Recap{ChatID: marked, Summary: "Made while the chat was held.", Model: "house", Sensitive: true}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{plain: false, held: true, marked: true} {
		got, err := Sensitive(ctx, s, id)
		if err != nil || got != want {
			t.Errorf("chat %s: sensitive is %v (%v), want %v", id, got, err, want)
		}
	}
	if !MayShow(false, false) || !MayShow(false, true) || MayShow(true, false) || !MayShow(true, true) {
		t.Error("MayShow does not follow fixed rule 3")
	}
	// A mark that cannot be read counts as sensitive.
	gone, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, _ := gone.Create("Test Person Two")
	other, _ := gone.Store(made.ID)
	other.Close()
	if got, err := Sensitive(ctx, other, plain); err == nil || !got {
		t.Errorf("a store that cannot be read gave %v, %v", got, err)
	}
}
