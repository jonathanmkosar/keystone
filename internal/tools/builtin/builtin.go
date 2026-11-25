// Package builtin holds the tools that come with Keystone. Each one only
// reads: the current date and time, a search of the person's own chats,
// and one of their chats by its id.
//
// Add puts them in a registry. It is called once, from where the server
// is put together. Each tool is given the store of the tenant its turn
// belongs to and reaches nothing else, so one person's tool can never
// read another person's chats.
//
// Web search, knowledge lookups and artifacts are other items' (P7-03,
// P6-05, P9-03), and each adds its own tools the same way.
package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

// The tools' names.
const (
	CurrentTime = "current_time"
	SearchChats = "search_chats"
	ReadChat    = "read_chat"
)

// Options are the values the tools run with. The caller reads them from
// the settings and passes them in.
type Options struct {
	// Limits are the chat record's own: how many results a search gives
	// and how long a snippet is.
	Limits chat.Limits
	// ReadChatMessages is the most messages read_chat returns:
	// agents.read_chat_max_messages.
	ReadChatMessages int
	// Now is the clock. Nil means the machine's.
	Now func() time.Time
}

// Add registers the built-in tools.
func Add(r *tools.Registry, o Options) error {
	if o.ReadChatMessages < 1 {
		return errors.New("builtin: read_chat needs room for at least one message")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	for _, t := range []tools.Tool{currentTime(o), searchChats(o), readChat(o)} {
		if err := r.Add(t); err != nil {
			return err
		}
	}
	return nil
}

func currentTime(o Options) tools.Tool {
	return tools.Tool{
		Name:        CurrentTime,
		Description: "The current date and time, where Keystone runs and in UTC. Use it whenever an answer rests on today's date, the day of the week or the time. It takes no input.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		ReadOnly:    true,
		Inside:      true, // the person's own chats, or the clock: not outside content (P7-02)
		Run: func(context.Context, tools.Input) (string, error) {
			now := o.Now()
			return fmt.Sprintf("It is %s, where Keystone runs (UTC%s).\nIn UTC it is %s.",
				now.Format("Monday, January 2, 2006, 15:04"), now.Format("-07:00"), now.UTC().Format("2006-01-02T15:04:05Z")), nil
		},
	}
}

func searchChats(o Options) tools.Tool {
	return tools.Tool{
		Name:        SearchChats,
		Description: "Search the person's own earlier chats by the words of their titles and messages. Every word given must be in the same title or the same message, so use one or two plain words. It returns each match with its chat's title and id and a few words around the match. The chat this turn is in is left out. Use read_chat with an id to read a chat that was found.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"The words to look for."}},"required":["query"],"additionalProperties":false}`),
		ReadOnly:    true,
		Inside:      true, // the person's own chats, or the clock: not outside content (P7-02)
		Run: func(ctx context.Context, in tools.Input) (string, error) {
			var args struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(in.Args, &args); err != nil || strings.TrimSpace(args.Query) == "" {
				return "", errors.New(`search_chats needs a "query": the words to look for`)
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			hits, err := chat.Search(in.Store, o.Limits, args.Query)
			if err != nil {
				if errors.Is(err, chat.ErrInvalid) {
					return "", err
				}
				return "", errors.New("the search could not be run")
			}
			var lines []string
			for _, h := range hits {
				if h.ChatID == in.ChatID {
					continue
				}
				title := h.Title
				if title == "" {
					title = "(no title)"
				}
				line := fmt.Sprintf("%d. %q, chat %s, %s. ", len(lines)+1, title, h.ChatID, h.When.Format("January 2, 2006"))
				switch {
				case h.MessageID == 0:
					line += "The title matched."
				case h.Role == seam.RoleUser:
					line += "The person wrote: " + h.Snippet
				default:
					line += "The reply said: " + h.Snippet
				}
				lines = append(lines, line)
			}
			if len(lines) == 0 {
				return fmt.Sprintf("No other chat matches %q. Every word must be in the same title or message, so fewer words may find more.", args.Query), nil
			}
			return fmt.Sprintf("%d matches for %q in the person's other chats:\n\n%s", len(lines), args.Query, strings.Join(lines, "\n")), nil
		},
	}
}

func readChat(o Options) tools.Tool {
	return tools.Tool{
		Name:        ReadChat,
		Description: fmt.Sprintf("Read one of the person's own chats by its id, as search_chats gives it: the title and the messages on its current branch, oldest first. A long chat comes back as its last %d messages.", o.ReadChatMessages),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"chat_id":{"type":"string","description":"The chat's id, sixteen characters."}},"required":["chat_id"],"additionalProperties":false}`),
		ReadOnly:    true,
		Inside:      true, // the person's own chats, or the clock: not outside content (P7-02)
		Run: func(ctx context.Context, in tools.Input) (string, error) {
			var args struct {
				ChatID string `json:"chat_id"`
			}
			if err := json.Unmarshal(in.Args, &args); err != nil || strings.TrimSpace(args.ChatID) == "" {
				return "", errors.New(`read_chat needs a "chat_id": the id of the chat to read`)
			}
			id := strings.TrimSpace(args.ChatID)
			if err := ctx.Err(); err != nil {
				return "", err
			}
			gone := fmt.Errorf("there is no chat with the id %q in this person's record. Use search_chats to find a chat's id", id)
			c, err := chat.Get(in.Store, id)
			if err != nil {
				if errors.Is(err, chat.ErrNotFound) || errors.Is(err, chat.ErrInvalid) {
					return "", gone
				}
				return "", errors.New("the chat could not be read")
			}
			path, err := chat.Path(in.Store, id)
			if err != nil {
				if errors.Is(err, chat.ErrNotFound) {
					return "", gone
				}
				return "", errors.New("the chat could not be read")
			}
			var b strings.Builder
			title := c.Title
			if title == "" {
				title = "(no title)"
			}
			fmt.Fprintf(&b, "Chat %s: %q. Started %s, last message %s.\n", c.ID, title, c.Created.Format("January 2, 2006"), c.Active.Format("January 2, 2006"))
			var said []string
			for _, m := range path {
				if text := spoken(m); text != "" {
					said = append(said, text)
				}
			}
			if left := len(said) - o.ReadChatMessages; left > 0 {
				fmt.Fprintf(&b, "The first %d messages are left out.\n", left)
				said = said[left:]
			}
			if len(said) == 0 {
				b.WriteString("\nThe chat has no messages.")
			}
			for _, text := range said {
				b.WriteString("\n" + text + "\n")
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

// spoken is one message as read_chat gives it: who wrote it and its text.
// A tool call is named, and a tool's result and a model's thinking are
// left out.
func spoken(m chat.Message) string {
	var parts []string
	for _, b := range m.Blocks {
		switch b.Type {
		case seam.BlockText:
			parts = append(parts, b.Text)
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
