package share

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

var bg = context.Background()

// Invented words, for throwaway folders only.
const (
	sharedWords  = "When does the harbour ferry leave on Sundays?"
	sharedReply  = "The first ferry leaves at nine."
	privateWords = "A private note about the lighthouse keys."
	otherWords   = "Tenant two's own words about the beehives."
	toolSaid     = "Search result: the ferry office opens at eight."
	thought      = "The person wants the Sunday times."
	imageRef     = "0000000000000000000000000000000000000000000000000000000000000abc"
)

func install(t *testing.T) (*tenant.Install, *tenant.Store, *tenant.Store) {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	open := func(name string) *tenant.Store {
		made, err := in.Create(name)
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
	return in, open("Test Person One"), open("Test Person Two")
}

// chatWith makes a chat titled title whose branch is the given messages,
// each answering the one before.
func chatWith(t *testing.T, s *tenant.Store, title string, msgs ...seam.Message) (string, []int64) {
	t.Helper()
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	lim, err := chat.DefaultLimits()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Rename(s, lim, c.ID, title); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	var parent int64
	for _, m := range msgs {
		got, err := chat.Append(s, c.ID, chat.NewMessage{ParentID: parent, Role: m.Role, Blocks: m.Blocks})
		if err != nil {
			t.Fatal(err)
		}
		parent = got.ID
		ids = append(ids, got.ID)
	}
	return c.ID, ids
}

func said(text string) seam.Message {
	return seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}}
}

func answered(text string) seam.Message {
	return seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}}
}

func draft() Draft { return Draft{MaxOpen: 10} }

// hold marks a chat as sent content from a sensitive space, in a turn of
// its own, as recall does.
func hold(s *tenant.Store, turnID, chatID string) error {
	if _, err := s.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES(?, ?, 'done', 'r', 'person', '{}', '2026-11-26T00:00:00.000Z')", turnID, chatID); err != nil {
		return err
	}
	return recall.Hold(bg, s, turnID, chatID)
}

func TestALinkShowsItsOwnChatAndNothingElse(t *testing.T) {
	in, one, two := install(t)
	shared, _ := chatWith(t, one, "Sunday ferry", said(sharedWords), answered(sharedReply))
	chatWith(t, one, "Lighthouse keys", said(privateWords), answered("Noted."))
	chatWith(t, two, "Beehives", said(otherWords), answered("Noted."))

	token, link, err := Create(bg, one, shared, draft())
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := TenantOf(token); !ok || id != one.TenantID() {
		t.Fatalf("the token names %q, want tenant one", id)
	}
	if link.State != StateOpen || link.View != ViewLive || link.ChatTitle != "Sunday ferry" {
		t.Fatalf("the new link is %+v", link)
	}
	s, found, err := Find(in, token)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.TenantID() != one.TenantID() || found.ID != link.ID {
		t.Fatalf("the token found tenant %s's link %s", s.TenantID(), found.ID)
	}
	if err := Usable(bg, s, found); err != nil {
		t.Fatal(err)
	}
	v, err := Read(bg, s, found, Show{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	text := string(raw)
	for _, want := range []string{"Sunday ferry", sharedWords, sharedReply} {
		if !strings.Contains(text, want) {
			t.Errorf("the view has no %q: %s", want, text)
		}
	}
	for _, never := range []string{privateWords, otherWords, "Lighthouse keys", "Beehives", "Test Person"} {
		if strings.Contains(text, never) {
			t.Errorf("the view holds %q, which is not of its chat: %s", never, text)
		}
	}
	if len(v.Entries) != 2 || v.Entries[0].Role != RolePerson || v.Entries[1].Role != RoleReply || v.AsOf == nil {
		t.Fatalf("the view's entries are %+v", v.Entries)
	}
}

func TestAForgedOrReaddressedTokenFindsNothing(t *testing.T) {
	in, one, two := install(t)
	shared, _ := chatWith(t, one, "Sunday ferry", said(sharedWords), answered(sharedReply))
	chatWith(t, two, "Beehives", said(otherWords), answered("Noted."))
	token, _, err := Create(bg, one, shared, draft())
	if err != nil {
		t.Fatal(err)
	}
	readdressed := strings.Replace(token, one.TenantID(), two.TenantID(), 1)
	flipped := token[:len(token)-1] + map[bool]string{true: "0", false: "1"}[token[len(token)-1] != '0']
	for name, tok := range map[string]string{
		"re-addressed to tenant two": readdressed,
		"one character changed":      flipped,
		"a session token's shape":    "kss_" + strings.TrimPrefix(token, "ksh_"),
		"no tenant on the install":   "ksh_0123456789abcdef_" + strings.Repeat("a", 64),
		"empty":                      "",
		"a path":                     "ksh_../" + one.TenantID(),
	} {
		if s, l, err := Find(in, tok); !errors.Is(err, ErrNotFound) {
			if s != nil {
				s.Close()
			}
			t.Errorf("a token %s found %+v (%v)", name, l, err)
		}
	}

	// Tenant one's link row copied into tenant two's database, as a
	// mistaken restore might do: the token still names tenant one, and the
	// re-addressed token's hash is not the row's.
	var id, view, created string
	var hash []byte
	if err := one.DB().QueryRow("SELECT id, token_hash, view, created_at FROM share_links").Scan(&id, &hash, &view, &created); err != nil {
		t.Fatal(err)
	}
	var twosChat string
	if err := two.DB().QueryRow("SELECT id FROM chats LIMIT 1").Scan(&twosChat); err != nil {
		t.Fatal(err)
	}
	if _, err := two.DB().Exec("INSERT INTO share_links(id, chat_id, token_hash, view, created_at) VALUES(?, ?, ?, ?, ?)", id, twosChat, hash, view, created); err != nil {
		t.Fatal(err)
	}
	if s, l, err := Find(in, readdressed); !errors.Is(err, ErrNotFound) {
		if s != nil {
			s.Close()
		}
		t.Errorf("with the row copied into tenant two, the re-addressed token found %+v (%v)", l, err)
	}
	s, l, err := Find(in, token)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.TenantID() != one.TenantID() || l.ChatID != shared {
		t.Errorf("with the row copied, tenant one's token found tenant %s's chat %s", s.TenantID(), l.ChatID)
	}
}

func TestTheTokenIsNotKept(t *testing.T) {
	_, one, _ := install(t)
	shared, _ := chatWith(t, one, "Sunday ferry", said(sharedWords), answered(sharedReply))
	token, _, err := Create(bg, one, shared, draft())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Dir(filepath.Dir(filepath.Dir(one.Files().Name())))
	one.Close()
	secret := token[len(token)-64:]
	err = filepath.Walk(data, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), secret) || strings.Contains(string(raw), token) {
			t.Errorf("%s holds the link's token", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestARevokedOrExpiredLinkIsRefused(t *testing.T) {
	in, one, _ := install(t)
	shared, _ := chatWith(t, one, "Sunday ferry", said(sharedWords), answered(sharedReply))
	token, link, err := Create(bg, one, shared, draft())
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := Revoke(bg, one, link.ID)
	if err != nil || revoked.State != StateRevoked || revoked.Revoked == nil {
		t.Fatalf("revoking answered %+v, %v", revoked, err)
	}
	if again, err := Revoke(bg, one, link.ID); err != nil || again.Revoked == nil || !again.Revoked.Equal(*revoked.Revoked) {
		t.Errorf("revoking twice answered %+v, %v", again, err)
	}
	s, found, err := Find(in, token)
	if err != nil {
		t.Fatalf("a revoked link is still found, to be refused: %v", err)
	}
	if err := Usable(bg, s, found); !errors.Is(err, ErrRevoked) {
		t.Errorf("a revoked link is usable: %v", err)
	}
	s.Close()
	if _, err := Revoke(bg, one, "0123456789abcdef"); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoking a link that is not there answered %v", err)
	}

	defer func() { now = time.Now }()
	token, link, err = Create(bg, one, shared, Draft{MaxOpen: 10, Lifetime: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if link.Expires == nil || link.Expires.Sub(link.Created) != time.Hour {
		t.Fatalf("a link made for an hour expires at %v", link.Expires)
	}
	now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	s, found, err = Find(in, token)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := Usable(bg, s, found); !errors.Is(err, ErrExpired) || found.State != StateExpired {
		t.Errorf("an expired link is usable: %v, state %s", err, found.State)
	}
}

func TestAHeldIncognitoOrEmptyChatIsNotShared(t *testing.T) {
	in, one, _ := install(t)
	held, _ := chatWith(t, one, "Held", said(sharedWords), answered(sharedReply))
	if err := hold(one, "turn-held", held); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Create(bg, one, held, draft()); !errors.Is(err, ErrHeld) {
		t.Errorf("a held chat was shared: %v", err)
	}
	hidden, _ := chatWith(t, one, "Incognito", said(sharedWords), answered(sharedReply))
	if err := memory.SetIncognito(bg, one, hidden, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Create(bg, one, hidden, draft()); !errors.Is(err, ErrIncognito) {
		t.Errorf("an incognito chat was shared: %v", err)
	}
	empty, _ := chatWith(t, one, "Empty")
	if _, _, err := Create(bg, one, empty, draft()); !errors.Is(err, ErrEmpty) {
		t.Errorf("an empty chat was shared: %v", err)
	}
	if _, _, err := Create(bg, one, "0123456789abcdef", draft()); !errors.Is(err, ErrNotFound) {
		t.Errorf("a chat that is not there was shared: %v", err)
	}

	// A link made before the chat was held, or made incognito, shows it no
	// more, and says why in its person's list.
	later, _ := chatWith(t, one, "Later held", said(sharedWords), answered(sharedReply))
	token, _, err := Create(bg, one, later, draft())
	if err != nil {
		t.Fatal(err)
	}
	if err := hold(one, "turn-later", later); err != nil {
		t.Fatal(err)
	}
	s, found, err := Find(in, token)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := Usable(bg, s, found); !errors.Is(err, ErrHeld) || found.State != StateRefused {
		t.Errorf("a link of a chat held since is usable: %v, state %s", err, found.State)
	}
	gone, _ := chatWith(t, one, "Later incognito", said(sharedWords), answered(sharedReply))
	tokenTwo, _, err := Create(bg, one, gone, draft())
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.SetIncognito(bg, one, gone, true); err != nil {
		t.Fatal(err)
	}
	s2, found2, err := Find(in, tokenTwo)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := Usable(bg, s2, found2); !errors.Is(err, ErrIncognito) {
		t.Errorf("a link of a chat made incognito since is usable: %v", err)
	}
}

func TestOpenLinksAreCountedAgainstTheMost(t *testing.T) {
	_, one, _ := install(t)
	shared, _ := chatWith(t, one, "Sunday ferry", said(sharedWords), answered(sharedReply))
	_, first, err := Create(bg, one, shared, Draft{MaxOpen: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Create(bg, one, shared, Draft{MaxOpen: 2}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Create(bg, one, shared, Draft{MaxOpen: 2}); !errors.Is(err, ErrTooMany) {
		t.Errorf("a third link past a most of two was made: %v", err)
	}
	if _, err := Revoke(bg, one, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Create(bg, one, shared, Draft{MaxOpen: 2}); err != nil {
		t.Errorf("a revoked link still counts: %v", err)
	}
	if _, _, err := Create(bg, one, shared, Draft{MaxOpen: 0}); !errors.Is(err, ErrTooMany) {
		t.Errorf("a link was made with a most of nothing: %v", err)
	}
	if _, _, err := Create(bg, one, shared, Draft{MaxOpen: 9, View: "everything"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a link with a view that is no view was made: %v", err)
	}
	list, err := List(bg, one, "")
	if err != nil || len(list) != 3 || list[0].Created.Before(list[2].Created) {
		t.Fatalf("the list is %+v, %v", list, err)
	}
	if mine, err := List(bg, one, shared); err != nil || len(mine) != 3 {
		t.Errorf("the chat's list is %d links, %v", len(mine), err)
	}
	if _, err := List(bg, one, "0123456789abcdef"); !errors.Is(err, ErrNotFound) {
		t.Errorf("listing a chat that is not there answered %v", err)
	}
}

func TestASnapshotKeepsItsBranchAndALiveLinkFollows(t *testing.T) {
	in, one, _ := install(t)
	shared, ids := chatWith(t, one, "Sunday ferry", said(sharedWords), answered(sharedReply))
	snapToken, snap, err := Create(bg, one, shared, Draft{MaxOpen: 9, View: ViewSnapshot})
	if err != nil {
		t.Fatal(err)
	}
	liveToken, _, err := Create(bg, one, shared, Draft{MaxOpen: 9})
	if err != nil {
		t.Fatal(err)
	}
	if snap.View != ViewSnapshot {
		t.Fatalf("the snapshot link is %+v", snap)
	}
	const later = "And on Mondays?"
	if _, err := chat.Append(one, shared, chat.NewMessage{ParentID: ids[1], Role: seam.RoleUser, Blocks: said(later).Blocks}); err != nil {
		t.Fatal(err)
	}
	// An edit of the first message makes another branch, which becomes the
	// current one.
	const edited = "When does the ferry leave on Saturdays?"
	if _, err := chat.Append(one, shared, chat.NewMessage{Role: seam.RoleUser, Blocks: said(edited).Blocks}); err != nil {
		t.Fatal(err)
	}
	read := func(token string) string {
		s, l, err := Find(in, token)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		v, err := Read(bg, s, l, Show{})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(v)
		return string(raw)
	}
	snapText := read(snapToken)
	if !strings.Contains(snapText, sharedWords) || !strings.Contains(snapText, sharedReply) || strings.Contains(snapText, later) || strings.Contains(snapText, edited) {
		t.Errorf("the snapshot shows %s", snapText)
	}
	liveText := read(liveToken)
	if !strings.Contains(liveText, edited) || strings.Contains(liveText, sharedWords) {
		t.Errorf("the live link does not follow the current branch: %s", liveText)
	}
}

func TestAViewHoldsNoThinkingNoToolInputOrResultAndOnlyItsImages(t *testing.T) {
	in, one, _ := install(t)
	input, _ := json.Marshal(map[string]string{"query": "ferry office"})
	shared, _ := chatWith(t, one, "Sunday ferry",
		seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{
			{Type: seam.BlockImage, MediaType: "image/png", Ref: imageRef},
			{Type: seam.BlockFile, MediaType: "text/plain", Name: "timetable.txt", Ref: strings.Repeat("b", 64)},
			{Type: seam.BlockText, Text: sharedWords},
		}},
		seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{
			{Type: seam.BlockThinking, Text: thought, Signature: "sig"},
			{Type: seam.BlockToolCall, ID: "call-1", Name: "web_search", Input: input},
			{Type: seam.BlockToolCall, ID: "call-2", Name: "web_fetch", Input: input},
		}},
		seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{
			{Type: seam.BlockToolResult, ID: "call-1", Content: []seam.Block{{Type: seam.BlockText, Text: toolSaid}}},
			{Type: seam.BlockToolResult, ID: "call-2", IsError: true, Content: []seam.Block{{Type: seam.BlockText, Text: "refused: not a public address"}}},
		}},
		answered(sharedReply),
	)
	token, _, err := Create(bg, one, shared, draft())
	if err != nil {
		t.Fatal(err)
	}
	s, l, err := Find(in, token)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	v, err := Read(bg, s, l, Show{Images: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	text := string(raw)
	for _, never := range []string{thought, toolSaid, "ferry office", "not a public address", "sig"} {
		if strings.Contains(text, never) {
			t.Errorf("the view holds %q: %s", never, text)
		}
	}
	if len(v.Entries) != 2 {
		t.Fatalf("want the person's message and one reply, got %+v", v.Entries)
	}
	reply := v.Entries[1].Parts
	if len(reply) != 3 || reply[0].Kind != PartTool || reply[0].Name != "web_search" || reply[0].Failed ||
		reply[1].Name != "web_fetch" || !reply[1].Failed || reply[2].Kind != PartText {
		t.Errorf("the reply's parts are %+v", reply)
	}
	person := v.Entries[0].Parts
	if len(person) != 3 || person[0].Image != imageRef || person[1].Kind != PartFile || person[1].Name != "timetable.txt" {
		t.Errorf("the person's parts are %+v", person)
	}
	if !v.HasImage(imageRef) || v.HasImage(strings.Repeat("b", 64)) || v.HasImage(strings.Repeat("c", 64)) {
		t.Error("the view gives the wrong images")
	}
	v, err = Read(bg, s, l, Show{Images: false})
	if err != nil {
		t.Fatal(err)
	}
	if v.HasImage(imageRef) || v.Entries[0].Parts[0].Image != "" || v.Entries[0].Parts[0].Kind != PartImage {
		t.Errorf("with images off, the image is %+v", v.Entries[0].Parts[0])
	}
}

func TestAnArtifactIsShownAsItsSourceOnlyWhenItIsOfTheChat(t *testing.T) {
	in, one, _ := install(t)
	call := func(id string) seam.Message {
		return seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockToolCall, ID: id, Name: "create_artifact", Input: json.RawMessage(`{}`)}}}
	}
	result := func(id, text string) seam.Message {
		return seam.Message{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockToolResult, ID: id, Content: []seam.Block{{Type: seam.BlockText, Text: text}}}}}
	}
	other, _ := chatWith(t, one, "Other", said("x"), answered("y"))
	shared, _ := chatWith(t, one, "Sunday ferry", said(sharedWords),
		call("a"), result("a", "Made the artifact â€œFerry timesâ€ (artifact 1111111111111111, version 1, type html)."),
		call("b"), result("b", "Made the artifact â€œNot hereâ€ (artifact 2222222222222222, version 1, type html)."),
		answered(sharedReply))
	stamp := "2026-11-26T00:00:00.000Z"
	for id, chatID := range map[string]string{"1111111111111111": shared, "2222222222222222": other} {
		if _, err := one.DB().Exec("INSERT INTO artifacts(id, chat_id, kind, title, latest, created_at, updated_at) VALUES(?, ?, 'html', 'T', 1, ?, ?)", id, chatID, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	token, _, err := Create(bg, one, shared, draft())
	if err != nil {
		t.Fatal(err)
	}
	s, l, err := Find(in, token)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var asked []string
	show := Show{Artifact: func(_ context.Context, _ *tenant.Store, id string, version int) (*Artifact, error) {
		asked = append(asked, id)
		return &Artifact{Title: "Ferry times", Type: "html", Version: version, Content: "<p>at nine</p>"}, nil
	}}
	v, err := Read(bg, s, l, show)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "1111111111111111" {
		t.Errorf("the artifacts asked for are %v, want only the chat's own", asked)
	}
	parts := v.Entries[1].Parts
	if len(parts) != 3 || parts[0].Kind != PartArtifact || parts[0].Artifact.Content != "<p>at nine</p>" || parts[1].Kind != PartTool {
		t.Errorf("the reply's parts are %+v", parts)
	}
	if v, err = Read(bg, s, l, Show{}); err != nil || v.Entries[1].Parts[0].Kind != PartTool {
		t.Errorf("with no artifacts shown, the step is %+v (%v)", v.Entries[1].Parts[0], err)
	}
}

func TestNotesAndTaskReplies(t *testing.T) {
	in, one, _ := install(t)
	shared, ids := chatWith(t, one, "Sunday ferry", said(sharedWords), answered("Part of an answer"), answered("A task's result."))
	// What a turn records about each reply: the first was stopped by the
	// person, the second is a background task's note.
	for i, stop := range map[int]string{1: "stopped", 2: stopTask} {
		if _, err := one.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES(?, ?, 'done', 'r', 'person', '{}', '2026-11-26T00:00:00.000Z')", "turn-"+stop, shared); err != nil {
			t.Fatal(err)
		}
		if _, err := one.DB().Exec("INSERT INTO turn_messages(message_id, turn_id, stop_reason) VALUES(?, ?, ?)", ids[i], "turn-"+stop, stop); err != nil {
			t.Fatal(err)
		}
	}
	token, _, err := Create(bg, one, shared, draft())
	if err != nil {
		t.Fatal(err)
	}
	s, l, err := Find(in, token)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, err := Read(bg, s, l, Show{})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Entries) != 3 {
		t.Fatalf("want the person's message, the stopped reply and the task's note apart, got %+v", v.Entries)
	}
	if p := v.Entries[1].Parts; len(p) != 2 || p[1].Kind != PartNote || p[1].Text != endNotes["stopped"] {
		t.Errorf("the stopped reply is %+v", p)
	}
}

func TestReasonsNeverQuoteTheToken(t *testing.T) {
	for _, err := range []error{ErrNotFound, ErrRevoked, ErrExpired, ErrHeld, ErrIncognito, ErrEmpty, ErrTooMany, ErrInvalid} {
		if r := Reason(err); r == "" || strings.Contains(r, "ksh_") {
			t.Errorf("the reason for %v is %q", err, r)
		}
	}
}
