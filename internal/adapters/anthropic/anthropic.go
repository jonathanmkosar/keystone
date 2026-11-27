// Package anthropic is the adapter for the Anthropic Messages API. It is a
// seam.Provider that turns a seam request into one streamed HTTP call and
// turns the reply's server-sent events back into seam events.
//
// It uses the standard library only. The base address comes from the
// registry entry, so the same adapter serves Anthropic itself and any
// gateway that speaks the same API, such as Ollama or LiteLLM.
//
// The key arrives as an argument, from the tenant's vault. The package
// reads no setting and no environment variable, writes no log, and takes
// the key out of every error it returns.
//
// docs/architecture/adapter-anthropic.md describes it.
package anthropic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Options are the values someone might change. The caller reads them from
// the settings registry and passes them in.
type Options struct {
	// Version is the value of the anthropic-version header.
	Version string
	// MaxRetries is how many times a call that failed before it sent
	// anything on is sent again. Zero means never.
	MaxRetries int
	// RetryBaseDelay is the wait before the first retry. Each later wait
	// is twice the one before, up to RetryMaxDelay.
	RetryBaseDelay time.Duration
	// RetryMaxDelay is the longest wait. A provider that asks for a longer
	// one is not called again.
	RetryMaxDelay time.Duration
	// HeaderTimeout is how long the endpoint may take to begin its answer.
	// It is not applied to a Transport the caller supplies.
	HeaderTimeout time.Duration
	// IdleTimeout is how long a stream may send nothing before the call
	// is given up.
	IdleTimeout time.Duration
	// CacheControl says whether the adapter marks a request for the
	// provider's cache: where the request's cache points say, or the end of
	// the system blocks and of the last message when it has none.
	CacheControl bool
	// BudgetThinkingModels lists the provider model names that take
	// thinking as a token budget. A name ending in * matches by its start.
	// Every other model is asked for adaptive thinking.
	BudgetThinkingModels []string
	// EffortModels lists the provider model names that take a request's
	// effort as output_config.effort. A name ending in * matches by its
	// start.
	EffortModels []string
	// EffortBudgetLow, EffortBudgetMedium and EffortBudgetHigh are the
	// thinking budgets a request's effort stands for, for a model with
	// thinking. xhigh and max take the high budget.
	EffortBudgetLow, EffortBudgetMedium, EffortBudgetHigh int
	// Transport carries the calls. Nil means Go's standard transport.
	Transport http.RoundTripper
}

func (o Options) check() error {
	switch {
	case strings.TrimSpace(o.Version) == "" || strings.ContainsAny(o.Version, "\r\n"):
		return errors.New("the API version is missing or is not one line")
	case o.MaxRetries < 0:
		return errors.New("the number of retries cannot be negative")
	case o.RetryBaseDelay < 0 || o.RetryMaxDelay < 0 || o.HeaderTimeout < 0 || o.IdleTimeout < 0:
		return errors.New("a wait cannot be negative")
	case o.MaxRetries > 0 && o.RetryMaxDelay < o.RetryBaseDelay:
		return errors.New("the longest retry wait is shorter than the first")
	case o.EffortBudgetLow < 0 || o.EffortBudgetMedium < 0 || o.EffortBudgetHigh < 0:
		return errors.New("an effort's thinking budget cannot be negative")
	}
	return nil
}

// Provider calls one model at one endpoint. It holds nothing that changes,
// so it is safe to use from several goroutines at once.
type Provider struct {
	model models.Model
	// key returns the provider key. It is a function so that printing a
	// Provider, with any verb, cannot print the key.
	key    func() string
	opts   Options
	client *http.Client

	messagesURL string
	countURL    string

	// sleep and jitter are replaced by the tests.
	sleep  func(ctx context.Context, d time.Duration) error
	jitter func(d time.Duration) time.Duration
}

// Builder returns the function that makes a provider for a registry entry.
// Its shape is adapters.Builder. Every provider it makes shares one HTTP
// client, so connections are kept between calls.
func Builder(opts Options) (func(models.Model, string) (seam.Provider, error), error) {
	if err := opts.check(); err != nil {
		return nil, fmt.Errorf("anthropic adapter: %w", err)
	}
	client := newClient(opts)
	return func(m models.Model, key string) (seam.Provider, error) {
		p, err := newProvider(m, key, opts, client)
		if err != nil {
			return nil, err
		}
		return p, nil
	}, nil
}

// New returns the provider for one registry entry. key is the value of the
// secret the entry names, and empty when the endpoint asks for none.
func New(m models.Model, key string, opts Options) (*Provider, error) {
	if err := opts.check(); err != nil {
		return nil, fmt.Errorf("anthropic adapter: %w", err)
	}
	return newProvider(m, key, opts, newClient(opts))
}

func newClient(opts Options) *http.Client {
	transport := opts.Transport
	if transport == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.ResponseHeaderTimeout = opts.HeaderTimeout
		transport = t
	}
	return &http.Client{
		Transport: transport,
		// A redirect is not followed. Go would send the key header on to
		// wherever the redirect points.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func newProvider(m models.Model, key string, opts Options, client *http.Client) (*Provider, error) {
	if m.Adapter != models.AdapterAnthropic {
		return nil, fmt.Errorf("the entry names the %s adapter, and this is the anthropic adapter", m.Adapter)
	}
	if m.ProviderModel == "" {
		return nil, errors.New("the entry has no provider_model to send")
	}
	if m.OutputLimit <= 0 {
		return nil, errors.New("the entry has no output limit")
	}
	u, err := url.Parse(m.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("the entry's endpoint is not an http or https address")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("the entry's endpoint holds more than an address and a path")
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return nil, errors.New("the key holds a character a header cannot carry")
		}
	}
	// The entry gives the base address. An address that already ends in
	// /v1, as some gateways are written, is not given a second one.
	base := strings.TrimRight(u.String(), "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return &Provider{
		model:       m,
		key:         func() string { return key },
		opts:        opts,
		client:      client,
		messagesURL: base + "/messages",
		countURL:    base + "/messages/count_tokens",
		sleep:       sleep,
		jitter:      jitter,
	}, nil
}

// Stream sends one request and passes the reply on, as seam.Provider
// describes. A call that fails before any event has been passed on is sent
// again, when the failure is one that may pass.
func (p *Provider) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	body, err := p.body(req, true)
	if err != nil {
		return p.fail(emit, &seam.Error{Kind: seam.ErrInvalid, Message: p.clean("anthropic adapter: " + err.Error())})
	}
	for attempt := 0; ; attempt++ {
		res, err := p.attempt(ctx, body, emit)
		if err != nil {
			return err
		}
		if res.fault == nil {
			return nil
		}
		wait, again := p.retry(attempt, res)
		if !again {
			return p.fail(emit, res.fault)
		}
		if err := p.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// CountTokens asks the endpoint how many input tokens a request holds:
// its system blocks, messages and tools. An endpoint that has no such call
// answers with an error, and the caller estimates.
func (p *Provider) CountTokens(ctx context.Context, req seam.Request) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	body, err := p.body(req, false)
	if err != nil {
		return 0, &seam.Error{Kind: seam.ErrInvalid, Message: p.clean("anthropic adapter: " + err.Error())}
	}
	for attempt := 0; ; attempt++ {
		n, res, err := p.count(ctx, body)
		if err != nil {
			return 0, err
		}
		if res.fault == nil {
			return n, nil
		}
		wait, again := p.retry(attempt, res)
		if !again {
			return 0, res.fault
		}
		if err := p.sleep(ctx, wait); err != nil {
			return 0, err
		}
	}
}

// result is how one attempt ended, when it did not end by the caller's own
// doing. A nil fault means the reply was whole.
type result struct {
	fault *seam.Error
	// started says an event was passed on, so the call cannot be sent
	// again without the receiver seeing the reply twice.
	started bool
	// after is the wait the provider asked for, or zero.
	after time.Duration
}

// retry says whether a failed attempt is sent again, and after how long.
func (p *Provider) retry(attempt int, res result) (time.Duration, bool) {
	if res.started || !res.fault.Retryable || attempt >= p.opts.MaxRetries {
		return 0, false
	}
	if res.after > p.opts.RetryMaxDelay {
		// The provider asked for longer than Keystone waits. Routing may
		// have another model to try.
		return 0, false
	}
	wait := p.opts.RetryBaseDelay
	for i := 0; i < attempt && wait < p.opts.RetryMaxDelay; i++ {
		wait *= 2
	}
	wait = p.jitter(min(wait, p.opts.RetryMaxDelay))
	return max(wait, res.after), true
}

// fail ends a stream with an error event and returns the error.
func (p *Provider) fail(emit func(seam.Event) error, e *seam.Error) error {
	if err := emit(seam.Event{Type: seam.EventError, Error: e}); err != nil {
		return err
	}
	return e
}

// post sends one JSON body. A call that got no answer comes back as a
// fault, unless the caller's own context ended it.
func (p *Provider) post(ctx context.Context, address string, body []byte, accept string) (*http.Response, *seam.Error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return nil, &seam.Error{Kind: seam.ErrInvalid, Message: "anthropic adapter: the request could not be made"}
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", accept)
	r.Header.Set("Anthropic-Version", p.opts.Version)
	if key := p.key(); key != "" {
		r.Header.Set("X-Api-Key", key)
	}
	resp, err := p.client.Do(r)
	if err != nil {
		// A url.Error repeats the address. The cause alone says enough.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, &seam.Error{
			Kind:      seam.ErrUnavailable,
			Message:   p.clean("the endpoint could not be reached: " + err.Error()),
			Retryable: true,
		}
	}
	return resp, nil
}

// clean makes text safe to return: the key is taken out, and it is cut to
// a length that fits a log line.
func (p *Provider) clean(s string) string {
	if key := p.key(); key != "" {
		s = strings.ReplaceAll(s, key, "(the key)")
	}
	s = strings.Join(strings.Fields(s), " ")
	const most = 400
	if r := []rune(s); len(r) > most {
		s = string(r[:most]) + "…"
	}
	return s
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// jitter returns a wait between half of d and d, so calls that failed
// together do not come back together.
func jitter(d time.Duration) time.Duration {
	if d <= 1 {
		return d
	}
	return d/2 + rand.N(d/2+1)
}
