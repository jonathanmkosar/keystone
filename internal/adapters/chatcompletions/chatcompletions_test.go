package chatcompletions

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
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

// testKey stands in for a provider key. It is not one.
const testKey = "an invented value, not a real key"

var fixtures = filepath.Join("..", "..", "..", "fixtures", "chat-completions")

// quick are options that fail fast and do not retry.
func quick() Options {
	return Options{
		ConnectTimeout: 5 * time.Second, ResponseTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second,
		MaxAttempts: 1, RetryBase: time.Millisecond, RetryMax: 5 * time.Millisecond,
	}
}

// exchange is one recorded exchange, as the fixture files hold it.
type exchange struct {
	TestData bool              `json:"test_data"`
	Name     string            `json:"name"`
	Style    string            `json:"style"`
	About    string            `json:"about"`
	Entry    models.Model      `json:"entry"`
	UsesKey  bool              `json:"uses_key"`
	Request  seam.Request      `json:"request"`
	Sent     json.RawMessage   `json:"sent"`
	Stream   []json.RawMessage `json:"stream"`
	Response seam.Response     `json:"response"`
}

func readJSON(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func exchanges(t *testing.T) []exchange {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(fixtures, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var all []exchange
	for _, n := range names {
		if filepath.Base(n) == "errors.json" {
			continue
		}
		var x exchange
		readJSON(t, filepath.Base(n), &x)
		if !x.TestData {
			t.Fatalf("%s does not say it is test data", n)
		}
		all = append(all, x)
	}
	return all
}

// sse writes a recorded stream. An object is one data event. A string is a
// line sent as it is: a comment, or the mark that ends the stream.
func sse(w http.ResponseWriter, stream []json.RawMessage) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	for _, item := range stream {
		var line string
		if json.Unmarshal(item, &line) != nil {
			line = "data: " + compact(item)
		}
		io.WriteString(w, line+"\n\n")
		w.(http.Flusher).Flush()
	}
}

func compact(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		panic(err)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

func sameJSON(t *testing.T, a, b any) bool {
	t.Helper()
	norm := func(v any) any {
		raw, ok := v.(json.RawMessage)
		if !ok {
			var err error
			if raw, err = json.Marshal(v); err != nil {
				t.Fatal(err)
			}
		}
		var out any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

// entry is a local entry that points at a test server.
func entry(url string) models.Model {
	return models.Model{
		Name: "house-small", Label: "House small", Adapter: models.AdapterChatCompletions,
		Endpoint: url + "/v1", ProviderModel: "example-small:8b",
		ContextWindow: 8192, OutputLimit: 2048, Class: models.ClassLocal,
	}
}

func ask(text string) seam.Request {
	return seam.Request{Model: "house-small", Messages: []seam.Message{
		{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}},
	}}
}

func provider(t *testing.T, m models.Model, key string, opts Options) *Provider {
	t.Helper()
	p, err := New(m, key, opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// chunk is one data event holding a piece of text.
func chunk(text string) string {
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}}}})
	return "data: " + string(raw) + "\n\n"
}

const stopChunk = `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}` + "\n\n" + "data: [DONE]\n\n"

// authority makes a certificate authority and a server certificate signed
// by it for 127.0.0.1, at test time. Nothing of them is kept.
func authority(t *testing.T) (caPEM string, server tls.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Invented Test Authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "invented gateway"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	return caPEM, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// gateway starts a TLS server whose certificate only the returned
// authority vouches for.
func gateway(t *testing.T, h http.Handler) (url, caPEM string) {
	t.Helper()
	caPEM, cert := authority(t)
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.Config.ErrorLog = nil
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.URL, caPEM
}

func TestRecordedExchangesForEachEndpointStyle(t *testing.T) {
	styles := map[string]bool{}
	for _, x := range exchanges(t) {
		t.Run(x.Name, func(t *testing.T) {
			styles[x.Style] = true
			if err := models.Check(x.Entry); err != nil {
				t.Fatalf("the fixture's entry is refused: %v", err)
			}
			var problems []string
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
					problems = append(problems, "called "+r.Method+" "+r.URL.Path)
				}
				want := ""
				if x.UsesKey {
					want = "Bearer " + testKey
				}
				if got := r.Header.Get("Authorization"); got != want {
					problems = append(problems, "the Authorization header is wrong")
				}
				body, _ := io.ReadAll(r.Body)
				if !sameJSON(t, json.RawMessage(body), x.Sent) {
					problems = append(problems, "sent:\n"+string(body)+"\nwant:\n"+compact(x.Sent))
				}
				sse(w, x.Stream)
			})
			m, key := x.Entry, ""
			if x.UsesKey {
				key = testKey
			}
			if x.Style == "gateway" {
				// The gateway has its own certificate authority.
				m.Endpoint, m.CACert = gateway(t, h)
				m.Endpoint += "/v1"
			} else {
				srv := httptest.NewServer(h)
				defer srv.Close()
				m.Endpoint = srv.URL + "/v1"
			}
			got, err := seam.Call(context.Background(), provider(t, m, key, quick()), x.Request)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range problems {
				t.Error(p)
			}
			want := x.Response
			if want.Usage.Estimated {
				// An estimate is checked for being one, not for its size.
				if !got.Usage.Estimated || got.Usage.InputTokens <= 0 || got.Usage.OutputTokens <= 0 {
					t.Errorf("the usage is not an estimate: %+v", got.Usage)
				}
				got.Usage, want.Usage = seam.Usage{}, seam.Usage{}
			}
			if !sameJSON(t, got, want) {
				g, _ := json.Marshal(got)
				w, _ := json.Marshal(want)
				t.Errorf("the reply is\n%s\nwant\n%s", g, w)
			}
		})
	}
	for _, s := range []string{"ollama", "openrouter", "litellm", "gateway"} {
		if !styles[s] {
			t.Errorf("no recorded exchange for the %s style", s)
		}
	}
}

type errorCase struct {
	Name      string            `json:"name"`
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers"`
	Body      json.RawMessage   `json:"body"`
	Chunk     json.RawMessage   `json:"chunk"`
	Kind      seam.ErrorKind    `json:"kind"`
	Retryable bool              `json:"retryable"`
	Says      string            `json:"says"`
}

func errorCases(t *testing.T) (refusals, inStream []errorCase) {
	t.Helper()
	var file struct {
		TestData bool        `json:"test_data"`
		About    string      `json:"about"`
		Errors   []errorCase `json:"errors"`
		InStream []errorCase `json:"in_stream"`
	}
	readJSON(t, "errors.json", &file)
	if !file.TestData || len(file.Errors) == 0 || len(file.InStream) == 0 {
		t.Fatal("errors.json is not test data, or is empty")
	}
	return file.Errors, file.InStream
}

// failed runs a call that must fail, and returns its error and the events.
func failed(t *testing.T, p *Provider, req seam.Request) (*seam.Error, []seam.Event) {
	t.Helper()
	var events []seam.Event
	err := p.Stream(context.Background(), req, func(e seam.Event) error {
		events = append(events, e)
		return nil
	})
	var failure *seam.Error
	if !errors.As(err, &failure) {
		t.Fatalf("the call returned %v, and a seam error was wanted", err)
	}
	if len(events) == 0 || events[len(events)-1].Type != seam.EventError || events[len(events)-1].Error != failure {
		t.Fatalf("the stream does not end with the error event")
	}
	for _, e := range events {
		if err := seam.ValidateEvent(e); err != nil {
			t.Errorf("an event is not valid: %v", err)
		}
	}
	return failure, events
}

func TestEachRefusalBecomesItsSeamError(t *testing.T) {
	refusals, _ := errorCases(t)
	kinds := map[seam.ErrorKind]bool{}
	for _, c := range refusals {
		t.Run(c.Name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range c.Headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(c.Status)
				var text string
				if json.Unmarshal(c.Body, &text) == nil {
					io.WriteString(w, text)
				} else {
					w.Write(c.Body)
				}
			}))
			defer srv.Close()
			failure, events := failed(t, provider(t, entry(srv.URL), "", quick()), ask("Hello."))
			if failure.Kind != c.Kind || failure.Retryable != c.Retryable {
				t.Errorf("got %s, retryable %v; want %s, retryable %v", failure.Kind, failure.Retryable, c.Kind, c.Retryable)
			}
			if !strings.Contains(failure.Message, c.Says) {
				t.Errorf("the message %q does not say %q", failure.Message, c.Says)
			}
			if len(events) != 1 {
				t.Errorf("%d events were sent for a refusal, want the error alone", len(events))
			}
			kinds[failure.Kind] = true
		})
	}
	for _, k := range []seam.ErrorKind{seam.ErrAuth, seam.ErrRateLimited, seam.ErrOverloaded, seam.ErrTooLong, seam.ErrInvalid, seam.ErrUnavailable, seam.ErrOther} {
		if !kinds[k] {
			t.Errorf("no refusal in errors.json becomes %s", k)
		}
	}
}

func TestAnErrorInsideAReplyEndsIt(t *testing.T) {
	_, inStream := errorCases(t)
	for _, c := range inStream {
		t.Run(c.Name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, chunk("Half an ans")+"data: "+compact(c.Chunk)+"\n\n")
			}))
			defer srv.Close()
			opts := quick()
			opts.MaxAttempts = 3
			failure, events := failed(t, provider(t, entry(srv.URL), "", opts), ask("Hello."))
			if failure.Kind != c.Kind || failure.Retryable != c.Retryable || !strings.Contains(failure.Message, c.Says) {
				t.Errorf("got %s, retryable %v, %q", failure.Kind, failure.Retryable, failure.Message)
			}
			if events[0].Type != seam.EventStart {
				t.Error("the text before the error was not sent on")
			}
			// What the caller has seen cannot be taken back, so no retry.
			if hits.Load() != 1 {
				t.Errorf("the call was sent %d times after the reply had begun", hits.Load())
			}
		})
	}
}

func TestABusyEndpointIsTriedAgainAndARefusedKeyIsNot(t *testing.T) {
	serve := func(statuses ...int) (*httptest.Server, *atomic.Int32) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := int(hits.Add(1))
			if n <= len(statuses) {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(statuses[n-1])
				io.WriteString(w, `{"error":{"message":"not now"}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, chunk("Here.")+stopChunk)
		}))
		t.Cleanup(srv.Close)
		return srv, &hits
	}
	opts := quick()
	opts.MaxAttempts = 3

	srv, hits := serve(503, 429)
	got, err := seam.Call(context.Background(), provider(t, entry(srv.URL), "", opts), ask("Hello."))
	if err != nil || seam.Text(got.Message) != "Here." || hits.Load() != 3 {
		t.Errorf("after two busy answers: %v, %q, %d calls", err, seam.Text(got.Message), hits.Load())
	}

	srv, hits = serve(503, 503, 503, 503)
	failure, _ := failed(t, provider(t, entry(srv.URL), "", opts), ask("Hello."))
	if failure.Kind != seam.ErrOverloaded || hits.Load() != 3 {
		t.Errorf("an endpoint that stays busy: %s after %d calls, want overloaded after 3", failure.Kind, hits.Load())
	}

	srv, hits = serve(401, 401)
	failure, _ = failed(t, provider(t, entry(srv.URL), "", opts), ask("Hello."))
	if failure.Kind != seam.ErrAuth || hits.Load() != 1 {
		t.Errorf("a refused key: %s after %d calls, want auth after 1", failure.Kind, hits.Load())
	}
}

func TestTheWaitGrowsAndIsCapped(t *testing.T) {
	p := &Provider{opts: Options{RetryBase: 100 * time.Millisecond, RetryMax: time.Second}}
	for attempt, most := range map[int]time.Duration{1: 100 * time.Millisecond, 2: 200 * time.Millisecond, 3: 400 * time.Millisecond, 9: time.Second} {
		for range 20 {
			if d := p.backoff(attempt, 0); d < most/2 || d > most {
				t.Errorf("attempt %d waits %s, want between %s and %s", attempt, d, most/2, most)
			}
		}
	}
	if d := p.backoff(1, 600*time.Millisecond); d != 600*time.Millisecond {
		t.Errorf("the endpoint asked for 600ms and the wait is %s", d)
	}
	if d := p.backoff(1, time.Hour); d != time.Second {
		t.Errorf("the endpoint asked for an hour and the wait is %s, want the cap", d)
	}
}

func TestAReplyThatBreaksOffIsUnavailable(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, chunk("Half an ans"))
	}))
	defer srv.Close()
	opts := quick()
	opts.MaxAttempts = 3
	failure, _ := failed(t, provider(t, entry(srv.URL), "", opts), ask("Hello."))
	if failure.Kind != seam.ErrUnavailable || !failure.Retryable || hits.Load() != 1 {
		t.Errorf("got %s, retryable %v, %d calls", failure.Kind, failure.Retryable, hits.Load())
	}
}

func TestAnEndpointThatIsNotThereIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	failure, _ := failed(t, provider(t, entry(url), "", quick()), ask("Hello."))
	if failure.Kind != seam.ErrUnavailable || !failure.Retryable {
		t.Errorf("got %s, retryable %v", failure.Kind, failure.Retryable)
	}
	if strings.Contains(failure.Message, "127.0.0.1") {
		t.Errorf("the message repeats the address: %s", failure.Message)
	}
}

// stalls sends one piece and then nothing, until the caller goes away.
func stalls(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, chunk("One piece"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCancellingStopsTheCall(t *testing.T) {
	srv := stalls(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	after := 0
	cancelled := false
	err := provider(t, entry(srv.URL), "", quick()).Stream(ctx, ask("Hello."), func(e seam.Event) error {
		if cancelled {
			after++
		}
		if e.Type == seam.EventBlockDelta {
			cancelled = true
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || after != 0 {
		t.Errorf("got %v and %d events after the cancel", err, after)
	}
}

func TestAReceiverThatGoesAwayEndsTheCall(t *testing.T) {
	srv := stalls(t)
	gone := errors.New("the receiver has gone")
	n := 0
	err := provider(t, entry(srv.URL), "", quick()).Stream(context.Background(), ask("Hello."), func(e seam.Event) error {
		n++
		if e.Type == seam.EventBlockDelta {
			return gone
		}
		return nil
	})
	if err != gone || n != 3 {
		t.Errorf("got %v after %d events, want the receiver's error after the start, the block and one piece", err, n)
	}
}

func TestAReplyThatGoesQuietIsEnded(t *testing.T) {
	srv := stalls(t)
	opts := quick()
	opts.IdleTimeout = 80 * time.Millisecond
	failure, _ := failed(t, provider(t, entry(srv.URL), "", opts), ask("Hello."))
	if failure.Kind != seam.ErrUnavailable || !strings.Contains(failure.Message, "sent nothing") {
		t.Errorf("got %s: %s", failure.Kind, failure.Message)
	}
}

func TestTheKeyIsInNoError(t *testing.T) {
	// An endpoint that repeats the key it was sent, in every way it can.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.URL.Path == "/stream/v1/chat/completions" {
			w.Header().Set("Content-Type", "text/event-stream")
			raw, _ := json.Marshal(map[string]any{"error": map[string]any{"message": "bad key " + sent}})
			io.WriteString(w, "data: "+string(raw)+"\n\n")
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "Incorrect key provided: " + sent}})
	}))
	defer srv.Close()
	for _, path := range []string{"", "/stream"} {
		failure, events := failed(t, provider(t, entry(srv.URL+path), testKey, quick()), ask("Hello."))
		raw, _ := json.Marshal(events)
		if strings.Contains(failure.Error(), testKey) || strings.Contains(string(raw), testKey) {
			t.Errorf("the key is in the error: %s", failure.Message)
		}
		if !strings.Contains(failure.Message, "(the key)") {
			t.Errorf("the endpoint's reason was lost: %s", failure.Message)
		}
	}
	// A builder's refusal does not hold it either.
	bad := entry("https://127.0.0.1:1")
	bad.CACert = filepath.Join(t.TempDir(), "missing.pem")
	if _, err := New(bad, testKey, quick()); err == nil || strings.Contains(err.Error(), testKey) {
		t.Errorf("a missing certificate file: %v", err)
	}
}

func TestACertificateIsTrustedOnlyThroughTheEntry(t *testing.T) {
	url, caPEM := gateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, chunk("Here.")+stopChunk)
	}))
	other, _ := authority(t)
	file := filepath.Join(t.TempDir(), "authority.pem")
	if err := os.WriteFile(file, []byte(caPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := quick()
	opts.MaxAttempts = 3

	for name, cert := range map[string]string{"with no ca_cert": "", "with another authority": other} {
		m := entry(url)
		m.CACert = cert
		failure, _ := failed(t, provider(t, m, testKey, opts), ask("Hello."))
		if failure.Kind != seam.ErrUnavailable || failure.Retryable || !strings.Contains(failure.Message, "certificate is not trusted") {
			t.Errorf("%s: got %s, retryable %v: %s", name, failure.Kind, failure.Retryable, failure.Message)
		}
	}
	for name, cert := range map[string]string{"as PEM text": caPEM, "as a file": file} {
		m := entry(url)
		m.CACert = cert
		got, err := seam.Call(context.Background(), provider(t, m, testKey, opts), ask("Hello."))
		if err != nil || seam.Text(got.Message) != "Here." {
			t.Errorf("%s: %v", name, err)
		}
	}

	// The authority in an entry is the only one that entry trusts.
	p := provider(t, func() models.Model { m := entry(url); m.CACert = caPEM; return m }(), "", opts)
	conf := p.client.Transport.(*http.Transport).TLSClientConfig
	if conf.RootCAs == nil || conf.MinVersion < tls.VersionTLS12 {
		t.Error("the entry's authority is not the transport's root, or old TLS is allowed")
	}
}

func TestACertificateThatIsNotOneIsRefused(t *testing.T) {
	_, cert := authority(t)
	der, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	// Written to a file, because the registry already refuses one as text.
	keyFile := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"a private key":  keyFile,
		"no certificate": empty,
		"broken PEM":     "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n",
	} {
		m := entry("https://127.0.0.1:1")
		m.CACert = value
		if _, err := New(m, "", quick()); err == nil {
			t.Errorf("%s was accepted as ca_cert", name)
		}
	}
}

func TestNothingSwitchesCertificateChecksOff(t *testing.T) {
	// The field's name is put together here so this file does not hold it.
	field := "Insecure" + "SkipVerify"
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatal("no source files found")
	}
	for _, f := range append(files, filepath.Join("..", "chatcompletions.go")) {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), field) {
			t.Errorf("%s names %s: an entry may add an authority, and nothing may turn the check off", f, field)
		}
	}
}

func TestALocalEntryIsCalledWithoutAProxy(t *testing.T) {
	local := provider(t, entry("http://127.0.0.1:11434"), "", quick())
	if local.client.Transport.(*http.Transport).Proxy != nil {
		t.Error("a local entry would be sent through the environment's proxy")
	}
	hosted := entry("https://router.provider-three.example/api")
	hosted.Class = models.ClassHosted
	if provider(t, hosted, testKey, quick()).client.Transport.(*http.Transport).Proxy == nil {
		t.Error("a hosted entry ignores the environment's proxy")
	}
}

func TestAReplyThatWasNotStreamedIsRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		io.WriteString(w, `{"id":"chatcmpl-601","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"One moment.","tool_calls":[{"id":"call_whole01","type":"function","function":{"name":"ferry_times","arguments":"{\"crossing\":\"Pell Island\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":40,"completion_tokens":18}}`)
	}))
	defer srv.Close()
	got, err := seam.Call(context.Background(), provider(t, entry(srv.URL), "", quick()), ask("Hello."))
	if err != nil {
		t.Fatal(err)
	}
	calls := seam.ToolCalls(got.Message)
	if seam.Text(got.Message) != "One moment." || len(calls) != 1 || calls[0].ID != "call_whole01" ||
		got.StopReason != seam.StopToolCall || got.Usage.InputTokens != 40 || got.Usage.OutputTokens != 18 {
		t.Errorf("got %+v", got)
	}
}

// replyTo runs one call against a server that sends the given events.
func replyTo(t *testing.T, events ...string) (seam.Response, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range events {
			io.WriteString(w, "data: "+e+"\n\n")
		}
	}))
	defer srv.Close()
	return seam.Call(context.Background(), provider(t, entry(srv.URL), "", quick()), ask("Hello."))
}

func TestToolCallsAsEndpointsReallySendThem(t *testing.T) {
	// No index, the same index again with a whole call each, arguments
	// sent as an object, and a call with no arguments at all.
	got, err := replyTo(t,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_a","function":{"name":"forecast","arguments":{"place":"Fenwater"}}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_b","function":{"name":"forecast","arguments":"{\"place\":\"Larkfield\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_c","function":{"name":"ferry_times"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`, `[DONE]`)
	if err != nil {
		t.Fatal(err)
	}
	calls := seam.ToolCalls(got.Message)
	if len(calls) != 3 {
		t.Fatalf("got %d calls: %+v", len(calls), calls)
	}
	if calls[0].ID != "call_a" || string(calls[0].Input) != `{"place":"Fenwater"}` ||
		calls[1].ID != "call_b" || calls[2].ID != "call_c" || string(calls[2].Input) != "{}" {
		t.Errorf("got %+v", calls)
	}

	// A call with no id at all is given one.
	got, err = replyTo(t,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"forecast","arguments":"{}"}}]},"finish_reason":"stop"}]}`, `[DONE]`)
	if err != nil {
		t.Fatal(err)
	}
	if calls := seam.ToolCalls(got.Message); len(calls) != 1 || !strings.HasPrefix(calls[0].ID, "call_") || got.StopReason != seam.StopToolCall {
		t.Errorf("got %+v", got)
	}

	// A call cut off by the output limit is dropped, and the text is kept.
	got, err = replyTo(t,
		`{"choices":[{"index":0,"delta":{"content":"Looking."}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_d","function":{"name":"forecast","arguments":"{\"place\":\"Fen"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`, `[DONE]`)
	if err != nil || len(seam.ToolCalls(got.Message)) != 0 || got.StopReason != seam.StopMaxTokens || seam.Text(got.Message) != "Looking." {
		t.Errorf("a cut-off call: %v, %+v", err, got)
	}

	// Input that is not JSON, in a reply that claims to be whole, fails.
	_, err = replyTo(t,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_e","function":{"name":"forecast","arguments":"{\"place\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`, `[DONE]`)
	var failure *seam.Error
	if !errors.As(err, &failure) || failure.Kind != seam.ErrOther {
		t.Errorf("broken tool input: %v", err)
	}

	// A refusal is text, and the reply stops as refused.
	got, err = replyTo(t,
		`{"choices":[{"index":0,"delta":{"refusal":"I cannot help with that."}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, `[DONE]`)
	if err != nil || got.StopReason != seam.StopRefused || seam.Text(got.Message) != "I cannot help with that." {
		t.Errorf("a refusal: %v, %+v", err, got)
	}
}

func TestARequestThatCannotBeSentIsRefusedBeforeAnyCall(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	p := provider(t, entry(srv.URL), "", quick())
	stored := seam.Request{Messages: []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{
		{Type: seam.BlockImage, MediaType: "image/png", Ref: "an-invented-hash"},
	}}}}
	for name, req := range map[string]seam.Request{"no messages": {}, "an image that was not read": stored} {
		failure, _ := failed(t, p, req)
		if failure.Kind != seam.ErrInvalid || failure.Retryable {
			t.Errorf("%s: got %s", name, failure.Kind)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("the endpoint was called %d times", hits.Load())
	}
}

func TestAnEntryForAnotherAdapterAndBadOptionsAreRefused(t *testing.T) {
	m := entry("http://127.0.0.1:11434")
	m.Adapter = models.AdapterResponses
	if _, err := New(m, "", quick()); err == nil {
		t.Error("an entry for the responses adapter was accepted")
	}
	for name, change := range map[string]func(*Options){
		"no attempts":      func(o *Options) { o.MaxAttempts = 0 },
		"no idle timeout":  func(o *Options) { o.IdleTimeout = 0 },
		"a cap below base": func(o *Options) { o.RetryMax = o.RetryBase / 2 },
	} {
		opts := quick()
		change(&opts)
		if _, err := New(entry("http://127.0.0.1:11434"), "", opts); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := New(entry("http://127.0.0.1:11434"), "two\nlines", quick()); err == nil || strings.Contains(err.Error(), "two") {
		t.Errorf("a key with a line break: %v", err)
	}
}

func TestEventsAreReadFromAnyLineEnding(t *testing.T) {
	var got []string
	in := ": a comment\r\nevent: message\r\ndata: one\r\n\r\ndata:two\ndata: three\n\nid: 7\n\ndata: last"
	if err := readEvents(strings.NewReader(in), func(d []byte) bool { got = append(got, string(d)); return true }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"one", "two\nthree", "last"}) {
		t.Errorf("got %q", got)
	}
}
