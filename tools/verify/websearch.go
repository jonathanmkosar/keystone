package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
	"github.com/jonathanmkosar/keystone/internal/turn"
)

// The web search's parts of verify (P7-03).
//
//   - webSearchRecorded is a step of "calls recorded": the test tenant's
//     recorded web search runs on the real server, with the search
//     provider pointed at a stand-in on this machine and an invented key
//     in the test tenant's vault. The call must be in the audit log
//     before and after, its result must be outside content, and every
//     [n] in the answer must name a source the stand-in returned.
//   - keysInWebSearch is a surface of "no keys in output": stand-ins that
//     repeat the key in their refusals and in their results.
//
// Nothing here leaves the machine: the stand-ins are served on it, and
// web_fetch is not called.

// The message of fixtures/test-tenant/turns/web-search.json.
const webSearchMessage = "When is the harbour museum open this week?"

// webStandIn is a stand-in Tavily on this machine. It answers each search
// by its query: "refused", "limit" and "broken" fail as Tavily does, with
// the Authorization header repeated in the body; "echo" answers with the
// header repeated in every field of a result; anything else answers with
// the two results below.
type webStandIn struct {
	*httptest.Server
	calls atomic.Int32
	auth  atomic.Value
}

var webStandInResults = []web.Result{
	{Title: "Harbour Museum: opening hours", URL: "https://museum.example/hours", Content: "Open from ten in the morning, Tuesday to Sunday."},
	{Title: "Visiting the harbour", URL: "https://visit.example/harbour", Content: "The museum is closed on Mondays."},
}

func newWebStandIn() *webStandIn {
	s := &webStandIn{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		auth := r.Header.Get("Authorization")
		s.auth.Store(auth)
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var body struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(raw, &body)
		switch body.Query {
		case "refused":
			http.Error(w, "unauthorized: "+auth, http.StatusUnauthorized)
		case "limit":
			http.Error(w, "too many: "+auth, http.StatusTooManyRequests)
		case "broken":
			http.Error(w, "broken: "+auth, http.StatusBadGateway)
		case "echo":
			key := strings.TrimPrefix(auth, "Bearer ")
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]string{{"title": "echo " + key, "url": "https://echo.example/", "content": "the key: " + key}}})
		default:
			var rows []map[string]string
			for _, res := range webStandInResults {
				rows = append(rows, map[string]string{"title": res.Title, "url": res.URL, "content": res.Content})
			}
			json.NewEncoder(w).Encode(map[string]any{"results": rows})
		}
	}))
	return s
}

// citation is a number in square brackets in a reply.
var citation = regexp.MustCompile(`\[(\d{1,4})\]`)

// webSearchRecorded runs the test tenant's recorded web search.
func webSearchRecorded() (problems []string, err error) {
	stand := newWebStandIn()
	defer stand.Close()
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	c.Web.SearchEndpoint = stand.URL + "/search"
	reg, err := app.Tools(c)
	if err != nil {
		return nil, err
	}
	r, err := openToolRig(reg)
	if err != nil {
		return nil, err
	}
	defer r.close()
	const key = "an invented search key for verify, not a real one"
	if err := r.test.Secrets().Set(web.KeyName, key); err != nil {
		return nil, err
	}
	got, err := r.send(webSearchMessage)
	if err != nil {
		return nil, err
	}
	if got.end.Status != turn.StatusDone {
		problems = append(problems, fmt.Sprintf("the recorded web search ended as %s", got.end.Status))
	}
	if len(got.calls) != 1 || got.calls[0].Name != web.SearchName {
		return append(problems, fmt.Sprintf("the recorded web search made %d tool calls, want one call to %s", len(got.calls), web.SearchName)), nil
	}
	call := got.calls[0]
	res, ok := got.results[call.ID]
	block := seam.Block{Type: seam.BlockToolResult, ID: call.ID, IsError: res.IsError, Content: res.Content}
	switch {
	case !ok:
		return append(problems, "the web search has no tool_result in the stream"), nil
	case res.IsError:
		return append(problems, "the web search gave an error: "+resultText(res)), nil
	case !tools.IsOutside(block):
		problems = append(problems, "the web search's result is not marked as outside content")
	}
	if stand.calls.Load() != 1 || stand.auth.Load() != "Bearer "+key {
		problems = append(problems, fmt.Sprintf("the stand-in search provider had %d searches, want one with the stored key as its bearer token", stand.calls.Load()))
	}

	// Every citation in the answer names a source the search returned.
	returned := map[string]bool{}
	for _, s := range webStandInResults {
		returned[s.URL] = true
	}
	sources := map[int]string{}
	for _, s := range web.Sources(resultText(res)) {
		if !returned[s.URL] {
			problems = append(problems, fmt.Sprintf("the result names a source the search did not return: [%d] %s", s.N, s.URL))
		}
		sources[s.N] = s.URL
	}
	if len(sources) != len(webStandInResults) {
		problems = append(problems, fmt.Sprintf("the result has %d numbered sources, and the search returned %d", len(sources), len(webStandInResults)))
	}
	rec := r.w.request("GET", api.Prefix+"chats/"+got.chatID, r.token, "", nil)
	var kept turn.ChatReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &kept) != nil || len(kept.Messages) == 0 {
		return nil, fmt.Errorf("the chat of the web search cannot be read back: status %d", rec.Code)
	}
	answer := kept.Messages[len(kept.Messages)-1]
	cited := citation.FindAllStringSubmatch(seam.Text(seam.Message{Blocks: answer.Blocks}), -1)
	if answer.Role != seam.RoleAssistant || len(cited) < 2 {
		problems = append(problems, fmt.Sprintf("the answer cites %d sources, and the recorded turn cites two", len(cited)))
	}
	for _, m := range cited {
		n, _ := strconv.Atoi(m[1])
		if _, ok := sources[n]; !ok {
			problems = append(problems, fmt.Sprintf("the answer cites [%d], which names no source the search returned", n))
		}
	}
	var record []seam.Message
	for _, m := range kept.Messages {
		record = append(record, seam.Message{Role: m.Role, Blocks: m.Blocks})
	}
	if !tools.ReadOutside(record) {
		problems = append(problems, "the chat record of the web search holds no result marked as outside content")
	}

	// The audit log: the call before and after, under the request id.
	entries, err := r.entriesOf(audit.KindTool, got.requestID)
	if err != nil {
		return nil, err
	}
	if len(entries) != 2 || entries[0].Action != web.SearchName || entries[0].Outcome != audit.OutcomeStarted || entries[1].Outcome != audit.OutcomeOK {
		problems = append(problems, fmt.Sprintf("the web search left %d audit entries under its request id, want one before it and one for its end", len(entries)))
	}
	for _, e := range entries {
		if strings.Contains(e.Sent+e.Received, key) {
			problems = append(problems, "the search key is in the audit log")
		}
	}
	return problems, nil
}

// keysInWebSearch runs web_search with value stored as the person's search
// key, against a stand-in that repeats it in every answer, and returns
// every result, the audit log and the data folder.
func keysInWebSearch(_, dir, value string) ([]keyOutput, error) {
	stand := newWebStandIn()
	defer stand.Close()
	in, err := tenant.OpenWithKeeper(filepath.Join(dir, "data"), secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		return nil, err
	}
	s, err := in.Store(made.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if err := s.Secrets().Set(web.KeyName, value); err != nil {
		return nil, err
	}
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	o := c.Web
	o.SearchEndpoint = stand.URL + "/search"
	reg := tools.New()
	if err := web.Add(reg, o); err != nil {
		return nil, err
	}
	var out []keyOutput
	for _, q := range []string{"harbour", "refused", "limit", "broken", "echo"} {
		res := reg.Run(context.Background(), tools.Call{Store: s, RequestID: "req-" + q, Actor: "person", ID: "call_" + q, Name: web.SearchName, Input: json.RawMessage(fmt.Sprintf(`{"query":%q}`, q))})
		out = append(out, keyOutput{"the result of the search for " + q, []byte(fmt.Sprintf("%+v", res))})
	}
	// The check is worth nothing unless the key really was sent.
	if stand.auth.Load() != "Bearer "+value {
		return out, fmt.Errorf("the stand-in search provider was not sent the stored key")
	}
	entries, err := audit.List(context.Background(), s, audit.Filter{})
	if err != nil {
		return out, err
	}
	out = append(out, keyOutput{"the audit log", []byte(fmt.Sprintf("%+v", entries))})
	files, err := folderFiles(filepath.Join(dir, "data"))
	return append(out, files...), err
}
