package turn

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
)

// Invented, and used in throwaway folders only.
const ordinaryPassword = "an ordinary person's phrase"

// rig is one data folder with the test tenant and an ordinary tenant in
// it. serve starts a server on it, and can be called again to stand for
// the server starting a second time.
type rig struct {
	t        *testing.T
	install  *tenant.Install
	fx       testtenant.Fixtures
	signIn   signin.Options
	test     tenant.Tenant // the test tenant
	ordinary tenant.Tenant // a tenant with no models
	// extra are adapters a test adds to the fake.
	extra adapters.Set
}

func newRig(t *testing.T) *rig {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	fx, err := testtenant.Shipped()
	if err != nil {
		t.Fatal(err)
	}
	o, err := testtenant.DefaultOptions()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := testtenant.Load(in, fx, o)
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := in.Create("Ordinary Person")
	if err != nil {
		t.Fatal(err)
	}
	g := &rig{t: t, install: in, fx: fx, signIn: o.SignIn, test: loaded.Tenant, ordinary: ordinary}
	g.with(ordinary.ID, func(s *tenant.Store) {
		if err := signin.SetPassword(s, ordinaryPassword, o.SignIn); err != nil {
			t.Fatal(err)
		}
	})
	return g
}

// with runs f on a tenant's store.
func (g *rig) with(tenantID string, f func(s *tenant.Store)) {
	g.t.Helper()
	s, err := g.install.Store(tenantID)
	if err != nil {
		g.t.Fatal(err)
	}
	defer s.Close()
	f(s)
}

// served is one start of the server on the rig's data folder.
type served struct {
	g      *rig
	runner *Runner
	server *httptest.Server
	tokens map[string]string
}

// serve starts a server. A nil provider gives the test tenant the fake
// provider playing the shipped recorded turns, with no wait.
func (g *rig) serve(provider seam.Provider) *served {
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
	for name, build := range g.extra {
		set[name] = build
	}
	runner := NewRunner(Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Adapters: set})
	runner.Recover()
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, Routes(runner))
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

// stop ends this start of the server, as stopping the process would: the
// turns it was working are left as they are in the database.
func (sv *served) stop() {
	sv.server.CloseClientConnections()
	sv.server.Close()
	sv.runner.Close()
}

func (sv *served) request(tenantID, method, path, body string, header map[string]string) *http.Response {
	sv.g.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, sv.server.URL+api.Prefix+path, rd)
	if err != nil {
		sv.g.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	req.AddCookie(&http.Cookie{Name: api.CookieName, Value: sv.tokens[tenantID]})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		sv.g.t.Fatal(err)
	}
	return resp
}

// call makes a call that answers with JSON, checks its status and reads
// the answer into out.
func (sv *served) call(tenantID, method, path, body string, want int, out any) {
	sv.g.t.Helper()
	resp := sv.request(tenantID, method, path, body, nil)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		sv.g.t.Fatalf("%s %s answered %d, want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			sv.g.t.Fatalf("%s %s: %v in %s", method, path, err, raw)
		}
	}
}

func (sv *served) newChat(tenantID string) string {
	sv.g.t.Helper()
	var made ChatReply
	sv.call(tenantID, "POST", "chats", "", http.StatusCreated, &made)
	return made.Chat.ID
}

// event is one server-sent event as a client reads it.
type event struct {
	id   string
	name string
	data string
}

func (e event) into(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(e.data), v); err != nil {
		t.Fatalf("the %s event's data does not read: %v in %s", e.name, err, e.data)
	}
}

// readEvents reads a stream until it closes, calling each for every event.
// each may be nil.
func readEvents(t *testing.T, body io.Reader, each func(event)) []event {
	t.Helper()
	var all []event
	var cur event
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			cur.id = line[4:]
		case strings.HasPrefix(line, "event: "):
			cur.name = line[7:]
		case strings.HasPrefix(line, "data: "):
			cur.data = line[6:]
		case line == "" && cur.name != "":
			all = append(all, cur)
			if each != nil {
				each(cur)
			}
			cur = event{}
		}
	}
	return all
}

// send sends a message and reads the whole stream.
func (sv *served) send(tenantID, chatID, body string) (events []event, turnID, requestID string) {
	sv.g.t.Helper()
	resp := sv.request(tenantID, "POST", "chats/"+chatID+"/turns", body, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		raw, _ := io.ReadAll(resp.Body)
		sv.g.t.Fatalf("sending a message answered %d %s: %s", resp.StatusCode, resp.Header.Get("Content-Type"), raw)
	}
	return readEvents(sv.g.t, resp.Body, nil), resp.Header.Get("X-Turn-Id"), resp.Header.Get("X-Request-Id")
}

func names(events []event) string {
	var out []string
	for _, e := range events {
		if n := len(out); n > 0 && (e.name == EventText || e.name == EventThinking) && out[n-1] == e.name {
			continue // a run of deltas reads as one
		}
		out = append(out, e.name)
	}
	return strings.Join(out, " ")
}

// joined is the text of every event of one name, in order.
func joined(t *testing.T, events []event, name string) string {
	t.Helper()
	text := ""
	for _, e := range events {
		if e.name == name {
			var d DeltaEvent
			e.into(t, &d)
			text += d.Text
		}
	}
	return text
}

func last(t *testing.T, events []event) Turn {
	t.Helper()
	if len(events) == 0 || events[len(events)-1].name != EventEnd {
		t.Fatalf("the stream did not end with an end event: %s", names(events))
	}
	var turn Turn
	events[len(events)-1].into(t, &turn)
	return turn
}

const bakery = "Suggest three names for a small bakery by the harbour."

// The item's first proof: a turn streams to a client.
func TestATurnStreamsToAClient(t *testing.T) {
	g := newRig(t)
	sv := g.serve(nil)
	id := sv.newChat(g.test.ID)

	body, _ := json.Marshal(SendBody{Text: bakery})
	events, turnID, requestID := sv.send(g.test.ID, id, string(body))
	if got, want := names(events), "turn turn message start thinking text message usage title end"; got != want {
		t.Fatalf("the stream was\n\t%s\nwant\n\t%s", got, want)
	}
	for i, e := range events {
		if e.id != itoa(i+1) {
			t.Fatalf("event %d has the id %q: ids count from 1 with no gaps", i+1, e.id)
		}
	}
	var first Turn
	events[0].into(t, &first)
	if first.ID != turnID || first.Status != StatusQueued || first.ChatID != id {
		t.Errorf("the first event is %+v, and the X-Turn-Id header is %q", first, turnID)
	}
	if text := joined(t, events, EventText); !strings.Contains(text, "Tide and Crumb") || !strings.Contains(text, "what a bakery is for.") {
		t.Errorf("the text deltas join to %q", text)
	}
	if n := strings.Count(names(events[:len(events)]), "text"); n != 1 {
		t.Errorf("want one run of text deltas, got %d", n)
	}
	if text := joined(t, events, EventThinking); !strings.HasPrefix(text, "A small bakery by a harbour.") {
		t.Errorf("the thinking deltas join to %q", text)
	}
	end := last(t, events)
	if end.Status != StatusDone || end.Model != TestModelName || end.StopReason != "end" || end.Usage.OutputTokens != 52 ||
		end.UserMessageID == 0 || end.ReplyMessageID <= end.UserMessageID || end.Ended == nil || end.LastEventID != int64(len(events)) {
		t.Errorf("the turn ended as %+v", end)
	}

	// The record holds the message and the reply, with what the turn recorded.
	var got ChatReply
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &got)
	if len(got.Messages) != 2 || len(got.Turns) != 0 || got.Chat.Busy {
		t.Fatalf("the chat reads back as %d messages, %d open turns, busy %v", len(got.Messages), len(got.Turns), got.Chat.Busy)
	}
	reply := got.Messages[1]
	if reply.Role != seam.RoleAssistant || reply.TurnID != turnID || reply.Model != TestModelName || reply.StopReason != "end" ||
		reply.Usage == nil || reply.Usage.InputTokens != 24 || len(reply.Blocks) != 2 || reply.Blocks[0].Signature != "sig-test-0101" {
		t.Errorf("the reply reads back as %+v", reply)
	}
	if got.Messages[0].TurnID != turnID || got.Messages[0].Model != "" {
		t.Errorf("the person's message reads back as %+v", got.Messages[0])
	}
	// The title is made from the message, because the fake writes none.
	if got.Chat.Title != "Suggest three names for a small" {
		t.Errorf("the chat's title is %q", got.Chat.Title)
	}
	if got.Model.Source != SourceTest || got.Model.Model == nil || !got.Model.Model.Test {
		t.Errorf("the test tenant's model reads as %+v", got.Model)
	}

	// The model call is in the audit log, before and after, under the
	// request id of the call that sent the message.
	g.with(g.test.ID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 || entries[0].Outcome != audit.OutcomeStarted || entries[1].Outcome != audit.OutcomeOK ||
			entries[0].Action != TestModelName || !strings.Contains(entries[0].Sent, "small bakery") ||
			!strings.Contains(entries[1].Received, "Tide and Crumb") || entries[1].Usage.OutputTokens != 52 {
			t.Errorf("the model call is recorded as %+v", entries)
		}
	})

	// A client that lost its connection after event 5 gets the rest.
	resp := sv.request(g.test.ID, "GET", "chats/"+id+"/turns/"+turnID+"/events", "", map[string]string{"Last-Event-ID": "5"})
	rest := readEvents(t, resp.Body, nil)
	resp.Body.Close()
	if len(rest) != len(events)-5 || rest[0].id != "6" || rest[0].data != events[5].data || rest[len(rest)-1].name != EventEnd {
		t.Errorf("resuming after event 5 gave %d events starting at %q, want %d starting at 6", len(rest), rest[0].id, len(events)-5)
	}
	resp = sv.request(g.test.ID, "GET", "chats/"+id+"/turns/"+turnID+"/events?after="+itoa(len(events)-1), "", nil)
	rest = readEvents(t, resp.Body, nil)
	resp.Body.Close()
	if len(rest) != 1 || rest[0].name != EventEnd {
		t.Errorf("resuming with after gave %s", names(rest))
	}

	// A second turn in the chat: the model asks for a tool. There is no
	// tool loop yet, so the call is answered with an error and the model
	// is called again. The chat keeps its title.
	body, _ = json.Marshal(SendBody{Text: "When does the north ferry leave on Tuesday?"})
	events, _, _ = sv.send(g.test.ID, id, string(body))
	if got, want := names(events), "turn turn message start text tool_call message usage tool_result message start text message usage end"; got != want {
		t.Fatalf("the stream of a turn with a tool call was\n\t%s\nwant\n\t%s", got, want)
	}
	if end := last(t, events); end.Status != StatusDone || end.Usage.InputTokens != 550 || end.StopReason != "end" {
		t.Errorf("the turn with a tool call ended as %+v", end)
	}
	for _, e := range events {
		if e.name == EventToolCall {
			var c ToolCallEvent
			e.into(t, &c)
			if c.Name != "lookup_timetable" || !strings.Contains(string(c.Input), `"north"`) || c.Step != 1 {
				t.Errorf("the tool call arrived as %+v", c)
			}
		}
	}

	// Retry the last reply: no new message, and a branch beside the old one.
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &got)
	old := got.Messages[len(got.Messages)-1]
	body, _ = json.Marshal(SendBody{RetryOf: old.ID})
	events, _, _ = sv.send(g.test.ID, id, string(body))
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the retry ended as %+v: %s", end, names(events))
	}
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &got)
	again := got.Messages[len(got.Messages)-1]
	if again.ID == old.ID || len(again.Siblings) != 2 || again.ParentID != old.ParentID {
		t.Errorf("after a retry the last message is %+v, and the one retried was %d", again, old.ID)
	}

	// The list, search, a new name, a star, and deletion.
	var list ListReply
	sv.call(g.test.ID, "GET", "chats?limit=2", "", http.StatusOK, &list)
	if len(list.Chats) != 2 || !list.More || list.Chats[0].ID != id {
		t.Errorf("the list reads as %+v", list)
	}
	var found SearchReply
	sv.call(g.test.ID, "GET", "chats/search?q=crumb", "", http.StatusOK, &found)
	if !slices.ContainsFunc(found.Hits, func(h chat.Hit) bool { return h.ChatID == id && h.MessageID != 0 }) {
		t.Errorf("search found %+v", found.Hits)
	}
	var changed Chat
	sv.call(g.test.ID, "PATCH", "chats/"+id, `{"title":"Names for the bakery","starred":true}`, http.StatusOK, &changed)
	if changed.Title != "Names for the bakery" || !changed.Starred {
		t.Errorf("the chat was changed to %+v", changed)
	}
	sv.call(g.test.ID, "PATCH", "chats/"+id, `{}`, http.StatusBadRequest, nil)
	sv.call(g.test.ID, "POST", "chats/"+id+"/turns", `{"text":"  "}`, http.StatusBadRequest, nil)
	sv.call(g.test.ID, "POST", "chats/"+id+"/turns", `{"text":"again","edit_of":`+itoa(int(old.ID))+`}`, http.StatusBadRequest, nil)
	// Another person's session reaches none of it.
	sv.call(g.ordinary.ID, "GET", "chats/"+id, "", http.StatusNotFound, nil)
	sv.call(g.ordinary.ID, "GET", "chats/"+id+"/turns/"+turnID+"/events", "", http.StatusNotFound, nil)
	sv.call(g.test.ID, "DELETE", "chats/"+id, "", http.StatusOK, nil)
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusNotFound, nil)
	g.with(g.test.ID, func(s *tenant.Store) {
		var left int
		if err := s.DB().QueryRow("SELECT (SELECT count(*) FROM turns WHERE chat_id = ?) + (SELECT count(*) FROM turn_events WHERE turn_id = ?)", id, turnID).Scan(&left); err != nil || left != 0 {
			t.Errorf("a deleted chat left %d turn rows behind (%v)", left, err)
		}
	})
}

func itoa(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

// slow is a provider that writes the start of a reply and then waits until
// it is cancelled, as a model does in the middle of a long answer.
type slow struct {
	writing   chan struct{} // closed when the first words are out
	cancelled chan struct{} // closed when the call's context ends
}

func newSlow() *slow { return &slow{writing: make(chan struct{}), cancelled: make(chan struct{})} }

func (p *slow) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	for _, e := range []seam.Event{
		{Type: seam.EventStart, Model: req.Model},
		{Type: seam.EventBlockStart, Index: 0, Block: &seam.Block{Type: seam.BlockText}},
		{Type: seam.EventBlockDelta, Index: 0, Text: "Half an ans"},
	} {
		if err := emit(e); err != nil {
			return err
		}
	}
	close(p.writing)
	<-ctx.Done()
	close(p.cancelled)
	return ctx.Err()
}

// The item's second proof: stop ends the model call, and what had streamed
// is kept.
func TestStopEndsTheModelCall(t *testing.T) {
	g := newRig(t)
	model := newSlow()
	sv := g.serve(model)
	id := sv.newChat(g.test.ID)

	resp := sv.request(g.test.ID, "POST", "chats/"+id+"/turns", `{"text":"Tell me a long story."}`, nil)
	defer resp.Body.Close()
	turnID := resp.Header.Get("X-Turn-Id")
	done := make(chan []event, 1)
	go func() { done <- readEvents(t, resp.Body, nil) }()

	select {
	case <-model.writing:
	case <-time.After(10 * time.Second):
		t.Fatal("the model was never called")
	}
	var open ChatReply
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &open)
	if len(open.Turns) != 1 || open.Turns[0].ID != turnID || open.Turns[0].Status != StatusRunning || !open.Chat.Busy {
		t.Errorf("while the turn runs the chat reads as %+v", open.Turns)
	}

	var stopped Turn
	sv.call(g.test.ID, "POST", "chats/"+id+"/turns/"+turnID+"/stop", "", http.StatusOK, &stopped)
	select {
	case <-model.cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("stop did not end the model call")
	}
	if stopped.Status != StatusStopped || stopped.ReplyMessageID == 0 {
		t.Errorf("the stop route answered %+v", stopped)
	}
	var events []event
	select {
	case events = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream did not close after the stop")
	}
	if got, want := names(events), "turn turn message start text message end"; got != want {
		t.Fatalf("the stopped stream was\n\t%s\nwant\n\t%s", got, want)
	}
	if end := last(t, events); end.Status != StatusStopped || end.Error != nil {
		t.Errorf("the stopped turn ended as %+v", end)
	}
	var kept ChatReply
	sv.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &kept)
	if len(kept.Messages) != 2 || kept.Messages[1].StopReason != StatusStopped || kept.Messages[1].Blocks[0].Text != "Half an ans" {
		t.Errorf("after a stop the chat holds %+v", kept.Messages)
	}
	// Stopping it again changes nothing.
	sv.call(g.test.ID, "POST", "chats/"+id+"/turns/"+turnID+"/stop", "", http.StatusOK, &stopped)
	if stopped.Status != StatusStopped {
		t.Errorf("a second stop answered %+v", stopped)
	}
}

// The item's third proof: a queued turn finishes after a restart. The
// first start of the server is stopped with one turn in the middle of its
// model call and one waiting behind it.
func TestAQueuedTurnFinishesAfterARestart(t *testing.T) {
	g := newRig(t)
	model := newSlow()
	first := g.serve(model)
	id := first.newChat(g.test.ID)

	ids := make([]string, 2)
	for i, text := range []string{"Tell me a long story.", bakery} {
		body, _ := json.Marshal(SendBody{Text: text})
		resp := first.request(g.test.ID, "POST", "chats/"+id+"/turns", string(body), nil)
		ids[i] = resp.Header.Get("X-Turn-Id")
		go func() {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}()
		if i == 0 {
			select {
			case <-model.writing:
			case <-time.After(10 * time.Second):
				t.Fatal("the model was never called")
			}
		}
	}
	var waiting Turn
	first.call(g.test.ID, "GET", "chats/"+id+"/turns/"+ids[1], "", http.StatusOK, &waiting)
	if waiting.Status != StatusQueued {
		t.Fatalf("the second turn is %s while the first runs, want queued", waiting.Status)
	}
	first.stop()
	g.with(g.test.ID, func(s *tenant.Store) {
		var open int
		if err := s.DB().QueryRow("SELECT count(*) FROM turns WHERE chat_id = ? AND status IN ('queued', 'running')", id).Scan(&open); err != nil || open != 2 {
			t.Fatalf("the stopped server left %d turns open, want 2 (%v)", open, err)
		}
	})

	// The server starts again, with a model that answers.
	second := g.serve(nil)
	resp := second.request(g.test.ID, "GET", "chats/"+id+"/turns/"+ids[1]+"/events", "", nil)
	events := readEvents(t, resp.Body, nil)
	resp.Body.Close()
	if end := last(t, events); end.Status != StatusDone || end.ID != ids[1] {
		t.Fatalf("after the restart the queued turn ended as %+v: %s", end, names(events))
	}
	if text := joined(t, events, EventText); !strings.Contains(text, "Tide and Crumb") {
		t.Errorf("the queued turn's reply was %q", text)
	}

	// The turn that was running is closed as failed, and keeps its words.
	resp = second.request(g.test.ID, "GET", "chats/"+id+"/turns/"+ids[0]+"/events", "", nil)
	events = readEvents(t, resp.Body, nil)
	resp.Body.Close()
	end := last(t, events)
	if end.Status != StatusFailed || end.Error == nil || end.Error.Code != CodeInterrupted {
		t.Errorf("after the restart the running turn ended as %+v", end)
	}
	if got, want := names(events), "turn turn message start text message error end"; got != want {
		t.Errorf("the interrupted turn's events are\n\t%s\nwant\n\t%s", got, want)
	}
	var got ChatReply
	second.call(g.test.ID, "GET", "chats/"+id, "", http.StatusOK, &got)
	if len(got.Messages) != 4 || got.Messages[1].Blocks[0].Text != "Half an ans" || got.Messages[1].StopReason != StatusFailed ||
		got.Messages[3].Role != seam.RoleAssistant || len(got.Turns) != 0 {
		t.Errorf("after the restart the chat holds %d messages and %d open turns", len(got.Messages), len(got.Turns))
	}
}

// A tenant with no usable model gets a clear error event, not a hang, and
// a model it does have is used.
func TestAnOrdinaryTenant(t *testing.T) {
	g := newRig(t)
	sv := g.serve(nil)
	id := sv.newChat(g.ordinary.ID)

	var choice ModelChoice
	sv.call(g.ordinary.ID, "GET", "chats/"+id+"/model", "", http.StatusOK, &choice)
	if choice.Model != nil || choice.Source != SourceNone || choice.Reason == "" {
		t.Errorf("with no models the choice is %+v", choice)
	}
	body, _ := json.Marshal(SendBody{Text: bakery})
	events, _, requestID := sv.send(g.ordinary.ID, id, string(body))
	if got, want := names(events), "turn error end"; got != want {
		t.Fatalf("with no model the stream was %q, want %q", got, want)
	}
	if end := last(t, events); end.Status != StatusFailed || end.Error == nil || end.Error.Code != CodeNoModel {
		t.Errorf("with no model the turn ended as %+v", end)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil || len(entries) != 0 {
			t.Errorf("a turn with no model recorded %d model calls (%v)", len(entries), err)
		}
		// Two models: one the build can call, and one whose adapter it lacks.
		for _, m := range []models.Model{
			{Name: "house-scripted", Label: "House Scripted", Adapter: models.AdapterFake, ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal},
			{Name: "example-large", Label: "Example Large", Adapter: models.AdapterAnthropic, Endpoint: "https://api.provider-one.example", ProviderModel: "example-large-1", ContextWindow: 200000, OutputLimit: 16000, Class: models.ClassHosted},
		} {
			if err := models.Add(context.Background(), s, models.Limits{MaxModels: 5}, m); err != nil {
				t.Fatal(err)
			}
		}
	})

	sv.call(g.ordinary.ID, "GET", "chats/"+id+"/model", "", http.StatusOK, &choice)
	if choice.Model == nil || choice.Model.Name != "house-scripted" || choice.Source != SourceFirst || len(choice.Models) != 1 {
		t.Errorf("with one model that can be called the choice is %+v", choice)
	}
	events, _, _ = sv.send(g.ordinary.ID, id, string(body))
	if end := last(t, events); end.Status != StatusDone || end.Model != "house-scripted" {
		t.Fatalf("the turn on a registry model ended as %+v: %s", end, names(events))
	}
	sv.call(g.ordinary.ID, "GET", "chats/"+id+"/model", "", http.StatusOK, &choice)
	if choice.Source != SourceChat {
		t.Errorf("after a turn the chat's model comes from %q, want chat", choice.Source)
	}
	// A model named in the turn that this build cannot call.
	named, _ := json.Marshal(SendBody{Text: bakery, Model: "example-large"})
	events, _, _ = sv.send(g.ordinary.ID, id, string(named))
	if end := last(t, events); end.Status != StatusFailed || end.Error == nil || end.Error.Code != CodeModelUnavailable {
		t.Errorf("a turn on a model with no adapter ended as %+v", end)
	}
	// A chat too long for the model's window is refused before it is sent.
	long, _ := json.Marshal(SendBody{Text: strings.Repeat("harbour ", 4000)})
	events, _, _ = sv.send(g.ordinary.ID, id, string(long))
	if end := last(t, events); end.Status != StatusFailed || end.Error == nil || end.Error.Code != string(seam.ErrTooLong) {
		t.Errorf("a turn that does not fit the window ended as %+v", end)
	}
}

// answers is a provider that gives one reply to a chat turn and another to
// a call with standing instructions, which is what a title call is.
type answers struct{ chat, title string }

func (p answers) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	text := p.chat
	if len(req.System) > 0 {
		text = p.title
	}
	events, err := seam.Events(seam.Response{
		Model:      req.Model,
		Message:    seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}},
		StopReason: seam.StopEnd,
		Usage:      seam.Usage{InputTokens: 1000, OutputTokens: 200},
	})
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := emit(e); err != nil {
			return err
		}
	}
	return nil
}

// A model that is not the fake writes the chat's title, and both of its
// calls are recorded with their cost.
func TestAModelWritesTheTitle(t *testing.T) {
	g := newRig(t)
	g.extra = adapters.Set{models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) {
		return answers{chat: "Three names follow.", title: "\"Names for a harbour bakery.\"\nSecond line"}, nil
	}}
	sv := g.serve(nil)
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		m := models.Model{
			Name: "house-small", Label: "House Small", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
			ProviderModel: "small-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal, Price: models.Price{Input: 2, Output: 10},
		}
		if err := models.Add(context.Background(), s, models.Limits{MaxModels: 5}, m); err != nil {
			t.Fatal(err)
		}
	})
	id := sv.newChat(g.ordinary.ID)
	body, _ := json.Marshal(SendBody{Text: bakery})
	events, _, requestID := sv.send(g.ordinary.ID, id, string(body))
	if got, want := names(events), "turn turn message start text message usage title end"; got != want {
		t.Fatalf("the stream was\n\t%s\nwant\n\t%s", got, want)
	}
	if end := last(t, events); end.Status != StatusDone || end.CostMicros != 4000 {
		t.Errorf("the turn ended as %+v", end)
	}
	var got ChatReply
	sv.call(g.ordinary.ID, "GET", "chats/"+id, "", http.StatusOK, &got)
	if got.Chat.Title != "Names for a harbour bakery" {
		t.Errorf("the title is %q", got.Chat.Title)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		entries, err := audit.List(context.Background(), s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil || len(entries) != 4 || entries[3].Outcome != audit.OutcomeOK || entries[3].CostMicros != 4000 ||
			!strings.Contains(entries[2].Sent, "The reply began") {
			t.Errorf("the chat call and the title call are recorded as %d entries (%v)", len(entries), err)
		}
	})
}

func TestARequestIsBuiltFromWhatAProviderTakes(t *testing.T) {
	text := func(s string) seam.Block { return seam.Block{Type: seam.BlockText, Text: s} }
	call := func(id string) seam.Block {
		return seam.Block{Type: seam.BlockToolCall, ID: id, Name: "lookup", Input: json.RawMessage(`{}`)}
	}
	result := func(id string) seam.Block {
		return seam.Block{Type: seam.BlockToolResult, ID: id, Content: []seam.Block{text("ok")}}
	}
	path := []chat.Message{
		{ID: 1, Role: seam.RoleUser, Blocks: []seam.Block{text("one")}},
		{ID: 2, Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockThinking, Text: "mine", Signature: "sig"}, text("two"), call("a")}},
		{ID: 3, Role: seam.RoleUser, Blocks: []seam.Block{result("a")}},
		{ID: 4, Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockThinking, Text: "another model's", Signature: "sig"}, call("b")}}, // stopped before its result
		{ID: 5, Role: seam.RoleUser, Blocks: []seam.Block{text("three")}},
		{ID: 6, Role: seam.RoleUser, Blocks: []seam.Block{text("four")}},
	}
	metas := map[int64]meta{2: {model: "m"}, 4: {model: "other"}}
	got := buildMessages(path, metas, "m")
	if err := seam.ValidateRequest(seam.Request{Messages: got}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || len(got[0].Blocks) != 1 || len(got[1].Blocks) != 3 || len(got[2].Blocks) != 3 || got[2].Blocks[0].Type != seam.BlockToolResult {
		raw, _ := json.Marshal(got)
		t.Errorf("the request's messages are %s", raw)
	}
}
