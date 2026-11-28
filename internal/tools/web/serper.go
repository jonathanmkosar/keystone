package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Serper's adapter (P7-07). Serper's API reference is behind a sign-in, so
// what it follows is what Serper shows in public and what its clients send,
// as read on November 28, 2026 (decision 0752): POST /search with q and num,
// the key in X-API-KEY, and the results in organic, each with title, link
// and snippet. A key it does not take is 401 or 403; spent credits are 402,
// or a 400 whose message names the credits; 429 is a rate limit.

func init() {
	register(Kind{Name: KindSerper, Title: "Serper", New: newSerper})
}

// serperSearchPath is the search's path below an entry's endpoint.
const serperSearchPath = "/search"

// newSerper makes Serper's adapter for one entry: at web.serper_endpoint,
// or at the entry's endpoint with /search.
func newSerper(p Provider, o Options) Adapter {
	s := Serper{Endpoint: o.Endpoints.Serper, Timeout: o.SearchTimeout, MaxBytes: o.SearchMaxBytes}
	if p.Endpoint != "" {
		s.Endpoint = searchAddress(p.Endpoint, "", serperSearchPath)
	}
	return s
}

// Serper searches Google's results through Serper.
type Serper struct {
	// Endpoint is the full search address: web.serper_endpoint, or an
	// entry's endpoint with /search.
	Endpoint string
	// Timeout bounds one search: agents.web_search_timeout.
	Timeout time.Duration
	// MaxBytes is the most of an answer read: agents.web_search_max_bytes.
	MaxBytes int64
	// Client sends the request. Nil means one of its own, which follows no
	// redirect.
	Client *http.Client
}

func (sp Serper) sender() sender {
	return sender{title: "Serper", timeout: sp.Timeout, maxBytes: sp.MaxBytes, client: sp.Client}
}

type serperRequest struct {
	Q   string `json:"q"`
	Num int    `json:"num"`
}

type serperAnswer struct {
	Organic []struct {
		Title   any `json:"title"`
		Link    any `json:"link"`
		Snippet any `json:"snippet"`
	} `json:"organic"`
}

// serperNum is the num asked for: 10, or 20 for more than ten. Serper
// counts a search of up to ten results as one credit, so asking for fewer
// saves nothing, and a number between the tens costs as the next ten.
func serperNum(count int) int {
	if count > 10 {
		return 20
	}
	return 10
}

// Search implements Adapter.
func (sp Serper) Search(ctx context.Context, key string, q Query) (Answer, error) {
	s := sp.sender()
	if sp.Endpoint == "" {
		return Answer{}, &Failure{Why: WhyUnavailable, Text: "the Serper provider " + errNoEndpoint.Error()}
	}
	body, err := json.Marshal(serperRequest{Q: q.Text, Num: serperNum(q.Count)})
	if err != nil {
		return Answer{}, &Failure{Why: WhyMalformed, Text: "Keystone could not write the search request"}
	}
	req, err := http.NewRequest(http.MethodPost, sp.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Answer{}, s.endpointFailure()
	}
	req.Header.Set("X-API-KEY", key)
	req.Header.Set("Content-Type", "application/json")
	rep, err := s.do(ctx, req)
	if err != nil {
		return Answer{}, err
	}
	switch rep.status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return Answer{}, s.refused()
	case http.StatusPaymentRequired:
		return Answer{}, &Failure{Why: WhyQuota, Text: "Serper says the credits are spent"}
	case http.StatusBadRequest:
		if strings.Contains(strings.ToLower(errorField(rep.body, "message")), "credit") {
			return Answer{}, &Failure{Why: WhyQuota, Text: "Serper says the credits are spent"}
		}
		return Answer{}, s.malformed(rep.status)
	case http.StatusUnprocessableEntity:
		return Answer{}, s.malformed(rep.status)
	case http.StatusTooManyRequests:
		return Answer{}, &Failure{Why: WhyRate, Wait: retryAfter(rep.header, time.Now()), Text: "Serper says a rate limit was reached: too many requests for now"}
	default:
		return Answer{}, s.unavailable(rep.status)
	}
	var answer serperAnswer
	if err := json.Unmarshal(rep.body, &answer); err != nil {
		return Answer{}, s.unreadable()
	}
	var out []Result
	for _, r := range answer.Organic {
		title, _ := r.Title.(string)
		address, _ := r.Link.(string)
		content, _ := r.Snippet.(string)
		if keep(&out, title, address, content, q.Count) {
			break
		}
	}
	return Answer{Results: out}, nil
}
