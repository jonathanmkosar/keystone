// Package decision chooses the model a decision call goes to and makes the
// call with its record (P14-02). A decision call is a state and typed
// questions sent to a decision model, which answers each question with a
// probability for every option; the router's verdict (P14-03) is one.
//
// Choose asks routing.Plan, with the kind decision, which decision model
// may take a call, so a call that holds sensitive content never reaches a
// hosted one: the guard is the one every other kind of call goes through.
// Run makes the call and records it as fixed rule 4 asks: a started entry
// in the audit log before anything is sent, an end entry with what came
// back, and a usage record of the kind decision, for a call that was
// refused or failed as much as for one that was answered. It refuses a
// sensitive call to a model that may not read it a second time, whatever
// its caller chose.
//
// This package is a guard for fixed rule 3. It imports neither the
// settings registry nor the tenant settings, itself or through another
// package: the model's name, the sensitive mark, the gate and the estimate
// are arguments. The fixed rules check in verify fails if that changes.
//
// docs/architecture/decision-calls.md describes it.
package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/usage"
)

// Kind is the kind of task a decision call's usage record carries.
const Kind = string(routing.KindDecision)

// ErrNoModel is wrapped when no decision model may take a call: none is
// named, the one named is not a usable decision model this build can call,
// or the call is sensitive and the one named may not read it. The rules
// alone decide then. When the call was sensitive, routing.ErrSensitive is
// wrapped too.
var ErrNoModel = errors.New("no decision model may take the call")

// Choose returns the decision model one call goes to: the model named, when
// it is a usable decision model that can is true for, and, for a sensitive
// call, one that may read sensitive content. There is no fallback to
// another decision model: a decision that cannot be had is left to rules.
// can is nil to take every model; a caller passes one that says whether
// this build can open the model's adapter as a decision model.
func Choose(name string, sensitive bool, usable []models.Model, can func(models.Model) bool) (models.Model, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return models.Model{}, fmt.Errorf("%w: no decision model is named", ErrNoModel)
	}
	var open []models.Model
	for _, m := range usable {
		if can == nil || can(m) {
			open = append(open, m)
		}
	}
	route, err := routing.Plan(
		routing.Rules{Models: map[routing.Kind]string{routing.KindDecision: name}},
		routing.Ask{Kind: routing.KindDecision, Model: name, Sensitive: sensitive},
		open,
	)
	if err != nil {
		return models.Model{}, fmt.Errorf("%w: %w", ErrNoModel, err)
	}
	m := route.Models[0]
	if sensitive && !m.MayReadSensitive() {
		// routing.Plan never returns one; this says so a second time.
		return models.Model{}, fmt.Errorf("%w: %w", ErrNoModel, routing.ErrSensitive)
	}
	return m, nil
}

// Opened is a decision model, opened: its entry, the Decider that calls it
// and the values it was built with, for the audit log to take out.
type Opened struct {
	Model   models.Model
	Decider seam.Decider
	Secrets []string
}

// Call is one decision call and who it is made for.
type Call struct {
	// RequestID and Actor are what the audit entries and the usage record
	// carry: the API call that set the decision off, and who made it.
	RequestID string
	Actor     string
	// Sensitive says the state may hold content from a sensitive space, as
	// the newest message of a held chat does.
	Sensitive bool
	// Request is the decision. Its model is the opened model's name; empty
	// takes it.
	Request seam.DecisionRequest
}

// Gate is asked about a call before it is sent, as the monthly cap is. A
// non-nil answer stops the call, which is recorded as refused and returned
// as that error.
type Gate func(ctx context.Context, m models.Model, req seam.DecisionRequest) *seam.Error

// Options are what a caller adds to Run, both optional.
type Options struct {
	// Gate is asked before the call is sent. nil sends every call.
	Gate Gate
	// Estimate counts a call whose endpoint gave no usage, as the budget
	// counts. nil leaves the count at zero.
	Estimate func(models.Model, seam.DecisionRequest) seam.Usage
}

// Run makes one decision call and records it. The result answers every
// question of the request, each with a probability for every option.
//
// A request that is not valid is refused with an error of the kind invalid
// and is not a call: nothing is sent and nothing recorded. A call that
// cannot be recorded is not made. Every other call has a started entry, an
// end entry and a usage record of the kind decision. A call is refused,
// recorded as such and not sent, when it is sensitive and the model may not
// read sensitive content, when the gate says no, or when the adapter
// refuses it as over its limits (an error of the kind too_long). A local
// model costs nothing.
func Run(ctx context.Context, s *tenant.Store, o Opened, c Call, opts Options) (seam.DecisionResult, error) {
	m, req := o.Model, c.Request
	if req.Model == "" {
		req.Model = m.Name
	}
	if req.Model != m.Name {
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrInvalid, Message: fmt.Sprintf("the decision request names the model %s, and the model opened is %s", req.Model, m.Name)}
	}
	if o.Decider == nil {
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrInvalid, Message: "the decision model " + m.Name + " was not opened"}
	}
	sent, err := seam.EncodeDecisionRequest(req)
	if err != nil {
		return seam.DecisionResult{}, &seam.Error{Kind: seam.ErrInvalid, Message: "the decision request is not valid: " + err.Error()}
	}
	bg := context.Background()
	start := audit.Entry{Kind: audit.KindModel, RequestID: c.RequestID, Actor: c.Actor, Action: m.Name, Outcome: audit.OutcomeStarted, Sent: string(sent)}
	if _, err := audit.Append(bg, s, start, o.Secrets...); err != nil {
		return seam.DecisionResult{}, fmt.Errorf("recording the decision call to %s: %w", m.Name, err)
	}
	var recordErr error
	finish := func(outcome audit.Outcome, received any, u seam.Usage, took time.Duration) {
		raw, _ := json.Marshal(received)
		cost := int64(0)
		if m.Class != models.ClassLocal {
			cost = m.Price.CostMicros(u.InputTokens, u.OutputTokens, 0, 0)
		}
		end := audit.Entry{
			Kind: audit.KindModel, RequestID: c.RequestID, Actor: c.Actor, Action: m.Name, Outcome: outcome,
			Received: string(raw), Usage: audit.Usage(u), CostMicros: cost, DurationMS: took.Milliseconds(),
		}
		if _, err := audit.Append(bg, s, end, o.Secrets...); err != nil {
			recordErr = fmt.Errorf("recording the end of the decision call to %s: %w", m.Name, err)
		}
		rec := usage.Record{
			RequestID: c.RequestID, Kind: Kind, Model: m.Name, Class: m.Class, Outcome: usage.OutcomeError,
			InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, Estimated: u.Estimated,
			CostMicros: cost, DurationMS: took.Milliseconds(),
		}
		switch outcome {
		case audit.OutcomeOK:
			rec.Outcome = usage.OutcomeOK
		case audit.OutcomeRefused:
			rec.Outcome = usage.OutcomeRefused
		}
		if _, err := usage.Add(bg, s, rec); err != nil && recordErr == nil {
			recordErr = fmt.Errorf("writing the usage record of the decision call to %s: %w", m.Name, err)
		}
	}
	// failed returns a call's own error, with the record's when the end of
	// the call could not be written.
	failed := func(err error) (seam.DecisionResult, error) {
		if recordErr != nil {
			return seam.DecisionResult{}, errors.Join(err, recordErr)
		}
		return seam.DecisionResult{}, err
	}
	refuse := func(e *seam.Error) (seam.DecisionResult, error) {
		finish(audit.OutcomeRefused, e, seam.Usage{}, 0)
		return failed(e)
	}

	// Fixed rule 3, a second time: whatever chose the model, a sensitive
	// state goes only to a model that may read it.
	if c.Sensitive && !m.MayReadSensitive() {
		return refuse(&seam.Error{Kind: seam.ErrInvalid, Message: "the decision call holds sensitive content, and the model " + m.Name + " is not local or trusted, so it was not sent"})
	}
	if opts.Gate != nil {
		if e := opts.Gate(ctx, m, req); e != nil {
			return refuse(e)
		}
	}

	began := time.Now()
	res, err := o.Decider.Decide(ctx, req)
	took := time.Since(began)
	if err != nil {
		var se *seam.Error
		if !errors.As(err, &se) {
			se = &seam.Error{Kind: seam.ErrOther, Message: err.Error()}
			if cerr := ctx.Err(); cerr != nil {
				se = &seam.Error{Kind: seam.ErrUnavailable, Message: "the call was stopped: " + cerr.Error()}
			}
		}
		outcome := audit.OutcomeError
		if se.Kind == seam.ErrTooLong || se.Kind == seam.ErrSpendCap {
			outcome = audit.OutcomeRefused
		}
		finish(outcome, se, seam.Usage{}, took)
		return failed(err)
	}
	if res.Model == "" {
		res.Model = req.Model
	}
	if err := seam.ValidateDecisionResult(req, res); err != nil {
		se := &seam.Error{Kind: seam.ErrOther, Message: "the decision answer is not usable: " + err.Error()}
		finish(audit.OutcomeError, se, res.Usage, took)
		return failed(se)
	}
	if res.Usage.InputTokens == 0 && res.Usage.OutputTokens == 0 && opts.Estimate != nil {
		res.Usage = opts.Estimate(m, req)
		res.Usage.Estimated = true
	}
	finish(audit.OutcomeOK, res, res.Usage, took)
	if recordErr != nil {
		// The answer came back, and the end of the call is not in the log:
		// the caller is told, and does not use an answer with no record.
		return seam.DecisionResult{}, recordErr
	}
	return res, nil
}

// Text is the decision request as plain text: the state, then each
// question's instructions and its options with their descriptions. It is
// what a caller counts for Options.Estimate and for the most a call could
// cost.
func Text(req seam.DecisionRequest) string {
	var b strings.Builder
	b.WriteString(req.State)
	for _, q := range req.Questions {
		b.WriteString("\n\n")
		b.WriteString(q.ID)
		b.WriteString(": ")
		b.WriteString(q.Instructions)
		for _, o := range q.Options {
			b.WriteString("\n- ")
			b.WriteString(o.ID)
			if o.Description != "" {
				b.WriteString(": ")
				b.WriteString(o.Description)
			}
		}
	}
	return b.String()
}

// AsRequest is the decision request as a seam request for the budget: one
// message of Text, and a reply of one token for each question, since a
// decision model writes none.
func AsRequest(m models.Model, req seam.DecisionRequest) seam.Request {
	return seam.Request{
		Model:           m.Name,
		Messages:        []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: Text(req)}}}},
		MaxOutputTokens: max(len(req.Questions), 1),
	}
}
