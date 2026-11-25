package approval

import (
	"errors"
	"reflect"
	"testing"
)

func TestOnlyAToolThatReadsRunsWithoutAnApproval(t *testing.T) {
	if err := Check(true, Approval{}); err != nil {
		t.Errorf("a tool that only reads was refused: %v", err)
	}
	if err := Check(false, Approval{}); !errors.Is(err, ErrNotBuilt) || !errors.Is(err, ErrNoApproval) {
		t.Errorf("a tool that changes something, with no approval, gave %v", err)
	}
	if (Approval{}).Granted() {
		t.Error("an approval nobody gave reads as granted")
	}
	// An approval a person gave, which only this package can write.
	if err := Check(false, Approval{granted: true}); err != nil {
		t.Errorf("an approved call was refused: %v", err)
	}
}

// Nothing outside the package can make an approval that is granted: the
// type's fields are not exported, and only Decide and Answered return one.
func TestAnApprovalCannotBeMadeOutsideThePackage(t *testing.T) {
	typ := reflect.TypeOf(Approval{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).IsExported() {
			t.Errorf("the field %s is exported, so any package could grant an approval", typ.Field(i).Name)
		}
	}
}

// The rule, over every policy: a tool that changes something never runs
// under its policy once the turn has read outside content, and never is
// never run.
func TestDecide(t *testing.T) {
	input := []byte(`{"to":"The lighthouse keeper"}`)
	for _, c := range []struct {
		readOnly, outside bool
		policy            Policy
		want              Verdict
		reason            Reason
	}{
		{true, false, "", VerdictRun, ""},
		{true, true, "", VerdictRun, ""},
		{true, false, Always, VerdictRun, ""},
		{true, true, Always, VerdictRun, ""},
		{true, false, Ask, VerdictAsk, ReasonPolicy},
		{true, true, Ask, VerdictAsk, ReasonPolicy},
		{true, false, Never, VerdictRefuse, ReasonPolicy},
		{true, true, Never, VerdictRefuse, ReasonPolicy},
		{false, false, "", VerdictAsk, ReasonPolicy},
		{false, false, Always, VerdictRun, ""},
		{false, false, Ask, VerdictAsk, ReasonPolicy},
		{false, false, Never, VerdictRefuse, ReasonPolicy},
		{false, false, "sometimes", VerdictAsk, ReasonPolicy},
		{false, true, "", VerdictAsk, ReasonOutside},
		{false, true, Always, VerdictAsk, ReasonOutside},
		{false, true, Ask, VerdictAsk, ReasonOutside},
		{false, true, "sometimes", VerdictAsk, ReasonOutside},
		{false, true, Never, VerdictRefuse, ReasonPolicy},
		{true, false, "sometimes", VerdictAsk, ReasonPolicy},
	} {
		d := Decide(Call{Tool: "send_postcard", Input: input, ReadOnly: c.readOnly, Policy: c.policy, Outside: c.outside})
		if d.Verdict != c.want || d.Reason != c.reason {
			t.Errorf("read-only %v, outside %v, policy %q: %s (%s), want %s (%s)", c.readOnly, c.outside, c.policy, d.Verdict, d.Reason, c.want, c.reason)
		}
		standing := d.Verdict == VerdictRun && !c.readOnly
		if d.Approval.Granted() != standing || d.Approval.Standing() != standing {
			t.Errorf("read-only %v, outside %v, policy %q: the approval is %+v", c.readOnly, c.outside, c.policy, d.Approval)
		}
		if !c.readOnly && c.outside && d.Approval.Granted() {
			t.Errorf("policy %q granted an approval after outside content", c.policy)
		}
	}
}

// An approval covers the one call it was given for.
func TestAnApprovalCoversOneCall(t *testing.T) {
	input := []byte(`{"to": "The lighthouse keeper", "message": "Thank you for the light."}`)
	for name, a := range map[string]Approval{
		"answered": Answered(Answer{Tool: "send_postcard", Input: input, Approved: true}),
		"standing": Decide(Call{Tool: "send_postcard", Input: input, Policy: Always}).Approval,
	} {
		if err := CheckCall(false, a, "send_postcard", []byte(`{"to":"The lighthouse keeper","message":"Thank you for the light."}`)); err != nil {
			t.Errorf("%s: the same call, spaced differently, was refused: %v", name, err)
		}
		if err := CheckCall(false, a, "send_postcard", []byte(`{"to":"Someone else","message":"Thank you for the light."}`)); !errors.Is(err, ErrNotThisCall) {
			t.Errorf("%s: other input gave %v", name, err)
		}
		if err := CheckCall(false, a, "send_letter", input); !errors.Is(err, ErrNotThisCall) {
			t.Errorf("%s: another tool gave %v", name, err)
		}
	}
	if a := Answered(Answer{Tool: "send_postcard", Input: input}); a.Granted() {
		t.Error("an answer that did not approve granted an approval")
	}
	if a := Answered(Answer{Tool: " ", Input: input, Approved: true}); a.Granted() {
		t.Error("an answer for no tool granted an approval")
	}
	if a := Answered(Answer{Tool: "send_postcard", Input: input, Approved: true}); a.Standing() {
		t.Error("an answered approval reads as the standing policy")
	}
	if err := CheckCall(false, Approval{}, "send_postcard", input); !errors.Is(err, ErrNoApproval) {
		t.Errorf("no approval gave %v", err)
	}
	if err := CheckCall(true, Approval{}, "read_chat", input); err != nil {
		t.Errorf("a tool that only reads was refused: %v", err)
	}
}

func TestPolicies(t *testing.T) {
	if Default(true) != Always || Default(false) != Ask {
		t.Error("the defaults are not always for a tool that reads and ask for one that changes something")
	}
	for _, p := range Policies {
		if got, err := ParsePolicy(" " + string(p) + " "); err != nil || got != p {
			t.Errorf("%s reads as %q, %v", p, got, err)
		}
	}
	if _, err := ParsePolicy("sometimes"); err == nil {
		t.Error("a policy that is not one of the three was read")
	}
}
