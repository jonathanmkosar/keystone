package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
)

// Search providers (P7-06) in "no keys in output".
//
// keysInSearchProviders stores value as the key of a person's own search
// provider, in their vault, and of an install provider, in the install
// vault (data/admin/secrets.bin). Each of the stand-in's answers (a result,
// a refusal, a limit, a failure, and a result that repeats the key in every
// field) is asked of a fresh provider of each kind, so a refused or cooling
// one never hides the next. It returns every result, what the provider
// lists show, the audit log and every file in the data folder, data/admin/
// with it. Nothing leaves the machine: the stand-in is served on it.
func keysInSearchProviders(_, dir, value string) ([]keyOutput, error) {
	stand := newWebStandIn()
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
		for _, q := range []string{"harbour", "refused", "limit", "broken", "echo"} {
			p := web.Provider{Name: set.group + "-" + q, Kind: web.KindTavily, Endpoint: stand.URL, Enabled: true}
			if err := set.ps.Add(ctx, p, value); err != nil {
				return out, err
			}
			stand.auth.Store("")
			res := reg.Run(ctx, tools.Call{Store: s, RequestID: "req-" + p.Name, Actor: "person", ID: "call_" + p.Name, Name: web.SearchName, Input: json.RawMessage(fmt.Sprintf(`{"query":%q}`, q))})
			out = append(out, keyOutput{"the result of the search for " + q + " through " + p.Name, []byte(fmt.Sprintf("%+v", res))})
			// The check is worth nothing unless the key really was sent.
			if stand.auth.Load() != "Bearer "+value {
				return out, fmt.Errorf("the stand-in search provider was not sent the key of %s", p.Name)
			}
			p.Enabled = false
			if err := set.ps.Update(ctx, p); err != nil {
				return out, err
			}
		}
		list, err := set.ps.List(ctx)
		if err != nil {
			return out, err
		}
		raw, _ := json.Marshal(list)
		out = append(out, keyOutput{"the " + set.group + " providers as the screens get them", raw},
			keyOutput{"the " + set.group + " providers, printed", []byte(fmt.Sprintf("%+v %#v", list, set.ps))})
		out = append(out, keyOutput{"the error for a key set on a provider that is not there", []byte(fmt.Sprint(set.ps.SetKey(ctx, "absent", value)))})
	}
	entries, err := audit.List(ctx, s, audit.Filter{})
	if err != nil {
		return out, err
	}
	out = append(out, keyOutput{"the audit log", []byte(fmt.Sprintf("%+v", entries))})
	files, err := folderFiles(data)
	return append(out, files...), err
}
