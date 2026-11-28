package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/figures"
	"github.com/jonathanmkosar/keystone/internal/jobs"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// The limits, by key, and their units, scopes and states: internal/figures'
// own since P15-02 (decision 0700). A run's budget is LimitRun, a colon, and
// the run's request id. LimitDisk is no longer in a person's report: the
// disk is the install's, shown only in the admin account (decision 0701).
const (
	LimitCap       = figures.LimitCap
	LimitReserve   = figures.LimitReserve
	LimitDisk      = figures.LimitDisk
	LimitDatabase  = figures.LimitDatabase
	LimitFiles     = figures.LimitFiles
	LimitArtifacts = figures.LimitArtifacts
	LimitRun       = figures.LimitRun

	UnitMicros = figures.UnitMicros
	UnitBytes  = figures.UnitBytes

	ScopeTenant  = figures.ScopeTenant
	ScopeInstall = figures.ScopeInstall

	StateOK      = figures.StateOK
	StateWatch   = figures.StateWatch
	StateFull    = figures.StateFull
	StateUnknown = figures.StateUnknown
)

// Limit is one place the install may run out of room, as it stands now.
type Limit struct {
	// Key is cap, memory_reserve, disk, database, files, artifacts, or run
	// and a run's request id after a colon.
	Key string `json:"key"`
	// Label names it for a person, and Note says what it counts.
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
	// Scope is tenant or install, and Unit usd_micros or bytes.
	Scope string `json:"scope"`
	Unit  string `json:"unit"`
	// Used is what is used, and Of the limit, in Unit.
	Used int64 `json:"used"`
	Of   int64 `json:"of"`
	// Percent is Used of Of, in whole percent, rounded down; it may pass
	// 100. A limit of nothing that holds something is 100.
	Percent int `json:"percent"`
	// Watch is the watch level, in percent of Of.
	Watch int `json:"watch"`
	// State is ok, watch, full or unknown, and Crossed says the watch level
	// is reached: the state is watch or full, or the monthly cap does not
	// read, which stops every paid call.
	State   string `json:"state"`
	Crossed bool   `json:"crossed"`
	Problem string `json:"problem,omitempty"`
	// Trend is how it has moved: from the snapshots, or for a run's budget
	// from its usage records.
	Trend Trend `json:"trend"`
	// ChatID and RunID name a run's chat and its task, for a run's budget.
	ChatID string `json:"chat_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`
}

// reading is a limit as measured, before its trend and its state.
type reading struct {
	Limit
	// since is the earliest time its trend may reach back to: the start of
	// the month for the cap and the reserve.
	since time.Time
	// resets is when the limit starts again, for the cap and the reserve.
	resets *time.Time
}

// measure reads where every limit but the runs' stands now: the ones the
// sampler records. It is figures.Measure, the function the admin reads too:
// the cap, the reserve, the database, the files and the artifacts. The disk
// left a person's report in P15-02 (decision 0701).
func measure(ctx context.Context, s *tenant.Store, reg []settings.Setting, cfg Settings, now time.Time) ([]reading, error) {
	got, err := figures.Measure(ctx, s, reg, cfg, now)
	if err != nil {
		return nil, err
	}
	out := make([]reading, 0, len(got))
	for _, r := range got {
		out = append(out, reading{
			Limit: Limit{Key: r.Key, Label: r.Label, Note: r.Note, Scope: r.Scope, Unit: r.Unit, Used: r.Used, Of: r.Of, Watch: r.Watch, Problem: r.Problem},
			since: r.Since, resets: r.Resets,
		})
	}
	return out, nil
}

// Limits returns where every limit stands for a tenant now, each with its
// share used, its watch level and its trend: the monthly cap, the memory
// reserve, the disk, the person's database, files and artifacts, and the
// budget of each background task and scheduled run under way.
func Limits(ctx context.Context, s *tenant.Store, reg []settings.Setting, now time.Time) ([]Limit, error) {
	cfg, err := ReadSettings(ctx, s, reg)
	if err != nil {
		return nil, err
	}
	return limits(ctx, s, reg, cfg, now)
}

func limits(ctx context.Context, s *tenant.Store, reg []settings.Setting, cfg Settings, now time.Time) ([]Limit, error) {
	readings, err := measure(ctx, s, reg, cfg, now)
	if err != nil {
		return nil, err
	}
	out := make([]Limit, 0, len(readings))
	for _, r := range readings {
		since := now.Add(-cfg.TrendWindow)
		if r.since.After(since) {
			since = r.since
		}
		points, err := Snapshots(ctx, s, r.Key, since)
		if err != nil {
			return nil, err
		}
		l := r.Limit
		if l.Problem == "" {
			points = append(points, Point{At: now.UTC(), Used: l.Used, Of: l.Of})
		}
		l.Trend = trend(points, l.Used, l.Of, now, r.resets, cfg.TrendMinSpan)
		judge(&l)
		out = append(out, l)
	}
	runs, err := runLimits(ctx, s, cfg, now)
	if err != nil {
		return nil, err
	}
	return append(out, runs...), nil
}

// runLimits is the budget of each background task and scheduled run under
// way, against what it has spent, with its trend from its usage records.
func runLimits(ctx context.Context, s *tenant.Store, cfg Settings, now time.Time) ([]Limit, error) {
	open, err := jobs.List(ctx, s, jobs.Filter{Open: true})
	if err != nil {
		return nil, err
	}
	var out []Limit
	for i := len(open) - 1; i >= 0; i-- { // oldest first
		t := open[i]
		recs, err := usage.List(ctx, s, usage.Filter{RequestID: t.RequestID})
		if err != nil {
			return nil, err
		}
		var spent int64
		var points []Point
		for _, rec := range recs {
			if usage.Counts(rec.Class) {
				spent += rec.CostMicros
				points = append(points, Point{At: rec.At, Used: spent, Of: t.BudgetMicros})
			}
		}
		points = append(points, Point{At: now.UTC(), Used: spent, Of: t.BudgetMicros})
		what := "Background task"
		if t.Scheduled {
			what = "Scheduled task"
		}
		l := Limit{
			Key: LimitRun + ":" + t.RequestID, Label: fmt.Sprintf("%s “%s”", what, t.Title), Scope: ScopeTenant, Unit: UnitMicros,
			Note: "What this run has spent on models that are not local, against its own budget.",
			Used: spent, Of: t.BudgetMicros, Watch: cfg.Watch[LimitRun], ChatID: t.WorkChatID, RunID: t.ID,
		}
		l.Trend = trend(points, l.Used, l.Of, now, nil, 0) // a run is short: its own calls tell its rate
		judge(&l)
		out = append(out, l)
	}
	return out, nil
}

// judge sets a limit's share, state and whether its watch level is
// crossed, by figures.Judge.
func judge(l *Limit) {
	l.Percent, l.State, l.Crossed = figures.Judge(l.Key, l.Used, l.Of, l.Watch, l.Problem)
}
