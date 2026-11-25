package turn

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/internal/tools/approval"
)

// The approval policy's part of a turn (P7-02). The rule itself is in
// internal/tools/approval, which reads no setting and stores nothing. What
// is here is the turn's side: the person's policy for each tool, whether
// the turn has read outside content, the pause while a person answers, and
// going on after a restart. docs/architecture/approvals.md is the whole of
// it.
//
// When a model call stops for tools, every call of the reply is decided
// before any of them runs. A call that asks gets a row in tool_approvals and
// an approval event, and the turn waits until each is answered, the person
// stops the turn, or agents.approval_timeout passes. Nothing of the step has
// run while it waits, so a server that stops then leaves nothing half done:
// the next start puts the turn back in its chat's queue, and it waits again.

// EventApproval is the stream event of one call that asks for a person's
// answer. It is sent when the call asks, and again when it is answered.
const EventApproval = "approval"

// The statuses of an approval.
const (
	ApprovalPending  = "pending"  // waiting for the person
	ApprovalApproved = "approved" // the person approved it
	ApprovalDeclined = "declined" // the person declined it
	ApprovalExpired  = "expired"  // nobody answered within agents.approval_timeout
	ApprovalStopped  = "stopped"  // the turn was stopped while it waited
	// approvalStanding is a call of a tool that changes something that ran
	// under the person's standing policy, always. It is kept so the call is
	// used once, and is never shown as a card.
	approvalStanding = "standing"
)

// KeyApprovalTimeout is how long a call waits for a person's answer.
const KeyApprovalTimeout = "agents.approval_timeout"

// The bounds of agents.approval_timeout.
const (
	approvalTimeoutMin = time.Second
	approvalTimeoutMax = 30 * 24 * time.Hour
)

// What a model reads in place of a result when a call that asked was not run.
const (
	textDeclined = "The person declined to give their approval, so this tool was not run. Do not try it again unless they ask."
	textExpired  = "Nobody answered the request for approval in time, so this tool was not run."
	textStopWait = "The turn was stopped while this tool waited for the person's approval, so it was not run."
	textNever    = "The person has set this tool never to run, so it was not run."
	textUsed     = "This call's approval was already used, so it was not run again. Keystone may have stopped while it ran: check whether it was done before asking again."
	textUnasked  = "Keystone could not ask the person about this call, so it was not run. The reason is in the server's log."
)

// Approval is one call that asked for a person's answer: the data of an
// approval event, and what the approval routes answer with.
type Approval struct {
	// ID names the approval in the routes that answer it.
	ID     string `json:"id"`
	TurnID string `json:"turn_id"`
	ChatID string `json:"chat_id"`
	// CallID is the tool call's id, as the tool_call event gave it.
	CallID string `json:"call_id"`
	Tool   string `json:"tool"`
	// Summary says what the call will do, for a person.
	Summary string `json:"summary"`
	// Input is the call's arguments, a JSON object.
	Input json.RawMessage `json:"input"`
	// Reason is policy (the person's policy for the tool says to ask) or
	// outside (the chat has read outside content, so every tool that
	// changes something asks). Why says it in a sentence.
	Reason string `json:"reason"`
	Why    string `json:"why"`
	// Status is pending, approved, declined, expired or stopped.
	Status  string     `json:"status"`
	Asked   time.Time  `json:"asked"`
	Decided *time.Time `json:"decided,omitempty"`
}

// ApprovalsReply answers GET .../approvals.
type ApprovalsReply struct {
	// Approvals are the turn's, oldest first.
	Approvals []Approval `json:"approvals"`
}

// The approval event joins the stream's documented events.
func init() {
	streamEvents = append(streamEvents, api.EventDoc{
		Name:        EventApproval,
		Description: "A tool call needs the person's answer before it runs (status pending), or was answered (approved, declined, expired or stopped). It replaces an earlier approval event with the same id. Answer it with POST .../approvals/{approval}/approve or /decline.",
		Data:        Approval{},
	})
}

// turnApprovals is the approval policy's state for one turn, shared by
// every step of it.
type turnApprovals struct {
	// resume is the step a turn that paused goes on from, after a restart.
	resume *pausedStep
	// outside says, in words, that the turn has read outside content. It is
	// what this server knows even if the mark could not be written.
	outside string
}

// pausedStep is a model call whose tool calls were being decided or run
// when the turn last stopped working.
type pausedStep struct {
	step      int
	messageID int64
}

// approvalState reads a turn's state when it starts or starts again. A turn
// goes on from a step when that step's calls were being decided or run and
// their results were never stored: the turn's newest message is the reply
// that made the calls.
func (r *Runner) approvalState(s *tenant.Store, t row) *turnApprovals {
	st := &turnApprovals{}
	var step int
	var msg int64
	err := s.DB().QueryRow("SELECT step, message_id FROM tool_approvals WHERE turn_id = ? ORDER BY step DESC LIMIT 1", t.ID).Scan(&step, &msg)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			r.logf("turns: %s turn %s: reading its approvals: %v", t.requestID, t.ID, err)
		}
		return st
	}
	var last sql.NullInt64
	if err := s.DB().QueryRow("SELECT max(message_id) FROM turn_messages WHERE turn_id = ?", t.ID).Scan(&last); err == nil && last.Int64 == msg {
		st.resume = &pausedStep{step: step, messageID: msg}
	}
	return st
}

// requeuePaused is Recover's question for a turn that was running: did it
// stop while its tool calls waited for a person, or ran under their
// approval? Such a turn goes back in the queue, and goes on from that step
// when it runs.
func (r *Runner) requeuePaused(s *tenant.Store, t row) bool {
	if r.approvalState(s, t).resume == nil {
		return false
	}
	res, err := s.DB().Exec("UPDATE turns SET status = 'queued' WHERE id = ? AND status = 'running'", t.ID)
	if err != nil {
		r.logf("turns: %s turn %s: putting it back in the queue: %v", t.requestID, t.ID, err)
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// resumeStep answers the calls of the step a turn paused at, as toolStep
// would have, and returns where the turn's loop goes on from. The knowledge
// lookups that step offered are made again for the call's route first.
func (r *Runner) resumeStep(ctx context.Context, s *tenant.Store, t *row, m models.Model, p seam.Provider, say func(string, any)) (step int, tail int64, again bool, failure *TurnError, stopped bool) {
	at := t.approvals.resume
	t.approvals.resume = nil
	msg, err := chat.GetMessage(s, t.ChatID, at.messageID)
	if err != nil {
		r.logf("turns: %s turn %s: reading the reply it paused at: %v", t.requestID, t.ID, err)
		return at.step, at.messageID, false, &TurnError{Code: CodeFailed, Message: "The turn failed inside Keystone. The reason is in the server's log."}, false
	}
	r.lookupTools(ctx, s, *t, p, m, r.offered(ctx, s, m))
	calls := seam.ToolCalls(seam.Message{Role: msg.Role, Blocks: msg.Blocks})
	tail, again, failure, stopped = r.toolStep(ctx, s, *t, at.step, at.messageID, calls, say)
	return at.step, tail, again, failure, stopped
}

// planned is what one call of a step will do once the step is decided.
type planned struct {
	verdict approval.Verdict
	// grant is the approval it runs with: the person's answer, or their
	// standing policy. The zero value for a tool that only reads.
	grant approval.Approval
	// row is the approval's id, marked used just before the call runs.
	row string
	// refusal is what the model reads when the call is not run.
	refusal string
}

// toolFor returns the tool a call names: a knowledge lookup the call's
// model call offered, or a tool in the registry.
func (r *Runner) toolFor(t row, name string) (tools.Tool, bool) {
	if l, ok := t.lookup(name); ok {
		return l, true
	}
	return r.o.Tools.Get(name)
}

// approveStep decides every call of one step, asks the person about those
// that need it, and waits for the answers. closing says the server is
// stopping: nothing more is written, and the turn is left for Recover.
func (r *Runner) approveStep(ctx context.Context, s *tenant.Store, t row, step int, tail int64, calls []seam.Block, say func(string, any)) (plans []planned, closing bool) {
	if t.approvals == nil {
		t.approvals = &turnApprovals{}
	}
	outside, why := r.readOutside(ctx, s, t)
	pols, polsOK := r.toolPolicies(ctx, s)
	have, err := approvalsAt(s, t.ID, tail)
	if err != nil {
		r.logf("turns: %s turn %s: reading the step's approvals: %v", t.requestID, t.ID, err)
	}
	plans = make([]planned, len(calls))
	waiting := false
	for i, c := range calls {
		tool, known := r.toolFor(t, c.Name)
		if !known {
			plans[i] = planned{verdict: approval.VerdictRun} // the registry answers that there is no such tool
			continue
		}
		pol, set := pols[c.Name]
		if !polsOK {
			pol = "unread" // not a policy, so the call asks
		}
		d := approval.Decide(approval.Call{Tool: c.Name, Input: c.Input, ReadOnly: tool.ReadOnly, Policy: pol, Outside: outside})
		got, kept := have[c.ID]
		switch {
		case kept && got.used:
			plans[i] = planned{verdict: approval.VerdictRefuse, refusal: textUsed}
		case d.Verdict == approval.VerdictRefuse:
			plans[i] = planned{verdict: approval.VerdictRefuse, refusal: textNever}
		case kept && got.Status != approvalStanding:
			// Asked in an earlier pass, before a restart: the answer stands.
			plans[i] = planned{verdict: approval.VerdictAsk, row: got.ID}
			waiting = waiting || got.Status == ApprovalPending
		case d.Verdict == approval.VerdictAsk:
			a, err := r.ask(s, t, step, tail, c, tool, d.Reason, askWhy(d.Reason, why, set, polsOK))
			if err != nil {
				r.logf("turns: %s turn %s: asking about the call to %s: %v", t.requestID, t.ID, c.Name, err)
				plans[i] = planned{verdict: approval.VerdictRefuse, refusal: textUnasked}
				break
			}
			say(EventApproval, a.Approval)
			plans[i] = planned{verdict: approval.VerdictAsk, row: a.ID}
			waiting = true
		case !tool.ReadOnly:
			// It runs under the person's standing policy, once.
			id, err := r.standing(s, t, step, tail, c, tool)
			if err != nil {
				r.logf("turns: %s turn %s: recording the call to %s: %v", t.requestID, t.ID, c.Name, err)
				plans[i] = planned{verdict: approval.VerdictRefuse, refusal: textUnasked}
				break
			}
			plans[i] = planned{verdict: approval.VerdictRun, grant: d.Approval, row: id}
		default:
			plans[i] = planned{verdict: approval.VerdictRun}
		}
		// A call after this one in the same step may read what this one
		// brings in, so it is decided as if the turn had read it.
		if !tool.Inside && !outside {
			outside, why = true, fmt.Sprintf("this chat reads outside content, from %s, earlier in the same step", c.Name)
		}
	}
	if waiting {
		if r.waitForAnswers(ctx, s, t, tail) {
			return nil, true
		}
		if have, err = approvalsAt(s, t.ID, tail); err != nil {
			r.logf("turns: %s turn %s: reading the answers: %v", t.requestID, t.ID, err)
		}
	}
	for i, c := range calls {
		if plans[i].verdict != approval.VerdictAsk {
			continue
		}
		got := have[c.ID]
		switch got.Status {
		case ApprovalApproved:
			plans[i] = planned{verdict: approval.VerdictRun, row: got.ID,
				grant: approval.Answered(approval.Answer{Tool: got.Tool, Input: got.Input, Approved: true})}
		case ApprovalDeclined:
			plans[i] = planned{verdict: approval.VerdictRefuse, refusal: textDeclined}
		case ApprovalExpired:
			plans[i] = planned{verdict: approval.VerdictRefuse, refusal: textExpired}
		default: // stopped, or an answer that could not be read
			plans[i] = planned{verdict: approval.VerdictRefuse, refusal: textStopWait}
		}
	}
	return plans, false
}

// askWhy is the sentence a card gives for asking.
func askWhy(reason approval.Reason, outside string, set, policiesRead bool) string {
	switch {
	case reason == approval.ReasonOutside:
		return "Asking because " + outside + "."
	case !policiesRead:
		return "Asking because your policies for tools could not be read."
	case set:
		return "Asking because this tool is set to ask."
	}
	return "Asking because a tool that changes something asks unless you set it to always."
}

// runPlanned runs one call as its step decided: refused with its reason,
// or run, with the approval it was given, which is marked used first. It
// then notes whether the call brought outside content into the turn.
func (r *Runner) runPlanned(ctx context.Context, s *tenant.Store, t row, c tools.Call, p planned) tools.Result {
	if p.verdict != approval.VerdictRun {
		return r.o.Tools.Decline(c, p.refusal)
	}
	if p.row != "" && ctx.Err() == nil && !markUsed(s, p.row) {
		return r.o.Tools.Decline(c, textUsed)
	}
	c.Approval = p.grant
	var res tools.Result
	if l, ok := t.lookup(c.Name); ok {
		res = runLookup(ctx, c, l) // a knowledge lookup (lookups.go)
	} else {
		res = r.o.Tools.Run(ctx, c)
	}
	r.noteOutside(s, t, c, res)
	return res
}

// markUsed marks an approval used, just before its call runs. It reports
// false when it was used already.
func markUsed(s *tenant.Store, id string) bool {
	res, err := s.DB().Exec("UPDATE tool_approvals SET used_at = ? WHERE id = ? AND used_at = ''", stamp(), id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// sourceLine finds a source in a knowledge search's result, as
// internal/knowledge/recall writes each hit: "1. source <id>: …".
var sourceLine = regexp.MustCompile(`(?m)^\d+\. ` + knowledge.HitSource + ` `)

// noteOutside marks the chat when a call brought outside content into it: a
// tool whose result is not Keystone's own and that ran, a source read with
// a lookup, or a search that named a source. A page is the person's own and
// is not outside content (the owner's open question, knowledge 005, is
// recommended so; P7-02 builds to it).
func (r *Runner) noteOutside(s *tenant.Store, t row, c tools.Call, res tools.Result) {
	var clause string
	_, lookup := t.lookup(c.Name)
	switch {
	case lookup && c.Name == recall.LookupReadSource:
		if res.Status == tools.StatusOK {
			clause = "this chat read a source from your knowledge store"
		}
	case lookup && c.Name == recall.LookupSearch:
		if res.Status == tools.StatusOK && sourceLine.MatchString(seam.Text(seam.Message{Blocks: res.Block.Content})) {
			clause = "this chat read a search of your knowledge store that named a source"
		}
	case lookup:
	default:
		if tool, ok := r.o.Tools.Get(c.Name); ok && !tool.Inside && res.Ran {
			clause = "this chat read outside content, from " + c.Name
		}
	}
	if clause == "" {
		return
	}
	if t.approvals != nil && t.approvals.outside == "" {
		t.approvals.outside = clause
	}
	if _, err := s.DB().Exec("INSERT INTO tool_outside(turn_id, chat_id, tool, why, at) VALUES(?, ?, ?, ?, ?)", t.ID, t.ChatID, c.Name, clause, stamp()); err != nil {
		r.logf("turns: %s turn %s: marking the chat as having read outside content: %v", t.requestID, t.ID, err)
	}
}

// readOutside says whether the turn has read outside content before the
// step it is deciding, and why, as a clause a card can give. The chat was
// marked by an earlier call; recall gave this turn, or an earlier turn of
// the chat, a source; or this server knows it from this turn. A mark that
// cannot be read counts as read.
func (r *Runner) readOutside(ctx context.Context, s *tenant.Store, t row) (bool, string) {
	if t.approvals != nil && t.approvals.outside != "" {
		return true, t.approvals.outside
	}
	var why string
	err := s.DB().QueryRowContext(ctx, "SELECT why FROM tool_outside WHERE chat_id = ? ORDER BY at LIMIT 1", t.ChatID).Scan(&why)
	switch {
	case err == nil:
		return true, why
	case !errors.Is(err, sql.ErrNoRows):
		r.logf("turns: %s turn %s: reading whether the chat read outside content, so it is taken that it did: %v", t.requestID, t.ID, err)
		return true, "Keystone could not read whether this chat has read outside content"
	}
	if t.recalled != nil && recalledSource(t.recalled.found.For(true).Record) {
		return true, "recall gave this chat a source from your knowledge store"
	}
	rows, err := s.DB().QueryContext(ctx, "SELECT record FROM turn_recall WHERE chat_id = ? AND record <> ''", t.ChatID)
	if err != nil {
		r.logf("turns: %s turn %s: reading what recall gave the chat, so it is taken that it read outside content: %v", t.requestID, t.ID, err)
		return true, "Keystone could not read what recall gave this chat"
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var rec recall.Record
		if rows.Scan(&raw) != nil || json.Unmarshal([]byte(raw), &rec) != nil {
			return true, "Keystone could not read what recall gave this chat"
		}
		if recalledSource(rec) {
			return true, "recall gave this chat a source from your knowledge store"
		}
	}
	return false, ""
}

// recalledSource says recall kept a source for a call.
func recalledSource(rec recall.Record) bool {
	for _, h := range rec.Hits {
		if h.Kept && h.Type == knowledge.HitSource {
			return true
		}
	}
	return false
}

// approvalRow is an approval as it is kept.
type approvalRow struct {
	Approval
	step      int
	messageID int64
	used      bool
}

const approvalColumns = `id, turn_id, chat_id, step, message_id, call_id, tool, input, summary, reason, why, status, asked_at, decided_at, used_at`

func scanApproval(sc interface{ Scan(...any) error }) (approvalRow, error) {
	var a approvalRow
	var input, asked, decided, used string
	err := sc.Scan(&a.ID, &a.TurnID, &a.ChatID, &a.step, &a.messageID, &a.CallID, &a.Tool, &input, &a.Summary, &a.Reason, &a.Why, &a.Status, &asked, &decided, &used)
	if err != nil {
		return approvalRow{}, err
	}
	a.Input, a.used = json.RawMessage(input), used != ""
	if a.Asked, err = time.Parse(timeLayout, asked); err != nil {
		return approvalRow{}, fmt.Errorf("approval %s: %w", a.ID, err)
	}
	if decided != "" {
		at, err := time.Parse(timeLayout, decided)
		if err != nil {
			return approvalRow{}, fmt.Errorf("approval %s: %w", a.ID, err)
		}
		a.Decided = &at
	}
	return a, nil
}

// approvalsAt returns the approvals of one step's calls, by the call's id.
func approvalsAt(s *tenant.Store, turnID string, messageID int64) (map[string]approvalRow, error) {
	rows, err := s.DB().Query("SELECT "+approvalColumns+" FROM tool_approvals WHERE turn_id = ? AND message_id = ?", turnID, messageID)
	if err != nil {
		return map[string]approvalRow{}, err
	}
	defer rows.Close()
	out := map[string]approvalRow{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return out, err
		}
		out[a.CallID] = a
	}
	return out, rows.Err()
}

// approvalsOf returns the approvals a turn asked for, oldest first. A
// pending one of a turn that has ended reads as stopped.
func approvalsOf(ctx context.Context, s *tenant.Store, t row) ([]Approval, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT "+approvalColumns+" FROM tool_approvals WHERE turn_id = ? AND status <> ? ORDER BY asked_at, step, call_id", t.ID, approvalStanding)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		if a.Status == ApprovalPending && ended(t) {
			a.Status = ApprovalStopped
		}
		out = append(out, a.Approval)
	}
	return out, rows.Err()
}

// getApproval returns one approval of one turn.
func getApproval(ctx context.Context, s *tenant.Store, turnID, id string) (approvalRow, error) {
	a, err := scanApproval(s.DB().QueryRowContext(ctx, "SELECT "+approvalColumns+" FROM tool_approvals WHERE id = ? AND turn_id = ? AND status <> ?", id, turnID, approvalStanding))
	if errors.Is(err, sql.ErrNoRows) {
		return approvalRow{}, errNoTurn
	}
	return a, err
}

// ask writes a pending approval for one call. A call already kept as
// standing, from before a restart, is asked about in its place.
func (r *Runner) ask(s *tenant.Store, t row, step int, tail int64, c seam.Block, tool tools.Tool, reason approval.Reason, why string) (approvalRow, error) {
	id, err := newID()
	if err != nil {
		return approvalRow{}, err
	}
	input := string(inputOf(c.Input))
	_, err = s.DB().Exec(`INSERT INTO tool_approvals(id, turn_id, chat_id, step, message_id, call_id, tool, input, summary, reason, why, status, asked_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)
		ON CONFLICT(turn_id, message_id, call_id) DO UPDATE SET status = 'pending', reason = excluded.reason, why = excluded.why, asked_at = excluded.asked_at
		WHERE tool_approvals.status = 'standing' AND tool_approvals.used_at = ''`,
		id, t.ID, t.ChatID, step, tail, c.ID, c.Name, input, tools.Summary(tool, inputOf(c.Input)), string(reason), why, stamp())
	if err != nil {
		return approvalRow{}, err
	}
	got, err := approvalsAt(s, t.ID, tail)
	if err != nil {
		return approvalRow{}, err
	}
	a, ok := got[c.ID]
	if !ok || a.Status != ApprovalPending {
		return approvalRow{}, fmt.Errorf("the approval for call %s is not pending", c.ID)
	}
	return a, nil
}

// standing records a call of a tool that changes something that runs under
// the person's standing policy, so it is run once, and returns its id.
func (r *Runner) standing(s *tenant.Store, t row, step int, tail int64, c seam.Block, tool tools.Tool) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	_, err = s.DB().Exec(`INSERT INTO tool_approvals(id, turn_id, chat_id, step, message_id, call_id, tool, input, summary, reason, why, status, asked_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 'policy', 'Ran under your policy for this tool: always.', 'standing', ?)
		ON CONFLICT(turn_id, message_id, call_id) DO NOTHING`,
		id, t.ID, t.ChatID, step, tail, c.ID, c.Name, string(inputOf(c.Input)), tools.Summary(tool, inputOf(c.Input)), stamp())
	if err != nil {
		return "", err
	}
	got, err := approvalsAt(s, t.ID, tail)
	if err != nil {
		return "", err
	}
	a, ok := got[c.ID]
	if !ok || a.Status != approvalStanding {
		return "", fmt.Errorf("the record of call %s is not standing", c.ID)
	}
	return a.ID, nil
}

func inputOf(raw json.RawMessage) json.RawMessage {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

// settleApproval settles a pending approval: the person's answer, an
// expiry or a stop. It reports whether this call settled it, and sends the
// event.
func settleApproval(s *tenant.Store, turnID, id, status, actor, requestID string) (Approval, bool, error) {
	res, err := s.DB().Exec("UPDATE tool_approvals SET status = ?, decided_at = ?, decided_by = ?, decided_request = ? WHERE id = ? AND turn_id = ? AND status = 'pending'",
		status, stamp(), actor, requestID, id, turnID)
	if err != nil {
		return Approval{}, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Approval{}, false, nil
	}
	a, err := getApproval(context.Background(), s, turnID, id)
	if err != nil {
		return Approval{}, true, err
	}
	return a.Approval, true, emitOne(s, turnID, EventApproval, a.Approval)
}

// waitForAnswers waits until no call of the step waits for an answer. An
// approval that waits past agents.approval_timeout expires. When the person
// stops the turn, what still waits is answered as stopped. It reports true
// when the server is stopping, and then writes nothing.
func (r *Runner) waitForAnswers(ctx context.Context, s *tenant.Store, t row, tail int64) (closing bool) {
	key := s.TenantID() + "/" + t.ID
	timeout := r.approvalTimeout(ctx, s)
	for {
		news := r.waitFor(key)
		got, err := approvalsAt(s, t.ID, tail)
		if err != nil {
			r.logf("turns: %s turn %s: reading the step's approvals while waiting: %v", t.requestID, t.ID, err)
			return false
		}
		var pending []approvalRow
		for _, a := range got {
			if a.Status != ApprovalPending {
				continue
			}
			if time.Since(a.Asked) >= timeout {
				if _, _, err := settleApproval(s, t.ID, a.ID, ApprovalExpired, "keystone", t.requestID); err != nil {
					r.logf("turns: %s turn %s: an approval expired and could not be noted: %v", t.requestID, t.ID, err)
				}
				r.wake(key)
				continue
			}
			pending = append(pending, a)
		}
		if len(pending) == 0 {
			return false
		}
		select {
		case <-news:
		case <-time.After(r.o.Config.StreamPoll):
		case <-ctx.Done():
			if r.isClosing() {
				return true
			}
			for _, a := range pending {
				if _, _, err := settleApproval(s, t.ID, a.ID, ApprovalStopped, t.actor, t.requestID); err != nil {
					r.logf("turns: %s turn %s: noting an approval as stopped: %v", t.requestID, t.ID, err)
				}
			}
			r.wake(key)
			return false
		}
	}
}

// approvalTimeout reads the person's agents.approval_timeout, within its
// bounds. A value that does not read is the shipped default.
func (r *Runner) approvalTimeout(ctx context.Context, s *tenant.Store) time.Duration {
	d, err := time.ParseDuration(r.setting(ctx, s, KeyApprovalTimeout))
	if err != nil {
		d = 24 * time.Hour
		for _, st := range r.reg {
			if st.Key == KeyApprovalTimeout {
				if v, err := time.ParseDuration(st.Default); err == nil {
					d = v
				}
			}
		}
	}
	return min(max(d, approvalTimeoutMin), approvalTimeoutMax)
}
