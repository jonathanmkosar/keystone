package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/adminapi"
	"github.com/jonathanmkosar/keystone/internal/adminview"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The admin mark and step-up (P15-01): the admin view and the admin routes.
func init() {
	componentDocs["internal/adminview"] = "docs/architecture/admin.md"
	componentDocs["internal/adminapi"] = "docs/architecture/admin.md"
	networkAllowed["internal/adminapi"] = "the admin routes' handlers, served by the server; it opens no connection"
}

// stepUpRoutes is every admin route that needs the admin mark and not a
// fresh step-up, and why. A route marked StepUp in the code and not listed
// here fails the admin isolation check, so the admin door cannot be widened
// in one place only (decision 0642).
var stepUpRoutes = map[string]string{
	"POST /api/v1/admin/step-up":         "proving the password again is how the step-up begins",
	"POST /api/v1/admin/step-up/passkey": "the passkey prompt the step-up is answered with",
}

const keystoneModule = "github.com/jonathanmkosar/keystone/"

// adminMayNotReach are the packages that hold a person's content: chats,
// titles, messages, files, memory, traces and error words. The admin view
// and the admin routes reach none of them, directly or through another
// (decision 0645). The first eleven are the spec's list; the rest hold a
// person's words too. A path ending in "/" stands for a folder and all
// below it.
var adminMayNotReach = []string{
	"internal/chat",
	"internal/turn",
	"internal/contextbuild",
	"internal/knowledge/",
	"internal/uploads",
	"internal/artifacts",
	"internal/projects",
	"internal/recap",
	"internal/inspector",
	"internal/share",
	"internal/shareapi",

	"internal/attach",
	"internal/compact",
	"internal/imported",
	"internal/importer/",
	"internal/jobs",
	"internal/schedules",
	"internal/subagents/",
	"internal/research/",
	"internal/wikijobs/",
	"internal/styles",
	"internal/visuals",
	"internal/embedding",
	"internal/connectors",
	"internal/mcp/",
	"internal/google/",
	"internal/operations",
	"internal/tools/",
}

// forbidden reports whether an import path is one adminMayNotReach names.
func forbidden(path string) bool {
	rel, ok := strings.CutPrefix(path, keystoneModule)
	if !ok {
		return false
	}
	for _, f := range adminMayNotReach {
		if dir, isDir := strings.CutSuffix(f, "/"); isDir {
			if rel == dir || strings.HasPrefix(rel, f) {
				return true
			}
		} else if rel == f {
			return true
		}
	}
	return false
}

// checkAdminIsolation is the standing check "admin isolation" (P15-01). An
// admin never reads another person's chats, titles, files, memory, traces
// or error words, and the shape of the code is what holds it: the admin
// view and the admin routes cannot reach the packages that keep them, the
// routes are not given the install, only the command line can set the
// mark, and the door refuses every admin route to a person without the mark
// and to an admin without a fresh step-up. P15-05 adds the drill: marker
// words in a second person's content, looked for in every admin reply.
func checkAdminIsolation(root string) error {
	var problems []string
	for _, p := range []func(string) ([]string, error){adminImports, adminWriters, adminDoorWalk} {
		got, err := p(root)
		if err != nil {
			return fmt.Errorf("the admin isolation check could not run: %w", err)
		}
		problems = append(problems, got...)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// adminImports holds the imports: adminview and adminapi reach no package
// that holds content, and adminapi does not import the tenant registry.
func adminImports(root string) ([]string, error) {
	var problems []string
	for _, pkg := range []string{"internal/adminview", "internal/adminapi"} {
		out, err := project.Run(root, "go", "list", "-deps", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", "./"+pkg)
		if err != nil {
			return nil, fmt.Errorf("go list %s: %v\n%s", pkg, err, out)
		}
		imports := map[string][]string{}
		for _, line := range strings.Split(out, "\n") {
			if fields := strings.Fields(line); len(fields) > 0 {
				imports[fields[0]] = fields[1:]
			}
		}
		var reached []string
		for path := range imports {
			if forbidden(path) {
				reached = append(reached, path)
			}
		}
		sort.Strings(reached)
		for _, path := range reached {
			problems = append(problems, fmt.Sprintf("%s reaches %s, which holds a person's content: %s", pkg, strings.TrimPrefix(path, keystoneModule), chain(imports, keystoneModule+pkg, path)))
		}
	}
	named, err := importerImports(filepath.Join(root, "internal", "adminapi"))
	if err != nil {
		return nil, err
	}
	for _, path := range named {
		if path == keystoneModule+"internal/tenant" {
			problems = append(problems, "internal/adminapi imports internal/tenant: the admin routes are given the admin view, never the install (decision 0644)")
		}
	}
	return problems, nil
}

// chain finds one line of imports from one package to another, for the
// message.
func chain(imports map[string][]string, from, to string) string {
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		if at == to {
			var path []string
			for p := to; p != ""; p = prev[p] {
				path = append([]string{strings.TrimPrefix(p, keystoneModule)}, path...)
			}
			return strings.Join(path, " -> ")
		}
		for _, next := range imports[at] {
			if _, seen := prev[next]; !seen {
				prev[next] = at
				queue = append(queue, next)
			}
		}
	}
	return strings.TrimPrefix(to, keystoneModule)
}

// adminWriters holds the one writer of the mark: no Go file outside
// internal/tenant and tools/tenant calls SetAdmin, tests left out.
func adminWriters(root string) ([]string, error) {
	var problems []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if project.Skip(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/tenant/") || strings.HasPrefix(rel, "tools/tenant/") || rel == "tools/verify/adminisolation.go" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), ".SetAdmin(") {
			problems = append(problems, rel+" calls SetAdmin: only the command line (tools/tenant) gives or takes the admin mark (decision 0640)")
		}
		return nil
	})
	return problems, err
}

// Invented passwords, used in a throwaway folder only.
const (
	adminWalkPassword  = "walk admin's own phrase"
	personWalkPassword = "walk person's own phrase"
)

// adminDoorWalk walks the door with the real handler on a throwaway folder.
func adminDoorWalk(string) ([]string, error) {
	dir, err := os.MkdirTemp("", "keystone-admin-isolation-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.Open(dir)
	if err != nil {
		return nil, err
	}
	o, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	made := map[string]string{}
	for name, password := range map[string]string{"Walk Admin": adminWalkPassword, "Walk Person": personWalkPassword} {
		t, err := in.Create(name)
		if err != nil {
			return nil, err
		}
		made[name] = t.ID
		s, err := in.Store(t.ID)
		if err != nil {
			return nil, err
		}
		err = signin.SetPassword(s, password, o)
		s.Close()
		if err != nil {
			return nil, err
		}
	}
	admin, person := made["Walk Admin"], made["Walk Person"]
	if _, err := in.SetAdmin(admin, true); err != nil {
		return nil, err
	}
	w, err := newWalker(in)
	if err != nil {
		return nil, err
	}
	var problems []string

	// The step-up routes in the code are the listed ones, and every admin
	// route is under the prefix.
	var adminRoutes []api.Route
	listed := map[string]bool{}
	for _, rt := range w.routes {
		if !rt.Admin {
			continue
		}
		adminRoutes = append(adminRoutes, rt)
		if rt.StepUp {
			listed[routeName(rt)] = true
			if _, ok := stepUpRoutes[routeName(rt)]; !ok {
				problems = append(problems, routeName(rt)+" opens without a fresh step-up and is not on the list in tools/verify/adminisolation.go")
			}
		}
	}
	for name := range stepUpRoutes {
		if !listed[name] {
			return nil, fmt.Errorf("%s is listed as a step-up route in tools/verify/adminisolation.go and is not one: take it off the list", name)
		}
	}
	if len(adminRoutes) == 0 {
		return nil, errors.New("the API has no admin route, so the walk proved nothing")
	}

	logLen := func(id string) (int, error) {
		s, err := in.Store(id)
		if err != nil {
			return 0, err
		}
		defer s.Close()
		got, err := audit.List(context.Background(), s, audit.Filter{})
		return len(got), err
	}
	entriesOf := func(id, requestID string) ([]audit.Entry, error) {
		s, err := in.Store(id)
		if err != nil {
			return nil, err
		}
		defer s.Close()
		return audit.List(context.Background(), s, audit.Filter{RequestID: requestID})
	}
	codeOf := func(rec *httptest.ResponseRecorder) string {
		var e api.ErrorBody
		json.Unmarshal(rec.Body.Bytes(), &e)
		return e.Code
	}
	// refusedEverywhere calls every admin route, and a path under the prefix
	// that is no route, and wants each refused with status and code, in the
	// caller's own log as actor and in the other's not at all.
	type walkCase struct {
		what, token, caller, other, actor, code string
		status                                  int
		skipStepUp                              bool
	}
	refusedEverywhere := func(c walkCase) error {
		paths := []struct{ method, path, name string }{{"GET", api.AdminPrefix + "no/such/route", "a path under the prefix that is no route"}}
		for _, rt := range adminRoutes {
			if c.skipStepUp && rt.StepUp {
				continue
			}
			paths = append(paths, struct{ method, path, name string }{rt.Method, routePart.ReplaceAllString(rt.Path, "x"), routeName(rt)})
		}
		for _, p := range paths {
			otherBefore, err := logLen(c.other)
			if err != nil {
				return err
			}
			rec := w.request(p.method, p.path, c.token, `{"password":"`+adminWalkPassword+`"}`, nil)
			if rec.Code != c.status || codeOf(rec) != c.code {
				problems = append(problems, fmt.Sprintf("%s answered %s with %d %s, want %d %s", p.name, c.what, rec.Code, codeOf(rec), c.status, c.code))
			}
			got, err := entriesOf(c.caller, rec.Header().Get("X-Request-Id"))
			if err != nil {
				return err
			}
			if len(got) != 2 || got[0].Actor != c.actor || got[1].Status != rec.Code {
				problems = append(problems, fmt.Sprintf("%s, refused to %s, is in the caller's log as %d entries (want 2, actor %s)", p.name, c.what, len(got), c.actor))
			}
			if n, err := logLen(c.other); err != nil {
				return err
			} else if n != otherBefore {
				problems = append(problems, fmt.Sprintf("%s, refused to %s, wrote in the other person's log", p.name, c.what))
			}
		}
		return nil
	}

	personToken, rec := w.signIn(person, personWalkPassword)
	if personToken == "" {
		return nil, fmt.Errorf("the walk's person cannot sign in: status %d", rec.Code)
	}
	adminToken, rec := w.signIn(admin, adminWalkPassword)
	if adminToken == "" {
		return nil, fmt.Errorf("the walk's admin cannot sign in: status %d", rec.Code)
	}

	// 1. A person without the mark, on every admin route, the step-up's too.
	if err := refusedEverywhere(walkCase{what: "a person without the mark", token: personToken, caller: person, other: admin,
		actor: api.ActorPerson, code: "not_admin", status: http.StatusForbidden}); err != nil {
		return nil, err
	}
	// 2. An admin without a step-up, on every admin route but the step-up's.
	if err := refusedEverywhere(walkCase{what: "an admin without a step-up", token: adminToken, caller: admin, other: person,
		actor: api.ActorAdmin, code: "step_up", status: http.StatusUnauthorized, skipStepUp: true}); err != nil {
		return nil, err
	}
	// 3. A wrong password at the step-up is refused, and recorded.
	rec = w.request("POST", api.AdminPrefix+"step-up", adminToken, `{"password":"`+personWalkPassword+`"}`, nil)
	if rec.Code != http.StatusForbidden || codeOf(rec) != "refused" {
		problems = append(problems, fmt.Sprintf("a step-up with the other person's password answered %d %s", rec.Code, codeOf(rec)))
	}
	if got, err := entriesOf(admin, rec.Header().Get("X-Request-Id")); err != nil {
		return nil, err
	} else if len(got) != 2 || got[1].Outcome != audit.OutcomeRefused {
		problems = append(problems, "a refused step-up is not in the admin's log as refused")
	}
	// 4. A good step-up opens the people route, which is in both logs.
	rec = w.request("POST", api.AdminPrefix+"step-up", adminToken, `{"password":"`+adminWalkPassword+`"}`, nil)
	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("the walk's admin cannot step up: status %d %s", rec.Code, codeOf(rec))
	}
	rec = w.request("GET", api.AdminPrefix+"people", adminToken, "", nil)
	var people adminapi.PeopleReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &people) != nil || len(people.People) != 2 {
		problems = append(problems, fmt.Sprintf("after a step-up the people route answered %d", rec.Code))
	}
	id := rec.Header().Get("X-Request-Id")
	mine, err := entriesOf(admin, id)
	if err != nil {
		return nil, err
	}
	theirs, err := entriesOf(person, id)
	if err != nil {
		return nil, err
	}
	if len(mine) != 2 || mine[0].Actor != api.ActorAdmin {
		problems = append(problems, fmt.Sprintf("the admin's read of people is in the admin's log as %d entries", len(mine)))
	}
	if len(theirs) != 2 || theirs[0].Actor != adminview.ActorPrefix+admin || theirs[1].Outcome != audit.OutcomeOK {
		problems = append(problems, fmt.Sprintf("the admin's read of people is in the other person's log as %d entries, want 2 with the actor %s", len(theirs), adminview.ActorPrefix+admin))
	}
	// 5. Closed, the step-up opens nothing again.
	if rec := w.request("DELETE", api.AdminPrefix+"step-up", adminToken, "", nil); rec.Code != http.StatusOK {
		return nil, fmt.Errorf("the walk's admin cannot close the admin area: status %d", rec.Code)
	}
	if err := refusedEverywhere(walkCase{what: "an admin whose step-up was closed", token: adminToken, caller: admin, other: person,
		actor: api.ActorAdmin, code: "step_up", status: http.StatusUnauthorized, skipStepUp: true}); err != nil {
		return nil, err
	}
	// 6. The mark taken away by another process, as the command line does,
	// after a fresh step-up: refused at once, with no restart.
	if rec := w.request("POST", api.AdminPrefix+"step-up", adminToken, `{"password":"`+adminWalkPassword+`"}`, nil); rec.Code != http.StatusOK {
		return nil, fmt.Errorf("the walk's admin cannot step up again: status %d", rec.Code)
	}
	command, err := tenant.Open(dir)
	if err != nil {
		return nil, err
	}
	if _, err := command.SetAdmin(admin, false); err != nil {
		return nil, err
	}
	if err := refusedEverywhere(walkCase{what: "a former admin with a fresh step-up", token: adminToken, caller: admin, other: person,
		actor: api.ActorPerson, code: "not_admin", status: http.StatusForbidden}); err != nil {
		return nil, err
	}
	return problems, nil
}
