package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Brave's adapter (P7-07), as Brave's Web Search API documents it on
// November 28, 2026 (decision 0750): GET /res/v1/web/search with q and
// count (at most 20), the key in X-Subscription-Token, and the results in
// web.results, each with title, url and description. Every answer carries
// X-RateLimit-Remaining and X-RateLimit-Reset, one value for each window
// (a second and a month), which say what is left of the month.

func init() {
	register(Kind{Name: KindBrave, Title: "Brave", New: newBrave})
}

// braveSearchPath is the search's path below an entry's endpoint.
const braveSearchPath = "/res/v1/web/search"

// braveMaxCount is the most results Brave gives for one search.
const braveMaxCount = 20

// newBrave makes Brave's adapter for one entry: at web.brave_endpoint, or
// at the entry's endpoint with /res/v1/web/search.
func newBrave(p Provider, o Options) Adapter {
	b := Brave{Endpoint: o.Endpoints.Brave, Timeout: o.SearchTimeout, MaxBytes: o.SearchMaxBytes, Now: o.Now}
	if p.Endpoint != "" {
		b.Endpoint = searchAddress(p.Endpoint, "", braveSearchPath)
	}
	return b
}

// Brave searches with Brave's Web Search API.
type Brave struct {
	// Endpoint is the full search address: web.brave_endpoint, or an
	// entry's endpoint with /res/v1/web/search.
	Endpoint string
	// Timeout bounds one search: agents.web_search_timeout.
	Timeout time.Duration
	// MaxBytes is the most of an answer read: agents.web_search_max_bytes.
	MaxBytes int64
	// Client sends the request. Nil means one of its own, which follows no
	// redirect.
	Client *http.Client
	// Now is the clock the quota's reset is read against. Nil is time.Now.
	Now func() time.Time
}

func (b Brave) sender() sender {
	return sender{title: "Brave", timeout: b.Timeout, maxBytes: b.MaxBytes, client: b.Client}
}

func (b Brave) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// braveAnswer is the part of Brave's answer Keystone reads.
type braveAnswer struct {
	Web struct {
		Results []struct {
			Title       any `json:"title"`
			URL         any `json:"url"`
			Description any `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

// Search implements Adapter. A good answer carries what is left of the
// month, from its headers.
func (b Brave) Search(ctx context.Context, key string, q Query) (Answer, error) {
	s := b.sender()
	if b.Endpoint == "" {
		return Answer{}, &Failure{Why: WhyUnavailable, Text: "the Brave provider " + errNoEndpoint.Error()}
	}
	u, err := url.Parse(b.Endpoint)
	if err != nil || u.Host == "" {
		return Answer{}, s.endpointFailure()
	}
	values := u.Query()
	values.Set("q", q.Text)
	values.Set("count", strconv.Itoa(min(max(q.Count, 1), braveMaxCount)))
	u.RawQuery = values.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return Answer{}, s.endpointFailure()
	}
	req.Header.Set("X-Subscription-Token", key)
	rep, err := s.do(ctx, req)
	if err != nil {
		return Answer{}, err
	}
	now := b.now()
	switch rep.status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return Answer{}, s.refused()
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		// Brave answers a key it does not know with 422 and the code
		// SUBSCRIPTION_TOKEN_INVALID, from its authentication component.
		code := strings.ToUpper(errorField(rep.body, "error", "code"))
		if strings.Contains(code, "TOKEN") || errorField(rep.body, "error", "meta", "component") == "authentication" {
			return Answer{}, s.refused()
		}
		return Answer{}, s.malformed(rep.status)
	case http.StatusPaymentRequired:
		// A prepaid plan whose balance is spent is paused until it is topped
		// up or the month's credits come back.
		return Answer{}, &Failure{Why: WhyQuota, Reset: braveQuota(rep.header, now).Reset, Text: "Brave says the plan's credits are spent"}
	case http.StatusTooManyRequests:
		return Answer{}, braveLimit(rep.header, rep.body, now)
	default:
		return Answer{}, s.unavailable(rep.status)
	}
	var answer braveAnswer
	if err := json.Unmarshal(rep.body, &answer); err != nil {
		return Answer{}, s.unreadable()
	}
	var out []Result
	for _, r := range answer.Web.Results {
		title, _ := r.Title.(string)
		address, _ := r.URL.(string)
		content, _ := r.Description.(string)
		if keep(&out, title, address, content, q.Count) {
			break
		}
	}
	return Answer{Results: out, Quota: braveQuota(rep.header, now)}, nil
}

// braveLimit reads a 429. With nothing left in the month's window, or a
// code that names the quota, it is a spent quota until the month's reset;
// otherwise it is a rate limit for as long as the spent window's reset, or
// Retry-After.
func braveLimit(h http.Header, body []byte, now time.Time) *Failure {
	month := braveQuota(h, now)
	code := strings.ToUpper(errorField(body, "error", "code"))
	if (month.Known && month.Left == 0) || strings.Contains(code, "QUOTA") {
		return &Failure{Why: WhyQuota, Reset: month.Reset, Text: "Brave says the month's quota is spent"}
	}
	wait := braveWait(h)
	if wait <= 0 {
		wait = retryAfter(h, now)
	}
	return &Failure{Why: WhyRate, Wait: wait, Text: "Brave says a rate limit was reached: too many requests for now"}
}

// braveWindows reads one of Brave's rate limit headers: a list with one
// number for each window. Nil when it is missing or does not read.
func braveWindows(h http.Header, name string) []int {
	v := strings.TrimSpace(h.Get(name))
	if v == "" {
		return nil
	}
	var out []int
	for _, part := range strings.Split(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// braveSizes reads X-RateLimit-Policy, "1;w=1, 15000;w=2592000": each
// window's size in seconds. Nil when it is missing or does not read.
func braveSizes(h http.Header) []int {
	v := strings.TrimSpace(h.Get("X-RateLimit-Policy"))
	if v == "" {
		return nil
	}
	var out []int
	for _, part := range strings.Split(v, ",") {
		i := strings.Index(part, "w=")
		if i < 0 {
			return nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(part[i+2:]))
		if err != nil || n <= 0 {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// longWindow is the shortest window that counts as a quota rather than a
// rate limit: a day.
const longWindow = 24 * 60 * 60

// braveQuota is what is left of the month, from the headers. With
// X-RateLimit-Policy it is the longest window, when that is a day or more;
// without it, the last window, as Brave lists them (HeaderQuota). Unknown
// when the headers say nothing about a long window.
func braveQuota(h http.Header, now time.Time) Quota {
	sizes := braveSizes(h)
	if sizes == nil {
		return HeaderQuota(h, now)
	}
	left, resets := braveWindows(h, "X-RateLimit-Remaining"), braveWindows(h, "X-RateLimit-Reset")
	longest := 0
	for i, size := range sizes {
		if size > sizes[longest] {
			longest = i
		}
	}
	if sizes[longest] < longWindow || longest >= len(left) {
		return Quota{}
	}
	q := Quota{Left: left[longest], Known: true}
	if longest < len(resets) {
		q.Reset = now.Add(time.Duration(resets[longest]) * time.Second).UTC().Truncate(time.Second)
	}
	return q
}

// braveWait is how long a rate limit lasts: the longest reset among the
// windows with nothing left, else the first window's reset. Zero when the
// headers do not say.
func braveWait(h http.Header) time.Duration {
	left, resets := braveWindows(h, "X-RateLimit-Remaining"), braveWindows(h, "X-RateLimit-Reset")
	if len(resets) == 0 {
		return 0
	}
	wait := -1
	for i, n := range left {
		if n == 0 && i < len(resets) && resets[i] > wait {
			wait = resets[i]
		}
	}
	if wait < 0 {
		wait = resets[0]
	}
	return time.Duration(wait) * time.Second
}
