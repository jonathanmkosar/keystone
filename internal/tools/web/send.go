package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// The sender the adapters of P7-07 share: Brave, Exa, Serper and SearXNG.
// It sends one request, reads the answer up to a bound, and turns a failure
// to connect into a *Failure. Each adapter then reads the status its own way,
// since each provider says "spent", "slow down" and "bad key" differently.
//
// A key travels only in the provider's own header. No redirect is followed:
// Go carries a header such as X-API-KEY to the host a redirect names, and a
// provider's key must reach that provider and nothing else. Nothing from an
// answer's body goes into an error's text: a service that repeats the key
// cannot put it in front of the model.

// errorBodyBytes is the most of an error answer read, for the provider's
// own error code. It is part of the shape of an error answer, not a choice.
const errorBodyBytes = 16 << 10

// sender sends one provider's requests.
type sender struct {
	// title is the service's name in an error: "Brave".
	title string
	// timeout bounds one request: agents.web_search_timeout.
	timeout time.Duration
	// maxBytes is the most of a good answer read:
	// agents.web_search_max_bytes. A larger one is not used.
	maxBytes int64
	// client sends the request. Nil means one of the sender's own.
	client *http.Client
	// direct says the environment's proxy is not used: for a SearXNG on
	// this machine or the home network, as for a local model.
	direct bool
}

// reply is a provider's answer: its status, headers and body. A good
// answer's body is read up to maxBytes, an error answer's up to
// errorBodyBytes.
type reply struct {
	status int
	header http.Header
	body   []byte
}

// noRedirects keeps a provider's key from following a redirect to another
// host: the redirect's own answer comes back, and reads as unavailable.
func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (s sender) httpClient() *http.Client {
	if s.client != nil {
		return s.client
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if s.direct {
		transport.Proxy = nil
	}
	return &http.Client{Transport: transport, CheckRedirect: noRedirects}
}

// do sends req and reads the answer. A failure to connect or a timeout is
// a *Failure saying the provider is unavailable; a cancelled turn is the
// context's error.
func (s sender) do(ctx context.Context, req *http.Request) (reply, error) {
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	req = req.WithContext(ctx)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.httpClient().Do(req)
	if err != nil {
		// The error's text names the address, and is never shown.
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return reply{}, &Failure{Why: WhyUnavailable, Text: fmt.Sprintf("%s did not answer within %s", s.title, s.timeout)}
		}
		if ctx.Err() != nil {
			return reply{}, ctx.Err()
		}
		return reply{}, &Failure{Why: WhyUnavailable, Text: "Keystone could not reach " + s.title}
	}
	defer resp.Body.Close()
	limit := int64(errorBodyBytes)
	if resp.StatusCode == http.StatusOK {
		limit = s.maxBytes + 1
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return reply{}, &Failure{Why: WhyUnavailable, Text: fmt.Sprintf("%s did not answer within %s", s.title, s.timeout)}
		}
		return reply{}, &Failure{Why: WhyUnavailable, Text: "Keystone could not read " + s.title + "'s answer"}
	}
	if resp.StatusCode == http.StatusOK && int64(len(raw)) > s.maxBytes {
		return reply{}, &Failure{Why: WhyUnavailable, Text: fmt.Sprintf("%s's answer was larger than the %d bytes Keystone reads (agents.web_search_max_bytes), so it was not used", s.title, s.maxBytes)}
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: raw}, nil
}

// unavailable is the failure for a status the adapter does not read
// otherwise.
func (s sender) unavailable(status int) *Failure {
	return &Failure{Why: WhyUnavailable, Text: fmt.Sprintf("%s is unavailable (it answered with status %d)", s.title, status)}
}

// malformed is the failure for a request the provider refused as wrong.
func (s sender) malformed(status int) *Failure {
	return &Failure{Why: WhyMalformed, Text: fmt.Sprintf("%s called the request malformed (it answered with status %d)", s.title, status)}
}

// refused is the failure for a key the provider refused. failWords says
// where the key is kept.
func (s sender) refused() *Failure {
	return &Failure{Why: WhyRefused, Text: s.title + " refused the key"}
}

// unreadable is the failure for a good status whose body is not the
// provider's answer.
func (s sender) unreadable() *Failure {
	return &Failure{Why: WhyUnavailable, Text: "Keystone could not read " + s.title + "'s answer"}
}

// endpointFailure is the failure for an endpoint that is not an address
// Keystone can call. The address is the install's or the entry's, and is
// not repeated.
func (s sender) endpointFailure() *Failure {
	return &Failure{Why: WhyUnavailable, Text: "the address of the " + s.title + " provider is not an address Keystone can call"}
}

// errorField reads one string from an error answer's JSON by a path of
// keys, such as "error", "code". Empty when it is not there. What it reads
// is compared with known words and never shown.
func errorField(body []byte, path ...string) string {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = m[k]
	}
	s, _ := v.(string)
	return s
}

// markup matches a tag in a result's title or extract, as Brave's
// descriptions carry <strong> around the words searched for.
var markup = regexp.MustCompile(`<[^<>]{0,200}>`)

// plainText takes the markup out of a provider's text, decodes its entities
// and collapses its spaces.
func plainText(s string) string {
	s = html.UnescapeString(markup.ReplaceAllString(s, ""))
	return strings.Join(strings.Fields(s), " ")
}

// keep adds one result when it can be shown: a title, an extract and an
// address that is citable. It reports whether count results are kept.
func keep(out *[]Result, title, address, content string, count int) bool {
	title, content = plainText(title), plainText(content)
	if title != "" && content != "" && citable(address) {
		*out = append(*out, Result{Title: title, URL: address, Content: content})
	}
	return len(*out) >= count
}

// searchAddress is an entry's endpoint with the provider's search path, or
// the default when the entry has none.
func searchAddress(endpoint, fallback, path string) string {
	if endpoint == "" {
		endpoint = fallback
	}
	return strings.TrimRight(endpoint, "/") + path
}
