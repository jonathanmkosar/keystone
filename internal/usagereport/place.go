package usagereport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// A usage record names no chat and no task: it carries the request id of
// the API call that set it off, which every model call made for that call
// carries too (usage.md). A placer finds, for a request id, the chat and
// the kind of work its calls are put under, from what the tenant's
// database still holds, in this order (decision 0430):
//
//  1. A background task whose own request id it is (jobs.request_id):
//     the work is a background task, or a scheduled task when a schedule
//     run started it (schedule_runs.job_id), and the chat is the task's
//     work chat. A task's turns, titles, summaries and memory passes all
//     carry its request id, as its budget counts them.
//  2. A turn sent under it (turns.request_id): the turn's chat. A chat
//     reply, its title, a summary of its oldest turns, a recap and its
//     memory pass are all made under the turn's request id.
//  3. The API call itself, from the audit log, when its path is under
//     /api/v1/chats/{id}/: that chat. A recap asked for by the recap
//     route is one.
//
// A chat found that way that the tenant no longer has, or none at all,
// puts the calls on the line of calls in no chat the person still has. A
// record's kind of work is its own kind unless it belongs to a task.

// place is where one request id's calls are put.
type place struct {
	chatID, chatTitle string
	// task says the request id is a background task's, and scheduled that
	// a schedule run started it.
	task, scheduled bool
}

// placed is one record's line in the chat and kind breakdowns.
type placed struct {
	chatID, chatTitle, work string
}

type placer struct {
	s      *tenant.Store
	known  map[string]place
	titles map[string]*string // nil: no such chat
	task   *sql.Stmt
	turn   *sql.Stmt
	chat   *sql.Stmt
}

func newPlacer(ctx context.Context, s *tenant.Store) (*placer, error) {
	p := &placer{s: s, known: map[string]place{}, titles: map[string]*string{}}
	var err error
	prepare := func(q string) *sql.Stmt {
		if err != nil {
			return nil
		}
		var st *sql.Stmt
		st, err = s.DB().PrepareContext(ctx, q)
		return st
	}
	p.task = prepare(`SELECT j.work_chat_id, EXISTS(SELECT 1 FROM schedule_runs r WHERE r.job_id = j.id)
		FROM jobs j WHERE j.request_id = ?`)
	p.turn = prepare("SELECT chat_id FROM turns WHERE request_id = ? ORDER BY seq LIMIT 1")
	p.chat = prepare("SELECT title FROM chats WHERE id = ?")
	if err != nil {
		p.close()
		return nil, fmt.Errorf("usage report: %w", err)
	}
	return p, nil
}

func (p *placer) close() {
	for _, st := range []*sql.Stmt{p.task, p.turn, p.chat} {
		if st != nil {
			st.Close()
		}
	}
}

// place is one record's chat and kind of work.
func (p *placer) place(ctx context.Context, r usage.Record) (placed, error) {
	pl, err := p.of(ctx, r.RequestID)
	if err != nil {
		return placed{}, err
	}
	out := placed{chatID: pl.chatID, chatTitle: pl.chatTitle, work: r.Kind}
	switch {
	case pl.scheduled:
		out.work = WorkSchedule
	case pl.task:
		out.work = WorkTask
	}
	return out, nil
}

// of is where one request id's calls are put, read once.
func (p *placer) of(ctx context.Context, requestID string) (place, error) {
	if pl, ok := p.known[requestID]; ok || requestID == "" {
		return pl, nil
	}
	var pl place
	chatID, err := p.find(ctx, requestID, &pl)
	if err != nil {
		return place{}, fmt.Errorf("usage report: placing request %s: %w", requestID, err)
	}
	if chatID != "" {
		title, err := p.title(ctx, chatID)
		if err != nil {
			return place{}, fmt.Errorf("usage report: reading chat %s: %w", chatID, err)
		}
		if title != nil {
			pl.chatID, pl.chatTitle = chatID, *title
			if pl.chatTitle == "" {
				pl.chatTitle = labelChat
			}
		}
	}
	p.known[requestID] = pl
	return pl, nil
}

// find is the chat a request id points to, in the order above, and marks
// a task's.
func (p *placer) find(ctx context.Context, requestID string, pl *place) (string, error) {
	var chatID string
	err := p.task.QueryRowContext(ctx, requestID).Scan(&chatID, &pl.scheduled)
	switch {
	case err == nil:
		pl.task = true
		return chatID, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", err
	}
	err = p.turn.QueryRowContext(ctx, requestID).Scan(&chatID)
	switch {
	case err == nil:
		return chatID, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", err
	}
	calls, err := audit.List(ctx, p.s, audit.Filter{Kind: audit.KindAPI, RequestID: requestID, Limit: 1})
	if err != nil || len(calls) == 0 {
		return "", err
	}
	return chatInPath(calls[0].Action), nil
}

// chatPath is an API call's action, its method and path, when the path is
// one chat's or under it.
var chatPath = regexp.MustCompile(`^[A-Z]+ /api/v1/chats/([^/]+)(/|$)`)

// chatInPath is the chat an API call's action names, or "".
func chatInPath(action string) string {
	// The audit log holds the path as the server read it, unescaped.
	if m := chatPath.FindStringSubmatch(action); m != nil {
		return m[1]
	}
	return ""
}

// title is a chat's title, or nil when the tenant has no such chat.
func (p *placer) title(ctx context.Context, chatID string) (*string, error) {
	if t, ok := p.titles[chatID]; ok {
		return t, nil
	}
	var title string
	err := p.chat.QueryRowContext(ctx, chatID).Scan(&title)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		p.titles[chatID] = nil
		return nil, nil
	case err != nil:
		return nil, err
	}
	p.titles[chatID] = &title
	return &title, nil
}
