// Package chatcompletions is the adapter for endpoints that speak the
// OpenAI-style Chat Completions format: OpenRouter, LiteLLM, Ollama, the
// Mac mini gateway and anything else that copies it. It is a seam.Provider
// built from one registry entry, and uses the standard library only.
//
// It sends one streamed request, reads the reply as server-sent events and
// turns it into seam events. A failed call is mapped to one of the seam's
// error kinds, and is sent again, after a wait, only while nothing has
// reached the caller.
//
// The key is the one handed to New. This package reads no environment
// variable for it, and takes it out of every message it writes. An entry
// may name one certificate authority to trust, for an endpoint with its own
// certificate. There is no way to switch certificate checks off.
//
// docs/architecture/adapter-chat-completions.md describes the translation.
package chatcompletions

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Options are the waits and the retry limits. They come from the settings
// registry: the caller reads them and passes them in.
type Options struct {
	// ConnectTimeout is the longest wait to connect, and again to finish
	// the TLS handshake.
	ConnectTimeout time.Duration
	// ResponseTimeout is the longest wait for the endpoint to start its
	// answer once the request is sent. A local model may be loading.
	ResponseTimeout time.Duration
	// IdleTimeout is the longest wait between two pieces of a reply.
	IdleTimeout time.Duration
	// MaxAttempts is how many times one call is sent at most. One means no
	// retry.
	MaxAttempts int
	// RetryBase is the wait before the second attempt. It doubles for each
	// attempt after that, up to RetryMax.
	RetryBase time.Duration
	RetryMax  time.Duration
	// SendReasoningEffort says whether a request's effort is sent as
	// reasoning_effort, for an entry with thinking. Off, nothing about
	// effort is sent.
	SendReasoningEffort bool
}

// Provider calls one registry entry's endpoint. It holds nothing that
// changes, so it is safe to use from several goroutines at once.
type Provider struct {
	model  models.Model
	url    string
	key    string
	client *http.Client
	opts   Options
}

// idleConnections is how long a finished connection is kept for the next
// call to the same endpoint.
const idleConnections = 30 * time.Second

// New returns the provider for one entry. key is the provider key, and
// empty when the endpoint asks for none. The error never holds the key.
func New(m models.Model, key string, opts Options) (*Provider, error) {
	if m.Adapter != models.AdapterChatCompletions {
		return nil, fmt.Errorf("the entry names the %s adapter, and this is the %s adapter", m.Adapter, models.AdapterChatCompletions)
	}
	if err := models.Check(m); err != nil {
		return nil, err
	}
	if opts.ConnectTimeout <= 0 || opts.ResponseTimeout <= 0 || opts.IdleTimeout <= 0 {
		return nil, errors.New("the connect, response and idle timeouts must each be more than zero")
	}
	if opts.MaxAttempts < 1 {
		return nil, errors.New("the number of attempts must be one or more")
	}
	if opts.RetryBase <= 0 || opts.RetryMax < opts.RetryBase {
		return nil, errors.New("the first retry wait must be more than zero, and the longest wait no shorter than it")
	}
	if strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("the key holds a line break, and cannot be sent")
	}

	conf := &tls.Config{MinVersion: tls.VersionTLS12}
	if m.CACert != "" {
		pool, err := certPool(m.CACert)
		if err != nil {
			return nil, err
		}
		// Only this authority is trusted for this entry, and the usual
		// ones are not. The certificate is still checked in full.
		conf.RootCAs = pool
	}
	dialer := &net.Dialer{Timeout: opts.ConnectTimeout}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSClientConfig:       conf,
		TLSHandshakeTimeout:   opts.ConnectTimeout,
		ResponseHeaderTimeout: opts.ResponseTimeout,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       idleConnections,
	}
	if m.Class != models.ClassLocal {
		// A local model is on this machine or the home network, and is
		// called directly: what is sent to it must not pass through a proxy.
		transport.Proxy = http.ProxyFromEnvironment
	}
	client := &http.Client{
		Transport: transport,
		// A redirect is not followed: the request, and what it carries,
		// goes to the address in the entry and nowhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Provider{
		model:  m,
		url:    strings.TrimRight(m.Endpoint, "/") + "/chat/completions",
		key:    key,
		client: client,
		opts:   opts,
	}, nil
}

// certPool reads the entry's ca_cert: PEM text, or the path of a PEM file.
// Nothing from the value is put in the error but a path.
func certPool(value string) (*x509.CertPool, error) {
	raw := []byte(value)
	if !strings.Contains(value, "-----BEGIN") {
		read, err := os.ReadFile(value)
		if err != nil {
			return nil, fmt.Errorf("ca_cert: the certificate file could not be read: %w", err)
		}
		raw = read
	}
	pool := x509.NewCertPool()
	found := 0
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("ca_cert holds a PEM block that is not a certificate (%s): give the authority's certificate and nothing else", block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("ca_cert holds a certificate that cannot be read")
		}
		pool.AddCert(cert)
		found++
	}
	if found == 0 {
		return nil, errors.New("ca_cert holds no certificate: it is PEM text, or the path of a PEM file")
	}
	return pool, nil
}

// Stream sends one request and streams the reply, as seam.Provider
// describes.
func (p *Provider) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	if err := seam.ValidateRequest(req); err != nil {
		return p.fail(ctx, emit, &seam.Error{Kind: seam.ErrInvalid, Message: "the request is not valid: " + err.Error()})
	}
	body, err := buildBody(p.model, req, p.opts.SendReasoningEffort)
	if err != nil {
		return p.fail(ctx, emit, &seam.Error{Kind: seam.ErrInvalid, Message: err.Error()})
	}
	for attempt := 1; ; attempt++ {
		out := p.attempt(ctx, body, emit)
		if out.err != nil {
			return out.err
		}
		if out.failure == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if out.started || !out.failure.Retryable || attempt >= p.opts.MaxAttempts {
			return p.fail(ctx, emit, out.failure)
		}
		wait := time.NewTimer(p.backoff(attempt, out.retryAfter))
		select {
		case <-ctx.Done():
			wait.Stop()
			return ctx.Err()
		case <-wait.C:
		}
	}
}

// fail ends a call with an error event and returns the error. The key is
// taken out of the message first.
func (p *Provider) fail(ctx context.Context, emit func(seam.Event) error, e *seam.Error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.Message = p.scrub(e.Message)
	if e.Message == "" {
		e.Message = "the call failed"
	}
	if err := emit(seam.Event{Type: seam.EventError, Error: e}); err != nil {
		return err
	}
	return e
}

// scrub takes the key out of text that came from the endpoint or from an
// error, in case it was repeated there.
func (p *Provider) scrub(s string) string {
	if p.key == "" {
		return s
	}
	return strings.ReplaceAll(s, p.key, "(the key)")
}

// backoff is the wait before the next attempt: the base doubled for each
// attempt so far, or what the endpoint asked for if that is longer, never
// more than the longest wait. The doubled wait is shortened by up to half
// at random, so calls that failed together do not return together.
func (p *Provider) backoff(attempt int, asked time.Duration) time.Duration {
	d := p.opts.RetryBase
	for i := 1; i < attempt && d < p.opts.RetryMax; i++ {
		d *= 2
	}
	d = min(d, p.opts.RetryMax)
	d = d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
	if asked > d {
		d = min(asked, p.opts.RetryMax)
	}
	return d
}

// outcome is how one attempt ended. err is the caller's own: the context's
// error or what emit returned. failure is the call's. started says events
// have reached the caller, so the call cannot be sent again.
type outcome struct {
	err        error
	failure    *seam.Error
	started    bool
	retryAfter time.Duration
}

// maxErrorBody is how much of a refusal's body is read.
const maxErrorBody = 64 << 10

func (p *Provider) attempt(parent context.Context, body []byte, emit func(seam.Event) error) outcome {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return outcome{failure: &seam.Error{Kind: seam.ErrInvalid, Message: "the entry's endpoint cannot be called"}}
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream, application/json")
	if p.key != "" {
		hreq.Header.Set("Authorization", "Bearer "+p.key)
	}
	resp, err := p.client.Do(hreq)
	if err != nil {
		if perr := parent.Err(); perr != nil {
			return outcome{err: perr}
		}
		return outcome{failure: connectFailure(err, p.opts)}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if perr := parent.Err(); perr != nil {
			return outcome{err: perr}
		}
		return outcome{failure: statusFailure(resp.StatusCode, raw), retryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}

	// The idle timer ends a reply that stops arriving without closing.
	var idled atomic.Bool
	idle := time.AfterFunc(p.opts.IdleTimeout, func() {
		idled.Store(true)
		cancel()
	})
	defer idle.Stop()

	r := &reply{ctx: parent, emit: emit, model: p.model.Name, sentBytes: len(body)}
	whole := strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "application/json")
	var readErr error
	if whole {
		// The endpoint did not stream. The whole reply is one piece.
		var raw []byte
		raw, readErr = io.ReadAll(io.LimitReader(resp.Body, maxEvent))
		if readErr == nil {
			r.done = true
			r.take(raw)
		}
	} else {
		readErr = readEvents(resp.Body, func(data []byte) bool {
			idle.Reset(p.opts.IdleTimeout)
			if string(bytes.TrimSpace(data)) == "[DONE]" {
				r.done = true
				return false
			}
			r.take(data)
			return r.err == nil && r.failure == nil
		})
	}
	out := outcome{started: r.started}
	switch {
	case r.err != nil:
		out.err = r.err
	case parent.Err() != nil:
		out.err = parent.Err()
	case r.failure != nil:
		out.failure = r.failure
	case readErr != nil && idled.Load():
		out.failure = &seam.Error{Kind: seam.ErrUnavailable, Retryable: true,
			Message: fmt.Sprintf("the endpoint sent nothing for %s, and the call was ended", p.opts.IdleTimeout)}
	case readErr != nil:
		out.failure = &seam.Error{Kind: seam.ErrUnavailable, Retryable: true, Message: "the reply broke off before it was complete"}
	default:
		r.finish()
		out.err, out.failure, out.started = r.err, r.failure, r.started
	}
	return out
}

// connectFailure sorts an error from sending the request. The address is
// left out of the message.
func connectFailure(err error, opts Options) *seam.Error {
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verify *tls.CertificateVerificationError
	if errors.As(err, &unknown) || errors.As(err, &host) || errors.As(err, &invalid) || errors.As(err, &verify) {
		return &seam.Error{Kind: seam.ErrUnavailable,
			Message: "the endpoint's certificate is not trusted. If the endpoint has its own certificate authority, name it in the entry's ca_cert"}
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return &seam.Error{Kind: seam.ErrUnavailable, Retryable: true,
			Message: fmt.Sprintf("the endpoint did not answer in time (connect %s, first answer %s)", opts.ConnectTimeout, opts.ResponseTimeout)}
	}
	var op *net.OpError
	why := "the connection failed"
	if errors.As(err, &op) && op.Op != "" {
		why = "the connection failed at " + op.Op
	}
	return &seam.Error{Kind: seam.ErrUnavailable, Retryable: true, Message: "the endpoint could not be reached: " + why}
}

// retryAfter reads a Retry-After header: a number of seconds or a date.
func retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}
