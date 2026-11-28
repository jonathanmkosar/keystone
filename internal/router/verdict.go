package router

import (
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The verdict's two questions, by id.
const (
	QuestionStep   = "step"
	QuestionEffort = "effort"
)

// verdictEfforts are the efforts the verdict chooses among. The router
// asks for no more than router.max_effort whatever it chooses.
var verdictEfforts = []seam.Effort{seam.EffortLow, seam.EffortMedium, seam.EffortHigh}

// request is the decision request the verdict sends for these candidates:
// the newest message's text, cut to router.verdict_max_chars, and the
// call's facts, never the rest of the chat; and the two questions, the
// step among the candidates and the effort.
func request(s Settings, f Facts, cands []Candidate, dm models.Model, usable []models.Model) seam.DecisionRequest {
	text := strings.TrimSpace(f.Text)
	if r := []rune(text); len(r) > s.VerdictMaxChars {
		text = string(r[:s.VerdictMaxChars])
	}
	if text == "" {
		text = "(no text)"
	}
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	previous := "none, this is the chat's first message"
	if f.PreviousStep != "" {
		previous = labelOf(f.PreviousStep, usable)
	}
	var b strings.Builder
	b.WriteString("Newest message:\n")
	b.WriteString(text)
	b.WriteString("\n\nFacts about the call:\n")
	fmt.Fprintf(&b, "- Estimated tokens: %d\n", f.Tokens)
	fmt.Fprintf(&b, "- Holds images: %s\n", yes(f.Images))
	fmt.Fprintf(&b, "- Holds files: %s\n", yes(f.Files))
	fmt.Fprintf(&b, "- Tools offered: %d\n", f.Tools)
	fmt.Fprintf(&b, "- Step the previous message used: %s\n", previous)
	fmt.Fprintf(&b, "- Turns in the chat so far: %d", f.Turns)

	steps := make([]seam.DecisionOption, len(cands))
	for i, c := range cands {
		desc := s.StepNotes[c.Model.Name]
		if desc == "" {
			desc = fmt.Sprintf("%s: step %d of %d, smallest first", label(c.Model), i+1, len(cands))
		}
		steps[i] = seam.DecisionOption{ID: c.Model.Name, Description: desc}
	}
	efforts := []seam.DecisionOption{
		{ID: string(seam.EffortLow), Description: "a quick answer: a fact, a short reply, a small change"},
		{ID: string(seam.EffortMedium), Description: "some thought: a few steps, a careful explanation"},
		{ID: string(seam.EffortHigh), Description: "hard thinking: a long plan, a proof, a tricky problem"},
	}
	return seam.DecisionRequest{
		Model: dm.Name,
		State: b.String(),
		Questions: []seam.DecisionQuestion{
			{ID: QuestionStep, Type: seam.QuestionChoice, Options: steps,
				Instructions: "Which is the smallest model that will answer the newest message well? The models are listed smallest first."},
			{ID: QuestionEffort, Type: seam.QuestionChoice, Options: efforts,
				Instructions: "How much thinking does the newest message need?"},
		},
	}
}

// read takes the step and the effort out of a verdict, which must answer
// the request it was sent for.
func read(req seam.DecisionRequest, out seam.DecisionResult) (seam.DecisionAnswer, seam.Effort, error) {
	if err := seam.ValidateDecisionResult(req, out); err != nil {
		return seam.DecisionAnswer{}, "", err
	}
	step, ok := out.Answer(QuestionStep)
	if !ok {
		return seam.DecisionAnswer{}, "", fmt.Errorf("the verdict has no step")
	}
	effort, ok := out.Answer(QuestionEffort)
	if !ok {
		return seam.DecisionAnswer{}, "", fmt.Errorf("the verdict has no effort")
	}
	for _, e := range verdictEfforts {
		if string(e) == effort.Choice {
			return step, e, nil
		}
	}
	return seam.DecisionAnswer{}, "", fmt.Errorf("the verdict's effort %q is not low, medium or high", short(effort.Choice))
}

// rulesReason is the reason line when the rules alone decided.
func rulesReason(why string, s Settings) string {
	switch why {
	case OnlyOneCandidate:
		return "Rules only: one step can take this message"
	case OnlyNoDecisionModel:
		return "Rules only: no decision model"
	case OnlyDecisionModelUnusable:
		return "Rules only: the decision model cannot be used"
	case OnlyHeld:
		return "Rules only: the decision model may not read this chat"
	case OnlyLocalUnavailable:
		return "Rules only: local machine not answering"
	case OnlyTimeout:
		return "Rules only: no decision within " + s.VerdictTimeout.String()
	}
	return "Rules only: the decision failed"
}

// label is what the picker shows for a model, or its name.
func label(m models.Model) string {
	if strings.TrimSpace(m.Label) != "" {
		return m.Label
	}
	return m.Name
}

// labelOf is the label of a registry name among the usable models, or the
// name.
func labelOf(name string, usable []models.Model) string {
	for _, m := range usable {
		if m.Name == name {
			return label(m)
		}
	}
	return name
}
