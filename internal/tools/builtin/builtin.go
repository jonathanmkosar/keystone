// Package builtin holds the tools that come with Keystone. Each one only
// reads: the current date and time, the past-chat search over the person's
// own chats, and one of their chats by its id.
//
// Add puts them in a registry. It is called once, from where the server
// is put together. Each tool is given the store of the tenant its turn
// belongs to and reaches nothing else, so one person's tool can never
// read another person's chats.
//
// The two chat tools obey fixed rule 3 (P5-05, pastchats.go): a chat that
// was sent content from a sensitive space reaches only a call whose every
// model may read such content. The registry's are made for a call that may
// not; the turn makes its own for each call's route with ChatTools.
//
// Web search, knowledge lookups and artifacts are other items' (P7-03,
// P6-05, P9-03), and each adds its own tools the same way.
package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jonathanmkosar/keystone/internal/chat"
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
	// Limits are the chat record's own: how many rows of each kind a search
	// reads and how long a snippet is.
	Limits chat.Limits
	// ReadChatMessages is the most messages read_chat returns:
	// agents.read_chat_max_messages.
	ReadChatMessages int
	// PastChats are the past-chat search's limits. Their zero value means
	// the settings registry's defaults.
	PastChats PastChats
	// Now is the clock. Nil means the machine's.
	Now func() time.Time
}

// Add registers the built-in tools. The chat tools it registers are made
// for a call that may not read sensitive content: they leave out every
// chat that was sent some.
func Add(r *tools.Registry, o Options) error {
	if o.ReadChatMessages < 1 {
		return errors.New("builtin: read_chat needs room for at least one message")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.PastChats == (PastChats{}) {
		var err error
		if o.PastChats, err = DefaultPastChats(); err != nil {
			return err
		}
	}
	chats, err := ChatTools(o, false, nil)
	if err != nil {
		return err
	}
	for _, t := range append([]tools.Tool{currentTime(o)}, chats...) {
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
