package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Exa's adapter (P7-07), as Exa's search reference and error codes document
// them on November 28, 2026 (decision 0751): POST /search with query,
// numResults and the contents wanted, the key in x-api-key, and the results
// in results, each with title and url and the contents asked for. Keystone
// asks for highlights, cut at agents.web_search_snippet_chars, and reads the
// summary or the text when there are none. An error answer's tag says why:
// 402 is spent credits or a spent budget, 403 a key without access or a
// query a content policy refused, 429 a rate limit.

func init() {
	register(Kind{Name: KindExa, Title: "Exa", New: newExa})
}

// exaSearchPath is the search's path below an entry's endpoint.
const exaSearchPath = "/search"

// newExa makes Exa's adapter for one entry: at web.exa_endpoint, or at the
// entry's endpoint with /search.
func newExa(p Provider, o Options) Adapter {
	e := Exa{Endpoint: o.Endpoints.Exa, SnippetChars: o.SnippetChars, Timeout: o.SearchTimeout, MaxBytes: o.SearchMaxBytes}
	if p.Endpoint != "" {
		e.Endpoint = searchAddress(p.Endpoint, "", exaSearchPath)
	}
	return e
}

// Exa searches with Exa's search API.
type Exa struct {
	// Endpoint is the full search address: web.exa_endpoint, or an entry's
	// endpoint with /search.
	Endpoint string
	// SnippetChars is the most characters of highlights asked for:
	// agents.web_search_snippet_chars. Zero leaves it to Exa.
	SnippetChars int
	// Timeout bounds one search: agents.web_search_timeout.
	Timeout time.Duration
	// MaxBytes is the most of an answer read: agents.web_search_max_bytes.
	MaxBytes int64
	// Client sends the request. Nil means one of its own, which follows no
	// redirect.
	Client *http.Client
}

func (e Exa) sender() sender {
	return sender{title: "Exa", timeout: e.Timeout, maxBytes: e.MaxBytes, client: e.Client}
}

type exaRequest struct {
	Query      string      `json:"query"`
	NumResults int         `json:"numResults"`
	Contents   exaContents `json:"contents"`
}

type exaContents struct {
	Highlights any `json:"highlights"`
}

type exaAnswer struct {
	Results []struct {
		Title      any   `json:"title"`
		URL        any   `json:"url"`
		Highlights []any `json:"highlights"`
		Summary    any   `json:"summary"`
		Text       any   `json:"text"`
	} `json:"results"`
}

// exaContentRefusals are the tags of a 403 that refuse the query itself,
// not the key: Exa's content policy.
var exaContentRefusals = []string{"PROHIBITED_CONTENT", "CONTENT_FILTER_ERROR"}

// Search implements Adapter.
func (e Exa) Search(ctx context.Context, key string, q Query) (Answer, error) {
	s := e.sender()
	if e.Endpoint == "" {
		return Answer{}, &Failure{Why: WhyUnavailable, Text: "the Exa provider " + errNoEndpoint.Error()}
	}
	var highlights any = true
	if e.SnippetChars > 0 {
		highlights = map[string]int{"maxCharacters": e.SnippetChars}
	}
	body, err := json.Marshal(exaRequest{Query: q.Text, NumResults: max(q.Count, 1), Contents: exaContents{Highlights: highlights}})
	if err != nil {
		return Answer{}, &Failure{Why: WhyMalformed, Text: "Keystone could not write the search request"}
	}
	req, err := http.NewRequest(http.MethodPost, e.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Answer{}, s.endpointFailure()
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("Content-Type", "application/json")
	rep, err := s.do(ctx, req)
	if err != nil {
		return Answer{}, err
	}
	tag := strings.ToUpper(errorField(rep.body, "tag"))
	switch rep.status {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return Answer{}, s.refused()
	case http.StatusPaymentRequired:
		if strings.HasPrefix(tag, "X402_") || strings.HasPrefix(tag, "MPP_") {
			// Exa asks to be paid per request: it did not take the key.
			return Answer{}, s.refused()
		}
		return Answer{}, &Failure{Why: WhyQuota, Text: "Exa says the credits or the budget are spent"}
	case http.StatusForbidden:
		if contains(exaContentRefusals, tag) {
			return Answer{}, &Failure{Why: WhyMalformed, Text: fmt.Sprintf("Exa refused the query under its content policy (it answered with status %d)", rep.status)}
		}
		return Answer{}, s.refused()
	case http.StatusTooManyRequests:
		return Answer{}, &Failure{Why: WhyRate, Wait: retryAfter(rep.header, time.Now()), Text: "Exa says a rate limit was reached: too many requests for now"}
	case http.StatusBadRequest:
		return Answer{}, s.malformed(rep.status)
	default:
		return Answer{}, s.unavailable(rep.status)
	}
	var answer exaAnswer
	if err := json.Unmarshal(rep.body, &answer); err != nil {
		return Answer{}, s.unreadable()
	}
	var out []Result
	for _, r := range answer.Results {
		title, _ := r.Title.(string)
		address, _ := r.URL.(string)
		var parts []string
		for _, h := range r.Highlights {
			if text, ok := h.(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		}
		content := strings.Join(parts, " … ")
		if content == "" {
			content, _ = r.Summary.(string)
		}
		if strings.TrimSpace(content) == "" {
			content, _ = r.Text.(string)
		}
		if keep(&out, title, address, content, q.Count) {
			break
		}
	}
	return Answer{Results: out}, nil
}
