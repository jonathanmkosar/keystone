package isolationdrill

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// lastChecks look over everything once the parts have run: the second
// person's whole folder, database and vault for the first person's marks;
// both audit logs for tokens and keys; and the sign-in door, which is the
// one place a try is recorded in the first person's log, as it should be.
func (g *rig) lastChecks() error {
	// 1. Nothing of the first person's anywhere the second person keeps.
	all, err := g.everything(g.two.id)
	if err != nil {
		return err
	}
	err = g.with(g.two.id, func(s *tenant.Store) error {
		names, err := s.Secrets().Names()
		if err != nil {
			return err
		}
		for _, n := range names {
			v, _, err := s.Secrets().Get(n)
			if err != nil {
				return err
			}
			all["secret "+n] = v
		}
		return nil
	})
	if err != nil {
		return err
	}
	var where []string
	for w := range all {
		where = append(where, w)
	}
	sort.Strings(where)
	files, tables, secretsSeen := 0, 0, 0
	for _, w := range where {
		switch {
		case strings.HasPrefix(w, "file "):
			files++
		case strings.HasPrefix(w, "table "):
			tables++
		default:
			secretsSeen++
		}
		for _, m := range g.allMarks() {
			if strings.Contains(all[w], m) {
				g.report.reached("%s: the second person's %s holds the mark of the first person's %s", AreaDisk, w, g.markName(m))
			}
		}
	}
	g.report.tried(AreaDisk, "every file, table and secret of the second person's, looked through for the first person's marks",
		fmt.Sprintf("%d files, %d tables and %d secrets; %s", files, tables, secretsSeen, map[bool]string{true: "none holds one", false: "see above"}[len(g.report.Reached) == 0]))

	// 2. Both audit logs: no session token, key or token of the first
	// person's in any entry.
	held := []string{g.one.token, g.two.token, g.mark("secret"), g.mark("connector-token"), g.marks["google-refresh"], g.first.shareToken, g.second.shareToken}
	for _, p := range []person{g.one, g.two} {
		var entries []audit.Entry
		err := g.with(p.id, func(s *tenant.Store) (err error) {
			entries, err = audit.List(context.Background(), s, audit.Filter{})
			return err
		})
		if err != nil {
			return err
		}
		for _, e := range entries {
			for _, h := range held {
				if h != "" && strings.Contains(e.Action+e.Sent+e.Received+e.Actor, h) {
					g.report.reached("%s: an entry of %s's log (%s %s) holds a session token, key or token", AreaAudit, p.name, e.Kind, e.Action)
				}
			}
		}
		g.report.tried(AreaAudit, "every entry of "+p.name+"'s audit log, looked through for both session tokens, the first person's key, connector token and Google refresh token, and both share tokens",
			fmt.Sprintf("%d entries, none holds one", len(entries)))
	}

	// 3. The sign-in door. A sign-in names the person it is for, and is
	// recorded in that person's log: a wrong password for the first person
	// belongs in the first person's log, whoever sent it. That is the one
	// change the second person can make there, and it must be that and
	// nothing else.
	before, err := g.look()
	if err != nil {
		return err
	}
	for _, try := range []struct {
		what, id, password, cookie string
	}{
		{"signing in as the first person with the second person's password, sent with the second person's session", g.one.id, passwordTwo, g.two.token},
		{"signing in as the first person with a guessed password", g.one.id, "a guessed phrase for the drill", ""},
	} {
		rec := g.do(call{method: "POST", path: api.Prefix + "session", token: try.cookie, body: jsonOf(api.SignInBody{Tenant: try.id, Password: try.password})})
		if rec.Code != http.StatusUnauthorized {
			g.report.reached("%s: %s answered %d", AreaAPI, try.what, rec.Code)
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == api.CookieName && c.Value != "" {
				g.report.reached("%s: %s set a session cookie", AreaAPI, try.what)
			}
		}
		g.report.tried(AreaAPI, try.what, "refused, "+short(rec)+"; recorded as a refused sign-in in the first person's own log, as every sign-in for them is")
	}
	after, err := g.look()
	if err != nil {
		return err
	}
	changes := before.changes(after)
	want := 0
	for _, c := range changes {
		if strings.HasPrefix(c, "audit log") && strings.Count(c, "api POST /api/v1/session refused") == 2 && strings.Count(c, ";") == 1 {
			want++
			continue
		}
		if strings.HasPrefix(c, "table signin_password") {
			continue // the count of wrong passwords, which makes the person wait
		}
		g.report.reached("%s: a sign-in try changed the first person's %s", AreaAPI, c)
	}
	if want != 1 {
		g.report.reached("%s: the two refused sign-ins are not in the first person's log as two refused sign-ins and nothing else: %s", AreaAudit, strings.Join(changes, "; "))
	}
	return nil
}
