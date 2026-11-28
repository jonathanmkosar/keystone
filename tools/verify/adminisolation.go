package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/adminapi"
	"github.com/jonathanmkosar/keystone/internal/adminview"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The admin account (P15-01): the admin database, the admin view, the
// admin's routes and the command that makes the accounts.
func init() {
	componentDocs["internal/adminview"] = "docs/architecture/admin.md"
	componentDocs["internal/adminapi"] = "docs/architecture/admin.md"
	componentDocs["internal/adminaccount"] = "docs/architecture/admin.md"
	componentDocs["tools/admin"] = "docs/architecture/admin.md"
	networkAllowed["internal/adminapi"] = "the admin routes' handlers, served by the server; it opens no connection"
}

// adminDoorRoutes is every admin route served before an admin has signed
// in, and why. Each is also a door route in routes.go. A route marked Admin
// and Door in the code and not listed here fails the admin isolation check
// (decision 0655).
var adminDoorRoutes = map[string]string{
	"POST /api/v1/admin/session":         "signing in to an admin account is how its session begins",
	"POST /api/v1/admin/session/passkey": "the passkey prompt an admin signs in with",
}

const keystoneModule = "github.com/jonathanmkosar/keystone/"

// adminMayNotReach are the packages that hold a person's content: chats,
// titles, messages, files, artifacts, memory, traces and error words. The
// admin view, the admin routes and the admin database reach none of them,
// directly or through another (decisions 0645 and 0655). The first eleven
// are the spec's list; the rest hold a person's words too. A path ending in
// "/" stands for a folder and all below it.
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

// adminPackages are the packages the import rules hold.
var adminPackages = []string{"internal/adminview", "internal/adminapi", "internal/adminaccount"}

// adminRouteWords are path parts no admin route may have: an admin account
// has no chat, file or model route (the spec's proof).
var adminRouteWords = []string{"chats", "messages", "turns", "files", "uploads", "artifacts", "models", "memory", "projects",
	"share", "shared", "inspector", "recap", "research", "subagents", "visuals", "browser", "connectors"}

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
// admin is a separate account that sees numbers and settings, never a
// person's data or logs. The shape of the code holds it: the admin side
// cannot reach the packages that keep a person's content or read a
// person's log, the admin routes are not given the install, accounts are
// made from the command line alone, the person's registry has no admin
// mark, and no admin route is a chat, file or model route. The door holds
// the rest, walked with the real handler: a person's token is refused on
// every admin route and an admin's on every person's route, and every admin
// call is in the admin audit log, and in each person's log it read.
// P15-05 adds the drill: marker words in a second person's content, looked
// for in every admin reply and in the server's log.
func checkAdminIsolation(root string) error {
	var problems []string
	for _, p := range []func(string) ([]string, error){adminImports, adminNoLogReads, adminWriters, adminNoMark, adminDoorWalk} {
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

// adminImports holds the imports: the admin packages reach no package that
// holds content, and adminapi does not import the tenant registry.
func adminImports(root string) ([]string, error) {
	var problems []string
	for _, pkg := range adminPackages {
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
		if _, ok := imports[keystoneModule+pkg]; !ok {
			return nil, fmt.Errorf("go list %s did not list the package itself", pkg)
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

// goFiles calls f with every Go file under root that is not a test, by its
// path from root with forward slashes, and its text.
func goFiles(root string, f func(rel, text string)) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
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
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		f(filepath.ToSlash(rel), string(raw))
		return nil
	})
}

// adminNoLogReads holds that the admin cannot read a person's log: the
// admin view and the admin routes never list a tenant's audit log.
func adminNoLogReads(root string) ([]string, error) {
	var problems []string
	err := goFiles(root, func(rel, text string) {
		if !strings.HasPrefix(rel, "internal/adminview/") && !strings.HasPrefix(rel, "internal/adminapi/") {
			return
		}
		for _, call := range []string{"audit.List(", "audit.ListByAdmin("} {
			if strings.Contains(text, call) {
				problems = append(problems, rel+" calls "+strings.TrimSuffix(call, "(")+": an admin never reads a person's audit log (decision 0655)")
			}
		}
	})
	return problems, err
}

// adminWriters holds that accounts are made and removed from the command
// line alone: no Go file outside internal/adminaccount and tools/admin
// calls AddAccount or RemoveAccount, tests and this check left out.
func adminWriters(root string) ([]string, error) {
	var problems []string
	err := goFiles(root, func(rel, text string) {
		if strings.HasPrefix(rel, "internal/adminaccount/") || strings.HasPrefix(rel, "tools/admin/") || rel == "tools/verify/adminisolation.go" {
			return
		}
		for _, call := range []string{".AddAccount(", ".RemoveAccount("} {
			if strings.Contains(text, call) {
				problems = append(problems, rel+" calls "+strings.Trim(call, ".(")+": only the command line (tools/admin) makes or removes an admin account (decision 0650)")
			}
		}
	})
	return problems, err
}

// adminNoMark holds that the person's registry has no admin mark: the first
// pass's mark on tenant.Tenant is gone, and does not come back (decision
// 0653).
func adminNoMark(string) ([]string, error) {
	var problems []string
	t := reflect.TypeOf(tenant.Tenant{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if strings.Contains(strings.ToLower(f.Name), "admin") || strings.Contains(strings.ToLower(name), "admin") {
			problems = append(problems, "tenant.Tenant has the field "+f.Name+": an admin is a separate account, and a person carries no admin mark (decision 0653)")
		}
	}
	return problems, nil
}

// Invented passwords, used in a throwaway folder only.
const (
	adminWalkPassword  = "walk admin's own phrase"
	personWalkPassword = "walk person's own phrase"
	otherWalkPassword  = "walk other person's phrase"
)

var adminTokenShape = regexp.MustCompile(`ksa_[a-f0-9]{64}`)

// adminWalk is the walk's state: the handler, the people, the admin.
type adminWalk struct {
	w        *walker
	dir      string
	in       *tenant.Install
	one, two string // the two people's tenant ids
	account  string // the admin account's id
	bodies   bytes.Buffer
	problems []string
}

func (a *adminWalk) fail(format string, args ...any) {
	a.problems = append(a.problems, fmt.Sprintf(format, args...))
}

// call makes one call with either cookie, or both, and keeps the body to
// look for tokens in later.
func (a *adminWalk) call(method, path, personCookie, adminCookie, body string) *httptest.ResponseRecorder {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if personCookie != "" {
		r.AddCookie(&http.Cookie{Name: api.CookieName, Value: personCookie})
	}
	if adminCookie != "" {
		r.AddCookie(&http.Cookie{Name: api.AdminCookieName, Value: adminCookie})
	}
	rec := httptest.NewRecorder()
	a.w.h.ServeHTTP(rec, r.WithContext(ctx))
	a.bodies.Write(rec.Body.Bytes())
	return rec
}

// adminSignIn signs the admin in at its door and returns the token from the
// admin cookie, checking the cookie on the way.
func (a *adminWalk) adminSignIn(account, password string) (string, *httptest.ResponseRecorder) {
	body, _ := json.Marshal(adminapi.SignInBody{Account: account, Password: password})
	rec := a.call("POST", api.AdminPrefix+"session", "", "", string(body))
	for _, c := range rec.Result().Cookies() {
		if c.Name == api.AdminCookieName && rec.Code == http.StatusOK {
			if c.Path != api.AdminPrefix || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				a.fail("the admin cookie is sent beyond the admin routes, or can be read by the page, or crosses sites: %+v", c)
			}
			if !adminTokenShape.MatchString(c.Value) || len(c.Value) != len("ksa_")+64 {
				a.fail("the admin's session token is not ksa_ and 32 random bytes as hex")
			}
			return c.Value, rec
		}
	}
	return "", rec
}

func (a *adminWalk) adminLog(requestID string) ([]audit.Entry, error) {
	db, err := adminaccount.Open(a.dir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Log(context.Background(), audit.Filter{RequestID: requestID})
}

func (a *adminWalk) adminLogLen() (int, error) {
	got, err := a.adminLog("")
	return len(got), err
}

func (a *adminWalk) personLog(id, requestID string) ([]audit.Entry, error) {
	s, err := a.in.Store(id)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return audit.List(context.Background(), s, audit.Filter{RequestID: requestID})
}

// logLens returns how many entries each person's log and the admin log hold.
func (a *adminWalk) logLens() ([3]int, error) {
	var n [3]int
	for i, id := range []string{a.one, a.two} {
		got, err := a.personLog(id, "")
		if err != nil {
			return n, err
		}
		n[i] = len(got)
	}
	var err error
	n[2], err = a.adminLogLen()
	return n, err
}

func errorCode(rec *httptest.ResponseRecorder) string {
	var e api.ErrorBody
	json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Code
}

// pathOf gives a route's path with something in each {name}.
func pathOf(rt api.Route) string { return routePart.ReplaceAllString(rt.Path, "x") }

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
	a := &adminWalk{dir: dir, in: in}
	for _, p := range []struct {
		id       *string
		name, pw string
	}{{&a.one, "Walk Person One", personWalkPassword}, {&a.two, "Walk Person Two", otherWalkPassword}} {
		t, err := in.Create(p.name)
		if err != nil {
			return nil, err
		}
		*p.id = t.ID
		s, err := in.Store(t.ID)
		if err != nil {
			return nil, err
		}
		err = signin.SetPassword(s, p.pw, o)
		s.Close()
		if err != nil {
			return nil, err
		}
	}
	// The admin account, made as the command line makes it.
	db, err := adminaccount.Open(dir)
	if err != nil {
		return nil, err
	}
	acc, err := db.AddAccount("Walk Admin")
	if err == nil {
		err = db.SetPassword(acc.ID, adminWalkPassword, o)
	}
	db.Close()
	if err != nil {
		return nil, err
	}
	a.account = acc.ID
	if a.w, err = newWalker(in); err != nil {
		return nil, err
	}

	// The routes: the admin's doors are the listed ones, and no admin route
	// is a chat, file or model route.
	var adminRoutes, personRoutes []api.Route
	doors := map[string]bool{}
	for _, rt := range a.w.routes {
		switch {
		case rt.Admin && rt.Door:
			doors[routeName(rt)] = true
			if _, ok := adminDoorRoutes[routeName(rt)]; !ok {
				a.fail("%s is an admin route served before sign-in and is not on the list in tools/verify/adminisolation.go", routeName(rt))
			}
		case rt.Admin:
			adminRoutes = append(adminRoutes, rt)
		case !rt.Door:
			personRoutes = append(personRoutes, rt)
		}
		if rt.Admin {
			for _, part := range strings.Split(strings.TrimPrefix(rt.Path, api.AdminPrefix), "/") {
				for _, word := range adminRouteWords {
					if part == word {
						a.fail("%s is an admin route to %s: an admin account has no chat, file or model route", routeName(rt), word)
					}
				}
			}
		}
	}
	for name := range adminDoorRoutes {
		if !doors[name] {
			return nil, fmt.Errorf("%s is listed as an admin door in tools/verify/adminisolation.go and is not one: take it off the list", name)
		}
	}
	if len(adminRoutes) == 0 || len(personRoutes) == 0 {
		return nil, errors.New("the API has no admin route or no person's route, so the walk proved nothing")
	}

	// Sign in: the person at their door, the admin at its own. Each door
	// opens its own kind of account alone.
	person, rec := a.w.signIn(a.one, personWalkPassword)
	if person == "" {
		return nil, fmt.Errorf("the walk's person cannot sign in: status %d", rec.Code)
	}
	if token, rec := a.w.signIn(a.account, adminWalkPassword); token != "" || rec.Code != http.StatusUnauthorized {
		a.fail("an admin account signed in at a person's door (status %d)", rec.Code)
	}
	if token, rec := a.adminSignIn(a.one, personWalkPassword); token != "" || rec.Code != http.StatusUnauthorized {
		a.fail("a person signed in at the admin's door (status %d)", rec.Code)
	}
	token, rec := a.adminSignIn(a.account, "not the admin's phrase at all")
	if token != "" || rec.Code != http.StatusUnauthorized || errorCode(rec) != "refused" {
		a.fail("a wrong admin password answered %d %s", rec.Code, errorCode(rec))
	}
	if got, err := a.adminLog(rec.Header().Get("X-Request-Id")); err != nil {
		return nil, err
	} else if len(got) != 1 || got[0].Outcome != audit.OutcomeRefused || got[0].Actor != adminaccount.ActorPrefix+a.account {
		a.fail("a refused admin sign-in is not in the admin log as refused")
	}
	admin, rec := a.adminSignIn("Walk Admin", adminWalkPassword)
	if admin == "" {
		return nil, fmt.Errorf("the walk's admin cannot sign in: status %d", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(admin)) {
		a.fail("the admin's sign-in answer holds the session token in its body")
	}
	// A call of the person's own, to look for in the admin's replies later.
	own := a.call("GET", api.Prefix+"session", person, "", "").Header().Get("X-Request-Id")

	// 1. A person's token on every admin route, and on an admin path that
	// is no route: refused, in their own log alone.
	adminPaths := []struct{ method, path, name string }{{"GET", api.AdminPrefix + "no/such/route", "an admin path that is no route"}}
	for _, rt := range adminRoutes {
		adminPaths = append(adminPaths, struct{ method, path, name string }{rt.Method, pathOf(rt), routeName(rt)})
	}
	for _, p := range adminPaths {
		before, err := a.logLens()
		if err != nil {
			return nil, err
		}
		rec := a.call(p.method, p.path, person, "", "{}")
		if rec.Code != http.StatusForbidden || errorCode(rec) != "not_admin" {
			a.fail("%s answered a person's session with %d %s, want 403 not_admin", p.name, rec.Code, errorCode(rec))
		}
		got, err := a.personLog(a.one, rec.Header().Get("X-Request-Id"))
		if err != nil {
			return nil, err
		}
		if len(got) != 2 || got[0].Actor != api.ActorPerson || got[1].Status != rec.Code {
			a.fail("%s, refused to a person, is in their log as %d entries (want 2, actor person)", p.name, len(got))
		}
		// In the admin cookie it is no admin's token: refused, unwritten.
		rec = a.call(p.method, p.path, "", person, "{}")
		if rec.Code != http.StatusUnauthorized {
			a.fail("%s answered a person's token in the admin cookie with %d, want 401", p.name, rec.Code)
		}
		after, err := a.logLens()
		if err != nil {
			return nil, err
		}
		if after[0] != before[0]+2 || after[1] != before[1] || after[2] != before[2] {
			a.fail("%s, called with a person's token, wrote to a log other than theirs (%v before, %v after)", p.name, before, after)
		}
	}

	// 2. The admin's token on every person's route, and on a path that is
	// no route, in either cookie: refused, in the admin log alone.
	personPaths := []struct{ method, path, name string }{{"GET", api.Prefix + "no/such/route", "a path that is no route"}}
	for _, rt := range personRoutes {
		personPaths = append(personPaths, struct{ method, path, name string }{rt.Method, pathOf(rt), routeName(rt)})
	}
	for _, p := range personPaths {
		for where, cookies := range map[string][2]string{"the person's cookie": {admin, ""}, "the admin cookie": {"", admin}} {
			before, err := a.logLens()
			if err != nil {
				return nil, err
			}
			rec := a.call(p.method, p.path, cookies[0], cookies[1], "{}")
			if rec.Code != http.StatusForbidden || errorCode(rec) != "admin_account" {
				a.fail("%s answered the admin's token in %s with %d %s, want 403 admin_account", p.name, where, rec.Code, errorCode(rec))
			}
			got, err := a.adminLog(rec.Header().Get("X-Request-Id"))
			if err != nil {
				return nil, err
			}
			if len(got) != 2 || got[0].Actor != adminaccount.ActorPrefix+a.account || got[1].Outcome != audit.OutcomeRefused {
				a.fail("%s, refused to the admin, is in the admin log as %d entries (want 2, refused)", p.name, len(got))
			}
			after, err := a.logLens()
			if err != nil {
				return nil, err
			}
			if after[0] != before[0] || after[1] != before[1] {
				a.fail("%s, called with the admin's token, wrote in a person's log", p.name)
			}
		}
	}

	// 3. The admin on every admin route: never refused for who it is, every
	// call in the admin log, and any entry it left in a person's log the
	// admin's own, before and after.
	var signOut api.Route
	for _, rt := range adminRoutes {
		if routeName(rt) == "DELETE "+api.AdminPrefix+"session" {
			signOut = rt
			continue
		}
		rec := a.call(rt.Method, pathOf(rt), "", admin, "{}")
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			a.fail("%s refused the admin's own session: %d %s", routeName(rt), rec.Code, errorCode(rec))
		}
		if err := a.wantBothLogs(routeName(rt), rt.Method+" "+pathOf(rt), rec); err != nil {
			return nil, err
		}
	}
	if signOut.Path == "" {
		a.fail("there is no route that signs an admin out")
	}

	// 4. People: in the admin log and in each person's, and the person sees
	// it in their own Settings.
	rec = a.call("GET", api.AdminPrefix+"people", "", admin, "")
	var people adminapi.PeopleReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &people) != nil || len(people.People) != 2 {
		a.fail("the people route answered the admin %d", rec.Code)
	}
	read := rec.Header().Get("X-Request-Id")
	for _, id := range []string{a.one, a.two} {
		got, err := a.personLog(id, read)
		if err != nil {
			return nil, err
		}
		if len(got) != 2 || got[0].Actor != adminview.ActorPrefix+a.account || got[1].Outcome != audit.OutcomeOK || !strings.Contains(got[0].Sent, `"by":"Walk Admin"`) {
			a.fail("the admin's read of people is in a person's log as %d entries, want 2 with the actor %s and the admin's name", len(got), adminview.ActorPrefix+a.account)
		}
	}
	seen := a.call("GET", api.Prefix+"audit/admin", person, "", "")
	var mine api.AuditReply
	found := 0
	if json.Unmarshal(seen.Body.Bytes(), &mine) == nil {
		for _, e := range mine.Entries {
			if e.RequestID == read {
				found++
			}
		}
	}
	if seen.Code != http.StatusOK || found != 2 {
		a.fail("the person does not see the admin's read in their own Settings: %d, %d entries", seen.Code, found)
	}
	// The admin read nothing of the person's own log: their own call is in
	// no admin reply.
	if own == "" || bytes.Contains(a.bodies.Bytes(), []byte(own)) {
		a.fail("an admin route's reply holds the request id of a person's own call: the admin read their log")
	}

	// 5. Signed out, and then removed from the command line: every admin
	// route refused at once.
	rec = a.call("DELETE", api.AdminPrefix+"session", "", admin, "")
	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("the walk's admin cannot sign out: status %d", rec.Code)
	}
	if err := a.wantBothLogs(routeName(signOut), "DELETE "+api.AdminPrefix+"session", rec); err != nil {
		return nil, err
	}
	a.refusedEverywhere(adminPaths, admin, "a signed-out admin session")
	again, _ := a.adminSignIn(a.account, adminWalkPassword)
	if again == "" {
		return nil, errors.New("the walk's admin cannot sign in again")
	}
	command, err := adminaccount.Open(dir)
	if err != nil {
		return nil, err
	}
	err = command.RemoveAccount(a.account)
	command.Close()
	if err != nil {
		return nil, err
	}
	a.refusedEverywhere(adminPaths, again, "a session of an account removed from the command line")

	// 6. No admin token anywhere but its cookie: no reply body and no line
	// of the server's log holds one.
	if adminTokenShape.Match(a.bodies.Bytes()) {
		a.fail("an admin session token is in a reply's body")
	}
	if adminTokenShape.Match(a.w.log.Bytes()) {
		a.fail("an admin session token is in the server's log")
	}
	return a.problems, nil
}

// wantBothLogs holds one admin call: two entries in the admin log, the
// admin's, before and after; and in each person's log nothing, or the
// admin's own pair for the same route.
func (a *adminWalk) wantBothLogs(name, action string, rec *httptest.ResponseRecorder) error {
	id := rec.Header().Get("X-Request-Id")
	got, err := a.adminLog(id)
	if err != nil {
		return err
	}
	if len(got) != 2 || got[0].Actor != adminaccount.ActorPrefix+a.account || got[0].Outcome != audit.OutcomeStarted || got[1].Status != rec.Code || got[0].Action != action {
		a.fail("%s, called by the admin, is in the admin log as %d entries (want 2: started, then status %d)", name, len(got), rec.Code)
	}
	for _, p := range []string{a.one, a.two} {
		theirs, err := a.personLog(p, id)
		if err != nil {
			return err
		}
		if len(theirs) != 0 && (len(theirs) != 2 || theirs[0].Actor != adminview.ActorPrefix+a.account || theirs[0].Outcome != audit.OutcomeStarted || theirs[0].Action != action) {
			a.fail("%s, called by the admin, left %d entries in a person's log that are not the admin's pair", name, len(theirs))
		}
	}
	return nil
}

// refusedEverywhere wants every admin path refused to token with 401 and
// written in no log.
func (a *adminWalk) refusedEverywhere(paths []struct{ method, path, name string }, token, what string) {
	before, err := a.logLens()
	if err != nil {
		a.fail("the logs could not be read: %v", err)
		return
	}
	for _, p := range paths {
		if rec := a.call(p.method, p.path, "", token, "{}"); rec.Code != http.StatusUnauthorized {
			a.fail("%s answered %s with %d, want 401", p.name, what, rec.Code)
		}
	}
	if after, err := a.logLens(); err != nil || after != before {
		a.fail("calls with %s wrote to a log (%v before, %v after)", what, before, after)
	}
}
