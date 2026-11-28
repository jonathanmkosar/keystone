// Package app is the one list of every route Keystone serves, and the
// place the API's settings are read.
//
// The binary serves app.Handler, the verify checks walk app.Routes, and the
// OpenAPI document is built from the same list, so all three see the same
// routes. A part that brings routes of its own adds them in Routes.
package app

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adminapi"
	"github.com/jonathanmkosar/keystone/internal/adminview"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/connectors"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/operations"
	"github.com/jonathanmkosar/keystone/internal/research"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/settingsapi"
	"github.com/jonathanmkosar/keystone/internal/shareapi"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/subagents"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/browser"
	"github.com/jonathanmkosar/keystone/internal/tools/web"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/internal/uploads"
	"github.com/jonathanmkosar/keystone/internal/usagereport"
	"github.com/jonathanmkosar/keystone/internal/visuals"
	"github.com/jonathanmkosar/keystone/internal/wikijobs"
	"github.com/jonathanmkosar/keystone/settings"
)

// APIVersion is the version of the API, as the OpenAPI document states it.
// It matches the v1 in every route's path, and is not the binary's version.
const APIVersion = "1"

// Config is everything the API runs with.
type Config struct {
	// Install is the data folder. It is not a setting: the caller opens it.
	Install *tenant.Install
	// Log is where the server's own log goes. Nil means nowhere.
	Log io.Writer

	// The rest are settings, in settings/core.json.

	SignIn       signin.Options
	ListPeople   bool          // signin.list_people
	SecureCookie bool          // server.secure_cookie
	MaxBodyBytes int64         // server.max_body_bytes
	Heartbeat    time.Duration // server.stream_heartbeat
	PageLimit    int           // server.page_limit
	StepUpFor    time.Duration // admin.step_up_for (P15-01)

	// Turns are the settings chats and turns run with, in
	// settings/chats.json.
	Turns turn.Config
	// Adapters are the model adapters this binary has. It is not a setting:
	// whoever starts the server makes the set. Nil means the fake alone.
	Adapters adapters.Set
	// runner works the turns. Handler and Open make it.
	runner *turn.Runner
	// Tools are the tools a turn may offer a model and run. It is not a
	// setting: whoever starts the server may make the registry. Nil means
	// the built-in tools, as Tools returns them.
	Tools *tools.Registry
	// toolsPlaced is turnOptions' own mark.
	toolsPlaced bool
	// Connectors is what each person's connectors run with (P7-04):
	// settings in settings/agents.json.
	Connectors connectors.Options
	// Web is what web_search and web_fetch run with (P7-03): settings in
	// settings/agents.json. A caller may give Web.Searcher its own search
	// provider, or point Web.SearchEndpoint somewhere else.
	Web web.Options
	// Jobs is what background tasks run with (P8-01), in
	// settings/agents.json.
	Jobs jobs.Config
	// jobs works the background tasks. Handler, Open and Routes make it,
	// before the turn runner, so the tool that starts a task can be given
	// it, and bind it to the turn runner after.
	jobs *jobs.Runner
	// Schedules is what scheduled tasks run with (P8-03), in
	// settings/agents.json.
	Schedules schedules.Config
	// Clock is the scheduler's clock (P8-03). It is not a setting. Nil means
	// the machine's; a check sets it to stand for time that passed while
	// the server was stopped.
	Clock func() time.Time
	// schedules keeps the schedules and starts their runs. Handler and
	// routes make it, after the task runner; Handler starts it.
	schedules *schedules.Scheduler
	// Unattended is what the switch that pauses every agent and schedule
	// runs with (P8-05), in settings/agents.json.
	Unattended unattended.Config
	// Uploads is what may be uploaded to a chat (P9-02), in
	// settings/uploads.json.
	Uploads uploads.Options
	// Artifacts is what artifacts are kept and shown with (P9-03), in
	// settings/artifacts.json.
	Artifacts artifacts.Options
	// WikiJobs is what the wiki upkeep jobs run with (P8-06), in
	// settings/agents.json.
	WikiJobs wikijobs.Config
	// wiki keeps the wiki jobs' schedules and writes their answers. Handler
	// and routes make it, after the scheduler; Handler starts it.
	wiki *wikijobs.Runner
	// Operations is what the sampler of every person's limits runs with
	// (P11-03), in settings/core.json.
	Operations operations.Config
	// Subagents is what sub-agents run with (P8-02), in settings/agents.json.
	Subagents subagents.Config
	// subagents starts sub-agents and gathers their results. Handler, Open
	// and routes make it before the turn runner, so run_subagents can be
	// given it, and bind it to the turn runner after.
	subagents *subagents.Runner
	// Research is what research mode runs with (P8-04), in settings/agents.json.
	Research research.Config
	// research starts and works research tasks. Handler, Open and routes make
	// it after the sub-agents' runner and before the turn runner, and bind it
	// to the turn runner after.
	research *research.Runner
	// Browser is what browser use runs with (P10-03), in settings/agents.json.
	// A caller may give Browser.Driver its own driver, such as the fake, and
	// Browser.HostAllowed its own rule for the hosts it serves pages from.
	Browser browser.Options
}

// Defaults returns the configuration as the settings registry ships it,
// with no data folder and no log.
func Defaults() (Config, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Config{}, err
	}
	return ReadConfig(func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	})
}

// ReadConfig builds the configuration from wherever setting values are
// kept. get returns the value for a setting's key.
func ReadConfig(get func(key string) (string, bool)) (Config, error) {
	var c Config
	var err error
	if c.SignIn, err = signin.ReadOptions(get); err != nil {
		return Config{}, err
	}
	if c.Turns, err = turn.ReadConfig(get); err != nil {
		return Config{}, err
	}
	// The connectors' settings (P7-04), in settings/agents.json.
	if c.Connectors, err = connectors.ReadOptions(get); err != nil {
		return Config{}, err
	}
	// The web tools' settings (P7-03), in settings/agents.json.
	if c.Web, err = web.ReadOptions(get); err != nil {
		return Config{}, err
	}
	// Background tasks' settings (P8-01), in settings/agents.json.
	if c.Jobs, err = jobs.ReadConfig(get); err != nil {
		return Config{}, err
	}
	// Scheduled tasks' settings (P8-03), in settings/agents.json.
	if c.Schedules, err = schedules.ReadConfig(get); err != nil {
		return Config{}, err
	}
	// The pause switch's settings (P8-05), in settings/agents.json.
	if c.Unattended, err = unattended.ReadConfig(get); err != nil {
		return Config{}, err
	}
	// Uploads' settings (P9-02), in settings/uploads.json.
	if c.Uploads, err = uploads.ReadOptions(get); err != nil {
		return Config{}, err
	}
	// Artifacts' settings (P9-03), in settings/artifacts.json.
	if c.Artifacts, err = artifacts.ReadOptions(get); err != nil {
		return Config{}, err
	}
	// The wiki upkeep jobs' settings (P8-06), in settings/agents.json.
	if c.WikiJobs, err = wikijobs.ReadConfig(get); err != nil {
		return Config{}, err
	}
	// The sampler's settings for Operations (P11-03), in settings/core.json.
	if c.Operations, err = operations.ReadConfig(get); err != nil {
		return Config{}, err
	}
	// Sub-agents' settings (P8-02), in settings/agents.json.
	if c.Subagents, err = subagents.ReadConfig(get); err != nil {
		return Config{}, err
	}
	// Research mode's settings (P8-04), in settings/agents.json.
	if c.Research, err = research.ReadConfig(get); err != nil {
		return Config{}, err
	}
	// Browser use's settings (P10-03), in settings/agents.json.
	if c.Browser, err = browser.ReadOptions(get); err != nil {
		return Config{}, err
	}
	read := func(key string, parse func(string) error) {
		if err != nil {
			return
		}
		v, ok := get(key)
		if !ok {
			err = fmt.Errorf("the %s setting is missing", key)
			return
		}
		if perr := parse(v); perr != nil {
			err = fmt.Errorf("%s: %w", key, perr)
		}
	}
	read("signin.list_people", func(v string) (e error) { c.ListPeople, e = strconv.ParseBool(v); return })
	read("server.secure_cookie", func(v string) (e error) { c.SecureCookie, e = strconv.ParseBool(v); return })
	read("server.max_body_bytes", func(v string) (e error) { c.MaxBodyBytes, e = strconv.ParseInt(v, 10, 64); return })
	read("server.stream_heartbeat", func(v string) (e error) { c.Heartbeat, e = time.ParseDuration(v); return })
	read("server.page_limit", func(v string) (e error) { c.PageLimit, e = strconv.Atoi(v); return })
	read("admin.step_up_for", func(v string) (e error) { c.StepUpFor, e = time.ParseDuration(v); return })
	if err != nil {
		return Config{}, err
	}
	switch {
	case c.StepUpFor <= 0:
		return Config{}, errors.New("admin.step_up_for must be longer than nothing")
	case c.MaxBodyBytes < 1:
		return Config{}, errors.New("server.max_body_bytes must be 1 or more")
	case c.Heartbeat <= 0:
		return Config{}, errors.New("server.stream_heartbeat must be longer than nothing")
	case c.PageLimit < 1:
		return Config{}, errors.New("server.page_limit must be 1 or more")
	}
	return c, nil
}

// Routes returns every route Keystone serves, in the order they are listed
// in the OpenAPI document. A part that brings routes adds them here:
//
//	all = append(all, chat.Routes(...)...)
func Routes(c Config) []api.Route {
	return routes(c, api.NewLog(c.Log))
}

func routes(c Config, log *api.Log) []api.Route {
	var all []api.Route
	all = append(all, api.ShellRoutes(api.Shell{
		Install:      c.Install,
		SignIn:       c.SignIn,
		ListPeople:   c.ListPeople,
		SecureCookie: c.SecureCookie,
		PageLimit:    c.PageLimit,
		Version:      APIVersion,
		Routes:       func() []api.Route { return all },
		Log:          log,
	})...)
	if c.jobs == nil {
		c.jobs = c.newJobs(log) // background tasks (P8-01), before the turn runner and its tools
	}
	if c.subagents == nil {
		c.subagents = c.newSubagents(log) // sub-agents (P8-02), before the turn runner and its tools
	}
	if c.research == nil {
		c.research = c.newResearch(log) // research mode (P8-04), after the sub-agents' runner and before the turn runner
	}
	runner := c.runner
	if runner == nil {
		runner = turn.NewRunner(c.turnOptions(log))
	}
	c.jobs.Bind(runner)
	c.subagents.Bind(runner)
	c.research.Bind(runner) // research mode (P8-04)
	all = append(all, turn.Routes(runner)...)
	all = append(all, turn.ApprovalRoutes(runner)...) // the approval policy's (P7-02)
	all = append(all, turn.MemoryRoutes(runner)...)   // the memory pass's: a chat's passes and its incognito flag (P6-06)
	// The Memory screen's, view, correct and forget, and incognito chats
	// that keep no record (P6-07).
	all = append(all, turn.MemoryScreenRoutes(runner)...)
	all = append(all, turn.IncognitoRoutes(runner)...)
	// The table of every chat and the batch delete (P4-06).
	all = append(all, turn.ManageRoutes(runner)...)
	// Embeddings: where a person's page vectors stand, and a refresh now (P6-08).
	all = append(all, turn.EmbeddingRoutes(runner)...)
	// The turn inspector's (P5-04).
	all = append(all, turn.InspectorRoutes(runner)...)
	all = append(all, turn.RecapRoutes(runner)...)        // chat recaps (P5-05)
	all = append(all, turn.AttachRoutes(runner)...)       // attached chats and handoff (P5-06)
	all = append(all, turn.ProjectRoutes(runner)...)      // projects (P9-01)
	all = append(all, turn.StyleRoutes(runner)...)        // styles (P9-05)
	all = append(all, turn.AutoRoutes(runner)...)         // Auto in the turn: try a stronger model (P14-04)
	all = append(all, connectors.Routes(c.Connectors)...) // the connectors' (P7-04)
	all = append(all, settingsapi.Routes()...)
	all = append(all, jobs.Routes(c.jobs)...) // background tasks' (P8-01)
	if c.schedules == nil {
		c.schedules = c.newSchedules(log) // scheduled tasks (P8-03), after the task runner
	}
	all = append(all, schedules.Routes(c.schedules)...) // scheduled tasks' (P8-03)
	// The switch that pauses every agent and schedule (P8-05).
	all = append(all, unattended.Routes(unattended.Options{Config: c.Unattended, Log: log,
		Runs: c.jobs.Runs, Pause: c.jobs.PauseAll, Rearm: c.schedules.Rearm, Resume: c.jobs.ResumeAll})...)
	// Uploads to a chat (P9-02).
	all = append(all, uploads.Routes(c.Uploads)...)
	all = append(all, usagereport.Routes(usagereport.Options{PageLimit: c.PageLimit, Log: log})...) // the usage report (P11-02)
	// Artifacts (P9-03): a chat's, one with its versions, compare, restore,
	// download and the sandboxed page.
	all = append(all, artifacts.Routes(c.Artifacts)...)
	// The wiki upkeep jobs (P8-06), after the scheduler.
	if c.wiki == nil {
		c.wiki = c.newWiki(log)
	}
	all = append(all, wikijobs.Routes(c.wiki)...)
	// Operations and Monitor (P11-03).
	all = append(all, operations.Routes(operations.Options{Schedules: c.schedules.List, Log: log, Now: c.Clock})...)
	// Share links (P10-04): the person's four, and the two doors that show a
	// link's chat without a session.
	all = append(all, shareapi.Routes(shareapi.Options{Install: c.Install, Log: log})...)
	// Sub-agents (P8-02): a chat's lanes, and a stop for one.
	all = append(all, subagents.Routes(c.subagents)...)
	// Inline visuals (P9-04): the page a reply's diagram is drawn in, and
	// keeping one as an artifact.
	all = append(all, visuals.Routes(visuals.Options{Artifacts: c.Artifacts})...)
	// Research mode (P8-04): what a task would run with, a chat's tasks, one,
	// and its stop.
	all = append(all, research.Routes(c.research)...)
	// Browser use (P10-03): a screenshot the agent's browser took.
	all = append(all, browser.Routes()...)
	// The admin routes (P15-01), behind the admin door. They are given the
	// one view of other people, and never the install (decision 0644).
	all = append(all, adminapi.Routes(adminapi.Options{View: adminview.New(c.Install), SignIn: c.SignIn, StepUpFor: c.StepUpFor, Log: log})...)
	return all
}

// Handler returns the handler for everything under /api/.
func Handler(c Config) (http.Handler, error) {
	log := api.NewLog(c.Log)
	if c.jobs == nil {
		c.jobs = c.newJobs(log) // background tasks (P8-01), before the turn runner and its tools
	}
	if c.subagents == nil {
		c.subagents = c.newSubagents(log) // sub-agents (P8-02), before the turn runner and its tools
	}
	if c.research == nil {
		c.research = c.newResearch(log) // research mode (P8-04), after the sub-agents' runner and before the turn runner
	}
	if c.runner == nil {
		c.runner = turn.NewRunner(c.turnOptions(log))
	}
	// A turn that was waiting when the server last stopped runs now.
	c.runner.Recover()
	// Incognito chats that were left, or idle too long, are deleted now and
	// from then on (P6-07).
	c.runner.SweepIncognito()
	// Page vectors that are missing or stale are made now and from then on
	// (P6-08).
	c.runner.SweepEmbeddings()
	// Then a background task whose round the stop cut short goes on (P8-01).
	c.jobs.Bind(c.runner)
	c.jobs.Recover()
	// Then sub-agents a stop left unfinished are ended as interrupted, as
	// the turns that started them were (P8-02).
	c.subagents.Bind(c.runner)
	c.subagents.Recover()
	// Then a research task a stop left unfinished goes on from its stage,
	// with what its parts had (P8-04).
	c.research.Bind(c.runner)
	c.research.Recover()
	// Then the scheduler starts, and a run missed while the server was
	// stopped starts once (P8-03).
	c.schedules = c.newSchedules(log)
	c.schedules.Start(c.runner)
	// Then the wiki upkeep jobs keep their schedules and write their runs'
	// answers (P8-06).
	c.wiki = c.newWiki(log)
	c.wiki.Start(c.runner)
	// Then every person's limits are recorded now and every
	// operations.snapshot_every, for Operations' trends (P11-03).
	operations.Start(c.runner, c.Install, c.Operations, log, c.Clock)
	return api.Handler(api.Options{
		Install:      c.Install,
		Log:          log,
		MaxBodyBytes: c.MaxBodyBytes,
		Heartbeat:    c.Heartbeat,
	}, routes(c, log))
}

// OpenAPI returns the OpenAPI document for the routes Keystone serves. It
// needs no data folder, because the document describes the routes and
// calls none of them.
func OpenAPI() ([]byte, error) {
	c, err := Defaults()
	if err != nil {
		return nil, err
	}
	return api.Document(Routes(c), APIVersion)
}

// turnOptions is what the runner of chats and turns is made with.
func (c Config) turnOptions(log *api.Log) turn.Options {
	// The tool loop's lines (P7-01). They are added ahead of the line
	// below and change nothing in it: the options are made as before, and
	// then given the tools. An integrator may fold this into one literal.
	if !c.toolsPlaced {
		c.toolsPlaced = true
		o := c.turnOptions(log)
		o.Connectors = connectors.NewPool(c.Connectors, log.Printf) // the connectors' sessions (P7-04), closed with the runner
		if o.Tools = c.Tools; o.Tools == nil {
			o.Tools = c.toolsOrNone(log)
		}
		o.Gate = jobs.Gate // a background task's budget (P8-01)
		// Unattended work at the cap or its budget: a local model with a
		// notice, or a stop (P8-05).
		o.Moved = unattended.Moved
		// Wiki upkeep jobs (P8-06): a job's tools in its runs' chats, only local
		// or trusted models for them, and no memory pass of them.
		o.Connectors = wikijobs.Connectors(o.Connectors)
		o.Gate = wikijobs.Gate(o.Gate)
		o.SkipMemory = wikijobs.SkipMemory
		// Sub-agents (P8-02): a sub-agent's chat has its own step limit, and no
		// memory pass reads it.
		if c.subagents != nil {
			o.MaxSteps = c.subagents.MaxSteps
			o.SkipMemory = c.subagents.SkipMemory(o.SkipMemory)
		}
		// Research mode (P8-04): no memory pass reads a research task's own chat.
		if c.research != nil {
			o.SkipMemory = c.research.SkipMemory(o.SkipMemory)
		}
		return o
	}
	return turn.Options{Install: c.Install, Log: log, Config: c.Turns, PageLimit: c.PageLimit, Adapters: c.Adapters}
}

// Open returns the handler as Handler does, and a function that stops the
// work the handler started and waits for it: the goroutines that run
// turns. A turn that is cut short this way is finished by the next start.
// A caller that removes the data folder afterwards, as a check does, calls
// it first.
func Open(c Config) (http.Handler, func(), error) {
	c.jobs = c.newJobs(api.NewLog(c.Log)) // background tasks (P8-01): closed with the turn runner

	// Sub-agents (P8-02): their supervisors are closed with the turn runner.
	c.subagents = c.newSubagents(api.NewLog(c.Log))
	c.research = c.newResearch(api.NewLog(c.Log)) // research mode (P8-04): closed with the turn runner
	c.runner = turn.NewRunner(c.turnOptions(api.NewLog(c.Log)))
	h, err := Handler(c)
	if err != nil {
		c.runner.Close()
		return nil, nil, err
	}
	return h, c.runner.Close, nil
}
