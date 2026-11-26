// Package approval is the test for the second fixed rule: a side effect
// outside Keystone happens only under the approval policy, and content the
// agent read never grants approval.
//
// It holds the rule and nothing else: the policy a person sets for a tool
// (always, ask or never), Decide, which says what one call may do, and the
// Approval a tool that changes something needs before it runs. An Approval
// can be made only here, and only two ways: by Decide, for a tool the
// person has set to always run, in a turn that has read nothing from
// outside Keystone; and by Answered, from a person's answer to one call.
// Whatever a model wrote or a tool returned is data, and no function here
// turns data into an Approval.
//
// Whatever the policy says, a tool that changes something asks once the
// turn has read outside content, and a call that reaches other people (an
// email sent, an invitation or an update to an event's guests) always asks
// and runs only on a person's own answer (P12-02). No policy and no setting
// can change that:
// the package imports nothing of Keystone's, so it can read no setting, and
// the fixed rules check in verify fails if that ever changes. What a caller
// stores or reads (the person's policies, whether the turn read outside
// content) comes in as arguments.
package approval

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
)

// ErrNoApproval refuses a tool that changes something when the call has no
// approval that covers it. Its text is what the model reads as the tool's
// result.
var ErrNoApproval = errors.New("this tool changes something, and it was not run: a tool that changes something runs only with a person's approval")

// ErrNotBuilt is the name P7-01 gave ErrNoApproval, kept so its callers
// still read.
var ErrNotBuilt = ErrNoApproval

// ErrNotThisCall refuses a call whose approval was given for another call:
// another tool, or the same tool with other input.
var ErrNotThisCall = errors.New("this tool changes something, and it was not run: the approval it came with was given for another call")

// ErrNotAnswered refuses a call that reaches other people when its approval
// is not a person's own answer to it: a standing policy never covers one.
var ErrNotAnswered = errors.New("this call reaches other people, and it was not run: such a call runs only when the person approves it themselves")

// Approval says a person agreed to one tool call. The zero value says
// nobody did. Its fields are not exported and only this package sets them,
// so code outside it can hold only the zero value or one this package made.
// In particular nothing a tool returned, and nothing a model wrote, can
// become one.
type Approval struct {
	granted bool
	// tool and digest name the one call the approval covers.
	tool   string
	digest [32]byte
	// standing says it came from the person's policy, not from an answer.
	standing bool
}

// Granted reports whether a person agreed.
func (a Approval) Granted() bool { return a.granted }

// Standing reports whether the approval came from the person's policy for
// the tool (always), rather than from their answer to this call.
func (a Approval) Standing() bool { return a.granted && a.standing }

// Covers reports whether the approval was given for this call: this tool,
// with this input. Input is compared as JSON, so spacing does not matter.
func (a Approval) Covers(tool string, input []byte) bool {
	return a.granted && a.tool == tool && a.digest == digest(input)
}

// Check says whether a tool may run. readOnly is the tool's own flag: true
// means it only reads. A nil answer lets the tool run. Any other answer
// refuses it, and says why in words the model can be given.
func Check(readOnly bool, a Approval) error {
	if readOnly || a.granted {
		return nil
	}
	return ErrNoApproval
}

// CheckCall is Check for one call: a tool that changes something runs only
// with an approval that covers this tool and this input.
func CheckCall(readOnly bool, a Approval, tool string, input []byte) error {
	if readOnly {
		return nil
	}
	if !a.granted {
		return ErrNoApproval
	}
	if !a.Covers(tool, input) {
		return ErrNotThisCall
	}
	return nil
}

// CheckReach is the second guard of a call that reaches other people: it
// runs only with an approval a person gave by answering that call. Decide
// never makes a standing approval for one; this refuses one if it ever came.
// A call that reaches nobody is not refused here.
func CheckReach(others bool, a Approval) error {
	if !others {
		return nil
	}
	if !a.granted || a.standing {
		return ErrNotAnswered
	}
	return nil
}

// Policy is a person's standing answer for one tool.
type Policy string

const (
	// Always runs the tool without asking, unless the turn has read
	// outside content and the tool changes something.
	Always Policy = "always"
	// Ask asks the person before each call.
	Ask Policy = "ask"
	// Never refuses every call. A tool set to never is not offered.
	Never Policy = "never"
)

// Policies are the three, in the order a screen offers them.
var Policies = []Policy{Always, Ask, Never}

// Default is a tool's policy until the person sets one: always for a tool
// that only reads, and ask for one that changes something.
func Default(readOnly bool) Policy {
	if readOnly {
		return Always
	}
	return Ask
}

// ParsePolicy reads a policy from text.
func ParsePolicy(s string) (Policy, error) {
	switch p := Policy(strings.TrimSpace(s)); p {
	case Always, Ask, Never:
		return p, nil
	}
	return "", errors.New("a policy is always, ask or never")
}

// Verdict is what happens to one call.
type Verdict string

const (
	// VerdictRun runs the call now.
	VerdictRun Verdict = "run"
	// VerdictAsk runs the call only once a person approves it.
	VerdictAsk Verdict = "ask"
	// VerdictRefuse answers the call with a refusal and never runs it.
	VerdictRefuse Verdict = "refuse"
)

// Reason says why a call asks or is refused.
type Reason string

const (
	// ReasonPolicy: the person's policy for the tool says so.
	ReasonPolicy Reason = "policy"
	// ReasonOutside: the tool changes something and the turn has read
	// outside content, so it asks whatever its policy.
	ReasonOutside Reason = "outside"
	// ReasonOthers: the call reaches other people, so it asks whatever its
	// policy (P12-02).
	ReasonOthers Reason = "others"
)

// Call is what Decide is told about one call.
type Call struct {
	// Tool is the tool's name, and Input the call's arguments as JSON.
	Tool  string
	Input []byte
	// ReadOnly is the tool's own flag: true means it only reads.
	ReadOnly bool
	// Policy is the person's policy for the tool. Empty means the default.
	// A value that is not a policy asks.
	Policy Policy
	// Outside says the turn has read content from outside Keystone: a web
	// page, a fetched file, an email, a connector's answer, a source from
	// the knowledge store.
	Outside bool
	// Others says the call reaches other people: it sends them a message,
	// or invites, updates or tells the guests of an event. Such a call asks
	// whatever its policy, since what it does cannot be taken back from
	// them (P12-02).
	Others bool
}

// Decision is what Decide says of one call.
type Decision struct {
	Verdict Verdict
	// Reason is why it asks or is refused. Empty when it runs.
	Reason Reason
	// Approval is set when a tool that changes something runs under the
	// person's standing policy. It covers this call only.
	Approval Approval
}

// Decide is the rule for one call.
//
//   - never refuses, whatever else is true;
//   - a call that reaches other people asks, whatever its policy;
//   - a tool that changes something asks once the turn has read outside
//     content, whatever its policy: content the agent read never grants
//     approval;
//   - ask asks, and a policy that is not one of the three asks;
//   - always runs, and a tool that changes something gets an approval that
//     covers this call alone.
func Decide(c Call) Decision {
	p := c.Policy
	if p == "" {
		p = Default(c.ReadOnly)
	}
	switch {
	case p == Never:
		return Decision{Verdict: VerdictRefuse, Reason: ReasonPolicy}
	case c.Others:
		return Decision{Verdict: VerdictAsk, Reason: ReasonOthers}
	case !c.ReadOnly && c.Outside:
		return Decision{Verdict: VerdictAsk, Reason: ReasonOutside}
	case p != Always:
		return Decision{Verdict: VerdictAsk, Reason: ReasonPolicy}
	case c.ReadOnly:
		return Decision{Verdict: VerdictRun}
	}
	return Decision{Verdict: VerdictRun, Approval: Approval{granted: true, tool: c.Tool, digest: digest(c.Input), standing: true}}
}

// Answer is a person's answer to one call that asked: the call, and whether
// they approved it. The caller reads it from the record the approval route
// wrote when the person answered, and from nothing else.
type Answer struct {
	Tool     string
	Input    []byte
	Approved bool
}

// Answered makes the approval a person's answer gives: one that covers the
// call they answered, or none when they did not approve it.
func Answered(a Answer) Approval {
	if !a.Approved || strings.TrimSpace(a.Tool) == "" {
		return Approval{}
	}
	return Approval{granted: true, tool: a.Tool, digest: digest(a.Input)}
}

// digest names a call's input. JSON is compacted first, so the same input
// stored and read back names the same call.
func digest(input []byte) [32]byte {
	var b bytes.Buffer
	if len(bytes.TrimSpace(input)) == 0 {
		input = []byte("{}")
	}
	if json.Compact(&b, input) == nil {
		return sha256.Sum256(b.Bytes())
	}
	return sha256.Sum256(input)
}
