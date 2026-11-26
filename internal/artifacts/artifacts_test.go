package artifacts

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Invented, for throwaway folders only.
const (
	pageOne = "<!doctype html><html><body><h1>Harbour tides</h1></body></html>"
	pageTwo = "<!doctype html><html><body><h1>Harbour tides</h1><p>High water at noon.</p></body></html>"
)

func testOptions() Options {
	return Options{MaxBytes: 64 << 10, TitleMaxChars: 60, Sandbox: []string{"allow-scripts", "allow-forms", "allow-modals"}}
}

func stores(t *testing.T) (*tenant.Install, *tenant.Store, *tenant.Store) {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	var out []*tenant.Store
	for _, name := range []string{"Test Person One", "Test Person Two"} {
		made, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		out = append(out, s)
	}
	return in, out[0], out[1]
}

func newChat(t *testing.T, s *tenant.Store) string {
	t.Helper()
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func mustCreate(t *testing.T, s *tenant.Store, chatID string, d Draft) Artifact {
	t.Helper()
	a, v, err := Create(context.Background(), s, testOptions(), chatID, d, "req-create")
	if err != nil {
		t.Fatal(err)
	}
	if a.Latest != 1 || v.Version != 1 || v.Origin != OriginModel {
		t.Fatalf("a new artifact is %+v with version %+v, want version 1 by the model", a, v)
	}
	return a
}

func text(s string) *string { return &s }

func TestCreateKeepsVersionOneInTheArtifactsFolder(t *testing.T) {
	_, s, _ := stores(t)
	ctx := context.Background()
	chatID := newChat(t, s)
	a := mustCreate(t, s, chatID, Draft{Type: KindHTML, Title: "  Harbour   tides ", Content: pageOne})
	if a.Title != "Harbour tides" || a.Type != KindHTML || a.ChatID != chatID || a.Link != "/artifacts/"+a.ID || a.Sensitive || !IsID(a.ID) {
		t.Fatalf("the artifact is %+v", a)
	}
	raw, err := s.Artifacts().ReadFile(chatID + "/" + a.ID + "/v1")
	if err != nil || string(raw) != pageOne {
		t.Fatalf("the version's file holds %q (%v)", raw, err)
	}
	v, content, err := Content(ctx, s, a.ID, 1)
	if err != nil || content != pageOne || v.Size != len(pageOne) || v.RequestID != "req-create" || len(v.SHA256) != 64 {
		t.Fatalf("version 1 reads %+v %q (%v)", v, content, err)
	}
	code := mustCreate(t, s, chatID, Draft{Type: KindCode, Language: "Python", Title: "Tide table", Content: "print('high water')\n"})
	if code.Language != "python" {
		t.Errorf("a code artifact's language is %q, want python", code.Language)
	}
	page := mustCreate(t, s, chatID, Draft{Type: KindMarkdown, Language: "python", Title: "Notes", Content: "# Notes"})
	if page.Language != "" {
		t.Errorf("a markdown artifact has the language %q, want none", page.Language)
	}
	list, err := List(ctx, s, chatID)
	if err != nil || len(list) != 3 || list[0].ID != a.ID {
		t.Fatalf("the chat lists %+v (%v)", list, err)
	}
	all, err := ListAll(ctx, s)
	if err != nil || len(all) != 3 {
		t.Fatalf("every artifact lists %d (%v)", len(all), err)
	}
}

func TestEveryVersionIsKeptAndAnUpdateIsANewOne(t *testing.T) {
	_, s, _ := stores(t)
	ctx := context.Background()
	o := testOptions()
	chatID := newChat(t, s)
	a := mustCreate(t, s, chatID, Draft{Type: KindHTML, Title: "Harbour tides", Content: pageOne})

	a2, v2, err := Update(ctx, s, o, chatID, a.ID, Change{Content: text(pageTwo)}, "req-2")
	if err != nil || a2.Latest != 2 || v2.Version != 2 {
		t.Fatalf("the whole new content wrote %+v %+v (%v)", a2, v2, err)
	}
	a3, v3, err := Update(ctx, s, o, chatID, a.ID, Change{OldText: "noon", NewText: "half past twelve", HasOldText: true, Title: "Harbour tides, Thursday"}, "req-3")
	if err != nil || a3.Latest != 3 || a3.Title != "Harbour tides, Thursday" || v3.Title != a3.Title {
		t.Fatalf("one replacement wrote %+v %+v (%v)", a3, v3, err)
	}
	want := map[int]string{1: pageOne, 2: pageTwo, 3: strings.Replace(pageTwo, "noon", "half past twelve", 1)}
	for n, body := range want {
		if _, got, err := Content(ctx, s, a.ID, n); err != nil || got != body {
			t.Errorf("version %d reads %q (%v), want %q", n, got, err, body)
		}
	}
	versions, err := Versions(ctx, s, a.ID)
	if err != nil || len(versions) != 3 || versions[0].Title != "Harbour tides" {
		t.Fatalf("the versions are %+v (%v)", versions, err)
	}

	for name, c := range map[string]Change{
		"old text that is not there":   {OldText: "midnight", NewText: "x", HasOldText: true},
		"old text that is there twice": {OldText: "</", NewText: "x", HasOldText: true},
		"both kinds of change":         {Content: text("x"), OldText: "noon", HasOldText: true},
		"no change at all":             {},
		"the same content again":       {Content: text(want[3])},
		"empty old text":               {OldText: "", NewText: "x", HasOldText: true},
	} {
		if _, _, err := Update(ctx, s, o, chatID, a.ID, c, "req"); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: %v, want a refusal", name, err)
		}
	}
	if got, _ := Get(ctx, s, a.ID); got.Latest != 3 {
		t.Errorf("after refused changes the newest version is %d, want 3", got.Latest)
	}
	// A model changes only the artifacts of the chat it is in.
	other := newChat(t, s)
	if _, _, err := Update(ctx, s, o, other, a.ID, Change{Content: text("x")}, "req"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an update from another chat: %v, want not found", err)
	}
}

func TestRestoreWritesAnOlderVersionAsTheNewest(t *testing.T) {
	_, s, _ := stores(t)
	ctx := context.Background()
	o := testOptions()
	chatID := newChat(t, s)
	a := mustCreate(t, s, chatID, Draft{Type: KindHTML, Title: "Harbour tides", Content: pageOne})
	if _, _, err := Update(ctx, s, o, chatID, a.ID, Change{Content: text(pageTwo), Title: "Harbour tides, again"}, "req-2"); err != nil {
		t.Fatal(err)
	}
	got, v, err := Restore(ctx, s, o, a.ID, 1, "req-restore")
	if err != nil || got.Latest != 3 || v.Origin != OriginRestore || v.RestoredFrom != 1 || v.Title != "Harbour tides" || got.Title != "Harbour tides" || v.RequestID != "req-restore" {
		t.Fatalf("restoring version 1 wrote %+v %+v (%v)", got, v, err)
	}
	if _, content, _ := Content(ctx, s, a.ID, 3); content != pageOne {
		t.Errorf("the restored version reads %q", content)
	}
	if _, content, _ := Content(ctx, s, a.ID, 2); content != pageTwo {
		t.Errorf("version 2 changed to %q after the restore", content)
	}
	if _, _, err := Restore(ctx, s, o, a.ID, 3, "req"); !errors.Is(err, ErrRefused) {
		t.Errorf("restoring the newest version: %v, want a refusal", err)
	}
	if _, _, err := Restore(ctx, s, o, a.ID, 9, "req"); !errors.Is(err, ErrNotFound) {
		t.Errorf("restoring a version that is not there: %v, want not found", err)
	}
}

func TestVersionsKeptLetsTheOldestGo(t *testing.T) {
	_, s, _ := stores(t)
	ctx := context.Background()
	o := testOptions()
	o.VersionsKept = 2
	chatID := newChat(t, s)
	a, _, err := Create(ctx, s, o, chatID, Draft{Type: KindMarkdown, Title: "Log", Content: "one"}, "r")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"two", "three", "four"} {
		if _, _, err := Update(ctx, s, o, chatID, a.ID, Change{Content: text(body)}, "r"); err != nil {
			t.Fatal(err)
		}
	}
	versions, err := Versions(ctx, s, a.ID)
	if err != nil || len(versions) != 2 || versions[0].Version != 3 || versions[1].Version != 4 {
		t.Fatalf("with two kept the versions are %+v (%v)", versions, err)
	}
	if _, err := s.Artifacts().Stat(chatID + "/" + a.ID + "/v1"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the file of a version let go is still there (%v)", err)
	}
	if _, _, err := Content(ctx, s, a.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("a version let go reads: %v", err)
	}
}

func TestWhatIsNotWrittenIsRefusedWithTheReason(t *testing.T) {
	_, s, _ := stores(t)
	ctx := context.Background()
	o := testOptions()
	chatID := newChat(t, s)
	for name, d := range map[string]Draft{
		"a type that is not one":   {Type: "spreadsheet", Title: "Sums", Content: "1,2"},
		"no title":                 {Type: KindMarkdown, Title: "   ", Content: "x"},
		"a title too long":         {Type: KindMarkdown, Title: strings.Repeat("t", 61), Content: "x"},
		"no content":               {Type: KindMarkdown, Title: "Empty", Content: " \n"},
		"content too large":        {Type: KindMarkdown, Title: "Large", Content: strings.Repeat("x", o.MaxBytes+1)},
		"content that is not text": {Type: KindMarkdown, Title: "Bytes", Content: "\xff\xfe"},
		"a language with a slash":  {Type: KindCode, Language: "../go", Title: "Code", Content: "x"},
	} {
		if _, _, err := Create(ctx, s, o, chatID, d, "r"); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: %v, want a refusal", name, err)
		}
	}
	if _, _, err := Create(ctx, s, o, "no-such-chat", Draft{Type: KindMarkdown, Title: "T", Content: "x"}, "r"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a chat that is not there: %v, want not found", err)
	}
	entries, err := fs.ReadDir(s.Artifacts().FS(), ".")
	if err != nil || len(entries) != 0 {
		t.Errorf("refused artifacts left %d entries in the folder (%v)", len(entries), err)
	}
}

func TestAnotherTenantReadsNothingAndPathsAreRefused(t *testing.T) {
	_, one, two := stores(t)
	ctx := context.Background()
	chatID := newChat(t, one)
	a := mustCreate(t, one, chatID, Draft{Type: KindSVG, Title: "Lighthouse", Content: `<svg xmlns="http://www.w3.org/2000/svg"/>`})
	if _, err := Get(ctx, two, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant's Get: %v", err)
	}
	if _, _, err := Content(ctx, two, a.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant's Content: %v", err)
	}
	if _, err := List(ctx, two, chatID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant's List of the chat: %v", err)
	}
	if all, err := ListAll(ctx, two); err != nil || len(all) != 0 {
		t.Errorf("another tenant lists %d artifacts (%v)", len(all), err)
	}
	if _, _, err := Restore(ctx, two, testOptions(), a.ID, 1, "r"); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant's Restore: %v", err)
	}
	for _, id := range []string{"../" + a.ID, "..", chatID + "/" + a.ID, "/etc/passwd", strings.ToUpper(a.ID), a.ID + "/v1"} {
		if _, err := Get(ctx, one, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("the id %q: %v, want not found", id, err)
		}
	}
	// The folder itself refuses a path that leaves it.
	if _, err := one.Artifacts().ReadFile("../keystone.db"); err == nil {
		t.Error("the artifacts folder read a file outside it")
	}
}

func TestDeletingTheChatRemovesItsArtifacts(t *testing.T) {
	_, s, _ := stores(t)
	ctx := context.Background()
	chatID := newChat(t, s)
	keep := newChat(t, s)
	a := mustCreate(t, s, chatID, Draft{Type: KindMermaid, Title: "Ferry route", Content: "graph TD; A-->B"})
	b := mustCreate(t, s, keep, Draft{Type: KindMarkdown, Title: "Kept", Content: "stays"})
	if err := chat.Delete(s, chatID); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(ctx, s, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("an artifact of a deleted chat: %v", err)
	}
	if _, err := s.Artifacts().Stat(chatID); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the deleted chat's artifacts folder is still there (%v)", err)
	}
	var n int
	if err := s.DB().QueryRow("SELECT count(*) FROM artifact_versions WHERE artifact_id = ?", a.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("the deleted chat's versions left %d rows (%v)", n, err)
	}
	if _, content, err := Content(ctx, s, b.ID, 1); err != nil || content != "stays" {
		t.Errorf("another chat's artifact reads %q (%v)", content, err)
	}
}

func TestAnArtifactOfAHeldChatIsSensitive(t *testing.T) {
	_, s, _ := stores(t)
	ctx := context.Background()
	o := testOptions()
	chatID := newChat(t, s)
	before := mustCreate(t, s, chatID, Draft{Type: KindMarkdown, Title: "Before", Content: "written before the hold"})
	if before.Sensitive {
		t.Fatal("an artifact of a chat that is not held is sensitive")
	}
	if _, err := s.DB().Exec("INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES('turn-held', ?, 'done', 'req', 'person', '{}', '2026-11-25T10:00:00.000Z')", chatID); err != nil {
		t.Fatal(err)
	}
	if err := recall.Hold(ctx, s, "turn-held", chatID); err != nil {
		t.Fatal(err)
	}
	// An artifact written before the hold reads as sensitive once the chat
	// is held: what it is changed into may draw on the sensitive content.
	if got, err := Get(ctx, s, before.ID); err != nil || !got.Sensitive {
		t.Errorf("an artifact of a chat held afterwards reads %+v (%v), want sensitive", got, err)
	}
	after, v, err := Create(ctx, s, o, chatID, Draft{Type: KindMarkdown, Title: "After", Content: "written while held"}, "r")
	if err != nil || !after.Sensitive || !v.Sensitive {
		t.Fatalf("an artifact written in a held chat is %+v %+v (%v), want both sensitive", after, v, err)
	}
	list, err := ListAll(ctx, s)
	if err != nil || len(list) != 2 || !list[0].Sensitive || !list[1].Sensitive {
		t.Errorf("the list reads %+v (%v), want both sensitive", list, err)
	}
	if !strings.Contains(said("Made", after, v), "sensitive") {
		t.Error("the model is not told an artifact of a held chat is sensitive")
	}
}

func TestOptionsNeverGiveTheFrameSameOrigin(t *testing.T) {
	get := func(sandbox string) func(string) (string, bool) {
		values := map[string]string{SettingMaxBytes: "1000", SettingVersionsKept: "0", SettingTitleMaxChars: "80", SettingSandbox: sandbox}
		return func(key string) (string, bool) { v, ok := values[key]; return v, ok }
	}
	o, err := ReadOptions(get("allow-scripts  allow-forms"))
	if err != nil || o.SandboxTokens() != "allow-scripts allow-forms" {
		t.Fatalf("the options read %+v (%v)", o, err)
	}
	for _, bad := range []string{"allow-scripts allow-same-origin", "allow-top-navigation", "allow-popups", "allow-popups-to-escape-sandbox", "allow-top-navigation-by-user-activation", "allow-storage-access-by-user-activation"} {
		if _, err := ReadOptions(get(bad)); err == nil {
			t.Errorf("the setting %q was taken", bad)
		}
	}
	// A permission that got past Check never reaches the frame.
	sneaked := Options{Sandbox: []string{"allow-scripts", "allow-same-origin", "allow-popups"}}
	if got := sneaked.SandboxTokens(); got != "allow-scripts" {
		t.Errorf("the frame's permissions are %q", got)
	}
}
