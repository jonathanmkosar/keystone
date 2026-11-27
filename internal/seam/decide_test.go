package seam

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func decisionRequest() DecisionRequest {
	return DecisionRequest{
		Model: "house-decide",
		State: "An invented message about the garden.",
		Questions: []DecisionQuestion{
			{ID: "step", Instructions: "Which model?", Type: QuestionChoice, Options: []DecisionOption{
				{ID: "house-chat", Description: "step 1 of 2"}, {ID: "mid-hosted", Description: "step 2 of 2"},
			}},
			{ID: "simple", Instructions: "Is it simple?", Type: QuestionYesNo},
		},
	}
}

func decisionResult() DecisionResult {
	return DecisionResult{
		Model: "house-decide",
		Answers: []DecisionAnswer{
			{Question: "step", Choice: "house-chat", Probabilities: map[string]float64{"house-chat": 0.8, "mid-hosted": 0.2}},
			{Question: "simple", Choice: "yes", Probabilities: map[string]float64{"yes": 0.7, "no": 0.3}},
		},
		Usage: Usage{InputTokens: 40, OutputTokens: 2},
	}
}

func TestDecisionRequestChecks(t *testing.T) {
	if err := ValidateDecisionRequest(decisionRequest()); err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeDecisionRequest(decisionRequest())
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeDecisionRequest(raw)
	if err != nil || !reflect.DeepEqual(back, decisionRequest()) {
		t.Errorf("the request did not come back the same: %+v, %v", back, err)
	}
	for _, c := range []struct {
		name   string
		change func(*DecisionRequest)
		want   string
	}{
		{"no model", func(r *DecisionRequest) { r.Model = " " }, "model"},
		{"no state", func(r *DecisionRequest) { r.State = "" }, "state"},
		{"no question", func(r *DecisionRequest) { r.Questions = nil }, "at least one question"},
		{"an id with a space", func(r *DecisionRequest) { r.Questions[0].ID = "the step" }, "is not 1 to 100"},
		{"an id too long", func(r *DecisionRequest) { r.Questions[0].ID = strings.Repeat("a", 101) }, "is not 1 to 100"},
		{"an id twice", func(r *DecisionRequest) { r.Questions[1].ID = "step" }, "used twice"},
		{"no instructions", func(r *DecisionRequest) { r.Questions[0].Instructions = "" }, "no instructions"},
		{"one option", func(r *DecisionRequest) { r.Questions[0].Options = r.Questions[0].Options[:1] }, "2 to 255"},
		{"an option twice", func(r *DecisionRequest) { r.Questions[0].Options[1].ID = "house-chat" }, "listed twice"},
		{"an odd type", func(r *DecisionRequest) { r.Questions[0].Type = "score" }, "not choice or yes_no"},
		{"a yes_no with another option", func(r *DecisionRequest) {
			r.Questions[1].Options = []DecisionOption{{ID: "maybe"}}
		}, "yes and no"},
	} {
		r := decisionRequest()
		r.Questions = append([]DecisionQuestion(nil), r.Questions...)
		r.Questions[0].Options = append([]DecisionOption(nil), r.Questions[0].Options...)
		c.change(&r)
		err := ValidateDecisionRequest(r)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error that says %q", c.name, err, c.want)
		}
	}
	// A yes_no may describe its two answers.
	r := decisionRequest()
	r.Questions[1].Options = []DecisionOption{{ID: OptionYes, Description: "short"}, {ID: OptionNo}}
	if err := ValidateDecisionRequest(r); err != nil {
		t.Errorf("a yes_no that describes yes and no was refused: %v", err)
	}
	if got := OptionIDs(r.Questions[1]); !reflect.DeepEqual(got, []string{"yes", "no"}) {
		t.Errorf("a yes_no's options are %v", got)
	}
}

func TestDecisionResultChecks(t *testing.T) {
	req := decisionRequest()
	if err := ValidateDecisionResult(req, decisionResult()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		change func(*DecisionResult)
		want   string
	}{
		{"an answer short", func(r *DecisionResult) { r.Answers = r.Answers[:1] }, "1 answers for 2 questions"},
		{"out of order", func(r *DecisionResult) { r.Answers[0], r.Answers[1] = r.Answers[1], r.Answers[0] }, "answer 1 is to"},
		{"a probability missing", func(r *DecisionResult) { delete(r.Answers[0].Probabilities, "mid-hosted") }, "1 probabilities for 2 options"},
		{"an option not asked", func(r *DecisionResult) {
			delete(r.Answers[0].Probabilities, "mid-hosted")
			r.Answers[0].Probabilities["large-hosted"] = 0.2
		}, "no probability for the option mid-hosted"},
		{"not a number", func(r *DecisionResult) { r.Answers[0].Probabilities["mid-hosted"] = math.NaN() }, "not from 0 to 1"},
		{"below zero", func(r *DecisionResult) {
			r.Answers[0].Probabilities = map[string]float64{"house-chat": 1.2, "mid-hosted": -0.2}
		}, "not from 0 to 1"},
		{"not summing to one", func(r *DecisionResult) { r.Answers[1].Probabilities["no"] = 0.6 }, "sum to"},
		{"a choice not offered", func(r *DecisionResult) { r.Answers[0].Choice = "large-hosted" }, "not one of its options"},
		{"a choice not the most probable", func(r *DecisionResult) { r.Answers[0].Choice = "mid-hosted" }, "not its most probable"},
		{"negative usage", func(r *DecisionResult) { r.Usage.InputTokens = -1 }, "negative"},
	} {
		r := decisionResult()
		for i := range r.Answers {
			p := map[string]float64{}
			for k, v := range r.Answers[i].Probabilities {
				p[k] = v
			}
			r.Answers[i].Probabilities = p
		}
		c.change(&r)
		err := ValidateDecisionResult(req, r)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error that says %q", c.name, err, c.want)
		}
	}
	// Rounding within a hundredth is taken.
	r := decisionResult()
	r.Answers[0].Probabilities = map[string]float64{"house-chat": 0.805, "mid-hosted": 0.2}
	if err := ValidateDecisionResult(req, r); err != nil {
		t.Errorf("probabilities summing to 1.005 were refused: %v", err)
	}
	if a, ok := decisionResult().Answer("simple"); !ok || a.Choice != "yes" {
		t.Errorf("Answer(simple) is %+v, %v", a, ok)
	}
	if got := ChoiceOf([]string{"a", "b", "c"}, map[string]float64{"a": 0.4, "b": 0.4, "c": 0.2}); got != "a" {
		t.Errorf("a tie went to %s, want the earlier option", got)
	}
}
