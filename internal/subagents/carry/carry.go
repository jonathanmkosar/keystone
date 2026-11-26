// Package carry is the rule for what passes between a chat and the
// sub-agents a turn of it starts (P8-02), for two of the fixed rules:
//
//   - Approval (rule 2): content an agent read never grants approval. A
//     sub-agent's chat starts marked as having read outside content when
//     its parent had, so every side effect in it asks; and a result that
//     brings outside content back marks the parent, so every later side
//     effect there asks too.
//   - Sensitive content (rule 3): a sub-agent of a held chat is held, so it
//     reaches only models that may read sensitive content; and the result
//     of a sub-agent that was sent sensitive content reaches only a parent
//     that is held too, since any other may reach a model that may not read
//     it.
//
// The package imports nothing of Keystone's, so no setting can reach it:
// verify's fixed rules check lists it as a guard of both rules.
// docs/architecture/sub-agents.md is the whole of it.
package carry

// Marks are what a chat's record says must go with words taken from it.
type Marks struct {
	// Outside says the chat has read outside content: a web page, a
	// connector's answer, a source from the knowledge store.
	Outside bool
	// Sensitive says the chat was sent content from a sensitive space, so it
	// is held to models that may read it.
	Sensitive bool
}

// Down returns the marks a sub-agent's chat starts with, from the marks of
// the chat it was started from: all of them. Its task and context are
// written by a model that has read whatever that chat read.
func Down(parent Marks) Marks { return parent }

// Up says what the chat a sub-agent was started from may be given of its
// work, from the marks of both chats when the sub-agent has ended.
//
// pass says the sub-agent's own words, its result, may reach the parent's
// turn. They may unless the sub-agent was sent sensitive content and the
// parent is not held. What Keystone itself says of the sub-agent, its
// status and its spend, is not its words and always passes.
//
// outside says the result that passes brings outside content with it, so
// it is marked as such and the parent chat must be marked as having read
// outside content before the result is given to its turn.
func Up(parent, agent Marks) (pass, outside bool) {
	pass = !agent.Sensitive || parent.Sensitive
	outside = pass && agent.Outside
	return pass, outside
}
