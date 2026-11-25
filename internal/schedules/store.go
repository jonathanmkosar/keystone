package schedules

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// errNoSchedule says the schedule is not in this tenant's record.
var errNoSchedule = errors.New("no such schedule")

// Times are stored as the chat record stores them: text of one fixed width,
// in UTC, so they sort as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

func format(t time.Time) string { return t.UTC().Format(timeLayout) }

func parse(s string) (time.Time, error) { return time.Parse(timeLayout, s) }

// row is a schedule as it is kept.
type row struct {
	id, name, prompt string
	rule             rule
	model            string
	incognito        bool
	enabled          bool
	zone             string
	next             time.Time // zero when none
	created, updated time.Time
}

const scheduleColumns = `id, name, prompt, kind, hour, minute, days, on_date, model, incognito, enabled, zone, next_at, created_at, updated_at`

func scanSchedule(sc interface{ Scan(...any) error }) (row, error) {
	var r row
	var kind, days, onDate, next, created, updated string
	var hour, minute int
	err := sc.Scan(&r.id, &r.name, &r.prompt, &kind, &hour, &minute, &days, &onDate, &r.model, &r.incognito, &r.enabled, &r.zone, &next, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, errNoSchedule
	}
	if err != nil {
		return row{}, err
	}
	rec := Recurrence{Kind: kind, Time: fmt.Sprintf("%02d:%02d", hour, minute), Minute: minute, Date: onDate}
	if days != "" {
		rec.Days = strings.Split(days, ",")
	}
	if r.rule, err = readRule(rec); err != nil {
		return row{}, fmt.Errorf("schedule %s: its stored recurrence does not read: %w", r.id, err)
	}
	if next != "" {
		if r.next, err = parse(next); err != nil {
			return row{}, fmt.Errorf("schedule %s: %w", r.id, err)
		}
	}
	if r.created, err = parse(created); err != nil {
		return row{}, fmt.Errorf("schedule %s: %w", r.id, err)
	}
	if r.updated, err = parse(updated); err != nil {
		return row{}, fmt.Errorf("schedule %s: %w", r.id, err)
	}
	return r, nil
}

// stored is a rule's columns: kind, hour, minute, days and on_date.
func (ru rule) stored() (kind string, hour, minute int, days, onDate string) {
	var names []string
	for _, d := range ru.days {
		names = append(names, nameOf(d))
	}
	if ru.kind == KindOnce {
		onDate = ru.date.Format("2006-01-02")
	}
	return ru.kind, ru.hour, ru.minute, strings.Join(names, ","), onDate
}

func nextText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return format(t)
}

func getSchedule(ctx context.Context, q queryer, id string) (row, error) {
	return scanSchedule(q.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id = ?", id))
}

func listSchedules(ctx context.Context, s *tenant.Store, where string, args ...any) ([]row, error) {
	q := "SELECT " + scheduleColumns + " FROM schedules"
	if where != "" {
		q += " WHERE " + where
	}
	rows, err := s.DB().QueryContext(ctx, q+" ORDER BY seq", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		r, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// runRow is a run as it is kept.
type runRow struct {
	id, scheduleID, cause string
	due                   time.Time
	missed                int
	late                  bool
	lateReason            string
	state                 string
	requestID, actor      string
	chatID, jobID         string
	err                   string
	created               time.Time
}

const runColumns = `id, schedule_id, cause, due_at, missed, late, late_reason, state, request_id, actor, chat_id, job_id, error, created_at`

func scanRun(sc interface{ Scan(...any) error }) (runRow, error) {
	var r runRow
	var due, created string
	err := sc.Scan(&r.id, &r.scheduleID, &r.cause, &due, &r.missed, &r.late, &r.lateReason, &r.state, &r.requestID, &r.actor, &r.chatID, &r.jobID, &r.err, &created)
	if err != nil {
		return runRow{}, err
	}
	if r.due, err = parse(due); err != nil {
		return runRow{}, fmt.Errorf("run %s: %w", r.id, err)
	}
	if r.created, err = parse(created); err != nil {
		return runRow{}, fmt.Errorf("run %s: %w", r.id, err)
	}
	return r, nil
}

func listRuns(ctx context.Context, s *tenant.Store, tail string, args ...any) ([]runRow, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT "+runColumns+" FROM schedule_runs "+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []runRow
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// lastRun returns a schedule's newest run, and false when it has none.
func lastRun(ctx context.Context, s *tenant.Store, scheduleID string) (runRow, bool, error) {
	got, err := listRuns(ctx, s, "WHERE schedule_id = ? ORDER BY seq DESC LIMIT 1", scheduleID)
	if err != nil || len(got) == 0 {
		return runRow{}, false, err
	}
	return got[0], true, nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// inTx runs f in one transaction, and rolls back if f fails.
func inTx(ctx context.Context, s *tenant.Store, f func(tx *sql.Tx) error) error {
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
