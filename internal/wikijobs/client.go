package wikijobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/knowledge/waymarks"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// A job reaches Waymarks through its HTTP API, as Waymarks' own MCP server
// does (internal/mcp/tools.go in Waymarks), under the job's own token and
// no other, so Waymarks' audit shows each job as its own principal. The
// instance is the one the person's knowledge store names:
// knowledge.waymarks_address, with its timeout and largest answer.

// client is one job's caller of the person's Waymarks instance.
type client struct {
	base     string
	token    string
	http     *http.Client
	timeout  time.Duration
	replyMax int64
}

// errNotSetUp says the job cannot reach Waymarks: no address, or no token
// of its own. Its text says what is missing and never holds a token.
var errNotSetUp = errors.New("the job is not set up")

// openClient makes a job's caller from the person's settings and the job's
// own token in their vault.
func openClient(ctx context.Context, s *tenant.Store, j Job) (*client, error) {
	if j.Store != StoreWaymarks || j.Secret == "" {
		return nil, fmt.Errorf("%w: %s does not keep Waymarks", errNotSetUp, j.Name)
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	get := func(key string) (string, error) { return tenantsettings.Get(ctx, s, reg, key) }
	address, err := get(waymarks.SettingAddress)
	if err != nil {
		return nil, err
	}
	base, err := checkAddress(address)
	if err != nil {
		return nil, err
	}
	text, err := get(waymarks.SettingTimeout)
	if err != nil {
		return nil, err
	}
	timeout, err := time.ParseDuration(text)
	if err != nil || timeout < 100*time.Millisecond || timeout > 2*time.Minute {
		return nil, fmt.Errorf("the setting %s is %q, and must be a time from 100ms to 2m", waymarks.SettingTimeout, text)
	}
	text, err = get(waymarks.SettingReplyMaxMB)
	if err != nil {
		return nil, err
	}
	mb, err := strconv.Atoi(text)
	if err != nil || mb < 1 || mb > 1024 {
		return nil, fmt.Errorf("the setting %s is %q, and must be a number from 1 to 1024", waymarks.SettingReplyMaxMB, text)
	}
	token, _, err := s.Secrets().Get(j.Secret)
	if err != nil {
		return nil, fmt.Errorf("the secret %s: %w", j.Secret, err)
	}
	if token = strings.TrimSpace(token); token == "" {
		return nil, fmt.Errorf("%w: there is no secret named %s, the job's own Waymarks token (docs/runbooks/wiki-jobs.md)", errNotSetUp, j.Secret)
	}
	// No redirect is followed, so the token never goes to another address.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &client{
		base: base, token: token, timeout: timeout, replyMax: int64(mb) << 20,
		http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

// checkAddress reads the address setting as the Waymarks adapter does.
func checkAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", fmt.Errorf("%w: the setting %s is empty, and it says where Waymarks is", errNotSetUp, waymarks.SettingAddress)
	}
	u, err := url.Parse(address)
	switch {
	case err != nil, u.Host == "", u.Hostname() == "", u.Scheme != "http" && u.Scheme != "https":
		return "", fmt.Errorf("%w: the setting %s is not an address such as http://127.0.0.1:7878", errNotSetUp, waymarks.SettingAddress)
	case u.User != nil, u.RawQuery != "", u.ForceQuery, u.Fragment != "":
		return "", fmt.Errorf("%w: the setting %s must be an address alone", errNotSetUp, waymarks.SettingAddress)
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/"), nil
}

// reply is one answer: its data, and whether Waymarks marked it as holding
// something from a sensitive space.
type reply struct {
	data      json.RawMessage
	sensitive bool
}

// refusal is an answer that is an error.
type refusal struct {
	status  int
	code    string
	message string
}

func (r *refusal) Error() string {
	return fmt.Sprintf("Waymarks refused it (%d %s): %s", r.status, r.code, r.message)
}

// scrub takes the job's token out of a text.
func (c *client) scrub(text string) string {
	if c.token != "" {
		text = strings.ReplaceAll(text, c.token, "(a token)")
	}
	return text
}

// call makes one call under the job's token. body, when not nil, is sent
// as JSON. Its errors never hold the token or the address.
func (c *client) call(ctx context.Context, method, path string, query url.Values, body any) (reply, error) {
	if err := ctx.Err(); err != nil {
		return reply{}, err
	}
	timed, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return reply{}, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(timed, method, target, rd)
	if err != nil {
		return reply{}, errors.New("Waymarks cannot be called at the address in the settings")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return reply{}, cerr
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return reply{}, fmt.Errorf("Waymarks did not answer within %s", c.timeout)
		}
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return reply{}, fmt.Errorf("Waymarks cannot be reached: %s", c.scrub(err.Error()))
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, c.replyMax+1))
	if err != nil {
		return reply{}, fmt.Errorf("Waymarks' answer could not be read: %s", c.scrub(err.Error()))
	}
	if int64(len(raw)) > c.replyMax {
		return reply{}, fmt.Errorf("Waymarks sent an answer larger than the %d MB the settings allow", c.replyMax>>20)
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return reply{}, errors.New("Waymarks sent a redirect, which is not followed")
	}
	var env struct {
		Data      json.RawMessage `json:"data"`
		Sensitive bool            `json:"sensitive"`
		Error     *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return reply{}, fmt.Errorf("Waymarks sent an answer that is not its own (status %d)", res.StatusCode)
	}
	if env.Error != nil {
		return reply{}, &refusal{status: res.StatusCode, code: c.scrub(env.Error.Code), message: c.scrub(env.Error.Message)}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || env.Data == nil {
		return reply{}, fmt.Errorf("Waymarks sent an answer that is not its own (status %d)", res.StatusCode)
	}
	return reply{data: env.Data, sensitive: env.Sensitive || res.Header.Get("Waymarks-Sensitive") == "true"}, nil
}

// into makes a call and reads its data into v.
func (c *client) into(ctx context.Context, method, path string, query url.Values, body, v any) (reply, error) {
	r, err := c.call(ctx, method, path, query, body)
	if err != nil {
		return r, err
	}
	if v != nil {
		if err := json.Unmarshal(r.data, v); err != nil {
			return r, fmt.Errorf("Waymarks' answer to %s is not in the shape Keystone knows", path)
		}
	}
	return r, nil
}

// spaces reads which of the spaces the job can read are sensitive. A space
// Waymarks gives no label of normal counts as sensitive.
func (c *client) spaces(ctx context.Context) (map[string]bool, error) {
	var list []struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	}
	if _, err := c.into(ctx, http.MethodGet, "/v1/spaces", nil, nil, &list); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, sp := range list {
		out[sp.Name] = sp.Label != "normal"
	}
	return out, nil
}
