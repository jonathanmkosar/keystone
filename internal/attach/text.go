package attach

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// The tool a call is offered while its chat has attached chats. It is made
// for each call, for the least of the models the call's route may reach,
// by the turn (internal/turn/attach.go).
const ToolName = "open_attached_chat"

// ToolDescription is what the model is told of the tool.
const ToolDescription = "Read the messages of a chat attached to this one, or of the chat this one continues from: its current branch, oldest first, a page at a time. Each attached chat is already in your context with its id and its summary; open it when the summary does not hold what you need. With one chat attached, chat_id may be left out. Page 1 is the start of the chat, and each page says how many there are."

// ToolSchema is the tool's input.
var ToolSchema = json.RawMessage(`{"type":"object","properties":{"chat_id":{"type":"string","description":"The attached chat's id, as your context gives it."},"page":{"type":"integer","minimum":1,"description":"Which page to read, from 1, the start of the chat. 1 when left out."}},"required":[],"additionalProperties":false}`)

// Card is one attached chat as a call carries it.
type Card struct {
	ID    string
	Title string
	// Active is when its last message was written.
	Active time.Time
	// Parent says the chat a call is in continues from this one.
	Parent bool
	// Recap is the chat's recap, nil when it has none.
	Recap *recap.Recap
	// Empty says it has no messages.
	Empty bool
	// Tool says the call offers ToolName, so the model can open the chat.
	Tool bool
}

func titled(title string) string {
	if strings.TrimSpace(title) == "" {
		return "(no title)"
	}
	return title
}

// Text is one attached chat as a call carries it: a label for the turn
// inspector, and the text that is sent. The text names the chat, its id and
// the date of its last message, then its recap. It changes only when the
// chat or its recap does, so the call's start can still be cached.
func Text(c Card) (label, text string) {
	title := titled(c.Title)
	var b strings.Builder
	if c.Parent {
		label = fmt.Sprintf("continued from %q", title)
		fmt.Fprintf(&b, "This chat continues from an earlier chat of the person's, %q (chat %s, last message %s). They handed it off to go on afresh, and this is what it holds.", title, c.ID, c.Active.Format("January 2, 2006"))
	} else {
		label = fmt.Sprintf("attached chat %q", title)
		fmt.Fprintf(&b, "The person attached their chat %q (chat %s, last message %s) to this one.", title, c.ID, c.Active.Format("January 2, 2006"))
	}
	switch {
	case c.Empty:
		b.WriteString("\nIt has no messages yet.")
	case c.Recap != nil:
		b.WriteString("\n" + recap.Text(*c.Recap))
	default:
		b.WriteString("\nIt has no summary yet.")
	}
	if c.Tool && !c.Empty {
		fmt.Fprintf(&b, "\nIts messages can be read with %s, chat_id %q.", ToolName, c.ID)
	}
	return label, b.String()
}

// LeftOutLabel is the turn inspector's label for the line LeftOut writes.
const LeftOutLabel = "attached chats left out"

// LeftOut is the line a call is given in place of attached chats it may not
// be given for fixed rule 3: their number, and never which.
func LeftOut(n int) string {
	if n == 1 {
		return "1 chat attached to this one is left out: it holds content from a sensitive space, and a model this call may reach may not read it. Answering from it needs a local or trusted model."
	}
	return fmt.Sprintf("%d chats attached to this one are left out: they hold content from a sensitive space, and a model this call may reach may not read them. Answering from them needs a local or trusted model.", n)
}

// Spoken is one message as a page gives it: who wrote it and what it says.
// A tool call is named; a tool's result and a model's thinking are left
// out, as read_chat leaves them out, so a page holds the person's words and
// the replies and nothing brought in from outside (decision 0360). A
// message left with nothing is empty.
func Spoken(m seam.Message) string {
	var parts []string
	for _, b := range m.Blocks {
		switch b.Type {
		case seam.BlockText:
			if t := strings.TrimSpace(b.Text); t != "" {
				parts = append(parts, t)
			}
		case seam.BlockToolCall:
			parts = append(parts, "[used the tool "+b.Name+"]")
		case seam.BlockImage:
			parts = append(parts, "[an image]")
		case seam.BlockFile:
			parts = append(parts, "[the file "+b.Name+"]")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	who := "The person"
	if m.Role == seam.RoleAssistant {
		who = "The reply"
	}
	return who + ": " + strings.Join(parts, "\n")
}

// cut shortens a line to at most most characters, saying so.
func cut(line string, most int) string {
	const mark = " [cut short]"
	if utf8.RuneCountInString(line) <= most {
		return line
	}
	r := []rune(line)
	keep := max(most-utf8.RuneCountInString(mark), 0)
	return string(r[:keep]) + mark
}

// pages splits the lines into pages: as many lines as fit in o.PageMaxChars
// characters, at most o.PageMessages, and always at least one.
func pages(lines []string, o Options) [][]string {
	var out [][]string
	var cur []string
	size := 0
	for _, l := range lines {
		n := utf8.RuneCountInString(l) + 2 // the blank line after it
		if len(cur) > 0 && (len(cur) >= o.PageMessages || size+n > o.PageMaxChars) {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, l)
		size += n
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// Page is one page of an attached chat: messages is its current branch,
// oldest first, and page counts from 1. Each message is cut to the page's
// size. A page that is not there is ErrInput, with how many there are.
func Page(id, title string, messages []seam.Message, page int, o Options) (string, error) {
	if err := CheckOptions(o); err != nil {
		return "", err
	}
	var lines []string
	for _, m := range messages {
		if s := Spoken(m); s != "" {
			lines = append(lines, cut(s, o.PageMaxChars-2))
		}
	}
	head := fmt.Sprintf("Chat %s: %q.", id, titled(title))
	all := pages(lines, o)
	if len(all) == 0 {
		if page != 1 {
			return "", fmt.Errorf("%w: chat %s has no messages, so it has no page %d", ErrInput, id, page)
		}
		return head + " It has no messages.", nil
	}
	if page < 1 || page > len(all) {
		return "", fmt.Errorf("%w: chat %s has %d pages, from 1 to %d, and no page %d", ErrInput, id, len(all), len(all), page)
	}
	first := 1
	for _, p := range all[:page-1] {
		first += len(p)
	}
	these := all[page-1]
	var b strings.Builder
	fmt.Fprintf(&b, "%s Page %d of %d: messages %d to %d of %d, oldest first.\n\n", head, page, len(all), first, first+len(these)-1, len(lines))
	b.WriteString(strings.Join(these, "\n\n"))
	if page < len(all) {
		fmt.Fprintf(&b, "\n\nPage %d holds the messages after these.", page+1)
	}
	return b.String(), nil
}
