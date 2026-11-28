package adminview

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
	"github.com/jonathanmkosar/keystone/internal/figures"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// The numbers of a person (P15-02). Each comes from internal/figures, the
// same functions the person's own Operations page is built on, so the admin
// and the person never see two figures for one thing. None is content: no
// title, no words of an error, no name of a chat, a file or a task.

// ErrNoPerson is an id that is no person on the install.
var ErrNoPerson = errors.New("adminview: there is no such person")

// Options are what the view's changes and the install's figures need
// beyond the install. internal/app hands them over; a view made with New
// has none, and refuses what needs them.
type Options struct {
	// Pause, Rearm and Resume are the switch that pauses every agent and
	// schedule, as its routes call it (decisions 0343, 0390): the task
	// runner's PauseAll, the scheduler's Rearm and the task runner's
	// ResumeAll. Disabling a person calls Pause once they are marked;
	// enabling calls Rearm before the mark is cleared, so runs missed while
	// disabled are not caught up, and Resume after. They change what runs,
	// and return nothing of the person's.
	Pause, Rearm, Resume func(ctx context.Context, s *tenant.Store) error
	// SetupCodeFor is how long a setup code may be used: admin.setup_code_for.
	SetupCodeFor time.Duration
	// SampleEvery is how often Operations' sampler records every person's
	// limits: operations.snapshot_every, for the install's sampler state.
	SampleEvery time.Duration
	// Now is the clock. Nil is the machine's.
	Now func() time.Time
}

// NewWith returns the view over an install with its options.
func NewWith(in *tenant.Install, o Options) *View { return &View{in: in, o: o} }

func (v *View) now() time.Time {
	if v.o.Now != nil {
		return v.o.Now()
	}
	return time.Now()
}

// known reports whether id is a person on the install.
func (v *View) known(id string) bool {
	for _, t := range v.in.Tenants() {
		if t.ID == id {
			return true
		}
	}
	return false
}

// Storage is the size of a person's database, files and artifacts, in
// bytes, as they are on disk.
type Storage struct {
	Database  int64 `json:"database"`
	Files     int64 `json:"files"`
	Artifacts int64 `json:"artifacts"`
}

// Numbers are what the admin sees of a person beside their name.
type Numbers struct {
	// Enabled is false while the admin has disabled them, since
	// DisabledAt.
	Enabled    bool      `json:"enabled"`
	DisabledAt time.Time `json:"disabled_at,omitzero"`
	// HasPassword says they have set a password, and SetupUntil is when
	// the newest setup code they have not used expires, if one is waiting.
	HasPassword bool      `json:"has_password"`
	SetupUntil  time.Time `json:"setup_until,omitzero"`
	// Spend is this month against their monthly cap.
	Spend figures.Cap `json:"spend"`
	// Storage is their database, files and artifacts.
	Storage Storage `json:"storage"`
	// Jobs is their tasks failed within the health window, and stuck.
	Jobs figures.Jobs `json:"jobs"`
	// Schedules is how many schedules they have.
	Schedules int `json:"schedules"`
	// Models counts their models by health over the window: good, errors,
	// failing, refused, idle.
	Models map[string]int `json:"models"`
	// Crossed are the keys of their limits at or past the watch level.
	Crossed []string `json:"crossed"`
}

// numbersRead is what readNumbers reads, as the person's log records it.
var numbersRead = []string{"last_sign_in", "enabled", "has_password", "spend", "storage", "jobs", "schedules", "model_health", "limits"}

// readNumbers reads a person's numbers from their store.
func readNumbers(ctx context.Context, s *tenant.Store, reg []settings.Setting, now time.Time) (Numbers, error) {
	var n Numbers
	at, off, err := s.Disabled(ctx)
	if err != nil {
		return n, err
	}
	n.Enabled, n.DisabledAt = !off, at
	if n.HasPassword, err = signin.HasPassword(s); err != nil {
		return n, err
	}
	cfg, err := figures.ReadSettings(ctx, s, reg)
	if err != nil {
		return n, err
	}
	readings, err := figures.Measure(ctx, s, reg, cfg, now)
	if err != nil {
		return n, err
	}
	for _, r := range readings {
		switch r.Key {
		case figures.LimitDatabase:
			n.Storage.Database = r.Used
		case figures.LimitFiles:
			n.Storage.Files = r.Used
		case figures.LimitArtifacts:
			n.Storage.Artifacts = r.Used
		}
	}
	if n.Spend, _, err = figures.Standing(ctx, s, reg, monthOf(now)); err != nil {
		return n, err
	}
	if n.Jobs, err = figures.ReadJobs(ctx, s, cfg, now); err != nil {
		return n, err
	}
	if n.Schedules, err = figures.Schedules(ctx, s); err != nil {
		return n, err
	}
	if n.Models, err = figures.ModelStates(ctx, s, cfg, now); err != nil {
		return n, err
	}
	if n.Crossed, err = figures.Crossed(ctx, s, reg, cfg, now); err != nil {
		return n, err
	}
	return n, nil
}

func monthOf(t time.Time) string { return t.UTC().Format("2006-01") }

// setupWaiting reads, for each person, when their newest unused setup code
// expires, from the admin database.
func (v *View) setupWaiting(now time.Time) (map[string]time.Time, error) {
	db, err := adminaccount.Open(v.in.Dir())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.SetupWaiting(now)
}

// readPerson reads one person whole, between the two entries in their log.
func (v *View) readPerson(ctx context.Context, a Asker, acc adminaccount.Account, t tenant.Tenant, reg []settings.Setting, waiting map[string]time.Time, now time.Time) (Person, error) {
	p := Person{ID: t.ID, Name: t.Name, Added: t.Created}
	err := v.with(ctx, a, t.ID, Touch{By: acc.Name, Read: numbersRead}, func(s *tenant.Store) (err error) {
		if p.LastSignIn, err = signin.LastSignIn(s); err != nil {
			return err
		}
		p.Numbers, err = readNumbers(ctx, s, reg, now)
		return err
	})
	if err != nil {
		return Person{}, err
	}
	p.SetupUntil = waiting[t.ID]
	return p, nil
}

// Person reads one person, as a row of People.
func (v *View) Person(ctx context.Context, a Asker, id string) (Person, error) {
	acc, err := v.check(a)
	if err != nil {
		return Person{}, err
	}
	for _, t := range v.in.Tenants() {
		if t.ID != id {
			continue
		}
		reg, err := settings.Registry()
		if err != nil {
			return Person{}, err
		}
		now := v.now()
		waiting, err := v.setupWaiting(now)
		if err != nil {
			return Person{}, err
		}
		return v.readPerson(ctx, a, acc, t, reg, waiting, now)
	}
	return Person{}, fmt.Errorf("%w: %s", ErrNoPerson, id)
}
