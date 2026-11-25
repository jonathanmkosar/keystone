package turn

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// Prompt caching (P5-03), through the real Anthropic adapter, against a
// stand-in for the Anthropic Messages API on this machine. Every question,
// answer and instruction here is invented. The real API is never called.

// The stand-in caches as the API does, as far as a test can tell:
//
//   - a block marked with cache_control asks for everything the API reads
//     up to the end of that block to be cached: the tools, then the system
//     blocks, then the messages, with the marks themselves left out;
//   - a prefix is found again only if every byte of it is the same;
//   - from each mark the API looks back up to 20 blocks for a prefix it has
//     cached, and reads the longest it finds;
//   - a prefix shorter than the model's least is not cached;
//   - what is read is billed as cache_read_input_tokens, what is written as
//     cache_creation_input_tokens, and input_tokens is the rest.
//
// A token is four bytes of a block's JSON.
const (
	standInLeast    = 1024
	standInLookBack = 20
)

type standIn struct {
	mu     sync.Mutex
	cached map[[32]byte]bool
	calls  []standInCall
}

// standInCall is one Messages call as the stand-in read it.
type standInCall struct {
	blocks []string // each block, where it is, as the API reads them, without marks
	marks  []int    // the indexes of the blocks that carried a mark
	read   int
	write  int
	input  int
}

func newStandIn() *standIn { return &standIn{cached: map[[32]byte]bool{}} }

// read lays a request body out as the API reads it.
func (s *standIn) read(body []byte) (standInCall, error) {
	var req struct {
		Tools    []map[string]any `json:"tools"`
		System   []map[string]any `json:"system"`
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return standInCall{}, err
	}
	var c standInCall
	add := func(where string, b map[string]any) {
		if _, ok := b["cache_control"]; ok {
			c.marks = append(c.marks, len(c.blocks))
			delete(b, "cache_control")
		}
		raw, _ := json.Marshal(b) // a map is written with its keys in order
		c.blocks = append(c.blocks, where+" "+string(raw))
	}
	for _, t := range req.Tools {
		add("tool", t)
	}
	for _, b := range req.System {
		add("system", b)
	}
	for i, m := range req.Messages {
		for _, b := range m.Content {
			add(fmt.Sprintf("message %d %s", i, m.Role), b)
		}
	}
	return c, nil
}

// tokens is the size of the first n blocks.
func (c standInCall) tokens(n int) int {
	size := 0
	for _, b := range c.blocks[:n] {
		size += len(b) + 1
	}
	return size / 4
}

// prefix is the key of the first n blocks.
func (c standInCall) prefix(n int) [32]byte {
	return sha256.Sum256([]byte(strings.Join(c.blocks[:n], "\n")))
}

func (s *standIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c, err := s.read(body)
	if err != nil {
		http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"not JSON"}}`, http.StatusBadRequest)
		return
	}
	if r.URL.Path == "/v1/messages/count_tokens" {
		fmt.Fprintf(w, `{"input_tokens": %d}`, c.tokens(len(c.blocks)))
		return
	}
	if r.URL.Path != "/v1/messages" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	// Read: the longest cached prefix within the look back of any mark.
	readTo := 0
	for _, m := range c.marks {
		for n := m + 1; n > 0 && n > m+1-standInLookBack; n-- {
			if s.cached[c.prefix(n)] {
				readTo = max(readTo, n)
				break
			}
		}
	}
	c.read = c.tokens(readTo)
	// Write: up to the last mark, when it is long enough to cache.
	if len(c.marks) > 0 {
		end := c.marks[len(c.marks)-1] + 1
		if end > readTo && c.tokens(end) >= standInLeast {
			c.write = c.tokens(end) - c.read
		}
	}
	for _, m := range c.marks {
		if c.tokens(m+1) >= standInLeast {
			s.cached[c.prefix(m+1)] = true
		}
	}
	c.input = c.tokens(len(c.blocks)) - c.read - c.write
	n := len(s.calls) + 1
	s.calls = append(s.calls, c)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	send := func(kind, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
	}
	send("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_standin_%d","type":"message","role":"assistant","model":"example-large-1","content":[],"stop_reason":null,"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":1}}}`, n, c.input, c.write, c.read))
	send("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	send("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Reply %d from the stand-in."}}`, n))
	send("content_block_stop", `{"type":"content_block_stop","index":0}`)
	send("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`)
	send("message_stop", `{"type":"message_stop"}`)
}

func (s *standIn) seen() []standInCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]standInCall(nil), s.calls...)
}

// standingText is invented standing instructions, long enough for the
// standing start to be cached on its own.
var standingText = strings.Repeat("Answer as the harbour office's assistant would: plainly, in short sentences, "+
	"and say which timetable or notice an answer comes from. When a question is about tides, give the time of high "+
	"water first. When it is about the ferry, give the next sailing first. ", 24)

// cachedModel is a hosted model behind the Anthropic adapter, at the
// stand-in's address, with cache prices.
func cachedModel(endpoint string) models.Model {
	return models.Model{
		Name: "cached-large", Label: "Cached Large", Adapter: models.AdapterAnthropic, Endpoint: endpoint,
		ProviderModel: "example-large-1", ContextWindow: 200000, OutputLimit: 1024, Class: models.ClassHosted,
		Price: models.Price{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
	}
}

// cacheRig is a rig whose ordinary tenant has cachedModel, the shipped
// Anthropic adapter, long standing instructions and no titles.
func cacheRig(t *testing.T, stand *standIn) (*rig, models.Model) {
	t.Helper()
	srv := httptest.NewServer(stand)
	t.Cleanup(srv.Close)
	g := newRig(t)
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	opts, err := adapters.AnthropicOptions(reg)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.CacheControl {
		t.Fatal("anthropic.cache_control ships off")
	}
	build, err := adapters.Anthropic(opts)
	if err != nil {
		t.Fatal(err)
	}
	g.extra = adapters.Set{models.AdapterAnthropic: build}
	m := cachedModel(srv.URL)
	g.addModels(m)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	g.set(tenantsettings.Person, contextbuild.KeySystemText, standingText)
	return g, m
}

// talk sends each question as the next message of one chat, and returns
// each turn's id and its one usage record.
func talk(t *testing.T, g *rig, sv *served, m models.Model, questions ...string) (chatID string, turnIDs []string, records []usage.Record) {
	t.Helper()
	chatID = sv.newChat(g.ordinary.ID)
	for _, q := range questions {
		body, _ := json.Marshal(SendBody{Text: q, Model: m.Name})
		events, turnID, requestID := sv.send(g.ordinary.ID, chatID, string(body))
		if end := last(t, events); end.Status != StatusDone {
			t.Fatalf("%q ended as %+v", q, end)
		}
		g.with(g.ordinary.ID, func(s *tenant.Store) {
			got, err := usage.List(context.Background(), s, usage.Filter{RequestID: requestID})
			if err != nil || len(got) != 1 {
				t.Fatalf("%q has the usage records %+v (%v)", q, got, err)
			}
			records = append(records, got[0])
		})
		turnIDs = append(turnIDs, turnID)
	}
	return chatID, turnIDs, records
}

// The item's proof: the second turn of a chat sends the first turn's prefix
// again, byte for byte, with its marks; the provider reads it from its
// cache; and the turn's usage record shows the tokens read, priced at the
// cache price. The third turn reads more. The inspector's totals show it.
func TestASecondTurnReadsWhatTheFirstCached(t *testing.T) {
	stand := newStandIn()
	g, cachedModel := cacheRig(t, stand)
	sv := g.serveInspector(nil, nil)
	questions := []string{
		"When is high water at the north pier tomorrow?",
		"And when does the first ferry leave after it?",
		"Is the harbour office open then?",
	}
	id, turnIDs, records := talk(t, g, sv, cachedModel, questions...)
	calls := stand.seen()
	if len(calls) != len(questions) {
		t.Fatalf("the stand-in had %d Messages calls, not %d", len(calls), len(questions))
	}
	first, second, third := calls[0], calls[1], calls[2]

	// The marks: the end of the standing start and of the last message on
	// the first turn; and on the later turns the end of the history too.
	if len(first.marks) != 2 || first.marks[1] != len(first.blocks)-1 {
		t.Fatalf("the first turn's marks are at %v of %d blocks", first.marks, len(first.blocks))
	}
	start := first.marks[0]
	for i, c := range []standInCall{second, third} {
		history := len(c.blocks) - 2 // the last answer, before the new question
		if want := []int{start, history, len(c.blocks) - 1}; fmt.Sprint(c.marks) != fmt.Sprint(want) {
			t.Fatalf("turn %d's marks are at %v, not %v", i+2, c.marks, want)
		}
	}

	// The second turn starts with every byte the first sent, marks aside,
	// and the third with every byte the second sent.
	for i, pair := range [][2]standInCall{{first, second}, {second, third}} {
		was, now := pair[0], pair[1]
		if len(now.blocks) <= len(was.blocks) || strings.Join(now.blocks[:len(was.blocks)], "\n") != strings.Join(was.blocks, "\n") {
			t.Fatalf("turn %d does not begin with what turn %d sent:\n%q\n%q", i+2, i+1, was.blocks, now.blocks)
		}
	}

	// What was cached and read.
	if first.read != 0 || first.write != first.tokens(len(first.blocks)) || first.write < standInLeast {
		t.Fatalf("the first turn read %d and wrote %d of %d tokens", first.read, first.write, first.tokens(len(first.blocks)))
	}
	if second.read != first.tokens(len(first.blocks)) {
		t.Fatalf("the second turn read %d tokens from the cache, and the first turn's whole call was %d", second.read, first.tokens(len(first.blocks)))
	}
	if third.read != second.tokens(len(second.blocks)) || third.read <= second.read {
		t.Fatalf("the third turn read %d tokens from the cache, and the second turn's whole call was %d", third.read, second.tokens(len(second.blocks)))
	}

	// The usage records carry what the provider said, priced with the
	// cache prices.
	for i, c := range calls {
		r := records[i]
		if r.CacheReadTokens != c.read || r.CacheWriteTokens != c.write || r.InputTokens != c.input || r.OutputTokens != 7 || r.Estimated {
			t.Errorf("turn %d's usage record is %+v, and the provider said read %d, write %d, input %d", i+1, r, c.read, c.write, c.input)
		}
		if want := cachedModel.Price.CostMicros(c.input, 7, c.read, c.write); r.CostMicros != want {
			t.Errorf("turn %d cost %d micros, and its tokens at the model's prices cost %d", i+1, r.CostMicros, want)
		}
	}
	if records[1].CacheReadTokens == 0 {
		t.Fatal("the second turn's usage record shows no cache hit")
	}
	// A read token costs a tenth of an input token at these prices, so the
	// second turn cost less than it would have with no cache.
	uncached := cachedModel.Price.CostMicros(second.tokens(len(second.blocks)), 7, 0, 0)
	if records[1].CostMicros >= uncached {
		t.Errorf("the second turn cost %d micros, and %d with no cache", records[1].CostMicros, uncached)
	}

	// The inspector shows the hits, and what each call was built from says
	// where the history ends.
	got := sv.inspectTurn(g.ordinary.ID, id, turnIDs[1])
	if got.Totals.CacheReadTokens != second.read || got.Totals.CacheWriteTokens != second.write {
		t.Errorf("the inspector's totals are %+v", got.Totals)
	}
	if len(got.Calls) == 0 || got.Calls[0].Context == nil || got.Calls[0].Context.Cache == nil || got.Calls[0].Context.Cache.History != 2 {
		t.Fatalf("the second turn's call records the points %+v", got.Calls)
	}
}

// Recall changes on every turn. It goes with the current message, after
// the history, so it spoils nothing before it: the second turn still reads
// the standing start, and from the third on each turn reads every turn but
// the last. Before P5-03 recall sat in the system text, and no turn read
// anything.
func TestRecallLeavesTheCachedStartAndHistoryAlone(t *testing.T) {
	stand := newStandIn()
	g, m := cacheRig(t, stand)
	seedKnowledge(g)
	g.set(tenantsettings.Person, memory.SettingOn, "false") // its calls would go to the same model
	sv := g.serve(nil)
	questions := []string{
		"When do the runner beans go in?",
		"Which bed do the runner beans go in?",
		"Should the runner beans go in before the last frost?",
	}
	_, _, records := talk(t, g, sv, m, questions...)
	calls := stand.seen()
	if len(calls) != len(questions) {
		t.Fatalf("the stand-in had %d Messages calls, not %d", len(calls), len(questions))
	}
	recalled := "Runner beans go in the north bed after the last frost."
	for i, c := range calls {
		// Recall is in the current message, just before the question, and
		// nowhere else.
		n := len(c.blocks)
		if !strings.Contains(c.blocks[n-2], recalled) || !strings.Contains(c.blocks[n-1], jsonText(questions[i])) {
			t.Fatalf("turn %d does not end with recall and then the question:\n%s\n%s", i+1, c.blocks[n-2], c.blocks[n-1])
		}
		for _, b := range c.blocks[:n-2] {
			if strings.Contains(b, recalled) {
				t.Fatalf("turn %d sends recall before the current message: %s", i+1, b)
			}
		}
	}
	first, second, third := calls[0], calls[1], calls[2]
	start := first.marks[0] + 1 // the blocks up to the end of the standing start
	if fmt.Sprint(second.marks) != fmt.Sprint([]int{start - 1, len(second.blocks) - 3, len(second.blocks) - 1}) {
		t.Fatalf("the second turn's marks are at %v of %d blocks", second.marks, len(second.blocks))
	}
	if second.read != first.tokens(start) || second.read < standInLeast {
		t.Fatalf("the second turn read %d tokens, and the standing start is %d", second.read, first.tokens(start))
	}
	history := second.marks[1] + 1 // the second turn's history: the first question, as written, and its answer
	if third.read != second.tokens(history) || third.read <= second.read {
		t.Fatalf("the third turn read %d tokens, and the second turn's history ends at %d", third.read, second.tokens(history))
	}
	if records[1].CacheReadTokens != second.read || records[2].CacheReadTokens != third.read {
		t.Fatalf("the usage records read %d and %d tokens from the cache", records[1].CacheReadTokens, records[2].CacheReadTokens)
	}
}

// jsonText is a text as it is written inside JSON.
func jsonText(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
