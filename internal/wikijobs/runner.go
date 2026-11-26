package wikijobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/schedules"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/wikijobs/scope"
	"github.com/jonathanmkosar/keystone/settings"
)

// The states of a run's record, in wiki_runs.
const (
	stateApplying    = "applying"
	stateApplied     = "applied"
	stateRefused     = "refused"
	stateFailed      = "failed"
	stateNothing     = "nothing"
	stateInterrupted = "interrupted"
)

// Options is what the jobs are made with.
type Options struct {
	// Install is the data folder.
	Install *tenant.Install
	// Log is the server's own log. Nil means none.
	Log *api.Log
	// Config is the install's settings for the jobs.
	Config Config
	// Schedules keeps each job's schedule and starts its runs.
	Schedules *schedules.Scheduler
}

// Runner keeps each person's jobs in step with their settings, and writes
// what each finished run's answer asks for.
type Runner struct {
	o Options

	mu      sync.Mutex
	turns   *turn.Runner
	started bool
	wake    chan struct{}
	// ticking is held while one look is made, so two never overlap.
	ticking sync.Mutex
}

// New makes the jobs' runner. Start starts its goroutine.
func New(o Options) *Runner { return &Runner{o: o, wake: make(chan struct{}, 1)} }

func (r *Runner) logf(format string, args ...any) { r.o.Log.Printf(format, args...) }

// Start marks the runs a stopped server left half written, then looks at
// once and every agents.wiki_poll, under the turn runner: closing it stops
// the goroutine. It reports false when it was started before, or cannot
// be.
func (r *Runner) Start(turns *turn.Runner) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.o.Install == nil || r.o.Schedules == nil || turns == nil {
		return false
	}
	r.turns, r.started = turns, true
	for _, t := range r.o.Install.Tenants() {
		r.withStore(t.ID, func(s *tenant.Store) { r.interrupted(context.Background(), s) })
	}
	if !turns.Go(r.loop) {
		r.started = false
		return false
	}
	return true
}

// Bind gives a runner that is not started the turn runner its notes are
// written with, for a caller that ticks by hand.
func (r *Runner) Bind(turns *turn.Runner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turns = turns
}

// Wake makes the runner look now.
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runner) loop(ctx context.Context) {
	for ctx.Err() == nil {
		wait := r.o.Config.Poll
		// While a run is under way its end is looked for every second, so
		// what it wrote is in its chat soon after it ends.
		if r.Tick(ctx) && wait > time.Second {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-time.After(wait):
		}
	}
}

// Tick looks once at every person: each job's schedule is put in step with
// their settings, and each finished run's answer is written. It reports
// whether a run of a job is still under way.
func (r *Runner) Tick(ctx context.Context) (open bool) {
	r.ticking.Lock()
	defer r.ticking.Unlock()
	if r.o.Install == nil {
		return false
	}
	for _, t := range r.o.Install.Tenants() {
		if ctx.Err() != nil {
			return open
		}
		r.withStore(t.ID, func(s *tenant.Store) {
			for _, j := range Jobs() {
				if _, err := r.sync(ctx, s, j); err != nil && ctx.Err() == nil {
					r.logf("wiki jobs: tenant %s: %s: %v", t.ID, j.Name, err)
				}
			}
			open = r.applyFinished(ctx, s) || open
		})
	}
	return open
}

func (r *Runner) withStore(tenantID string, f func(s *tenant.Store)) {
	s, err := r.o.Install.Store(tenantID)
	if err != nil {
		r.logf("wiki jobs: opening the store of tenant %s: %v", tenantID, err)
		return
	}
	defer s.Close()
	f(s)
}

// wanted is a job as the person's settings have it.
type wanted struct {
	on      bool
	when    string
	model   string // the job's own, or the memory pass's
	budget  float64
	prompt  string
	problem string // why it cannot run, or ""
	rule    schedules.Recurrence
}

func (r *Runner) setting(ctx context.Context, s *tenant.Store, key string) (string, error) {
	reg, err := settings.Registry()
	if err != nil {
		return "", err
	}
	return tenantsettings.Get(ctx, s, reg, key)
}

// want reads a job's settings, and says what keeps it from running.
func (r *Runner) want(ctx context.Context, s *tenant.Store, j Job) (wanted, error) {
	var w wanted
	text := map[string]string{}
	for _, key := range []string{KeyOn(j.Name), KeyWhen(j.Name), KeyModel(j.Name), KeyBudget(j.Name), KeyMemoryModel, knowledge.SettingAdapter} {
		v, err := r.setting(ctx, s, key)
		if err != nil {
			return w, err
		}
		text[key] = strings.TrimSpace(v)
	}
	w.on = text[KeyOn(j.Name)] == "true"
	w.when = text[KeyWhen(j.Name)]
	w.prompt = strings.NewReplacer("{max_batches}", strconv.Itoa(r.o.Config.MaxBatches), "{max_new_pages}", strconv.Itoa(r.o.Config.MaxNewPages)).
		Replace(r.o.Config.Prompts[j.Name])
	if w.model = text[KeyModel(j.Name)]; w.model == "" {
		w.model = text[KeyMemoryModel]
	}
	var err error
	if w.budget, err = strconv.ParseFloat(text[KeyBudget(j.Name)], 64); err != nil || w.budget < 0 {
		w.problem = fmt.Sprintf("%s is not an amount of dollars, 0 or more", KeyBudget(j.Name))
	}
	if w.rule, err = ParseWhen(w.when); err != nil && w.problem == "" {
		w.problem = fmt.Sprintf("%s: %v", KeyWhen(j.Name), err)
	}
	if w.problem == "" {
		w.problem = r.blocked(ctx, s, j, w.model, text[knowledge.SettingAdapter])
	}
	return w, nil
}

// blocked says what keeps a job from running on this person's install: the
// wrong store, no token of its own, or no model that may serve it.
func (r *Runner) blocked(ctx context.Context, s *tenant.Store, j Job, model, adapter string) string {
	if adapter != j.Store {
		return fmt.Sprintf("your knowledge store is %q, and this job keeps the %s store", adapter, j.Store)
	}
	if j.Secret != "" {
		token, _, err := s.Secrets().Get(j.Secret)
		if err != nil {
			return fmt.Sprintf("the secret %s could not be read: %v", j.Secret, err)
		}
		if strings.TrimSpace(token) == "" {
			return fmt.Sprintf("there is no secret named %s, the job's own Waymarks token (docs/runbooks/wiki-jobs.md)", j.Secret)
		}
	}
	if model == "" {
		return fmt.Sprintf("no model is named: set %s, or routing.memory_model, to a local or trusted model", KeyModel(j.Name))
	}
	m, err := models.Get(ctx, s, model)
	if err != nil {
		return fmt.Sprintf("the model %q is not in your registry", model)
	}
	if !scope.MayServe(string(m.Class)) {
		return fmt.Sprintf("the model %q is %s, and a wiki job runs only on a local or trusted model", model, m.Class)
	}
	return ""
}

// digest names what a job's schedule was made from.
func (w wanted) digest() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%t\x00%s\x00%s\x00%g\x00%s\x00%s", w.on, w.when, w.model, w.budget, w.prompt, w.problem)))
	return hex.EncodeToString(sum[:8])
}

// owned is a job's row in wiki_jobs.
type owned struct {
	scheduleID, synced string
}

func readOwned(ctx context.Context, s *tenant.Store, job string) (owned, bool, error) {
	var o owned
	err := s.DB().QueryRowContext(ctx, "SELECT schedule_id, synced FROM wiki_jobs WHERE job = ?", job).Scan(&o.scheduleID, &o.synced)
	if errors.Is(err, sql.ErrNoRows) {
		return o, false, nil
	}
	return o, err == nil, err
}

func writeOwned(ctx context.Context, s *tenant.Store, job string, o owned) error {
	_, err := s.DB().ExecContext(ctx, `INSERT INTO wiki_jobs(job, schedule_id, synced, changed_at) VALUES(?, ?, ?, ?)
		ON CONFLICT(job) DO UPDATE SET schedule_id = excluded.schedule_id, synced = excluded.synced, changed_at = excluded.changed_at`,
		job, o.scheduleID, o.synced, stamp())
	return err
}

func stamp() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// sync puts a job's schedule in step with the person's settings: made the
// first time the job is switched on, changed when the settings change, and
// paused while the job is off or cannot run. A change made to the schedule
// on the Scheduled screen stands until the settings change. It returns the
// schedule's id, or "" for a job that has never been on.
func (r *Runner) sync(ctx context.Context, s *tenant.Store, j Job) (string, error) {
	return r.syncAs(ctx, s, j, false)
}

// syncAs is sync; force says to make the schedule even for a job that has
// never been on, as run now does.
func (r *Runner) syncAs(ctx context.Context, s *tenant.Store, j Job, force bool) (string, error) {
	w, err := r.want(ctx, s, j)
	if err != nil {
		return "", err
	}
	o, had, err := readOwned(ctx, s, j.Name)
	if err != nil {
		return "", err
	}
	exists := false
	if had && o.scheduleID != "" {
		if _, err := r.o.Schedules.Get(ctx, s, o.scheduleID); err == nil {
			exists = true
		} else if !errors.Is(err, schedules.ErrNotFound) {
			return "", err
		}
	}
	if !exists && !w.on && !force {
		return "", nil // never on, or its schedule was deleted while off
	}
	digest := w.digest()
	if exists && o.synced == digest {
		return o.scheduleID, nil
	}
	if !exists && had && o.synced == digest && w.problem != "" && !force {
		return "", nil // on, and still cannot run
	}
	enabled := w.on && w.problem == ""
	if w.problem != "" {
		// A job that cannot run keeps its schedule, paused, until it can.
		if exists {
			if _, err := r.o.Schedules.SetEnabled(ctx, s, o.scheduleID, false); err != nil {
				return "", err
			}
			return o.scheduleID, writeOwned(ctx, s, j.Name, owned{scheduleID: o.scheduleID, synced: digest})
		}
		if !force {
			return "", writeOwned(ctx, s, j.Name, owned{synced: digest})
		}
		return "", fmt.Errorf("%w: %s", errNotSetUp, w.problem)
	}
	budget := w.budget
	body := schedules.ScheduleBody{Name: j.Title, Prompt: w.prompt, Recurrence: w.rule, Model: w.model, Enabled: &enabled, BudgetUSD: &budget}
	var got schedules.Schedule
	if exists {
		got, err = r.o.Schedules.Update(ctx, s, o.scheduleID, body)
	} else {
		got, err = r.o.Schedules.Create(ctx, s, body)
	}
	if err != nil {
		return "", err
	}
	return got.ID, writeOwned(ctx, s, j.Name, owned{scheduleID: got.ID, synced: digest})
}

// interrupted marks the runs a stopped server left half written, and says
// so in each run's chat.
func (r *Runner) interrupted(ctx context.Context, s *tenant.Store) {
	rows, err := s.DB().QueryContext(ctx, "SELECT run_id, job, chat_id, note_turn FROM wiki_runs WHERE state = ?", stateApplying)
	if err != nil {
		r.logf("wiki jobs: tenant %s: reading the runs left half written: %v", s.TenantID(), err)
		return
	}
	type left struct{ run, job, chat, note string }
	var list []left
	for rows.Next() {
		var l left
		if rows.Scan(&l.run, &l.job, &l.chat, &l.note) == nil {
			list = append(list, l)
		}
	}
	rows.Close()
	for _, l := range list {
		j, ok := Get(l.job)
		if !ok {
			continue
		}
		r.finish(ctx, s, j, l.run, l.chat, l.note, stateInterrupted, nil, "")
	}
}

// finished is a wiki run whose task has ended and whose answer is not yet
// written.
type finished struct {
	runID, chatID, taskID, requestID, job string
}

// applyFinished writes the answer of each run whose task has ended, and
// reports whether one has not ended.
func (r *Runner) applyFinished(ctx context.Context, s *tenant.Store) (open bool) {
	rows, err := s.DB().QueryContext(ctx, `
		SELECT r.id, r.chat_id, r.job_id, COALESCE(t.request_id, r.request_id), w.job FROM schedule_runs r JOIN wiki_jobs w ON w.schedule_id = r.schedule_id
		LEFT JOIN jobs t ON t.id = r.job_id
		WHERE r.job_id <> '' AND r.chat_id <> '' AND NOT EXISTS (SELECT 1 FROM wiki_runs x WHERE x.run_id = r.id)
		ORDER BY r.seq`)
	if err != nil {
		r.logf("wiki jobs: tenant %s: reading the runs: %v", s.TenantID(), err)
		return false
	}
	var list []finished
	for rows.Next() {
		var f finished
		if rows.Scan(&f.runID, &f.chatID, &f.taskID, &f.requestID, &f.job) == nil {
			list = append(list, f)
		}
	}
	rows.Close()
	for _, f := range list {
		if ctx.Err() != nil {
			return open
		}
		open = !r.applyRun(ctx, s, f) || open
	}
	return open
}

// applyRun writes one ended run's answer, once. It reports false for a run
// that has not ended.
func (r *Runner) applyRun(ctx context.Context, s *tenant.Store, f finished) (ended bool) {
	j, ok := Get(f.job)
	if !ok {
		return true
	}
	t, err := jobs.Read(ctx, s, f.taskID)
	if err != nil {
		return true // its task is gone with its chat
	}
	switch t.Status {
	case jobs.StatusDone:
		if t.NoteMessageID == 0 {
			return false // its result is not yet noted
		}
	case jobs.StatusStopped, jobs.StatusFailed:
		r.record(ctx, s, f, j, stateNothing, "The run did not finish, so nothing was written.", nil, "")
		return true
	default:
		return false // still working
	}
	noteTurn, err := newID()
	if err != nil {
		return false
	}
	// Written before anything is applied, so a restart never applies it again.
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO wiki_runs(run_id, job, chat_id, task_id, state, note_turn, at) VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO NOTHING`, f.runID, j.Name, f.chatID, f.taskID, stateApplying, noteTurn, stamp()); err != nil {
		r.logf("wiki jobs: tenant %s: run %s: %v", s.TenantID(), f.runID, err)
		return false
	}
	state, why, written := r.applyAnswer(ctx, s, j, f, t.Result)
	r.finish(ctx, s, j, f.runID, f.chatID, noteTurn, state, written, why)
	return true
}

// applyAnswer reads, checks and writes one run's answer.
func (r *Runner) applyAnswer(ctx context.Context, s *tenant.Store, j Job, f finished, reply string) (state, why string, written []Written) {
	plan, err := ParseAnswer(reply)
	if err != nil {
		return stateRefused, "its answer is not one JSON object in the shape its instructions give (" + err.Error() + ")", nil
	}
	if plan.Empty() {
		return stateApplied, "", nil
	}
	rc := recorder{s: s, requestID: f.requestID, actor: "wiki." + j.Name}
	var st scope.Store
	var c *client
	if j.Store == StoreWaymarks {
		if c, err = openClient(ctx, s, j); err != nil {
			return stateFailed, err.Error(), nil
		}
		st, err = waymarksStore(ctx, s, c, plan)
	} else {
		st, err = builtinStore(ctx, s, plan)
	}
	if err != nil {
		if errors.Is(err, scope.ErrRefused) {
			return stateRefused, strings.TrimPrefix(err.Error(), scope.ErrRefused.Error()+": "), nil
		}
		return stateFailed, "what the answer cites could not be read: " + err.Error(), nil
	}
	if err := scope.Check(j.May, r.o.Config.Limits(), plan, st); err != nil {
		return stateRefused, strings.TrimPrefix(err.Error(), scope.ErrRefused.Error()+": "), nil
	}
	if j.Store == StoreWaymarks {
		written, err = applyWaymarks(ctx, c, rc, plan)
	} else {
		written, err = applyBuiltin(ctx, s, rc, plan)
	}
	if err != nil {
		return stateFailed, err.Error(), written
	}
	return stateApplied, "", written
}

// record ends a run's record without writing a note.
func (r *Runner) record(ctx context.Context, s *tenant.Store, f finished, j Job, state, summary string, written []Written, note string) {
	raw, _ := json.Marshal(written)
	if written == nil {
		raw = []byte("[]")
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO wiki_runs(run_id, job, chat_id, task_id, state, summary, written, note_turn, at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET state = excluded.state, summary = excluded.summary, written = excluded.written, at = excluded.at`,
		f.runID, j.Name, f.chatID, f.taskID, state, summary, string(raw), note, stamp()); err != nil {
		r.logf("wiki jobs: tenant %s: run %s: %v", s.TenantID(), f.runID, err)
	}
}

// finish ends a run's record and says in its chat what was written.
func (r *Runner) finish(ctx context.Context, s *tenant.Store, j Job, runID, chatID, noteTurn, state string, written []Written, why string) {
	say := noteText(j, state, written, why)
	r.record(ctx, s, finished{runID: runID, chatID: chatID, job: j.Name}, j, state, say, written, noteTurn)
	r.mu.Lock()
	turns := r.turns
	r.mu.Unlock()
	if turns == nil || noteTurn == "" {
		return
	}
	// The run's chat is held, and stays held (fixed rule 3).
	if _, err := turns.Note(ctx, s.TenantID(), chatID, noteTurn, audit.NewRequestID(), "wiki."+j.Name,
		[]seam.Block{{Type: seam.BlockText, Text: say}}, turn.Carry{Sensitive: true}); err != nil {
		r.logf("wiki jobs: tenant %s: the note of run %s: %v", s.TenantID(), runID, err)
	}
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
