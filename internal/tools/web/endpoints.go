package web

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Endpoints are where an entry of each kind with a default searches when
// it gives no endpoint of its own (P7-07): the web. settings
// web.brave_endpoint, web.exa_endpoint and web.serper_endpoint, each a full
// search address, as agents.web_search_endpoint is Tavily's. SearXNG has no
// default: its entry names its own.
type Endpoints struct {
	Brave  string // web.brave_endpoint
	Exa    string // web.exa_endpoint
	Serper string // web.serper_endpoint
}

// EndpointKeys are the settings ReadOptions reads into Endpoints.
var EndpointKeys = []string{"web.brave_endpoint", "web.exa_endpoint", "web.serper_endpoint"}

// readEndpoints reads the default search addresses. Each is required.
func readEndpoints(get func(key string) (string, bool)) (Endpoints, error) {
	v := map[string]string{}
	for _, key := range EndpointKeys {
		s, ok := get(key)
		if !ok {
			return Endpoints{}, fmt.Errorf("the %s setting is missing", key)
		}
		if s = strings.TrimSpace(s); s == "" {
			return Endpoints{}, fmt.Errorf("%s must be an http or https address", key)
		}
		v[key] = s
	}
	e := Endpoints{Brave: v["web.brave_endpoint"], Exa: v["web.exa_endpoint"], Serper: v["web.serper_endpoint"]}
	return e, e.check()
}

// check checks the addresses that are set. One that is not set leaves its
// kind with no default, so an entry of it must name its own endpoint.
func (e Endpoints) check() error {
	for key, address := range map[string]string{"web.brave_endpoint": e.Brave, "web.exa_endpoint": e.Exa, "web.serper_endpoint": e.Serper} {
		if address == "" {
			continue
		}
		u, err := url.Parse(address)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			return fmt.Errorf("%s must be an http or https address with no user name", key)
		}
	}
	return nil
}

// errNoEndpoint is an entry of a kind whose default address is not set, and
// that names none of its own.
var errNoEndpoint = errors.New("has no endpoint: give the entry one, or set its kind's web. endpoint setting")
