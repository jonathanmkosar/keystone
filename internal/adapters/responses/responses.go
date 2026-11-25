// Package responses is the adapter for the OpenAI Responses API. It is a
// seam.Provider for one registry entry: it turns a seam.Request into one
// POST to the entry's endpoint, reads the reply as server-sent events, and
// turns those into the seam's events.
//
// It uses the standard library only. The key is handed to New by the caller
// (adapters.Open reads it from the tenant's vault). It goes in one request
// header and nowhere else: not in an error, not in a log, and not in what
// the provider prints as. The package reads no environment variable.
//
// docs/architecture/adapter-responses.md has the wire format, and says
// which parts of it are sure and which are inferred.
package responses

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/settings"
)

// Options are the values someone might change. The caller reads them from
// the settings registry and passes them in.
type Options struct {
	// MaxRetries is how many times a call that failed before any of its
	// reply arrived is sent again. Zero sends it once.
	MaxRetries int
	// RetryBaseDelay is the wait before the first retry. It doubles each
	// time, up to RetryMaxDelay.
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
	// HeaderTimeout is how long to wait for the reply to begin.
	HeaderTimeout time.Duration
	// IdleTimeout is how long a stream may send nothing before it is given
	// up on.
	IdleTimeout time.Duration
	// EffortMediumFrom and EffortHighFrom are the thinking budgets, in
	// tokens, at which the effort asked for becomes medium and high. A
	// smaller budget asks for low.
	EffortMediumFrom int
	EffortHighFrom   int
}

// OptionsFrom reads the options from the defaults in a settings registry.
func OptionsFrom(reg []settings.Setting) (Options, error) {
	var o Options
	var err error
	number := func(key string) int {
		st, ok := settings.Lookup(reg, key)
		if !ok {
			err = errors.Join(err, errors.New(key+" is missing from the settings registry"))
			return 0
		}
		n, e := strconv.Atoi(st.Default)
		if e != nil {
			err = errors.Join(err, fmt.Errorf("%s: %w", key, e))
		}
		return n
	}
	length := func(key string) time.Duration {
		st, ok := settings.Lookup(reg, key)
		if !ok {
			err = errors.Join(err, errors.New(key+" is missing from the settings registry"))
			return 0
		}
		d, e := time.ParseDuration(st.Default)
		if e != nil {
			err = errors.Join(err, fmt.Errorf("%s: %w", key, e))
		}
		return d
	}
	o.MaxRetries = number("responses.max_retries")
	o.RetryBaseDelay = length("responses.retry_base_delay")
	o.RetryMaxDelay = length("responses.retry_max_delay")
	o.HeaderTimeout = length("responses.header_timeout")
	o.IdleTimeout = length("responses.idle_timeout")
	o.EffortMediumFrom = number("responses.effort_medium_from")
	o.EffortHighFrom = number("responses.effort_high_from")
	if err != nil {
		return Options{}, err
	}
	return o, checkOptions(o)
}

// DefaultOptions reads the options from the settings compiled into the
// binary.
func DefaultOptions() (Options, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Options{}, err
	}
	return OptionsFrom(reg)
}

func checkOptions(o Options) error {
	switch {
	case o.MaxRetries < 0:
		return errors.New("the number of retries cannot be negative")
	case o.RetryBaseDelay < 0 || o.RetryMaxDelay < 0 || o.RetryMaxDelay < o.RetryBaseDelay:
		return errors.New("the retry waits cannot be negative, and the longest cannot be shorter than the first")
	case o.HeaderTimeout <= 0 || o.IdleTimeout <= 0:
		return errors.New("the header timeout and the idle timeout must be more than zero")
	case o.EffortMediumFrom <= 0 || o.EffortHighFrom < o.EffortMediumFrom:
		return errors.New("the effort budgets must be more than zero, and high cannot start below medium")
	}
	return nil
}

// Provider calls one model through the Responses API. It holds nothing
// that changes, so it is safe to use from several goroutines at once.
type Provider struct {
	model  models.Model
	url    string
	key    string
	opts   Options
	client *http.Client
}

// New returns the provider for a registry entry. key is the provider key,
// and may be empty for an endpoint that asks for none. An error from New
// never holds the key.
func New(m models.Model, key string, opts Options) (*Provider, error) {
	if m.Adapter != models.AdapterResponses {
		return nil, fmt.Errorf("the entry names the %s adapter, and this is the %s adapter", m.Adapter, models.AdapterResponses)
	}
	if err := checkOptions(opts); err != nil {
		return nil, err
	}
	if m.ProviderModel == "" {
		return nil, errors.New("the entry has no provider_model")
	}
	u, err := url.Parse(m.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("the entry's endpoint is not an http or https address")
	}
	if strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("the key holds a line break and cannot be sent")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = opts.HeaderTimeout
	return &Provider{
		model:  m,
		url:    strings.TrimRight(m.Endpoint, "/") + "/responses",
		key:    key,
		opts:   opts,
		client: &http.Client{Transport: transport},
	}, nil
}

// String keeps the key out of anything that prints the provider.
func (p *Provider) String() string { return "responses provider for model " + p.model.Name }

// GoString does the same for %#v.
func (p *Provider) GoString() string { return p.String() }

// emitFailed marks an error that came from the caller's emit function, so
// it is handed back as it is and never retried.
type emitFailed struct{ err error }

func (e emitFailed) Error() string { return e.err.Error() }

// Stream sends one request, as seam.Provider describes.
func (p *Provider) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	sent := false
	send := func(e seam.Event) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		sent = true
		if err := emit(e); err != nil {
			return emitFailed{err}
		}
		return nil
	}
	fail := func(e *seam.Error) error {
		e.Message = p.scrub(e.Message)
		if err := send(seam.Event{Type: seam.EventError, Error: e}); err != nil {
			var ef emitFailed
			if errors.As(err, &ef) {
				return ef.err
			}
			return err
		}
		return e
	}

	if err := seam.ValidateRequest(req); err != nil {
		return fail(&seam.Error{Kind: seam.ErrInvalid, Message: "the request is not valid: " + err.Error()})
	}
	body, err := p.body(req)
	if err != nil {
		return fail(&seam.Error{Kind: seam.ErrInvalid, Message: err.Error()})
	}

	for attempt := 0; ; attempt++ {
		retryAfter, err := p.attempt(ctx, body, send)
		if err == nil {
			return nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		var ef emitFailed
		if errors.As(err, &ef) {
			return ef.err
		}
		var se *seam.Error
		if !errors.As(err, &se) {
			se = &seam.Error{Kind: seam.ErrOther, Message: err.Error()}
		}
		// Nothing is sent twice: once part of a reply has been passed on,
		// the failure is the caller's to handle.
		if sent || !se.Retryable || attempt >= p.opts.MaxRetries {
			return fail(se)
		}
		wait := p.opts.RetryBaseDelay << attempt
		if wait > p.opts.RetryMaxDelay || wait < 0 {
			wait = p.opts.RetryMaxDelay
		}
		if retryAfter > 0 {
			// The provider said how long. If that is longer than Keystone
			// is set to wait, the caller gets the error and can fall back.
			if retryAfter > p.opts.RetryMaxDelay {
				return fail(se)
			}
			wait = retryAfter
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// attempt sends the request once and passes the reply on. It returns nil
// when the reply reached its stop event. retryAfter is the wait the
// provider asked for, when it asked.
func (p *Provider) attempt(ctx context.Context, body []byte, send func(seam.Event) error) (retryAfter time.Duration, err error) {
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	hreq, err := http.NewRequestWithContext(actx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return 0, &seam.Error{Kind: seam.ErrInvalid, Message: "the request could not be built for the entry's endpoint"}
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	if p.key != "" {
		hreq.Header.Set("Authorization", "Bearer "+p.key)
	}
	resp, err := p.client.Do(hreq)
	if err != nil {
		return 0, &seam.Error{Kind: seam.ErrUnavailable, Message: "the endpoint could not be reached: " + transportReason(err), Retryable: true}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return retryAfterOf(resp.Header), statusError(resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "text/event-stream") {
		return 0, &seam.Error{Kind: seam.ErrOther, Message: "the endpoint answered without a stream of events, so it does not speak the Responses API as this adapter does"}
	}

	// The watchdog ends a stream that has gone silent.
	var idled atomic.Bool
	watchdog := time.AfterFunc(p.opts.IdleTimeout, func() {
		idled.Store(true)
		cancel()
	})
	defer watchdog.Stop()

	tr := &translator{model: p.model.Name, send: send}
	readErr := readEvents(resp.Body, func(name string, data []byte) error {
		watchdog.Reset(p.opts.IdleTimeout)
		return tr.event(name, data)
	})
	switch {
	case tr.done:
		return 0, nil
	case readErr == nil || errors.Is(readErr, io.EOF):
		return 0, &seam.Error{Kind: seam.ErrUnavailable, Message: "the reply broke off before it was finished", Retryable: true}
	}
	var se *seam.Error
	var ef emitFailed
	switch {
	case errors.As(readErr, &se), errors.As(readErr, &ef):
		return 0, readErr
	case ctx.Err() != nil:
		return 0, ctx.Err()
	case idled.Load():
		return 0, &seam.Error{Kind: seam.ErrUnavailable, Message: "the reply stopped arriving and was given up on after " + p.opts.IdleTimeout.String(), Retryable: true}
	case errors.Is(readErr, errEventTooBig):
		return 0, &seam.Error{Kind: seam.ErrOther, Message: readErr.Error()}
	}
	return 0, &seam.Error{Kind: seam.ErrUnavailable, Message: "the reply broke off before it was finished: " + transportReason(readErr), Retryable: true}
}

// scrub takes the key out of a message, in case a provider or a proxy
// echoed it, and keeps the message short.
func (p *Provider) scrub(msg string) string {
	if p.key != "" {
		msg = strings.ReplaceAll(msg, p.key, "(the key)")
	}
	return shorten(msg)
}

func shorten(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if r := []rune(msg); len(r) > maxMessage {
		msg = string(r[:maxMessage]) + "…"
	}
	return msg
}

// transportReason is the cause of a failed connection without the address,
// which the standard library puts in front of it.
func transportReason(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		err = ue.Err
	}
	return err.Error()
}

// retryAfterOf reads how long the provider asked Keystone to wait.
func retryAfterOf(h http.Header) time.Duration {
	if ms := h.Get("Retry-After-Ms"); ms != "" {
		if n, err := strconv.ParseFloat(ms, 64); err == nil && n > 0 {
			return time.Duration(n * float64(time.Millisecond))
		}
	}
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		if n <= 0 {
			return 0
		}
		return time.Duration(n * float64(time.Second))
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}

const (
	// maxErrorBody is how much of a refused call's body is read.
	maxErrorBody = 64 << 10
	// maxMessage is the most characters of an error message that are kept.
	maxMessage = 300
)
