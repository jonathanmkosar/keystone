package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/wikijobs"
	"github.com/jonathanmkosar/keystone/internal/wikijobs/scope"
	"github.com/jonathanmkosar/keystone/settings"
)

// The standing check "wiki jobs" (P8-06). The plan's words: the Waymarks
// agent-kit jobs run as Keystone scheduled agents, each with its own token,
// and turn the sources the memory pass adds into cited pages.
//
//  1. The guard, internal/wikijobs/scope, which reads no setting: only a
//     local or trusted model may serve a wiki run; an answer that asks for
//     more than the job's grant, or puts a sensitive source on a page in a
//     normal space, is refused whole. Every job's tools only read, and each
//     Waymarks job's grant is the agent kit's.
//  2. On the real server, put together as internal/app puts it, on a
//     throwaway folder with the built-in store: a fact told in chat is
//     added by the memory pass, and after a run of the built-in store's
//     compile job it is on a page that cites it. The run's chat is held for
//     sensitive content, no memory pass read it, the hosted model in the
//     person's fallback chain was never sent a call, and the write is in
//     the audit log under the run's request id.
//
// The proof against Waymarks itself, with each job under its own token in
// Waymarks' audit, is go test ./internal/wikijobs: against a fake Waymarks
// always, and against the real binary when it is on the machine.

func init() {
	componentDocs["internal/wikijobs"] = "docs/architecture/wiki-jobs.md"
	componentDocs["internal/wikijobs/scope"] = "docs/architecture/wiki-jobs.md"
	networkAllowed["internal/wikijobs"] = "calls the person's own Waymarks instance at knowledge.waymarks_address under each job's own token; its tests call a fake Waymarks on this machine, and a Waymarks test instance started from the binary on 127.0.0.1 when it is there"
	fixedRuleGuards["rules.sensitive_content"] = append(fixedRuleGuards["rules.sensitive_content"], "internal/wikijobs/scope")
	fixedRuleGuards["rules.approval"] = append(fixedRuleGuards["rules.approval"], "internal/wikijobs/scope")
	// A Waymarks token is wm_ and hex.
	secretShapes = append(secretShapes, regexp.MustCompile(`wm_[0-9a-f]{32,}`))
}

// wikiPassword is invented, and guards a folder that is removed.
const wikiPassword = "an invented phrase for the wiki jobs check"

const wikiFact = "The person keeps a rowing boat at the north quay."

func checkWikiJobs(string) error {
	problems := wikiGuardProblems()
	more, err := wikiRunProblems()
	if err != nil {
		return err
	}
	problems = append(problems, more...)
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

func wikiGuardProblems() []string {
	var problems []string
	for class, want := range map[models.Class]bool{models.ClassLocal: true, models.ClassTrusted: true, models.ClassHosted: false, "": false} {
		if scope.MayServe(string(class)) != want {
			problems = append(problems, fmt.Sprintf("a %q model may serve a wiki run: %v, want %v", class, !want, want))
		}
	}
	st := scope.Store{Sources: map[scope.ID]bool{"1": false, "2": true}, Spaces: map[string]bool{"health": true}}
	lim := scope.Limits{Batches: 20, NewPages: 10}
	page := func(space string, src scope.ID) scope.Plan {
		return scope.Plan{Batches: []scope.Batch{{Note: "n", Writes: []scope.Write{{Space: space, Path: "a", Title: "A", Body: "b", Sources: []scope.ID{src}}}}}}
	}
	compile, _ := wikijobs.Get(wikijobs.JobCompile)
	if scope.Check(compile.May, lim, page("memory", "1"), st) != nil || scope.Check(compile.May, lim, page("health", "2"), st) != nil {
		problems = append(problems, "an answer that fits the compile job's grant was refused")
	}
	if scope.Check(compile.May, lim, page("memory", "2"), st) == nil {
		problems = append(problems, "a sensitive source was let onto a page in a normal space")
	}
	if scope.Check(compile.May, lim, scope.Plan{Issues: []scope.Issue{{Raise: &scope.Raise{PageID: "1", Title: "x"}}}}, st) == nil {
		problems = append(problems, "the compile job may raise an issue, which its grant does not allow")
	}
	for _, j := range wikijobs.Jobs() {
		for _, t := range wikijobs.JobTools(j) {
			if !t.ReadOnly || t.Inside {
				problems = append(problems, fmt.Sprintf("the %s job's tool %s writes, or is taken for Keystone's own content", j.Name, t.Name))
			}
		}
	}
	return problems
}

// wikiModel is the person's local model in the check: it answers a chat,
// pulls the fact out of it for the memory pass, and works as the built-in
// store's compile job.
type wikiModel struct{}

func (wikiModel) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	var all strings.Builder
	for _, b := range req.System {
		all.WriteString(b.Text)
	}
	listed := ""
	for _, m := range req.Messages {
		all.WriteString(seam.Text(m))
		for _, b := range m.Blocks {
			if b.Type == seam.BlockToolResult {
				for _, c := range b.Content {
					listed += c.Text
				}
			}
		}
	}
	text := all.String()
	msg := seam.Message{Role: seam.RoleAssistant}
	stop := seam.StopEnd
	switch {
	case strings.Contains(text, "long-term memory of a personal assistant"):
		facts := `{"facts": []}`
		if strings.Contains(text, "rowing boat") {
			facts = `{"facts": ["` + wikiFact + `"]}`
		}
		msg.Blocks = []seam.Block{{Type: seam.BlockText, Text: facts}}
	case strings.Contains(text, "compile job of this person's built-in knowledge store") && listed == "":
		msg.Blocks = []seam.Block{{Type: seam.BlockToolCall, ID: "call_1", Name: "list_uncompiled_sources", Input: json.RawMessage(`{}`)}}
		stop = seam.StopToolCall
	case strings.Contains(text, "compile job of this person's built-in knowledge store"):
		var list []struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(listed), &list)
		id := ""
		if len(list) > 0 {
			id = list[0].ID
		}
		msg.Blocks = []seam.Block{{Type: seam.BlockText, Text: "```json\n" + `{"batches": [{"note": "File the boat", "writes": [{"space": "memory", "path": "boats/rowing-boat", "title": "Rowing boat", "body": "` + wikiFact + ` [[source:` + id + `]]", "sources": ["` + id + `"]}]}], "report": "One page."}` + "\n```"}}
	default:
		msg.Blocks = []seam.Block{{Type: seam.BlockText, Text: "Noted."}}
	}
	events, err := seam.Events(seam.Response{Model: req.Model, Message: msg, StopReason: stop, Usage: seam.Usage{InputTokens: 100, OutputTokens: 20}})
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

// wikiPaid is a hosted model a wiki run must never call.
type wikiPaid struct{ n *atomic.Int32 }

func (p wikiPaid) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	p.n.Add(1)
	events, _ := seam.Events(seam.Response{Model: req.Model, Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: "From the paid model."}}}, StopReason: seam.StopEnd})
	for _, e := range events {
		if err := emit(e); err != nil {
			return err
		}
	}
	return nil
}

func wikiRunProblems() (problems []string, err error) {
	dir, err := os.MkdirTemp("", "keystone-wikijobs-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	house := models.Model{Name: "house-example", Label: "House Example", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
		ProviderModel: "small-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal, Capabilities: []models.Capability{models.CapTools}}
	paid := models.Model{Name: "paid-example", Label: "Paid Example", Adapter: models.AdapterAnthropic, Endpoint: "https://models.example",
		ProviderModel: "example-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassHosted, Capabilities: []models.Capability{models.CapTools}, Price: models.Price{Input: 2, Output: 10}}
	person, err := in.Create("Wiki Jobs Person")
	if err != nil {
		return nil, err
	}
	so, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	lim, err := adapters.Limits(reg)
	if err != nil {
		return nil, err
	}
	s, err := in.Store(person.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if err := signin.SetPassword(s, wikiPassword, so); err != nil {
		return nil, err
	}
	for _, m := range []models.Model{house, paid} {
		if err := models.Add(ctx, s, lim, m); err != nil {
			return nil, err
		}
	}
	for key, value := range map[string]string{
		"chats.default_model": house.Name, "routing.memory_model": house.Name, "routing.fallback_models": paid.Name,
		knowledge.SettingAdapter: wikijobs.StoreBuiltin,
	} {
		if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, key, value); err != nil {
			return nil, err
		}
	}

	var paidCalls atomic.Int32
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	c.Adapters = adapters.Set{
		models.AdapterAnthropic:       func(models.Model, string) (seam.Provider, error) { return wikiPaid{&paidCalls}, nil },
		models.AdapterChatCompletions: func(models.Model, string) (seam.Provider, error) { return wikiModel{}, nil },
	}
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	token, rec := w.signIn(person.ID, wikiPassword)
	if token == "" {
		return nil, fmt.Errorf("the person cannot sign in: status %d", rec.Code)
	}
	call := func(method, path string, want int, out any) error {
		rec := w.request(method, api.Prefix+path, token, "", nil)
		if rec.Code != want {
			return fmt.Errorf("%s %s answered %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		if out != nil {
			return json.Unmarshal(rec.Body.Bytes(), out)
		}
		return nil
	}
	state := func(job string) (wikijobs.JobState, error) {
		var got wikijobs.JobsReply
		if err := call("GET", "wiki-jobs", http.StatusOK, &got); err != nil {
			return wikijobs.JobState{}, err
		}
		for _, j := range got.Jobs {
			if j.Name == job {
				return j, nil
			}
		}
		return wikijobs.JobState{}, fmt.Errorf("no %s in the list", job)
	}
	wait := func(what string, ok func() (bool, error)) error {
		deadline := time.Now().Add(40 * time.Second)
		for {
			done, err := ok()
			if err != nil || done {
				return err
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("waited too long for %s", what)
			}
			time.Sleep(25 * time.Millisecond)
		}
	}

	st, err := state(wikijobs.JobBuiltinCompile)
	if err != nil {
		return nil, err
	}
	if st.On {
		problems = append(problems, "the built-in store's compile job is on as shipped")
	}
	// A fact told in chat.
	var made turn.ChatReply
	if err := call("POST", "chats", http.StatusCreated, &made); err != nil {
		return nil, err
	}
	rec = w.request("POST", api.Prefix+"chats/"+made.Chat.ID+"/turns", token, `{"text": "I keep a rowing boat at the north quay."}`, nil)
	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("the message was not sent: status %d", rec.Code)
	}
	ks, err := knowledge.Open(ctx, wikijobs.StoreBuiltin, s)
	if err != nil {
		return nil, err
	}
	var source string
	if err := wait("the memory pass", func() (bool, error) {
		hits, err := ks.Search(ctx, knowledge.Query{Text: "rowing boat"})
		for _, h := range hits {
			if h.Type == knowledge.HitSource {
				source = h.ID
			}
		}
		return source != "", err
	}); err != nil {
		return nil, err
	}
	// A run of the job, and what it wrote.
	var run schedules.Run
	if err := call("POST", "wiki-jobs/"+wikijobs.JobBuiltinCompile+"/run", http.StatusCreated, &run); err != nil {
		return nil, err
	}
	if err := wait("the run to be written", func() (bool, error) {
		st, err = state(wikijobs.JobBuiltinCompile)
		return st.LastWritten != nil && st.LastWritten.RunID == run.ID && st.LastWritten.State != "applying", err
	}); err != nil {
		return nil, err
	}
	if st.LastWritten.State != "applied" {
		problems = append(problems, fmt.Sprintf("the compile run was %s: %s", st.LastWritten.State, st.LastWritten.Summary))
	}
	cat, err := ks.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	cited := false
	for _, e := range cat {
		if e.Path == "boats/rowing-boat" {
			p, err := ks.ReadPage(ctx, e.ID)
			cited = err == nil && strings.Contains(p.Body, wikiFact) && strings.Contains(p.Body, "[[source:"+source+"]]") && len(p.Sources) == 1 && p.Sources[0] == source
		}
	}
	if !cited {
		problems = append(problems, "the fact told in chat is not on a page that cites its source after the compile run")
	}
	if held, err := recall.Held(ctx, s, run.ChatID); err != nil || !held {
		problems = append(problems, "the wiki run's chat is not held for sensitive content")
	}
	if passes, err := memory.Passes(ctx, s, run.ChatID); err != nil || len(passes) != 0 {
		problems = append(problems, "a memory pass read the wiki run's chat")
	}
	if n := paidCalls.Load(); n != 0 {
		problems = append(problems, fmt.Sprintf("the hosted model in the fallback chain was called %d times", n))
	}
	var requestID string
	if err := s.DB().QueryRowContext(ctx, "SELECT request_id FROM jobs WHERE id = ?", run.TaskID).Scan(&requestID); err != nil {
		return nil, err
	}
	entries, err := audit.List(ctx, s, audit.Filter{RequestID: requestID})
	if err != nil {
		return nil, err
	}
	wrote, called := false, false
	for _, e := range entries {
		wrote = wrote || (e.Action == "builtin_write_page" && e.Outcome == audit.OutcomeOK && e.Actor == "wiki."+wikijobs.JobBuiltinCompile)
		called = called || (e.Kind == audit.KindTool && e.Action == "list_uncompiled_sources" && e.Outcome == audit.OutcomeOK)
	}
	if !wrote || !called {
		problems = append(problems, fmt.Sprintf("the run's tool call or its write is not in the audit log under its request id (tool %v, write %v)", called, wrote))
	}
	return problems, nil
}
