package testtenant

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	fixture "github.com/jonathanmkosar/keystone/fixtures/test-tenant"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// testOptions are quick sign-in settings: the shipped round count is for
// real passwords.
func testOptions() Options {
	return Options{
		SignIn: signin.Options{
			PasswordIterations: 50, PasswordMinLength: 12, SessionLifetime: time.Hour,
			FailuresBeforeWait: 5, FailureWait: time.Minute,
			PasskeyRPID: "localhost", PasskeyOrigin: "http://localhost:7900", PasskeyChallengeLifetime: time.Minute,
		},
		Chat: chat.Limits{TitleLength: 150, SearchResults: 50, SnippetWords: 24},
	}
}

func shipped(t *testing.T) Fixtures {
	t.Helper()
	fx, err := Shipped()
	if err != nil {
		t.Fatal(err)
	}
	return fx
}

func open(t *testing.T) *tenant.Install {
	t.Helper()
	in, err := tenant.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func store(t *testing.T, in *tenant.Install, id string) *tenant.Store {
	t.Helper()
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestShippedFixturesAreWhole(t *testing.T) {
	fx := shipped(t)
	if fx.Name == "" || fx.Password == "" || len(fx.Chats) < 3 || len(fx.Cases) == 0 || len(fx.Scripts) == 0 {
		t.Fatalf("the shipped fixtures are thin: %d chats, %d cases, %d scripts", len(fx.Chats), len(fx.Cases), len(fx.Scripts))
	}
	for _, c := range fx.Chats {
		if len(c.Replies) != len(c.Messages) || c.Hash == "" {
			t.Errorf("chat %s was not replayed", c.File)
		}
	}
}

func TestLoadTwiceGivesTheSameTenant(t *testing.T) {
	fx, o, in := shipped(t), testOptions(), open(t)

	// A person with the same name is not the test tenant, before or after.
	person, err := in.Create(fx.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := Find(in); err != nil || found {
		t.Fatalf("Find on an install with no test tenant: found %v, err %v", found, err)
	}

	first, err := Load(in, fx, o)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || !first.PasswordSet || first.ChatsMade != len(fx.Chats) || first.ChatsKept != 0 {
		t.Fatalf("first load: %+v", first)
	}
	if first.Tenant.ID == person.ID {
		t.Fatal("the loader took a person's tenant for the test tenant")
	}
	s := store(t, in, first.Tenant.ID)
	token, _, err := signin.SignIn(s, fx.Password, o.SignIn)
	if err != nil {
		t.Fatalf("the fixture's password does not sign in: %v", err)
	}

	second, err := Load(in, fx, o)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.PasswordSet || second.ChatsMade != 0 || second.ChatsKept != len(fx.Chats) || second.Tenant != first.Tenant {
		t.Fatalf("second load changed something: %+v", second)
	}
	if len(in.Tenants()) != 2 {
		t.Fatalf("%d tenants after two loads, want 2", len(in.Tenants()))
	}
	if _, err := signin.Check(s, token); err != nil {
		t.Errorf("a second load ended a session: %v", err)
	}
	chats, err := chat.List(s, chat.Filter{})
	if err != nil || len(chats) != len(fx.Chats) {
		t.Fatalf("%d chats after two loads, want %d (err %v)", len(chats), len(fx.Chats), err)
	}

	// Only the loaded tenant is marked.
	if got, found, err := Find(in); err != nil || !found || got.ID != first.Tenant.ID {
		t.Fatalf("Find: %+v, found %v, err %v", got, found, err)
	}
	if is, err := IsTest(s); err != nil || !is {
		t.Errorf("IsTest on the test tenant: %v, %v", is, err)
	}
	ps := store(t, in, person.ID)
	if is, err := IsTest(ps); err != nil || is {
		t.Errorf("IsTest on a person with the same name: %v, %v", is, err)
	}
	if has, _ := signin.HasPassword(ps); has {
		t.Error("the loader gave a person a password")
	}
	if theirs, _ := chat.List(ps, chat.Filter{}); len(theirs) != 0 {
		t.Error("the loader wrote chats into a person's tenant")
	}

	failed, err := RunCases(s, fx, o.Chat)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range failed {
		t.Error(f)
	}
}

func TestLoadRestoresAndKeeps(t *testing.T) {
	fx, o, in := shipped(t), testOptions(), open(t)
	res, err := Load(in, fx, o)
	if err != nil {
		t.Fatal(err)
	}
	s := store(t, in, res.Tenant.ID)

	// A chat the person made, a loaded chat they deleted, a fixture that
	// changed, and a password they changed.
	own, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	chats, _ := chat.List(s, chat.Filter{})
	var gone string
	for _, c := range chats {
		if c.Title == fx.Chats[0].Title {
			gone = c.ID
		}
	}
	if err := chat.Delete(s, gone); err != nil {
		t.Fatal(err)
	}
	fx.Chats[1].Hash = "changed"
	if err := signin.SetPassword(s, "another-invented-password", o.SignIn); err != nil {
		t.Fatal(err)
	}

	res, err = Load(in, fx, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || !res.PasswordSet || res.ChatsMade != 2 || res.ChatsKept != len(fx.Chats)-2 {
		t.Fatalf("load after changes: %+v", res)
	}
	if _, err := chat.Get(s, own.ID); err != nil {
		t.Errorf("the person's own chat did not survive a load: %v", err)
	}
	chats, _ = chat.List(s, chat.Filter{})
	if len(chats) != len(fx.Chats)+1 {
		t.Errorf("%d chats, want the %d loaded and the person's one", len(chats), len(fx.Chats))
	}
	if failed, err := RunCases(s, fx, o.Chat); err != nil || len(failed) > 0 {
		t.Errorf("use cases after a reload: %v, %v", failed, err)
	}
}

func TestRunCasesReportsAWrongResult(t *testing.T) {
	fx, o, in := shipped(t), testOptions(), open(t)
	res, err := Load(in, fx, o)
	if err != nil {
		t.Fatal(err)
	}
	s := store(t, in, res.Tenant.ID)
	yes := false
	fx.Cases = []Case{
		{Name: "wrong turn", Kind: KindTurn, Ask: "How long is the walk there and back?", Expect: Expect{StopReason: "tool_call", TextContains: []string{"no such words"}, ToolCalls: []string{"nothing"}}},
		{Name: "wrong search", Kind: KindSearch, Query: "ferry", Expect: Expect{Titles: []string{}}},
		{Name: "wrong chat", Kind: KindChat, Title: fx.Chats[2].Title, Expect: Expect{Messages: 99, Starred: &yes, Branches: 9, TextContains: []string{"no such words"}}},
		{Name: "no chat", Kind: KindChat, Title: "No such chat", Expect: Expect{Messages: 1}},
	}
	failed, err := RunCases(s, fx, o.Chat)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 3+1+4+1 {
		t.Errorf("%d problems reported, want 9:\n%s", len(failed), strings.Join(failed, "\n"))
	}
}

func TestReadRefuses(t *testing.T) {
	ask := `{"role":"user","blocks":[{"type":"text","text":"Suggest three names for a small bakery by the harbour."}]}`
	cases := map[string]struct{ file, content, want string }{
		"a file not marked as test data":  {"tenant.json", `{"name":"Test Person One","password":"invented-test-password-1"}`, "not marked as test data"},
		"a field it does not know":        {"tenant.json", `{"test_data":true,"name":"Test Person One","password":"invented-test-password-1","email":"x"}`, "unknown field"},
		"a tenant with no password":       {"tenant.json", `{"test_data":true,"name":"Test Person One"}`, "needs a name and a password"},
		"a chat with a reply written in":  {"chats/01-bakery-names.json", `{"test_data":true,"title":"T","messages":[{"role":"assistant","blocks":[{"type":"text","text":"Hello."}]}]}`, "not the person's"},
		"an edit of a later message":      {"chats/01-bakery-names.json", `{"test_data":true,"title":"T","messages":[{"edits":1,` + ask[1:] + `]}`, "not an earlier message"},
		"a chat that ends on a tool":      {"chats/01-bakery-names.json", `{"test_data":true,"title":"T","messages":[{"role":"user","blocks":[{"type":"text","text":"When does the north ferry leave on Tuesday?"}]}]}`, "ends on a tool call"},
		"two chats with one title":        {"chats/01-bakery-names.json", `{"test_data":true,"title":"North ferry times","messages":[` + ask + `]}`, "two chats are titled"},
		"a use case of an unknown kind":   {"cases/first-cases.json", `{"test_data":true,"cases":[{"name":"n","kind":"dance","expect":{}}]}`, "not turn, search or chat"},
		"a use case that expects nothing": {"cases/first-cases.json", `{"test_data":true,"cases":[{"name":"n","kind":"turn","ask":"Hello","expect":{}}]}`, "expects nothing"},
		"a turn file not marked":          {"turns/north-ferry.json", `{"scripts":[]}`, "not marked as test data"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			err := fs.WalkDir(fixture.FS, ".", func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				raw, err := fs.ReadFile(fixture.FS, p)
				fsys[p] = &fstest.MapFile{Data: raw}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Read(fsys); err != nil {
				t.Fatalf("the copy of the shipped fixtures: %v", err)
			}
			fsys[c.file] = &fstest.MapFile{Data: []byte(c.content)}
			if _, err := Read(fsys); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Read gave %v, want an error holding %q", err, c.want)
			}
		})
	}
}
