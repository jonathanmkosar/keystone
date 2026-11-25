package anthropic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// KeyOutputs is this adapter's part of verify's "no keys in output" check.
// It builds a provider with key and calls it against stand-in endpoints
// that fail in each way a real one can, several of them repeating the key
// in what they answer. It returns everything the adapter gave back: each
// error, each event, and the provider as printed.
//
// No connection is opened. The stand-ins answer in this process. The error
// is for a check that proved nothing, because the key never reached the
// endpoint.
func KeyOutputs(key string) (map[string][]byte, error) {
	entry := models.Model{
		Name: "example-large", Label: "Example Large", Adapter: models.AdapterAnthropic,
		Endpoint: "https://api.provider-one.example", ProviderModel: "example-large-1", Key: "provider.test",
		ContextWindow: 200000, OutputLimit: 1000, Class: models.ClassHosted,
	}
	req := seam.Request{Messages: []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Hello."}}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sse := func(events ...string) string { return strings.Join(events, "") }
	event := func(data string) string { return "data: " + data + "\n\n" }
	cases := []struct {
		name   string
		status int
		body   string
		err    error
	}{
		{"a refused key", 401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key: ` + key + `"}}`, nil},
		{"a refused request", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"the header x-api-key was ` + key + `"}}`, nil},
		{"a busy provider", 529, `{"type":"error","error":{"type":"overloaded_error","message":"busy, ` + key + `"}}`, nil},
		{"an answer that is not JSON", 502, "bad gateway for " + key, nil},
		{"an endpoint that cannot be reached", 0, "", errors.New("dial failed with header " + key)},
		{"an error inside the stream", 200, sse(
			event(`{"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":0}}}`),
			event(`{"type":"error","error":{"type":"api_error","message":"failed while holding `+key+`"}}`)), nil},
		{"a stream that is out of order", 200, sse(event(`{"type":"content_block_stop","index":0}`)), nil},
		{"a reply that works", 200, sse(
			event(`{"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":0}}}`),
			event(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			event(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello."}}`),
			event(`{"type":"content_block_stop","index":0}`),
			event(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`),
			event(`{"type":"message_stop"}`)), nil},
	}

	out := map[string][]byte{}
	say := func(where string, values ...any) {
		for _, v := range values {
			out[where] = append(out[where], fmt.Sprintf("%v %+v %#v\n", v, v, v)...)
		}
	}
	reached := 0
	for _, c := range cases {
		opts := Options{Version: "2023-06-01", MaxRetries: 1, CacheControl: true,
			Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("X-Api-Key") == key {
					reached++
				}
				if c.err != nil {
					return nil, c.err
				}
				return &http.Response{
					StatusCode: c.status, Header: http.Header{"Request-Id": {"req_test_0001"}},
					Body: io.NopCloser(strings.NewReader(c.body)), Request: r,
				}, nil
			})}
		p, err := New(entry, key, opts)
		if err != nil {
			return out, err
		}
		say("the provider, printed", p, *p)
		err = p.Stream(ctx, req, func(e seam.Event) error {
			say(c.name+": an event", e, e.Error)
			return nil
		})
		say(c.name+": the error from Stream", err)
		n, err := p.CountTokens(ctx, req)
		say(c.name+": the token count", n, err)
	}
	if reached == 0 {
		return out, errors.New("the key did not reach the endpoint, so the check proved nothing")
	}

	// An entry the builder refuses, and a key no header can carry.
	bad := entry
	bad.Endpoint = "https://" + key + "@api.provider-one.example"
	_, err := New(bad, key, Options{Version: "2023-06-01"})
	say("the refusal of an entry with the key in its address", err)
	_, err = New(entry, key+"\n", Options{Version: "2023-06-01"})
	say("the refusal of a key with a line break", err)
	return out, nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
