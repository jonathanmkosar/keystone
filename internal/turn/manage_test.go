package turn

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/imported"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/share"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Managing many chats (P4-06). Every chat, file and word here is invented.

// serveManage starts a server as serve does, with the table and the batch
// delete, and the project routes, beside the chat and turn routes.
func (g *rig) serveManage(provider seam.Provider) *served {
	g.t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 50 * time.Millisecond
	if provider == nil {
		if provider, err = testtenant.Provider(g.fx, fake.Options{DeltaSize: 12}); err != nil {
			g.t.Fatal(err)
		}
	}
	set, err := adapters.Fake(g.fx.Scripts, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	runner := NewRunner(Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Adapters: set})
	runner.Recover()
	routes := append(Routes(runner), ManageRoutes(runner)...)
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, append(routes, ProjectRoutes(runner)...))
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

// written makes a chat with a title and messages, a person's and a reply by
// turns, straight in the chat record, and returns its id.
func written(t *testing.T, s *tenant.Store, title string, messages ...string) string {
	t.Helper()
	c, err := chat.Create(s)
	if err != nil {
		t.Fatal(err)
	}
	if title != "" {
		lim, err := chat.DefaultLimits()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := chat.Rename(s, lim, c.ID, title); err != nil {
			t.Fatal(err)
		}
	}
	var parent int64
	for i, words := range messages {
		role := seam.RoleUser
		if i%2 == 1 {
			role = seam.RoleAssistant
		}
		m, err := chat.Append(s, c.ID, chat.NewMessage{ParentID: parent, Role: role, Blocks: []seam.Block{{Type: seam.BlockText, Text: words}}})
		if err != nil {
			t.Fatal(err)
		}
		parent = m.ID
		time.Sleep(2 * time.Millisecond) // so no two chats were last active in the same millisecond
	}
	return c.ID
}

// tableOf asks for the table and returns it.
func (sv *served) tableOf(tenantID, query string) TableReply {
	sv.g.t.Helper()
	var got TableReply
	sv.call(tenantID, "GET", "chats/table"+query, "", http.StatusOK, &got)
	return got
}

func rowIDs(rows []TableRow) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func rowOf(t *testing.T, rows []TableRow, id string) TableRow {
	t.Helper()
	for _, r := range rows {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("the table has no row for chat %s", id)
	return TableRow{}
}

func exec(t *testing.T, s *tenant.Store, query string, args ...any) {
	t.Helper()
	if _, err := s.DB().Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

// aTask writes a background task's row, started from one chat and working
// in another, as internal/jobs would.
func aTask(t *testing.T, s *tenant.Store, id, chatID, workChatID, status string) {
	t.Helper()
	exec(t, s, `INSERT INTO jobs(id, chat_id, work_chat_id, title, task, started_by, status, request_id, started_request, actor, budget_micros, max_rounds, created_at)
		VALUES(?, ?, ?, 'An invented task', 'Count the gulls.', 'person', ?, ?, 'req-start', 'person', 0, 1, ?)`,
		id, chatID, workChatID, status, "req-"+id, stamp())
}

// aSubagent writes a sub-agent's row, as internal/subagents would.
func aSubagent(t *testing.T, s *tenant.Store, id, chatID, workChatID, status string) {
	t.Helper()
	exec(t, s, `INSERT INTO subagents(id, chat_id, turn_id, call_id, position, work_chat_id, title, task, request_id, actor, budget_micros, max_steps, status, created_at)
		VALUES(?, ?, 'a-turn', 'a-call', 1, ?, 'An invented helper', 'Count the terns.', ?, 'person', 0, 1, ?, ?)`,
		id, chatID, workChatID, "req-"+id, status, stamp())
}

// The table lists every chat with its marks, and searches, narrows, sorts
// and pages them. The first proof is here: a search finds a chat by a word
// in a message, with the word marked.
func TestTheTableOfEveryChat(t *testing.T) {
	g := newRig(t)
	sv := g.serveManage(nil)
	me := g.ordinary.ID
	ctx := context.Background()

	var quay, walk, bakery, kites, helper, work, untitled string
	g.with(me, func(s *tenant.Store) {
		quay = written(t, s, "Quay repairs", "Who mends the quay wall?", "The harbour board mends the quay wall each spring.")
		walk = written(t, s, "Lighthouse walk", "What should I bring?", "A windproof jacket and a paper map of the saltmarsh.", "Anything else?", "A flask of tea.")
		bakery = written(t, s, "bakery names", "Suggest three names for a small bakery by the harbour.")
		kites = written(t, s, "Kites on the saltmarsh", "When is the wind best?")
		helper = written(t, s, "A helper's chat", "Count the terns.", "Forty terns.")
		work = written(t, s, "A task's chat", "Count the gulls.", "Sixty gulls.")
		untitled = written(t, s, "", "A chat with no title yet.")

		// The marks: a star, an import, a task's chat and a sub-agent's, a
		// hold, a share link and the incognito flag.
		if _, err := chat.Star(s, walk, true); err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
		if err := imported.MarkChat(ctx, s.DB(), bakery, imported.NewMark{
			Mark:   imported.Mark{From: "New Keystone", Kind: "chat", At: at, Turns: 1, First: at, Last: at, Models: []string{"an-invented-model"}},
			Source: "new-keystone", ImportID: "0123456789abcdef",
		}); err != nil {
			t.Fatal(err)
		}
		aTask(t, s, "task-done", quay, work, StatusDone)
		aSubagent(t, s, "sub-done", quay, helper, StatusDone)
		exec(t, s, "INSERT INTO turns(id, chat_id, status, request_id, actor, input, created_at) VALUES('held-turn', ?, 'done', 'req-held', 'person', '{}', ?)", kites, stamp())
		if err := recall.Hold(ctx, s, "held-turn", kites); err != nil {
			t.Fatal(err)
		}
		if _, _, err := share.Create(ctx, s, quay, share.Draft{MaxOpen: 5}); err != nil {
			t.Fatal(err)
		}
		if err := memory.SetIncognito(ctx, s, untitled, true); err != nil {
			t.Fatal(err)
		}
	})
	// A project, with the walk in it.
	name := "Harbour notes"
	body, _ := json.Marshal(ProjectBody{Name: &name})
	var project ProjectReply
	sv.call(me, "POST", "projects", string(body), http.StatusCreated, &project)
	sv.call(me, "PUT", "projects/"+project.Project.ID+"/chats/"+walk, "", http.StatusOK, nil)

	// Every chat, the newest message first, with its marks.
	all := sv.tableOf(me, "")
	if want := []string{untitled, work, helper, kites, bakery, walk, quay}; !slices.Equal(rowIDs(all.Rows), want) || all.Total != 7 || all.More {
		t.Fatalf("the table lists %v (total %d, more %v), want %v", rowIDs(all.Rows), all.Total, all.More, want)
	}
	if all.Sort != SortActive || all.Order != OrderDesc || all.PageRows != 50 || all.DeleteMax != 100 || all.IDs != nil {
		t.Errorf("the table says of itself: %+v", all)
	}
	if r := rowOf(t, all.Rows, walk); !r.Starred || r.Messages != 4 || r.Title != "Lighthouse walk" || r.Project == nil || r.Project.Name != name || r.Project.ID != project.Project.ID ||
		r.Imported != nil || r.Task || r.Subagent || r.Held || r.Shared != 0 || r.Incognito || r.Busy || r.Working || r.Match != nil || r.Active.IsZero() || r.Created.IsZero() {
		t.Errorf("the walk's row is %+v", r)
	}
	if r := rowOf(t, all.Rows, bakery); r.Imported == nil || r.Imported.From != "New Keystone" || r.Starred || r.Project != nil {
		t.Errorf("the imported chat's row is %+v", r)
	}
	if r := rowOf(t, all.Rows, work); !r.Task || r.Subagent || r.Working {
		t.Errorf("the task's chat's row is %+v", r)
	}
	if r := rowOf(t, all.Rows, helper); !r.Subagent || r.Task {
		t.Errorf("the sub-agent's chat's row is %+v", r)
	}
	if r := rowOf(t, all.Rows, kites); !r.Held {
		t.Errorf("the held chat's row is %+v", r)
	}
	if r := rowOf(t, all.Rows, quay); r.Shared != 1 || r.Task || r.Subagent {
		t.Errorf("the shared chat's row is %+v", r)
	}
	if r := rowOf(t, all.Rows, untitled); !r.Incognito || r.Title != "" || r.Messages != 1 {
		t.Errorf("the incognito chat's row is %+v", r)
	}

	// The proof: a word that is only in a message finds its chat, with the
	// word marked in the snippet.
	found := sv.tableOf(me, "?q=windproof")
	if len(found.Rows) != 1 || found.Rows[0].ID != walk || found.Total != 1 || found.Sort != SortMatch || found.Query != "windproof" {
		t.Fatalf("a word in a message found %+v", found)
	}
	m := found.Rows[0].Match
	if m == nil || m.Title || m.Messages != 1 || m.MessageID == 0 || m.Role != seam.RoleAssistant {
		t.Fatalf("the match is %+v", m)
	}
	var marked []string
	for _, p := range m.Snippet {
		if p.Match {
			marked = append(marked, p.Text)
		}
	}
	if !slices.Equal(marked, []string{"windproof"}) {
		t.Errorf("the snippet marks %v in %+v", marked, m.Snippet)
	}
	// A word in a title and in messages: title matches first, each chat once.
	found = sv.tableOf(me, "?q=saltmarsh")
	if want := []string{kites, walk}; !slices.Equal(rowIDs(found.Rows), want) {
		t.Fatalf("saltmarsh found %v, want %v", rowIDs(found.Rows), want)
	}
	if m := found.Rows[0].Match; m == nil || !m.Title || len(m.TitleParts) != 2 || !m.TitleParts[1].Match || m.Messages != 0 || m.Snippet != nil {
		t.Errorf("the title match is %+v", m)
	}
	// The search with a filter, and words nothing holds.
	if got := sv.tableOf(me, "?q=saltmarsh&filter=starred"); !slices.Equal(rowIDs(got.Rows), []string{walk}) || got.Total != 1 {
		t.Errorf("saltmarsh among the starred found %v", rowIDs(got.Rows))
	}
	for _, q := range []string{"?q=zeppelin", "?q=" + url.QueryEscape("?!"), "?q=" + url.QueryEscape(`quay" OR "bakery`)} {
		if got := sv.tableOf(me, q); len(got.Rows) != 0 || got.Total != 0 {
			t.Errorf("%s found %v", q, rowIDs(got.Rows))
		}
	}

	// The filters, alone and together.
	for filter, want := range map[string][]string{
		"starred":          {walk},
		"imported":         {bakery},
		"project":          {walk},
		"task":             {work},
		"subagent":         {helper},
		"starred,project":  {walk},
		"starred,imported": {},
	} {
		if got := sv.tableOf(me, "?filter="+filter); !slices.Equal(rowIDs(got.Rows), want) || got.Total != len(want) {
			t.Errorf("filter=%s gave %v, want %v", filter, rowIDs(got.Rows), want)
		}
	}

	// The orders. A chat with no title comes last either way.
	for query, want := range map[string][]string{
		"?sort=title":               {helper, work, bakery, kites, walk, quay, untitled},
		"?sort=title&order=desc":    {quay, walk, kites, bakery, work, helper, untitled},
		"?sort=messages":            {walk, work, helper, quay, untitled, kites, bakery},
		"?sort=messages&order=asc":  {untitled, kites, bakery, work, helper, quay, walk},
		"?sort=active&order=asc":    {quay, walk, bakery, kites, helper, work, untitled},
		"?sort=match":               {untitled, work, helper, kites, bakery, walk, quay},
		"?q=saltmarsh&sort=active":  {kites, walk},
		"?q=saltmarsh&order=desc":   {walk, kites},
		"?q=saltmarsh&sort=title":   {kites, walk},
		"?q=saltmarsh&sort=message": nil,
	} {
		if want == nil {
			sv.call(me, "GET", "chats/table"+query, "", http.StatusBadRequest, nil)
			continue
		}
		if got := sv.tableOf(me, query); !slices.Equal(rowIDs(got.Rows), want) {
			t.Errorf("%s gave %v, want %v", query, rowIDs(got.Rows), want)
		}
	}

	// Pages, and the ids of every chat that matches.
	page := sv.tableOf(me, "?limit=3&offset=3&ids=true")
	if want := []string{kites, bakery, walk}; !slices.Equal(rowIDs(page.Rows), want) || page.Total != 7 || !page.More {
		t.Errorf("the second page is %v (total %d, more %v)", rowIDs(page.Rows), page.Total, page.More)
	}
	if !slices.Equal(page.IDs, rowIDs(all.Rows)) || page.IDsCut {
		t.Errorf("the ids of every chat are %v (cut %v)", page.IDs, page.IDsCut)
	}
	if last := sv.tableOf(me, "?limit=3&offset=6"); len(last.Rows) != 1 || last.More {
		t.Errorf("the last page is %v (more %v)", rowIDs(last.Rows), last.More)
	}
	if past := sv.tableOf(me, "?offset=70"); len(past.Rows) != 0 || past.Total != 7 || past.More {
		t.Errorf("a page past the end is %+v", past)
	}
	// The settings: rows a page, and the most ids given at once.
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	g.with(me, func(s *tenant.Store) {
		for key, value := range map[string]string{KeyTablePageRows: "2", KeyTableSelectMax: "3"} {
			if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, key, value); err != nil {
				t.Fatal(err)
			}
		}
	})
	small := sv.tableOf(me, "?ids=true&limit=40")
	if len(small.Rows) != 2 || !small.More || small.PageRows != 2 || len(small.IDs) != 3 || !small.IDsCut || small.Total != 7 {
		t.Errorf("with two rows a page and three ids at once the table is %+v", small)
	}

	// What the table refuses.
	for _, query := range []string{"?sort=newest", "?order=up", "?filter=held", "?filter=starred,unknown", "?limit=0", "?limit=x", "?offset=-1"} {
		sv.call(me, "GET", "chats/table"+query, "", http.StatusBadRequest, nil)
	}

	// Another person's table holds none of it, and their search finds none.
	theirs := sv.tableOf(g.test.ID, "?ids=true")
	for _, id := range rowIDs(all.Rows) {
		if slices.Contains(theirs.IDs, id) {
			t.Errorf("another person's table lists chat %s", id)
		}
	}
	// (The test tenant's own lighthouse chat holds some of the same words.)
	for _, q := range []string{"windproof", "saltmarsh", "quay"} {
		for _, id := range rowIDs(sv.tableOf(g.test.ID, "?q="+q).Rows) {
			if slices.Contains(rowIDs(all.Rows), id) {
				t.Errorf("another person's search for %s found chat %s", q, id)
			}
		}
	}
	if got := sv.tableOf(g.test.ID, "?q=terns"); got.Total != 0 {
		t.Errorf("another person's search found %v", rowIDs(got.Rows))
	}
}

// has reports whether a file is in a folder of the tenant's.
func has(root interface {
	Stat(string) (fs.FileInfo, error)
}, name string) bool {
	_, err := root.Stat(name)
	return err == nil
}

// The item's proof: the selected chats and nothing else are deleted, with
// their files; another person's chat ids in the batch are refused and
// nothing of theirs changes; and each chat is in the audit log.
func TestTheSelectedChatsAndNothingElseAreDeleted(t *testing.T) {
	g := newRig(t)
	sv := g.serveManage(nil)
	me, other := g.ordinary.ID, g.test.ID
	ctx := context.Background()

	const own, both, held = "an-own-file.txt", "a-file-two-chats-hold.txt", "the-other-persons-file.txt"
	var a, b, c, d, x string
	g.with(me, func(s *tenant.Store) {
		a = written(t, s, "Tide tables", "When is high water at the quay?", "At a quarter past nine, marker COBALT-PLOVER.")
		b = written(t, s, "Boat names", "Suggest a name for a small boat.")
		c = written(t, s, "Kept: the kiln", "How hot does a kiln run?")
		d = written(t, s, "Kept: the kayak", "How long is a sea kayak?")
		for name, chats := range map[string][]string{own: {a}, both: {b, c}} {
			if err := s.Files().WriteFile(name, []byte("invented words\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, id := range chats {
				if err := chat.AddFile(s, id, name); err != nil {
					t.Fatal(err)
				}
			}
		}
		// The chat's artifacts are a folder named by its id.
		if err := s.Artifacts().MkdirAll(a+"/an-artifact", 0o700); err != nil {
			t.Fatal(err)
		}
		if err := s.Artifacts().WriteFile(a+"/an-artifact/1.md", []byte("# An invented artifact\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := share.Create(ctx, s, a, share.Draft{MaxOpen: 5}); err != nil {
			t.Fatal(err)
		}
	})
	// A project's file that chat a holds too: the project still holds it
	// after the chat is gone.
	name := "Harbour notes"
	body, _ := json.Marshal(ProjectBody{Name: &name})
	var project ProjectReply
	sv.call(me, "POST", "projects", string(body), http.StatusCreated, &project)
	var projectFile ProjectFileReply
	sv.call(me, "POST", "projects/"+project.Project.ID+"/files?name=tides.md", "# Tides\n\nInvented tide notes.\n", http.StatusCreated, &projectFile)
	g.with(me, func(s *tenant.Store) {
		if err := chat.AddFile(s, a, projectFile.ID); err != nil {
			t.Fatal(err)
		}
	})
	// The other person's chat, with a file and a message of its own.
	g.with(other, func(s *tenant.Store) {
		x = written(t, s, "The other person's chat", "Marker AMBER-GANNET, which nobody else may delete.")
		if err := s.Files().WriteFile(held, []byte("theirs\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := chat.AddFile(s, x, held); err != nil {
			t.Fatal(err)
		}
	})
	theirsBefore := sv.tableOf(other, "?ids=true")
	var theirLog []audit.Entry
	g.with(other, func(s *tenant.Store) {
		var err error
		if theirLog, err = audit.List(ctx, s, audit.Filter{}); err != nil {
			t.Fatal(err)
		}
	})

	// What is refused whole, with nothing deleted.
	for _, bad := range []string{`{}`, `{"ids":[]}`, `{"ids":[""]}`, `{"ids":["../` + other + `"]}`, `{"ids":["` + strings.Repeat("a", 65) + `"]}`, `{"chats":["` + a + `"]}`} {
		sv.call(me, "POST", "chats/delete", bad, http.StatusBadRequest, nil)
	}
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	setMax := func(value string) {
		g.with(me, func(s *tenant.Store) {
			if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, KeyDeleteBatchMax, value); err != nil {
				t.Fatal(err)
			}
		})
	}
	setMax("2")
	ids, _ := json.Marshal(DeleteManyBody{IDs: []string{a, b, c}})
	resp := sv.request(me, "POST", "chats/delete", string(ids), nil)
	var refusal struct {
		Code string `json:"code"`
	}
	json.NewDecoder(resp.Body).Decode(&refusal)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || refusal.Code != "too_many" {
		t.Fatalf("three chats with two allowed answered %d %s", resp.StatusCode, refusal.Code)
	}
	// The ids are counted as sent: one said three times is three.
	sv.call(me, "POST", "chats/delete", `{"ids":["`+a+`","`+a+`","`+a+`"]}`, http.StatusBadRequest, nil)
	if got := sv.tableOf(me, ""); got.Total != 4 || got.DeleteMax != 2 {
		t.Fatalf("after the refusals the person has %d chats (delete max %d)", got.Total, got.DeleteMax)
	}
	setMax("100")

	// The delete: two of the person's own, the other person's, an id of
	// nobody's, and one said twice.
	const nobody = "00000000deadbeef"
	ids, _ = json.Marshal(DeleteManyBody{IDs: []string{a, x, b, nobody, a}})
	resp = sv.request(me, "POST", "chats/delete", string(ids), nil)
	var got DeleteManyReply
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("the delete answered %d (%v)", resp.StatusCode, err)
	}
	resp.Body.Close()
	requestID := resp.Header.Get("X-Request-Id")
	if !slices.Equal(got.Deleted, []string{a, b}) {
		t.Errorf("deleted %v, want %v", got.Deleted, []string{a, b})
	}
	if len(got.Skipped) != 2 || got.Skipped[0].ID != x || got.Skipped[0].Reason != SkipNotFound || got.Skipped[0].Title != "" || got.Skipped[0].Why == "" ||
		got.Skipped[1].ID != nobody || got.Skipped[1].Reason != SkipNotFound {
		t.Errorf("skipped %+v, want the other person's chat and nobody's, each not_found with no title", got.Skipped)
	}

	// The selected chats are gone, with what was theirs; nothing else is.
	sv.call(me, "GET", "chats/"+a, "", http.StatusNotFound, nil)
	sv.call(me, "GET", "chats/"+b, "", http.StatusNotFound, nil)
	left := sv.tableOf(me, "")
	if want := []string{d, c}; !slices.Equal(rowIDs(left.Rows), want) || left.Total != 2 {
		t.Errorf("the chats left are %v, want %v", rowIDs(left.Rows), want)
	}
	if after := sv.tableOf(me, "?q=cobalt"); after.Total != 0 {
		t.Errorf("a deleted chat is still found by its words: %v", rowIDs(after.Rows))
	}
	g.with(me, func(s *tenant.Store) {
		if has(s.Files(), own) {
			t.Error("a deleted chat's own file is still in the files folder")
		}
		if !has(s.Files(), both) {
			t.Error("a file a kept chat holds went with the deleted chat")
		}
		if !has(s.Files(), projectFile.ID) {
			t.Error("a file a project holds went with the deleted chat")
		}
		if has(s.Artifacts(), a) {
			t.Error("a deleted chat's artifacts folder is still there")
		}
		for table, column := range map[string]string{"messages": "chat_id", "share_links": "chat_id", "chat_files": "chat_id", "turns": "chat_id"} {
			var n int
			if err := s.DB().QueryRow("SELECT count(*) FROM "+table+" WHERE "+column+" IN (?, ?)", a, b).Scan(&n); err != nil || n != 0 {
				t.Errorf("%s still holds %d rows of the deleted chats (%v)", table, n, err)
			}
		}
		if names, err := chat.Files(s, c); err != nil || !slices.Equal(names, []string{both}) {
			t.Errorf("the kept chat's files are %v (%v)", names, err)
		}
	})
	var stays ProjectReply
	sv.call(me, "GET", "projects/"+project.Project.ID, "", http.StatusOK, &stays)
	if len(stays.Files) != 1 || stays.Files[0].ID != projectFile.ID {
		t.Errorf("the project's files are %+v", stays.Files)
	}

	// Nothing of the other person's changed, and nothing was written in
	// their audit log.
	theirsAfter := sv.tableOf(other, "?ids=true")
	if !slices.Equal(theirsAfter.IDs, theirsBefore.IDs) || !slices.Contains(theirsAfter.IDs, x) {
		t.Errorf("the other person's chats were %v and are %v", theirsBefore.IDs, theirsAfter.IDs)
	}
	if r := rowOf(t, theirsAfter.Rows, x); r.Messages != 1 || r.Title != "The other person's chat" {
		t.Errorf("the other person's chat is now %+v", r)
	}
	g.with(other, func(s *tenant.Store) {
		if !has(s.Files(), held) {
			t.Error("the other person's file is gone")
		}
		log, err := audit.List(ctx, s, audit.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		// Reading their own table twice added their own calls and no other.
		for _, e := range log[min(len(theirLog), len(log)):] {
			if !strings.HasPrefix(e.Action, "GET ") {
				t.Errorf("the other person's audit log gained %s %s", e.Action, e.Outcome)
			}
		}
	})

	// Each chat is in the person's audit log under the call's request id,
	// between the call's own two entries.
	g.with(me, func(s *tenant.Store) {
		log, err := audit.List(ctx, s, audit.Filter{RequestID: requestID})
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, e := range log {
			lines = append(lines, e.Action+" "+string(e.Outcome))
			if e.Kind != audit.KindAPI || e.Actor != api.ActorPerson {
				t.Errorf("an entry of the delete is %+v", e)
			}
		}
		one := "DELETE " + api.Prefix + "chats/"
		want := []string{
			"POST " + api.Prefix + "chats/delete started",
			one + a + " started", one + a + " ok",
			one + x + " refused",
			one + b + " started", one + b + " ok",
			one + nobody + " refused",
			"POST " + api.Prefix + "chats/delete ok",
		}
		if !slices.Equal(lines, want) {
			t.Errorf("the audit log holds\n\t%s\nwant\n\t%s", strings.Join(lines, "\n\t"), strings.Join(want, "\n\t"))
		}
	})

	// Deleting what is gone already deletes nothing more.
	sv.call(me, "POST", "chats/delete", string(ids), http.StatusOK, &got)
	if len(got.Deleted) != 0 || len(got.Skipped) != 4 {
		t.Errorf("a second delete answered %+v", got)
	}
	// A person with no session deletes nothing.
	req, _ := http.NewRequest("POST", sv.server.URL+api.Prefix+"chats/delete", strings.NewReader(`{"ids":["`+c+`"]}`))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a delete with no session answered %v (%v)", resp.StatusCode, err)
	} else {
		resp.Body.Close()
	}
	sv.call(me, "GET", "chats/"+c, "", http.StatusOK, nil)
}

// A chat with a turn running, and one a task or a sub-agent is still at
// work in or for, is skipped and said so; nothing is stopped; and it can be
// deleted once the work has ended.
func TestAChatAtWorkIsSkipped(t *testing.T) {
	g := newRig(t)
	model := newSlow()
	sv := g.serveManage(model)
	me := g.test.ID

	running := sv.newChat(me)
	resp := sv.request(me, "POST", "chats/"+running+"/turns", `{"text":"Tell me a long story."}`, nil)
	defer resp.Body.Close()
	turnID := resp.Header.Get("X-Turn-Id")
	done := make(chan []event, 1)
	go func() { done <- readEvents(t, resp.Body, nil) }()
	select {
	case <-model.writing:
	case <-time.After(10 * time.Second):
		t.Fatal("the model was never called")
	}

	var idle, from, in, parent, helper string
	g.with(me, func(s *tenant.Store) {
		idle = written(t, s, "An idle chat", "Nothing is at work here.")
		from = written(t, s, "A task was started here", "Start the count.")
		in = written(t, s, "A task works here", "Count the gulls.")
		parent = written(t, s, "A sub-agent was started here", "Ask a helper.")
		helper = written(t, s, "A sub-agent works here", "Count the terns.")
		aTask(t, s, "task-at-work", from, in, StatusRunning)
		aSubagent(t, s, "sub-at-work", parent, helper, StatusQueued)
	})
	if r := rowOf(t, sv.tableOf(me, "").Rows, running); !r.Busy || r.Working {
		t.Errorf("the running chat's row is %+v", r)
	}
	if r := rowOf(t, sv.tableOf(me, "").Rows, from); r.Busy || !r.Working {
		t.Errorf("the row of a chat whose task is at work is %+v", r)
	}

	ids, _ := json.Marshal(DeleteManyBody{IDs: []string{running, idle, from, in, parent, helper}})
	var got DeleteManyReply
	sv.call(me, "POST", "chats/delete", string(ids), http.StatusOK, &got)
	if !slices.Equal(got.Deleted, []string{idle}) {
		t.Errorf("deleted %v, want the idle chat alone", got.Deleted)
	}
	want := map[string]string{running: SkipTurnRunning, from: SkipTaskRunning, in: SkipTaskRunning, parent: SkipTaskRunning, helper: SkipTaskRunning}
	if len(got.Skipped) != len(want) {
		t.Fatalf("skipped %+v", got.Skipped)
	}
	for _, s := range got.Skipped {
		if want[s.ID] != s.Reason || s.Why == "" {
			t.Errorf("chat %s (%s) was skipped as %q, want %q", s.ID, s.Title, s.Reason, want[s.ID])
		}
	}
	if got.Skipped[1].Title != "A task was started here" {
		t.Errorf("a skipped chat's title is %q", got.Skipped[1].Title)
	}

	// Nothing was stopped: the turn still runs, and every skipped chat is there.
	select {
	case <-model.cancelled:
		t.Fatal("the delete stopped the running turn")
	default:
	}
	var open ChatReply
	sv.call(me, "GET", "chats/"+running, "", http.StatusOK, &open)
	if len(open.Turns) != 1 || open.Turns[0].ID != turnID || open.Turns[0].Status != StatusRunning {
		t.Errorf("after the delete the running chat's turns are %+v", open.Turns)
	}
	for id := range want {
		sv.call(me, "GET", "chats/"+id, "", http.StatusOK, nil)
	}
	g.with(me, func(s *tenant.Store) {
		log, err := audit.List(context.Background(), s, audit.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		var skipped int
		for _, e := range log {
			if e.Action == "DELETE "+api.Prefix+"chats/"+running && e.Outcome == audit.OutcomeRefused && e.Status == http.StatusConflict && e.Received == SkipTurnRunning {
				skipped++
			}
		}
		if skipped != 1 {
			t.Errorf("the skipped chat is in the audit log %d times as refused", skipped)
		}
	})

	// Once the work has ended, they go.
	var stopped Turn
	sv.call(me, "POST", "chats/"+running+"/turns/"+turnID+"/stop", "", http.StatusOK, &stopped)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream did not close after the stop")
	}
	g.with(me, func(s *tenant.Store) {
		exec(t, s, "UPDATE jobs SET status = 'done' WHERE id = 'task-at-work'")
		exec(t, s, "UPDATE subagents SET status = 'stopped' WHERE id = 'sub-at-work'")
	})
	// The goroutine that worked the chat's turns lets go a moment after the
	// turn ends.
	deadline := time.Now().Add(10 * time.Second)
	for {
		ids, _ = json.Marshal(DeleteManyBody{IDs: []string{running}})
		sv.call(me, "POST", "chats/delete", string(ids), http.StatusOK, &got)
		if len(got.Deleted) == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !slices.Equal(got.Deleted, []string{running}) {
		t.Fatalf("with its turn stopped the chat was not deleted: %+v", got)
	}
	ids, _ = json.Marshal(DeleteManyBody{IDs: []string{from, in, parent, helper}})
	sv.call(me, "POST", "chats/delete", string(ids), http.StatusOK, &got)
	if len(got.Deleted) != 4 || len(got.Skipped) != 0 {
		t.Errorf("with the work ended the delete answered %+v", got)
	}
	g.with(me, func(s *tenant.Store) {
		if _, err := chat.Get(s, running); !errors.Is(err, chat.ErrNotFound) {
			t.Errorf("the chat is still in the record: %v", err)
		}
	})
}
