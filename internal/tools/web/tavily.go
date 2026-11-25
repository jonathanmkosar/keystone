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
	"strings"
	"time"
)

// Searcher is a web search provider. Tavily is the first.
type Searcher interface {
	// Search sends query to the provider with the person's key and returns
	// at most count results, best first. An error's text is written for
	// the model and the person to read, and never holds the key.
	Search(ctx context.Context, key, query string, count int) ([]Result, error)
}

// Result is one web search result.
type Result struct {
	Title   string
	URL     string
	Content string
}

// Tavily searches with Tavily's search API, in the shape New Keystone
// used: a basic search by default, no generated answer, no raw page text,
// and the parameters as sent (auto_parameters off).
type Tavily struct {
	// Endpoint is the search address: agents.web_search_endpoint.
	Endpoint string
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

// Search implements Searcher.
func (t Tavily) Search(ctx context.Context, key, query string, count int) ([]Result, error) {
	body, err := json.Marshal(tavilyRequest{Query: query, SearchDepth: t.Depth, MaxResults: count})
	if err != nil {
		return nil, errors.New("Keystone could not write the search request")
	}
	if t.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, bytes.NewReader(body))
	if err != nil {
		// The endpoint is a setting, and a setting that is not an address
		// is the install's fault. The error names no key.
		return nil, errors.New("the web search address in agents.web_search_endpoint is not an address Keystone can call")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	client := t.Client
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return nil, fmt.Errorf("Tavily did not answer within %s, so the search has no results. Try again later", t.Timeout)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Keystone could not reach Tavily, so the search has no results. Try again later")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, errors.New("Tavily refused the key stored as " + KeyName + ". Check the key, and store it again if it changed")
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 432 || resp.StatusCode == 433:
		return nil, errors.New("Tavily says a usage or rate limit was reached, so the search has no results. Try again later")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("Tavily is unavailable (it answered with status %d), so the search has no results. Try again later", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, t.MaxBytes+1))
	if err != nil {
		return nil, errors.New("Keystone could not read Tavily's answer, so the search has no results")
	}
	if int64(len(raw)) > t.MaxBytes {
		return nil, fmt.Errorf("Tavily's answer was larger than the %d bytes Keystone reads (agents.web_search_max_bytes), so it was not used", t.MaxBytes)
	}
	var answer tavilyAnswer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, errors.New("Keystone could not read Tavily's answer, so the search has no results")
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
		if len(out) == count {
			break
		}
	}
	return out, nil
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
