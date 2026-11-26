package approval

import (
	"errors"
	"testing"
)

// A call that reaches other people (P12-02) asks whatever its policy, with
// or without outside content; never still refuses it; and no approval is
// made for it under a standing policy.
func TestACallThatReachesOthersAlwaysAsks(t *testing.T) {
	for _, outside := range []bool{false, true} {
		for _, readOnly := range []bool{false, true} {
			for _, p := range []Policy{Always, Ask, "", "sometimes"} {
				d := Decide(Call{Tool: "send_mail", Input: []byte(`{"to":["someone@example.com"]}`), ReadOnly: readOnly, Policy: p, Outside: outside, Others: true})
				if d.Verdict != VerdictAsk || d.Reason != ReasonOthers || d.Approval.Granted() {
					t.Errorf("policy %q, outside %v, read-only %v: %+v", p, outside, readOnly, d)
				}
			}
			if d := Decide(Call{Tool: "send_mail", Policy: Never, Outside: outside, ReadOnly: readOnly, Others: true}); d.Verdict != VerdictRefuse {
				t.Errorf("never, outside %v: %+v", outside, d)
			}
		}
	}
	// A call of the same tool that reaches nobody follows its policy.
	if d := Decide(Call{Tool: "add_event", Input: []byte(`{}`), Policy: Always}); d.Verdict != VerdictRun || !d.Approval.Standing() {
		t.Errorf("a personal change set to always gave %+v", d)
	}
}

// The second guard: only a person's own answer lets a call that reaches
// others run.
func TestOnlyAnAnswerCoversACallThatReachesOthers(t *testing.T) {
	input := []byte(`{"to":["someone@example.com"]}`)
	standing := Decide(Call{Tool: "send_mail", Input: input, Policy: Always}).Approval
	answered := Answered(Answer{Tool: "send_mail", Input: input, Approved: true})
	if err := CheckReach(true, Approval{}); !errors.Is(err, ErrNotAnswered) {
		t.Errorf("no approval gave %v", err)
	}
	if err := CheckReach(true, standing); !errors.Is(err, ErrNotAnswered) {
		t.Errorf("a standing approval gave %v", err)
	}
	if err := CheckReach(true, answered); err != nil {
		t.Errorf("an answer was refused: %v", err)
	}
	if err := CheckReach(false, standing); err != nil {
		t.Errorf("a call that reaches nobody was refused: %v", err)
	}
}
