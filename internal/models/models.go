// Package models is the model registry: one entry for each model a tenant
// can use, kept in that tenant's own database. An entry says where the
// model is, which adapter speaks to it, how much it can read and write,
// what it can do, what it costs, and whether it is local, hosted or
// trusted.
//
// The class is what fixed rule 3 rests on: content from a sensitive space
// reaches only models the registry marks local or trusted. So this package
// is a guard for that rule. It imports neither the settings registry nor
// the tenant settings, itself or through another package, and takes the one
// limit it has as an argument. The fixed rules check in verify fails if
// that changes.
//
// Turning an entry into a provider is not here. See internal/adapters.
// docs/architecture/models.md describes both.
package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Class says where a model runs, and so what it may be sent.
type Class string

const (
	// ClassLocal is a model on this machine or on the home network.
	ClassLocal Class = "local"
	// ClassHosted is a model run by someone else. It never receives
	// content from a sensitive space.
	ClassHosted Class = "hosted"
	// ClassTrusted is a model that is not local and that the admin has
	// chosen to trust with content from a sensitive space.
	ClassTrusted Class = "trusted"
)

// Capability is one thing a model can do. A call that needs it is routed
// only to a model that has it.
type Capability string

const (
	CapTools     Capability = "tools"
	CapVision    Capability = "vision"
	CapFiles     Capability = "files"
	CapThinking  Capability = "thinking"
	CapEmbedding Capability = "embedding"
)

// Capabilities returns every capability an entry may list.
func Capabilities() []Capability {
	return []Capability{CapTools, CapVision, CapFiles, CapThinking, CapEmbedding}
}

// The adapters an entry may name. The plan fixes the three real ones.
const (
	AdapterFake            = "fake"
	AdapterAnthropic       = "anthropic"
	AdapterResponses       = "responses"
	AdapterChatCompletions = "chat_completions"
)

// Adapter is what the registry knows about one adapter: enough to check an
// entry that names it. The code that speaks to the provider is elsewhere.
type Adapter struct {
	Name string
	// Speaks is the format the adapter translates to and from.
	Speaks string
	// InProcess says the adapter runs inside Keystone and opens no
	// connection. Its entries have no endpoint and no key, and are local.
	InProcess bool
}

// Adapters returns the adapters an entry may name.
func Adapters() []Adapter {
	return []Adapter{
		{Name: AdapterFake, Speaks: "scripts, for tests and the test tenant", InProcess: true},
		{Name: AdapterAnthropic, Speaks: "Anthropic Messages"},
		{Name: AdapterResponses, Speaks: "OpenAI Responses"},
		{Name: AdapterChatCompletions, Speaks: "Chat Completions"},
	}
}

func adapter(name string) (Adapter, bool) {
	for _, a := range Adapters() {
		if a.Name == name {
			return a, true
		}
	}
	return Adapter{}, false
}

// Price is what a model costs, in US dollars for a million tokens. A local
// model costs nothing.
type Price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// CostMicros is what one call cost, in millionths of a US dollar, which is
// the unit the audit log keeps. The four counts are the seam's: they do not
// overlap, and thinking is part of the output.
func (p Price) CostMicros(input, output, cacheRead, cacheWrite int) int64 {
	// Dollars for a million tokens is millionths of a dollar for one.
	cost := float64(input)*p.Input + float64(output)*p.Output +
		float64(cacheRead)*p.CacheRead + float64(cacheWrite)*p.CacheWrite
	return int64(math.Round(cost))
}

// Model is one entry in a tenant's registry.
type Model struct {
	// Name is the registry name, the one a seam.Request carries.
	Name string `json:"name"`
	// Label is what the model picker shows.
	Label string `json:"label"`
	// Adapter is one of Adapters.
	Adapter string `json:"adapter"`
	// Endpoint is the base address the adapter calls. An in-process
	// adapter has none.
	Endpoint string `json:"endpoint,omitempty"`
	// ProviderModel is the provider's own name for the model, which the
	// adapter sends.
	ProviderModel string `json:"provider_model,omitempty"`
	// Key is the name of the secret in the tenant's vault that holds the
	// provider key. It is never the key. Empty means the endpoint asks for
	// none.
	Key string `json:"key,omitempty"`
	// ContextWindow is the most tokens one call may hold, input and output
	// together.
	ContextWindow int `json:"context_window"`
	// OutputLimit is the most tokens the model writes in one reply.
	OutputLimit int `json:"output_limit"`
	// Capabilities are kept in order and without repeats.
	Capabilities []Capability `json:"capabilities,omitempty"`
	Price        Price        `json:"price"`
	Class        Class        `json:"class"`
	// CACert is one certificate authority to trust for this entry's
	// endpoint, for an endpoint that has its own certificate, such as a
	// gateway on the home network. It is PEM text, or the path of a PEM
	// file. It is never a private key. Empty means the usual authorities.
	CACert string `json:"ca_cert,omitempty"`
}

// MayReadSensitive reports whether content from a sensitive space may be
// sent to the model. This is fixed rule 3, and the only place the class is
// turned into yes or no. Nothing can change the answer but the entry's own
// class, and anything that is not local or trusted is no.
func (m Model) MayReadSensitive() bool {
	return m.Class == ClassLocal || m.Class == ClassTrusted
}

// Has reports whether the model has a capability.
func (m Model) Has(c Capability) bool {
	return slices.Contains(m.Capabilities, c)
}

// ErrInvalid is wrapped by the error for an entry that is refused. The
// error's text is the reason, and is safe to show.
var ErrInvalid = errors.New("the model entry is not valid")

func refuse(name, format string, args ...any) error {
	who := "the entry"
	if name != "" {
		who = "model " + name
	}
	return fmt.Errorf("%w: %s: %s", ErrInvalid, who, fmt.Sprintf(format, args...))
}

const (
	maxLabel         = 80
	maxProviderModel = 200
	maxEndpoint      = 500
)

var (
	nameShape = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	// keyShape is the shape of a secret's name in the vault.
	keyShape = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)
)

// Check refuses an entry that is not valid, and says why. The reason names
// the field and what would be accepted.
func Check(m Model) error {
	if !nameShape.MatchString(m.Name) {
		return refuse("", "the name %q is not a registry name: use 1 to 63 lower-case letters, digits, dots, dashes and underscores, starting with a letter or a digit", short(m.Name))
	}
	no := func(format string, args ...any) error { return refuse(m.Name, format, args...) }

	if err := checkLine("label", m.Label, maxLabel, true); err != nil {
		return no("%v", err)
	}
	a, ok := adapter(m.Adapter)
	if !ok {
		var names []string
		for _, a := range Adapters() {
			names = append(names, a.Name)
		}
		return no("the adapter %q is not one of %s", short(m.Adapter), strings.Join(names, ", "))
	}
	switch m.Class {
	case ClassLocal, ClassHosted, ClassTrusted:
	case "":
		return no("the class is missing: say local, hosted or trusted")
	default:
		return no("the class %q is not local, hosted or trusted", short(string(m.Class)))
	}
	if err := checkLine("provider_model", m.ProviderModel, maxProviderModel, false); err != nil {
		return no("%v", err)
	}
	if m.Key != "" && !keyShape.MatchString(m.Key) {
		// The value is not echoed, in case a key was pasted here.
		return no("key is the name of a secret in the tenant's vault, such as provider.example, and never the key itself: use 1 to 100 lower-case letters, digits, dots, dashes and underscores")
	}

	if a.InProcess {
		switch {
		case m.Endpoint != "":
			return no("the %s adapter runs inside Keystone and takes no endpoint", a.Name)
		case m.Key != "":
			return no("the %s adapter runs inside Keystone and takes no key", a.Name)
		case m.Class != ClassLocal:
			return no("the %s adapter runs inside Keystone, so its class is local", a.Name)
		case m.CACert != "":
			return no("the %s adapter runs inside Keystone and takes no ca_cert", a.Name)
		}
	} else {
		if m.ProviderModel == "" {
			return no("provider_model is missing: it is the provider's own name for the model")
		}
		home, err := checkEndpoint(m.Endpoint)
		if err != nil {
			return no("%v", err)
		}
		if m.Class == ClassLocal && !home {
			return no("the class is local and the endpoint is not on this machine or the home network: a local endpoint is localhost, a private address, a name with no dot, or a name ending in %s. Mark a model you trust somewhere else as trusted", strings.Join(homeSuffixes, ", "))
		}
		if err := checkCACert(m); err != nil {
			return no("%v", err)
		}
	}

	if m.ContextWindow <= 0 {
		return no("context_window is %d, and it must be more than zero", m.ContextWindow)
	}
	if m.OutputLimit <= 0 {
		return no("output_limit is %d, and it must be more than zero", m.OutputLimit)
	}
	if m.OutputLimit > m.ContextWindow {
		return no("output_limit is %d, which is more than the context window of %d", m.OutputLimit, m.ContextWindow)
	}

	for i, c := range m.Capabilities {
		if !slices.Contains(Capabilities(), c) {
			var names []string
			for _, c := range Capabilities() {
				names = append(names, string(c))
			}
			return no("the capability %q is not one of %s", short(string(c)), strings.Join(names, ", "))
		}
		if slices.Contains(m.Capabilities[:i], c) {
			return no("the capability %s is listed twice", c)
		}
	}

	for _, p := range []struct {
		name  string
		value float64
	}{
		{"input", m.Price.Input}, {"output", m.Price.Output},
		{"cache_read", m.Price.CacheRead}, {"cache_write", m.Price.CacheWrite},
	} {
		if math.IsNaN(p.value) || math.IsInf(p.value, 0) || p.value < 0 {
			return no("price.%s must be zero or more: it is US dollars for a million tokens", p.name)
		}
	}
	return nil
}

// checkLine checks a field that is one line of text.
func checkLine(field, value string, most int, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%s is missing", field)
		}
		return nil
	}
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must be text with no space before or after it", field)
	}
	if strings.ContainsFunc(value, unicode.IsControl) {
		return fmt.Errorf("%s must be one line", field)
	}
	if n := utf8.RuneCountInString(value); n > most {
		return fmt.Errorf("%s is %d characters, and the most is %d", field, n, most)
	}
	return nil
}

// checkEndpoint checks the address an adapter calls, and reports whether it
// is on this machine or the home network. The address itself is not put in
// an error, in case a key was pasted into it.
func checkEndpoint(endpoint string) (home bool, err error) {
	if endpoint == "" {
		return false, errors.New("endpoint is missing: it is the address the adapter calls, such as https://models.example/v1")
	}
	if len(endpoint) > maxEndpoint {
		return false, fmt.Errorf("endpoint is longer than %d characters", maxEndpoint)
	}
	u, parseErr := url.Parse(endpoint)
	if parseErr != nil || u.Host == "" || u.Hostname() == "" || u.Opaque != "" {
		return false, errors.New("endpoint is not an address: it needs http or https, a host and an optional path, such as https://models.example/v1")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false, errors.New("endpoint must start with http:// or https://")
	}
	if u.User != nil {
		return false, errors.New("endpoint must not hold a user name or a password: a key goes in the tenant's vault, and the entry names it in key")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false, errors.New("endpoint must not hold a query or a fragment: a key goes in the tenant's vault, and the entry names it in key")
	}
	home = onHomeNetwork(u.Hostname())
	if u.Scheme == "http" && !home {
		return false, errors.New("endpoint uses http, which is allowed only on this machine or the home network: use https")
	}
	return home, nil
}

// maxCACert is the most text ca_cert may hold: room for a short chain.
const maxCACert = 32000

// checkCACert checks the certificate authority an entry trusts for its own
// endpoint. It is PEM text or the path of a PEM file, and the adapter reads
// it. It can only add an authority for an https endpoint: nothing here, and
// no other field, turns certificate checks off. The value is not put in an
// error.
func checkCACert(m Model) error {
	if m.CACert == "" {
		return nil
	}
	if !strings.HasPrefix(strings.ToLower(m.Endpoint), "https://") {
		return errors.New("ca_cert is for an https endpoint, and this endpoint uses http")
	}
	if len(m.CACert) > maxCACert {
		return fmt.Errorf("ca_cert is longer than %d characters", maxCACert)
	}
	if strings.Contains(m.CACert, "PRIVATE KEY") {
		return errors.New("ca_cert holds a private key: give the authority's certificate and nothing else")
	}
	if strings.Contains(m.CACert, "-----BEGIN") {
		return nil
	}
	if strings.TrimSpace(m.CACert) != m.CACert || strings.ContainsFunc(m.CACert, unicode.IsControl) {
		return errors.New("ca_cert is PEM text or the path of a PEM file on one line")
	}
	return nil
}

// homeSuffixes end the names that only resolve on a home network.
var homeSuffixes = []string{".local", ".lan", ".home.arpa", ".internal", ".localhost"}

// sharedAddresses is the range kept for carrier and overlay networks.
var sharedAddresses = netip.MustParsePrefix("100.64.0.0/10")

// onHomeNetwork reports whether a host is this machine or one on the home
// network, from its name or address alone. Nothing is looked up. It is
// fixed in code and not a setting, because the local class rests on it.
func onHomeNetwork(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || sharedAddresses.Contains(addr)
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return true
	}
	for _, s := range homeSuffixes {
		if strings.HasSuffix(host, s) {
			return true
		}
	}
	return false
}

// Decode reads one entry from JSON. It refuses a field it does not know,
// anything after the entry, and an entry Check refuses.
func Decode(raw []byte) (Model, error) {
	var m Model
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Model{}, fmt.Errorf("%w: %s", ErrInvalid, jsonReason(err))
	}
	if _, err := dec.Token(); err != io.EOF {
		return Model{}, fmt.Errorf("%w: there is more than one JSON value", ErrInvalid)
	}
	if err := Check(m); err != nil {
		return Model{}, err
	}
	return tidy(m), nil
}

// Encode writes a checked entry as JSON, the way it is stored.
func Encode(m Model) ([]byte, error) {
	if err := Check(m); err != nil {
		return nil, err
	}
	return json.Marshal(tidy(m))
}

// jsonReason is a decoding error with nothing from the input in it but a
// field's name.
func jsonReason(err error) string {
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &typeErr):
		return fmt.Sprintf("the field %s must be a %s", typeErr.Field, typeErr.Type)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return "the entry has a field the registry does not know: " + strings.TrimPrefix(err.Error(), "json: unknown field ")
	}
	return "the entry is not a JSON object the registry can read"
}

// tidy returns the entry with its capabilities in the order Capabilities
// lists them, so the same entry is always stored the same way.
func tidy(m Model) Model {
	var caps []Capability
	for _, c := range Capabilities() {
		if slices.Contains(m.Capabilities, c) {
			caps = append(caps, c)
		}
	}
	m.Capabilities = caps
	return m
}

// short trims text for an error message.
func short(s string) string {
	const most = 40
	if utf8.RuneCountInString(s) <= most {
		return s
	}
	return string([]rune(s)[:most]) + "..."
}
