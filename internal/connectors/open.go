package connectors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/mcp"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
)

// How a connector is reached: the transport, made from the person's record
// and their vault, and nothing else.

// removed is what a token is replaced with in anything a server sends back.
const removed = "(the token, removed)"

// passedOn are the variables of Keystone's own environment a stdio
// connector's command is given, so it can find programs and its folders.
// Nothing else of Keystone's environment reaches it: the command gets these,
// the connector's own variables, and its token.
var passedOn = map[string]bool{
	"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "SYSTEMDRIVE": true, "WINDIR": true, "COMSPEC": true,
	"TEMP": true, "TMP": true, "TMPDIR": true, "HOME": true, "USERPROFILE": true, "HOMEDRIVE": true, "HOMEPATH": true,
	"APPDATA": true, "LOCALAPPDATA": true, "PROGRAMDATA": true, "PROGRAMFILES": true, "PROGRAMFILES(X86)": true, "PROGRAMW6432": true,
	"COMMONPROGRAMFILES": true, "USERNAME": true, "USER": true, "LOGNAME": true, "LANG": true, "LC_ALL": true, "TZ": true,
	"NUMBER_OF_PROCESSORS": true, "PROCESSOR_ARCHITECTURE": true, "OS": true,
	"XDG_RUNTIME_DIR": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_CACHE_HOME": true,
}

// reach is what it takes to open a session with one connector.
type reach struct {
	transport mcp.Transport
	// fingerprint changes when anything a session was opened with
	// changes: the connector's entry or its token. It is a hash, kept in
	// memory only.
	fingerprint string
	// redact takes the token out of a text from the server.
	redact func(string) string
}

// errNoToken is a connector whose token is named and not stored.
type errNoToken struct{ secret string }

func (e errNoToken) Error() string {
	return fmt.Sprintf("its token, stored as %s, is missing: store it on Settings → Connectors", e.secret)
}

// reachFor makes the transport for one connector of the person whose store
// s is. The token is read from their vault now.
func (o Options) reachFor(s *tenant.Store, c Connector) (reach, error) {
	token := ""
	if c.Secret != "" {
		v, ok, err := s.Secrets().Get(c.Secret)
		if err != nil {
			return reach{}, errors.New("its token could not be read from your vault")
		}
		if !ok || v == "" {
			return reach{}, errNoToken{c.Secret}
		}
		token = v
	}
	entry, _ := json.Marshal(c)
	sum := sha256.Sum256(append(append(entry, 0), token...))
	r := reach{fingerprint: hex.EncodeToString(sum[:]), redact: redactor(token)}
	switch c.Transport {
	case TransportStdio:
		r.transport = mcp.Stdio{Command: c.Command, Args: c.Args, Env: environment(c, token), Redact: r.redact}
	case TransportHTTP:
		if err := checkHTTP(c); err != nil {
			return reach{}, err // checked again, so a stored entry that would now be refused is not reached
		}
		header := http.Header{}
		if token != "" {
			header.Set("Authorization", "Bearer "+token)
		}
		r.transport = mcp.HTTP{URL: c.URL, Header: header, Client: o.client(c.Local)}
	default:
		return reach{}, errors.New("its transport is not one Keystone has")
	}
	return r, nil
}

// environment is the whole environment a stdio connector's command gets.
func environment(c Connector, token string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if ok && passedOn[strings.ToUpper(name)] {
			env = append(env, kv)
		}
	}
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+c.Env[k])
	}
	if c.TokenEnv != "" && token != "" {
		env = append(env, c.TokenEnv+"="+token)
	}
	return env
}

// client is the HTTP client for a remote connector. A connector that is not
// marked local connects only to public addresses, and one that is marked
// local only to addresses that are not, each checked as it connects, as
// web_fetch checks them. Neither uses a proxy or follows a redirect.
func (o Options) client(local bool) *http.Client {
	allow := web.IsPublic
	if local {
		allow = func(a netip.Addr) bool {
			return !web.IsPublic(a) && a.IsValid() && !a.Unmap().IsUnspecified() && !a.Unmap().IsMulticast()
		}
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         web.Dial(o.ConnectTimeout, allow),
			TLSHandshakeTimeout: o.ConnectTimeout,
			ForceAttemptHTTP2:   true,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     90 * time.Second,
		},
		CheckRedirect: mcp.NoRedirects,
	}
}

// redactor returns a function that takes a token out of a text.
func redactor(token string) func(string) string {
	if len(token) < 4 {
		return func(s string) string { return s }
	}
	return func(s string) string { return strings.ReplaceAll(s, token, removed) }
}

// connect opens a session with one connector, within the connect timeout,
// and lists its tools.
func (o Options) connect(ctx context.Context, r reach) (*mcp.Client, []mcp.Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, o.ConnectTimeout)
	defer cancel()
	c, err := mcp.Connect(ctx, r.transport, mcp.Options{MaxMessageBytes: o.MaxBytes})
	if err != nil {
		return nil, nil, reason(err, ctx, o.ConnectTimeout)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		c.Close()
		return nil, nil, reason(err, ctx, o.ConnectTimeout)
	}
	return c, tools, nil
}

// reason is an error from reaching a server, in words for a person.
func reason(err error, ctx context.Context, timeout time.Duration) error {
	var np web.ErrNotPublic
	switch {
	case errors.As(err, &np):
		return errors.New("its address is not one Keystone may connect to: a server that is not marked as your own local server must be on the public internet, and one that is must be on this machine or your home network")
	case ctx.Err() != nil && errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("it did not answer within %s", timeout)
	}
	return err
}

// Check reaches a connector now, lists its tools and keeps them, or keeps
// why it could not be reached. It returns the connector as it is kept
// after.
func (o Options) Check(ctx context.Context, s *tenant.Store, name string) (Kept, error) {
	k, err := Get(ctx, s, name)
	if err != nil {
		return Kept{}, err
	}
	problem := ""
	r, err := o.reachFor(s, k.Connector)
	var listed []mcp.Tool
	if err == nil {
		var c *mcp.Client
		if c, listed, err = o.connect(ctx, r); err == nil {
			c.Close()
		}
	}
	if err != nil {
		problem = r.redactText(err.Error())
	}
	if err := SaveTools(ctx, s, name, listed, problem, o.MaxTools); err != nil {
		return Kept{}, err
	}
	return Get(ctx, s, name)
}

// redactText takes the token out of a text, when there is a token.
func (r reach) redactText(s string) string {
	if r.redact == nil {
		return s
	}
	return r.redact(s)
}
