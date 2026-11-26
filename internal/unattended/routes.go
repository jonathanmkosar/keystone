package unattended

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Count is how many of a person's unattended runs are working and how many
// the switch holds.
type Count struct {
	// Working is the runs with work under way: a round running or waiting to
	// run, or one that has not yet seen the switch.
	Working int
	// Held is the runs the switch holds: stopped part of the way, or not yet
	// started. They go on when it goes off.
	Held int
}

// SwitchReply answers the routes of the switch.
type SwitchReply struct {
	// Paused says the switch that pauses every agent and schedule is on.
	Paused bool `json:"paused"`
	// Working is how many of the person's unattended runs have work under
	// way. Right after a pause it is the ones that had not stopped within
	// agents.pause_wait.
	Working int `json:"working"`
	// Held is how many the switch holds.
	Held int `json:"held"`
}

// Config is the install's settings the switch runs with, in
// settings/agents.json.
type Config struct {
	PauseWait time.Duration // agents.pause_wait
}

// ReadConfig builds the configuration from wherever setting values are
// kept. get returns the value for a setting's key.
func ReadConfig(get func(key string) (string, bool)) (Config, error) {
	v, ok := get("agents.pause_wait")
	if !ok {
		return Config{}, errors.New("the agents.pause_wait setting is missing")
	}
	wait, err := time.ParseDuration(v)
	if err != nil {
		return Config{}, fmt.Errorf("agents.pause_wait: %w", err)
	}
	if wait < 0 {
		return Config{}, errors.New("agents.pause_wait cannot be negative")
	}
	return Config{PauseWait: wait}, nil
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

// Options is what the switch's routes are served with: the install's
// settings, and the parts that run unattended work. Each function takes the
// store of the person the call is for, and reaches no other.
type Options struct {
	Config Config
	Log    *api.Log
	// Runs counts the person's unattended runs (jobs.Runner.Runs).
	Runs func(ctx context.Context, s *tenant.Store) (Count, error)
	// Pause is called once the switch is on: what runs unattended work
	// looks at it now instead of at its next look (jobs.Runner.PauseAll).
	Pause func(ctx context.Context, s *tenant.Store) error
	// Rearm is called before the switch goes off: every schedule's due time
	// that passed is moved on, so nothing paused is made up
	// (schedules.Scheduler.Rearm).
	Rearm func(ctx context.Context, s *tenant.Store) error
	// Resume is called once the switch is off: the held runs go on
	// (jobs.Runner.ResumeAll).
	Resume func(ctx context.Context, s *tenant.Store) error
}

// Routes returns the routes of the switch that pauses every agent and
// schedule.
func Routes(o Options) []api.Route {
	const agents = api.Prefix + "agents"
	return []api.Route{
		{
			Method: "GET", Path: agents, Handle: o.get,
			Summary: "The switch that pauses every agent and schedule of yours, and how many of your background tasks and scheduled runs are working and held.",
			Reply:   SwitchReply{},
		},
		{
			Method: "POST", Path: agents + "/pause", Handle: o.pause,
			Summary: "Pause every agent and schedule at once. Each background task and scheduled run that is working is stopped, with what it did kept, and waits; one started meanwhile waits too; no schedule starts a run. The call waits up to agents.pause_wait for the work to stop. It takes no body.",
			Reply:   SwitchReply{},
		},
		{
			Method: "POST", Path: agents + "/resume", Handle: o.resume,
			Summary: "Let every agent and schedule go on. A paused task goes on with a new round, a held one starts, and each schedule is due at its first due time after now: the times that passed while paused are not made up. It takes no body.",
			Reply:   SwitchReply{},
		},
	}
}

func (o Options) fail(w http.ResponseWriter, req *http.Request, c api.Call, err error) {
	o.Log.Printf("%s %s %s: %v", c.RequestID, req.Method, req.URL.Path, err)
	api.Error(w, req, http.StatusInternalServerError, "failed", "the call failed")
}

// reply reads the switch and the counts.
func (o Options) reply(ctx context.Context, s *tenant.Store) (SwitchReply, error) {
	on, err := Paused(ctx, s)
	if err != nil {
		return SwitchReply{}, err
	}
	out := SwitchReply{Paused: on}
	if o.Runs != nil {
		n, err := o.Runs(ctx, s)
		if err != nil {
			return SwitchReply{}, err
		}
		out.Working, out.Held = n.Working, n.Held
	}
	return out, nil
}

func (o Options) get(w http.ResponseWriter, req *http.Request, c api.Call) {
	out, err := o.reply(req.Context(), c.Store)
	if err != nil {
		o.fail(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

func (o Options) pause(w http.ResponseWriter, req *http.Request, c api.Call) {
	ctx := req.Context()
	if err := SetPaused(ctx, c.Store, true); err != nil {
		o.fail(w, req, c, err)
		return
	}
	if o.Pause != nil {
		if err := o.Pause(ctx, c.Store); err != nil {
			o.fail(w, req, c, err)
			return
		}
	}
	// Wait for the work that was under way to stop, up to agents.pause_wait.
	deadline := time.Now().Add(o.Config.PauseWait)
	look := max(o.Config.PauseWait/100, 10*time.Millisecond)
	for {
		out, err := o.reply(ctx, c.Store)
		if err != nil {
			o.fail(w, req, c, err)
			return
		}
		if out.Working == 0 || !time.Now().Before(deadline) {
			api.WriteJSON(w, http.StatusOK, out)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(look):
		}
	}
}

func (o Options) resume(w http.ResponseWriter, req *http.Request, c api.Call) {
	ctx := req.Context()
	if o.Rearm != nil {
		if err := o.Rearm(ctx, c.Store); err != nil {
			o.fail(w, req, c, err)
			return
		}
	}
	if err := SetPaused(ctx, c.Store, false); err != nil {
		o.fail(w, req, c, err)
		return
	}
	if o.Resume != nil {
		if err := o.Resume(ctx, c.Store); err != nil {
			o.fail(w, req, c, err)
			return
		}
	}
	out, err := o.reply(ctx, c.Store)
	if err != nil {
		o.fail(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}
