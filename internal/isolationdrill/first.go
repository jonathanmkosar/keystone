package isolationdrill

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/connectors"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/knowledge/builtin"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/shareapi"
	"github.com/jonathanmkosar/keystone/internal/styles"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/uploads"
)

// first is what the first person has, by id, for the second to try.
type first struct {
	// chatA is their main chat: messages, an upload, a pin, a style, a
	// project, an attached chat, a handoff and a task. chatB holds an
	// artifact and is attached to A. chatC was handed off from A. chatD
	// has a turn that waits for an approval.
	chatA, chatB, chatC, chatD, incognito string
	turnA, turnD, approval                string
	messageA                              int64
	requestA                              string
	upload                                string
	artifact                              string
	project, projectFile                  string
	style                                 string
	schedule                              string
	task, taskChat                        string
	page, source, memorySource            string
	connector                             string
	tools                                 []string
	googleState                           string
	shareLink, shareToken                 string
	// subagent is a sub-agent of chat A's turn (P8-02), put in the first
	// person's record as a finished one.
	subagent string
	// research is a research task started from chat A (P8-04), put in the
	// first person's record as a finished one.
	research string
	// browserShot is a screenshot the agent's browser took in chat A
	// (P10-03), put in the first person's record.
	browserShot string
	// googleAuth are the credentials the stand-in Google saw with the
	// first person's calls.
	googleAuth []string
}

// Words the first person's things are named with. They are not marks: the
// second person may know or guess them, and tries them.
const (
	titleA        = "Lantern Accounts"
	artifactTitle = "Lantern Ledger"
	projectName   = "Harbour Works"
	styleName     = "Lantern Voice"
	scheduleName  = "Lantern brief"
	spaceName     = "lantern"
	connectorName = "lanternnotes"
	secretName    = "provider.lantern"
	tokenName     = "connector.lantern-notes"
	knownWords    = "lantern ledger blue tin"
)

func runSuffix() string {
	var b [4]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// mark returns the first person's marker word for what holds it: invented,
// found nowhere else, so finding it anywhere the second person can see is a
// leak.
func (g *rig) mark(what string) string {
	if m, ok := g.marks[what]; ok {
		return m
	}
	m := "quillmark" + strings.ReplaceAll(what, "-", "") + g.suffix
	g.marks[what] = m
	return m
}

// allMarks returns every mark, longest first.
func (g *rig) allMarks() []string {
	var out []string
	for _, m := range g.marks {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// as makes one JSON call as a person and reads the answer into out.
func (g *rig) as(p person, method, path, body string, want int, out any) error {
	rec := g.do(call{method: method, path: api.Prefix + path, token: p.token, body: body})
	if rec.Code != want {
		return fmt.Errorf("%s %s answered %d, want %d: %s", method, path, rec.Code, want, strip(rec.Body.String()))
	}
	if out != nil {
		return json.Unmarshal(rec.Body.Bytes(), out)
	}
	return nil
}

func jsonInto(raw []byte, out any) error { return json.Unmarshal(raw, out) }

// sent is one turn as its stream told it.
type sent struct {
	turnID, requestID string
	end               turn.Turn
	calls             []turn.ToolCallEvent
	results           map[string]turn.ToolResultEvent
	stream            string
}

// send sends a message in a chat as a person and reads the turn to its end.
func (g *rig) send(p person, chatID string, body turn.SendBody) (sent, error) {
	rec := g.do(call{method: "POST", path: api.Prefix + "chats/" + chatID + "/turns", token: p.token, body: jsonOf(body)})
	out := sent{turnID: rec.Header().Get("X-Turn-Id"), requestID: rec.Header().Get("X-Request-Id"), stream: rec.Body.String(),
		results: map[string]turn.ToolResultEvent{}}
	evs := events(out.stream)
	if rec.Code != http.StatusOK || len(evs) == 0 || evs[len(evs)-1].name != turn.EventEnd {
		return out, fmt.Errorf("a turn in %s did not stream to its end: %d %s", p.name, rec.Code, strip(out.stream))
	}
	for _, e := range evs {
		switch e.name {
		case turn.EventEnd:
			json.Unmarshal([]byte(e.data), &out.end)
		case turn.EventToolCall:
			var c turn.ToolCallEvent
			if json.Unmarshal([]byte(e.data), &c) == nil {
				out.calls = append(out.calls, c)
			}
		case turn.EventToolResult:
			var r turn.ToolResultEvent
			if json.Unmarshal([]byte(e.data), &r) == nil {
				out.results[r.ID] = r
			}
		}
	}
	return out, nil
}

// newChat makes a chat as a person.
func (g *rig) newChat(p person) (string, error) {
	var made turn.ChatReply
	err := g.as(p, "POST", "chats", "", http.StatusCreated, &made)
	return made.Chat.ID, err
}

// fillFirst gives the first person data of every kind, through the API as
// they would, and waits until nothing of theirs is still being written.
func (g *rig) fillFirst() error {
	f, one, ctx := &g.first, g.one, context.Background()

	// What the model says in the first person's turns.
	g.model.plan("hello", []seam.Block{text("The blue tin it is. " + g.mark("reply"))})
	g.model.facts[g.mark("message")] = []string{"The lantern ledger code is " + g.mark("fact") + "."}
	g.model.plan("make-artifact",
		[]seam.Block{use(artifacts.CreateName, map[string]string{"type": "markdown", "title": artifactTitle,
			"content": "# Lantern Ledger\n\nThe code is " + g.mark("artifact") + ".\n"})},
		[]seam.Block{use(artifacts.UpdateName, map[string]string{"artifact_id": artifactTitle,
			"old_text": "The code is", "new_text": "The code, " + g.mark("artifact-version") + ", is"})},
		[]seam.Block{text("The ledger is beside the chat.")})
	g.model.plan("gmail", []seam.Block{use("gmail_search", map[string]string{"query": "lunch"})}, []seam.Block{text("Searched.")})
	g.model.plan("ask", []seam.Block{use(jobs.ToolName, map[string]string{"text": "Count the lanterns.", "title": "Lanterns"})}, []seam.Block{text("Asked.")})
	g.model.plan("task-one", []seam.Block{text("The lantern task is done. " + g.mark("task-result"))})

	// The profile and the vault.
	if err := g.as(one, "PUT", "profile", jsonOf(map[string]string{"text": "I keep the harbour lanterns. " + g.mark("profile")}), http.StatusOK, nil); err != nil {
		return err
	}
	for name, value := range map[string]string{secretName: g.mark("secret"), tokenName: g.mark("connector-token")} {
		if err := g.as(one, "PUT", "secrets/"+name, jsonOf(map[string]string{"value": value}), http.StatusOK, nil); err != nil {
			return err
		}
	}

	// A connector, whose server wants the first person's token.
	var row connectors.Row
	if err := g.as(one, "POST", "connectors", jsonOf(connectors.Body{Name: connectorName, Label: "Lantern notes", URL: g.notesURL, Local: true, Secret: tokenName}),
		http.StatusCreated, &row); err != nil {
		return err
	}
	if row.Problem != "" || len(row.Tools) == 0 {
		return fmt.Errorf("the first person's connector lists no tools: %s", row.Problem)
	}
	f.connector = connectorName
	for _, t := range row.Tools {
		f.tools = append(f.tools, t.Name)
	}

	// Google, signed in through the stand-in; then a second sign-in started
	// and not finished, whose state the second person tries.
	var start connectors.GoogleStartReply
	rec := g.do(call{method: "POST", path: api.Prefix + "google/start", token: one.token, body: `{}`, extra: asPage})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &start) != nil {
		return fmt.Errorf("the first person cannot start a Google sign-in: %d %s", rec.Code, strip(rec.Body.String()))
	}
	back, err := g.google.Authorize(start.URL)
	if err != nil {
		return err
	}
	bu, err := url.Parse(back)
	if err != nil {
		return err
	}
	rec = g.do(call{method: "POST", path: api.Prefix + "google/finish", token: one.token, extra: asPage,
		body: jsonOf(connectors.GoogleFinishBody{State: bu.Query().Get("state"), Code: bu.Query().Get("code")})})
	if rec.Code != http.StatusOK {
		return fmt.Errorf("the first person cannot finish a Google sign-in: %d %s", rec.Code, strip(rec.Body.String()))
	}
	rec = g.do(call{method: "POST", path: api.Prefix + "google/start", token: one.token, body: `{}`, extra: asPage})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &start) != nil {
		return fmt.Errorf("the first person cannot start a second Google sign-in: %d", rec.Code)
	}
	if su, err := url.Parse(start.URL); err == nil {
		f.googleState = su.Query().Get("state")
	}
	err = g.with(one.id, func(s *tenant.Store) error {
		refresh, ok, err := s.Secrets().Get(connectors.GoogleSecret)
		if err != nil || !ok {
			return fmt.Errorf("the first person's Google refresh token is not in their vault (%v)", err)
		}
		g.marks["google-refresh"] = refresh
		return nil
	})
	if err != nil {
		return err
	}

	// Chat A: an upload, a message that names it, a title, a star and a pin.
	if f.chatA, err = g.newChat(one); err != nil {
		return err
	}
	var up uploads.AddReply
	rec = g.do(call{method: "POST", path: api.Prefix + "chats/" + f.chatA + "/uploads?name=ledger.txt", token: one.token,
		raw: []byte("The lantern ledger, kept in the blue tin.\n" + g.mark("upload") + "\n")})
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &up) != nil {
		return fmt.Errorf("the first person cannot upload a file: %d %s", rec.Code, strip(rec.Body.String()))
	}
	f.upload = up.ID
	got, err := g.send(one, f.chatA, turn.SendBody{Blocks: []seam.Block{
		text("drill:hello The lantern ledger is kept in the blue tin. " + g.mark("message")),
		{Type: seam.BlockFile, MediaType: up.MediaType, Name: up.Name, Ref: up.ID},
	}})
	if err != nil {
		return err
	}
	f.turnA, f.requestA = got.turnID, got.requestID
	title, star := titleA, true
	if err := g.as(one, "PATCH", "chats/"+f.chatA, jsonOf(turn.PatchBody{Title: &title, Starred: &star}), http.StatusOK, nil); err != nil {
		return err
	}
	var chatA turn.ChatReply
	if err := g.as(one, "GET", "chats/"+f.chatA, "", http.StatusOK, &chatA); err != nil {
		return err
	}
	for _, m := range chatA.Messages {
		if m.Role == "user" {
			f.messageA = m.ID
			break
		}
	}
	if f.messageA == 0 {
		return fmt.Errorf("the first person's chat holds no message of theirs")
	}
	if err := g.as(one, "PUT", fmt.Sprintf("chats/%s/pins/%d", f.chatA, f.messageA), "", http.StatusOK, nil); err != nil {
		return err
	}
	// A share link of chat A (P10-04): its token opens the chat for anyone
	// who holds it, by design; the second person tries every other way.
	var link shareapi.CreateReply
	if err := g.as(one, "POST", "chats/"+f.chatA+"/shares", `{"view":"live"}`, http.StatusCreated, &link); err != nil {
		return err
	}
	f.shareLink, f.shareToken = link.Link.ID, link.Token
	g.marks["share-token"] = link.Token
	// A sub-agent of chat A's turn (P8-02), as a finished row in the first
	// person's own record: the second person asks for it and tries to stop it.
	f.subagent = "5ab0a9e7d1c3f246"
	err = g.with(one.id, func(s *tenant.Store) error {
		_, err := s.DB().Exec(`INSERT INTO subagents(id, chat_id, turn_id, call_id, position, work_chat_id, title, task, request_id, actor, budget_micros, max_steps, status, result, created_at)
			VALUES(?, ?, ?, 'call-drill', 1, ?, 'Lantern check', 'Read the lantern ledger.', 'req-drill-subagent', 'person', 250000, 8, 'done', 'The lantern ledger is in the blue tin.', '2026-11-26T00:00:00Z')`,
			f.subagent, f.chatA, f.turnA, f.chatB)
		return err
	})
	if err != nil {
		return err
	}
	// A research task started from chat A (P8-04), as a finished row in the
	// first person's own record: the second person asks for it and tries to
	// stop it.
	f.research = "7c3e91d0a5b2f684"
	err = g.with(one.id, func(s *tenant.Store) error {
		_, err := s.DB().Exec(`INSERT INTO research(id, chat_id, work_chat_id, title, question, status, request_id, actor, budget_micros, max_parts, sources_per_part, report_words, call_id, summary, note_message_id, created_at)
			VALUES(?, ?, ?, 'Lantern ledger', 'Where is the lantern ledger kept?', 'done', 'req-drill-research', 'person', 3000000, 4, 4, 1200, 'research-drill', 'The lantern ledger is in the blue tin.', 1, '2026-11-26T00:00:00.000Z')`,
			f.research, f.chatA, f.chatB)
		return err
	})
	if err != nil {
		return err
	}
	// A screenshot the agent's browser took in chat A (P10-03): the second
	// person asks for it by its id.
	f.browserShot = "3f9c0a7e5d1b4c2a8e6f0b9d7c5a3e1f"
	err = g.with(one.id, func(s *tenant.Store) error {
		_, err := s.DB().Exec(`INSERT INTO browser_shots(id, chat_id, call_id, url, title, media_type, data, created_at)
			VALUES(?, ?, 'call-drill-shot', 'https://harbour.example/lantern', 'Lantern ledger', 'image/jpeg', ?, '2026-11-27T00:00:00.000Z')`,
			f.browserShot, f.chatA, []byte{0xff, 0xd8, 0xff, 0xd9})
		return err
	})
	if err != nil {
		return err
	}
	// The memory pass of chat A's turn, into the built-in store.
	err = waitFor("the first person's memory pass", 20*time.Second, func() (bool, error) {
		var p memory.Pass
		var ok bool
		err := g.with(one.id, func(s *tenant.Store) (err error) { p, ok, err = memory.Get(ctx, s, f.turnA); return })
		if err != nil || !ok || p.Status == memory.StatusQueued || p.Status == memory.StatusRunning {
			return false, err
		}
		if p.SourceID == "" {
			return false, fmt.Errorf("the first person's memory pass kept nothing: %s %s", p.Status, p.Reason)
		}
		f.memorySource = p.SourceID
		return true, nil
	})
	if err != nil {
		return err
	}

	// Chat B: an artifact in two versions, attached to A; and a handoff.
	if f.chatB, err = g.newChat(one); err != nil {
		return err
	}
	if _, err := g.send(one, f.chatB, turn.SendBody{Text: "drill:make-artifact"}); err != nil {
		return err
	}
	var arts artifacts.ListReply
	if err := g.as(one, "GET", "chats/"+f.chatB+"/artifacts", "", http.StatusOK, &arts); err != nil {
		return err
	}
	if len(arts.Artifacts) != 1 {
		return fmt.Errorf("the first person's chat holds %d artifacts, want 1", len(arts.Artifacts))
	}
	f.artifact = arts.Artifacts[0].ID
	if err := g.as(one, "PUT", "chats/"+f.chatA+"/attached/"+f.chatB, "", http.StatusOK, nil); err != nil {
		return err
	}
	var handed turn.HandoffReply
	if err := g.as(one, "POST", "chats/"+f.chatA+"/handoff", "", http.StatusCreated, &handed); err != nil {
		return err
	}
	f.chatC = handed.Chat.Chat.ID

	// A style, a project with a file, and a schedule.
	name, words := styleName, "Write as the harbour master. "+g.mark("style")
	var st styles.Style
	if err := g.as(one, "POST", "styles", jsonOf(turn.StyleBody{Name: &name, Instructions: &words}), http.StatusCreated, &st); err != nil {
		return err
	}
	f.style = st.ID
	if err := g.as(one, "PUT", "chats/"+f.chatA+"/style", jsonOf(turn.ChatStyleBody{Style: st.ID}), http.StatusOK, nil); err != nil {
		return err
	}
	pname, instructions := projectName, "Keep the harbour lanterns lit. "+g.mark("project")
	var proj turn.ProjectReply
	if err := g.as(one, "POST", "projects", jsonOf(turn.ProjectBody{Name: &pname, Instructions: &instructions}), http.StatusCreated, &proj); err != nil {
		return err
	}
	f.project = proj.Project.ID
	var pf turn.ProjectFileReply
	rec = g.do(call{method: "POST", path: api.Prefix + "projects/" + f.project + "/files?name=harbour.md", token: one.token,
		raw: []byte("# Harbour\n\n" + g.mark("project-file") + "\n")})
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &pf) != nil {
		return fmt.Errorf("the first person cannot add a project file: %d %s", rec.Code, strip(rec.Body.String()))
	}
	f.projectFile = pf.ID
	if err := g.as(one, "PUT", "projects/"+f.project+"/chats/"+f.chatA, "", http.StatusOK, nil); err != nil {
		return err
	}
	off := false
	var sch schedules.Schedule
	if err := g.as(one, "POST", "schedules", jsonOf(schedules.ScheduleBody{Name: scheduleName, Prompt: "drill:task-one " + g.mark("schedule"),
		Recurrence: schedules.Recurrence{Kind: schedules.KindDaily, Time: "03:17"}, Enabled: &off}), http.StatusCreated, &sch); err != nil {
		return err
	}
	f.schedule = sch.ID

	// Knowledge the person keeps, as an import or a wiki job writes it.
	err = g.with(one.id, func(s *tenant.Store) error {
		page, err := builtin.WritePage(ctx, s, builtin.NewPage{Space: spaceName, Path: "ledger/code", Title: "Lantern Ledger Code", Body: "The ledger's code is " + g.mark("page") + "."})
		if err != nil {
			return err
		}
		src, err := builtin.AddText(ctx, s, builtin.NewText{Space: spaceName, Name: "Lantern notes", Origin: "drill", Dated: "2026-11-01", Text: "Notes on the lanterns. " + g.mark("source")})
		f.page, f.source = page.ID, src.ID
		return err
	})
	if err != nil {
		return err
	}

	// A background task from chat A, to its end.
	var task jobs.Task
	if err := g.as(one, "POST", "chats/"+f.chatA+"/tasks", jsonOf(jobs.StartBody{Text: "drill:task-one " + g.mark("task"), Title: "Lantern task"}), http.StatusCreated, &task); err != nil {
		return err
	}
	f.task, f.taskChat = task.ID, task.WorkChatID
	if err := g.taskEnds(one, f.chatA, f.task); err != nil {
		return err
	}

	// An incognito chat, a Gmail search in chat C, and a tool policy.
	var incognito turn.ChatReply
	if err := g.as(one, "POST", "chats/incognito", "", http.StatusCreated, &incognito); err != nil {
		return err
	}
	f.incognito = incognito.Chat.ID
	before := len(g.google.Calls())
	if _, err := g.send(one, f.chatC, turn.SendBody{Text: "drill:gmail"}); err != nil {
		return err
	}
	for _, c := range g.google.Calls()[before:] {
		if c.Auth != "" {
			f.googleAuth = append(f.googleAuth, c.Auth)
		}
	}
	if len(f.googleAuth) == 0 {
		return fmt.Errorf("the first person's Gmail search reached the stand-in Google with no credentials")
	}
	if err := g.as(one, "PUT", "tools/current_time/policy", jsonOf(turn.PolicyBody{Policy: "ask"}), http.StatusOK, nil); err != nil {
		return err
	}

	// Chat D: a turn that waits for the person's approval. The client
	// leaves; the turn waits on.
	if f.chatD, err = g.newChat(one); err != nil {
		return err
	}
	rec = g.do(call{method: "POST", path: api.Prefix + "chats/" + f.chatD + "/turns", token: one.token,
		body: jsonOf(turn.SendBody{Text: "drill:ask"}), timeout: 1500 * time.Millisecond})
	f.turnD = rec.Header().Get("X-Turn-Id")
	err = waitFor("the first person's approval", 10*time.Second, func() (bool, error) {
		var id string
		// No row yet is no error: the turn may still be on its way to it.
		g.with(one.id, func(s *tenant.Store) error {
			return s.DB().QueryRow("SELECT id FROM tool_approvals WHERE status = 'pending' AND turn_id = ?", f.turnD).Scan(&id)
		})
		f.approval = id
		return id != "", nil
	})
	if err != nil {
		return err
	}
	return g.quiet(one)
}

// taskEnds waits for a background task to end and its note to be written.
func (g *rig) taskEnds(p person, chatID, taskID string) error {
	return waitFor("a background task of "+p.name, 30*time.Second, func() (bool, error) {
		var t jobs.Task
		rec := g.do(call{method: "GET", path: api.Prefix + "chats/" + chatID + "/tasks/" + taskID, token: p.token})
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &t) != nil {
			return false, fmt.Errorf("a task of %s cannot be read: %d", p.name, rec.Code)
		}
		switch t.Status {
		case jobs.StatusQueued, jobs.StatusRunning:
			return false, nil
		case jobs.StatusDone:
			return t.NoteMessageID != 0, nil
		}
		return false, fmt.Errorf("a task of %s ended as %s: %+v", p.name, t.Status, t.Error)
	})
}

// quiet waits until a person's memory passes are done and their folder and
// database have stopped changing.
func (g *rig) quiet(p person) error {
	ctx := context.Background()
	err := waitFor("the memory passes of "+p.name, 20*time.Second, func() (bool, error) {
		var open int
		err := g.with(p.id, func(s *tenant.Store) error {
			return s.DB().QueryRowContext(ctx, "SELECT count(*) FROM memory_passes WHERE status IN (?, ?)", memory.StatusQueued, memory.StatusRunning).Scan(&open)
		})
		return open == 0, err
	})
	if err != nil {
		return err
	}
	var last string
	return waitFor("the folder of "+p.name+" to stop changing", 20*time.Second, func() (bool, error) {
		l, err := g.lookAt(p.id)
		if err != nil {
			return false, err
		}
		now := l.sum()
		same := now == last
		last = now
		if !same {
			time.Sleep(150 * time.Millisecond)
		}
		return same, nil
	})
}

// idText is an id for a report line: the first person's ids are written as
// what they are, never as themselves.
func (g *rig) idText(s string) string {
	f := g.first
	for _, r := range []struct{ id, as string }{
		{f.chatA, "<one's chat>"}, {f.chatB, "<one's attached chat>"}, {f.chatC, "<one's handed-off chat>"}, {f.chatD, "<one's waiting chat>"},
		{f.incognito, "<one's incognito chat>"}, {f.turnA, "<one's turn>"}, {f.turnD, "<one's waiting turn>"}, {f.approval, "<one's approval>"},
		{f.upload, "<one's upload>"}, {f.artifact, "<one's artifact>"}, {f.project, "<one's project>"}, {f.projectFile, "<one's project file>"},
		{f.style, "<one's style>"}, {f.schedule, "<one's schedule>"}, {f.task, "<one's task>"}, {f.taskChat, "<one's task chat>"},
		{f.requestA, "<one's request>"}, {f.googleState, "<one's Google state>"}, {f.shareToken, "<one's share token>"}, {f.shareLink, "<one's share link>"}, {f.subagent, "<one's sub-agent>"},
		{g.second.shareToken, "<two's share token>"}, {g.one.id, "<one's tenant>"},
		{g.second.chat, "<two's chat>"}, {g.second.attached, "<two's attached chat>"}, {g.second.project, "<two's project>"},
		{g.second.artifact, "<two's artifact>"}, {g.second.upload, "<two's upload>"}, {g.two.id, "<two's tenant>"},
	} {
		if r.id != "" {
			s = strings.ReplaceAll(s, r.id, r.as)
		}
	}
	if f.research != "" { // P8-04
		s = strings.ReplaceAll(s, f.research, "<one's research task>")
	}
	if f.browserShot != "" { // P10-03
		s = strings.ReplaceAll(s, f.browserShot, "<one's screenshot>")
	}
	return s
}

// longIDs are the first person's ids long enough that finding one in an
// answer means it came from their data, not from a count both people have.
func (g *rig) longIDs() []string {
	f := g.first
	var out []string
	for _, id := range []string{f.chatA, f.chatB, f.chatC, f.chatD, f.incognito, f.turnA, f.turnD, f.approval, f.upload, f.artifact,
		f.project, f.projectFile, f.style, f.schedule, f.task, f.taskChat, f.requestA, f.googleState, f.shareLink, f.subagent} {
		if len(id) >= 12 {
			out = append(out, id)
		}
	}
	if len(f.research) >= 12 { // P8-04
		out = append(out, f.research)
	}
	if len(f.browserShot) >= 12 { // P10-03
		out = append(out, f.browserShot)
	}
	return out
}
