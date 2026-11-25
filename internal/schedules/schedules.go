// Package schedules is scheduled tasks (P8-03): a prompt on a schedule, in
// the person's time zone, whose every run is a chat turn. The plan's words:
// a prompt on a schedule; each run is a chat turn; a missed run happens
// once when the machine is back.
//
// A run is not worked here. When a schedule is due, the scheduler makes a
// chat titled by the schedule and the date and starts a background task
// (internal/jobs) that works in that chat, so the run is the chat: it is in
// the sidebar, every step streams and is recorded, a tool that changes
// something waits for the person, the task's budget and the monthly cap
// hold, and a run cut short by a restart resumes. What this package keeps
// is each schedule, the next time it is due, and a row for each run.
//
// docs/architecture/schedules.md is the contract.
package schedules

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	// The time zone database, compiled in, so a machine that has none, as
	// Windows does not, still knows every zone's name and its changes of
	// the clocks.
	_ "time/tzdata"

	"github.com/jonathanmkosar/keystone/settings"
)

// The person's own settings.
const (
	// KeyTimeZone is the person's time zone, an IANA name. Empty means the
	// machine's.
	KeyTimeZone = "agents.time_zone"
	// KeyPaused holds every schedule while it is on.
	KeyPaused = "agents.schedules_paused"
)

// ActorSchedule is who a scheduled run's model calls and tool calls are
// recorded as, in the audit log. A run the person asked for with run now is
// recorded as the person.
const ActorSchedule = "schedule"

// Why a run was started.
const (
	CauseSchedule = "schedule" // a due time came
	CauseNow      = "now"      // the person asked
)

// Why a run was late.
const (
	// LateStopped says Keystone was not running at the due time.
	LateStopped = "stopped"
	// LateBusy says the run before was still going, or the person had too
	// many background tasks open, at the due time.
	LateBusy = "busy"
)

// The statuses of a run, beside the statuses of its task (queued, running,
// done, stopped, failed), which are passed on as they are.
const (
	// RunStarting is a run whose task is not yet written. With an error it
	// is waiting to start, and is tried again.
	RunStarting = "starting"
	// RunFailed is a run that could not start. Error says why.
	RunFailed = "failed"
	// RunGone is a run whose chat was deleted, and its task with it.
	RunGone = "gone"
)

// Schedule is one scheduled task.
type Schedule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Prompt is the message each run sends, in a new chat.
	Prompt     string     `json:"prompt"`
	Recurrence Recurrence `json:"recurrence"`
	// Says is the recurrence in words, as the screen shows it.
	Says string `json:"says"`
	// Model is the registry name of the model its runs use. Empty means a
	// new chat's own.
	Model string `json:"model,omitempty"`
	// Incognito makes each run's chat incognito: no memory pass reads it.
	Incognito bool `json:"incognito"`
	// Enabled is false while the person has paused it.
	Enabled bool `json:"enabled"`
	// Held says the person's switch that holds every schedule is on.
	Held bool `json:"held"`
	// Next is the next time it is due, in the person's time zone. Absent
	// while it is paused or held, and for a one-time schedule that has run.
	Next *time.Time `json:"next,omitempty"`
	// LastRun is its newest run, when it has one.
	LastRun *Run      `json:"last_run,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Run is one run of a schedule.
type Run struct {
	ID         string `json:"id"`
	ScheduleID string `json:"schedule_id"`
	// Trigger is schedule (a due time came) or now (the person asked).
	Trigger string `json:"trigger"`
	// Due is the due time it was for, in the person's time zone: the latest
	// of those that passed when it stands for several, or when the person
	// asked.
	Due time.Time `json:"due"`
	// Started is when the scheduler started it.
	Started time.Time `json:"started"`
	// Late says it started more than agents.schedule_late_after after Due,
	// and LateReason why: stopped or busy.
	Late       bool   `json:"late"`
	LateReason string `json:"late_reason,omitempty"`
	// Missed is how many due times it stands for: one, or more when
	// Keystone was not running over several. Zero for a run the person
	// asked for.
	Missed int `json:"missed"`
	// Status is starting, queued, running, done, stopped, failed or gone.
	Status string `json:"status"`
	// Waiting says a tool call of the run waits for the person's approval,
	// in its chat.
	Waiting bool `json:"waiting"`
	// Progress is where its task is, in a few words.
	Progress string `json:"progress,omitempty"`
	// ChatID is its chat, where it works and where its result is.
	ChatID string `json:"chat_id,omitempty"`
	// TaskID is its background task, in ChatID.
	TaskID string `json:"task_id,omitempty"`
	// Error says, for a person, why it failed, or why it waits to start.
	Error string `json:"error,omitempty"`
	// Steps is how many model calls it has finished, and SpentMicros what
	// it has spent on models that are not local, in millionths of a dollar.
	Steps       int   `json:"steps"`
	SpentMicros int64 `json:"spent_micros"`
	// Began and Ended are when its task began work and ended.
	Began *time.Time `json:"began,omitempty"`
	Ended *time.Time `json:"ended,omitempty"`
}

// ScheduleBody makes or changes a schedule. A change replaces every field.
type ScheduleBody struct {
	Name       string     `json:"name"`
	Prompt     string     `json:"prompt"`
	Recurrence Recurrence `json:"recurrence"`
	// Model is a registry name, or empty for a new chat's own.
	Model     string `json:"model,omitempty"`
	Incognito bool   `json:"incognito,omitempty"`
	// Enabled, when given, pauses or resumes it. Absent means on for a new
	// schedule, and as it was for a change.
	Enabled *bool `json:"enabled,omitempty"`
}

// Zone is the time zone schedules run in.
type Zone struct {
	// Name is the IANA name from agents.time_zone, or empty for the
	// machine's.
	Name string `json:"name,omitempty"`
	// Label names it for a person.
	Label string `json:"label"`
	// Problem says why agents.time_zone was not used: it names no zone
	// Keystone knows.
	Problem string `json:"problem,omitempty"`
}

// SchedulesReply answers GET schedules.
type SchedulesReply struct {
	// Schedules are the person's, oldest first.
	Schedules []Schedule `json:"schedules"`
	// Paused says the switch that holds every schedule is on.
	Paused bool `json:"paused"`
	Zone   Zone `json:"zone"`
	// Max is the most schedules one person may have.
	Max int `json:"max"`
}

// ScheduleReply answers GET schedules/{id}.
type ScheduleReply struct {
	Schedule Schedule `json:"schedule"`
	// Runs are its newest runs, newest first, up to
	// agents.schedule_runs_listed.
	Runs []Run `json:"runs"`
	Zone Zone  `json:"zone"`
}

// DeletedReply answers DELETE schedules/{id}.
type DeletedReply struct {
	Deleted bool `json:"deleted"`
}

// Config is the values of the install's settings schedules run with, in
// settings/agents.json. agents.time_zone and agents.schedules_paused are
// the person's own, and are read when they are needed.
type Config struct {
	Poll           time.Duration // agents.schedule_poll
	LateAfter      time.Duration // agents.schedule_late_after
	MaxSchedules   int           // agents.schedule_max
	PromptMaxChars int           // agents.schedule_prompt_max_chars
	RunsListed     int           // agents.schedule_runs_listed
}

// ReadConfig builds the configuration from wherever setting values are
// kept. get returns the value for a setting's key.
func ReadConfig(get func(key string) (string, bool)) (Config, error) {
	var c Config
	var err error
	read := func(key string, into any) {
		if err != nil {
			return
		}
		v, ok := get(key)
		if !ok {
			err = fmt.Errorf("the %s setting is missing", key)
			return
		}
		switch p := into.(type) {
		case *int:
			*p, err = strconv.Atoi(v)
		case *time.Duration:
			*p, err = time.ParseDuration(v)
		}
		if err != nil {
			err = fmt.Errorf("%s: %w", key, err)
		}
	}
	read("agents.schedule_poll", &c.Poll)
	read("agents.schedule_late_after", &c.LateAfter)
	read("agents.schedule_max", &c.MaxSchedules)
	read("agents.schedule_prompt_max_chars", &c.PromptMaxChars)
	read("agents.schedule_runs_listed", &c.RunsListed)
	if err != nil {
		return Config{}, err
	}
	switch {
	case c.Poll <= 0:
		return Config{}, errors.New("agents.schedule_poll must be longer than nothing")
	case c.LateAfter < 0:
		return Config{}, errors.New("agents.schedule_late_after cannot be negative")
	case c.MaxSchedules < 1 || c.PromptMaxChars < 1 || c.RunsListed < 1:
		return Config{}, errors.New("agents.schedule_max, agents.schedule_prompt_max_chars and agents.schedule_runs_listed must each be 1 or more")
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
