package responses

import (
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
	"github.com/jonathanmkosar/keystone/settings"
)

// testKey is invented. It is sent to a server on this machine and must
// never come back in an error or an event.
const testKey = "invented-key-for-the-replay-server-0001"

var testOptions = Options{
	MaxRetries:       2,
	RetryBaseDelay:   time.Millisecond,
	RetryMaxDelay:    20 * time.Millisecond,
	HeaderTimeout:    5 * time.Second,
	IdleTimeout:      5 * time.Second,
	EffortMediumFrom: 4096,
	EffortHighFrom:   16384,
}

type exchange struct {
	WireRequest json.RawMessage   `json:"wire_request"`
	Status      int               `json:"status"`
	Headers     map[string]string `json:"headers"`
	Events      []json.RawMessage `json:"events"`
	Body        json.RawMessage   `json:"body"`
}

type fixtureCase struct {
	Name         string              `json:"name"`
	Note         string              `json:"note"`
	Capabilities []models.Capability `json:"capabilities"`
	Request      seam.Request        `json:"request"`
	Exchanges    []exchange          `json:"exchanges"`
	Response     json.RawMessage     `json:"response"`
	Error        *struct {
		Kind       seam.ErrorKind `json:"kind"`
		Retryable  bool           `json:"retryable"`
		MessageHas string         `json:"message_has"`
	} `json:"error"`
	PartialText string `json:"partial_text"`
}

func entry(endpoint string, caps ...models.Capability) models.Model {
	return models.Model{
		Name:          "example-responses",
		Label:         "Example Responses",
		Adapter:       models.AdapterResponses,
		Endpoint:      endpoint,
		ProviderModel: "example-model-1",
		Key:           "provider.example",
		ContextWindow: 128000,
		OutputLimit:   8000,
		Capabilities:  caps,
		Class:         models.ClassHosted,
	}
}

// replay answers each call with the next exchange, and notes what was
// wrong with the call.
type replay struct {
	mu        sync.Mutex
	exchanges []exchange
	calls     int
	problems  []string
}

func (r *replay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.calls
	r.calls++
	wrong := func(format string, args ...any) {
		r.problems = append(r.problems, fmt.Sprintf("call %d: ", n+1)+fmt.Sprintf(format, args...))
	}
	body, _ := io.ReadAll(req.Body)
	if req.Method != http.MethodPost || req.URL.Path != "/v1/responses" {
		wrong("%s %s, want POST /v1/responses", req.Method, req.URL.Path)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer "+testKey {
		wrong("the Authorization header is not the key as a bearer token")
	}
	if strings.Contains(string(body), testKey) || strings.Contains(req.URL.String(), testKey) {
		wrong("the key is in the body or the address")
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		wrong("Content-Type is %q", ct)
	}
	if n >= len(r.exchanges) {
		wrong("the fixture has no exchange for it")
		http.Error(w, "no more exchanges", http.StatusTeapot)
		return
	}
	ex := r.exchanges[n]
	if len(ex.WireRequest) > 0 && !sameJSON(body, ex.WireRequest) {
		wrong("the body sent is\n%s\nand the fixture wants\n%s", body, ex.WireRequest)
	}
	for k, v := range ex.Headers {
		w.Header().Set(k, v)
	}
	status := ex.Status
	if status == 0 {
		status = http.StatusOK
	}
	if ex.Events == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, strings.ReplaceAll(string(ex.Body), "{{key}}", testKey))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.WriteHeader(status)
	for _, e := range ex.Events {
		var head struct {
			Type string `json:"type"`
		}
		json.Unmarshal(e, &head)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", head.Type, compactJSON(e))
		w.(http.Flusher).Flush()
	}
}

// compactJSON puts a JSON value on one line, as an event's data must be.
func compactJSON(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func loadCases(t *testing.T) []fixtureCase {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "fixtures", "responses-adapter", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	var all []fixtureCase
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			TestData bool          `json:"test_data"`
			Cases    []fixtureCase `json:"cases"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if !file.TestData || len(file.Cases) == 0 {
			t.Fatalf("%s is not marked as test data, or holds no case", f)
		}
		all = append(all, file.Cases...)
	}
	return all
}

// TestRecordedExchanges replays every fixture through a server on this
// machine: the body sent, the number of calls, and the reply or the error.
func TestRecordedExchanges(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range loadCases(t) {
		if seen[c.Name] {
			t.Fatalf("two cases are named %s", c.Name)
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			rp := &replay{exchanges: c.Exchanges}
			srv := httptest.NewServer(rp)
			defer srv.Close()
			p, err := New(entry(srv.URL+"/v1", c.Capabilities...), testKey, testOptions)
			if err != nil {
				t.Fatal(err)
			}

			var col seam.Collector
			var events []seam.Event
			err = p.Stream(context.Background(), c.Request, func(e seam.Event) error {
				events = append(events, e)
				if e.Type == seam.EventError {
					if verr := seam.ValidateEvent(e); verr != nil {
						t.Errorf("the error event is not valid: %v", verr)
					}
					return nil
				}
				return col.Add(e)
			})

			for _, problem := range rp.problems {
				t.Error(problem)
			}
			if rp.calls != len(c.Exchanges) {
				t.Errorf("the adapter made %d calls, and the fixture has %d exchanges", rp.calls, len(c.Exchanges))
			}
			seenJSON, _ := json.Marshal(events)
			if strings.Contains(string(seenJSON), testKey) || (err != nil && strings.Contains(err.Error(), testKey)) {
				t.Error("the key is in an event or in the error")
			}

			if c.Error != nil {
				var se *seam.Error
				if !errors.As(err, &se) {
					t.Fatalf("want a %s error, got %v", c.Error.Kind, err)
				}
				if se.Kind != c.Error.Kind || se.Retryable != c.Error.Retryable {
					t.Errorf("got kind %s retryable %v (%s), want kind %s retryable %v", se.Kind, se.Retryable, se.Message, c.Error.Kind, c.Error.Retryable)
				}
				if !strings.Contains(se.Message, c.Error.MessageHas) {
					t.Errorf("the message %q does not hold %q", se.Message, c.Error.MessageHas)
				}
				if last := events[len(events)-1]; last.Type != seam.EventError || last.Error != se {
					t.Errorf("the stream did not end with the error event")
				}
				if got := seam.Text(col.Partial()); got != c.PartialText {
					t.Errorf("the text that arrived before the failure is %q, want %q", got, c.PartialText)
				}
				return
			}
			if err != nil {
				t.Fatalf("the call failed: %v", err)
			}
			got, err := col.Response()
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, _ := json.Marshal(got)
			if !sameJSON(gotJSON, c.Response) {
				t.Errorf("the reply is\n%s\nand the fixture wants\n%s", gotJSON, compactJSON(c.Response))
			}
		})
	}
}

// streamServer sends the given lines and then holds the connection open
// until the client goes away.
func streamServer(t *testing.T, lines ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			io.WriteString(w, l)
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

const oneDelta = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_invented_090\",\"content_index\":0,\"delta\":\"The path \"}\n\n"

func hello() seam.Request {
	return seam.Request{Messages: []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Hello."}}}}}
}

func TestCancelStopsTheStream(t *testing.T) {
	srv := streamServer(t, oneDelta)
	p, err := New(entry(srv.URL), testKey, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var after int
	err = p.Stream(ctx, hello(), func(e seam.Event) error {
		if e.Type == seam.EventBlockDelta {
			cancel()
			return nil
		}
		if ctx.Err() != nil {
			after++
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the context's error", err)
	}
	if after != 0 {
		t.Errorf("%d events were sent after the cancel", after)
	}
}

func TestSilentStreamIsGivenUpOn(t *testing.T) {
	srv := streamServer(t, ": keep-alive\n\n", oneDelta)
	opts := testOptions
	opts.IdleTimeout = 60 * time.Millisecond
	p, err := New(entry(srv.URL), testKey, opts)
	if err != nil {
		t.Fatal(err)
	}
	var se *seam.Error
	err = p.Stream(context.Background(), hello(), func(seam.Event) error { return nil })
	if !errors.As(err, &se) || se.Kind != seam.ErrUnavailable || !se.Retryable {
		t.Fatalf("got %v, want an unavailable error that may be retried", err)
	}
}

func TestEmitErrorEndsTheCall(t *testing.T) {
	srv := streamServer(t, oneDelta)
	p, err := New(entry(srv.URL), testKey, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("the caller has had enough")
	calls := 0
	err = p.Stream(context.Background(), hello(), func(seam.Event) error {
		calls++
		return stop
	})
	if err != stop || calls != 1 {
		t.Fatalf("got %v after %d events, want the caller's error after one", err, calls)
	}
}

func TestUnreachableEndpointFails(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close() // nothing listens there now
	opts := testOptions
	opts.MaxRetries = 0 // a refused connection is slow to report on some systems
	p, err := New(entry(addr), testKey, opts)
	if err != nil {
		t.Fatal(err)
	}
	var se *seam.Error
	err = p.Stream(context.Background(), hello(), func(seam.Event) error { return nil })
	if !errors.As(err, &se) || se.Kind != seam.ErrUnavailable || !se.Retryable {
		t.Fatalf("got %v, want an unavailable error", err)
	}
	if strings.Contains(se.Message, addr) {
		t.Errorf("the message repeats the address: %s", se.Message)
	}
}

func TestRequestsTheAdapterRefuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a refused request reached the endpoint")
	}))
	defer srv.Close()
	p, err := New(entry(srv.URL), testKey, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	stored := hello()
	stored.Messages[0].Blocks = append(stored.Messages[0].Blocks, seam.Block{Type: seam.BlockImage, MediaType: "image/png", Ref: "0f0f"})
	for name, req := range map[string]seam.Request{"no messages": {}, "a stored file": stored} {
		var se *seam.Error
		err := p.Stream(context.Background(), req, func(seam.Event) error { return nil })
		if !errors.As(err, &se) || se.Kind != seam.ErrInvalid || se.Retryable {
			t.Errorf("%s: got %v, want an invalid error", name, err)
		}
	}
}

func TestNewRefusesAndHidesTheKey(t *testing.T) {
	good := entry("https://models.example/v1")
	if _, err := New(good, testKey, testOptions); err != nil {
		t.Fatal(err)
	}
	wrongAdapter := good
	wrongAdapter.Adapter = models.AdapterFake
	noEndpoint := good
	noEndpoint.Endpoint = ""
	badOptions := testOptions
	badOptions.IdleTimeout = 0
	for name, try := range map[string]func() error{
		"another adapter": func() error { _, err := New(wrongAdapter, testKey, testOptions); return err },
		"no endpoint":     func() error { _, err := New(noEndpoint, testKey, testOptions); return err },
		"bad options":     func() error { _, err := New(good, testKey, badOptions); return err },
		"a broken key":    func() error { _, err := New(good, testKey+"\n", testOptions); return err },
	} {
		err := try()
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: the error holds the key", name)
		}
	}
	p, _ := New(good, testKey, testOptions)
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(verb, p), testKey) {
			t.Errorf("printing the provider with %s shows the key", verb)
		}
	}
}

func TestNoKeyMeansNoHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["Authorization"]; ok {
			t.Error("an Authorization header was sent with no key")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()
	p, err := New(entry(srv.URL), "", testOptions)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seam.Call(context.Background(), p, hello()); err != nil {
		t.Fatal(err)
	}
}

func TestOptionsComeFromTheSettings(t *testing.T) {
	o, err := DefaultOptions()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkOptions(o); err != nil {
		t.Fatal(err)
	}
	if _, err := OptionsFrom([]settings.Setting{}); err == nil {
		t.Error("an empty registry gave options")
	}
}

func TestStatusesAreSorted(t *testing.T) {
	for status, want := range map[int]struct {
		kind  seam.ErrorKind
		retry bool
	}{
		401: {seam.ErrAuth, false}, 403: {seam.ErrAuth, false}, 404: {seam.ErrInvalid, false},
		408: {seam.ErrUnavailable, true}, 413: {seam.ErrTooLong, false}, 422: {seam.ErrInvalid, false},
		429: {seam.ErrRateLimited, true}, 500: {seam.ErrUnavailable, true}, 503: {seam.ErrOverloaded, true},
		504: {seam.ErrUnavailable, true}, 529: {seam.ErrOverloaded, true}, 418: {seam.ErrOther, false},
	} {
		e := statusError(status, []byte("not json"))
		if e.Kind != want.kind || e.Retryable != want.retry || e.Message == "" {
			t.Errorf("%d: got %s retryable %v, want %s retryable %v", status, e.Kind, e.Retryable, want.kind, want.retry)
		}
	}
}

func TestReadEvents(t *testing.T) {
	in := ": a comment\r\nevent: one\r\ndata: {\"a\":1}\r\n\r\ndata: first\ndata: second\n\nid: 7\nretry: 5\n\ndata:tight\n\ndata: cut off"
	var got []string
	err := readEvents(strings.NewReader(in), func(name string, data []byte) error {
		got = append(got, name+"|"+string(data))
		return nil
	})
	want := []string{`one|{"a":1}`, "|first\nsecond", "|tight"}
	if err != io.EOF || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q and %v, want %q and the end of the input", got, err, want)
	}
}
