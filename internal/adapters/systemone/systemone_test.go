package systemone

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// testKey is invented, and only ever sent to a server on this machine.
const testKey = "invented-test-key-for-systemone"

func quick() Options {
	return Options{Timeout: 2 * time.Second, MaxRequestBytes: 65536, MaxQuestions: 64}
}

// file is fixtures/systemone/exchanges.json.
type file struct {
	TestData  bool `json:"test_data"`
	Exchanges []struct {
		Name      string               `json:"name"`
		Entry     models.Model         `json:"entry"`
		UsesKey   bool                 `json:"uses_key"`
		Request   seam.DecisionRequest `json:"request"`
		Sent      json.RawMessage      `json:"sent"`
		SentOrder []string             `json:"sent_order"`
		Answer    json.RawMessage      `json:"answer"`
		Response  seam.DecisionResult  `json:"response"`
	} `json:"exchanges"`
	Refusals []struct {
		Name   string          `json:"name"`
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
		Kind   seam.ErrorKind  `json:"kind"`
	} `json:"refusals"`
	Malformed []struct {
		Name string          `json:"name"`
		Body json.RawMessage `json:"body"`
	} `json:"malformed"`
}

func readFile(t *testing.T) file {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "systemone", "exchanges.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if !f.TestData || len(f.Exchanges) == 0 || len(f.Refusals) == 0 || len(f.Malformed) == 0 {
		t.Fatal("the exchanges are not marked as test data, or a part is empty")
	}
	return f
}

func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// body writes a raw JSON value as the server's answer: a JSON string is
// sent as its text, so a body that is not JSON can be recorded.
func body(w io.Writer, raw json.RawMessage) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		io.WriteString(w, text)
		return
	}
	w.Write(raw)
}

// Each recorded exchange: the adapter sends the body it shows, in its
// order, to {endpoint}/v1/systemone with the key when it has one, and reads
// the answer into the seam's result, with each option's probability.
func TestExchanges(t *testing.T) {
	for _, x := range readFile(t).Exchanges {
		t.Run(x.Name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, _ := io.ReadAll(r.Body)
				switch {
				case r.Method != http.MethodPost || r.URL.Path != "/v1/systemone":
					t.Errorf("the call went to %s %s", r.Method, r.URL.Path)
				case !sameJSON(got, x.Sent):
					t.Errorf("the body sent is\n%s\nwant\n%s", got, x.Sent)
				case x.UsesKey != (r.Header.Get("Authorization") == "Bearer "+testKey):
					t.Errorf("the key header is %q, uses_key %v", r.Header.Get("Authorization"), x.UsesKey)
				}
				at := -1
				for _, name := range x.SentOrder {
					i := strings.Index(string(got), `"`+name+`":{`)
					if i <= at {
						t.Errorf("the question %s is not written in the request's order: %s", name, got)
					}
					at = i
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(x.Answer)
			}))
			defer srv.Close()
			entry := x.Entry
			entry.Endpoint = strings.Replace(entry.Endpoint, "http://127.0.0.1:11434", srv.URL, 1)
			key := ""
			if x.UsesKey {
				key = testKey
			}
			p, err := New(entry, key, quick())
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Decide(context.Background(), x.Request)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, x.Response) {
				t.Errorf("the result is %+v, want %+v", got, x.Response)
			}
			if err := seam.ValidateDecisionResult(x.Request, got); err != nil {
				t.Error(err)
			}
		})
	}
}

func entry(url string) models.Model {
	return models.Model{
		Name: "house-decide", Label: "House decisions", Adapter: models.AdapterSystemone, Endpoint: url,
		ProviderModel: "example-decide:9b", ContextWindow: 8192, OutputLimit: 1,
		Capabilities: []models.Capability{models.CapDecision}, Class: models.ClassLocal,
	}
}

// standIn answers every call with one status and body, and counts calls.
func standIn(t *testing.T, status int, raw json.RawMessage) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		body(w, raw)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// the request the malformed answers are read against.
var pair = seam.DecisionRequest{
	Model: "house-decide", State: "An invented state.",
	Questions: []seam.DecisionQuestion{
		{ID: "step", Instructions: "Pick one.", Type: seam.QuestionChoice, Options: []seam.DecisionOption{{ID: "a"}, {ID: "b"}}},
		{ID: "check", Instructions: "Yes or no?", Type: seam.QuestionYesNo},
	},
}

// A refusal becomes the seam error of its kind, sent once, and its message
// never holds the key.
func TestRefusals(t *testing.T) {
	for _, r := range readFile(t).Refusals {
		t.Run(r.Name, func(t *testing.T) {
			srv, calls := standIn(t, r.Status, r.Body)
			p, err := New(entry(srv.URL), testKey, quick())
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Decide(context.Background(), pair)
			var se *seam.Error
			if !errors.As(err, &se) || se.Kind != r.Kind {
				t.Fatalf("the refusal became %v, want the kind %s", err, r.Kind)
			}
			if strings.Contains(se.Message, testKey) {
				t.Error("the message holds the key")
			}
			if calls.Load() != 1 {
				t.Errorf("the call was sent %d times, want once", calls.Load())
			}
		})
	}
	// A refusal that repeats the key has it taken out.
	srv, _ := standIn(t, http.StatusUnauthorized, json.RawMessage(`{"error": "the key invented-test-key-for-systemone is not known"}`))
	p, err := New(entry(srv.URL), testKey, quick())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Decide(context.Background(), pair); err == nil || strings.Contains(err.Error(), testKey) {
		t.Errorf("the refusal kept the key: %v", err)
	}
}

// An answer that does not answer the request is refused as other.
func TestMalformed(t *testing.T) {
	for _, m := range readFile(t).Malformed {
		t.Run(m.Name, func(t *testing.T) {
			srv, calls := standIn(t, http.StatusOK, m.Body)
			p, err := New(entry(srv.URL), "", quick())
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Decide(context.Background(), pair)
			var se *seam.Error
			if !errors.As(err, &se) || se.Kind != seam.ErrOther || calls.Load() != 1 {
				t.Errorf("the answer became %v after %d calls, want an error of the kind other after one", err, calls.Load())
			}
		})
	}
}

// The proof's second line: a request over the size limit or the question
// limit is refused before it is sent.
func TestLimitsAreKeptBeforeSending(t *testing.T) {
	srv, calls := standIn(t, http.StatusOK, json.RawMessage(`{}`))
	opts := quick()
	opts.MaxQuestions = 2
	opts.MaxRequestBytes = 600
	p, err := New(entry(srv.URL), "", opts)
	if err != nil {
		t.Fatal(err)
	}
	three := pair
	three.Questions = append(append([]seam.DecisionQuestion(nil), pair.Questions...),
		seam.DecisionQuestion{ID: "third", Instructions: "And this?", Type: seam.QuestionYesNo})
	_, err = p.Decide(context.Background(), three)
	var se *seam.Error
	if !errors.As(err, &se) || se.Kind != seam.ErrTooLong || !strings.Contains(se.Message, "3 questions") {
		t.Errorf("three questions over a limit of two gave %v", err)
	}
	big := pair
	big.State = strings.Repeat("A long invented state. ", 40)
	_, err = p.Decide(context.Background(), big)
	if !errors.As(err, &se) || se.Kind != seam.ErrTooLong || !strings.Contains(se.Message, "at most 600") {
		t.Errorf("a request over 600 bytes gave %v", err)
	}
	// Exactly at the limit is sent.
	sized, err := Body("example-decide:9b", pair)
	if err != nil {
		t.Fatal(err)
	}
	opts.MaxRequestBytes = len(sized)
	p, _ = New(entry(srv.URL), "", opts)
	if _, err := p.Decide(context.Background(), pair); errors.As(err, &se) && se.Kind == seam.ErrTooLong {
		t.Errorf("a request exactly at the limit was refused: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("%d calls reached the endpoint, want only the one at the limit", calls.Load())
	}
	// A request that is not valid is not sent either.
	bad := pair
	bad.Questions = []seam.DecisionQuestion{{ID: "one", Instructions: "Pick.", Type: seam.QuestionChoice, Options: []seam.DecisionOption{{ID: "only"}}}}
	if _, err := p.Decide(context.Background(), bad); !errors.As(err, &se) || se.Kind != seam.ErrInvalid || calls.Load() != 1 {
		t.Errorf("a choice of one option gave %v, with %d calls", err, calls.Load())
	}
}

// A call not answered within the timeout is given up as unavailable, and a
// call whose context is done returns the context's error.
func TestTimeoutAndCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	opts := quick()
	opts.Timeout = 100 * time.Millisecond
	p, err := New(entry(srv.URL), "", opts)
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	_, err = p.Decide(context.Background(), pair)
	var se *seam.Error
	if !errors.As(err, &se) || se.Kind != seam.ErrUnavailable || !strings.Contains(se.Message, "100ms") {
		t.Errorf("a late answer gave %v", err)
	}
	if time.Since(began) > 2*time.Second {
		t.Errorf("the timeout took %s", time.Since(began))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Decide(ctx, pair); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled call gave %v", err)
	}
}

// The entry writes no reply, a redirect is not followed, and an entry that
// is not a systemone decision model is refused.
func TestStreamRedirectAndEntry(t *testing.T) {
	p, err := New(entry("http://127.0.0.1:11434"), "", quick())
	if err != nil {
		t.Fatal(err)
	}
	var events []seam.Event
	err = p.Stream(context.Background(), seam.Request{Model: "house-decide"}, func(e seam.Event) error { events = append(events, e); return nil })
	var se *seam.Error
	if !errors.As(err, &se) || se.Kind != seam.ErrInvalid || len(events) != 1 || events[0].Type != seam.EventError {
		t.Errorf("a reply asked of a decision model gave %v and %+v", err, events)
	}

	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere.Add(1) }))
	defer other.Close()
	moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/v1/systemone", http.StatusTemporaryRedirect)
	}))
	defer moved.Close()
	p, _ = New(entry(moved.URL), "", quick())
	if _, err := p.Decide(context.Background(), pair); err == nil || elsewhere.Load() != 0 {
		t.Errorf("a redirect answered %v and reached the other address %d times", err, elsewhere.Load())
	}

	chat := entry("http://127.0.0.1:11434")
	chat.Adapter = models.AdapterChatCompletions
	if _, err := New(chat, "", quick()); err == nil {
		t.Error("an entry of another adapter was taken")
	}
	if _, err := New(entry("http://127.0.0.1:11434"), "", Options{}); err == nil {
		t.Error("options with no limits were taken")
	}
	for in, want := range map[string]string{
		"http://127.0.0.1:11434":     "http://127.0.0.1:11434/v1/systemone",
		"http://127.0.0.1:11434/":    "http://127.0.0.1:11434/v1/systemone",
		"http://127.0.0.1:11434/v1":  "http://127.0.0.1:11434/v1/systemone",
		"http://gateway.lan/ollama/": "http://gateway.lan/ollama/v1/systemone",
	} {
		if got := decideURL(in); got != want {
			t.Errorf("decideURL(%s) is %s, want %s", in, got, want)
		}
	}
}
