// Package unattended is what Keystone keeps for work that runs without the
// person watching (P8-05): background tasks, scheduled runs and, once P8-02
// lands, sub-agents. The plan's words: each background run, schedule and
// sub-agent has its own budget; at the monthly cap, unattended work falls
// back to local models or stops, and says so; a per-tenant switch pauses
// all agents and schedules at once.
//
// Three things are here, and nothing that runs a turn:
//
//   - The run registry. Every unattended run is a row keyed by its own
//     request id, with its kind, its id and its budget. Its spend is the sum
//     of the usage records of that request id, and Gate, the turn runner's
//     gate, finds the run from a turn's request id. A kind of run gets a
//     budget by registering.
//   - At the cap or a run's budget, Moved, the turn runner's hook for a
//     route that moves past a model, lets the call go on to a local model
//     and keeps a notice of it, or ends the call when the person has
//     switched fallback off for unattended work.
//   - The switch, agents.paused, and its routes. The parts that run
//     unattended work read the switch themselves and say how many runs are
//     working and held.
//
// docs/architecture/unattended-spend.md is the contract.
package unattended

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/usage"
	"github.com/jonathanmkosar/keystone/settings"
)

// Kind is what sort of unattended run a run is.
type Kind string

const (
	// KindTask is a background task (P8-01) started from a chat.
	KindTask Kind = "task"
	// KindSchedule is a run of a scheduled task (P8-03): a background task
	// that works in a chat of its own.
	KindSchedule Kind = "schedule"
	// KindSubagent is a sub-agent (P8-02), which runs beside a turn and
	// reports to it. Nothing registers one until P8-02 lands.
	KindSubagent Kind = "subagent"
)

// Noun is the kind as a person reads it in a sentence.
func (k Kind) Noun() string {
	switch k {
	case KindSchedule:
		return "scheduled task"
	case KindSubagent:
		return "sub-agent"
	}
	return "background task"
}

// The person's settings this package reads, in settings/agents.json.
const (
	// KeyTaskBudget is a background task's budget, in US dollars, when the
	// person gives the task none of its own.
	KeyTaskBudget = "agents.task_budget_usd"
	// KeyScheduleBudget is the budget of each run of a schedule that has
	// none of its own.
	KeyScheduleBudget = "agents.schedule_budget_usd"
	// KeySubagentBudget is a sub-agent's budget.
	KeySubagentBudget = "agents.subagent_budget_usd"
	// KeyFallback says whether unattended work may go on with a local model
	// when the monthly cap or its own budget refuses a call.
	KeyFallback = "agents.unattended_fallback"
	// KeyPaused is the switch that pauses every agent and schedule.
	KeyPaused = "agents.paused"
)

// BudgetKey is the setting that holds the budget of a kind of run.
func BudgetKey(k Kind) string {
	switch k {
	case KindSchedule:
		return KeyScheduleBudget
	case KindSubagent:
		return KeySubagentBudget
	}
	return KeyTaskBudget
}

// ErrInvalid says a run or a budget was refused.
var ErrInvalid = errors.New("unattended: refused")

// Run is one unattended run.
type Run struct {
	Kind Kind
	// ID is the run's own id within its kind: the task's id for a task and
	// a scheduled run, and the sub-agent's for a sub-agent.
	ID string
	// RequestID is the request id every turn of the run is sent under. It
	// is the run's key.
	RequestID string
	// Title names the run for a person, in the notices.
	Title string
	// BudgetMicros is the most the run may spend on models that are not
	// local, in millionths of a US dollar.
	BudgetMicros int64
}

// Execer is a database or a transaction, to write a run with the row it
// belongs to.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Times are stored as the chat record stores them.
const timeLayout = "2006-01-02T15:04:05.000Z"

func stamp() string { return time.Now().UTC().Format(timeLayout) }

// Register writes a run. A caller writes it in the transaction that writes
// the run's own row, before any turn of the run can be sent, so no call of
// the run is ever made before its budget is known.
func Register(ctx context.Context, q Execer, r Run) error {
	switch {
	case r.Kind != KindTask && r.Kind != KindSchedule && r.Kind != KindSubagent:
		return fmt.Errorf("%w: %q is not a kind of run", ErrInvalid, r.Kind)
	case strings.TrimSpace(r.RequestID) == "":
		return fmt.Errorf("%w: a run needs the request id its turns are sent under", ErrInvalid)
	case r.BudgetMicros < 0:
		return fmt.Errorf("%w: a budget cannot be negative", ErrInvalid)
	}
	_, err := q.ExecContext(ctx, "INSERT INTO unattended_runs(request_id, kind, ref_id, title, budget_micros, created_at) VALUES(?, ?, ?, ?, ?, ?)",
		r.RequestID, string(r.Kind), r.ID, r.Title, r.BudgetMicros, stamp())
	return err
}

func scanRun(row *sql.Row) (Run, bool, error) {
	var r Run
	var kind string
	err := row.Scan(&r.RequestID, &kind, &r.ID, &r.Title, &r.BudgetMicros)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	r.Kind = Kind(kind)
	return r, true, nil
}

// ForRequest returns the run whose turns are sent under requestID, and
// false when no run is.
func ForRequest(ctx context.Context, q queryer, requestID string) (Run, bool, error) {
	return scanRun(q.QueryRowContext(ctx, "SELECT request_id, kind, ref_id, title, budget_micros FROM unattended_runs WHERE request_id = ?", requestID))
}

// ForTurn returns the run a turn belongs to, by the request id it was sent
// under, and false when it is no run's: a turn the person sent in a chat.
func ForTurn(ctx context.Context, s *tenant.Store, turnID string) (Run, bool, error) {
	return scanRun(s.DB().QueryRowContext(ctx, `SELECT u.request_id, u.kind, u.ref_id, u.title, u.budget_micros
		FROM unattended_runs u JOIN turns t ON t.request_id = u.request_id WHERE t.id = ?`, turnID))
}

// Spent is what a run has spent on models that are not local, from the
// usage records of its request id: every model call it made, the ones that
// failed and the summaries of its chat among them.
func Spent(ctx context.Context, s *tenant.Store, requestID string) (int64, error) {
	recs, err := usage.List(ctx, s, usage.Filter{RequestID: requestID})
	if err != nil {
		return 0, err
	}
	var n int64
	for _, r := range recs {
		if usage.Counts(r.Class) {
			n += r.CostMicros
		}
	}
	return n, nil
}

// Micros is an amount in US dollars in millionths of a dollar. It refuses
// one that is negative or not a number.
func Micros(usd float64) (int64, error) {
	if math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0 {
		return 0, fmt.Errorf("%w: a budget is a number of US dollars, 0 or more", ErrInvalid)
	}
	c, err := usage.NewCap(usd, 0)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return c.LimitMicros, nil
}

var registry = sync.OnceValues(settings.Registry)

// setting reads one of the person's settings: theirs, or the default.
func setting(ctx context.Context, s *tenant.Store, key string) (string, error) {
	reg, err := registry()
	if err != nil {
		return "", err
	}
	v, err := tenantsettings.Get(ctx, s, reg, key)
	return strings.TrimSpace(v), err
}

// Budget is the person's budget for a run of a kind that is given none of
// its own, in millionths of a US dollar.
func Budget(ctx context.Context, s *tenant.Store, k Kind) (int64, error) {
	key := BudgetKey(k)
	v, err := setting(ctx, s, key)
	if err != nil {
		return 0, err
	}
	usd, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	n, err := Micros(usd)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

// Fallback reports whether the person lets unattended work go on with a
// local model when the cap or its budget refuses a call.
func Fallback(ctx context.Context, s *tenant.Store) (bool, error) {
	v, err := setting(ctx, s, KeyFallback)
	if err != nil {
		return false, err
	}
	return strconv.ParseBool(v)
}

// Paused reports whether the person's switch that pauses every agent and
// schedule is on. A caller that gets an error counts the switch as on:
// unattended work does not go on when the person's wish cannot be read.
func Paused(ctx context.Context, s *tenant.Store) (bool, error) {
	// P15-02: a person the admin disabled is paused, whatever their switch.
	if _, off, err := s.Disabled(ctx); off || err != nil {
		return true, err
	}
	v, err := setting(ctx, s, KeyPaused)
	if err != nil {
		return true, err
	}
	on, err := strconv.ParseBool(v)
	if err != nil {
		return true, fmt.Errorf("%s: %w", KeyPaused, err)
	}
	return on, nil
}

// SetPaused turns the switch on or off, as the person.
func SetPaused(ctx context.Context, s *tenant.Store, on bool) error {
	reg, err := registry()
	if err != nil {
		return err
	}
	return tenantsettings.Set(ctx, s, reg, tenantsettings.Person, KeyPaused, strconv.FormatBool(on))
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
