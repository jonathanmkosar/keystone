package loadtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/tools/builtin"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/settings"
)

// loadPassword is invented, and guards a folder that is removed.
const loadPassword = "an invented phrase for the load test"

// person is one invented person on the rig, signed in.
type person struct {
	index     int
	name, id  string
	token     string
	store     *tenant.Store // the generator's own handle
	generated *tally        // what the fast path wrote for them
}

// rig is the load test's data folder, people and server.
type rig struct {
	p   Profile
	dir string
	in  *tenant.Install
	reg []settings.Setting
	cfg app.Config
	h   http.Handler
	log *lockedLog
	pl  *planner
	now time.Time

	people []*person
	// queries are the words the search measurements look for, and the
	// messages that make the scripted model call search_chats with each.
	queries []string

	// real is every model call the rig's own turns set off, by person, and
	// extraFileBytes what the measurements' own uploads put in the first
	// person's files folder.
	mu             sync.Mutex
	real           map[string][]realCall
	extraFileBytes int64
	// loose are the requests of turns sent in the long chat, each of which
	// may set off a summary of its oldest turns that no stream tells.
	loose map[string]bool

	closers []func()
}

// realCall is one model call a turn sent through the real handler made, as
// its stream told it.
type realCall struct {
	requestID, model, kind string
	usage                  seam.Usage
	costMicros             int64
}

// lockedLog is the server's log, written from the server's goroutines.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// searchAsk is the message that makes the scripted model search for q.
func searchAsk(q string) string { return "Search my earlier chats for: " + q }

// The words the scripted model says outside a generated chat.
const (
	measuredTitle = "Measured chat"
	searchedReply = "That is what the earlier chats hold."
)

// openRig makes a throwaway data folder under parent (the system's
// temporary folder when it is empty), the profile's people, and the real
// server on it with the fake provider on the seam. scripts are played
// before the rig's own, for a caller that needs the model to say given
// words.
func openRig(p Profile, parent string, scripts []fake.Script) (g *rig, err error) {
	g = &rig{p: p, real: map[string][]realCall{}, loose: map[string]bool{}, now: time.Now()}
	// A failure returns a nil rig, so the one to close is held apart:
	// closing g itself would close nothing and panic.
	opened := g
	defer func() {
		if err != nil {
			opened.close()
		}
	}()
	if g.dir, err = os.MkdirTemp(parent, "keystone-loadtest-"); err != nil {
		return nil, err
	}
	g.closers = append(g.closers, func() { os.RemoveAll(g.dir) })
	if g.in, err = tenant.OpenWithKeeper(g.dir, secrets.NewMemoryKeeper()); err != nil {
		return nil, err
	}
	if g.reg, err = settings.Registry(); err != nil {
		return nil, err
	}
	// Encryption at rest as the server has it (P15-06): on, as it ships,
	// every person the rig makes is sealed, and the load runs through the
	// VFS and the sealed folders.
	if s, ok := settings.Lookup(g.reg, "encryption.at_rest"); ok && s.Default == "true" {
		g.in.EncryptAtRest(tenant.AtRest{On: true})
	}
	g.pl = newPlanner(p, g.now)
	if g.cfg, err = app.Defaults(); err != nil {
		return nil, err
	}
	// The password's rounds are not what this test measures.
	g.cfg.SignIn.PasswordIterations = 1000
	lim, err := adapters.Limits(g.reg)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	for i := 0; i <= p.Tenants; i++ {
		who := &person{index: i, name: "Load Person " + strconv.Itoa(i+1)}
		made, err := g.in.Create(who.name)
		if err != nil {
			return nil, err
		}
		who.id = made.ID
		if who.store, err = g.in.Store(made.ID); err != nil {
			return nil, err
		}
		s := who.store
		g.closers = append(g.closers, func() { s.Close() })
		if err := signin.SetPassword(s, loadPassword, g.cfg.SignIn); err != nil {
			return nil, err
		}
		for _, m := range []models.Model{hostedModel, localModel} {
			if err := models.Add(ctx, s, lim, m); err != nil {
				return nil, err
			}
		}
		// A recap is written two idle minutes after a turn, by a model call
		// no run of this length can count on; the load's recaps are
		// generated rows.
		if err := tenantsettings.Set(ctx, s, g.reg, tenantsettings.Person, turn.KeyRecap, "false"); err != nil {
			return nil, err
		}
		g.people = append(g.people, who)
	}

	opts, err := fake.DefaultOptions()
	if err != nil {
		return nil, err
	}
	opts.Delay = 0 // the model answers at once: what is measured is Keystone's own time
	provider, err := fake.New(append(scripts, g.ownScripts()...), opts)
	if err != nil {
		return nil, err
	}
	build := func(models.Model, string) (seam.Provider, error) { return provider, nil }
	g.log = &lockedLog{}
	g.cfg.Install, g.cfg.Log = g.in, g.log
	g.cfg.Adapters = adapters.Set{models.AdapterFake: build, models.AdapterChatCompletions: build}
	h, stop, err := app.Open(g.cfg)
	if err != nil {
		return nil, err
	}
	// The server is stopped before the stores are closed and the folder is
	// removed: closers run last first.
	g.h, g.closers = h, append(g.closers, stop)
	for _, who := range g.people {
		if who.token, err = g.signIn(who.id); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// ownScripts are what the fake provider says in the rig's own turns: a
// reply of the profile's length to any message, a title, and a search of
// the earlier chats for each of the rig's queries.
func (g *rig) ownScripts() []fake.Script {
	v := g.pl.vocab
	mid := g.pl.chat(0, g.p.Chats/2, g.p.Chats, 1, nil)
	g.queries = []string{
		v.words[3] + " " + v.words[20],
		strings.Join(mid.topic, " "),
		v.words[10] + " " + mid.topic[0],
		v.words[len(v.words)/3],
		v.words[1] + " " + v.words[2] + " " + v.words[5],
	}
	text := func(s string) *seam.Response {
		return &seam.Response{Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: s}}}, StopReason: seam.StopEnd}
	}
	reply := text(g.measuredReply())
	reply.Usage = seam.Usage{InputTokens: 4000, OutputTokens: g.p.ReplyChars / 4}
	title := text(measuredTitle)
	title.Usage = titleUsage
	out := []fake.Script{
		{Name: "load test: a measured reply", When: fake.When{}, Steps: []fake.Step{{Reply: reply}}},
		{Name: "load test: a title", When: fake.When{SystemContains: titleMark(g.cfg.Turns.TitlePrompt)}, Steps: []fake.Step{{Reply: title}}},
	}
	for i, q := range g.queries {
		input, _ := json.Marshal(map[string]string{"query": q})
		call := &seam.Response{Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{
			{Type: seam.BlockToolCall, ID: "load-search-" + strconv.Itoa(i+1), Name: builtin.SearchChats, Input: input}}},
			StopReason: seam.StopToolCall, Usage: seam.Usage{InputTokens: 3400, OutputTokens: 20}}
		said := text(searchedReply)
		said.Usage = seam.Usage{InputTokens: 3900, OutputTokens: 10}
		out = append(out, fake.Script{Name: "load test: search " + strconv.Itoa(i+1), When: fake.When{UserText: searchAsk(q)},
			Steps: []fake.Step{{Reply: call}, {Reply: said}}})
	}
	return out
}

// titleMark is the part of the title prompt a script knows a title call by.
func titleMark(prompt string) string {
	if len(prompt) > 40 {
		return prompt[:40]
	}
	return prompt
}

// measuredReply is the reply of a measured turn: the profile's length of
// invented words, the same in every run.
func (g *rig) measuredReply() string {
	r := g.pl.chat(0, 0, 1, 1, nil).rand
	s := g.pl.vocab.text(r, g.p.ReplyChars/5, nil)
	for len(s) < g.p.ReplyChars {
		s += " " + g.pl.vocab.text(r, 40, nil)
	}
	return s[:g.p.ReplyChars]
}

// close stops the server and removes the folder.
func (g *rig) close() {
	for i := len(g.closers) - 1; i >= 0; i-- {
		g.closers[i]()
	}
	g.closers = nil
}

// signIn signs a person in through the door and returns the session token.
func (g *rig) signIn(id string) (string, error) {
	body, _ := json.Marshal(api.SignInBody{Tenant: id, Password: loadPassword})
	rec := g.do("POST", "session", "", body)
	for _, c := range rec.Result().Cookies() {
		if c.Name == api.CookieName && rec.Code == http.StatusOK && c.Value != "" {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("signing in answered %d", rec.Code)
}

// request builds one call to the API. A body is sent as JSON, or as bytes
// when raw is set.
func request(method, path, token string, body []byte, raw bool) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, "http://keystone.test"+api.Prefix+path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if raw {
			r.Header.Set("Content-Type", "application/octet-stream")
		}
	} else {
		r = httptest.NewRequest(method, "http://keystone.test"+api.Prefix+path, nil)
	}
	if token != "" {
		r.AddCookie(&http.Cookie{Name: api.CookieName, Value: token})
	}
	return r
}

// do sends one call to the real server's handler, in memory.
func (g *rig) do(method, path, token string, body []byte) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, request(method, path, token, body, false))
	return rec
}

// as makes one JSON call as a person and reads the answer into out.
func (g *rig) as(who *person, method, path string, body any, want int, out any) error {
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return err
		}
	}
	rec := g.do(method, path, who.token, raw)
	if rec.Code != want {
		return fmt.Errorf("%s %s answered %d, want %d: %s", method, path, rec.Code, want, clip(rec.Body.String()))
	}
	if out != nil {
		return json.Unmarshal(rec.Body.Bytes(), out)
	}
	return nil
}

// upload sends bytes to a route that takes a file, as a person, and reads
// the answer into out.
func (g *rig) upload(who *person, path string, data []byte, out any) error {
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, request("POST", path, who.token, data, true))
	if rec.Code != http.StatusCreated {
		return fmt.Errorf("POST %s answered %d: %s", path, rec.Code, clip(rec.Body.String()))
	}
	return json.Unmarshal(rec.Body.Bytes(), out)
}

// timed makes one GET as a person and returns how long the handler took.
func (g *rig) timed(who *person, path string, out any) (time.Duration, error) {
	rec := httptest.NewRecorder()
	req := request("GET", path, who.token, nil, false)
	began := time.Now()
	g.h.ServeHTTP(rec, req)
	took := time.Since(began)
	if rec.Code != http.StatusOK {
		return 0, fmt.Errorf("GET %s answered %d: %s", path, rec.Code, clip(rec.Body.String()))
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			return 0, fmt.Errorf("GET %s: %w", path, err)
		}
	}
	return took, nil
}

// newChat makes a chat as a person.
func (g *rig) newChat(who *person) (string, error) {
	var made turn.ChatReply
	err := g.as(who, "POST", "chats", nil, http.StatusCreated, &made)
	return made.Chat.ID, err
}

func clip(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// streamed is one turn as its stream told it, with when each part came,
// counted from the moment the message was sent.
type streamed struct {
	code              int
	requestID, turnID string
	// opened is when the first model call opened, first when the first
	// words came, last when the last did, and done when the stream ended.
	opened, first, last, done time.Duration
	// toolAsked and toolAnswered are when a tool call and its result came.
	toolAsked, toolAnswered time.Duration
	chars                   int
	usage                   []turn.UsageEvent
	titled                  bool
	end                     turn.Turn
	ended                   bool
	failure                 string
}

// timedWriter is a response that notes when each event of a stream is
// written to it.
type timedWriter struct {
	head  http.Header
	began time.Time
	out   *streamed
	rest  []byte
}

func (w *timedWriter) Header() http.Header { return w.head }
func (w *timedWriter) WriteHeader(code int) {
	if w.out.code == 0 {
		w.out.code = code
	}
}
func (w *timedWriter) Flush() {}

func (w *timedWriter) Write(p []byte) (int, error) {
	at := time.Since(w.began)
	if w.out.code == 0 {
		w.out.code = http.StatusOK
	}
	w.rest = append(w.rest, p...)
	for {
		i := bytes.Index(w.rest, []byte("\n\n"))
		if i < 0 {
			break
		}
		w.event(string(w.rest[:i]), at)
		w.rest = w.rest[i+2:]
	}
	return len(p), nil
}

func (w *timedWriter) event(chunk string, at time.Duration) {
	var name, data string
	for _, line := range strings.Split(chunk, "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	o := w.out
	switch name {
	case turn.EventStart:
		if o.opened == 0 {
			o.opened = at
		}
	case turn.EventText:
		if o.first == 0 {
			o.first = at
		}
		o.last = at
		var d turn.DeltaEvent
		if json.Unmarshal([]byte(data), &d) == nil {
			o.chars += len(d.Text)
		}
	case turn.EventToolCall:
		if o.toolAsked == 0 {
			o.toolAsked = at
		}
	case turn.EventToolResult:
		if o.toolAnswered == 0 {
			o.toolAnswered = at
		}
	case turn.EventUsage:
		var u turn.UsageEvent
		if json.Unmarshal([]byte(data), &u) == nil {
			o.usage = append(o.usage, u)
		}
	case turn.EventTitle:
		o.titled = true
	case turn.EventError:
		o.failure = data
	case turn.EventEnd:
		o.ended, o.done = true, at
		json.Unmarshal([]byte(data), &o.end)
	}
}

// send sends a message in a chat as a person, reads the turn to its end,
// and notes every model call it made. A turn that does not end as done is
// an error.
func (g *rig) send(who *person, chatID string, body turn.SendBody) (streamed, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return streamed{}, err
	}
	var out streamed
	req := request("POST", "chats/"+chatID+"/turns", who.token, raw, false)
	w := &timedWriter{head: http.Header{}, out: &out, began: time.Now()}
	g.h.ServeHTTP(w, req)
	if len(w.rest) > 0 && out.code != http.StatusOK {
		out.failure = string(w.rest)
	}
	out.requestID, out.turnID = w.head.Get("X-Request-Id"), w.head.Get("X-Turn-Id")
	if out.code != http.StatusOK || !out.ended || out.end.Status != turn.StatusDone {
		return out, fmt.Errorf("a turn of %s did not end as done: status %d, turn %q, %s", who.name, out.code, out.end.Status, clip(out.failure))
	}
	calls := make([]realCall, 0, len(out.usage)+1)
	hosted := false
	for _, u := range out.usage {
		calls = append(calls, realCall{requestID: out.requestID, model: u.Model, kind: "chat", usage: u.Usage, costMicros: u.CostMicros})
		hosted = hosted || u.Model == hostedModel.Name
	}
	// A chat's first reply on a model that is not the fake adapter's own is
	// followed by a call for its title, which the stream does not tell.
	if out.titled && hosted {
		calls = append(calls, realCall{requestID: out.requestID, model: hostedModel.Name, kind: "title", usage: titleUsage,
			costMicros: hostedModel.Price.CostMicros(titleUsage.InputTokens, titleUsage.OutputTokens, 0, 0)})
	}
	g.mu.Lock()
	g.real[who.id] = append(g.real[who.id], calls...)
	g.mu.Unlock()
	return out, nil
}
