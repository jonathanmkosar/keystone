package wikijobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// JobState is one job as the person's settings and record have it.
type JobState struct {
	Job
	// On is the job's switch, agents.wiki_<job>_on.
	On bool `json:"on"`
	// Problem says why the job cannot run, for a person: the wrong store,
	// no token of its own, no model that may serve it, or a setting that
	// does not read. Empty when it can.
	Problem string `json:"problem,omitempty"`
	// When is its setting, and Says the same in words once it has a
	// schedule.
	When string `json:"when"`
	Says string `json:"says,omitempty"`
	// Model is the model its runs use: its own setting, or the memory
	// pass's.
	Model string `json:"model,omitempty"`
	// BudgetUSD is its budget for each run.
	BudgetUSD float64 `json:"budget_usd"`
	// HasToken says its own Waymarks token is in the person's vault. The
	// token itself is never sent.
	HasToken bool `json:"has_token"`
	// ScheduleID is the schedule it owns, once it has one.
	ScheduleID string `json:"schedule_id,omitempty"`
	// Held says a switch that holds every schedule or every agent is on.
	Held bool `json:"held"`
	// Next is its next run, in the person's zone, while it is on.
	Next *time.Time `json:"next,omitempty"`
	// LastRun is its newest run, and LastWritten what Keystone wrote for
	// the newest run that has ended.
	LastRun     *schedules.Run `json:"last_run,omitempty"`
	LastWritten *RunRecord     `json:"last_written,omitempty"`
}

// RunRecord is what Keystone wrote for one run.
type RunRecord struct {
	RunID string `json:"run_id"`
	// State is applied, refused, failed, nothing or interrupted, or
	// applying while it writes.
	State   string    `json:"state"`
	Summary string    `json:"summary"`
	Written []Written `json:"written"`
	At      time.Time `json:"at"`
}

// JobsReply answers GET wiki-jobs.
type JobsReply struct {
	Jobs []JobState `json:"jobs"`
}

// Routes returns the wiki jobs' routes.
func Routes(r *Runner) []api.Route {
	const all = api.Prefix + "wiki-jobs"
	const one = all + "/{job}"
	return []api.Route{
		{
			Method: "GET", Path: all, Handle: r.list,
			Summary: "The wiki upkeep jobs, each with its switch, why it cannot run if it cannot, its schedule, model and budget, whether its own Waymarks token is stored, and its last and next run.",
			Reply:   JobsReply{},
		},
		{
			Method: "POST", Path: one + "/on", Handle: r.switchOn,
			Summary: "Switch a wiki job on: it runs on its schedule from its next due time. It takes no body.",
			Reply:   JobState{},
		},
		{
			Method: "POST", Path: one + "/off", Handle: r.switchOff,
			Summary: "Switch a wiki job off: it starts no run. A run under way goes on. It takes no body.",
			Reply:   JobState{},
		},
		{
			Method: "POST", Path: one + "/run", Handle: r.runNow,
			Summary: "Run a wiki job now, on or off, in a chat of its own. It is refused while one of its runs is under way, and while it cannot run. It takes no body.",
			Reply:   schedules.Run{},
		},
	}
}

func (r *Runner) failed(w http.ResponseWriter, req *http.Request, c api.Call, err error) {
	switch {
	case errors.Is(err, errNotSetUp):
		api.Error(w, req, http.StatusConflict, "not_set_up", err.Error())
	case errors.Is(err, schedules.ErrRunOpen):
		api.Error(w, req, http.StatusConflict, "run_open", err.Error())
	case errors.Is(err, schedules.ErrInvalid):
		api.Error(w, req, http.StatusBadRequest, "bad_request", err.Error())
	default:
		r.logf("%s %s %s: %v", c.RequestID, req.Method, req.URL.Path, err)
		api.Error(w, req, http.StatusInternalServerError, "failed", "the call failed")
	}
}

func (r *Runner) list(w http.ResponseWriter, req *http.Request, c api.Call) {
	out := JobsReply{Jobs: []JobState{}}
	for _, j := range Jobs() {
		st, err := r.State(req.Context(), c.Store, j)
		if err != nil {
			r.failed(w, req, c, err)
			return
		}
		out.Jobs = append(out.Jobs, st)
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// job reads the job a path names, or answers not_found.
func job(w http.ResponseWriter, req *http.Request) (Job, bool) {
	j, ok := Get(req.PathValue("job"))
	if !ok {
		api.Error(w, req, http.StatusNotFound, "not_found", "there is no such wiki job")
	}
	return j, ok
}

func (r *Runner) switchOn(w http.ResponseWriter, req *http.Request, c api.Call) {
	r.turn(w, req, c, true)
}

func (r *Runner) switchOff(w http.ResponseWriter, req *http.Request, c api.Call) {
	r.turn(w, req, c, false)
}

func (r *Runner) turn(w http.ResponseWriter, req *http.Request, c api.Call, on bool) {
	j, ok := job(w, req)
	if !ok {
		return
	}
	if err := r.SetOn(req.Context(), c.Store, j, on); err != nil {
		r.failed(w, req, c, err)
		return
	}
	st, err := r.State(req.Context(), c.Store, j)
	if err != nil {
		r.failed(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, st)
}

func (r *Runner) runNow(w http.ResponseWriter, req *http.Request, c api.Call) {
	j, ok := job(w, req)
	if !ok {
		return
	}
	run, err := r.RunNow(req.Context(), c.Store, j, c.RequestID, api.ActorPerson)
	if err != nil {
		r.failed(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, run)
}

// SetOn switches a job on or off, and puts its schedule in step at once.
func (r *Runner) SetOn(ctx context.Context, s *tenant.Store, j Job, on bool) error {
	reg, err := settings.Registry()
	if err != nil {
		return err
	}
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, KeyOn(j.Name), strconv.FormatBool(on)); err != nil {
		return err
	}
	_, err = r.sync(ctx, s, j)
	r.o.Schedules.Wake()
	return err
}

// RunNow runs a job now, on or off: its schedule is made first if it has
// none. A job that cannot run is refused with what keeps it.
func (r *Runner) RunNow(ctx context.Context, s *tenant.Store, j Job, requestID, actor string) (schedules.Run, error) {
	w, err := r.want(ctx, s, j)
	if err != nil {
		return schedules.Run{}, err
	}
	if w.problem != "" {
		return schedules.Run{}, fmt.Errorf("%w: %s", errNotSetUp, w.problem)
	}
	id, err := r.syncAs(ctx, s, j, true)
	if err != nil {
		return schedules.Run{}, err
	}
	run, err := r.o.Schedules.RunNow(ctx, s, id, requestID, actor)
	if err == nil {
		r.Wake()
	}
	return run, err
}

// State reads one job as the person's settings and record have it.
func (r *Runner) State(ctx context.Context, s *tenant.Store, j Job) (JobState, error) {
	w, err := r.want(ctx, s, j)
	if err != nil {
		return JobState{}, err
	}
	st := JobState{Job: j, On: w.on, Problem: w.problem, When: w.when, Model: w.model, BudgetUSD: w.budget}
	if j.Secret != "" {
		token, _, err := s.Secrets().Get(j.Secret)
		st.HasToken = err == nil && token != ""
	}
	o, had, err := readOwned(ctx, s, j.Name)
	if err != nil {
		return JobState{}, err
	}
	if had && o.scheduleID != "" {
		got, err := r.o.Schedules.Get(ctx, s, o.scheduleID)
		switch {
		case err == nil:
			st.ScheduleID, st.Says, st.Held, st.Next, st.LastRun = got.Schedule.ID, got.Schedule.Says, got.Schedule.Held, got.Schedule.Next, got.Schedule.LastRun
		case !errors.Is(err, schedules.ErrNotFound):
			return JobState{}, err
		}
	}
	if st.LastWritten, err = lastRecord(ctx, s, j.Name); err != nil {
		return JobState{}, err
	}
	return st, nil
}

// lastRecord reads what Keystone wrote for a job's newest ended run.
func lastRecord(ctx context.Context, s *tenant.Store, job string) (*RunRecord, error) {
	var rec RunRecord
	var written, at string
	err := s.DB().QueryRowContext(ctx, "SELECT run_id, state, summary, written, at FROM wiki_runs WHERE job = ? ORDER BY at DESC, run_id DESC LIMIT 1", job).
		Scan(&rec.RunID, &rec.State, &rec.Summary, &written, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rec.Written = []Written{}
	_ = json.Unmarshal([]byte(written), &rec.Written)
	rec.At, _ = time.Parse("2006-01-02T15:04:05.000Z", at)
	return &rec, nil
}
