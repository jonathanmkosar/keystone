// Package isolationdrill is the tenant isolation drill (P13-02): a second
// person, signed in as themselves, tries every way this build has to reach
// the first person's data, and each try must be refused or come back with
// nothing of the first person's, and be in the second person's own audit
// log.
//
// Run makes a throwaway data folder with two invented people and starts the
// real server on it, as internal/app puts it together, with a scripted local
// model on the model seam, a test MCP server and a stand-in Google, all in
// memory or on this machine. It fills the first person with data of every
// kind, each piece carrying an invented marker word, then has the second
// try the API, the tools, the jobs and agents, and the files on disk. It
// returns every attempt with its result, and every way in it found.
//
// The drill is the item's proof. Its test runs it, and so does the standing
// check "isolation drill" in tools/verify. A part that adds a way to reach a
// person's data adds its tries here: a route is tried by the walk without a
// line here, as long as its ids are in pathValue.
package isolationdrill

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/connectors"
	"github.com/jonathanmkosar/keystone/internal/google/googletest"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/mcp/mcptest"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/wikijobs"
	"github.com/jonathanmkosar/keystone/settings"
)

// The parts of the drill, as the report names them.
const (
	AreaAPI   = "session and API"
	AreaTools = "tools"
	AreaJobs  = "jobs and agents"
	AreaDisk  = "files on disk"
	AreaAudit = "audit log"
)

// Attempt is one way in that was tried.
type Attempt struct {
	Area string
	// Tried says what was tried, with the first person's ids written as
	// what they are, such as <one's chat>.
	Tried string
	// Result is how it ended: refused with a status, answered with nothing
	// of the first person's, or the second person's own data.
	Result string
}

// Report is what one run of the drill found.
type Report struct {
	Attempts []Attempt
	// Reached is every way in that reached the first person's data, or an
	// attempt that is not in the second person's audit log. The drill passes
	// when it is empty.
	Reached []string
	// Notes are things seen that are not a way in, such as a route that
	// failed where it could have refused.
	Notes []string
}

func (r *Report) tried(area, what, result string) {
	r.Attempts = append(r.Attempts, Attempt{Area: area, Tried: what, Result: result})
}

func (r *Report) reached(format string, args ...any) {
	r.Reached = append(r.Reached, fmt.Sprintf(format, args...))
}

func (r *Report) note(format string, args ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
}

// person is one invented person, signed in.
type person struct {
	name, id, password, token string
}

// Invented, for the drill's throwaway folder only.
const (
	passwordOne = "drill person one's invented phrase"
	passwordTwo = "drill person two's invented phrase"
)

// The models each person has. The names differ, so the second person's
// tries with the first's model name reach no model of their own.
var (
	modelOne = models.Model{Name: "lantern-local", Label: "Lantern Local", Adapter: models.AdapterChatCompletions,
		Endpoint: "http://localhost:8091/v1", ProviderModel: "small-1", ContextWindow: 32768, OutputLimit: 2048,
		Class: models.ClassLocal, Capabilities: []models.Capability{models.CapTools}}
	modelTwo = models.Model{Name: "drill-local", Label: "Drill Local", Adapter: models.AdapterChatCompletions,
		Endpoint: "http://localhost:8092/v1", ProviderModel: "small-1", ContextWindow: 32768, OutputLimit: 2048,
		Class: models.ClassLocal, Capabilities: []models.Capability{models.CapTools}}
)

// rig is the drill's data folder, server and people.
type rig struct {
	dir    string
	in     *tenant.Install
	reg    []settings.Setting
	h      http.Handler
	routes []api.Route
	log    *lockedLog
	model  *model
	report *Report

	one, two person
	first    first  // the first person's ids
	second   second // the second person's own ids
	// marks are the first person's marker words, by what holds them, and
	// suffix makes them this run's own.
	marks  map[string]string
	suffix string

	notes, openMCP    *mcptest.Server // the first person's connector's server, and an open one
	notesURL, openURL string
	google            *googletest.Server

	closers []func()
}

// lockedLog is the server's log, written from the server's goroutines.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// Run runs the drill once and returns what it found. An error means the
// drill could not run, and proves nothing either way.
func Run() (*Report, error) {
	g := &rig{report: &Report{}, model: newModel(), marks: map[string]string{}, suffix: runSuffix()}
	defer g.close()
	if err := g.open(); err != nil {
		return nil, fmt.Errorf("the drill could not start: %w", err)
	}
	if err := g.fillFirst(); err != nil {
		return nil, fmt.Errorf("the drill could not fill the first person's data: %w", err)
	}
	if err := g.fillSecond(); err != nil {
		return nil, fmt.Errorf("the drill could not set up the second person: %w", err)
	}
	for _, part := range []struct {
		area string
		run  func() error
	}{
		{AreaTools, g.tools},
		{AreaJobs, g.jobs},
		{AreaAPI, g.api},
	} {
		if err := g.guarded(part.area, part.run); err != nil {
			return nil, fmt.Errorf("the drill's part %q could not run: %w", part.area, err)
		}
	}
	if err := g.lastChecks(); err != nil {
		return nil, fmt.Errorf("the drill's last checks could not run: %w", err)
	}
	return g.report, nil
}

// open makes the data folder, the two people and the server.
func (g *rig) open() error {
	var err error
	if g.dir, err = os.MkdirTemp("", "keystone-isolation-drill-"); err != nil {
		return err
	}
	g.closers = append(g.closers, func() { os.RemoveAll(g.dir) })
	if g.in, err = tenant.OpenWithKeeper(g.dir, secrets.NewMemoryKeeper()); err != nil {
		return err
	}
	if g.reg, err = settings.Registry(); err != nil {
		return err
	}
	o, err := signin.Defaults()
	if err != nil {
		return err
	}
	lim, err := adapters.Limits(g.reg)
	if err != nil {
		return err
	}
	ctx := context.Background()
	for _, p := range []struct {
		who      *person
		name, pw string
		model    models.Model
	}{
		{&g.one, "Drill Person One", passwordOne, modelOne},
		{&g.two, "Drill Person Two", passwordTwo, modelTwo},
	} {
		made, err := g.in.Create(p.name)
		if err != nil {
			return err
		}
		*p.who = person{name: p.name, id: made.ID, password: p.pw}
		err = g.with(made.ID, func(s *tenant.Store) error {
			if err := signin.SetPassword(s, p.pw, o); err != nil {
				return err
			}
			if err := models.Add(ctx, s, lim, p.model); err != nil {
				return err
			}
			for key, value := range map[string]string{
				"chats.default_model":    p.model.Name,
				"routing.memory_model":   p.model.Name,
				knowledge.SettingAdapter: wikijobs.StoreBuiltin,
				turn.KeyTitleAuto:        "false",
				turn.KeyRecap:            "false",
				memory.SettingOn:         "true",
			} {
				if err := tenantsettings.Set(ctx, s, g.reg, tenantsettings.Person, key, value); err != nil {
					return fmt.Errorf("setting %s: %w", key, err)
				}
			}
			// The admin's part of Google: the invented OAuth client.
			if err := tenantsettings.Set(ctx, s, g.reg, tenantsettings.Admin, connectors.KeyGoogleClientID, googletest.ClientID); err != nil {
				return err
			}
			return s.Secrets().Set(connectors.GoogleClientSecret, googletest.ClientSecret)
		})
		if err != nil {
			return err
		}
	}

	// The servers on this machine: the first person's connector, which wants
	// their token; an open one; and the stand-in Google.
	g.notes, g.openMCP = mcptest.Notes(), mcptest.Notes()
	g.notes.Token = g.mark("connector-token")
	ns, os2 := httptest.NewServer(g.notes), httptest.NewServer(g.openMCP)
	g.closers = append(g.closers, ns.Close, os2.Close)
	g.notesURL, g.openURL = ns.URL+"/mcp", os2.URL+"/mcp"
	g.google = googletest.New()
	gs := httptest.NewServer(g.google)
	g.closers = append(g.closers, gs.Close)

	c, err := app.Defaults()
	if err != nil {
		return err
	}
	g.routes = app.Routes(c) // the list only: what the binary serves
	g.log = &lockedLog{}
	c.Install, c.Log = g.in, io.Writer(g.log)
	c.Adapters = adapters.Set{models.AdapterChatCompletions: g.model.build}
	c.Connectors.Google.Endpoints, c.Connectors.Google.OnThisMachine = g.google.Endpoints(gs.URL), true
	h, stop, err := app.Open(c)
	if err != nil {
		return err
	}
	// The server is stopped before the folder is removed: closers run last
	// first.
	g.h, g.closers = h, append(g.closers, stop)

	for _, p := range []*person{&g.one, &g.two} {
		if p.token, err = g.signIn(p.id, p.password, ""); err != nil {
			return err
		}
	}
	return nil
}

// signIn signs a person in through the door and returns the session token.
func (g *rig) signIn(id, password, cookie string) (string, error) {
	rec := g.do(call{method: "POST", path: api.Prefix + "session", token: cookie, body: jsonOf(api.SignInBody{Tenant: id, Password: password})})
	for _, c := range rec.Result().Cookies() {
		if c.Name == api.CookieName && rec.Code == http.StatusOK && c.Value != "" {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("signing in answered %d", rec.Code)
}

// with runs f on a person's store, opened for it alone.
func (g *rig) with(id string, f func(s *tenant.Store) error) error {
	s, err := g.in.Store(id)
	if err != nil {
		return err
	}
	defer s.Close()
	return f(s)
}

// close stops the server and removes the folder.
func (g *rig) close() {
	for i := len(g.closers) - 1; i >= 0; i-- {
		g.closers[i]()
	}
}

// guarded runs one part of the drill between two looks at the first
// person's folder, database and audit log, and reports any change the part
// made to them.
func (g *rig) guarded(area string, run func() error) error {
	before, err := g.look()
	if err != nil {
		return err
	}
	if err := run(); err != nil {
		return err
	}
	after, err := g.look()
	if err != nil {
		return err
	}
	changes := before.changes(after)
	for _, change := range changes {
		g.report.reached("the second person's %s changed the first person's %s", area, change)
	}
	result := "unchanged"
	if len(changes) > 0 {
		result = "CHANGED: " + strings.Join(changes, "; ")
	}
	g.report.tried(AreaDisk, fmt.Sprintf("the first person's folder (%d files, hashed) and database (%d tables, row by row) before and after the second person's %s", len(before.files), len(before.tables), area), result)
	g.report.tried(AreaAudit, fmt.Sprintf("the first person's audit log (%d entries) before and after the second person's %s", len(before.audit), area),
		map[bool]string{true: "no entry added", false: "ENTRIES ADDED"}[len(after.audit) == len(before.audit)])
	return nil
}

// errStop is returned by a step that found the drill cannot go on.
var errStop = errors.New("the drill cannot go on")

// short is a status and its code, for a result.
func short(rec *httptest.ResponseRecorder) string {
	code := ""
	var e api.ErrorBody
	if rec.Code >= 400 && jsonInto(rec.Body.Bytes(), &e) == nil && e.Code != "" {
		code = " " + e.Code
	}
	return fmt.Sprintf("%d%s", rec.Code, code)
}

// strip is a helper for keeping report lines on one line.
func strip(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 240 {
		s = s[:240] + "..."
	}
	return s
}
