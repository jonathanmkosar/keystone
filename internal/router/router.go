// Package router chooses the model and the effort for one message of a
// chat on Auto (P14-03). The admin's ladder lists the models a person's
// Auto may use, smallest first. Four rules, which cost nothing, narrow the
// ladder to the candidates for this call; with two or more, a decision
// model's verdict picks one and an effort, with a probability. The chosen
// step's model and the effort are then the ask the turn gives routing.Plan,
// where the privacy filter, the capability filter and the fallback chain
// work as they always have.
//
// Every message is routed afresh, from that message and its call's facts:
// nothing from an earlier message holds a chat on a step. The turn holds
// the choice for that message's whole turn, tool steps included.
//
// The router reads nothing itself: no store, no setting, no clock it was
// not given. The turn passes the settings, the facts and a function that
// makes the decision call. It holds no state between calls.
//
// This package is a guard for fixed rule 3. Each step is asked of
// routing.Plan on its own, so the router only ever narrows or orders what
// the plan allows; a held chat keeps only steps that may read sensitive
// content, its verdict goes only to a decision model that may read it
// (decision.Choose), and the step chosen is checked once more before it is
// returned, whatever the verdict said. It imports neither the settings
// registry nor the tenant settings, itself or through another package. The
// fixed rules check in verify fails if that changes.
//
// docs/architecture/router.md describes it.
package router

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/decision"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The codes of a Refusal, which the turn passes on as its own.
const (
	// CodeSensitive: the chat is held and no candidate may read sensitive
	// content. The same code the turn has always used.
	CodeSensitive = "sensitive"
	// CodeLocalUnavailable: the local machine is not answering and no
	// other step may take the message. A held chat waits for the machine.
	CodeLocalUnavailable = "local_unavailable"
	// CodeNoModel: no step of the ladder can take the call.
	CodeNoModel = "no_model"
	// CodeNoLadder: the person has no ladder, so Auto is not offered.
	CodeNoLadder = "no_ladder"
	// CodeNoStronger: "Try a stronger model" has no step above the one
	// that answered that may take the call.
	CodeNoStronger = "no_stronger"
)

// NoticeLocalUnavailable is the notice a reply carries when local steps
// were passed over because the local machine was not answering.
const NoticeLocalUnavailable = "local_unavailable"

// Refusal is why a message on Auto cannot be routed. Its message is safe to
// show the person.
type Refusal struct {
	Code    string
	Message string
	// Skipped is every step left out, and why.
	Skipped []Skip
}

func (r *Refusal) Error() string { return "router: " + r.Code + ": " + r.Message }

// Unwrap gives routing.ErrSensitive for CodeSensitive, so a caller that
// already sorts routing's errors sorts this one with them.
func (r *Refusal) Unwrap() error {
	if r.Code == CodeSensitive {
		return routing.ErrSensitive
	}
	return nil
}

// Verdict makes one decision call: the turn opens m as a decision model
// (adapters.OpenDecider) and sends req through decision.Run, which writes
// its audit entries and usage record, with sensitive as the call's mark.
// The context ends at router.verdict_timeout.
type Verdict func(ctx context.Context, m models.Model, sensitive bool, req seam.DecisionRequest) (seam.DecisionResult, error)

// Input is everything one message is routed from.
type Input struct {
	Settings Settings
	Facts    Facts
	// Usable are the person's usable models (models.Usable).
	Usable []models.Model
	// Verdict makes the decision call. nil means the rules alone decide.
	Verdict Verdict
	// CanDecide says whether this build can open a model as a decision
	// model (adapters.Decides on its adapter). nil takes every model.
	CanDecide func(models.Model) bool
}

// Why the rules alone decided, in Record.RulesOnlyWhy.
const (
	// OnlyOneCandidate: one step was left, so no verdict was asked.
	OnlyOneCandidate = "one_candidate"
	// OnlyNoDecisionModel: no decision model is named, or no verdict
	// function was given.
	OnlyNoDecisionModel = "no_decision_model"
	// OnlyDecisionModelUnusable: the decision model named is not a usable
	// decision model this build can open.
	OnlyDecisionModelUnusable = "decision_model_unusable"
	// OnlyHeld: the chat is held and the decision model may not read it.
	OnlyHeld = "held"
	// OnlyLocalUnavailable: the decision model is local and the local
	// machine is not answering.
	OnlyLocalUnavailable = "local_unavailable"
	// OnlyTimeout: no verdict within router.verdict_timeout.
	OnlyTimeout = "timeout"
	// OnlyError: the decision call failed, or its answer was not usable.
	OnlyError = "error"
	// OnlyStronger: the person asked for a stronger model than the one
	// that answered (Stronger).
	OnlyStronger = "stronger_asked"
)

// Shown are the facts the decision model is shown besides the message's
// text. A verdict's record keeps them as JSON; the text is in the chat.
type Shown struct {
	Tokens       int    `json:"tokens"`
	Images       bool   `json:"images"`
	Files        bool   `json:"files"`
	Tools        int    `json:"tools"`
	PreviousStep string `json:"previous_step,omitempty"`
	Turns        int    `json:"turns"`
}

// Record is one routing's record, the shape of a row of router_verdicts
// (P14-04) less its ids and time.
type Record struct {
	// DecisionModel is the decision model asked, or empty.
	DecisionModel string `json:"decision_model,omitempty"`
	Shown         Shown  `json:"shown"`
	// StepAnswered and Probability are the verdict's step and its
	// probability; EffortAnswered its effort. Empty and zero with no
	// verdict.
	StepAnswered   string      `json:"step_answered,omitempty"`
	Probability    float64     `json:"probability,omitempty"`
	EffortAnswered seam.Effort `json:"effort_answered,omitempty"`
	// Step and Effort are what was chosen.
	Step   string      `json:"step"`
	Effort seam.Effort `json:"effort"`
	// MovedUp: the verdict was under router.min_confidence and the step
	// moved one candidate up.
	MovedUp bool `json:"moved_up,omitempty"`
	// TimedOut: no verdict within router.verdict_timeout.
	TimedOut bool `json:"timed_out,omitempty"`
	// RulesOnly and RulesOnlyWhy (an Only constant): no verdict decided.
	RulesOnly    bool   `json:"rules_only,omitempty"`
	RulesOnlyWhy string `json:"rules_only_why,omitempty"`
	// LatencyMS is how long the decision call took, or zero with none.
	LatencyMS int64 `json:"latency_ms,omitempty"`
	// LocalUnavailable: this message's decision call found the local
	// machine not answering. The turn keeps the time as LocalFailedAt for
	// the messages after it.
	LocalUnavailable bool `json:"local_unavailable,omitempty"`
}

// Result is the router's answer for one message.
type Result struct {
	// Model is the chosen step's model, the ask's model for routing.Plan.
	Model models.Model
	// Step is its place on the ladder, from 1, of Steps.
	Step, Steps int
	// Effort is the effort for the call, never above router.max_effort.
	Effort seam.Effort
	// Reason is one line, written by Keystone and never by a model.
	Reason string
	// Notice is NoticeLocalUnavailable or empty.
	Notice string
	// Switched says the step is not the previous message's, so the call
	// loses that provider's prompt cache.
	Switched bool
	// Candidates are the steps the rules left, smallest first, and
	// Skipped the steps they left out.
	Candidates []Candidate
	Skipped    []Skip
	Record     Record
}

// Route chooses the model and the effort for one message on Auto. The
// error is a *Refusal when no step may take the message, the context's
// error when the turn was stopped while the verdict was asked, and an
// error for settings that cannot be worked from.
func Route(ctx context.Context, in Input) (Result, error) {
	s, f := in.Settings, in.Facts
	if err := s.Check(); err != nil {
		return Result{}, err
	}
	cands, skipped := Candidates(s, f, in.Usable)
	if len(cands) == 0 {
		return Result{Skipped: skipped}, refusal(s, skipped)
	}
	res := Result{Steps: len(s.Ladder), Candidates: cands, Skipped: skipped}
	res.Record.Shown = shown(f)
	down := f.localDown(s)
	if down && hasSkip(skipped, SkipLocalDown) {
		res.Notice = NoticeLocalUnavailable
	}

	// rulesOnly: the lowest candidate, at the default effort.
	rulesOnly := func(why string) (Result, error) {
		res.Record.RulesOnly, res.Record.RulesOnlyWhy = true, why
		return finish(in, res, cands[0], s.capped(s.DefaultEffort), rulesReason(why, s))
	}
	if len(cands) == 1 {
		return rulesOnly(OnlyOneCandidate)
	}
	if in.Verdict == nil || s.DecisionModel == "" {
		return rulesOnly(OnlyNoDecisionModel)
	}
	dm, err := decision.Choose(s.DecisionModel, f.Held, in.Usable, in.CanDecide)
	if err != nil {
		if errors.Is(err, routing.ErrSensitive) {
			return rulesOnly(OnlyHeld)
		}
		return rulesOnly(OnlyDecisionModelUnusable)
	}
	if f.Held && !dm.MayReadSensitive() {
		// decision.Choose never returns one; this says so a second time.
		return rulesOnly(OnlyHeld)
	}
	res.Record.DecisionModel = dm.Name
	if down && dm.Class == models.ClassLocal {
		res.Notice = NoticeLocalUnavailable
		return rulesOnly(OnlyLocalUnavailable)
	}

	req := request(s, f, cands, dm, in.Usable)
	vctx, cancel := context.WithTimeout(ctx, s.VerdictTimeout)
	began := time.Now()
	out, err := in.Verdict(vctx, dm, f.Held, req)
	res.Record.LatencyMS = time.Since(began).Milliseconds()
	timedOut := errors.Is(vctx.Err(), context.DeadlineExceeded)
	cancel()
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return Result{}, ctx.Err()
		case timedOut:
			res.Record.TimedOut = true
			return rulesOnly(OnlyTimeout)
		case unavailable(err) && dm.Class == models.ClassLocal:
			// The local machine is not answering: rule 4 for this message.
			res.Record.LocalUnavailable = true
			f.LocalFailedAt, f.Now = began, began
			again, skippedAgain := Candidates(s, f, in.Usable)
			if len(again) == 0 {
				return Result{Skipped: skippedAgain, Record: res.Record}, refusal(s, skippedAgain)
			}
			cands, res.Candidates, res.Skipped = again, again, skippedAgain
			res.Notice = NoticeLocalUnavailable
			return rulesOnly(OnlyLocalUnavailable)
		}
		return rulesOnly(OnlyError)
	}
	if timedOut {
		res.Record.TimedOut = true
		return rulesOnly(OnlyTimeout)
	}
	step, effort, err := read(req, out)
	if err != nil {
		return rulesOnly(OnlyError)
	}
	at := -1
	for i, c := range cands {
		if c.Model.Name == step.Choice {
			at = i
		}
	}
	if at < 0 {
		return rulesOnly(OnlyError)
	}
	p := step.Probabilities[step.Choice]
	res.Record.StepAnswered, res.Record.Probability, res.Record.EffortAnswered = step.Choice, p, effort
	reason := fmt.Sprintf("%s, effort %s, decision %.2f", label(cands[at].Model), s.capped(effort), p)
	if p < s.MinConfidence && at+1 < len(cands) {
		at++
		res.Record.MovedUp = true
		reason = fmt.Sprintf("Moved up: decision unsure (%.2f)", p)
	}
	return finish(in, res, cands[at], s.capped(effort), reason)
}

// Stronger is "Try a stronger model" (P14-04): the candidate one above the
// step that answered, at effort, capped. It is refused with CodeNoStronger
// when the step that answered is the top candidate or is no longer on the
// ladder.
func Stronger(in Input, answered string, effort seam.Effort) (Result, error) {
	s, f := in.Settings, in.Facts
	if err := s.Check(); err != nil {
		return Result{}, err
	}
	at := 0
	for i, r := range s.Ladder {
		if r.Name == answered {
			at = i + 1
		}
	}
	cands, skipped := Candidates(s, f, in.Usable)
	if at == 0 {
		return Result{Skipped: skipped}, &Refusal{Code: CodeNoStronger, Skipped: skipped, Message: "The model that answered is not on the ladder now, so there is no step above it."}
	}
	for _, c := range cands {
		if c.Step > at {
			res := Result{Steps: len(s.Ladder), Candidates: cands, Skipped: skipped}
			res.Record.Shown = shown(f)
			res.Record.RulesOnly, res.Record.RulesOnlyWhy = true, OnlyStronger
			if f.localDown(s) && hasSkip(skipped, SkipLocalDown) {
				res.Notice = NoticeLocalUnavailable
			}
			return finish(in, res, c, s.capped(effort), "Stronger model asked for")
		}
	}
	return Result{Skipped: skipped}, &Refusal{Code: CodeNoStronger, Skipped: skipped, Message: "No step above the one that answered may take this message."}
}

// finish fills the result for the chosen candidate and checks it a last
// time against fixed rule 3.
func finish(in Input, res Result, c Candidate, effort seam.Effort, reason string) (Result, error) {
	f := in.Facts
	if f.Held && !c.Model.MayReadSensitive() {
		// Candidates never keeps one; nothing a verdict says can put one
		// back.
		return Result{Skipped: res.Skipped}, &Refusal{Code: CodeSensitive, Skipped: res.Skipped, Message: "This chat holds content from a sensitive space, and the step chosen is not local or trusted, so it was not sent."}
	}
	res.Model, res.Step, res.Effort = c.Model, c.Step, effort
	res.Record.Step, res.Record.Effort = c.Model.Name, effort
	if f.PreviousStep != "" && f.PreviousStep != c.Model.Name {
		res.Switched = true
		reason += "; switched from " + labelOf(f.PreviousStep, in.Usable)
	}
	res.Reason = reason
	return res, nil
}

func shown(f Facts) Shown {
	return Shown{Tokens: f.Tokens, Images: f.Images, Files: f.Files, Tools: f.Tools, PreviousStep: f.PreviousStep, Turns: f.Turns}
}

func hasSkip(skipped []Skip, why string) bool {
	for _, k := range skipped {
		if k.Why == why {
			return true
		}
	}
	return false
}

// unavailable reports whether a decision call failed because its endpoint
// did not answer.
func unavailable(err error) bool {
	var se *seam.Error
	return errors.As(err, &se) && se.Kind == seam.ErrUnavailable
}
