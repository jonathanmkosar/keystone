package fake

import (
	"context"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The fake as a decision model (P14-02). It answers a decision call from
// the scripts whose "when" has "decision": true, as it answers a turn from
// the others (decision 0011): the request's state is the user text, the
// questions' instructions are the system text, and the model is the
// request's. The script's one step is the answer, or the error the call
// fails with. An answer that does not answer the request, one answer for
// each question with a probability for every option, is refused, as a real
// endpoint's would be.

// Decide answers one decision call, as seam.Decider describes. A request
// no script answers ends with an error of the kind invalid.
func (p *Provider) Decide(ctx context.Context, req seam.DecisionRequest) (seam.DecisionResult, error) {
	if err := ctx.Err(); err != nil {
		return seam.DecisionResult{}, err
	}
	if err := seam.ValidateDecisionRequest(req); err != nil {
		return seam.DecisionResult{}, refused(fmt.Errorf("the request is not valid: %w", err))
	}
	var instructions []string
	for _, q := range req.Questions {
		instructions = append(instructions, q.Instructions)
	}
	state := fold(req.State)
	s, ok := p.match(state, state, fold(strings.Join(instructions, "\n")), req.Model, true)
	if !ok {
		return seam.DecisionResult{}, refused(fmt.Errorf("no decision script answers a request whose state is %q", short(state)))
	}
	step := s.Steps[0]
	if step.Error != nil {
		failure := *step.Error
		return seam.DecisionResult{}, &failure
	}
	res := *step.Decision
	res.Answers = append([]seam.DecisionAnswer(nil), res.Answers...)
	if res.Model == "" {
		res.Model = req.Model
	}
	if err := seam.ValidateDecisionResult(req, res); err != nil {
		return seam.DecisionResult{}, refused(fmt.Errorf("decision script %q does not answer the request: %w", s.Name, err))
	}
	return res, nil
}
