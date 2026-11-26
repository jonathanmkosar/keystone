package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

// Why a round was sent, which decides the message it is sent with.
const (
	whyTask     = "task"     // the first round: the task's own words
	whyContinue = "continue" // the round before reached the step limit
	whyResume   = "resume"   // a restart cut the round before short
)

// The prefix of a work chat's title.
const workTitlePrefix = "Background task: "

// The errors Start gives, beside the chat record's.
var (
	errInvalid = errors.New("the task is refused")
	errTooMany = errors.New("too many background tasks are open")
)

// Options is everything a Runner is made with.
type Options struct {
	// Install is the data folder. The runner opens a store of its own for
	// each task it works.
	Install *tenant.Install
	// Log is the server's own log. Nil means none.
	Log *api.Log
	// Config is the install's settings for tasks.
	Config Config
	// Turns is what turns run with: how often a stream looks for news, how
	// many events it reads at a time, how long a stop waits, and the chat
	// record's limits.
	Turns turn.Config
}

// Runner starts and works background tasks. It keeps nothing a restart
// would lose: a task, its rounds and its events are rows. What it holds in
// memory is which tasks have a goroutine at work, and who is waiting to
// be told a task has news.
type Runner struct {
	o Options

	mu      sync.Mutex
	turns   *turn.Runner
	working map[string]bool          // tenant/task: a goroutine is working it
	waiting map[string]chan struct{} // tenant/task (the worker) and tenant/task/events (streams)
}

// NewRunner returns a runner. It works nothing until it is bound to the
// turn runner, with Bind.
func NewRunner(o Options) *Runner {
	return &Runner{o: o, working: map[string]bool{}, waiting: map[string]chan struct{}{}}
}

// Bind gives the runner the turn runner its tasks' rounds run on. The
// first one given stays. A task's goroutine runs under the turn runner, so
// closing the turn runner stops it and waits for it, and a task that was
// running is finished by Recover when a server next starts.
func (r *Runner) Bind(t *turn.Runner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turns == nil {
		r.turns = t
	}
}

func (r *Runner) bound() *turn.Runner {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.turns
}

func (r *Runner) logf(format string, args ...any) { r.o.Log.Printf(format, args...) }

func (r *Runner) waitFor(key string) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := r.waiting[key]
	if ch == nil {
		ch = make(chan struct{})
		r.waiting[key] = ch
	}
	return ch
}

func (r *Runner) wake(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch := r.waiting[key]; ch != nil {
		close(ch)
		delete(r.waiting, key)
	}
}

// Recover starts the work of every task a stopped server left unfinished,
// for every tenant: those waiting or running, and those that ended and
// whose result is not yet in their chat. The turn runner's Recover runs
// first, so a round the stop cut short is already closed as interrupted,
// and the task resumes with another.
func (r *Runner) Recover() {
	if r.o.Install == nil {
		return
	}
	for _, t := range r.o.Install.Tenants() {
		s, err := r.o.Install.Store(t.ID)
		if err != nil {
			r.logf("tasks: recovering tenant %s: %v", t.ID, err)
			continue
		}
		ids, err := unfinished(context.Background(), s)
		s.Close()
		if err != nil {
			r.logf("tasks: recovering tenant %s: %v", t.ID, err)
			continue
		}
		for _, id := range ids {
			r.kick(t.ID, id)
		}
	}
}

// kick starts a goroutine that works a task, unless one is at work.
func (r *Runner) kick(tenantID, id string) {
	key := tenantID + "/" + id
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turns == nil || r.working[key] {
		return
	}
	r.working[key] = true
	if !r.turns.Go(func(ctx context.Context) { r.work(ctx, tenantID, id) }) {
		delete(r.working, key)
	}
}

// Start writes a task, with its work chat, and starts its work. chatID is
// the chat it is started from, startedRequest the API call that started
// it, actor who it runs for, and startedBy person or tool.
func (r *Runner) Start(ctx context.Context, s *tenant.Store, chatID, startedRequest, actor, startedBy string, body StartBody) (Task, error) {
	text := strings.TrimSpace(body.Text)
	if text == "" {
		return Task{}, fmt.Errorf("%w: the task is empty: say what to do", errInvalid)
	}
	if _, err := chat.Get(s, chatID); err != nil {
		return Task{}, err
	}
	var inWork int
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE work_chat_id = ?", chatID).Scan(&inWork); err != nil {
		return Task{}, err
	}
	if inWork > 0 {
		return Task{}, fmt.Errorf("%w: a background task cannot start another background task", errInvalid)
	}
	budget, err := r.budget(ctx, s, body, unattended.KindTask)
	if err != nil {
		return Task{}, err
	}
	model := ""
	if t := r.bound(); t != nil {
		model = t.ChatModel(ctx, s, chatID)
	}
	title := r.titleOf(body.Title, text)
	tooMany := func(q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	}) error {
		var open int
		if err := q.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE status IN ('queued', 'running')").Scan(&open); err != nil {
			return err
		}
		if open >= r.o.Config.OpenMax {
			return fmt.Errorf("%w: %d are waiting or running, the most one person may have", errTooMany, open)
		}
		return nil
	}
	if err := tooMany(s.DB()); err != nil {
		return Task{}, err
	}
	work, err := chat.Create(s)
	if err != nil {
		return Task{}, err
	}
	drop := func(err error) (Task, error) {
		if derr := chat.Delete(s, work.ID); derr != nil {
			r.logf("tasks: removing the chat made for a task that did not start: %v", derr)
		}
		return Task{}, err
	}
	if _, err := chat.Rename(s, r.o.Turns.Limits, work.ID, workTitlePrefix+title); err != nil {
		return drop(err)
	}
	// A task's rounds carry the words of the chat it was started from, so a
	// task from an incognito chat works in an incognito chat: no memory pass
	// reads its rounds (P6-06). A flag that cannot be read counts as on.
	if on, err := memory.Incognito(ctx, s, chatID); err != nil || on {
		if err := memory.SetIncognito(ctx, s, work.ID, true); err != nil {
			return drop(err)
		}
	}
	id, err := newID()
	if err != nil {
		return drop(err)
	}
	var j row
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		if err := tooMany(tx); err != nil {
			return err
		}
		requestID := audit.NewRequestID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO jobs(id, chat_id, work_chat_id, title, task, started_by, status, request_id,
			started_request, actor, model, budget_micros, max_rounds, progress, created_at)
			VALUES(?, ?, ?, ?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, chatID, work.ID, title, text, startedBy, requestID, startedRequest, actor, model,
			budget, r.o.Config.MaxRounds, "Waiting to start", stamp()); err != nil {
			return err
		}
		// The task is an unattended run, with its budget, before any of its
		// turns can be sent (P8-05).
		if err := unattended.Register(ctx, tx, unattended.Run{Kind: unattended.KindTask, ID: id, RequestID: requestID, Title: title, BudgetMicros: budget}); err != nil {
			return err
		}
		j, err = emitTask(ctx, tx, id, EventTask)
		return err
	})
	if err != nil {
		return drop(err)
	}
	r.kick(s.TenantID(), id)
	return j.Task, nil
}

// budget is a task's budget, in millionths of a dollar: the one its body
// gives (P8-05), or the person's for its kind, agents.task_budget_usd or
// agents.schedule_budget_usd.
func (r *Runner) budget(ctx context.Context, s *tenant.Store, body StartBody, kind unattended.Kind) (int64, error) {
	if body.BudgetUSD != nil {
		n, err := unattended.Micros(*body.BudgetUSD)
		if err != nil {
			return 0, fmt.Errorf("%w: %s", errInvalid, strings.TrimPrefix(err.Error(), unattended.ErrInvalid.Error()+": "))
		}
		return n, nil
	}
	return unattended.Budget(ctx, s, kind)
}

// titleOf is a task's title: the one given, or the first words of the
// task, on one line, short enough that the work chat's title fits.
func (r *Runner) titleOf(given, text string) string {
	words := strings.Fields(given)
	if len(words) == 0 {
		words = strings.Fields(text)
		if len(words) > 6 {
			words = words[:6]
		}
	}
	title := []rune(strings.Join(words, " "))
	most := r.o.Turns.Limits.TitleLength - len([]rune(workTitlePrefix))
	if most < 1 {
		most = 1
	}
	if len(title) > most {
		title = append([]rune(strings.TrimSpace(string(title[:most-1]))), '…')
	}
	return string(title)
}

// watch is the round a task's goroutine is following: its turn, and the
// newest of the turn's events it has read.
type watch struct {
	turnID string
	after  int64
	words  string
}

// work is a task's goroutine. It reads the task, does the next thing it
// needs, and waits for news, until the task has ended and its result is
// in the chat it was started from. It has a store of its own, because the
// store a call came with closes when the call ends.
func (r *Runner) work(ctx context.Context, tenantID, id string) {
	key := tenantID + "/" + id
	defer func() {
		r.mu.Lock()
		delete(r.working, key)
		delete(r.waiting, key) // only this goroutine waits on the task's own key
		r.mu.Unlock()
	}()
	s, err := r.o.Install.Store(tenantID)
	if err != nil {
		r.logf("tasks: opening the store of tenant %s: %v", tenantID, err)
		return
	}
	defer s.Close()
	w := &watch{}
	var last row
	for ctx.Err() == nil {
		news := r.waitFor(key)
		j, err := getJob(ctx, s.DB(), id)
		if errors.Is(err, errNoTask) {
			// It went with the chat it was started from: its round goes too.
			if last.ID != "" {
				r.stopRound(ctx, s, last)
			}
			return
		}
		if err != nil {
			if ctx.Err() == nil {
				r.logf("tasks: reading task %s of tenant %s: %v", id, tenantID, err)
			}
			return
		}
		last = j
		if ended(j.Status) {
			r.deliver(ctx, s, j)
			return
		}
		turnNews, wait := r.next(ctx, s, j, w)
		if !wait {
			continue
		}
		select {
		case <-news:
		case <-turnNews:
		case <-time.After(r.o.Turns.StreamPoll):
		case <-ctx.Done():
			return
		}
	}
}

// next does the next thing a task that has not ended needs, and says
// whether to wait for news before looking again, and on what.
func (r *Runner) next(ctx context.Context, s *tenant.Store, j row, w *watch) (<-chan struct{}, bool) {
	if j.stopAsked {
		r.stopRound(ctx, s, j)
		r.finish(ctx, s, j, StatusStopped, nil, "")
		return nil, false
	}
	if done, wait := r.pause(ctx, s, j); done { // the switch that pauses every agent (P8-05, pause.go)
		return nil, wait
	}
	if j.Status == StatusQueued {
		if _, err := r.change(ctx, s, j.ID, "status = 'running', started_at = ?, progress = ?", stamp(), "Starting"); err != nil {
			r.logf("tasks: %s starting task %s: %v", j.requestID, j.ID, err)
			return nil, true
		}
		return nil, false
	}
	cur, rounds, ok, err := turn.Latest(ctx, s, j.WorkChatID, j.requestID)
	if err != nil {
		if ctx.Err() == nil {
			r.logf("tasks: %s reading the round of task %s: %v", j.requestID, j.ID, err)
		}
		return nil, true
	}
	if !ok {
		if j.Rounds > 0 {
			r.finish(ctx, s, j, StatusFailed, &TaskError{Code: CodeChatDeleted, Message: "Its chat was deleted, so it stopped."}, "")
			return nil, false
		}
		return nil, !r.send(ctx, s, j, whyTask, 1)
	}
	if cur.Status == turn.StatusQueued || cur.Status == turn.StatusRunning {
		news := r.bound().Changed(s.TenantID(), cur.ID)
		r.follow(ctx, s, j, cur, rounds, w)
		return news, true
	}
	r.follow(ctx, s, j, cur, rounds, w)
	r.tell(ctx, s, j, cur) // the round's calls the cap or its budget refused, in its chat (P8-05, pause.go)
	return nil, !r.after(ctx, s, j, cur, rounds)
}

// after decides what follows a round that has ended, and reports whether
// it did something.
func (r *Runner) after(ctx context.Context, s *tenant.Store, j row, cur turn.Turn, rounds int) bool {
	interrupted := cur.Status == turn.StatusFailed && cur.Error != nil && cur.Error.Code == turn.CodeInterrupted
	switch {
	case cur.Status == turn.StatusDone && cur.StopReason != turn.StopStepLimit:
		result, err := turn.ReplyText(s, cur)
		if err != nil {
			r.logf("tasks: %s reading the result of task %s: %v", j.requestID, j.ID, err)
		}
		return r.finish(ctx, s, j, StatusDone, nil, strings.TrimSpace(result))
	case cur.Status == turn.StatusDone || interrupted:
		if rounds >= j.MaxRounds {
			return r.finish(ctx, s, j, StatusFailed, &TaskError{Code: CodeRoundLimit,
				Message: fmt.Sprintf("It took %d rounds, the most a background task may take, and had not finished.", j.MaxRounds)}, "")
		}
		why := whyContinue
		if interrupted {
			why = whyResume
		}
		return r.send(ctx, s, j, why, rounds+1)
	case cur.Status == turn.StatusStopped:
		return r.finish(ctx, s, j, StatusStopped, nil, "")
	}
	e := &TaskError{Code: CodeFailed, Message: "Its round failed inside Keystone. The reason is in the server's log."}
	if cur.Error != nil {
		e = &TaskError{Code: cur.Error.Code, Message: cur.Error.Message}
	}
	return r.finish(ctx, s, j, StatusFailed, e, "")
}

// send sends a task's next round, and reports whether it did something: a
// round sent or the task ended. The first round carries the marks of the
// chat the task was started from into the work chat.
func (r *Runner) send(ctx context.Context, s *tenant.Store, j row, why string, round int) bool {
	if _, err := chat.Get(s, j.WorkChatID); errors.Is(err, chat.ErrNotFound) {
		return r.finish(ctx, s, j, StatusFailed, &TaskError{Code: CodeChatDeleted, Message: "Its chat was deleted, so it stopped."}, "")
	}
	text, words := j.Task.Task, "Starting"
	switch why {
	case whyContinue:
		text, words = r.o.Config.ContinueText, fmt.Sprintf("Carrying on, round %d", round)
	case whyResume:
		text, words = r.o.Config.ResumeText, "Resuming after a restart"
	case whyUnpause: // P8-05, pause.go
		text, words = r.o.Config.UnpauseText, "Going on after a pause"
	}
	var carry turn.Carry
	if why == whyTask {
		carry = r.bound().Carried(ctx, s, j.ChatID)
		if carry.Outside != "" {
			carry.Outside = "the chat this task was started from had read outside content"
		}
	}
	t, err := r.bound().Send(ctx, s, j.WorkChatID, j.requestID, j.actor, turn.SendBody{Text: text, Model: j.model}, carry)
	switch {
	case err == nil:
	case errors.Is(err, turn.ErrClosing) || ctx.Err() != nil:
		return false
	case errors.Is(err, chat.ErrNotFound):
		return r.finish(ctx, s, j, StatusFailed, &TaskError{Code: CodeChatDeleted, Message: "Its chat was deleted, so it stopped."}, "")
	default:
		// A full queue in the work chat, the person's own turns: it waits.
		r.logf("tasks: %s sending round %d of task %s: %v", j.requestID, round, j.ID, err)
		return false
	}
	if _, err := r.change(ctx, s, j.ID, "rounds = ?, turn_id = ?, waiting = 0, progress = ?", round, t.ID, words); err != nil && !errors.Is(err, errNoTask) {
		r.logf("tasks: %s noting round %d of task %s: %v", j.requestID, round, j.ID, err)
	}
	return true
}

// follow reads what the round's turn has done since it last looked, and
// writes where the task is when that changed: its step, the words for what
// it is doing, whether a call waits for the person, and what it has spent.
func (r *Runner) follow(ctx context.Context, s *tenant.Store, j row, cur turn.Turn, rounds int, w *watch) {
	if w.turnID != cur.ID {
		*w = watch{turnID: cur.ID}
	}
	// A delta changes the words at most; any other event may change the
	// step, the wait or the spend, which are read from the record.
	words, changed := w.words, false
	for {
		events, err := turn.Events(ctx, s, cur.ID, w.after, r.o.Turns.StreamBatch)
		if err != nil {
			if ctx.Err() == nil {
				r.logf("tasks: %s reading the events of task %s: %v", j.requestID, j.ID, err)
			}
			return
		}
		for _, e := range events {
			w.after = e.ID
			switch e.Name {
			case turn.EventText:
				words = "Writing"
			case turn.EventThinking:
				words = "Thinking"
			case turn.EventStart:
				words, changed = "Working out the next step", true
			case turn.EventToolCall:
				var call turn.ToolCallEvent
				if json.Unmarshal(e.Data, &call) == nil && call.Name != "" {
					words = "Using " + call.Name
				}
				changed = true
			default:
				changed = true
			}
		}
		if len(events) < r.o.Turns.StreamBatch {
			break
		}
	}
	if !changed && words == w.words {
		return
	}
	w.words = words
	if words == "" {
		words = j.Progress
	}
	pending, err := turn.Pending(ctx, s, cur.ID)
	if err != nil {
		r.logf("tasks: %s reading whether task %s waits for an approval: %v", j.requestID, j.ID, err)
	}
	if pending != "" {
		words = "Waiting for your approval to run " + pending
	}
	steps, err := turn.Calls(ctx, s, j.WorkChatID, j.requestID)
	if err != nil {
		steps = j.Steps
	}
	spent, err := Spent(ctx, s, j.requestID)
	if err != nil {
		spent = j.SpentMicros
	}
	if steps == j.Steps && (pending != "") == j.Waiting && words == j.Progress && spent == j.SpentMicros && rounds == j.Rounds && cur.ID == j.TurnID {
		return
	}
	if _, err := r.change(ctx, s, j.ID, "steps = ?, waiting = ?, progress = ?, spent_micros = ?, rounds = ?, turn_id = ?",
		steps, pending != "", words, spent, rounds, cur.ID); err != nil && !errors.Is(err, errNoTask) {
		r.logf("tasks: %s noting where task %s is: %v", j.requestID, j.ID, err)
	}
}

// stopRound stops the task's round, if one is open.
func (r *Runner) stopRound(ctx context.Context, s *tenant.Store, j row) {
	cur, _, ok, err := turn.Latest(ctx, s, j.WorkChatID, j.requestID)
	if err != nil || !ok || (cur.Status != turn.StatusQueued && cur.Status != turn.StatusRunning) {
		return
	}
	if _, err := r.bound().Stop(ctx, s, j.WorkChatID, cur.ID); err != nil && !errors.Is(err, turn.ErrNoTurn) && ctx.Err() == nil {
		r.logf("tasks: %s stopping the round of task %s: %v", j.requestID, j.ID, err)
	}
}

// finish ends a task, and reports true: it did something.
func (r *Runner) finish(ctx context.Context, s *tenant.Store, j row, status string, failure *TaskError, result string) bool {
	text := ""
	if failure != nil {
		raw, _ := json.Marshal(failure)
		text = string(raw)
	}
	words := "Done"
	switch status {
	case StatusStopped:
		words = "Stopped"
	case StatusFailed:
		words = "Did not finish"
	}
	noteTurn, err := newID()
	if err != nil {
		r.logf("tasks: %s ending task %s: %v", j.requestID, j.ID, err)
		return true
	}
	steps, err := turn.Calls(ctx, s, j.WorkChatID, j.requestID)
	if err != nil {
		steps = j.Steps
	}
	spent, err := Spent(ctx, s, j.requestID)
	if err != nil {
		spent = j.SpentMicros
	}
	if _, err := r.change(ctx, s, j.ID, `status = ?, error = ?, result = ?, waiting = 0, progress = ?, steps = ?, spent_micros = ?,
		note_turn_id = CASE WHEN note_turn_id = '' THEN ? ELSE note_turn_id END, ended_at = ?`,
		status, text, result, words, steps, spent, noteTurn, stamp()); err != nil && !errors.Is(err, errNoTask) {
		r.logf("tasks: %s ending task %s: %v", j.requestID, j.ID, err)
	}
	return true
}

// deliver writes a task's result in the chat it was started from, as a
// note, and sends the end event. A chat that holds the result of a task
// whose chat read outside content, or was held for sensitive content, is
// marked so too, before another of its turns can run.
func (r *Runner) deliver(ctx context.Context, s *tenant.Store, j row) {
	if j.NoteMessageID != 0 {
		return
	}
	// A task that worked in the chat it was started from (P8-03, inchat.go):
	// a reply is its result, already in the chat.
	if inPlace(j) && r.deliverInPlace(ctx, s, j) {
		return
	}
	if j.noteTurnID == "" {
		r.logf("tasks: %s task %s ended with no turn for its note", j.requestID, j.ID)
		return
	}
	t := r.bound()
	carry := t.Carried(ctx, s, j.WorkChatID)
	if carry.Outside != "" {
		carry.Outside = "this chat holds the result of a background task that read outside content"
	}
	blocks := []seam.Block{{Type: seam.BlockText, Text: r.withNotices(ctx, s, j, noteText(j))}} // P8-05: and any model that stood in
	msg, err := t.Note(ctx, s.TenantID(), j.ChatID, j.noteTurnID, j.requestID, j.actor, blocks, carry)
	if err != nil {
		if !errors.Is(err, chat.ErrNotFound) && !errors.Is(err, turn.ErrClosing) && ctx.Err() == nil {
			r.logf("tasks: %s writing the result of task %s in its chat, which is tried again at the next start: %v", j.requestID, j.ID, err)
		}
		return
	}
	key := s.TenantID() + "/" + j.ID
	err = inTx(ctx, s, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET note_message_id = ? WHERE id = ?", msg.ID, j.ID); err != nil {
			return err
		}
		_, err := emitTask(ctx, tx, j.ID, EventEnd)
		return err
	})
	if err != nil && !errors.Is(err, errNoTask) {
		r.logf("tasks: %s noting the result of task %s: %v", j.requestID, j.ID, err)
	}
	r.wake(key + "/events")
}

// noteText is what a task's note says in the chat it was started from.
func noteText(j row) string {
	if inPlace(j) { // P8-03, inchat.go
		return inPlaceNote(j)
	}
	switch j.Status {
	case StatusDone:
		if j.Result == "" {
			return fmt.Sprintf("Background task “%s” is done. It gave no result in words: its chat has what it did.", j.Title)
		}
		return fmt.Sprintf("Background task “%s” is done. Its result:\n\n%s", j.Title, j.Result)
	case StatusStopped:
		return fmt.Sprintf("Background task “%s” was stopped before it finished. Its chat has what it did.", j.Title)
	}
	why := "It failed."
	if j.Error != nil && j.Error.Message != "" {
		why = j.Error.Message
	}
	return fmt.Sprintf("Background task “%s” did not finish. %s Its chat has what it did.", j.Title, why)
}

// change writes to a task's row, sends the task as it then stands, and
// wakes its streams.
func (r *Runner) change(ctx context.Context, s *tenant.Store, id, set string, args ...any) (row, error) {
	j, err := change(ctx, s, id, set, args...)
	if err == nil {
		r.wake(s.TenantID() + "/" + id + "/events")
	}
	return j, err
}
