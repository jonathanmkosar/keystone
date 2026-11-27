package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// testKey stands in for a provider key. It is invented.
const testKey = "an invented key for the tests, not a real one"

// exchange is one recorded call: what the seam asked, what the adapter
// must send, what the endpoint answers, and what the seam must see.
type exchange struct {
	Name  string `json:"name"`
	Model struct {
		ProviderModel string              `json:"provider_model"`
		Capabilities  []models.Capability `json:"capabilities"`
		OutputLimit   int                 `json:"output_limit"`
		NoKey         bool                `json:"no_key"`
		Path          string              `json:"path"`
	} `json:"model"`
	Options struct {
		CacheControl         *bool    `json:"cache_control"`
		BudgetThinkingModels []string `json:"budget_thinking_models"`
	} `json:"options"`
	BareEvents bool            `json:"bare_events"`
	Request    json.RawMessage `json:"request"`
	Sent       json.RawMessage `json:"sent"`
	Answers    []struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Events  []json.RawMessage `json:"events"`
		Body    json.RawMessage   `json:"body"`
		Text    string            `json:"text"`
	} `json:"answers"`
	Reply json.RawMessage `json:"reply"`
	Error *struct {
		Kind       seam.ErrorKind `json:"kind"`
		Retryable  bool           `json:"retryable"`
		MessageHas string         `json:"message_has"`
	} `json:"error"`
	Partial  string   `json:"partial"`
	Attempts *int     `json:"attempts"`
	Waits    []string `json:"waits"`
}

func loadExchanges(t *testing.T) map[string][]exchange {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "fixtures", "anthropic")
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(names) == 0 {
		t.Fatalf("no fixtures in %s: %v", dir, err)
	}
	out := map[string][]exchange{}
	for _, name := range names {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			TestData  bool       `json:"test_data"`
			Exchanges []exchange `json:"exchanges"`
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&file); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !file.TestData || len(file.Exchanges) == 0 {
			t.Fatalf("%s is not marked as test data, or holds no exchange", name)
		}
		out[filepath.Base(name)] = file.Exchanges
	}
	return out
}

func testOptions() Options {
	return Options{
		Version:        "2023-06-01",
		MaxRetries:     2,
		RetryBaseDelay: 500 * time.Millisecond,
		RetryMaxDelay:  20 * time.Second,
		HeaderTimeout:  10 * time.Second,
		IdleTimeout:    10 * time.Second,
		CacheControl:   true,
	}
}

func testEntry(endpoint string) models.Model {
	return models.Model{
		Name: "example-large", Label: "Example Large", Adapter: models.AdapterAnthropic,
		Endpoint: endpoint, ProviderModel: "example-large-1", Key: "provider.one",
		ContextWindow: 200000, OutputLimit: 16000,
		Capabilities: []models.Capability{models.CapTools, models.CapVision, models.CapFiles, models.CapThinking},
		Class:        models.ClassHosted,
	}
}

// quiet makes a provider wait no time between tries, and notes each wait
// it would have made.
func quiet(p *Provider) *[]time.Duration {
	var waits []time.Duration
	var mu sync.Mutex
	p.jitter = func(d time.Duration) time.Duration { return d }
	p.sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		return ctx.Err()
	}
	return &waits
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, a)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	return reflect.DeepEqual(x, y)
}

func writeEvents(w http.ResponseWriter, events []json.RawMessage, bare bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	for _, raw := range events {
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &head)
		var line bytes.Buffer
		_ = json.Compact(&line, raw)
		if !bare {
			fmt.Fprintf(w, "event: %s\n", head.Type)
		}
		fmt.Fprintf(w, "data: %s\n\n", line.Bytes())
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// TestRecordedExchanges replays every exchange under fixtures/anthropic
// through a server on this machine.
func TestRecordedExchanges(t *testing.T) {
	total := 0
	for file, exchanges := range loadExchanges(t) {
		for _, x := range exchanges {
			total++
			t.Run(file+"/"+x.Name, func(t *testing.T) { replay(t, x) })
		}
	}
	if total < 25 {
		t.Fatalf("only %d exchanges were found", total)
	}
}

func replay(t *testing.T, x exchange) {
	req, err := seam.DecodeRequest(x.Request)
	if err != nil {
		t.Fatalf("the fixture's request: %v", err)
	}
	key := testKey
	if x.Model.NoKey {
		key = ""
	}
	var mu sync.Mutex
	calls := 0
	var problems []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		i := calls
		calls++
		note := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			note("call %d was %s %s", i, r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Api-Key"); got != key {
			note("call %d carried the wrong key header", i)
		}
		if r.Header.Get("Authorization") != "" {
			note("call %d carried an Authorization header", i)
		}
		if got := r.Header.Get("Anthropic-Version"); got != "2023-06-01" {
			note("call %d carried version %q", i, got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			note("call %d carried content type %q", i, got)
		}
		body, _ := io.ReadAll(r.Body)
		if len(x.Sent) > 0 && !sameJSON(t, body, x.Sent) {
			note("call %d sent\n%s\nand the fixture expects\n%s", i, body, x.Sent)
		}
		mu.Unlock()
		if i >= len(x.Answers) {
			http.Error(w, "the fixture has no answer for this call", http.StatusTeapot)
			return
		}
		a := x.Answers[i]
		for k, v := range a.Headers {
			w.Header().Set(k, v)
		}
		switch {
		case a.Events != nil:
			writeEvents(w, a.Events, x.BareEvents)
		case a.Body != nil:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(a.Status)
			w.Write(a.Body)
		default:
			w.WriteHeader(a.Status)
			io.WriteString(w, a.Text)
		}
	}))
	defer srv.Close()

	p, err := New(fixtureEntry(x, srv.URL+x.Model.Path), key, fixtureOptions(x))
	if err != nil {
		t.Fatal(err)
	}
	waits := quiet(p)

	var c seam.Collector
	sawError := false
	err = p.Stream(context.Background(), req, func(e seam.Event) error {
		if err := seam.ValidateEvent(e); err != nil {
			t.Errorf("the adapter sent an event the seam refuses: %v", err)
		}
		if sawError {
			t.Errorf("a %s event came after the error event", e.Type)
		}
		sawError = e.Type == seam.EventError
		return c.Add(e)
	})

	mu.Lock()
	defer mu.Unlock()
	for _, p := range problems {
		t.Error(p)
	}
	if x.Attempts != nil && calls != *x.Attempts {
		t.Errorf("the endpoint was called %d times, and the fixture expects %d", calls, *x.Attempts)
	}
	if x.Waits != nil {
		var got []string
		for _, w := range *waits {
			got = append(got, w.String())
		}
		if !reflect.DeepEqual(got, x.Waits) {
			t.Errorf("the waits were %v, and the fixture expects %v", got, x.Waits)
		}
	}

	switch {
	case x.Error != nil:
		var se *seam.Error
		if !errors.As(err, &se) {
			t.Fatalf("Stream returned %v, and the fixture expects a %s error", err, x.Error.Kind)
		}
		if !sawError {
			t.Error("Stream returned an error and sent no error event")
		}
		if se.Kind != x.Error.Kind || se.Retryable != x.Error.Retryable {
			t.Errorf("the error is %s (retryable %v), and the fixture expects %s (retryable %v): %s",
				se.Kind, se.Retryable, x.Error.Kind, x.Error.Retryable, se.Message)
		}
		if !strings.Contains(se.Message, x.Error.MessageHas) {
			t.Errorf("the message %q does not hold %q", se.Message, x.Error.MessageHas)
		}
		if strings.Contains(se.Message, testKey) {
			t.Error("the message holds the key")
		}
		if got := seam.Text(c.Partial()); got != x.Partial {
			t.Errorf("the text that arrived before the failure is %q, and the fixture expects %q", got, x.Partial)
		}
	case x.Reply != nil:
		if err != nil {
			t.Fatalf("Stream failed: %v", err)
		}
		resp, err := c.Response()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := seam.DecodeResponse(x.Reply); err != nil {
			t.Fatalf("the fixture's reply: %v", err)
		}
		got, _ := json.Marshal(resp)
		if !sameJSON(t, got, x.Reply) {
			t.Errorf("the reply is\n%s\nand the fixture expects\n%s", got, x.Reply)
		}
	default:
		t.Fatal("the fixture gives neither a reply nor an error")
	}
}

// fixtureEntry is the test entry with what an exchange changes in it.
func fixtureEntry(x exchange, endpoint string) models.Model {
	entry := testEntry(endpoint)
	if x.Model.ProviderModel != "" {
		entry.ProviderModel = x.Model.ProviderModel
	}
	if x.Model.Capabilities != nil {
		entry.Capabilities = x.Model.Capabilities
	}
	if x.Model.OutputLimit != 0 {
		entry.OutputLimit = x.Model.OutputLimit
	}
	if x.Model.NoKey {
		entry.Key = ""
	}
	return entry
}

// fixtureOptions are the test options with what an exchange changes in them.
func fixtureOptions(x exchange) Options {
	opts := testOptions()
	if x.Options.CacheControl != nil {
		opts.CacheControl = *x.Options.CacheControl
	}
	opts.BudgetThinkingModels = x.Options.BudgetThinkingModels
	return opts
}

func textRequest(text string) seam.Request {
	return seam.Request{Messages: []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}}}}
}

// hang answers with the start of a reply and then sends nothing until the
// caller goes away.
func hang(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEvents(w, []json.RawMessage{
			json.RawMessage(`{"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":0}}}`),
			json.RawMessage(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"One moment"}}`),
		}, false)
		select {
		case <-r.Context().Done():
		case <-time.After(20 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestACancelledCallStopsWithTheContextsError(t *testing.T) {
	srv := hang(t)
	p, err := New(testEntry(srv.URL), testKey, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var seen []seam.EventType
	err = p.Stream(ctx, textRequest("Hello."), func(e seam.Event) error {
		seen = append(seen, e.Type)
		if e.Type == seam.EventBlockDelta {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream returned %v, and a cancelled call returns the context's error", err)
	}
	if last := seen[len(seen)-1]; last != seam.EventBlockDelta {
		t.Errorf("a %s event came after the call was cancelled", last)
	}

	// A context that is over before the call sends nothing at all.
	calls := 0
	err = p.Stream(ctx, textRequest("Hello."), func(seam.Event) error { calls++; return nil })
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Errorf("a call with a cancelled context returned %v after %d events", err, calls)
	}
}

func TestAReceiverThatGoesAwayEndsTheCall(t *testing.T) {
	srv := hang(t)
	p, err := New(testEntry(srv.URL), testKey, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	gone := errors.New("the browser went away")
	calls := 0
	err = p.Stream(context.Background(), textRequest("Hello."), func(seam.Event) error {
		calls++
		return gone
	})
	if err != gone || calls != 1 {
		t.Errorf("Stream returned %v after %d events, and it should return the receiver's error after one", err, calls)
	}
}

func TestAQuietStreamIsGivenUp(t *testing.T) {
	srv := hang(t)
	opts := testOptions()
	opts.IdleTimeout = 100 * time.Millisecond
	p, err := New(testEntry(srv.URL), testKey, opts)
	if err != nil {
		t.Fatal(err)
	}
	var c seam.Collector
	err = p.Stream(context.Background(), textRequest("Hello."), c.Add)
	var se *seam.Error
	if !errors.As(err, &se) || se.Kind != seam.ErrUnavailable || !strings.Contains(se.Message, "sent nothing") {
		t.Fatalf("Stream returned %v, and a quiet stream is unavailable", err)
	}
	if got := seam.Text(c.Partial()); got != "One moment" {
		t.Errorf("the text kept is %q", got)
	}
}

func TestAnEndpointThatCannotBeReachedIsTriedAgain(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	address := srv.URL
	srv.Close() // nothing listens there now
	p, err := New(testEntry(address), testKey, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	waits := quiet(p)
	var c seam.Collector
	err = p.Stream(context.Background(), textRequest("Hello."), c.Add)
	var se *seam.Error
	if !errors.As(err, &se) || se.Kind != seam.ErrUnavailable || !se.Retryable {
		t.Fatalf("Stream returned %v, and an endpoint that cannot be reached is unavailable and may be retried", err)
	}
	if len(*waits) != 2 {
		t.Errorf("the adapter waited %d times, and the options allow two retries", len(*waits))
	}
	if strings.Contains(se.Message, testKey) || strings.Contains(se.Message, address) {
		t.Errorf("the message repeats the key or the address: %s", se.Message)
	}
}

func TestWaitsDoubleUpToTheLongest(t *testing.T) {
	opts := testOptions()
	opts.MaxRetries = 10
	opts.RetryMaxDelay = 3 * time.Second
	p, err := New(testEntry("https://api.provider-one.example"), testKey, opts)
	if err != nil {
		t.Fatal(err)
	}
	p.jitter = func(d time.Duration) time.Duration { return d }
	busy := result{fault: &seam.Error{Kind: seam.ErrOverloaded, Message: "busy", Retryable: true}}
	var got []time.Duration
	for attempt := 0; attempt < 5; attempt++ {
		wait, again := p.retry(attempt, busy)
		if !again {
			t.Fatalf("attempt %d was not tried again", attempt)
		}
		got = append(got, wait)
	}
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 3 * time.Second, 3 * time.Second}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the waits are %v, and %v is expected", got, want)
	}
	if _, again := p.retry(10, busy); again {
		t.Error("a call was tried again after the last retry")
	}
	if _, again := p.retry(0, result{fault: busy.fault, started: true}); again {
		t.Error("a call was tried again after part of its reply was passed on")
	}
	if _, again := p.retry(0, result{fault: &seam.Error{Kind: seam.ErrInvalid, Message: "no"}}); again {
		t.Error("a call that cannot work was tried again")
	}
	for i := 0; i < 200; i++ {
		if d := jitter(time.Second); d < 500*time.Millisecond || d > time.Second {
			t.Fatalf("the jitter gave %s for one second", d)
		}
	}
}

func TestCountTokens(t *testing.T) {
	var body []byte
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" || r.Header.Get("X-Api-Key") != testKey {
			t.Errorf("the count went to %s with the wrong path or key", r.URL.Path)
		}
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			io.WriteString(w, `{"input_tokens": 42}`)
		} else {
			io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"no such call"}}`)
		}
	}))
	defer srv.Close()
	p, err := New(testEntry(srv.URL), testKey, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	req := textRequest("How many tokens is this?")
	req.System = []seam.Block{{Type: seam.BlockText, Text: "Answer briefly."}}
	req.Tools = []seam.Tool{{Name: "clock", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	n, err := p.CountTokens(context.Background(), req)
	if err != nil || n != 42 {
		t.Fatalf("CountTokens gave %d, %v", n, err)
	}
	want := `{"model":"example-large-1",
		"system":[{"type":"text","text":"Answer briefly.","cache_control":{"type":"ephemeral"}}],
		"tools":[{"name":"clock","input_schema":{"type":"object"}}],
		"messages":[{"role":"user","content":[{"type":"text","text":"How many tokens is this?","cache_control":{"type":"ephemeral"}}]}]}`
	if !sameJSON(t, body, []byte(want)) {
		t.Errorf("the count sent\n%s", body)
	}

	// An endpoint with no such call: the caller gets an error and estimates.
	status = http.StatusNotFound
	_, err = p.CountTokens(context.Background(), req)
	var se *seam.Error
	if !errors.As(err, &se) || se.Kind != seam.ErrInvalid {
		t.Errorf("CountTokens returned %v for an endpoint that has no count", err)
	}
}

func TestTheEventReader(t *testing.T) {
	stream := ": a comment to keep the connection open\r\n" +
		"event: one\r\ndata: {\"a\":1}\r\n\r\n" +
		"\n\n" +
		"event: named and empty\n\n" +
		"data:first line\ndata: second line\nid: 7\nretry: 100\n\n" +
		"data: cut off before its blank line\n"
	r := newEventReader(strings.NewReader(stream))
	var got []string
	for {
		ev, err := r.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev.name+"|"+string(ev.data))
	}
	want := []string{`one|{"a":1}`, "|first line\nsecond line"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the reader gave %q, and %q is expected", got, want)
	}
}

func TestEntriesAndOptionsThatAreRefused(t *testing.T) {
	good := testEntry("https://api.provider-one.example")
	entries := map[string]func(*models.Model){
		"another adapter":      func(m *models.Model) { m.Adapter = models.AdapterResponses },
		"no provider model":    func(m *models.Model) { m.ProviderModel = "" },
		"no output limit":      func(m *models.Model) { m.OutputLimit = 0 },
		"no endpoint":          func(m *models.Model) { m.Endpoint = "" },
		"not http":             func(m *models.Model) { m.Endpoint = "ftp://files.example" },
		"a key in the address": func(m *models.Model) { m.Endpoint = "https://api.provider-one.example?key=" + "abc" },
	}
	for name, change := range entries {
		m := good
		change(&m)
		if _, err := New(m, testKey, testOptions()); err == nil {
			t.Errorf("%s: the entry was accepted", name)
		} else if strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: the refusal holds the key", name)
		}
	}
	if _, err := New(good, testKey+"\r\nX-Other: 1", testOptions()); err == nil || strings.Contains(err.Error(), testKey) {
		t.Errorf("a key with a line break in it: %v", err)
	}
	options := map[string]func(*Options){
		"no version":             func(o *Options) { o.Version = "" },
		"a version of two lines": func(o *Options) { o.Version = "2023-06-01\nX-Other: 1" },
		"negative retries":       func(o *Options) { o.MaxRetries = -1 },
		"a negative wait":        func(o *Options) { o.IdleTimeout = -time.Second },
		"longest under first":    func(o *Options) { o.RetryMaxDelay = time.Millisecond },
	}
	for name, change := range options {
		o := testOptions()
		change(&o)
		if _, err := New(good, testKey, o); err == nil {
			t.Errorf("%s: the options were accepted by New", name)
		}
		if _, err := Builder(o); err == nil {
			t.Errorf("%s: the options were accepted by Builder", name)
		}
	}
	build, err := Builder(testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if p, err := build(good, testKey); err != nil || p == nil {
		t.Errorf("the builder refused a good entry: %v", err)
	}
}

// TestTheKeyIsNeverGivenBack runs the surface verify uses, so a leak fails
// here as well as there.
func TestTheKeyIsNeverGivenBack(t *testing.T) {
	outputs, err := KeyOutputs(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) < 10 {
		t.Fatalf("only %d outputs were gathered", len(outputs))
	}
	for where, data := range outputs {
		if bytes.Contains(data, []byte(testKey)) {
			t.Errorf("%s holds the key:\n%s", where, data)
		}
	}
}

func TestErrorKinds(t *testing.T) {
	cases := []struct {
		status    int
		typ       string
		message   string
		kind      seam.ErrorKind
		retryable bool
	}{
		{401, "", "", seam.ErrAuth, false},
		{403, "", "", seam.ErrAuth, false},
		{429, "", "", seam.ErrRateLimited, true},
		{529, "", "", seam.ErrOverloaded, true},
		{413, "", "", seam.ErrTooLong, false},
		{400, "", "this model's maximum context length is 8192 tokens", seam.ErrTooLong, false},
		{400, "", "something else", seam.ErrInvalid, false},
		{422, "", "", seam.ErrInvalid, false},
		{408, "", "", seam.ErrUnavailable, true},
		{500, "", "", seam.ErrUnavailable, true},
		{503, "", "", seam.ErrUnavailable, true},
		{504, "timeout_error", "", seam.ErrUnavailable, true},
		{409, "", "", seam.ErrOther, true},
		{418, "", "", seam.ErrOther, false},
		{0, "overloaded_error", "", seam.ErrOverloaded, true},
		{0, "api_error", "", seam.ErrUnavailable, true},
		{0, "rate_limit_error", "", seam.ErrRateLimited, true},
		{0, "", "", seam.ErrOther, false},
		// The provider's type is trusted over the status.
		{500, "authentication_error", "", seam.ErrAuth, false},
	}
	for _, c := range cases {
		kind, retryable := classify(c.status, c.typ, c.message)
		if kind != c.kind || retryable != c.retryable {
			t.Errorf("status %d, type %q: %s (retryable %v), and %s (retryable %v) is expected", c.status, c.typ, kind, retryable, c.kind, c.retryable)
		}
	}
	h := http.Header{}
	h.Set("Retry-After", "2")
	if d := retryAfter(h); d != 2*time.Second {
		t.Errorf("retry-after of 2 read as %s", d)
	}
	h.Set("Retry-After-Ms", "250")
	if d := retryAfter(h); d != 250*time.Millisecond {
		t.Errorf("retry-after-ms of 250 read as %s", d)
	}
	if d := retryAfter(http.Header{"Retry-After": {"soon"}}); d != 0 {
		t.Errorf("a retry-after that is not a time read as %s", d)
	}
}
