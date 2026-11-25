// Package waymarks is the knowledge store adapter for Waymarks: a client of
// one Waymarks instance, over its HTTP API. It is the adapter named
// "waymarks".
//
// A tenant has one instance. Its address is a setting of that tenant, and
// its tokens are secrets in that tenant's vault, read by name through the
// tenant's handle. Nothing here reads a token from a setting or from the
// environment, and no error carries one.
//
// Waymarks gives each token a grant, so the adapter holds one token for each
// kind of work and sends each call under the token of its role: reads and
// searches under the reader, a new source under the intake token, and page
// vectors under the embedder. The capabilities are set from which tokens
// the vault holds.
//
// An instance that cannot be reached, is slow, refuses the token, or
// answers with something that is not Waymarks gives ErrUnavailable inside
// the timeout setting, so a chat carries on without it.
package waymarks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// Name is the adapter's name: a value of the knowledge.adapter setting.
const Name = "waymarks"

// The settings this adapter reads when a store is opened.
const (
	SettingAddress       = "knowledge.waymarks_address"
	SettingTimeout       = "knowledge.waymarks_timeout"
	SettingSearchResults = "knowledge.waymarks_search_max_results"
	SettingVectorsNeeded = "knowledge.waymarks_vectors_needed_max"
	SettingReplyMaxMB    = "knowledge.waymarks_reply_max_mb"
)

// The names of the tenant's secrets that hold its Waymarks tokens, one for
// each role. The reader is needed. The other two are optional, and each
// switches capabilities on.
const (
	// SecretReader reads and searches, for chat and sub-agents.
	SecretReader = "knowledge.waymarks.reader"
	// SecretIntake adds sources and reads nothing, for the memory pass.
	SecretIntake = "knowledge.waymarks.intake"
	// SecretEmbedder reads pages and stores their vectors, for the
	// embedding job.
	SecretEmbedder = "knowledge.waymarks.embedder"
)

// What Waymarks itself allows in one call.
const (
	waymarksSearchMax = 100
	waymarksNeedsMax  = 1000
)

func init() { knowledge.Register(Name, open) }

// store is one tenant's Waymarks instance, as three callers. It holds no
// content and no label between calls.
type store struct {
	base     string // the instance's address, with no slash at the end
	http     *http.Client
	timeout  time.Duration
	replyMax int64

	reader, intake, embedder string

	searchResults int
	vectorsNeeded int
}

// notSetUp is the answer when the tenant chose Waymarks and has not said
// where it is or given a token. It reads as unavailable, so a chat carries
// on, and it says what is missing.
func notSetUp(format string, args ...any) error {
	return fmt.Errorf("%w: Waymarks is not set up: %s", knowledge.ErrUnavailable, fmt.Sprintf(format, args...))
}

func open(ctx context.Context, t *tenant.Store) (knowledge.Store, error) {
	if t == nil {
		return nil, errors.New("the Waymarks adapter reads a tenant's address and tokens, and was given no tenant")
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	get := func(key string) (string, error) {
		text, err := tenantsettings.Get(ctx, t, reg, key)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return "", cerr
			}
			return "", fmt.Errorf("the setting %s: %w", key, err)
		}
		return text, nil
	}
	s := &store{}

	address, err := get(SettingAddress)
	if err != nil {
		return nil, err
	}
	if s.base, err = checkAddress(address); err != nil {
		return nil, err
	}

	text, err := get(SettingTimeout)
	if err != nil {
		return nil, err
	}
	if s.timeout, err = time.ParseDuration(text); err != nil || s.timeout < 100*time.Millisecond || s.timeout > 2*time.Minute {
		return nil, fmt.Errorf("the setting %s is %q, and must be a time from 100ms to 2m", SettingTimeout, text)
	}

	var replyMB int
	for _, want := range []struct {
		key      string
		into     *int
		min, max int
	}{
		{SettingSearchResults, &s.searchResults, 1, waymarksSearchMax},
		{SettingVectorsNeeded, &s.vectorsNeeded, 1, waymarksNeedsMax},
		{SettingReplyMaxMB, &replyMB, 1, 1024},
	} {
		text, err := get(want.key)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(text)
		if err != nil || n < want.min || n > want.max {
			return nil, fmt.Errorf("the setting %s is %q, and must be a number from %d to %d", want.key, text, want.min, want.max)
		}
		*want.into = n
	}
	s.replyMax = int64(replyMB) << 20

	for _, want := range []struct {
		name string
		into *string
	}{
		{SecretReader, &s.reader}, {SecretIntake, &s.intake}, {SecretEmbedder, &s.embedder},
	} {
		value, _, err := t.Secrets().Get(want.name)
		if err != nil {
			return nil, fmt.Errorf("the secret %s: %w", want.name, err)
		}
		*want.into = strings.TrimSpace(value)
	}
	if s.reader == "" {
		return nil, notSetUp("the tenant has no secret named %s, the token Keystone reads with", SecretReader)
	}

	// The standard transport, with its own pool of connections. No redirect
	// is followed: an instance that sends one is not answering as Waymarks,
	// and a token is never sent on to wherever it points.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	s.http = &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return s, nil
}

// checkAddress reads the address setting: http or https, a host, and maybe
// a path the instance is served under. Nothing else may be in it.
func checkAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", notSetUp("the setting %s is empty, and it says where the instance is", SettingAddress)
	}
	u, err := url.Parse(address)
	switch {
	case err != nil, u.Host == "", u.Hostname() == "":
		return "", notSetUp("the setting %s is not an address such as http://127.0.0.1:7878", SettingAddress)
	case u.Scheme != "http" && u.Scheme != "https":
		return "", notSetUp("the setting %s must start with http:// or https://", SettingAddress)
	case u.User != nil:
		return "", notSetUp("the setting %s must not hold a name or a password: a token is a secret, not part of the address", SettingAddress)
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return "", notSetUp("the setting %s must be an address alone, with no ? and no #", SettingAddress)
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/"), nil
}

// Capabilities follow the tokens. The reader is always there, and it can
// read the catalog, which the marker is made from. The intake token adds
// sources. The embedder stores page vectors, and a search by vector is
// offered only when it is there: an instance nobody pushes vectors to has
// none to search.
func (s *store) Capabilities() knowledge.Capabilities {
	return knowledge.Capabilities{
		AddSource:     s.intake != "",
		CatalogMarker: true,
		VectorSearch:  s.embedder != "",
		PageVectors:   s.embedder != "",
	}
}

// Close lets go of the connections the handle kept open.
func (s *store) Close() error {
	s.http.CloseIdleConnections()
	return nil
}
