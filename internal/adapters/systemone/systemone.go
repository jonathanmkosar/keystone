// Package systemone is the adapter for Ollama's decision API (P14-02):
// POST {endpoint}/v1/systemone, which Ollama serves from version 0.35.1 for
// decision models. It is a seam.Decider built from one registry entry, and
// uses the standard library only.
//
// A decision call sends a state and named questions, each a choice among
// options or a yes or no, and gets back a probability for every option. The
// adapter turns the seam's DecisionRequest into Ollama's shape and the
// answer back into a DecisionResult. Before anything is sent it refuses a
// request larger than Options.MaxRequestBytes or with more than
// Options.MaxQuestions questions, the limits Ollama itself keeps. A call is
// sent once and must be answered within Options.Timeout: a decision that is
// late is not worth waiting for, and its caller falls back to rules.
//
// An entry of this adapter takes decision calls and nothing else, so its
// Stream refuses every request. The key, when the entry names one, is the
// one handed to New, and is taken out of every message. An entry may name
// one certificate authority to trust. There is no way to switch
// certificate checks off.
//
// docs/architecture/adapter-systemone.md describes the translation.
package systemone

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Options are the adapter's limits. They come from the settings registry:
// the caller reads them and passes them in.
type Options struct {
	// Timeout is the longest one call may take, from connecting to the
	// whole answer (systemone.timeout).
	Timeout time.Duration
	// MaxRequestBytes is the largest request body sent, in bytes
	// (systemone.max_request_bytes).
	MaxRequestBytes int
	// MaxQuestions is the most questions one request may hold
	// (systemone.max_questions).
	MaxQuestions int
}

// Provider calls one registry entry's decision endpoint. It holds nothing
// that changes, so it is safe to use from several goroutines at once.
type Provider struct {
	model  models.Model
	url    string
	key    string
	client *http.Client
	opts   Options
}

// maxAnswer is the largest answer that is read. An answer holds a few
// numbers for each option, so this is far above any real one. It is a bound
// on what is read, not a limit a person tunes.
const maxAnswer = 4 << 20

// maxErrorBody is how much of a refusal's body is read, and maxReason how
// much of its message is kept.
const (
	maxErrorBody = 64 << 10
	maxReason    = 300
)

// New returns the provider for one entry. key is the provider key, and
// empty when the endpoint asks for none, as a local Ollama does. The error
// never holds the key.
func New(m models.Model, key string, opts Options) (*Provider, error) {
	if m.Adapter != models.AdapterSystemone {
		return nil, fmt.Errorf("the entry names the %s adapter, and this is the %s adapter", m.Adapter, models.AdapterSystemone)
	}
	if err := models.Check(m); err != nil {
		return nil, err
	}
	if opts.Timeout <= 0 {
		return nil, errors.New("the timeout must be more than zero")
	}
	if opts.MaxRequestBytes <= 0 || opts.MaxQuestions <= 0 {
		return nil, errors.New("the largest request and the most questions must each be more than zero")
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
		// Only this authority is trusted for this entry, and the usual ones
		// are not. The certificate is still checked in full.
		conf.RootCAs = pool
	}
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: opts.Timeout}).DialContext,
		TLSClientConfig:     conf,
		TLSHandshakeTimeout: opts.Timeout,
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
	}
	if m.Class != models.ClassLocal {
		// A local model is called directly: what is sent to it must not
		// pass through a proxy.
		transport.Proxy = http.ProxyFromEnvironment
	}
	client := &http.Client{
		Transport: transport,
		// A redirect is not followed: the state goes to the address in the
		// entry and nowhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Provider{model: m, url: decideURL(m.Endpoint), key: key, client: client, opts: opts}, nil
}

// decideURL is where an entry's decision calls go: {endpoint}/v1/systemone.
// An endpoint written with Ollama's /v1 already on it is not given a second.
func decideURL(endpoint string) string {
	base := strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/systemone"
	}
	return base + "/v1/systemone"
}

// certPool reads the entry's ca_cert: PEM text, or the path of a PEM file.
// Nothing from the value is put in the error but a path. It is the Chat
// Completions adapter's rule, kept here so the two adapters stay apart.
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

// Stream refuses every request, as seam.Provider describes a failed call:
// a decision model writes no reply.
func (p *Provider) Stream(ctx context.Context, _ seam.Request, emit func(seam.Event) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e := &seam.Error{Kind: seam.ErrInvalid, Message: "the model " + p.model.Name + " is a decision model: it takes decision calls and writes no reply"}
	if err := emit(seam.Event{Type: seam.EventError, Error: e}); err != nil {
		return err
	}
	return e
}

// Decide answers one decision call, as seam.Decider describes. A request
// that is not valid, holds too many questions or is too large is refused
// before anything is sent.
func (p *Provider) Decide(ctx context.Context, req seam.DecisionRequest) (seam.DecisionResult, error) {
	if err := ctx.Err(); err != nil {
		return seam.DecisionResult{}, err
	}
	if err := seam.ValidateDecisionRequest(req); err != nil {
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrInvalid, Message: "the request is not valid: " + err.Error()}
	}
	if n := len(req.Questions); n > p.opts.MaxQuestions {
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrTooLong, Message: fmt.Sprintf(
			"the request holds %d questions, and a decision call takes at most %d, so it was not sent", n, p.opts.MaxQuestions)}
	}
	body, err := Body(p.model.ProviderModel, req)
	if err != nil {
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrInvalid, Message: "the request could not be written"}
	}
	if len(body) > p.opts.MaxRequestBytes {
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrTooLong, Message: fmt.Sprintf(
			"the request is %d bytes, and a decision call takes at most %d, so it was not sent", len(body), p.opts.MaxRequestBytes)}
	}
	res, failure, err := p.send(ctx, req, body)
	if err != nil {
		return seam.DecisionResult{}, err
	}
	if failure != nil {
		failure.Message = p.scrub(failure.Message)
		return seam.DecisionResult{}, failure
	}
	res.Model = req.Model
	return res, nil
}

// send makes the call once. err is the caller's own, the context's; failure
// is the call's.
func (p *Provider) send(ctx context.Context, req seam.DecisionRequest, body []byte) (seam.DecisionResult, *seam.Error, error) {
	actx, cancel := context.WithTimeout(ctx, p.opts.Timeout)
	defer cancel()
	late := func() *seam.Error {
		return &seam.Error{Kind: seam.ErrUnavailable, Retryable: true, Message: fmt.Sprintf("the decision endpoint did not answer within %s", p.opts.Timeout)}
	}
	hreq, err := http.NewRequestWithContext(actx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrInvalid, Message: "the entry's endpoint cannot be called"}, nil
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	if p.key != "" {
		hreq.Header.Set("Authorization", "Bearer "+p.key)
	}
	resp, err := p.client.Do(hreq)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return seam.DecisionResult{}, nil, cerr
		}
		if actx.Err() != nil {
			return seam.DecisionResult{}, late(), nil
		}
		return seam.DecisionResult{}, connectFailure(err), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if cerr := ctx.Err(); cerr != nil {
			return seam.DecisionResult{}, nil, cerr
		}
		return seam.DecisionResult{}, statusFailure(resp.StatusCode, raw), nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return seam.DecisionResult{}, nil, cerr
		}
		if actx.Err() != nil {
			return seam.DecisionResult{}, late(), nil
		}
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrUnavailable, Retryable: true, Message: "the answer broke off before it was complete"}, nil
	}
	if len(raw) > maxAnswer {
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrOther, Message: "the answer is larger than a decision answer is read"}, nil
	}
	res, err := Read(req, raw)
	if err != nil {
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrOther, Message: "the answer is not a decision answer: " + err.Error()}, nil
	}
	return res, nil, nil
}

// scrub takes the key out of text that came from the endpoint or from an
// error, in case it was repeated there.
func (p *Provider) scrub(s string) string {
	if p.key == "" {
		return s
	}
	return strings.ReplaceAll(s, p.key, "(the key)")
}

// connectFailure sorts an error from sending the request. The address is
// left out of the message.
func connectFailure(err error) *seam.Error {
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verify *tls.CertificateVerificationError
	if errors.As(err, &unknown) || errors.As(err, &host) || errors.As(err, &invalid) || errors.As(err, &verify) {
		return &seam.Error{Kind: seam.ErrUnavailable,
			Message: "the endpoint's certificate is not trusted. If the endpoint has its own certificate authority, name it in the entry's ca_cert"}
	}
	var op *net.OpError
	why := "the connection failed"
	if errors.As(err, &op) && op.Op != "" {
		why = "the connection failed at " + op.Op
	}
	return &seam.Error{Kind: seam.ErrUnavailable, Retryable: true, Message: "the decision endpoint could not be reached: " + why}
}

// statusFailure turns a refusal into the seam's error of its kind. Ollama's
// body is {"error": "..."}; its message is kept, cut short.
func statusFailure(status int, raw []byte) *seam.Error {
	var body struct {
		Error string `json:"error"`
	}
	reason := ""
	if json.Unmarshal(raw, &body) == nil {
		reason = strings.Join(strings.Fields(body.Error), " ")
		if r := []rune(reason); len(r) > maxReason {
			reason = string(r[:maxReason]) + "..."
		}
	}
	say := func(kind seam.ErrorKind, retry bool, what string) *seam.Error {
		msg := fmt.Sprintf("%s (status %d)", what, status)
		if reason != "" {
			msg += ": " + reason
		}
		return &seam.Error{Kind: kind, Retryable: retry, Message: msg}
	}
	switch {
	case status == http.StatusBadRequest && strings.Contains(strings.ToLower(reason), "context"):
		return say(seam.ErrTooLong, false, "the decision endpoint says the request does not fit the model")
	case status == http.StatusBadRequest:
		return say(seam.ErrInvalid, false, "the decision endpoint refused the request")
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return say(seam.ErrAuth, false, "the decision endpoint refused the key")
	case status == http.StatusNotFound:
		return say(seam.ErrInvalid, false, "the decision endpoint does not have the model: pull it there first")
	case status == http.StatusRequestEntityTooLarge:
		return say(seam.ErrTooLong, false, "the decision endpoint says the request is too large")
	case status == http.StatusTooManyRequests:
		return say(seam.ErrRateLimited, true, "the decision endpoint asked Keystone to slow down")
	case status == http.StatusServiceUnavailable:
		return say(seam.ErrOverloaded, true, "the decision endpoint is too busy")
	case status >= 500:
		return say(seam.ErrUnavailable, true, "the decision endpoint failed")
	}
	return say(seam.ErrOther, false, "the decision endpoint refused the call")
}
