package compact

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/seam"
)

// charsPerWord is about how many characters an English word takes with the
// space after it. It turns the summary's room, which is in Keystone's
// estimated tokens, into the most words the prompt asks for.
const charsPerWord = 6

// Ask is what one summary request is made from.
type Ask struct {
	Options Options
	// Model is the name the request is sent with: the summary route's
	// first model.
	Model string
	// Previous is the summary so far. Empty for a chat's first summary.
	Previous string
	// Messages is the branch as the call was built (contextbuild.Input).
	Messages []seam.Message
	// Turns are the call's earlier turns, each as the indexes [first, end)
	// of its messages, oldest first.
	Turns [][2]int
	// Span is the turns to take in.
	Span Span
	// Room is the most the summary may take in a call, in estimated tokens
	// with no margin: the summary's share less what the heading before it
	// takes.
	Room int
	// CharsPerToken is the budget's chars_per_token, which turns Room into
	// words.
	CharsPerToken int
	// Fits says whether a request fits the summary model's window, as the
	// budget's guard will judge it.
	Fits func(seam.Request) bool
}

// ErrNoRoom says no summary can be asked for: the call has no room for
// one, or the summary so far and the instruction leave the summary model
// no room for a turn.
var ErrNoRoom = errors.New("compact: there is no room for a summary")

// ErrEmpty says the model's answer held no summary.
var ErrEmpty = errors.New("compact: the answer holds no summary")

// Request writes the request that asks for the summary, and returns the
// turn it ends before: Span.To, or an earlier turn when the summary
// model's window does not hold them all. It always takes in one turn at
// least: a turn too large for the window on its own is cut, and says so.
func Request(a Ask) (seam.Request, int, error) {
	o := a.Options
	if a.Span.From < 0 || a.Span.To > len(a.Turns) || a.Span.To <= a.Span.From {
		return seam.Request{}, 0, fmt.Errorf("compact: turns %d to %d are not among the %d earlier turns", a.Span.From, a.Span.To, len(a.Turns))
	}
	most := min(o.SummaryMaxTokens, a.Room)
	words := most * max(a.CharsPerToken, 1) / charsPerWord
	if most < 1 || words < 1 {
		return seam.Request{}, 0, ErrNoRoom
	}
	texts := make([]string, 0, a.Span.To-a.Span.From)
	for _, t := range a.Turns[a.Span.From:a.Span.To] {
		texts = append(texts, renderTurn(a.Messages[t[0]:t[1]], o.MessageMaxChars))
	}
	build := func(turns []string) seam.Request {
		var body strings.Builder
		if a.Previous != "" {
			body.WriteString("The summary so far:\n" + a.Previous + "\n\nThe turns that follow it, oldest first:\n\n")
		} else {
			body.WriteString("There is no summary yet. The turns to summarise, oldest first:\n\n")
		}
		body.WriteString(strings.Join(turns, "\n\n"))
		return seam.Request{
			Model:           a.Model,
			System:          []seam.Block{{Type: seam.BlockText, Text: strings.ReplaceAll(o.Prompt, "{words}", strconv.Itoa(words))}},
			Messages:        []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: body.String()}}}},
			MaxOutputTokens: most,
		}
	}
	// The most turns that fit. Fits is true for fewer turns whenever it is
	// for more, so the search halves.
	n := sort.Search(len(texts), func(i int) bool { return !a.Fits(build(texts[:i+1])) })
	if n > 0 {
		return build(texts[:n]), a.Span.From + n, nil
	}
	// The first turn alone is too large: as much of it as fits.
	runes := []rune(texts[0])
	cut := func(k int) []string { return []string{string(runes[:k]) + " [The rest of this turn is cut.]"} }
	k := sort.Search(len(runes)+1, func(i int) bool { return !a.Fits(build(cut(i))) }) - 1
	if k < 0 {
		return seam.Request{}, 0, ErrNoRoom
	}
	return build(cut(k)), a.Span.From + 1, nil
}

// Answer reads the summary from the model's reply. A reply cut short by
// its limit is kept as far as it went.
func Answer(resp seam.Response) (string, error) {
	text := strings.TrimSpace(seam.Text(resp.Message))
	if text == "" {
		return "", ErrEmpty
	}
	return text, nil
}

// renderTurn writes one turn as plain text for the summary model: who said
// what, each tool call and what it gave back. Thinking is left out. Each
// message is cut to most characters.
func renderTurn(messages []seam.Message, most int) string {
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
				if t := strings.TrimSpace(b.Text); t != "" {
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
				did = append(did, fmt.Sprintf("[The tool %s %s: %s]", name, verb, strings.TrimSpace(seam.Text(seam.Message{Blocks: b.Content}))))
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
	return strings.Join(lines, "\n")
}
