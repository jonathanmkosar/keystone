package subagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/subagents/carry"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

// The supervisor of one call of run_subagents. It runs under the turn
// runner (turn.Runner.Go), with a store of its own, so a call's sub-agents
// are seen to their end even though the tool's own goroutine is not waited
// for. At every look it reads the call's sub-agents and does what each
// needs: a sub-agent waiting for a lane is sent its turn while fewer than
// agents.subagents_at_once are working; one whose turn has ended is
// finished; one the person, the parent turn or the switch stopped has its
// turn stopped. When every one has ended it hands the tool its result.
//
//   - The parent turn stopped, or the call ran past its timeout: every
//     sub-agent still working is stopped and ended, and nothing is handed
//     back: the turn no longer waits.
//   - The runner closing: nothing more is written. The next start ends the
//     sub-agents as interrupted (Recover), as the turn runner closes the
//     parent turn.
//   - The switch that pauses every agent (agents.paused), read at every
//     look: every sub-agent still working is stopped through its turn's
//     context and ends as paused, and the turn gets the results at once
//     (decision 0484).
//
// A look comes when a sub-agent's turn has news, when a lane stops one,
// and at least every chats.stream_poll, but no more often than ten times a
// stream_poll, since a turn has news with every word it streams.

// supervise works one call's sub-agents to their end. base ends when the
// runner closes; call is the tool call's context, which ends when the turn
// is stopped or the call runs out of time.
func (r *Runner) supervise(base, call context.Context, b batch, out chan<- outcome) {
	s, err := r.o.Install.Store(b.tenantID)
	if err != nil {
		r.logf("sub-agents: opening the store of tenant %s: %v", b.tenantID, err)
		out <- outcome{err: errors.New("the sub-agents could not be run; the reason is in the server's log")}
		return
	}
	defer s.Close()
	defer r.forget(b.key)
	stopping := map[string]bool{} // the sub-agents whose turn this supervisor has stopped
	sent := map[string]string{}   // the turns this supervisor sent and could not yet note
	gap := max(r.o.Turns.StreamPoll/10, time.Millisecond)
	var last time.Time
	for base.Err() == nil {
		news := r.waitFor(b.key)
		if why := callEnded(call); why != "" {
			r.stopAll(base, s, b, why)
			return
		}
		rows, err := r.rows(base, s, b)
		if err != nil {
			if base.Err() == nil {
				r.logf("sub-agents: reading the sub-agents of tenant %s: %v", b.tenantID, err)
			}
			r.idle(base, call, news, nil)
			continue
		}
		paused, perr := unattended.Paused(base, s)
		if perr != nil && base.Err() == nil {
			r.logf("sub-agents: tenant %s: reading %s, so the sub-agents are stopped: %v", b.tenantID, unattended.KeyPaused, perr)
		}
		var waits []<-chan struct{}
		var toStop []row
		var queued []row
		working, open := 0, 0
		for _, a := range rows {
			if ended(a.Status) {
				continue
			}
			if paused && a.stopWhy == "" {
				if err := change(base, s, a.ID, "stop_why = ?", byPause); err == nil {
					a.stopWhy = byPause
				}
			}
			if a.Status == StatusQueued {
				if turnID, ok := sent[a.ID]; ok {
					r.noteSent(base, s, a, turnID, sent)
					working++
					open++
					continue
				}
				if a.stopWhy != "" {
					r.endUnstarted(base, s, a)
					continue
				}
				queued = append(queued, a)
				open++
				continue
			}
			t, err := turn.Get(base, s, a.WorkChatID, a.WorkTurnID)
			if err != nil {
				if errors.Is(err, turn.ErrNoTurn) || errors.Is(err, chat.ErrNotFound) {
					r.end(base, s, a, endState{status: StatusFailed, failure: &SubagentError{Code: CodeChatDeleted, Message: "Its chat was deleted while it worked, so it stopped."}})
					continue
				}
				if base.Err() == nil {
					r.logf("sub-agents: %s reading the turn of sub-agent %s: %v", a.requestID, a.ID, err)
				}
				open++
				working++
				continue
			}
			if t.Status != turn.StatusQueued && t.Status != turn.StatusRunning {
				r.finish(base, s, a, t)
				continue
			}
			open++
			working++
			if a.stopWhy != "" && !stopping[a.ID] {
				stopping[a.ID] = true
				toStop = append(toStop, a)
				continue
			}
			waits = append(waits, r.bound().Changed(b.tenantID, t.ID))
		}
		if len(toStop) > 0 {
			r.stopTurns(base, s, toStop)
			continue // look again at once: the stopped turns have ended
		}
		for _, a := range queued {
			if working >= r.o.Config.AtOnce {
				break
			}
			if r.send(base, s, a, sent) {
				working++
			}
		}
		if open == 0 {
			if base.Err() == nil {
				out <- r.report(base, s, b)
			}
			return
		}
		// No more often than ten times a stream_poll.
		if wait := gap - time.Since(last); wait > 0 {
			select {
			case <-time.After(wait):
			case <-base.Done():
				return
			}
		}
		last = time.Now()
		r.idle(base, call, news, waits)
	}
}

// idle waits for news: the call's own, a turn's, the call's end, the
// runner's end, or chats.stream_poll.
func (r *Runner) idle(base, call context.Context, news <-chan struct{}, turns []<-chan struct{}) {
	cases := []reflect.SelectCase{
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(news)},
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(call.Done())},
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(base.Done())},
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(time.After(r.o.Turns.StreamPoll))},
	}
	for _, ch := range turns {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch)})
	}
	reflect.Select(cases)
}

// forget lets go of a call's waiting channel once its supervisor is done.
func (r *Runner) forget(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.waiting, key)
}

// callEnded says why a call's context ended, as stop_why keeps it: its
// timeout, or the parent turn's stop. "" while it has not.
func callEnded(call context.Context) string {
	switch err := call.Err(); {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return byTimeout
	}
	return byParent
}

// rows reads a call's sub-agents, by place.
func (r *Runner) rows(ctx context.Context, s *tenant.Store, b batch) ([]row, error) {
	if len(b.ids) == 0 {
		return nil, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(b.ids)), ", ")
	args := make([]any, len(b.ids))
	for i, id := range b.ids {
		args[i] = id
	}
	return list(ctx, s.DB(), "id IN ("+marks+")", args...)
}

// send sends a sub-agent its turn, in its own chat, with the marks of the
// chat it was started from written there first (carry.Down), and reports
// whether it is now working. The marks are read now, not when the call
// began, so whatever the parent chat read meanwhile goes with them.
func (r *Runner) send(ctx context.Context, s *tenant.Store, a row, sent map[string]string) bool {
	t := r.bound()
	from := t.Carried(ctx, s, a.ChatID)
	down := carry.Down(carry.Marks{Outside: from.Outside != "", Sensitive: from.Sensitive})
	c := turn.Carry{Sensitive: down.Sensitive}
	if down.Outside {
		c.Outside = "the chat this sub-agent was started from had read outside content"
	}
	sentTurn, err := t.Send(ctx, s, a.WorkChatID, a.requestID, a.actor, turn.SendBody{Text: r.message(a), Model: a.Model}, c)
	switch {
	case err == nil:
	case errors.Is(err, turn.ErrClosing) || ctx.Err() != nil:
		return false
	case errors.Is(err, chat.ErrNotFound):
		r.end(ctx, s, a, endState{status: StatusFailed, failure: &SubagentError{Code: CodeChatDeleted, Message: "Its chat was deleted before it started."}})
		return false
	default:
		r.logf("sub-agents: %s sending the turn of sub-agent %s: %v", a.requestID, a.ID, err)
		r.end(ctx, s, a, endState{status: StatusFailed, failure: &SubagentError{Code: CodeFailed, Message: "It could not be started inside Keystone. The reason is in the server's log."}})
		return false
	}
	sent[a.ID] = sentTurn.ID
	r.noteSent(ctx, s, a, sentTurn.ID, sent)
	return true
}

// noteSent writes on a sub-agent's row that its turn was sent. One that
// cannot be written is tried again at the next look, and its turn is never
// sent twice.
func (r *Runner) noteSent(ctx context.Context, s *tenant.Store, a row, turnID string, sent map[string]string) {
	if err := change(ctx, s, a.ID, "status = 'running', work_turn_id = ?, started_at = ?", turnID, stamp()); err != nil {
		if ctx.Err() == nil {
			r.logf("sub-agents: %s noting the turn of sub-agent %s, which is tried again: %v", a.requestID, a.ID, err)
		}
		return
	}
	delete(sent, a.ID)
}

// stopTurns stops the turns of sub-agents, all at once, and waits for each
// stop to be answered: up to chats.stop_wait.
func (r *Runner) stopTurns(ctx context.Context, s *tenant.Store, list []row) {
	var wg sync.WaitGroup
	for _, a := range list {
		if a.WorkTurnID == "" {
			continue
		}
		wg.Add(1)
		go func(a row) {
			defer wg.Done()
			if _, err := r.bound().Stop(ctx, s, a.WorkChatID, a.WorkTurnID); err != nil && !errors.Is(err, turn.ErrNoTurn) && ctx.Err() == nil {
				r.logf("sub-agents: %s stopping the turn of sub-agent %s: %v", a.requestID, a.ID, err)
			}
		}(a)
	}
	wg.Wait()
}

// stopAll stops every sub-agent of a call that has not ended, because the
// turn that started them was stopped or the call ran out of time, and ends
// each. Nothing is handed back: the turn no longer waits.
func (r *Runner) stopAll(ctx context.Context, s *tenant.Store, b batch, why string) {
	rows, err := r.rows(ctx, s, b)
	if err != nil {
		r.logf("sub-agents: reading the sub-agents of tenant %s to stop them: %v", b.tenantID, err)
		return
	}
	var running []row
	for i, a := range rows {
		if ended(a.Status) {
			continue
		}
		if a.stopWhy == "" {
			if err := change(ctx, s, a.ID, "stop_why = ?", why); err == nil {
				rows[i].stopWhy = why
			}
		}
		if a.Status == StatusRunning {
			running = append(running, rows[i])
		}
	}
	r.stopTurns(ctx, s, running)
	for _, a := range rows {
		if ended(a.Status) {
			continue
		}
		if a.Status == StatusRunning {
			if t, err := turn.Get(ctx, s, a.WorkChatID, a.WorkTurnID); err == nil && t.Status != turn.StatusQueued && t.Status != turn.StatusRunning {
				r.finish(ctx, s, a, t)
				continue
			}
		}
		r.endUnstarted(ctx, s, a)
	}
}

// endState is how a sub-agent ended.
type endState struct {
	status    string
	failure   *SubagentError
	result    string
	notice    string
	steps     int
	spent     int64
	outside   bool
	sensitive bool
}

// stopped is what a sub-agent that was stopped ends as, by who stopped it.
func (r *Runner) stopped(why string) (string, *SubagentError) {
	switch why {
	case byPause:
		return StatusPaused, nil
	case byTimeout:
		return StatusFailed, &SubagentError{Code: CodeTimeout, Message: fmt.Sprintf("It was still working when the call that started it had waited %s, the most it may, so it was stopped.", r.o.Config.Timeout)}
	}
	return StatusStopped, nil
}

// endUnstarted ends a sub-agent whose turn was never sent or is not yet
// over, by who stopped it, with what its chat holds so far.
func (r *Runner) endUnstarted(ctx context.Context, s *tenant.Store, a row) {
	status, failure := r.stopped(a.stopWhy)
	e := endState{status: status, failure: failure}
	if a.WorkTurnID != "" {
		e.steps, _ = turn.Calls(ctx, s, a.WorkChatID, a.requestID)
		e.spent, _ = unattended.Spent(ctx, s, a.requestID)
		if t, err := turn.Get(ctx, s, a.WorkChatID, a.WorkTurnID); err == nil {
			e.result = r.lastWords(ctx, s, t)
		}
	}
	c := r.bound().Carried(ctx, s, a.WorkChatID)
	e.outside, e.sensitive = c.Outside != "", c.Sensitive
	r.end(ctx, s, a, e)
}

// finish ends a sub-agent whose turn has ended: done with its reply, at its
// step limit with what it had written, stopped by whoever stopped it, or
// failed with the turn's error. Its chat is told of any call the cap or its
// budget refused (P8-05), and what that came to is kept as its notice.
func (r *Runner) finish(ctx context.Context, s *tenant.Store, a row, t turn.Turn) {
	e := endState{}
	e.steps, _ = turn.Calls(ctx, s, a.WorkChatID, a.requestID)
	e.spent, _ = unattended.Spent(ctx, s, a.requestID)
	r.tell(ctx, s, a, t)
	notice, err := unattended.Summary(ctx, s, a.requestID)
	if err != nil {
		r.logf("sub-agents: %s reading the notices of sub-agent %s: %v", a.requestID, a.ID, err)
	}
	e.notice = notice
	c := r.bound().Carried(ctx, s, a.WorkChatID)
	e.outside, e.sensitive = c.Outside != "", c.Sensitive
	switch {
	case t.Status == turn.StatusDone && t.StopReason != turn.StopStepLimit:
		e.status = StatusDone
		reply, err := turn.ReplyText(s, t)
		if err != nil {
			r.logf("sub-agents: %s reading the result of sub-agent %s: %v", a.requestID, a.ID, err)
		}
		e.result = strings.TrimSpace(reply)
	case t.Status == turn.StatusDone:
		e.status = StatusFailed
		e.failure = &SubagentError{Code: CodeStepLimit, Message: fmt.Sprintf("It made %d model calls, the most a sub-agent may, and had not finished.", a.MaxSteps)}
		e.result = r.lastWords(ctx, s, t)
	case t.Status == turn.StatusStopped:
		e.status, e.failure = r.stopped(a.stopWhy)
		e.result = r.lastWords(ctx, s, t)
	default:
		e.status = StatusFailed
		e.failure = &SubagentError{Code: CodeFailed, Message: "It failed inside Keystone. The reason is in the server's log."}
		if t.Error != nil {
			e.failure = &SubagentError{Code: t.Error.Code, Message: t.Error.Message}
		}
		e.result = r.lastWords(ctx, s, t)
	}
	r.end(ctx, s, a, e)
}

// end writes how a sub-agent ended, and wakes a stop that waits for it.
func (r *Runner) end(ctx context.Context, s *tenant.Store, a row, e endState) {
	failure := ""
	if e.failure != nil {
		raw, _ := json.Marshal(e.failure)
		failure = string(raw)
	}
	if err := change(ctx, s, a.ID, `status = ?, error = ?, result = ?, notice = ?, steps = ?, spent_micros = ?, outside = ?, sensitive = ?, ended_at = ?`,
		e.status, failure, e.result, e.notice, e.steps, e.spent, e.outside, e.sensitive, stamp()); err != nil && !errors.Is(err, errNoSubagent) && ctx.Err() == nil {
		r.logf("sub-agents: %s ending sub-agent %s: %v", a.requestID, a.ID, err)
	}
	r.wake(s.TenantID() + "/" + a.ID)
}

// tell writes in a sub-agent's chat a note of each of its calls the monthly
// cap or its budget refused, naming the model that wrote the reply in its
// place, or saying it stopped (decision 0160, P8-05).
func (r *Runner) tell(ctx context.Context, s *tenant.Store, a row, t turn.Turn) {
	err := unattended.Tell(ctx, s, a.requestID, t.ID, true, func(noteTurn, text string) error {
		blocks := []seam.Block{{Type: seam.BlockText, Text: text}}
		_, err := r.bound().Note(ctx, s.TenantID(), a.WorkChatID, noteTurn, audit.NewRequestID(), a.actor, blocks, turn.Carry{})
		if errors.Is(err, chat.ErrNotFound) {
			return nil
		}
		return err
	})
	if err != nil && !errors.Is(err, turn.ErrClosing) && ctx.Err() == nil {
		r.logf("sub-agents: %s telling the chat of sub-agent %s that a call was refused: %v", a.requestID, a.ID, err)
	}
}

// lastWords is the text of the last message a model wrote in a turn that
// has any: what a sub-agent had written when it stopped or failed. A note
// Keystone wrote, such as the one at the step limit, is not its words.
func (r *Runner) lastWords(ctx context.Context, s *tenant.Store, t turn.Turn) string {
	rows, err := s.DB().QueryContext(ctx, "SELECT message_id FROM turn_messages WHERE turn_id = ? AND model <> '' ORDER BY message_id DESC LIMIT 20", t.ID)
	if err != nil {
		return ""
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		m, err := chat.GetMessage(s, t.ChatID, id)
		if err != nil {
			continue
		}
		if text := strings.TrimSpace(seam.Text(seam.Message{Role: m.Role, Blocks: m.Blocks})); text != "" {
			return text
		}
	}
	return ""
}

// Recover ends the sub-agents a stopped server left unfinished, for every
// tenant, as interrupted: the turn that started them was cut short too, and
// is closed as failed by the turn runner's Recover, which runs first. A
// sub-agent's turn that the turn runner put back in its queue, because it
// waited for an approval, is stopped: nothing waits for its result now.
func (r *Runner) Recover() {
	if r.o.Install == nil || r.bound() == nil {
		return
	}
	ctx := context.Background()
	for _, t := range r.o.Install.Tenants() {
		s, err := r.o.Install.Store(t.ID)
		if err != nil {
			r.logf("sub-agents: recovering tenant %s: %v", t.ID, err)
			continue
		}
		open, err := list(ctx, s.DB(), "status IN ('queued', 'running')")
		if err != nil {
			r.logf("sub-agents: recovering tenant %s: %v", t.ID, err)
			s.Close()
			continue
		}
		var running []row
		for _, a := range open {
			if a.WorkTurnID != "" {
				running = append(running, a)
			}
		}
		r.stopTurns(ctx, s, running)
		for _, a := range open {
			e := endState{status: StatusFailed, failure: &SubagentError{Code: CodeInterrupted,
				Message: "Keystone stopped while it was working, and the turn that started it was cut short too. What it did is in its chat."}}
			if a.WorkTurnID != "" {
				e.steps, _ = turn.Calls(ctx, s, a.WorkChatID, a.requestID)
				e.spent, _ = unattended.Spent(ctx, s, a.requestID)
				if wt, err := turn.Get(ctx, s, a.WorkChatID, a.WorkTurnID); err == nil {
					e.result = r.lastWords(ctx, s, wt)
				}
			}
			c := r.bound().Carried(ctx, s, a.WorkChatID)
			e.outside, e.sensitive = c.Outside != "", c.Sensitive
			r.end(ctx, s, a, e)
		}
		s.Close()
	}
}
