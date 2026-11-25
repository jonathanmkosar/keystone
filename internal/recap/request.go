package recap

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// Ask is what one recap request is made from.
type Ask struct {
	Options Options
	// Model is the name the request is sent with: the summary route's first
	// model.
	Model string
	// Title is the chat's title, empty when it has none.
	Title string
	// Previous is the recap so far, when it stands for the messages before
	// Messages on the chat's current branch. Nil makes a recap from the
	// start.
	Previous *Recap
	// Messages are the messages of the chat's current branch the recap is
	// to take in, oldest first: all of them, or those after Previous.
	Messages []seam.Message
	// Fits says whether a request fits the summary model's window, as the
	// budget's guard will judge it.
	Fits func(seam.Request) bool
}

// Request writes the request that asks for a recap. The one message holds
// the chat's title, the recap so far or a line saying there is none, and
// the messages, oldest first, written as plain text. When they are more
// than Options.InputMaxChars, or more than the model's window holds, the
// newest are kept, with the chat's first message when there is no recap
// so far, and a line says how many were left out between. A message too
// large on its own is cut.
func Request(a Ask) (seam.Request, error) {
	o := a.Options
	lines := render(a.Messages, o.MessageMaxChars)
	if len(lines) == 0 {
		return seam.Request{}, ErrNothing
	}
	// With no recap so far, the chat's first message says what it is about,
	// so it is kept whatever else is left out.
	var head []string
	if a.Previous == nil && len(lines) > 1 {
		head, lines = lines[:1], lines[1:]
	}
	system := o.Prompt
	for k, v := range map[string]int{"{words}": o.SummaryMaxWords, "{topics}": o.MaxTopics, "{decisions}": o.MaxDecisions} {
		system = strings.ReplaceAll(system, k, strconv.Itoa(v))
	}
	build := func(tail []string) seam.Request {
		var b strings.Builder
		if t := strings.TrimSpace(a.Title); t != "" {
			fmt.Fprintf(&b, "The chat's title: %s\n\n", t)
		} else {
			b.WriteString("The chat has no title yet.\n\n")
		}
		if p := a.Previous; p != nil {
			b.WriteString("The recap so far:\n" + Text(*p) + "\n\nThe messages that follow it, oldest first:\n\n")
		} else {
			b.WriteString("There is no recap yet. The chat's messages, oldest first:\n\n")
		}
		body := append([]string(nil), head...)
		if left := len(lines) - len(tail); left > 0 {
			body = append(body, fmt.Sprintf("[%d messages here are left out.]", left))
		}
		body = append(body, tail...)
		b.WriteString(strings.Join(body, "\n"))
		return seam.Request{
			Model:           a.Model,
			System:          []seam.Block{{Type: seam.BlockText, Text: system}},
			Messages:        []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: b.String()}}}},
			MaxOutputTokens: o.MaxOutputTokens,
		}
	}
	fits := func(tail []string) bool {
		if size(head)+size(tail) > o.InputMaxChars {
			return false
		}
		return a.Fits == nil || a.Fits(build(tail))
	}
	// The most of the newest messages that fit. Fewer fit whenever more do,
	// so the search halves.
	n := sort.Search(len(lines), func(i int) bool { return !fits(lines[len(lines)-i-1:]) })
	if n > 0 {
		return build(lines[len(lines)-n:]), nil
	}
	// The newest message alone is too large: as much of it as fits.
	runes := []rune(lines[len(lines)-1])
	cut := func(k int) []string { return []string{string(runes[:k]) + " [The rest of this message is cut.]"} }
	k := sort.Search(len(runes)+1, func(i int) bool { return !fits(cut(i)) }) - 1
	if k < 1 {
		return seam.Request{}, ErrNoRoom
	}
	return build(cut(k)), nil
}

// Text is a recap as plain text: its summary, its topics and its
// decisions, one line each, a decision to a line.
func Text(r Recap) string {
	var b strings.Builder
	b.WriteString("Summary: " + r.Summary)
	if len(r.Topics) > 0 {
		b.WriteString("\nTopics: " + strings.Join(r.Topics, "; "))
	}
	if len(r.Decisions) > 0 {
		b.WriteString("\nDecisions:")
		for _, d := range r.Decisions {
			b.WriteString("\n- " + d)
		}
	}
	return b.String()
}

// size is how many characters lines take, with the line break after each.
func size(lines []string) int {
	n := 0
	for _, l := range lines {
		n += utf8.RuneCountInString(l) + 1
	}
	return n
}

// render writes messages as plain text, one line each: who said what, each
// tool call and what it gave back. Thinking is left out, an image or a file
// is named, and each message is cut to most characters.
func render(messages []seam.Message, most int) []string {
	names := map[string]string{} // a tool call's id: the tool's name
	var lines []string
	for _, m := range messages {
		who := "The person"
		if m.Role == seam.RoleAssistant {
			who = "The assistant"
		}
		var said, did []string
		for _, b := range m.Blocks {
			switch b.Type {
			case seam.BlockText:
				if t := strings.Join(strings.Fields(b.Text), " "); t != "" {
					said = append(said, t)
				}
			case seam.BlockImage:
				said = append(said, "[an image]")
			case seam.BlockFile:
				said = append(said, strings.TrimSpace("[a file "+b.Name)+"]")
			case seam.BlockToolCall:
				names[b.ID] = b.Name
				did = append(did, fmt.Sprintf("[The assistant used the tool %s with %s.]", b.Name, string(b.Input)))
			case seam.BlockToolResult:
				name := names[b.ID]
				if name == "" {
					name = "a tool"
				}
				verb := "answered"
				if b.IsError {
					verb = "failed"
				}
				text := strings.Join(strings.Fields(seam.Text(seam.Message{Blocks: b.Content})), " ")
				did = append(did, fmt.Sprintf("[The tool %s %s: %s]", name, verb, text))
			}
		}
		line := ""
		if len(said) > 0 {
			line = who + ": " + strings.Join(said, " ")
		}
		if len(did) > 0 {
			line = strings.TrimSpace(line + " " + strings.Join(did, " "))
		}
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > most {
			line = string([]rune(line)[:most]) + " [cut]"
		}
		lines = append(lines, line)
	}
	return lines
}
