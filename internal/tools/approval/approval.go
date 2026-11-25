// Package approval is the test for the second fixed rule: a side effect
// outside Keystone happens only under the approval policy, and content the
// agent read never grants approval.
//
// It is one function, Check, and one type, Approval. A tool that only
// reads may run. A tool that changes something may run only with an
// Approval that says a person agreed to this call. Nothing in Keystone can
// make such an Approval yet: the approval policy is the next item (P7-02),
// and it will add the one function that makes one, here. Until then the
// rule holds by construction, because every tool that changes something is
// refused.
//
// The package imports nothing of Keystone's, and so can read no setting.
// The fixed rules check in verify fails if that ever changes.
package approval

import "errors"

// ErrNotBuilt refuses a tool that changes something while approvals do not
// exist. Its text is what the model reads as the tool's result.
var ErrNotBuilt = errors.New("this tool changes something, and it was not run: a tool that changes something needs a person's approval, and approvals are not built yet")

// Approval says a person agreed to one tool call. The zero value says
// nobody did. Its field is not exported and no function here sets it, so
// code outside this package can hold only the zero value. In particular
// nothing a tool returned, and nothing a model wrote, can become one.
type Approval struct {
	granted bool
}

// Granted reports whether a person agreed.
func (a Approval) Granted() bool { return a.granted }

// Check says whether a tool may run. readOnly is the tool's own flag: true
// means it only reads. A nil answer lets the tool run. Any other answer
// refuses it, and says why in words the model can be given.
func Check(readOnly bool, a Approval) error {
	if readOnly || a.granted {
		return nil
	}
	return ErrNotBuilt
}
