package isolationdrill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/attach"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/connectors"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/mcp/mcptest"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools/builtin"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// second is what the second person has of their own: a chat with another
// of theirs attached (so open_attached_chat is offered in it), an upload, a
// project, an artifact, and two connectors.
type second struct {
	chat, attached, project, artifact, upload string
	// shareToken is a share link of their own chat, made in the API part.
	shareToken string
	// openTools are the tools of their connector to an open server, and
	// notesTools those of their connector to the first person's server,
	// with the first person's token name.
	openTools, notesTools []string
}

// The second person's connectors and the model that names the first
// person's key.
const (
	openConnector  = "quayopen"
	notesConnector = "quaynotes"
	vaultProbe     = "vault-probe"
)

// fillSecond gives the second person things of their own to try from.
func (g *rig) fillSecond() error {
	s, two := &g.second, g.two
	g.model.plan("two-hello", []seam.Block{text("Rowing it is.")})
	g.model.facts["south quay"] = []string{"The person rows at the south quay."}
	g.model.plan("two-artifact", []seam.Block{use(artifacts.CreateName, map[string]string{"type": "markdown", "title": "Quay Notes", "content": "Rowing times at the south quay.\n"})},
		[]seam.Block{text("Made.")})
	g.model.plan("noop", []seam.Block{text("Nothing to do.")})

	var err error
	if s.attached, err = g.newChat(two); err != nil {
		return err
	}
	if _, err := g.send(two, s.attached, turn.SendBody{Text: "drill:two-hello I row at the south quay."}); err != nil {
		return err
	}
	if _, err := g.send(two, s.attached, turn.SendBody{Text: "drill:two-artifact"}); err != nil {
		return err
	}
	var arts artifacts.ListReply
	if err := g.as(two, "GET", "chats/"+s.attached+"/artifacts", "", http.StatusOK, &arts); err != nil {
		return err
	}
	if len(arts.Artifacts) != 1 {
		return fmt.Errorf("the second person's chat holds %d artifacts, want 1", len(arts.Artifacts))
	}
	s.artifact = arts.Artifacts[0].ID
	if s.chat, err = g.newChat(two); err != nil {
		return err
	}
	if err := g.as(two, "PUT", "chats/"+s.chat+"/attached/"+s.attached, "", http.StatusOK, nil); err != nil {
		return err
	}
	rec := g.do(call{method: "POST", path: api.Prefix + "chats/" + s.chat + "/uploads?name=quay.txt", token: two.token, raw: []byte("The south quay timetable.\n")})
	var up struct {
		ID string `json:"id"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &up) != nil {
		return fmt.Errorf("the second person cannot upload a file: %d", rec.Code)
	}
	s.upload = up.ID
	var proj turn.ProjectReply
	pname := "Quay Works"
	if err := g.as(two, "POST", "projects", jsonOf(turn.ProjectBody{Name: &pname}), http.StatusCreated, &proj); err != nil {
		return err
	}
	s.project = proj.Project.ID

	// A connector to an open server, and one to the first person's server
	// that names the first person's token: the second person's vault has no
	// such secret, so no token may be sent.
	for _, c := range []struct {
		body  connectors.Body
		tools *[]string
	}{
		{connectors.Body{Name: openConnector, Label: "Open notes", URL: g.openURL, Local: true}, &s.openTools},
		{connectors.Body{Name: notesConnector, Label: "Their notes", URL: g.notesURL, Local: true, Secret: tokenName}, &s.notesTools},
	} {
		rec := g.do(call{method: "POST", path: api.Prefix + "connectors", token: two.token, body: jsonOf(c.body)})
		var row connectors.Row
		json.Unmarshal(rec.Body.Bytes(), &row)
		for _, t := range row.Tools {
			*c.tools = append(*c.tools, t.Name)
		}
		g.report.tried(AreaTools, fmt.Sprintf("the second person adds a connector %s to %s", c.body.Name, map[bool]string{true: "the first person's server, naming the first person's token", false: "an open server"}[c.body.Secret != ""]),
			fmt.Sprintf("%s, token present %v, %d tools, problem %q", short(rec), row.TokenPresent, len(row.Tools), strip(row.Problem)))
	}
	if len(s.openTools) == 0 {
		return fmt.Errorf("the second person's connector to an open server lists no tools")
	}
	// The first person's server answers only their token: a list from it
	// means the token went with the second person's call.
	if len(s.notesTools) > 0 {
		g.report.reached("tools: the second person's connector to the first person's server, naming their token, was given its tools")
	}

	// A model that names the first person's key in its entry.
	probe := modelTwo
	probe.Name, probe.Key = vaultProbe, secretName
	if err := g.as(two, "POST", "models", jsonOf(probe), http.StatusCreated, nil); err != nil {
		return err
	}
	return g.quiet(two)
}

// battery is the steps of a turn whose model calls every tool a turn may
// offer, with the first person's ids, titles and hashes: the tools that
// read and change Keystone's records first, then the connectors' tools,
// once load_tools has given their definitions.
func (g *rig) battery() [][]seam.Block {
	f, s := g.first, g.second
	first := []seam.Block{
		use(builtin.SearchChats, map[string]string{"query": "Lantern Accounts"}),
		use(builtin.SearchChats, map[string]string{"query": knownWords}),
		use(builtin.ReadChat, map[string]string{"chat_id": f.chatA}),
		use(builtin.ReadChat, map[string]string{"chat_id": f.chatB}),
		use(builtin.ReadChat, map[string]string{"chat_id": f.taskChat}),
		use(builtin.ReadChat, map[string]string{"chat_id": f.incognito}),
		use(builtin.ReadChat, map[string]string{"chat_id": "../" + g.one.id + "/" + f.chatA}),
		use(attach.ToolName, map[string]string{"chat_id": f.chatB}),
		use(attach.ToolName, map[string]any{"chat_id": f.chatA, "page": 1}),
		use(recall.LookupSearch, map[string]string{"query": knownWords}),
		use(recall.LookupSearch, map[string]string{"query": "lantern ledger code", "space": spaceName}),
		use(recall.LookupReadPage, map[string]string{"id": f.page}),
		use(recall.LookupReadSource, map[string]string{"id": f.source}),
		use(recall.LookupReadSource, map[string]string{"id": f.memorySource}),
		use(artifacts.ReadName, map[string]string{"artifact_id": f.artifact}),
		use(artifacts.ReadName, map[string]string{"artifact_id": artifactTitle}),
		use(artifacts.ReadName, map[string]any{"artifact_id": f.artifact, "version": 1}),
		use(artifacts.UpdateName, map[string]string{"artifact_id": f.artifact, "old_text": "The code", "new_text": "Taken by the second person"}),
		use(artifacts.UpdateName, map[string]string{"artifact_id": artifactTitle, "content": "Overwritten by the second person."}),
		use(connectors.LoadName, map[string][]string{"names": append(append(append([]string(nil), f.tools...), s.openTools...), s.notesTools...)}),
		use("gmail_search", map[string]string{"query": "lunch"}),
		use("gmail_read", map[string]string{"message_id": "lunch-1"}),
		use("calendar_list_events", map[string]string{}),
		use("calendar_update_event", map[string]string{"event_id": "event-1", "title": "Taken"}),
	}
	var second []seam.Block
	for _, name := range f.tools {
		second = append(second, use(name, map[string]string{"query": "lantern"}))
	}
	for _, name := range append(append([]string(nil), s.openTools...), s.notesTools...) {
		if strings.HasSuffix(name, "whoami") || strings.HasSuffix(name, "search") {
			second = append(second, use(name, map[string]string{"query": "lantern"}))
		}
	}
	return [][]seam.Block{first, second, {text("Those were the tries.")}}
}

// approveAll approves every call of a person's that waits for approval,
// until stop is called, so a try that asks still runs and is refused, or
// not, where it runs.
func (g *rig) approveAll(p person) (stop func()) {
	quit, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		answered := map[string]bool{}
		for {
			select {
			case <-quit:
				return
			case <-time.After(25 * time.Millisecond):
			}
			type pending struct{ id, turn, chat string }
			var waiting []pending
			g.with(p.id, func(s *tenant.Store) error {
				rows, err := s.DB().Query("SELECT id, turn_id, chat_id FROM tool_approvals WHERE status = 'pending'")
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var a pending
					if rows.Scan(&a.id, &a.turn, &a.chat) == nil && !answered[a.id] {
						waiting = append(waiting, a)
					}
				}
				return nil
			})
			for _, a := range waiting {
				answered[a.id] = true
				g.do(call{method: "POST", path: api.Prefix + "chats/" + a.chat + "/turns/" + a.turn + "/approvals/" + a.id + "/approve", token: p.token})
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(quit); <-finished }) }
}

// tools is the drill's part on the tools a turn offers.
func (g *rig) tools() error {
	two := g.two
	g.model.plan("tools", g.battery()...)
	for _, name := range []string{artifacts.UpdateName, artifacts.ReadName, connectors.LoadName} {
		g.do(call{method: "PUT", path: api.Prefix + "tools/" + name + "/policy", token: two.token, body: `{"policy":"always"}`})
	}
	stop := g.approveAll(two)
	defer stop()
	googleBefore, notesBefore, openBefore := len(g.google.Calls()), len(g.notes.Calls()), len(g.openMCP.Calls())

	got, err := g.send(two, g.second.chat, turn.SendBody{Text: "drill:tools"})
	if err != nil {
		return err
	}
	stop()
	var results []callResult
	for _, c := range got.calls {
		r := got.results[c.ID]
		results = append(results, callResult{id: c.ID, name: c.Name, input: string(c.Input), text: seam.Text(seam.Message{Blocks: r.Content}), isError: r.IsError})
	}
	if len(results) < 20 {
		return fmt.Errorf("the second person's turn made %d tool calls, and the battery has more: %s", len(results), strip(got.stream))
	}
	if err := g.judgeCalls(AreaTools, "a turn of the second person's", got.requestID, results); err != nil {
		return err
	}

	// Gmail and Calendar: the second person has no Google sign-in, so the
	// stand-in must not have been called at all; and the first person's
	// connector token must not have gone with any of the second person's
	// calls to either server.
	if n := len(g.google.Calls()) - googleBefore; n != 0 {
		g.report.reached("tools: the second person's Gmail and Calendar calls reached the stand-in Google %d times, with no sign-in of theirs", n)
	}
	g.report.tried(AreaTools, "Gmail and Calendar calls of the second person's, who has not signed in to Google, against the stand-in", fmt.Sprintf("%d calls reached the stand-in", len(g.google.Calls())-googleBefore))
	g.tokenNotSent(AreaTools, notesBefore, openBefore)

	// A model that names the first person's key, and the first person's
	// model by name.
	for _, try := range []struct{ model, what string }{
		{vaultProbe, "a turn on the second person's model whose entry names the first person's key"},
		{modelOne.Name, "a turn on the first person's model, by name"},
	} {
		chatID, err := g.newChat(two)
		if err != nil {
			return err
		}
		got, err := g.send(two, chatID, turn.SendBody{Text: "drill:noop", Model: try.model})
		if err != nil {
			return err
		}
		result := fmt.Sprintf("the turn ended %s", got.end.Status)
		if got.end.Error != nil {
			result += ": " + got.end.Error.Message
		}
		if got.end.Status == turn.StatusDone {
			g.report.reached("tools: %s was answered", try.what)
		}
		g.report.tried(AreaTools, try.what, result+"; "+g.judge(AreaTools, try.what, got.stream, "", false))
	}
	for _, key := range g.model.openedWith() {
		if key == g.mark("secret") {
			g.report.reached("tools: a model was opened with the first person's key")
		}
	}
	return nil
}

// callResult is one tool call and what it returned.
type callResult struct {
	id, name, input, text string
	isError               bool
}

// judgeCalls looks for the first person's marks in every call's result,
// records each try, and checks each is in the second person's audit log
// under the request id: as a tool call before and after, or, for a tool the
// person does not have, in the model call that asked for it.
func (g *rig) judgeCalls(area, where, requestID string, results []callResult) error {
	var entries []audit.Entry
	err := g.with(g.two.id, func(s *tenant.Store) (err error) {
		entries, err = audit.List(context.Background(), s, audit.Filter{RequestID: requestID})
		return err
	})
	if err != nil {
		return err
	}
	if requestID == "" || len(entries) == 0 {
		g.report.reached("%s: %s left nothing in the second person's audit log", area, where)
	}
	for _, r := range results {
		tried := fmt.Sprintf("%s %s in %s", r.name, g.idText(r.input), where)
		result := g.judge(area, tried, r.text, r.input, false)
		if r.isError {
			result = "refused: " + strip(g.idText(r.text)) + "; " + result
		} else {
			result = "answered: " + strip(g.idText(r.text)) + "; " + result
		}
		inLog := ""
		for _, e := range entries {
			switch {
			case e.Kind == audit.KindTool && e.Action == r.name && e.Outcome == audit.OutcomeStarted && strings.Contains(e.Sent, strings.Trim(r.input, "{}")):
				inLog = "as a tool call"
			case inLog == "" && e.Kind == audit.KindModel && e.Outcome != audit.OutcomeStarted && strings.Contains(e.Received, r.id):
				inLog = "in the model call that asked for it"
			}
		}
		if inLog == "" {
			g.report.reached("%s: %s is not in the second person's audit log", area, tried)
			inLog = "NOT AUDITED"
		}
		g.report.tried(area, tried, result+"; in the second person's log "+inLog)
	}
	return nil
}

// tokenNotSent checks the calls the test MCP servers took since the counts
// given: none may carry the first person's connector token.
func (g *rig) tokenNotSent(area string, notesBefore, openBefore int) {
	token := g.mark("connector-token")
	n := 0
	for name, calls := range map[string][]string{"the first person's server": authOf(g.notes.Calls()[notesBefore:]), "an open server": authOf(g.openMCP.Calls()[openBefore:])} {
		for _, auth := range calls {
			n++
			if strings.Contains(auth, token) {
				g.report.reached("%s: a call of the second person's reached %s with the first person's connector token", area, name)
			}
		}
	}
	g.report.tried(area, "the first person's connector token, looked for in every call the second person's connectors made", fmt.Sprintf("%d calls, none with it", n))
}

func authOf(calls []mcptest.Call) []string {
	var out []string
	for _, c := range calls {
		out = append(out, c.Auth)
	}
	return out
}
