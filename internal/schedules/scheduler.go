package schedules

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/settings"
)

// The errors the functions give, beside the chat record's.
var (
	errInvalid = errors.New("the schedule is refused")
	errTooMany = errors.New("too many schedules")
	errRunOpen = errors.New("a run of this schedule is under way")
	errMoved   = errors.New("the schedule changed meanwhile")
)

// Options is everything a Scheduler is made with.
type Options struct {
	// Install is the data folder. The scheduler opens each tenant's store
	// when it looks for due schedules.
	Install *tenant.Install
	// Log is the server's own log. Nil means none.
	Log *api.Log
	// Config is the install's settings for schedules.
	Config Config
	// Jobs starts each run as a background task.
	Jobs *jobs.Runner
	// Limits are the chat record's: a run's chat title must fit them.
	Limits chat.Limits
	// Now is the scheduler's clock. Nil means the machine's. A test or a
	// check sets it to stand for time that passed while the server was
	// stopped.
	Now func() time.Time
}

// Scheduler keeps the schedules and starts their runs. It keeps nothing a
// restart would lose: a schedule's next due time and its runs are rows, and
// a run is a background task, which goes on after a restart by itself.
type Scheduler struct {
	o Options

	// firing is held while one tenant's due schedules are started, and
	// while a run is started on request, so two runs of one schedule
	// never start at once.
	firing sync.Mutex

	mu      sync.Mutex
	started bool
	since   time.Time // when it started, on its clock
	wake    chan struct{}

	once   sync.Once
	reg    []settings.Setting
	regErr error
}

// New returns a scheduler. It starts nothing until Start.
func New(o Options) *Scheduler {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Scheduler{o: o, wake: make(chan struct{}, 1)}
}

func (sc *Scheduler) now() time.Time { return sc.o.Now() }

func (sc *Scheduler) logf(format string, args ...any) { sc.o.Log.Printf(format, args...) }

func (sc *Scheduler) registry() ([]settings.Setting, error) {
	sc.once.Do(func() { sc.reg, sc.regErr = settings.Registry() })
	return sc.reg, sc.regErr
}

// Start starts the scheduler's goroutine under the turn runner, so closing
// the turn runner stops it and waits for it. It looks for due schedules at
// once, which is when a run missed while the server was stopped starts,
// and then every agents.schedule_poll at the longest, sooner when a
// schedule is due sooner or one is made or changed. It reports false, and
// starts nothing, when it has started already or cannot.
func (sc *Scheduler) Start(turns *turn.Runner) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.started || sc.o.Install == nil || sc.o.Jobs == nil || turns == nil {
		return false
	}
	sc.started, sc.since = true, sc.now()
	if !turns.Go(sc.loop) {
		sc.started = false
		return false
	}
	return true
}

// Wake makes the scheduler look for due schedules now.
func (sc *Scheduler) Wake() {
	select {
	case sc.wake <- struct{}{}:
	default:
	}
}

func (sc *Scheduler) loop(ctx context.Context) {
	for ctx.Err() == nil {
		soonest := sc.Tick(ctx)
		wait := sc.o.Config.Poll
		if !soonest.IsZero() {
			if d := soonest.Sub(sc.now()); d > 0 && d < wait {
				wait = d
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-sc.wake:
		case <-time.After(wait):
		}
	}
}

// Tick looks once at every tenant's schedules and starts the runs that are
// due. It returns the soonest due time still to come, or the zero time.
func (sc *Scheduler) Tick(ctx context.Context) time.Time {
	var soonest time.Time
	for _, t := range sc.o.Install.Tenants() {
		if ctx.Err() != nil {
			break
		}
		if at := sc.tickTenant(ctx, t.ID); !at.IsZero() && (soonest.IsZero() || at.Before(soonest)) {
			soonest = at
		}
	}
	return soonest
}

func (sc *Scheduler) tickTenant(ctx context.Context, tenantID string) time.Time {
	sc.firing.Lock()
	defer sc.firing.Unlock()
	s, err := sc.o.Install.Store(tenantID)
	if err != nil {
		sc.logf("schedules: opening the store of tenant %s: %v", tenantID, err)
		return time.Time{}
	}
	defer s.Close()
	now := sc.now()
	z := sc.zone(ctx, s)
	held, err := sc.heldAll(ctx, s) // P8-05: or the switch that pauses every agent (unattended.go)
	if err != nil {
		// Unattended work does not start on a switch that cannot be read.
		sc.logf("schedules: tenant %s: reading %s, so every schedule is held: %v", tenantID, KeyPaused, err)
		held = true
	}

	// A run whose start was cut short, or that waits to start, is started.
	starting, err := listRuns(ctx, s, "WHERE state = 'starting' ORDER BY seq")
	if err != nil {
		sc.logf("schedules: tenant %s: reading the runs that are starting: %v", tenantID, err)
	}
	for _, run := range starting {
		sc.begin(ctx, s, run, z)
	}

	due, err := listSchedules(ctx, s, "enabled = 1 AND next_at <> ''")
	if err != nil {
		sc.logf("schedules: tenant %s: reading the schedules: %v", tenantID, err)
		return time.Time{}
	}
	var soonest time.Time
	for _, r := range due {
		next := r.next
		switch {
		case r.zone != z.Name:
			// The person's time zone changed: the next due time is worked
			// out again, in the new zone, from now.
			next = sc.rearm(ctx, s, r, now, z)
		case !r.next.After(now) && held:
			// Held: the due time passes and is not made up afterwards.
			next = sc.rearm(ctx, s, r, now, z)
		case !r.next.After(now):
			open, err := sc.open(ctx, s, r.id)
			if err != nil {
				sc.logf("schedules: tenant %s: reading the last run of schedule %s: %v", tenantID, r.id, err)
				continue
			}
			if open {
				continue // it starts once the run before has ended, late
			}
			_, at, err := sc.fire(ctx, s, r, now, z, CauseSchedule, ActorSchedule, "")
			if err != nil {
				if !errors.Is(err, errMoved) {
					sc.logf("schedules: tenant %s: starting a run of schedule %s: %v", tenantID, r.id, err)
				}
				continue
			}
			next = at
		}
		if !held && next.After(now) && (soonest.IsZero() || next.Before(soonest)) {
			soonest = next
		}
	}
	return soonest
}

// rearm works out a schedule's next due time again, from now, and returns
// it. Nothing is made up for the due times that passed.
func (sc *Scheduler) rearm(ctx context.Context, s *tenant.Store, r row, now time.Time, z zoneInfo) time.Time {
	next := r.rule.next(now, z.loc)
	if _, err := s.DB().ExecContext(ctx, "UPDATE schedules SET next_at = ?, zone = ? WHERE id = ? AND next_at = ?",
		nextText(next), z.Name, r.id, nextText(r.next)); err != nil {
		sc.logf("schedules: tenant %s: moving the next due time of schedule %s: %v", s.TenantID(), r.id, err)
		return r.next
	}
	return next
}

// open reports whether a schedule's newest run is under way: starting, or
// its task waiting or running.
func (sc *Scheduler) open(ctx context.Context, s *tenant.Store, scheduleID string) (bool, error) {
	last, ok, err := lastRun(ctx, s, scheduleID)
	if err != nil || !ok {
		return false, err
	}
	switch last.state {
	case "starting":
		return true, nil
	case "failed":
		return false, nil
	}
	t, err := jobs.Read(ctx, s, last.jobID)
	if errors.Is(err, jobs.ErrNoTask) {
		return false, nil // it went with its chat
	}
	if err != nil {
		return false, err
	}
	return t.Status == jobs.StatusQueued || t.Status == jobs.StatusRunning || t.Status == jobs.StatusPaused, nil
}

// fire writes a run of a schedule and starts it, and returns the run and the
// schedule's next due time.
//
// A run for a due time stands for every due time that passed up to now: its
// due time is the latest of them, and the schedule's next due time is moved
// past now in the same transaction that writes the run, so a missed run
// happens once, whatever happens next. The database refuses a second run
// for one due time.
func (sc *Scheduler) fire(ctx context.Context, s *tenant.Store, r row, now time.Time, z zoneInfo, cause, actor, requestID string) (runRow, time.Time, error) {
	run := runRow{scheduleID: r.id, cause: cause, actor: actor, requestID: requestID, state: "starting", due: now, created: now}
	next := r.next
	if cause == CauseSchedule {
		run.due, run.missed = r.rule.latest(r.next, now, z.loc)
		next = r.rule.next(now, z.loc)
		if run.missed > 1 || now.Sub(run.due) > sc.o.Config.LateAfter {
			run.late, run.lateReason = true, LateBusy
			if run.due.Before(sc.since) {
				run.lateReason = LateStopped
			}
		}
	}
	if run.requestID == "" {
		run.requestID = audit.NewRequestID()
	}
	var err error
	if run.id, err = newID(); err != nil {
		return runRow{}, r.next, err
	}
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO schedule_runs("+runColumns+") VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			run.id, run.scheduleID, run.cause, format(run.due), run.missed, run.late, run.lateReason, run.state,
			run.requestID, run.actor, "", "", "", format(run.created)); err != nil {
			return err
		}
		if cause != CauseSchedule {
			return nil
		}
		res, err := tx.ExecContext(ctx, "UPDATE schedules SET next_at = ?, zone = ? WHERE id = ? AND next_at = ? AND enabled = 1",
			nextText(next), z.Name, r.id, nextText(r.next))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errMoved
		}
		return nil
	})
	if err != nil {
		return runRow{}, r.next, err
	}
	sc.begin(ctx, s, run, z)
	return run, next, nil
}

// begin starts a run that is starting: its chat, titled by the schedule and
// the date, then its background task in that chat. Each step is noted on
// the run before the next, so a run cut short is begun again where it
// stopped, and its chat is made once.
func (sc *Scheduler) begin(ctx context.Context, s *tenant.Store, run runRow, z zoneInfo) {
	r, err := getSchedule(ctx, s.DB(), run.scheduleID)
	if err != nil {
		if !errors.Is(err, errNoSchedule) {
			sc.logf("schedules: %s reading the schedule of run %s: %v", run.requestID, run.id, err)
		}
		return
	}
	failed := func(why string) {
		if _, err := s.DB().ExecContext(ctx, "UPDATE schedule_runs SET state = 'failed', error = ? WHERE id = ?", why, run.id); err != nil {
			sc.logf("schedules: %s noting that run %s failed: %v", run.requestID, run.id, err)
		}
	}
	if run.chatID == "" {
		c, err := chat.Create(s)
		if err != nil {
			sc.logf("schedules: %s making the chat of run %s: %v", run.requestID, run.id, err)
			failed("Its chat could not be made. The reason is in the server's log.")
			return
		}
		drop := func(err error) {
			if derr := chat.Delete(s, c.ID); derr != nil {
				sc.logf("schedules: %s removing the chat of run %s: %v", run.requestID, run.id, derr)
			}
			sc.logf("schedules: %s making the chat of run %s: %v", run.requestID, run.id, err)
			failed("Its chat could not be made. The reason is in the server's log.")
		}
		if _, err := chat.Rename(s, sc.o.Limits, c.ID, runTitle(r, run, z, sc.o.Limits)); err != nil {
			drop(err)
			return
		}
		if r.incognito {
			if err := memory.SetIncognito(ctx, s, c.ID, true); err != nil {
				drop(err)
				return
			}
		}
		if _, err := s.DB().ExecContext(ctx, "UPDATE schedule_runs SET chat_id = ? WHERE id = ?", c.ID, run.id); err != nil {
			drop(err)
			return
		}
		run.chatID = c.ID
	}
	body, err := runBody(ctx, s, r) // P8-05: a scheduled run, with its schedule's budget (unattended.go)
	if err != nil {
		sc.logf("schedules: %s reading the budget of run %s: %v", run.requestID, run.id, err)
		return // tried again at the next look
	}
	t, err := sc.o.Jobs.StartIn(ctx, s, run.chatID, run.requestID, run.actor, body, r.model)
	switch {
	case err == nil:
		if _, err := s.DB().ExecContext(ctx, "UPDATE schedule_runs SET job_id = ?, state = 'started', error = '' WHERE id = ?", t.ID, run.id); err != nil {
			sc.logf("schedules: %s noting the task of run %s: %v", run.requestID, run.id, err)
		}
	case errors.Is(err, jobs.ErrTooMany):
		const why = "It waits to start: you have the most background tasks one person may have open at once. It starts when one ends."
		if _, err := s.DB().ExecContext(ctx, "UPDATE schedule_runs SET error = ? WHERE id = ?", why, run.id); err != nil {
			sc.logf("schedules: %s noting that run %s waits: %v", run.requestID, run.id, err)
		}
	case errors.Is(err, chat.ErrNotFound):
		failed("Its chat was deleted before it began.")
	case errors.Is(err, jobs.ErrInvalid):
		failed(err.Error())
	default:
		sc.logf("schedules: %s starting the task of run %s: %v", run.requestID, run.id, err)
		failed("It could not start. The reason is in the server's log.")
	}
}

// runTitle is a run's chat title: the schedule's name and the date of its
// due time, with the time for an hourly schedule, cut to fit.
func runTitle(r row, run runRow, z zoneInfo, lim chat.Limits) string {
	at := run.due.In(z.loc)
	date := at.Format("Mon 2 Jan 2006")
	if r.rule.kind == KindHourly || run.cause == CauseNow {
		date += " " + at.Format("15:04")
	}
	suffix := []rune(", " + date)
	name := []rune(r.name)
	most := lim.TitleLength - len(suffix)
	if most < 1 {
		return string([]rune(r.name + string(suffix))[:max(lim.TitleLength, 1)])
	}
	if len(name) > most {
		name = append([]rune(strings.TrimSpace(string(name[:most-1]))), '…')
	}
	return string(name) + string(suffix)
}

// zoneInfo is the time zone a person's schedules run in.
type zoneInfo struct {
	Zone
	loc *time.Location
}

// zone reads the person's time zone. A name that is empty, or that names
// no zone Keystone knows, is the machine's.
func (sc *Scheduler) zone(ctx context.Context, s *tenant.Store) zoneInfo {
	machine := func(problem string) zoneInfo {
		abbr, _ := sc.now().In(time.Local).Zone()
		return zoneInfo{Zone: Zone{Label: fmt.Sprintf("This computer's time zone (%s)", abbr), Problem: problem}, loc: time.Local}
	}
	reg, err := sc.registry()
	if err != nil {
		return machine("")
	}
	v, err := tenantsettings.Get(ctx, s, reg, KeyTimeZone)
	name := strings.TrimSpace(v)
	if err != nil || name == "" {
		return machine("")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return machine(fmt.Sprintf("“%s”, in your time zone setting, is not a time zone Keystone knows, so your schedules use this computer's. Give a name such as Europe/Lisbon.", name))
	}
	return zoneInfo{Zone: Zone{Name: name, Label: name}, loc: loc}
}

// held reads the person's switch that holds every schedule.
func (sc *Scheduler) held(ctx context.Context, s *tenant.Store) (bool, error) {
	reg, err := sc.registry()
	if err != nil {
		return false, err
	}
	v, err := tenantsettings.Get(ctx, s, reg, KeyPaused)
	if err != nil {
		return false, err
	}
	return strconv.ParseBool(strings.TrimSpace(v))
}

// The functions the routes call. Each takes the store of the person the
// call is for, and reaches no other.

// List returns the person's schedules, oldest first, with the switch and
// the time zone.
func (sc *Scheduler) List(ctx context.Context, s *tenant.Store) (SchedulesReply, error) {
	z := sc.zone(ctx, s)
	held, err := sc.held(ctx, s)
	if err != nil {
		return SchedulesReply{}, err
	}
	list, err := listSchedules(ctx, s, "")
	if err != nil {
		return SchedulesReply{}, err
	}
	reply := SchedulesReply{Schedules: []Schedule{}, Paused: held, Zone: z.Zone, Max: sc.o.Config.MaxSchedules}
	for _, r := range list {
		out, err := sc.scheduleOf(ctx, s, r, z, held)
		if err != nil {
			return SchedulesReply{}, err
		}
		reply.Schedules = append(reply.Schedules, out)
	}
	return reply, nil
}

// Get returns one schedule and its newest runs.
func (sc *Scheduler) Get(ctx context.Context, s *tenant.Store, id string) (ScheduleReply, error) {
	z := sc.zone(ctx, s)
	held, err := sc.held(ctx, s)
	if err != nil {
		return ScheduleReply{}, err
	}
	r, err := getSchedule(ctx, s.DB(), id)
	if err != nil {
		return ScheduleReply{}, err
	}
	out, err := sc.scheduleOf(ctx, s, r, z, held)
	if err != nil {
		return ScheduleReply{}, err
	}
	runs, err := listRuns(ctx, s, "WHERE schedule_id = ? ORDER BY seq DESC LIMIT ?", id, sc.o.Config.RunsListed)
	if err != nil {
		return ScheduleReply{}, err
	}
	reply := ScheduleReply{Schedule: out, Runs: []Run{}, Zone: z.Zone}
	for _, run := range runs {
		reply.Runs = append(reply.Runs, sc.runOf(ctx, s, run, z.loc))
	}
	return reply, nil
}

// Create makes a schedule. Its first due time is the first after now: a
// time that has passed today is not run late.
func (sc *Scheduler) Create(ctx context.Context, s *tenant.Store, body ScheduleBody) (Schedule, error) {
	z, now := sc.zone(ctx, s), sc.now()
	b, err := sc.check(ctx, s, body, z, now)
	if err != nil {
		return Schedule{}, err
	}
	id, err := newID()
	if err != nil {
		return Schedule{}, err
	}
	enabled := body.Enabled == nil || *body.Enabled
	next := time.Time{}
	if enabled {
		next = b.rule.next(now, z.loc)
	}
	kind, hour, minute, days, onDate := b.rule.stored()
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM schedules").Scan(&n); err != nil {
			return err
		}
		if n >= sc.o.Config.MaxSchedules {
			return fmt.Errorf("%w: you have %d, the most one person may have", errTooMany, n)
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO schedules("+scheduleColumns+") VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			id, b.name, b.prompt, kind, hour, minute, days, onDate, b.model, body.Incognito, enabled, z.Name, nextText(next), format(now), format(now))
		if err != nil {
			return err
		}
		return setBudget(ctx, tx, id, body.BudgetUSD) // P8-05 (unattended.go)
	})
	if err != nil {
		return Schedule{}, err
	}
	sc.Wake()
	return sc.one(ctx, s, id)
}

// Update replaces a schedule's every field. Its next due time is worked out
// again from now.
func (sc *Scheduler) Update(ctx context.Context, s *tenant.Store, id string, body ScheduleBody) (Schedule, error) {
	z, now := sc.zone(ctx, s), sc.now()
	r, err := getSchedule(ctx, s.DB(), id)
	if err != nil {
		return Schedule{}, err
	}
	b, err := sc.check(ctx, s, body, z, now)
	if err != nil {
		return Schedule{}, err
	}
	enabled := r.enabled
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	next := time.Time{}
	if enabled {
		next = b.rule.next(now, z.loc)
	}
	kind, hour, minute, days, onDate := b.rule.stored()
	res, err := s.DB().ExecContext(ctx, `UPDATE schedules SET name = ?, prompt = ?, kind = ?, hour = ?, minute = ?, days = ?, on_date = ?,
		model = ?, incognito = ?, enabled = ?, zone = ?, next_at = ?, updated_at = ? WHERE id = ?`,
		b.name, b.prompt, kind, hour, minute, days, onDate, b.model, body.Incognito, enabled, z.Name, nextText(next), format(now), id)
	if err != nil {
		return Schedule{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Schedule{}, errNoSchedule
	}
	if err := setBudget(ctx, s.DB(), id, body.BudgetUSD); err != nil { // P8-05 (unattended.go)
		return Schedule{}, err
	}
	sc.Wake()
	return sc.one(ctx, s, id)
}

// SetEnabled pauses or resumes a schedule. A schedule resumed is due next
// at its first due time after now: the times that passed while it was
// paused are not made up.
func (sc *Scheduler) SetEnabled(ctx context.Context, s *tenant.Store, id string, on bool) (Schedule, error) {
	z, now := sc.zone(ctx, s), sc.now()
	r, err := getSchedule(ctx, s.DB(), id)
	if err != nil {
		return Schedule{}, err
	}
	next := time.Time{}
	if on {
		next = r.rule.next(now, z.loc)
	}
	if _, err := s.DB().ExecContext(ctx, "UPDATE schedules SET enabled = ?, zone = ?, next_at = ?, updated_at = ? WHERE id = ?",
		on, z.Name, nextText(next), format(now), id); err != nil {
		return Schedule{}, err
	}
	sc.Wake()
	return sc.one(ctx, s, id)
}

// Delete removes a schedule and the record of its runs. Each run's chat
// stays, as any chat does, and a run under way goes on.
func (sc *Scheduler) Delete(ctx context.Context, s *tenant.Store, id string) error {
	res, err := s.DB().ExecContext(ctx, "DELETE FROM schedules WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoSchedule
	}
	return nil
}

// RunNow starts a run of a schedule at once, as the person asked: paused or
// held, it runs. It changes no due time. It is refused while a run of the
// schedule is under way. requestID is the call that asked, and actor who
// the run's calls are recorded as.
func (sc *Scheduler) RunNow(ctx context.Context, s *tenant.Store, id, requestID, actor string) (Run, error) {
	sc.firing.Lock()
	defer sc.firing.Unlock()
	z, now := sc.zone(ctx, s), sc.now()
	r, err := getSchedule(ctx, s.DB(), id)
	if err != nil {
		return Run{}, err
	}
	open, err := sc.open(ctx, s, id)
	if err != nil {
		return Run{}, err
	}
	if open {
		return Run{}, fmt.Errorf("%w: it starts again when that run ends", errRunOpen)
	}
	run, _, err := sc.fire(ctx, s, r, now, z, CauseNow, actor, requestID)
	if err != nil {
		return Run{}, err
	}
	got, err := listRuns(ctx, s, "WHERE id = ?", run.id)
	if err != nil || len(got) == 0 {
		return Run{}, fmt.Errorf("reading run %s: %v", run.id, err)
	}
	return sc.runOf(ctx, s, got[0], z.loc), nil
}

// Hold turns the person's switch that holds every schedule on or off. When
// it goes off, every schedule is due next at its first due time after now:
// the times that passed while it was on are not made up.
func (sc *Scheduler) Hold(ctx context.Context, s *tenant.Store, on bool) (SchedulesReply, error) {
	reg, err := sc.registry()
	if err != nil {
		return SchedulesReply{}, err
	}
	if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, KeyPaused, strconv.FormatBool(on)); err != nil {
		return SchedulesReply{}, err
	}
	if !on {
		sc.firing.Lock()
		z, now := sc.zone(ctx, s), sc.now()
		list, err := listSchedules(ctx, s, "enabled = 1 AND next_at <> ''")
		for _, r := range list {
			if !r.next.After(now) {
				sc.rearm(ctx, s, r, now, z)
			}
		}
		sc.firing.Unlock()
		if err != nil {
			return SchedulesReply{}, err
		}
	}
	sc.Wake()
	return sc.List(ctx, s)
}

// checked is a body that passed check.
type checked struct {
	name, prompt, model string
	rule                rule
}

// check reads a schedule's body, and refuses what does not fit.
func (sc *Scheduler) check(ctx context.Context, s *tenant.Store, body ScheduleBody, z zoneInfo, now time.Time) (checked, error) {
	bad := func(format string, args ...any) (checked, error) {
		return checked{}, fmt.Errorf("%w: %s", errInvalid, fmt.Sprintf(format, args...))
	}
	b := checked{name: strings.TrimSpace(body.Name), prompt: strings.TrimSpace(body.Prompt), model: strings.TrimSpace(body.Model)}
	switch {
	case b.name == "":
		return bad("a schedule needs a name")
	case strings.ContainsAny(b.name, "\r\n"):
		return bad("a schedule's name is one line")
	case len([]rune(b.name)) > sc.o.Limits.TitleLength:
		return bad("a schedule's name has at most %d characters", sc.o.Limits.TitleLength)
	case b.prompt == "":
		return bad("a schedule needs a prompt: the message each run sends")
	case len([]rune(b.prompt)) > sc.o.Config.PromptMaxChars:
		return bad("a schedule's prompt has at most %d characters", sc.o.Config.PromptMaxChars)
	}
	if err := checkBudget(body.BudgetUSD); err != nil { // P8-05 (unattended.go)
		return checked{}, err
	}
	var err error
	if b.rule, err = readRule(body.Recurrence); err != nil {
		return checked{}, fmt.Errorf("%w: %s", errInvalid, strings.TrimPrefix(err.Error(), errRecurrence.Error()+": "))
	}
	if b.rule.kind == KindOnce && b.rule.next(now, z.loc).IsZero() {
		return bad("that time has passed: a one-time schedule runs at a time still to come, in %s", z.Label)
	}
	if b.model != "" {
		if _, err := models.Get(ctx, s, b.model); err != nil {
			if errors.Is(err, models.ErrUnknown) {
				return bad("there is no model named %q in your registry", b.model)
			}
			return bad("the model %q cannot be used: %v", b.model, err)
		}
	}
	return b, nil
}

// one returns one schedule as the routes show it.
func (sc *Scheduler) one(ctx context.Context, s *tenant.Store, id string) (Schedule, error) {
	z := sc.zone(ctx, s)
	held, err := sc.held(ctx, s)
	if err != nil {
		return Schedule{}, err
	}
	r, err := getSchedule(ctx, s.DB(), id)
	if err != nil {
		return Schedule{}, err
	}
	return sc.scheduleOf(ctx, s, r, z, held)
}

func (sc *Scheduler) scheduleOf(ctx context.Context, s *tenant.Store, r row, z zoneInfo, held bool) (Schedule, error) {
	if paused, err := unattended.Paused(ctx, s); paused || err != nil { // P8-05: the switch that pauses every agent holds it too
		held = true
	}
	rec := r.rule.recurrence()
	out := Schedule{
		ID: r.id, Name: r.name, Prompt: r.prompt, Recurrence: rec, Says: Says(rec), Model: r.model,
		Incognito: r.incognito, Enabled: r.enabled, Held: held, Created: r.created, Updated: r.updated,
	}
	if r.enabled && !held && !r.next.IsZero() {
		next := r.next
		if r.zone != z.Name {
			// Moved at the scheduler's next look; shown as it will be.
			next = r.rule.next(sc.now(), z.loc)
		}
		if !next.IsZero() {
			at := next.In(z.loc)
			out.Next = &at
		}
	}
	last, ok, err := lastRun(ctx, s, r.id)
	if err != nil {
		return Schedule{}, err
	}
	if ok {
		run := sc.runOf(ctx, s, last, z.loc)
		out.LastRun = &run
	}
	if err := sc.budgets(ctx, s, &out); err != nil { // P8-05 (unattended.go)
		return Schedule{}, err
	}
	return out, nil
}

// runOf is a run as the routes show it, with where its task is.
func (sc *Scheduler) runOf(ctx context.Context, s *tenant.Store, rr runRow, loc *time.Location) Run {
	out := Run{
		ID: rr.id, ScheduleID: rr.scheduleID, Trigger: rr.cause, Due: rr.due.In(loc), Started: rr.created.In(loc),
		Late: rr.late, LateReason: rr.lateReason, Missed: rr.missed, ChatID: rr.chatID, TaskID: rr.jobID, Error: rr.err,
	}
	switch rr.state {
	case "starting":
		out.Status = RunStarting
		return out
	case "failed":
		out.Status = RunFailed
		return out
	}
	t, err := jobs.Read(ctx, s, rr.jobID)
	if err != nil {
		out.Status = RunGone
		out.Error = "Its chat was deleted."
		if !errors.Is(err, jobs.ErrNoTask) {
			sc.logf("schedules: reading the task of run %s: %v", rr.id, err)
			out.Error = "Its task could not be read. The reason is in the server's log."
		}
		return out
	}
	out.Status, out.Waiting, out.Progress, out.Steps, out.SpentMicros = t.Status, t.Waiting, t.Progress, t.Steps, t.SpentMicros
	if t.Started != nil {
		at := t.Started.In(loc)
		out.Began = &at
	}
	if t.Ended != nil {
		at := t.Ended.In(loc)
		out.Ended = &at
	}
	if t.Error != nil {
		out.Error = t.Error.Message
	}
	return out
}
