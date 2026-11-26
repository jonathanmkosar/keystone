package chatcompletions

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

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// embedFile is fixtures/chat-completions/embeddings/exchanges.json.
type embedFile struct {
	TestData  bool `json:"test_data"`
	About     string
	Exchanges []struct {
		Name     string             `json:"name"`
		Style    string             `json:"style"`
		About    string             `json:"about"`
		Entry    models.Model       `json:"entry"`
		UsesKey  bool               `json:"uses_key"`
		Request  seam.EmbedRequest  `json:"request"`
		Sent     json.RawMessage    `json:"sent"`
		Answer   json.RawMessage    `json:"answer"`
		Response seam.EmbedResponse `json:"response"`
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

func readEmbedFile(t *testing.T) embedFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtures, "embeddings", "exchanges.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f embedFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if !f.TestData || len(f.Exchanges) == 0 {
		t.Fatal("the embedding exchanges are not marked as test data, or there are none")
	}
	return f
}

// sameEmbedJSON reports whether two JSON texts hold the same value.
func sameEmbedJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// Each hand-written exchange: the adapter sends the body it shows to
// {endpoint}/embeddings, with the key when it has one, and reads the
// answer into the seam's response.
func TestEmbedExchanges(t *testing.T) {
	for _, x := range readEmbedFile(t).Exchanges {
		t.Run(x.Name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				switch {
				case r.Method != http.MethodPost || r.URL.Path != "/v1/embeddings":
					t.Errorf("the call went to %s %s", r.Method, r.URL.Path)
				case !sameEmbedJSON(body, x.Sent):
					t.Errorf("the body sent is\n%s\nwant\n%s", body, x.Sent)
				case x.UsesKey != (r.Header.Get("Authorization") == "Bearer "+testKey):
					t.Errorf("the call's key header is %q, uses_key %v", r.Header.Get("Authorization"), x.UsesKey)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write(x.Answer)
			}))
			defer srv.Close()
			entry := x.Entry
			entry.Endpoint = srv.URL + "/v1"
			key := ""
			if x.UsesKey {
				key = testKey
			}
			p, err := New(entry, key, quick())
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Embed(context.Background(), x.Request)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, x.Response) {
				t.Errorf("the response is %+v, want %+v", got, x.Response)
			}
		})
	}
}

// standIn is a server that answers every embedding call with one status
// and body, and counts the calls.
func standIn(t *testing.T, status int, body []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		var text string
		if json.Unmarshal(body, &text) == nil {
			io.WriteString(w, text)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func embedEntry(url string) models.Model {
	return models.Model{
		Name: "house-embed", Label: "House embed", Adapter: models.AdapterChatCompletions, Endpoint: url + "/v1",
		ProviderModel: "example-embed:v1", ContextWindow: 8192, OutputLimit: 1,
		Capabilities: []models.Capability{models.CapEmbedding}, Class: models.ClassLocal,
	}
}

// A refusal becomes the seam error of its kind, a retryable one is sent
// again up to the limit, and no message holds the key.
func TestEmbedRefusals(t *testing.T) {
	two := []string{"one", "two"}
	for _, r := range readEmbedFile(t).Refusals {
		t.Run(r.Name, func(t *testing.T) {
			srv, calls := standIn(t, r.Status, r.Body)
			opts := quick()
			opts.MaxAttempts = 2
			p, err := New(embedEntry(srv.URL), testKey, opts)
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Embed(context.Background(), seam.EmbedRequest{Model: "house-embed", Texts: two})
			var se *seam.Error
			if !errors.As(err, &se) || se.Kind != r.Kind {
				t.Fatalf("the refusal became %v, want the kind %s", err, r.Kind)
			}
			if strings.Contains(se.Message, testKey) {
				t.Error("the message holds the key")
			}
			want := int32(1)
			if se.Retryable {
				want = 2
			}
			if calls.Load() != want {
				t.Errorf("the call was sent %d times, want %d", calls.Load(), want)
			}
		})
	}
}

// An answer that is not one usable vector for each text is refused, and
// not sent again.
func TestEmbedMalformed(t *testing.T) {
	for _, m := range readEmbedFile(t).Malformed {
		t.Run(m.Name, func(t *testing.T) {
			srv, calls := standIn(t, http.StatusOK, m.Body)
			opts := quick()
			opts.MaxAttempts = 3
			p, err := New(embedEntry(srv.URL), "", opts)
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Embed(context.Background(), seam.EmbedRequest{Model: "house-embed", Texts: []string{"one", "two"}})
			var se *seam.Error
			if !errors.As(err, &se) || se.Kind != seam.ErrOther || calls.Load() != 1 {
				t.Errorf("the answer became %v after %d calls, want an error of the kind other after one", err, calls.Load())
			}
		})
	}
}

// An entry that is not an embedding model sends nothing, and neither does
// a request with a blank text or a context that is done.
func TestEmbedSendsNothing(t *testing.T) {
	srv, calls := standIn(t, http.StatusOK, []byte(`{"data":[]}`))
	chat := embedEntry(srv.URL)
	chat.Capabilities = []models.Capability{models.CapTools}
	p, err := New(chat, "", quick())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Embed(context.Background(), seam.EmbedRequest{Model: "house-embed", Texts: []string{"a"}}); err == nil {
		t.Error("a chat model made vectors")
	}
	p, err = New(embedEntry(srv.URL), "", quick())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Embed(context.Background(), seam.EmbedRequest{Model: "house-embed", Texts: []string{"a", " "}}); err == nil {
		t.Error("a blank text was sent")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Embed(ctx, seam.EmbedRequest{Model: "house-embed", Texts: []string{"a"}}); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled call answered %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("%d calls reached the endpoint", calls.Load())
	}
	// A redirect is not followed: the texts go to the entry's address only.
	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere.Add(1) }))
	defer other.Close()
	moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/v1/embeddings", http.StatusTemporaryRedirect)
	}))
	defer moved.Close()
	p, err = New(embedEntry(moved.URL), "", quick())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Embed(context.Background(), seam.EmbedRequest{Model: "house-embed", Texts: []string{"a"}}); err == nil || elsewhere.Load() != 0 {
		t.Errorf("a redirect answered %v and reached the other address %d times", err, elsewhere.Load())
	}
}
