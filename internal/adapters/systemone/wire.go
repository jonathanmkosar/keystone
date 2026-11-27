package systemone

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The wire shape, as Ollama's decision API reads and writes it:
//
//	{"model": "example-decide:9b", "state": "...",
//	 "questions": {
//	   "step":   {"type": "choice", "instructions": "...", "criteria": {"house-chat": "...", "mid-hosted": "..."}},
//	   "simple": {"type": "noul", "instructions": "...", "criteria": {"true": "...", "false": "..."}}}}
//
//	{"model": "example-decide:9b",
//	 "answers": {
//	   "step":   {"type": "choice", "choice": "house-chat", "probabilities": {"house-chat": 0.91, "mid-hosted": 0.09}, "confidence": 0.56},
//	   "simple": {"type": "noul", "noul": 0.8}},
//	 "usage": {"input_tokens": 212, "output_tokens": 2}}
//
// Questions and options are JSON objects keyed by id. They are written in
// the request's order, so the model reads the options in the order the
// caller gave them. A yes_no question is Ollama's noul, whose answer is the
// probability of true: yes is that, and no is the rest.

const (
	wireChoice = "choice"
	wireYesNo  = "noul"
)

// field is one key and value of a JSON object written in order.
type field struct {
	key   string
	value any
}

// object is a JSON object whose keys keep their order.
type object []field

func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := json.Marshal(f.key)
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(f.value)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Body is the request body for one decision call: the provider's own name
// for the model, the state and the questions. It is what the size limit
// measures. The request is not checked here; Decide checks it first.
func Body(providerModel string, req seam.DecisionRequest) ([]byte, error) {
	questions := object{}
	for _, q := range req.Questions {
		wire := object{}
		switch q.Type {
		case seam.QuestionYesNo:
			wire = append(wire, field{"type", wireYesNo}, field{"instructions", q.Instructions})
			// Only the answers the caller describes; the model's own
			// defaults stand for the rest.
			criteria := object{}
			for _, o := range q.Options {
				if o.Description == "" {
					continue
				}
				key := "false"
				if o.ID == seam.OptionYes {
					key = "true"
				}
				criteria = append(criteria, field{key, o.Description})
			}
			if len(criteria) > 0 {
				wire = append(wire, field{"criteria", criteria})
			}
		default:
			wire = append(wire, field{"type", wireChoice}, field{"instructions", q.Instructions})
			criteria := object{}
			for _, o := range q.Options {
				var desc any // null: the option has no description
				if o.Description != "" {
					desc = o.Description
				}
				criteria = append(criteria, field{o.ID, desc})
			}
			wire = append(wire, field{"criteria", criteria})
		}
		questions = append(questions, field{q.ID, wire})
	}
	return json.Marshal(object{
		{"model", providerModel},
		{"state", req.State},
		{"questions", questions},
	})
}

// answer is one answer as the endpoint writes it.
type answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Noul          *float64           `json:"noul"`
}

// Read turns the endpoint's answer into the seam's result for one request:
// one answer for each question, in the request's order, with a probability
// for every option. An answer that does not answer the request is refused.
// The choice is worked out from the probabilities, so it is always the
// most probable option.
func Read(req seam.DecisionRequest, raw []byte) (seam.DecisionResult, error) {
	var r struct {
		Answers map[string]answer `json:"answers"`
		Usage   *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return seam.DecisionResult{}, errors.New("it is not the JSON of a decision answer")
	}
	if r.Answers == nil {
		if r.Error != "" {
			return seam.DecisionResult{}, errors.New("the endpoint reported an error")
		}
		return seam.DecisionResult{}, errors.New("it has no answers")
	}
	if len(r.Answers) != len(req.Questions) {
		return seam.DecisionResult{}, fmt.Errorf("it holds %d answers for %d questions", len(r.Answers), len(req.Questions))
	}
	out := seam.DecisionResult{}
	for _, q := range req.Questions {
		a, ok := r.Answers[q.ID]
		if !ok {
			return seam.DecisionResult{}, fmt.Errorf("it has no answer to %s", q.ID)
		}
		probs := map[string]float64{}
		switch q.Type {
		case seam.QuestionYesNo:
			if a.Type != wireYesNo || a.Noul == nil {
				return seam.DecisionResult{}, fmt.Errorf("the answer to %s is not a yes or no", q.ID)
			}
			yes := *a.Noul
			if math.IsNaN(yes) || yes < 0 || yes > 1 {
				return seam.DecisionResult{}, fmt.Errorf("the answer to %s is not a probability from 0 to 1", q.ID)
			}
			probs[seam.OptionYes], probs[seam.OptionNo] = yes, 1-yes
		default:
			if a.Type != wireChoice || a.Probabilities == nil {
				return seam.DecisionResult{}, fmt.Errorf("the answer to %s is not a choice", q.ID)
			}
			probs = a.Probabilities
		}
		out.Answers = append(out.Answers, seam.DecisionAnswer{
			Question: q.ID, Choice: seam.ChoiceOf(seam.OptionIDs(q), probs), Probabilities: probs,
		})
	}
	if r.Usage != nil {
		out.Usage = seam.Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens}
	}
	if err := seam.ValidateDecisionResult(req, out); err != nil {
		return seam.DecisionResult{}, err
	}
	return out, nil
}
