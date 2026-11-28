package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
)

// Brave, Exa and Serper (P7-07) in "no keys in output". Each kind sends its
// key in a header of its own (X-Subscription-Token, x-api-key, X-API-KEY),
// so each has a surface beside Tavily's: the stored value is the key of a
// person's own provider and of an install provider of each kind, and the
// stand-in answers, in each kind's shape, with a result, a refusal, a limit,
// a failure and a result that repeats the key in every field, each asked of
// a fresh provider. SearXNG takes no key, so it has nothing to leak.
func init() {
	keySurfaces = append(keySurfaces, struct {
		name string
		run  func(root, dir, value string) ([]keyOutput, error)
	}{"Brave, Exa and Serper", keysInSearchKinds})
}

// kindStandIn answers searches as Brave, Exa or Serper would, from the words
// searched for, and keeps the last key it was sent in each kind's header.
type kindStandIn struct {
	*httptest.Server
	key atomic.Value
}

func newKindStandIn() *kindStandIn {
	s := &kindStandIn{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Exa's x-api-key and Serper's X-API-KEY are one header to Go.
		key := r.Header.Get("X-Subscription-Token") + r.Header.Get("X-Api-Key")
		s.key.Store(key)
		query := r.URL.Query().Get("q")
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var body struct {
			Query string `json:"query"`
			Q     string `json:"q"`
		}
		_ = json.Unmarshal(raw, &body)
		query += body.Query + body.Q
		kind := strings.Trim(strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)[0], "/")
		title, address, extract := "Harbour Museum", "https://museum.example/hours", "Open from ten."
		switch query {
		case "refused":
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":{"code":"SUBSCRIPTION_TOKEN_INVALID","detail":%q},"message":%q,"tag":"INVALID_API_KEY"}`, key, key)
			return
		case "limit":
			w.Header().Set("Retry-After", "30")
			w.Header().Set("X-RateLimit-Remaining", "0, 10")
			w.Header().Set("X-RateLimit-Reset", "1, 100000")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"message":%q}`, key)
			return
		case "broken":
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, key)
			return
		case "echo":
			title, address, extract = "echo "+key, "https://echo.example/"+url.PathEscape(key), "the key "+key
		}
		w.Header().Set("Content-Type", "application/json")
		switch kind {
		case web.KindBrave:
			fmt.Fprintf(w, `{"web":{"results":[{"title":%q,"url":%q,"description":%q}]}}`, title, address, extract)
		case web.KindExa:
			fmt.Fprintf(w, `{"results":[{"title":%q,"url":%q,"highlights":[%q]}]}`, title, address, extract)
		default:
			fmt.Fprintf(w, `{"organic":[{"title":%q,"link":%q,"snippet":%q}]}`, title, address, extract)
		}
	}))
	return s
}

func keysInSearchKinds(_, dir, value string) ([]keyOutput, error) {
	stand := newKindStandIn()
	defer stand.Close()
	data := filepath.Join(dir, "data")
	in, err := tenant.OpenWithKeeper(data, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		return nil, err
	}
	s, err := in.Store(made.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	inst, err := web.OpenInstall(in)
	if err != nil {
		return nil, err
	}
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	o := c.Web
	o.Install = in
	reg := tools.New()
	if err := web.Add(reg, o); err != nil {
		return nil, err
	}
	ctx := context.Background()
	var out []keyOutput
	sets := []struct {
		group string
		ps    *web.Providers
	}{{web.GroupOwn, web.Own(s, inst)}, {web.GroupInstall, inst.Providers}}
	for _, set := range sets {
		for _, kind := range []string{web.KindBrave, web.KindExa, web.KindSerper} {
			for _, q := range []string{"harbour", "refused", "limit", "broken", "echo"} {
				// The stand-in tells the kinds apart by the first part of the
				// path, which each adapter keeps below the entry's endpoint.
				p := web.Provider{Name: set.group + "-" + kind + "-" + q, Kind: kind, Endpoint: stand.URL + "/" + kind, Enabled: true}
				if err := set.ps.Add(ctx, p, value); err != nil {
					return out, err
				}
				stand.key.Store("")
				res := reg.Run(ctx, tools.Call{Store: s, RequestID: "req-" + p.Name, Actor: "person", ID: "call_" + p.Name, Name: web.SearchName, Input: json.RawMessage(fmt.Sprintf(`{"query":%q}`, q))})
				out = append(out, keyOutput{"the result of the search for " + q + " through " + p.Name, []byte(fmt.Sprintf("%+v", res))})
				// The check is worth nothing unless the key really was sent.
				if stand.key.Load() != value {
					return out, fmt.Errorf("the stand-in %s was not sent the key of %s", kind, p.Name)
				}
				p.Enabled = false
				if err := set.ps.Update(ctx, p); err != nil {
					return out, err
				}
			}
		}
		list, err := set.ps.List(ctx)
		if err != nil {
			return out, err
		}
		raw, _ := json.Marshal(list)
		out = append(out, keyOutput{"the " + set.group + " providers of each kind as the screens get them", raw})
	}
	entries, err := audit.List(ctx, s, audit.Filter{})
	if err != nil {
		return out, err
	}
	out = append(out, keyOutput{"the audit log", []byte(fmt.Sprintf("%+v", entries))})
	files, err := folderFiles(data)
	return append(out, files...), err
}
