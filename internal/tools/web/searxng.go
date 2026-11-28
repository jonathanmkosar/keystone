package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/jonathanmkosar/keystone/internal/models"
)

// SearXNG's adapter (P7-07), as SearXNG's search API documents it on
// November 28, 2026 (decision 0753): GET /search with q and format=json,
// no key, and the results in results, each with title, url and content.
// JSON must be switched on in the instance's settings.yml (search.formats),
// or it answers 403; its limiter answers 429.
//
// A SearXNG runs on the owner's machine. Its endpoint must be this machine
// or the home network by the address rule of a local model (decision 0051,
// models.OnHomeNetwork), or the admin marks the install's entry trusted.
// One on this machine or the home network is called without the
// environment's proxy, as a local model is. Its searches still leave the
// house, so web tools stay off in a held chat whichever provider answers
// (decision 0252).

func init() {
	register(Kind{Name: KindSearXNG, Title: "SearXNG", NoKey: true, Unmetered: true, New: newSearXNG, CheckEntry: checkSearXNG})
}

// searxngSearchPath is the search's path below an entry's endpoint.
const searxngSearchPath = "/search"

// errSearXNGAddress is a SearXNG entry whose endpoint is neither local nor
// trusted.
var errSearXNGAddress = errors.New("a SearXNG's endpoint must be on this machine or the home network (localhost, a name with no dot, a name ending in .local, .lan, .home.arpa, .internal or .localhost, or a loopback, private, link-local or 100.64.0.0/10 address), or an install entry the admin marks trusted")

// searxngLocal reports whether an endpoint is on this machine or the home
// network, from its address alone.
func searxngLocal(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && u.Host != "" && models.OnHomeNetwork(u.Hostname())
}

// checkSearXNG is a SearXNG entry's own check: it names its endpoint, and
// the endpoint is local or the entry is trusted.
func checkSearXNG(p Provider) error {
	switch {
	case p.Endpoint == "":
		return errors.New("a SearXNG provider needs its endpoint: the address of the SearXNG, on this machine or the home network")
	case !searxngLocal(p.Endpoint) && !p.Trusted:
		return errSearXNGAddress
	}
	return nil
}

// newSearXNG makes SearXNG's adapter for one entry, at its endpoint with
// /search.
func newSearXNG(p Provider, o Options) Adapter {
	local := searxngLocal(p.Endpoint)
	return SearXNG{Endpoint: searchAddress(p.Endpoint, "", searxngSearchPath), Local: local, Allowed: p.Endpoint != "" && (local || p.Trusted),
		Timeout: o.SearchTimeout, MaxBytes: o.SearchMaxBytes}
}

// SearXNG searches with a SearXNG instance's search API.
type SearXNG struct {
	// Endpoint is the full search address: an entry's endpoint with
	// /search.
	Endpoint string
	// Local says the endpoint is on this machine or the home network, so
	// the environment's proxy is not used.
	Local bool
	// Allowed says the endpoint is local or trusted. An entry that is
	// neither, which its check refuses, sends nothing.
	Allowed bool
	// Timeout bounds one search: agents.web_search_timeout.
	Timeout time.Duration
	// MaxBytes is the most of an answer read: agents.web_search_max_bytes.
	MaxBytes int64
	// Client sends the request. Nil means one of its own, which follows no
	// redirect.
	Client *http.Client
}

func (x SearXNG) sender() sender {
	return sender{title: "SearXNG", timeout: x.Timeout, maxBytes: x.MaxBytes, client: x.Client, direct: x.Local}
}

type searxngAnswer struct {
	Results []struct {
		Title   any `json:"title"`
		URL     any `json:"url"`
		Content any `json:"content"`
	} `json:"results"`
	Unresponsive []any `json:"unresponsive_engines"`
}

// Search implements Adapter. key is empty: a SearXNG takes none.
func (x SearXNG) Search(ctx context.Context, _ string, q Query) (Answer, error) {
	s := x.sender()
	if !x.Allowed {
		return Answer{}, &Failure{Why: WhyUnavailable, Text: "this SearXNG's address is neither on this machine or the home network nor marked trusted, so nothing was sent to it"}
	}
	u, err := url.Parse(x.Endpoint)
	if err != nil || u.Host == "" {
		return Answer{}, s.endpointFailure()
	}
	values := u.Query()
	values.Set("q", q.Text)
	values.Set("format", "json")
	u.RawQuery = values.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return Answer{}, s.endpointFailure()
	}
	// SearXNG's bot detection, when its limiter is on, refuses a request
	// with no Accept-Language as a bot's.
	req.Header.Set("Accept-Language", "en")
	rep, err := s.do(ctx, req)
	if err != nil {
		return Answer{}, err
	}
	switch rep.status {
	case http.StatusOK:
	case http.StatusForbidden:
		// A format the instance has not switched on. Not a refused key: a
		// SearXNG has none, so it could never come back from refused.
		return Answer{}, &Failure{Why: WhyUnavailable, Text: "SearXNG would not answer in JSON (status 403): add json to search.formats in its settings.yml"}
	case http.StatusTooManyRequests:
		return Answer{}, &Failure{Why: WhyRate, Wait: retryAfter(rep.header, time.Now()), Text: "SearXNG's limiter says too many requests for now"}
	case http.StatusBadRequest:
		return Answer{}, s.malformed(rep.status)
	default:
		return Answer{}, s.unavailable(rep.status)
	}
	var answer searxngAnswer
	if err := json.Unmarshal(rep.body, &answer); err != nil {
		return Answer{}, s.unreadable()
	}
	if len(answer.Results) == 0 && len(answer.Unresponsive) > 0 {
		// Every engine it asked failed: the instance is up, but cannot
		// search. The search moves on.
		return Answer{}, &Failure{Why: WhyUnavailable, Text: fmt.Sprintf("SearXNG answered with no results, and %d of its engines did not answer", len(answer.Unresponsive))}
	}
	var out []Result
	for _, r := range answer.Results {
		title, _ := r.Title.(string)
		address, _ := r.URL.(string)
		content, _ := r.Content.(string)
		if keep(&out, title, address, content, q.Count) {
			break
		}
	}
	return Answer{Results: out}, nil
}
