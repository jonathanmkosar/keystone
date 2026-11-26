package importertest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/imported"
	"github.com/jonathanmkosar/keystone/internal/importer"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/builtin"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// otherPassword is invented, and used in throwaway folders only.
const otherPassword = "another person's phrase for the import proof"

// proofClock is the server's clock in the proof: it says what it is told.
type proofClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *proofClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *proofClock) set(text string) {
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		panic(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

// Proof is the item's proof (P12-03), on the real server, in a new data
// folder inside dir, with the standard fixture as the source:
//
//   - the dry run counts what the import then brings, and writes nothing;
//   - imported chats open through the API with their messages in order,
//     their original times and the models New Keystone named, wear their
//     mark, and are found by the chat search and by search_chats;
//   - the morning brief became a paused schedule, which does not run while
//     it is paused and runs, with the fake provider, once it is switched on;
//   - a second import adds nothing;
//   - the import called no model, is in the tenant's audit log, and nothing
//     of it is in another tenant's store or reachable from its session.
//
// The person imported into is the test tenant, whose recorded turns answer
// the brief's prompt. It returns what failed, in words, and an error when
// the proof could not be run at all.
func Proof(dir string) ([]string, error) {
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	ctx := context.Background()

	in, err := tenant.OpenWithKeeper(filepath.Join(dir, "data"), secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	fx, err := testtenant.Shipped()
	if err != nil {
		return nil, err
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		return nil, err
	}
	loaded, err := testtenant.Load(in, fx, o)
	if err != nil {
		return nil, err
	}
	other, err := in.Create("Invented Other Person")
	if err != nil {
		return nil, err
	}
	s, err := in.Store(loaded.Tenant.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	theirs, err := in.Store(other.ID)
	if err != nil {
		return nil, err
	}
	defer theirs.Close()
	if err := signin.SetPassword(theirs, otherPassword, o.SignIn); err != nil {
		return nil, err
	}

	file := filepath.Join(dir, "source", "workspace.sqlite")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, err
	}
	if err := Build(file, Standard()); err != nil {
		return nil, err
	}
	src, err := importer.OpenSource(file)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	countOf := func(st *tenant.Store, table string) int {
		var n int
		st.DB().QueryRow("SELECT count(*) FROM " + table).Scan(&n)
		return n
	}
	modelCalls := func(st *tenant.Store) int {
		entries, _ := audit.List(ctx, st, audit.Filter{Kind: audit.KindModel})
		return len(entries)
	}
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)

	// 1. The dry run says what would come, and writes nothing.
	chatsBefore, auditBefore, callsBefore := countOf(s, "chats"), countOf(s, "audit_log"), modelCalls(s)
	would, err := importer.Check(ctx, s, src, "", now)
	if err != nil {
		return nil, err
	}
	if !would.Check || would.Chats != 5 || would.Turns != 7 || would.Messages != 12 || would.Schedules != 3 || !would.Brief {
		say("the dry run counts %d chats, %d turns, %d messages and %d schedules (brief %v), and must count 5, 7, 12 and 3 with the brief", would.Chats, would.Turns, would.Messages, would.Schedules, would.Brief)
	}
	if countOf(s, "chats") != chatsBefore || countOf(s, "audit_log") != auditBefore || countOf(s, "imports") != 0 || countOf(s, "schedules") != 0 {
		say("the dry run wrote something: %d chats, %d audit entries, %d imports, %d schedules", countOf(s, "chats")-chatsBefore, countOf(s, "audit_log")-auditBefore, countOf(s, "imports"), countOf(s, "schedules"))
	}
	var printed bytes.Buffer
	would.Write(&printed)
	for _, word := range []string{BeanWord, PintleWord, BriefName, WeeklyName, "harbour brief", BuildSummary, Workspace} {
		if strings.Contains(printed.String(), word) {
			say("the dry run prints %q, a word of the source", word)
		}
	}

	// 2. The import.
	did, err := importer.Run(ctx, s, src, "", now)
	if err != nil {
		return nil, err
	}
	if did.Chats != would.Chats || did.Turns != would.Turns || did.Messages != would.Messages || did.Schedules != would.Schedules || did.ImportID == "" {
		say("the import brought %d chats, %d turns, %d messages and %d schedules, and the dry run said %d, %d, %d and %d", did.Chats, did.Turns, did.Messages, did.Schedules, would.Chats, would.Turns, would.Messages, would.Schedules)
	}
	if n := modelCalls(s); n != callsBefore {
		say("the import made %d model calls, and must make none", n-callsBefore)
	}
	entries, err := audit.List(ctx, s, audit.Filter{RequestID: "import-" + did.ImportID})
	if err != nil {
		return nil, err
	}
	if len(entries) != 2 || entries[0].Kind != audit.KindTool || entries[0].Action != importer.Action || entries[0].Outcome != audit.OutcomeStarted || entries[1].Outcome != audit.OutcomeOK || entries[1].Actor != importer.Actor {
		say("the import's audit entries are %+v, and must be a started and an ok entry of the tool %s by %s", entries, importer.Action, importer.Actor)
	}
	for _, e := range entries {
		for _, word := range []string{BeanWord, PintleWord, BriefName} {
			if strings.Contains(e.Sent+e.Received, word) {
				say("the import's audit entry holds %q, a word of the source", word)
			}
		}
	}
	if records, err := usage.List(ctx, s, usage.Filter{RequestID: "import-" + did.ImportID}); err != nil || len(records) != 0 {
		say("the import has %d usage records (%v), and must have none", len(records), err)
	}

	// 3. The real server, with a clock the proof sets.
	clock := &proofClock{}
	clock.set("2026-11-26T15:00:00Z") // 09:00 in Chicago
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	var log bytes.Buffer
	c.Install, c.Log, c.Clock = in, &log, clock.now
	c.Schedules.Poll = 10 * time.Millisecond
	h, stop, err := app.Open(c)
	if err != nil {
		return nil, err
	}
	defer stop()
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var r *http.Request
		if method == "GET" || method == "DELETE" {
			r = httptest.NewRequest(method, api.Prefix+path, nil)
		} else {
			r = httptest.NewRequest(method, api.Prefix+path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			r.AddCookie(&http.Cookie{Name: api.CookieName, Value: token})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r.WithContext(rctx))
		return rec
	}
	signIn := func(id, password string) (string, error) {
		body, _ := json.Marshal(api.SignInBody{Tenant: id, Password: password})
		rec := request("POST", "session", "", string(body))
		for _, ck := range rec.Result().Cookies() {
			if ck.Name == api.CookieName && rec.Code == http.StatusOK {
				return ck.Value, nil
			}
		}
		return "", fmt.Errorf("tenant %s cannot sign in: status %d", id, rec.Code)
	}
	call := func(token, method, path string, want int, out any) error {
		rec := request(method, path, token, "{}")
		if rec.Code != want {
			return fmt.Errorf("%s %s answered %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		if out != nil {
			return json.Unmarshal(rec.Body.Bytes(), out)
		}
		return nil
	}
	token, err := signIn(loaded.Tenant.ID, fx.Password)
	if err != nil {
		return nil, err
	}
	otherToken, err := signIn(other.ID, otherPassword)
	if err != nil {
		return nil, err
	}

	// 4. Imported chats are in the list with their mark, and the chats that
	// were here before have none.
	var listed turn.ListReply
	if err := call(token, "GET", "chats?limit=100", http.StatusOK, &listed); err != nil {
		return nil, err
	}
	var beans, pintle turn.Chat
	marked := 0
	for _, ch := range listed.Chats {
		if ch.Imported == nil {
			continue
		}
		marked++
		if ch.Imported.From != "New Keystone" {
			say("an imported chat says it came from %q", ch.Imported.From)
		}
		switch {
		case strings.Contains(ch.Title, BeanWord):
			beans = ch
		case strings.Contains(ch.Title, PintleWord):
			pintle = ch
		}
	}
	if marked != did.Chats || len(listed.Chats) != chatsBefore+did.Chats {
		say("the list of chats holds %d chats, %d of them marked as imported, and must hold %d and %d", len(listed.Chats), marked, chatsBefore+did.Chats, did.Chats)
	}
	if beans.ID == "" || pintle.ID == "" {
		say("the bean chat and the pintle chat are not both in the list of chats")
		return problems, nil
	}
	fixture := Standard()
	if !beans.Created.Equal(fixture.Turns[0].At) || !beans.Active.Equal(fixture.Turns[1].At.Add(fixture.Turns[1].Took)) {
		say("the bean chat was made at %s and last active at %s, and must keep its first and last message's times", beans.Created, beans.Active)
	}
	if m := beans.Imported; m.Kind != imported.ChatStretch || m.Turns != 2 || len(m.Models) != 1 || m.Models[0] != LocalModel || m.Steps["search.completed"] != 1 || !strings.Contains(m.Note, "1 web search") {
		say("the bean chat's mark is %+v", *m)
	}

	// 5. An imported chat opens: its messages in order, with their roles,
	// their original times and the model New Keystone named.
	var opened turn.ChatReply
	if err := call(token, "GET", "chats/"+beans.ID, http.StatusOK, &opened); err != nil {
		return nil, err
	}
	text := func(m turn.Message) string { return seam.Text(seam.Message{Role: m.Role, Blocks: m.Blocks}) }
	if opened.Chat.Imported == nil {
		say("an opened imported chat has no mark")
	}
	if n := len(opened.Messages); n != 4 {
		say("the bean chat opens with %d messages, and must have 4", n)
	} else {
		want := []struct {
			role  seam.Role
			holds string
			at    time.Time
			model string
		}{
			{seam.RoleUser, BeanAsk, fixture.Turns[0].At, ""},
			{seam.RoleAssistant, BeanReply, fixture.Turns[0].At.Add(fixture.Turns[0].Took), LocalModel},
			{seam.RoleUser, BeanAsk2, fixture.Turns[1].At, ""},
			{seam.RoleAssistant, BeanReply2, fixture.Turns[1].At.Add(fixture.Turns[1].Took), LocalModel},
		}
		for i, w := range want {
			m := opened.Messages[i]
			if m.Role != w.role || !strings.Contains(text(m), w.holds) || !m.Created.Equal(w.at) || m.Model != w.model || m.TurnID != "" {
				say("message %d of the bean chat is %s at %s by %q, and must be %s at %s by %q, holding its words", i+1, m.Role, m.Created, m.Model, w.role, w.at, w.model)
			}
		}
		if !strings.Contains(text(opened.Messages[1]), "Sources:") || !strings.Contains(text(opened.Messages[1]), "(https://gardening.example/beans-north-fence)") {
			say("the first reply does not end with its web source")
		}
		if !strings.Contains(text(opened.Messages[2]), BeanFileName) || !strings.Contains(text(opened.Messages[2]), BeanFileText) {
			say("the second message does not carry its attached file's text")
		}
	}
	opened = turn.ChatReply{}
	if err := call(token, "GET", "chats/"+pintle.ID, http.StatusOK, &opened); err != nil {
		return nil, err
	}
	if len(opened.Messages) != 2 || opened.Messages[1].Model != PickedModel {
		say("the pintle chat opens with %d messages, and its reply must name %q, the model the person picked there", len(opened.Messages), PickedModel)
	}

	// 6. Found by the chat search, and by search_chats, the model's own.
	var found turn.SearchReply
	if err := call(token, "GET", "chats/search?q="+BeanWord, http.StatusOK, &found); err != nil {
		return nil, err
	}
	hit := false
	for _, hh := range found.Hits {
		hit = hit || hh.ChatID == beans.ID
	}
	if !hit {
		say("the chat search for %q does not find the imported chat", BeanWord)
	}
	lim, err := chat.DefaultLimits()
	if err != nil {
		return nil, err
	}
	list, err := builtin.ChatTools(builtin.Options{Limits: lim, ReadChatMessages: 40, PastChats: builtin.PastChats{MaxResults: 5, Passages: 2}}, false, nil)
	if err != nil {
		return nil, err
	}
	reg := tools.New()
	for _, tl := range list {
		if err := reg.Add(tl); err != nil {
			return nil, err
		}
	}
	res := reg.Run(ctx, tools.Call{Store: s, RequestID: "import-proof", Actor: "person", ChatID: pintle.ID, ID: "call_1", Name: builtin.SearchChats, Input: json.RawMessage(`{"query":"` + BeanWord + ` beans"}`)})
	if got := seam.Text(seam.Message{Blocks: res.Block.Content}); res.Status != tools.StatusOK || !strings.Contains(got, beans.ID) || !strings.Contains(got, "hand's width") && !strings.Contains(got, BeanWord) {
		say("search_chats for the bean chat came back %s:\n%s", res.Status, got)
	}
	res = reg.Run(ctx, tools.Call{Store: theirs, RequestID: "import-proof", Actor: "person", ChatID: "none", ID: "call_2", Name: builtin.SearchChats, Input: json.RawMessage(`{"query":"` + BeanWord + ` beans"}`)})
	if got := seam.Text(seam.Message{Blocks: res.Block.Content}); strings.Contains(got, beans.ID) || strings.Contains(got, BeanWord+" beans along") {
		say("search_chats in another tenant's store finds the imported chat:\n%s", got)
	}

	// 7. The morning brief is a paused schedule, in the zone New Keystone
	// kept its times in.
	var scheds schedules.SchedulesReply
	if err := call(token, "GET", "schedules", http.StatusOK, &scheds); err != nil {
		return nil, err
	}
	var brief schedules.Schedule
	for _, sc := range scheds.Schedules {
		if sc.Enabled {
			say("an imported schedule is switched on")
		}
		if sc.Name == BriefName {
			brief = sc
		}
	}
	if len(scheds.Schedules) != 3 || scheds.Zone.Name != "America/Chicago" {
		say("the person has %d schedules in the zone %q, and must have 3 in America/Chicago", len(scheds.Schedules), scheds.Zone.Name)
	}
	if brief.ID == "" || brief.Prompt != BriefPrompt || brief.Recurrence.Kind != schedules.KindDaily || brief.Recurrence.Time != "07:00" || brief.Next != nil {
		say("the morning brief is the schedule %+v, and must be daily at 07:00 with its prompt, paused", brief)
		return problems, nil
	}
	runsOf := func() ([]schedules.Run, error) {
		var got schedules.ScheduleReply
		err := call(token, "GET", "schedules/"+brief.ID, http.StatusOK, &got)
		return got.Runs, err
	}
	// A morning passes while it is paused: nothing runs.
	clock.set("2026-11-27T13:30:00Z") // 07:30 in Chicago
	time.Sleep(300 * time.Millisecond)
	if runs, err := runsOf(); err != nil || len(runs) != 0 {
		say("the paused brief ran %d times (%v)", len(runs), err)
	}
	// Switched on, it is due the next morning, and runs then.
	var on schedules.Schedule
	if err := call(token, "POST", "schedules/"+brief.ID+"/resume", http.StatusOK, &on); err != nil {
		return nil, err
	}
	if !on.Enabled || on.Next == nil || on.Next.UTC().Format(time.RFC3339) != "2026-11-28T13:00:00Z" {
		say("switched on at 07:30, the brief is next due at %v, and must be due at 07:00 the next morning in Chicago", on.Next)
	}
	clock.set("2026-11-28T13:00:30Z")
	var run schedules.Run
	var task jobs.Task
	deadline := time.Now().Add(15 * time.Second)
	for {
		runs, err := runsOf()
		if err != nil {
			return nil, err
		}
		if len(runs) > 0 && runs[0].TaskID != "" {
			run = runs[0]
			if err := call(token, "GET", "chats/"+run.ChatID+"/tasks/"+run.TaskID, http.StatusOK, &task); err != nil {
				return nil, err
			}
			if task.NoteMessageID != 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			say("switched on and past its due time, the brief has %d runs and none has ended", len(runs))
			return problems, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	opened = turn.ChatReply{}
	if err := call(token, "GET", "chats/"+run.ChatID, http.StatusOK, &opened); err != nil {
		return nil, err
	}
	if n := len(opened.Messages); n < 2 || text(opened.Messages[0]) != BriefPrompt || !strings.HasPrefix(text(opened.Messages[n-1]), BriefResult) || opened.Chat.Imported != nil {
		say("the brief's run is a chat of %d messages that does not hold the prompt and the recorded reply, or is marked as imported: %q … %q", n, text(opened.Messages[0]), text(opened.Messages[max(n-1, 0)]))
	}
	if opened.Chat.Title != BriefName+", Sat 28 Nov 2026" {
		say("the brief's run is titled %q", opened.Chat.Title)
	}

	// 8. A second import adds nothing.
	chatsNow, schedulesNow := countOf(s, "chats"), countOf(s, "schedules")
	again, err := importer.Run(ctx, s, src, "", now.Add(time.Hour))
	if err != nil {
		return nil, err
	}
	if again.Chats != 0 || again.Messages != 0 || again.Schedules != 0 || again.AlreadyTurns != 7 || again.AlreadyRoutines != 3 || again.ImportID != "" {
		say("a second import brought %d chats, %d messages and %d schedules, with %d turns and %d routines here already", again.Chats, again.Messages, again.Schedules, again.AlreadyTurns, again.AlreadyRoutines)
	}
	if countOf(s, "chats") != chatsNow || countOf(s, "schedules") != schedulesNow || countOf(s, "imports") != 1 {
		say("after a second import the person has %d more chats, %d more schedules and %d import runs", countOf(s, "chats")-chatsNow, countOf(s, "schedules")-schedulesNow, countOf(s, "imports"))
	}

	// 9. Nothing of it is the other tenant's.
	for _, table := range []string{"chats", "messages", "schedules", "imports", "import_sources", "imported_chats", "imported_messages"} {
		if n := countOf(theirs, table); n != 0 {
			say("the other tenant's %s table holds %d rows after the import", table, n)
		}
	}
	if logged, _ := audit.List(ctx, theirs, audit.Filter{}); len(logged) > 0 {
		for _, e := range logged {
			if e.Action == importer.Action {
				say("the import is in the other tenant's audit log")
			}
		}
	}
	for _, path := range []string{"chats/" + beans.ID, "schedules/" + brief.ID} {
		if rec := request("GET", path, otherToken, ""); rec.Code != http.StatusNotFound {
			say("the other tenant's session reads %s: status %d", path, rec.Code)
		}
	}
	listed = turn.ListReply{}
	if err := call(otherToken, "GET", "chats", http.StatusOK, &listed); err != nil || len(listed.Chats) != 0 {
		say("the other tenant's list of chats holds %d chats (%v)", len(listed.Chats), err)
	}
	return problems, nil
}
