package web

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
)

// Searcher is a search provider that answers for every entry, whatever its
// kind. Options.Searcher takes one, so a caller can stand one in; without
// one, each entry's kind makes its adapter.
type Searcher interface {
	// Search sends query to the provider with the key and returns at most
	// count results, best first. An error's text is written for the model
	// and the person to read, and never holds the key; a *Failure says why.
	Search(ctx context.Context, key, query string, count int) ([]Result, error)
}

// Result is one web search result.
type Result struct {
	Title   string
	URL     string
	Content string
}

func init() {
	register(Kind{Name: KindTavily, Title: "Tavily", New: newTavily})
}

// newTavily makes Tavily's adapter for one entry. An entry with no
// endpoint searches at agents.web_search_endpoint, as P7-03 did; one with
// an endpoint searches at its /search, and checks at its /usage.
func newTavily(p Provider, o Options) Adapter {
	t := Tavily{Endpoint: o.SearchEndpoint, Depth: o.SearchDepth, Timeout: o.SearchTimeout, MaxBytes: o.SearchMaxBytes}
	if p.Endpoint != "" {
		base := strings.TrimRight(p.Endpoint, "/")
		t.Endpoint, t.UsageEndpoint = base+"/search", base+"/usage"
	}
	return t
}

// Tavily searches with Tavily's search API, in the shape New Keystone
// used: a basic search by default, no generated answer, no raw page text,
// and the parameters as sent (auto_parameters off). Its free check is
// Tavily's usage check, which costs no credits.
type Tavily struct {
	// Endpoint is the search address: agents.web_search_endpoint, or an
	// entry's endpoint with /search.
	Endpoint string
	// UsageEndpoint is the usage check's address. Empty is the search
	// address with its last part, /search, as /usage.
	UsageEndpoint string
	// Depth is basic or advanced: agents.web_search_depth.
	Depth string
	// Timeout is how long one search may take: agents.web_search_timeout.
	Timeout time.Duration
	// MaxBytes is the most bytes of an answer that are read:
	// agents.web_search_max_bytes. A larger answer is refused.
	MaxBytes int64
	// Client sends the request. Nil means a client of its own, which uses
	// the environment's proxy, if any.
	Client *http.Client
}

type tavilyRequest struct {
	Query             string `json:"query"`
	SearchDepth       string `json:"search_depth"`
	MaxResults        int    `json:"max_results"`
	IncludeAnswer     bool   `json:"include_answer"`
	IncludeRawContent bool   `json:"include_raw_content"`
	IncludeImages     bool   `json:"include_images"`
	AutoParameters    bool   `json:"auto_parameters"`
}

type tavilyAnswer struct {
	Results []struct {
		Title   any `json:"title"`
		URL     any `json:"url"`
		Content any `json:"content"`
	} `json:"results"`
}

// Search implements Adapter.
func (t Tavily) Search(ctx context.Context, key string, q Query) (Answer, error) {
	body, err := json.Marshal(tavilyRequest{Query: q.Text, SearchDepth: t.Depth, MaxResults: q.Count})
	if err != nil {
		return Answer{}, &Failure{Why: WhyMalformed, Text: "Keystone could not write the search request"}
	}
	raw, err := t.call(ctx, http.MethodPost, t.Endpoint, key, body)
	if err != nil {
		return Answer{}, err
	}
	var answer tavilyAnswer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return Answer{}, &Failure{Why: WhyUnavailable, Text: "Keystone could not read Tavily's answer"}
	}
	var out []Result
	for _, r := range answer.Results {
		title, _ := r.Title.(string)
		address, _ := r.URL.(string)
		content, _ := r.Content.(string)
		if strings.TrimSpace(title) == "" || strings.TrimSpace(content) == "" || !citable(address) {
			continue
		}
		out = append(out, Result{Title: title, URL: address, Content: content})
		if len(out) == q.Count {
			break
		}
	}
	return Answer{Results: out}, nil
}

// tavilyUsage is the part of Tavily's usage check Keystone reads: the
// key's own limit, if it has one, and the account's plan and pay-as-you-go
// credits. A limit that is not set is null.
type tavilyUsage struct {
	Key struct {
		Usage *int `json:"usage"`
		Limit *int `json:"limit"`
	} `json:"key"`
	Account struct {
		PlanUsage  *int `json:"plan_usage"`
		PlanLimit  *int `json:"plan_limit"`
		PaygoUsage *int `json:"paygo_usage"`
		PaygoLimit *int `json:"paygo_limit"`
	} `json:"account"`
}

// Check implements Checker with Tavily's usage check, GET /usage, which
// costs no credits. What is left is the least of the key's own limit and
// the account's plan and pay-as-you-go credits, less what each has used. A
// basic search is one credit.
func (t Tavily) Check(ctx context.Context, key string) (Quota, error) {
	address := t.UsageEndpoint
	if address == "" {
		u, err := url.Parse(t.Endpoint)
		if err != nil {
			return Quota{}, &Failure{Why: WhyUnavailable, Text: "the usage check's address is not an address Keystone can call"}
		}
		u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/search") + "/usage"
		u.RawQuery = ""
		address = u.String()
	}
	raw, err := t.call(ctx, http.MethodGet, address, key, nil)
	if err != nil {
		return Quota{}, err
	}
	var u tavilyUsage
	if err := json.Unmarshal(raw, &u); err != nil {
		return Quota{}, &Failure{Why: WhyUnavailable, Text: "Keystone could not read Tavily's usage check"}
	}
	return u.quota(), nil
}

func (u tavilyUsage) quota() Quota {
	var q Quota
	least := func(left int) {
		if !q.Known || left < q.Left {
			q.Left, q.Known = max(0, left), true
		}
	}
	if u.Key.Limit != nil {
		least(*u.Key.Limit - deref(u.Key.Usage))
	}
	if u.Account.PlanLimit != nil {
		left := *u.Account.PlanLimit - deref(u.Account.PlanUsage)
		if u.Account.PaygoLimit != nil {
			left += max(0, *u.Account.PaygoLimit-deref(u.Account.PaygoUsage))
		}
		least(left)
	}
	return q
}

func deref(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

// call sends one request to Tavily with the key and returns the body of a
// 200 answer. Any other answer is a *Failure that says why, and nothing
// from the answer's body goes into it.
func (t Tavily) call(ctx context.Context, method, address, key string, body []byte) ([]byte, error) {
	if t.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.Timeout)
		defer cancel()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, reader)
	if err != nil {
		// The endpoint is a setting or an entry, and one that is not an
		// address is the install's fault. The error names no key.
		return nil, &Failure{Why: WhyUnavailable, Text: "the web search address in agents.web_search_endpoint is not an address Keystone can call"}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	client := t.Client
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return nil, &Failure{Why: WhyUnavailable, Text: fmt.Sprintf("Tavily did not answer within %s", t.Timeout)}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Failure{Why: WhyUnavailable, Text: "Keystone could not reach Tavily"}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, &Failure{Why: WhyRefused, Text: "Tavily refused the key"}
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, &Failure{Why: WhyRate, Wait: retryAfter(resp.Header, time.Now()), Text: "Tavily says a usage or rate limit was reached: too many requests for now"}
	case resp.StatusCode == 432 || resp.StatusCode == 433:
		// 432 is the plan's or the key's limit, 433 the pay-as-you-go limit.
		return nil, &Failure{Why: WhyQuota, Text: "Tavily says a usage or rate limit was reached: the credits are spent"}
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity:
		return nil, &Failure{Why: WhyMalformed, Text: fmt.Sprintf("Tavily called the request malformed (it answered with status %d)", resp.StatusCode)}
	case resp.StatusCode != http.StatusOK:
		return nil, &Failure{Why: WhyUnavailable, Text: fmt.Sprintf("Tavily is unavailable (it answered with status %d)", resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, t.MaxBytes+1))
	if err != nil {
		return nil, &Failure{Why: WhyUnavailable, Text: "Keystone could not read Tavily's answer"}
	}
	if int64(len(raw)) > t.MaxBytes {
		return nil, &Failure{Why: WhyUnavailable, Text: fmt.Sprintf("Tavily's answer was larger than the %d bytes Keystone reads (agents.web_search_max_bytes), so it was not used", t.MaxBytes)}
	}
	return raw, nil
}

// retryAfter reads a Retry-After header: seconds, or a date. Zero when
// there is none, or it does not read.
func retryAfter(h http.Header, now time.Time) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// HeaderQuota reads what is left from the rate limit headers Brave sends
// with every answer: X-RateLimit-Remaining and X-RateLimit-Reset, each a
// list with one value for each window, the last the month's, the reset in
// seconds from now. P7-07's Brave adapter returns it in its Answer. Quota
// is not Known when the headers are missing or do not read.
func HeaderQuota(h http.Header, now time.Time) Quota {
	last := func(name string) (int, bool) {
		v := strings.TrimSpace(h.Get(name))
		if v == "" {
			return 0, false
		}
		parts := strings.Split(v, ",")
		n, err := strconv.Atoi(strings.TrimSpace(parts[len(parts)-1]))
		return n, err == nil && n >= 0
	}
	left, ok := last("X-RateLimit-Remaining")
	if !ok {
		return Quota{}
	}
	q := Quota{Left: left, Known: true}
	if secs, ok := last("X-RateLimit-Reset"); ok {
		q.Reset = now.Add(time.Duration(secs) * time.Second).UTC().Truncate(time.Second)
	}
	return q
}

// citable reports whether a result's address may be shown and linked to:
// http or https, with a host, no user name or password, no spaces or
// control characters, and not too long.
func citable(address string) bool {
	if address == "" || len(address) > maxURL || strings.ContainsAny(address, " \\") {
		return false
	}
	for _, r := range address {
		if r < 0x21 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(address)
	if err != nil || u.User != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// isTimeout reports whether err is a network timeout.
func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}
