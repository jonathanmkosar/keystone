package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/artifacts"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/research/cite"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/subagents"
	"github.com/jonathanmkosar/keystone/internal/subagents/carry"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// The steps of a task that has not ended. next reads where the task is from
// its row, its turns and its parts, and does the one thing it needs, so a
// task goes on from any point: after a restart, after the switch that
// pauses every agent goes off, or at the next look.
//
//   - A stop the person asked for wins over everything: the step under way
//     is stopped and the task ends as stopped.
//   - The switch (agents.paused), read at every look: the task's own turn
//     is stopped, its parts are ended by their supervisor as any sub-agent
//     is (decision 0484), and the task is held. When the switch goes off it
//     sends its plan or its report again, or goes on to its report with
//     what its parts had.
//   - A turn a restart cut short is sent again. A turn the person stopped
//     in the task's own chat stops the task.

// next does the next thing a task that has not ended needs. It returns a
// channel that has news of the turn it waits on, when it waits on one, and
// whether to wait before looking again.
func (r *Runner) next(ctx context.Context, s *tenant.Store, j row) (<-chan struct{}, bool) {
	if j.stopAsked {
		r.stopSteps(ctx, s, j)
		r.finish(ctx, s, j, StatusStopped, nil)
		return nil, false
	}
	paused, err := unattended.Paused(ctx, s)
	if err != nil && ctx.Err() == nil {
		r.logf("research: %s reading %s, so task %s is held: %v", j.requestID, unattended.KeyPaused, j.ID, err)
	}
	switch {
	case paused && !j.paused:
		// Its own turn is stopped, and sent again when the switch goes off.
		r.stopOwn(ctx, s, j)
		words := "Paused: every agent is paused. It goes on when they are resumed."
		if j.stored == StatusQueued {
			words = "Held: every agent is paused. It starts when they are resumed."
		}
		set := "paused = 1, progress = ?"
		switch j.Stage {
		case StagePlan:
			set += ", plan_turn_id = ''"
		case StageWrite:
			set += ", write_turn_id = ''"
		}
		if err := change(ctx, s, j.ID, set, words); err != nil && !errors.Is(err, errNoResearch) && ctx.Err() == nil {
			r.logf("research: %s holding task %s: %v", j.requestID, j.ID, err)
		}
		return nil, true
	case paused:
		return nil, true
	case j.paused:
		if err := change(ctx, s, j.ID, "paused = 0, progress = ?", "Going on after a pause"); err != nil && !errors.Is(err, errNoResearch) {
			r.logf("research: %s letting task %s go on: %v", j.requestID, j.ID, err)
			return nil, true
		}
		return nil, false
	}
	if j.stored == StatusQueued {
		if err := change(ctx, s, j.ID, "status = 'running', stage = ?, started_at = ?, progress = ?", StagePlan, stamp(), "Planning the parts"); err != nil {
			if !errors.Is(err, errNoResearch) && ctx.Err() == nil {
				r.logf("research: %s starting task %s: %v", j.requestID, j.ID, err)
			}
			return nil, true
		}
		return nil, false
	}
	if _, err := chat.Get(s, j.WorkChatID); errors.Is(err, chat.ErrNotFound) {
		r.stopSteps(ctx, s, j)
		r.fail(ctx, s, j, CodeChatDeleted, "Its own chat was deleted while it worked, so it stopped.")
		return nil, false
	}
	switch j.Stage {
	case StageParts:
		return r.parts(ctx, s, j)
	case StageWrite:
		return r.write(ctx, s, j)
	}
	return r.plan(ctx, s, j)
}

// own reads the turn of the task's own chat a step waits on. ok is false
// when the step has sent none, or the one it sent is gone.
func (r *Runner) own(ctx context.Context, s *tenant.Store, j row, turnID string) (turn.Turn, bool) {
	if turnID == "" {
		return turn.Turn{}, false
	}
	t, err := turn.Get(ctx, s, j.WorkChatID, turnID)
	if err != nil {
		if !errors.Is(err, turn.ErrNoTurn) && !errors.Is(err, chat.ErrNotFound) && ctx.Err() == nil {
			r.logf("research: %s reading a turn of task %s: %v", j.requestID, j.ID, err)
		}
		return turn.Turn{}, false
	}
	return t, true
}

func running(t turn.Turn) bool {
	return t.Status == turn.StatusQueued || t.Status == turn.StatusRunning
}

func interrupted(t turn.Turn) bool {
	return t.Status == turn.StatusFailed && t.Error != nil && t.Error.Code == turn.CodeInterrupted
}

// send sends a turn of the task's own chat, under the task's request id,
// and notes it in column. It reports whether to wait before looking again.
func (r *Runner) send(ctx context.Context, s *tenant.Store, j row, column, text, words string, c turn.Carry) bool {
	_, rounds, _, err := turn.Latest(ctx, s, j.WorkChatID, j.requestID)
	if err != nil {
		if ctx.Err() == nil {
			r.logf("research: %s counting the turns of task %s: %v", j.requestID, j.ID, err)
		}
		return true
	}
	if rounds >= r.o.Config.MaxRounds {
		r.fail(ctx, s, j, CodeRoundLimit, fmt.Sprintf("Its own chat took %d turns, the most a research task may take, and the report was not written.", rounds))
		return false
	}
	t, err := r.bound().Send(ctx, s, j.WorkChatID, j.requestID, j.actor, turn.SendBody{Text: text, Model: j.model}, c)
	switch {
	case err == nil:
	case errors.Is(err, turn.ErrClosing) || ctx.Err() != nil:
		return true
	case errors.Is(err, chat.ErrNotFound):
		r.fail(ctx, s, j, CodeChatDeleted, "Its own chat was deleted while it worked, so it stopped.")
		return false
	case errors.Is(err, chat.ErrInvalid):
		r.logf("research: %s a message of task %s was refused: %v", j.requestID, j.ID, err)
		r.fail(ctx, s, j, CodeFailed, "A step's message was refused inside Keystone. The reason is in the server's log.")
		return false
	default:
		// A full queue in its own chat, the person's own turns: it waits.
		r.logf("research: %s sending a turn of task %s, which is tried again: %v", j.requestID, j.ID, err)
		return true
	}
	if err := change(ctx, s, j.ID, column+" = ?, progress = ?", t.ID, words); err != nil && !errors.Is(err, errNoResearch) && ctx.Err() == nil {
		r.logf("research: %s noting a turn of task %s: %v", j.requestID, j.ID, err)
	}
	return false
}

// plan is the first step: one turn that splits the question into parts.
func (r *Runner) plan(ctx context.Context, s *tenant.Store, j row) (<-chan struct{}, bool) {
	t, ok := r.own(ctx, s, j, j.planTurnID)
	if !ok {
		// The marks of the chat the question came from go with it (fixed rules
		// 2 and 3), before the turn can run.
		from := r.bound().Carried(ctx, s, j.ChatID)
		down := carry.Down(carry.Marks{Outside: from.Outside != "", Sensitive: from.Sensitive})
		c := turn.Carry{Sensitive: down.Sensitive}
		if down.Outside {
			c.Outside = "the chat this research was started from had read outside content"
		}
		return nil, r.send(ctx, s, j, "plan_turn_id", planMessage(r.o.Config, j), "Planning the parts", c)
	}
	switch {
	case running(t):
		news := r.bound().Changed(s.TenantID(), t.ID)
		if now, ok := r.own(ctx, s, j, j.planTurnID); ok && !running(now) {
			return nil, false
		}
		return news, true
	case interrupted(t):
		return nil, r.resend(ctx, s, j, "plan_turn_id")
	case t.Status == turn.StatusStopped:
		r.finish(ctx, s, j, StatusStopped, nil)
		return nil, false
	case t.Status != turn.StatusDone:
		e := &ResearchError{Code: CodeFailed, Message: "Its plan failed inside Keystone. The reason is in the server's log."}
		if t.Error != nil {
			e = &ResearchError{Code: t.Error.Code, Message: t.Error.Message}
		}
		if e.Code == string(seam.ErrSpendCap) {
			e.Message = "The research stopped at its budget before it found anything. " + e.Message
		}
		r.finish(ctx, s, j, StatusFailed, e)
		return nil, false
	}
	reply, err := turn.ReplyText(s, t)
	if err != nil {
		r.logf("research: %s reading the plan of task %s: %v", j.requestID, j.ID, err)
	}
	plan := parsePlan(reply, j.maxParts, j.Title, j.Question)
	raw, _ := json.Marshal(plan)
	if err := change(ctx, s, j.ID, "plan = ?, stage = ?, progress = ?", string(raw), StageParts, "Starting "+count(len(plan), "part", "parts")); err != nil {
		if !errors.Is(err, errNoResearch) && ctx.Err() == nil {
			r.logf("research: %s keeping the plan of task %s: %v", j.requestID, j.ID, err)
		}
		return nil, true
	}
	return nil, false
}

// resend forgets a turn a restart cut short, so the step sends it again.
func (r *Runner) resend(ctx context.Context, s *tenant.Store, j row, column string) bool {
	if err := change(ctx, s, j.ID, column+" = '', progress = ?", "Resuming after a restart"); err != nil {
		if !errors.Is(err, errNoResearch) && ctx.Err() == nil {
			r.logf("research: %s resuming task %s: %v", j.requestID, j.ID, err)
		}
		return true
	}
	return false
}

// parts is the second step: one sub-agent for each part of the plan, each
// with its share of the budget, until every one has ended.
func (r *Runner) parts(ctx context.Context, s *tenant.Store, j row) (<-chan struct{}, bool) {
	subs, err := r.o.Subagents.Parts(ctx, s, j.WorkChatID, j.callID)
	if err != nil {
		if ctx.Err() == nil {
			r.logf("research: %s reading the parts of task %s: %v", j.requestID, j.ID, err)
		}
		return nil, true
	}
	if len(subs) == 0 {
		return nil, r.startParts(ctx, s, j)
	}
	for _, a := range subs {
		if !subagents.Ended(a) {
			return nil, true // a look every chats.stream_poll, as their supervisor takes
		}
	}
	// Every part has ended. What they did not spend goes to the report step:
	// the task's own run may now spend what is left of the whole budget.
	var spent int64
	for _, a := range subs {
		spent += a.SpentMicros
	}
	if err := unattended.SetBudget(ctx, s.DB(), j.requestID, max(j.BudgetMicros-spent, 0)); err != nil {
		r.logf("research: %s giving the report step of task %s what its parts did not spend: %v", j.requestID, j.ID, err)
	}
	if err := change(ctx, s, j.ID, "stage = ?, progress = ?", StageWrite, "Writing the report"); err != nil {
		if !errors.Is(err, errNoResearch) && ctx.Err() == nil {
			r.logf("research: %s moving task %s on to its report: %v", j.requestID, j.ID, err)
		}
		return nil, true
	}
	return nil, false
}

// startParts gives the plan and the report their share of the budget, the
// parts the rest in even shares, and starts the parts. The task's own run
// is lowered first, so the runs together can never spend more than the
// task's budget. It reports whether to wait before looking again.
func (r *Runner) startParts(ctx context.Context, s *tenant.Store, j row) bool {
	if len(j.Plan) == 0 {
		r.fail(ctx, s, j, CodeFailed, "Its plan could not be read. The reason is in the server's log.")
		return false
	}
	ownSpent, err := unattended.Spent(ctx, s, j.requestID)
	if err != nil {
		if ctx.Err() == nil {
			r.logf("research: %s reading the spend of task %s: %v", j.requestID, j.ID, err)
		}
		return true
	}
	ownShare := max(j.BudgetMicros*int64(r.o.Config.ReportSharePercent)/100, min(ownSpent, j.BudgetMicros))
	each := (j.BudgetMicros - ownShare) / int64(len(j.Plan))
	if err := unattended.SetBudget(ctx, s.DB(), j.requestID, ownShare); err != nil {
		r.logf("research: %s setting the share of the budget task %s keeps for its report: %v", j.requestID, j.ID, err)
		return true
	}
	given := partContext(r.o.Config, j)
	list := make([]subagents.Part, len(j.Plan))
	for i, p := range j.Plan {
		list[i] = subagents.Part{Title: p.Title, Task: p.Task, Context: given, BudgetMicros: each}
	}
	err = r.o.Subagents.StartParts(ctx, s, j.WorkChatID, j.planTurnID, j.callID, j.model, list)
	switch {
	case err == nil:
	case errors.Is(err, subagents.ErrPaused) || ctx.Err() != nil:
		return true
	case errors.Is(err, chat.ErrNotFound):
		r.fail(ctx, s, j, CodeChatDeleted, "Its own chat was deleted while it worked, so it stopped.")
		return false
	default:
		r.logf("research: %s starting the parts of task %s: %v", j.requestID, j.ID, err)
		r.fail(ctx, s, j, CodeFailed, "Its parts could not be started inside Keystone. The reason is in the server's log.")
		return false
	}
	if err := change(ctx, s, j.ID, "progress = ?", "Researching "+count(len(list), "part", "parts")); err != nil && !errors.Is(err, errNoResearch) && ctx.Err() == nil {
		r.logf("research: %s noting the parts of task %s: %v", j.requestID, j.ID, err)
	}
	return false
}

// found reads what the parts gave: the sources numbered across them, and
// each part's findings in those numbers.
func (r *Runner) found(ctx context.Context, s *tenant.Store, j row) ([]cite.Source, []finding, []subagents.Subagent, error) {
	subs, err := r.o.Subagents.Parts(ctx, s, j.WorkChatID, j.callID)
	if err != nil {
		return nil, nil, nil, err
	}
	own := r.bound().Carried(ctx, s, j.WorkChatID)
	sources, findings, err := gather(s, carry.Marks{Outside: own.Outside != "", Sensitive: own.Sensitive}, subs, r.o.PartChars)
	return sources, findings, subs, err
}

// write is the third step: one turn that writes the report from what the
// parts found. A report step no model could take, because the budget or
// anything else refused it, ends the task with a report Keystone puts
// together from the findings, which says so.
func (r *Runner) write(ctx context.Context, s *tenant.Store, j row) (<-chan struct{}, bool) {
	t, ok := r.own(ctx, s, j, j.writeTurnID)
	switch {
	case !ok:
	case running(t):
		news := r.bound().Changed(s.TenantID(), t.ID)
		if now, ok := r.own(ctx, s, j, j.writeTurnID); ok && !running(now) {
			return nil, false
		}
		return news, true
	case interrupted(t):
		return nil, r.resend(ctx, s, j, "write_turn_id")
	case t.Status == turn.StatusStopped:
		r.finish(ctx, s, j, StatusStopped, nil)
		return nil, false
	}
	sources, findings, subs, err := r.found(ctx, s, j)
	if err != nil {
		if ctx.Err() == nil {
			r.logf("research: %s reading what the parts of task %s found: %v", j.requestID, j.ID, err)
		}
		return nil, true
	}
	if !ok {
		anything, atBudget, outside := false, false, false
		var why []string
		for _, f := range findings {
			anything = anything || f.text != ""
			atBudget = atBudget || f.atBudget
			outside = outside || f.outside
			if f.why != "" {
				why = append(why, f.why)
			}
		}
		if !anything {
			e := &ResearchError{Code: CodeNothingFound, Message: "None of its parts found anything to write a report from."}
			if atBudget {
				e = &ResearchError{Code: string(seam.ErrSpendCap), Message: "The research stopped at its budget before it found anything."}
			}
			if len(why) > 0 {
				e.Message += " " + why[0]
			}
			r.finish(ctx, s, j, StatusFailed, e)
			return nil, false
		}
		raw, _ := json.Marshal(sources)
		if err := change(ctx, s, j.ID, "sources = ?, sources_read = ?", string(raw), len(sources)); err != nil {
			if !errors.Is(err, errNoResearch) && ctx.Err() == nil {
				r.logf("research: %s keeping the sources of task %s: %v", j.requestID, j.ID, err)
			}
			return nil, true
		}
		// What the parts read is outside content, and the report step reads
		// it: the task's own chat is marked before the turn can run (fixed
		// rule 2).
		c := turn.Carry{}
		if outside {
			c.Outside = "this research task's parts read outside content"
		}
		return nil, r.send(ctx, s, j, "write_turn_id", writeMessage(r.o.Config, j, findings, sources), "Writing the report", c)
	}
	body, atBudget, why := "", false, ""
	if t.Status == turn.StatusDone {
		reply, err := turn.ReplyText(s, t)
		if err != nil {
			r.logf("research: %s reading the report of task %s: %v", j.requestID, j.ID, err)
		}
		body = strings.TrimSpace(reply)
	} else if t.Error != nil {
		atBudget = t.Error.Code == string(seam.ErrSpendCap)
		why = t.Error.Message
	}
	if body != "" {
		// A model's report cites by the numbers it was sent, the ones kept
		// when its turn was.
		sources = j.Sources
	}
	r.done(ctx, s, j, body, why, atBudget, sources, findings, subs)
	return nil, false
}

// done ends a task with its report: the one a model wrote, or, when body is
// empty, one put together from the findings. The citations are checked, the
// sources listed, and what the person should know is written in.
func (r *Runner) done(ctx context.Context, s *tenant.Store, j row, body, why string, atBudget bool, sources []cite.Source, findings []finding, subs []subagents.Subagent) {
	spent := r.spent(ctx, s, j, subs)
	earlier := 0
	var about []string
	for _, f := range findings {
		earlier += f.dropped
		atBudget = atBudget || f.atBudget
		switch {
		case f.held:
			about = append(about, fmt.Sprintf("The part “%s” was sent content from a sensitive space, so what it found stays in its own chat and is not in this report.", f.title))
		case f.status != subagents.StatusDone:
			line := fmt.Sprintf("The part “%s” %s", f.title, statusWords(f))
			if f.why != "" {
				line += ": " + strings.TrimRight(f.why, ". ")
			}
			about = append(about, line+".")
		}
	}
	notice, err := unattended.Summary(ctx, s, j.requestID)
	if err != nil {
		r.logf("research: %s reading the notices of task %s: %v", j.requestID, j.ID, err)
	}
	for _, a := range subs {
		if a.Notice != "" && notice == "" {
			notice = a.Notice
		}
	}
	atBudget = atBudget || notice != ""
	if body == "" {
		reason := "No model wrote this report."
		switch {
		case atBudget:
			reason = "The research reached its budget before its report could be written."
		case why != "":
			reason = "The report could not be written: " + strings.TrimRight(why, ". ") + "."
		}
		body = fallbackBody(reason, findings)
	}
	if atBudget {
		about = append([]string{budgetLine(j, spent)}, about...)
	}
	if notice != "" {
		about = append(about, notice)
	}
	c := compose(j, body, sources, about, earlier, r.o.Config.SummaryChars)
	if most := r.o.Artifacts.MaxBytes; most > 0 && len(c.report) > most {
		cut := []rune(c.report[:max(most-200, 0)])
		c.report = strings.TrimSpace(string(cut[:max(len(cut)-1, 0)])) + "\n\n[The report was cut here: it was longer than one artifact may be.]\n"
	}
	cited, _ := json.Marshal(c.cited)
	noteTurn, err := newID()
	if err != nil {
		r.logf("research: %s ending task %s: %v", j.requestID, j.ID, err)
		return
	}
	lines := about
	if len(lines) > 0 && atBudget {
		lines = lines[1:] // the note says the budget's line itself
	}
	if err := change(ctx, s, j.ID, `status = 'done', stage = '', report = ?, summary = ?, sources = ?, sources_read = ?, dropped = ?,
		at_budget = ?, notice = ?, spent_micros = ?, progress = 'Done', ended_at = ?,
		note_turn_id = CASE WHEN note_turn_id = '' THEN ? ELSE note_turn_id END`,
		c.report, c.summary, string(cited), len(sources), c.dropped, atBudget, strings.Join(lines, " "), spent, stamp(), noteTurn); err != nil &&
		!errors.Is(err, errNoResearch) && ctx.Err() == nil {
		r.logf("research: %s ending task %s: %v", j.requestID, j.ID, err)
	}
}

// spent is what the whole task has spent on models that are not local: its
// own turns and its parts.
func (r *Runner) spent(ctx context.Context, s *tenant.Store, j row, subs []subagents.Subagent) int64 {
	n, err := unattended.Spent(ctx, s, j.requestID)
	if err != nil {
		n = 0
	}
	for _, a := range subs {
		n += a.SpentMicros
	}
	return n
}

// fail ends a task as failed, with why.
func (r *Runner) fail(ctx context.Context, s *tenant.Store, j row, code, message string) {
	r.finish(ctx, s, j, StatusFailed, &ResearchError{Code: code, Message: message})
}

// finish ends a task that has no report: stopped, or failed with why.
func (r *Runner) finish(ctx context.Context, s *tenant.Store, j row, status string, failure *ResearchError) {
	text := ""
	if failure != nil {
		raw, _ := json.Marshal(failure)
		text = string(raw)
	}
	words := "Stopped"
	if status == StatusFailed {
		words = "Did not finish"
	}
	noteTurn, err := newID()
	if err != nil {
		r.logf("research: %s ending task %s: %v", j.requestID, j.ID, err)
		return
	}
	subs, _ := r.o.Subagents.Parts(ctx, s, j.WorkChatID, j.callID)
	atBudget := failure != nil && failure.Code == string(seam.ErrSpendCap)
	if err := change(ctx, s, j.ID, `status = ?, error = ?, at_budget = ?, spent_micros = ?, progress = ?, ended_at = ?,
		note_turn_id = CASE WHEN note_turn_id = '' THEN ? ELSE note_turn_id END`,
		status, text, atBudget, r.spent(ctx, s, j, subs), words, stamp(), noteTurn); err != nil && !errors.Is(err, errNoResearch) && ctx.Err() == nil {
		r.logf("research: %s ending task %s: %v", j.requestID, j.ID, err)
	}
}

// stopOwn stops the turn of the task's own chat that is under way, if one
// is.
func (r *Runner) stopOwn(ctx context.Context, s *tenant.Store, j row) {
	for _, id := range []string{j.planTurnID, j.writeTurnID} {
		t, ok := r.own(ctx, s, j, id)
		if !ok || !running(t) {
			continue
		}
		if _, err := r.bound().Stop(ctx, s, j.WorkChatID, t.ID); err != nil && !errors.Is(err, turn.ErrNoTurn) && ctx.Err() == nil {
			r.logf("research: %s stopping a turn of task %s: %v", j.requestID, j.ID, err)
		}
	}
}

// stopSteps stops whatever a task has under way: its own turn, and its
// parts.
func (r *Runner) stopSteps(ctx context.Context, s *tenant.Store, j row) {
	r.stopOwn(ctx, s, j)
	if _, err := r.o.Subagents.StopParts(ctx, s, j.WorkChatID, j.callID); err != nil && ctx.Err() == nil {
		r.logf("research: %s stopping the parts of task %s: %v", j.requestID, j.ID, err)
	}
}

// deliver writes a task's end where the person asked: a note in the chat it
// was started from, and its report as a Markdown artifact there.
//
// The note comes first. It is written with the chat's queue held, and marks
// the chat as having read outside content when the report draws on any,
// before the report can be read there, so every later side effect in that
// chat asks (fixed rule 2). A task whose own chat was sent sensitive
// content, started from a chat that is not held, keeps its report and its
// summary in its own chat: the other may reach a model that may not read
// them (fixed rule 3, carry.Up).
func (r *Runner) deliver(ctx context.Context, s *tenant.Store, j row) {
	t := r.bound()
	origin, own := t.Carried(ctx, s, j.ChatID), t.Carried(ctx, s, j.WorkChatID)
	pass, outside := carry.Up(carry.Marks{Outside: origin.Outside != "", Sensitive: origin.Sensitive},
		carry.Marks{Outside: own.Outside != "", Sensitive: own.Sensitive})
	target := j.ChatID
	if !pass {
		target = j.WorkChatID
	}
	if j.NoteMessageID == 0 {
		if j.noteTurnID == "" {
			r.logf("research: %s task %s ended with no turn for its note", j.requestID, j.ID)
			return
		}
		c := turn.Carry{}
		if j.stored == StatusDone && outside {
			c.Outside = "this chat holds the summary and the report of research that read outside content"
		}
		blocks := []seam.Block{{Type: seam.BlockText, Text: noteText(j, pass)}}
		msg, err := t.Note(ctx, s.TenantID(), j.ChatID, j.noteTurnID, j.requestID, j.actor, blocks, c)
		if err != nil {
			if !errors.Is(err, chat.ErrNotFound) && !errors.Is(err, turn.ErrClosing) && ctx.Err() == nil {
				r.logf("research: %s writing the note of task %s in its chat, which is tried again at the next start: %v", j.requestID, j.ID, err)
			}
			return
		}
		if err := change(ctx, s, j.ID, "note_message_id = ?", msg.ID); err != nil {
			if !errors.Is(err, errNoResearch) && ctx.Err() == nil {
				r.logf("research: %s noting the note of task %s: %v", j.requestID, j.ID, err)
			}
			return
		}
	}
	if j.report == "" || j.ArtifactID != "" {
		return
	}
	id, err := r.save(ctx, s, j, target)
	if err != nil {
		if !errors.Is(err, chat.ErrNotFound) && ctx.Err() == nil {
			r.logf("research: %s saving the report of task %s, which is tried again at the next start: %v", j.requestID, j.ID, err)
		}
		return
	}
	if err := change(ctx, s, j.ID, "artifact_id = ?, artifact_chat_id = ?", id, target); err != nil && !errors.Is(err, errNoResearch) && ctx.Err() == nil {
		r.logf("research: %s noting the report of task %s: %v", j.requestID, j.ID, err)
	}
}

// save keeps the report as a Markdown artifact of a chat, once: an artifact
// of that chat already written for the task's request id is the report.
func (r *Runner) save(ctx context.Context, s *tenant.Store, j row, chatID string) (string, error) {
	have, err := artifacts.List(ctx, s, chatID)
	if err != nil {
		return "", err
	}
	for _, a := range have {
		versions, err := artifacts.Versions(ctx, s, a.ID)
		if err != nil {
			return "", err
		}
		for _, v := range versions {
			if v.RequestID == j.requestID {
				return a.ID, nil
			}
		}
	}
	a, _, err := artifacts.Create(ctx, s, r.o.Artifacts, chatID, artifacts.Draft{Type: artifacts.KindMarkdown, Title: j.Title, Content: j.report}, j.requestID)
	return a.ID, err
}

// noteText is what a task's note says in the chat it was started from.
// pass says its report and summary may be written there.
func noteText(j row, pass bool) string {
	switch j.stored {
	case StatusDone:
		if !pass {
			return fmt.Sprintf("Research “%s” is done. It was sent content from a sensitive space, so its report stays in its own chat and is not written here: this chat may reach a model that may not read it.", j.Title)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Research “%s” is done.", j.Title)
		if j.AtBudget {
			b.WriteString(" " + budgetLine(j, j.SpentMicros))
		}
		if j.Summary != "" {
			b.WriteString("\n\n" + j.Summary)
		}
		fmt.Fprintf(&b, "\n\nThe full report is saved in this chat as the artifact “%s”. It cites %s of the %s the research read",
			j.Title, count(len(j.Sources), "source", "sources"), count(j.SourcesRead, "source", "sources"))
		if j.Dropped > 0 {
			fmt.Fprintf(&b, "; %s that named no source that was read %s taken out", count(j.Dropped, "citation", "citations"), wasWere(j.Dropped))
		}
		fmt.Fprintf(&b, ". It spent %s of its budget of %s.", usage.Dollars(j.SpentMicros), usage.Dollars(j.BudgetMicros))
		if j.Notice != "" {
			b.WriteString("\n\n" + j.Notice)
		}
		return b.String()
	case StatusStopped:
		return fmt.Sprintf("Research “%s” was stopped before its report was written. Its own chat and its parts' chats have what it did.", j.Title)
	}
	why := "It failed."
	if j.Error != nil && j.Error.Message != "" {
		why = j.Error.Message
	}
	return fmt.Sprintf("Research “%s” did not finish. %s Its own chat and its parts' chats have what it did.", j.Title, why)
}

// show is a task as the routes give it: its parts, the sources they have
// read so far, and where it is, read now for one that has not ended.
func (r *Runner) show(ctx context.Context, s *tenant.Store, j row) Research {
	out := j.Research
	out.Parts = []subagents.Subagent{}
	if j.Stage == "" && !ended(j.stored) || r.o.Subagents == nil {
		return out
	}
	subs, err := r.o.Subagents.Parts(ctx, s, j.WorkChatID, j.callID)
	if err != nil {
		return out
	}
	if subs != nil {
		out.Parts = subs
	}
	if ended(j.stored) {
		return out
	}
	out.SpentMicros = r.spent(ctx, s, j, subs)
	done := 0
	for _, a := range subs {
		out.Waiting = out.Waiting || a.Waiting
		if subagents.Ended(a) {
			done++
		}
	}
	for _, id := range []string{j.planTurnID, j.writeTurnID} {
		if id == "" {
			continue
		}
		if pending, _ := turn.Pending(ctx, s, id); pending != "" {
			out.Waiting = true
		}
	}
	if j.Stage == StageParts && len(subs) > 0 && !j.paused {
		out.Progress = fmt.Sprintf("Researching: %d of %s done", done, count(len(subs), "part", "parts"))
	}
	if j.Stage == StageParts || len(j.Sources) == 0 {
		// This is for the person, whose own data it is, and for no model: every
		// part's sources are shown, a held part's too.
		if sources, _, err := gather(s, carry.Marks{Sensitive: true}, subs, r.o.PartChars); err == nil && sources != nil {
			out.Sources, out.SourcesRead = sources, len(sources)
		}
	}
	return out
}
