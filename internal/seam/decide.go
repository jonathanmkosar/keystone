package seam

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// A decision is a call of its own shape (P14-02), as an embedding is: a
// state and typed questions in, and for each question a probability for
// every option out. A decision model writes no reply, so it is not a
// Provider: an adapter whose provider decides also has the Decide method,
// and a registry entry says so with the decision capability.

// QuestionType is the kind of answer a question asks for.
type QuestionType string

const (
	// QuestionChoice asks for one of the question's options.
	QuestionChoice QuestionType = "choice"
	// QuestionYesNo asks yes or no. Its options are always OptionYes and
	// OptionNo; the question may list them to describe them.
	QuestionYesNo QuestionType = "yes_no"
)

// The two options of a yes_no question.
const (
	OptionYes = "yes"
	OptionNo  = "no"
)

// The bounds a decision request is held to on the seam. The size of a
// request and how many questions it may hold are an adapter's settings;
// these are the shape every decision API Keystone speaks accepts.
const (
	// DecisionMinOptions and DecisionMaxOptions bound a choice question.
	DecisionMinOptions = 2
	DecisionMaxOptions = 255
	// decisionProbabilitySlack is how far a set of probabilities may sum
	// from one, for the rounding an endpoint does.
	decisionProbabilitySlack = 0.01
)

// decisionID is the shape of a question's or an option's id: 1 to 100 ASCII
// letters, digits, dots, dashes and underscores, which is what Ollama's
// decision API takes for a question's name. A registry name fits it.
var decisionID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

// DecisionOption is one answer a question may get.
type DecisionOption struct {
	ID string `json:"id"`
	// Description says what the option means, for the decision model. It
	// may be empty.
	Description string `json:"description,omitempty"`
}

// DecisionQuestion is one question about the state.
type DecisionQuestion struct {
	ID           string       `json:"id"`
	Instructions string       `json:"instructions"`
	Type         QuestionType `json:"type"`
	// Options are a choice question's answers, 2 to 255 of them. A yes_no
	// question lists none, or describes yes and no.
	Options []DecisionOption `json:"options,omitempty"`
}

// DecisionRequest is one decision call. Each question is answered on its
// own: no answer is shown to a later question.
type DecisionRequest struct {
	// Model is the registry name of the decision model.
	Model string `json:"model"`
	// State is the text the questions are about.
	State     string             `json:"state"`
	Questions []DecisionQuestion `json:"questions"`
}

// DecisionAnswer is one question's answer.
type DecisionAnswer struct {
	// Question is the question's id.
	Question string `json:"question"`
	// Choice is the option with the highest probability.
	Choice string `json:"choice"`
	// Probabilities holds a probability from 0 to 1 for every option of the
	// question, and nothing else. They sum to one.
	Probabilities map[string]float64 `json:"probabilities"`
}

// DecisionResult is what a decision call gave back: one answer for each
// question of the request, in the request's order.
type DecisionResult struct {
	// Model is the registry name of the model that answered.
	Model   string           `json:"model"`
	Answers []DecisionAnswer `json:"answers"`
	// Usage counts the call's tokens, as the endpoint counts them. Zero
	// means the endpoint did not say.
	Usage Usage `json:"usage"`
}

// Decider is anything that answers decision calls: the systemone adapter
// and the fake. Like a Provider, it is safe to use from several goroutines
// at once, it never puts a key in an error, and a failed call returns a
// *Error. A call whose context is done returns the context's error.
type Decider interface {
	Decide(ctx context.Context, req DecisionRequest) (DecisionResult, error)
}

// OptionIDs returns the ids of a question's options, in order: a choice
// question's own, or yes and no.
func OptionIDs(q DecisionQuestion) []string {
	if q.Type == QuestionYesNo {
		return []string{OptionYes, OptionNo}
	}
	ids := make([]string, len(q.Options))
	for i, o := range q.Options {
		ids[i] = o.ID
	}
	return ids
}

// Answer returns the answer to the question with this id.
func (r DecisionResult) Answer(question string) (DecisionAnswer, bool) {
	for _, a := range r.Answers {
		if a.Question == question {
			return a, true
		}
	}
	return DecisionAnswer{}, false
}

// ChoiceOf returns the option with the highest probability, the earlier of
// two equal ones in ids' order, or "" when ids is empty.
func ChoiceOf(ids []string, probabilities map[string]float64) string {
	best, most := "", -1.0
	for _, id := range ids {
		if p := probabilities[id]; p > most {
			best, most = id, p
		}
	}
	return best
}

// ValidateDecisionRequest refuses a request with no model, a blank state,
// no question, a question id used twice, or a question that is not one the
// decision APIs take.
func ValidateDecisionRequest(r DecisionRequest) error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("a decision request needs the model's name")
	}
	if strings.TrimSpace(r.State) == "" {
		return fmt.Errorf("a decision request needs a state")
	}
	if len(r.Questions) == 0 {
		return fmt.Errorf("a decision request needs at least one question")
	}
	seen := map[string]bool{}
	for i, q := range r.Questions {
		if !decisionID.MatchString(q.ID) {
			return fmt.Errorf("question %d: the id %q is not 1 to 100 letters, digits, dots, dashes and underscores", i+1, short(q.ID))
		}
		if seen[q.ID] {
			return fmt.Errorf("the question id %s is used twice", q.ID)
		}
		seen[q.ID] = true
		if err := checkQuestion(q); err != nil {
			return fmt.Errorf("question %s: %w", q.ID, err)
		}
	}
	return nil
}

func checkQuestion(q DecisionQuestion) error {
	if strings.TrimSpace(q.Instructions) == "" {
		return fmt.Errorf("it has no instructions")
	}
	ids := map[string]bool{}
	for _, o := range q.Options {
		if !decisionID.MatchString(o.ID) {
			return fmt.Errorf("the option id %q is not 1 to 100 letters, digits, dots, dashes and underscores", short(o.ID))
		}
		if ids[o.ID] {
			return fmt.Errorf("the option %s is listed twice", o.ID)
		}
		ids[o.ID] = true
	}
	switch q.Type {
	case QuestionChoice:
		if n := len(q.Options); n < DecisionMinOptions || n > DecisionMaxOptions {
			return fmt.Errorf("a choice question has %d to %d options, and this one has %d", DecisionMinOptions, DecisionMaxOptions, n)
		}
	case QuestionYesNo:
		for _, o := range q.Options {
			if o.ID != OptionYes && o.ID != OptionNo {
				return fmt.Errorf("a yes_no question's options are yes and no, and it lists %s", o.ID)
			}
		}
	default:
		return fmt.Errorf("the type %q is not choice or yes_no", short(string(q.Type)))
	}
	return nil
}

// ValidateDecisionResult refuses a result that does not answer its request:
// a question with no answer or two, an answer to a question not asked, a
// probability missing for an option or given for one the question does not
// have, a probability that is not a finite number from 0 to 1, a set that
// does not sum to one, or a choice that is not the most probable option.
func ValidateDecisionResult(req DecisionRequest, res DecisionResult) error {
	if len(res.Answers) != len(req.Questions) {
		return fmt.Errorf("the decision answer has %d answers for %d questions", len(res.Answers), len(req.Questions))
	}
	for i, q := range req.Questions {
		a := res.Answers[i]
		if a.Question != q.ID {
			return fmt.Errorf("answer %d is to %q, and question %d is %s", i+1, short(a.Question), i+1, q.ID)
		}
		ids := OptionIDs(q)
		if len(a.Probabilities) != len(ids) {
			return fmt.Errorf("the answer to %s has %d probabilities for %d options", q.ID, len(a.Probabilities), len(ids))
		}
		sum := 0.0
		for _, id := range ids {
			p, ok := a.Probabilities[id]
			if !ok {
				return fmt.Errorf("the answer to %s has no probability for the option %s", q.ID, id)
			}
			if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
				return fmt.Errorf("the answer to %s gives the option %s a probability that is not from 0 to 1", q.ID, id)
			}
			sum += p
		}
		if math.Abs(sum-1) > decisionProbabilitySlack {
			return fmt.Errorf("the probabilities of the answer to %s sum to %.4f, not 1", q.ID, sum)
		}
		best, ok := a.Probabilities[a.Choice]
		if !ok {
			return fmt.Errorf("the answer to %s chose %q, which is not one of its options", q.ID, short(a.Choice))
		}
		if top := a.Probabilities[ChoiceOf(ids, a.Probabilities)]; best < top {
			return fmt.Errorf("the answer to %s chose %s, which is not its most probable option", q.ID, a.Choice)
		}
	}
	return ValidateUsage(res.Usage)
}

func EncodeDecisionRequest(r DecisionRequest) ([]byte, error) {
	return encode(r, ValidateDecisionRequest)
}

func DecodeDecisionRequest(raw []byte) (DecisionRequest, error) {
	return decode(raw, ValidateDecisionRequest)
}

// short trims text for an error message.
func short(s string) string {
	const most = 40
	if r := []rune(s); len(r) > most {
		return string(r[:most]) + "..."
	}
	return s
}
