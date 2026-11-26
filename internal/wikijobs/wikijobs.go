// Package wikijobs is the wiki upkeep jobs (P8-06): Waymarks' agent-kit
// jobs (compile, lint, upkeep and fix issues) and a simple compile job for
// the built-in store, run as Keystone scheduled agents, so the sources the
// memory pass adds become cited pages.
//
// A job is a schedule (internal/schedules) the job owns, made and kept in
// step with the person's settings: on or off, when it runs, its model and
// its budget for each run. A run is the schedule's run: a background task
// in a chat of its own, with its budget and the pause switch as every
// unattended run has. In that chat the job is offered the read tools its
// grant has, each a call to its store under the job's own token, and it
// ends with one JSON answer: the batches it wants written. When the run is
// done, Keystone checks the answer (internal/wikijobs/scope) and writes it
// through the job's own grant, and says in the chat what it wrote.
//
// docs/architecture/wiki-jobs.md is the contract.
package wikijobs

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/wikijobs/scope"
	"github.com/jonathanmkosar/keystone/settings"
)

// The stores a job writes to, by the adapter's name.
const (
	StoreWaymarks = "waymarks"
	StoreBuiltin  = "built-in"
)

// The jobs, by name.
const (
	JobCompile        = "compile"
	JobLint           = "lint"
	JobUpkeep         = "upkeep"
	JobFixIssues      = "fix_issues"
	JobBuiltinCompile = "builtin_compile"
)

// Job is one wiki upkeep job.
type Job struct {
	// Name is the job's name, as in its settings and its routes.
	Name string `json:"name"`
	// Title names it for a person, and its schedule.
	Title string `json:"title"`
	// Store is the adapter whose store it keeps: waymarks or builtin.
	Store string `json:"store"`
	// Secret is the name of its own Waymarks token in the person's vault.
	// Empty for the built-in store, which needs none.
	Secret string `json:"secret,omitempty"`
	// Principal is the name the runbook gives its principal in Waymarks.
	Principal string `json:"principal,omitempty"`
	// Grant is the agent kit's grant for it, as Waymarks' command line
	// takes it.
	Grant string `json:"grant,omitempty"`
	// Kit is the agent kit's file its instructions follow.
	Kit string `json:"kit,omitempty"`
	// Tools are the read tools it is offered.
	Tools []string `json:"tools"`
	// May is what its grant lets it write.
	May scope.Actions `json:"-"`
}

// The read tools of Waymarks' MCP server a job may be offered, by the
// agent kit's names.
var (
	waymarksPageTools  = []string{"whoami", "read_schema", "read_catalog", "list_pages", "read_page", "page_links", "list_sources", "read_source", "search"}
	waymarksIssueTools = []string{"list_issues", "read_issue"}
	builtinTools       = []string{"read_catalog", "list_uncompiled_sources", "read_source", "read_page"}
)

// catalogue is every job, in the order a screen lists them. The grants are the
// agent kit's files (agent-kit/grants/*.json) as text.
var catalogue = []Job{
	{
		Name: JobCompile, Title: "Wiki upkeep: compile pages", Store: StoreWaymarks,
		Secret: "knowledge.waymarks.compile", Principal: "keystone-compile",
		Grant: "*:read,search,write_pages,delete_pages", Kit: "agent-kit/jobs/compile.md",
		Tools: waymarksPageTools,
		May:   scope.Actions{Write: true, Delete: true},
	},
	{
		Name: JobLint, Title: "Wiki upkeep: lint", Store: StoreWaymarks,
		Secret: "knowledge.waymarks.lint", Principal: "keystone-lint",
		Grant: "*:read,search,write_pages,delete_pages,raise_issues", Kit: "agent-kit/jobs/lint.md",
		Tools: append(append(slices.Clone(waymarksPageTools), "lint_report"), waymarksIssueTools...),
		May:   scope.Actions{Write: true, Delete: true, Raise: true},
	},
	{
		Name: JobUpkeep, Title: "Wiki upkeep: the daily round", Store: StoreWaymarks,
		Secret: "knowledge.waymarks.upkeep", Principal: "keystone-upkeep",
		Grant: "*:read,search,write_pages,delete_pages,raise_issues,close_issues", Kit: "agent-kit/jobs/upkeep.md",
		Tools: append(append(slices.Clone(waymarksPageTools), "lint_report"), waymarksIssueTools...),
		May:   scope.Actions{Write: true, Delete: true, Raise: true, Close: true},
	},
	{
		Name: JobFixIssues, Title: "Wiki upkeep: fix issues", Store: StoreWaymarks,
		Secret: "knowledge.waymarks.fix_issues", Principal: "keystone-fix-issues",
		Grant: "*:read,search,write_pages,close_issues", Kit: "agent-kit/jobs/fix-issues.md",
		Tools: append(slices.Clone(waymarksPageTools), waymarksIssueTools...),
		May:   scope.Actions{Write: true, Close: true},
	},
	{
		Name: JobBuiltinCompile, Title: "Wiki upkeep: compile pages in the built-in store", Store: StoreBuiltin,
		Tools: builtinTools,
		May:   scope.Actions{Write: true},
	},
}

// Jobs returns every job, in the order a screen lists them.
func Jobs() []Job {
	out := make([]Job, len(catalogue))
	for i, j := range catalogue {
		j.Tools = slices.Clone(j.Tools)
		out[i] = j
	}
	return out
}

// Get returns one job by name.
func Get(name string) (Job, bool) {
	for _, j := range Jobs() {
		if j.Name == name {
			return j, true
		}
	}
	return Job{}, false
}

// The person's settings of each job, in settings/agents.json. %s is the
// job's name.
const (
	keyOn     = "agents.wiki_%s_on"
	keyWhen   = "agents.wiki_%s_when"
	keyModel  = "agents.wiki_%s_model"
	keyBudget = "agents.wiki_%s_budget_usd"
	keyPrompt = "agents.wiki_%s_prompt"
)

// KeyOn is the setting that switches a job on.
func KeyOn(job string) string { return fmt.Sprintf(keyOn, job) }

// KeyWhen is the setting that says when a job runs.
func KeyWhen(job string) string { return fmt.Sprintf(keyWhen, job) }

// KeyModel is the setting that names a job's model.
func KeyModel(job string) string { return fmt.Sprintf(keyModel, job) }

// KeyBudget is the setting that holds a job's budget for each run.
func KeyBudget(job string) string { return fmt.Sprintf(keyBudget, job) }

// KeyPrompt is the install's setting that holds a job's instructions.
func KeyPrompt(job string) string { return fmt.Sprintf(keyPrompt, job) }

// KeyMemoryModel is the model a job runs on when its own setting names
// none: the memory pass's, which is local first.
const KeyMemoryModel = "routing.memory_model"

// Config is the values of the install's settings the jobs run with, in
// settings/agents.json.
type Config struct {
	Poll        time.Duration     // agents.wiki_poll
	MaxBatches  int               // agents.wiki_max_batches
	MaxNewPages int               // agents.wiki_max_new_pages
	Prompts     map[string]string // agents.wiki_<job>_prompt
}

// Limits are what one run may write.
func (c Config) Limits() scope.Limits {
	return scope.Limits{Batches: c.MaxBatches, NewPages: c.MaxNewPages}
}

// ReadConfig builds the configuration from wherever setting values are
// kept. get returns the value for a setting's key.
func ReadConfig(get func(key string) (string, bool)) (Config, error) {
	c := Config{Prompts: map[string]string{}}
	value := func(key string) (string, error) {
		v, ok := get(key)
		if !ok {
			return "", fmt.Errorf("the %s setting is missing", key)
		}
		return v, nil
	}
	v, err := value("agents.wiki_poll")
	if err != nil {
		return Config{}, err
	}
	if c.Poll, err = time.ParseDuration(v); err != nil || c.Poll <= 0 {
		return Config{}, fmt.Errorf("agents.wiki_poll must be a time longer than nothing, and is %q", v)
	}
	for key, into := range map[string]*int{"agents.wiki_max_batches": &c.MaxBatches, "agents.wiki_max_new_pages": &c.MaxNewPages} {
		v, err := value(key)
		if err != nil {
			return Config{}, err
		}
		if *into, err = strconv.Atoi(v); err != nil || *into < 1 {
			return Config{}, fmt.Errorf("%s must be a number, 1 or more, and is %q", key, v)
		}
	}
	for _, j := range catalogue {
		v, err := value(KeyPrompt(j.Name))
		if err != nil {
			return Config{}, err
		}
		if strings.TrimSpace(v) == "" {
			return Config{}, fmt.Errorf("%s cannot be empty: it is what the job is told to do", KeyPrompt(j.Name))
		}
		c.Prompts[j.Name] = v
	}
	return c, nil
}

// DefaultConfig reads the configuration from the settings registry's
// defaults.
func DefaultConfig() (Config, error) {
	reg, err := settings.Registry()
	if err != nil {
		return Config{}, err
	}
	return ReadConfig(func(key string) (string, bool) {
		s, ok := settings.Lookup(reg, key)
		return s.Default, ok
	})
}

// errWhen says a job's agents.wiki_<job>_when does not read.
var errWhen = errors.New("not a time a job can run")

var days = map[string]bool{"mon": true, "tue": true, "wed": true, "thu": true, "fri": true, "sat": true, "sun": true}

// ParseWhen reads when a job runs, as its setting says it: "hourly 15"
// (minutes past each hour), "daily 02:30", "weekdays 06:00", or "weekly
// mon,thu 03:30".
func ParseWhen(text string) (schedules.Recurrence, error) {
	f := strings.Fields(strings.ToLower(text))
	bad := func() (schedules.Recurrence, error) {
		return schedules.Recurrence{}, fmt.Errorf("%w: %q; say hourly 15, daily 02:30, weekdays 06:00 or weekly mon,thu 03:30", errWhen, text)
	}
	clock := func(s string) bool {
		t, err := time.Parse("15:04", s)
		return err == nil && t.Format("15:04") == s
	}
	switch {
	case len(f) == 2 && f[0] == schedules.KindHourly:
		m, err := strconv.Atoi(f[1])
		if err != nil || m < 0 || m > 59 {
			return bad()
		}
		return schedules.Recurrence{Kind: schedules.KindHourly, Minute: m}, nil
	case len(f) == 2 && (f[0] == schedules.KindDaily || f[0] == schedules.KindWeekdays) && clock(f[1]):
		return schedules.Recurrence{Kind: f[0], Time: f[1]}, nil
	case len(f) == 3 && f[0] == schedules.KindWeekly && clock(f[2]):
		var list []string
		for _, d := range strings.Split(f[1], ",") {
			if !days[d] {
				return bad()
			}
			list = append(list, d)
		}
		return schedules.Recurrence{Kind: schedules.KindWeekly, Days: list, Time: f[2]}, nil
	}
	return bad()
}
