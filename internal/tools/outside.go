package tools

import (
	"strings"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Outside content (P7-03). A tool whose result brings text from outside
// Keystone into a turn, such as a web page or a search result, opens that
// result with OutsideMark. The mark is part of the result the tool
// returns, so it is in the stream, the chat record and the audit log, and
// the tool loop and the approval policy can tell from the record alone
// that a turn read outside content. The second fixed rule says content
// the agent read never grants approval; once a turn has read outside
// content, every tool in it that changes something asks.
//
// The mark only ever adds caution. A result that carries it by mistake
// makes side effects ask; it can never let one run. A tool that brings
// outside content and forgets the mark is the danger, which is why the
// approval policy (P7-02) also counts every tool that does not say it is
// Keystone's own as outside.

// OutsideMark opens the result of a tool call that brought outside
// content into the turn.
const OutsideMark = "[Outside content]"

// MarkOutside returns text as the result of a tool that brought outside
// content: the mark, a space, then text.
func MarkOutside(text string) string {
	return OutsideMark + " " + text
}

// IsOutside reports whether a block is the result of a tool call that
// brought outside content into the turn: a tool_result that is not an
// error and whose text opens with OutsideMark.
func IsOutside(b seam.Block) bool {
	if b.Type != seam.BlockToolResult || b.IsError {
		return false
	}
	for _, c := range b.Content {
		if c.Type == seam.BlockText {
			return strings.HasPrefix(c.Text, OutsideMark)
		}
	}
	return false
}

// ReadOutside reports whether any of the messages holds a tool result
// that brought outside content: whether a turn, or a chat, has read some.
func ReadOutside(messages []seam.Message) bool {
	for _, m := range messages {
		for _, b := range m.Blocks {
			if IsOutside(b) {
				return true
			}
		}
	}
	return false
}
