package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tools"
	"github.com/jonathanmkosar/keystone/settings"
)

// The past-chat tools (P5-05): search_chats, the past-chat search, and
// read_chat. search_chats was a search of titles and messages (P7-01) and
// is extended here, under the same name, rather than joined by a second
// tool (decision 0322). It now finds a chat by any of the words given, in
// its title, its recap (internal/recap: a summary, topics and decisions)
// and its messages, and gives each chat's recap with the passages that
// match best.
//
// What either tool may give depends on the call (fixed rule 3): a chat that
// was sent content from a sensitive space, or whose recap was made while it
// was, reaches a call only when every model the call may reach may read
// such content. internal/recap holds that answer, and both tools ask it for
// every chat they would give. ChatTools makes them for one answer.

// The settings of the past-chat search, in settings/chats.json.
const (
	SettingPastChatsResults  = "chats.past_chats_max_results"
	SettingPastChatsPassages = "chats.past_chats_passages"
)

// PastChats are the past-chat search's limits.
type PastChats struct {
	// MaxResults is the most chats one search returns, and the number when
	// the model asks for none: chats.past_chats_max_results.
	MaxResults int
	// Passages is the most matching passages given for each chat:
	// chats.past_chats_passages.
	Passages int
}

// Check refuses limits no search can run with, naming the setting.
func (p PastChats) Check() error {
	switch {
	case p.MaxResults < 1 || p.MaxResults > 50:
		return fmt.Errorf("builtin: %s must be from 1 to 50", SettingPastChatsResults)
	case p.Passages < 0 || p.Passages > 20:
		return fmt.Errorf("builtin: %s must be from 0 to 20", SettingPastChatsPassages)
	}
	return nil
}

// DefaultPastChats reads the past-chat search's limits from the settings
// registry's defaults.
func DefaultPastChats() (PastChats, error) {
	reg, err := settings.Registry()
	if err != nil {
		return PastChats{}, err
	}
	var p PastChats
	for key, into := range map[string]*int{SettingPastChatsResults: &p.MaxResults, SettingPastChatsPassages: &p.Passages} {
		s, ok := settings.Lookup(reg, key)
		if !ok {
			return PastChats{}, fmt.Errorf("builtin: the %s setting is missing from the registry", key)
		}
		if *into, err = strconv.Atoi(s.Default); err != nil {
			return PastChats{}, fmt.Errorf("builtin: %s: %w", key, err)
		}
	}
	return p, p.Check()
}

// Hold marks the chat a call is made in as holding content from a
// sensitive space, as recall.Hold does, before a result that gives such
// content is handed to the model. An error keeps the result from it.
type Hold func(ctx context.Context, in tools.Input) error

// errNotHeld is what the model reads when a result held sensitive content
// and the chat it would go into could not be held.
var errNotHeld = errors.New("the result came from a chat that holds content from a sensitive space, and this chat could not be marked as holding it, so it was not given")

// ChatTools returns search_chats and read_chat made for one model call.
// mayReadSensitive is the answer for the least of the models the call's
// route may reach. When it is false, every chat that holds sensitive
// content is left out, and only counted. When it is true, such a chat is
// given, and hold is called first, so the chat the call is in is held to
// models that may read it: what the model reads stays in that chat and is
// sent with every later call of it.
func ChatTools(o Options, mayReadSensitive bool, hold Hold) ([]tools.Tool, error) {
	switch {
	case o.ReadChatMessages < 1:
		return nil, errors.New("builtin: read_chat needs room for at least one message")
	case mayReadSensitive && hold == nil:
		return nil, errors.New("builtin: past-chat tools that may give sensitive content need a way to hold the chat")
	}
	if err := o.PastChats.Check(); err != nil {
		return nil, err
	}
	return []tools.Tool{searchChats(o, mayReadSensitive, hold), readChat(o, mayReadSensitive, hold)}, nil
}

// given hands a result to the model: at once, or once the chat it goes
// into is held when the result holds sensitive content.
func given(ctx context.Context, in tools.Input, text string, sensitive bool, hold Hold) (string, error) {
	if !sensitive {
		return text, nil
	}
	if hold == nil || hold(ctx, in) != nil {
		return "", errNotHeld
	}
	return text, nil
}

// leftOut is the line that says how many chats were left out for fixed
// rule 3, and never which. more says other chats were given.
func leftOut(n int, more bool) string {
	what := ""
	if more {
		what = "more "
	}
	if n == 1 {
		return "1 " + what + "chat matched and is left out: it holds content from a sensitive space, and a model this call may reach may not read it."
	}
	return fmt.Sprintf("%d %schats matched and are left out: they hold content from a sensitive space, and a model this call may reach may not read it.", n, what)
}

// found is one chat a search found, with how well each part of it matched.
// A score is the full-text rank, lower is better, and 0 is no match.
type found struct {
	id                 string
	title, recap, best float64
	titled             bool
	passages           []chat.Match
}

// score ranks a chat: a match in its title or its recap counts twice, and
// so does a match in more than one part.
func (f found) score() float64 { return 2*f.title + 2*f.recap + f.best }

func searchChats(o Options, mayRead bool, hold Hold) tools.Tool {
	return tools.Tool{
		Name:        SearchChats,
		Description: "Search the person's own earlier chats: their titles, each chat's summary, topics and decisions, and the words of their messages. Any of the words given may match, and a chat that matches more of them, or matches in its title or summary, comes first. For each chat it returns its title, id and date, its summary, topics and the decisions made in it when it has them, and the passages that match best. The chat this turn is in is left out. Use it when the person refers to something from an earlier chat, and read_chat with an id to read one in full.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"The words to look for."},"limit":{"type":"integer","minimum":1,"description":"The most chats to return. Fewer may come back."}},"required":["query"],"additionalProperties":false}`),
		ReadOnly:    true,
		Inside:      true, // the person's own chats: not outside content (P7-02)
		Run: func(ctx context.Context, in tools.Input) (string, error) {
			var args struct {
				Query string `json:"query"`
				Limit *int   `json:"limit"`
			}
			if err := json.Unmarshal(in.Args, &args); err != nil || strings.TrimSpace(args.Query) == "" || (args.Limit != nil && *args.Limit < 1) {
				return "", errors.New(`search_chats needs a "query": the words to look for, and a "limit", if one is given, of 1 or more`)
			}
			limit := o.PastChats.MaxResults
			if args.Limit != nil {
				limit = min(*args.Limit, limit)
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			titles, messages, err := chat.MatchAny(in.Store, args.Query, o.Limits.SearchResults, o.Limits.SnippetWords)
			if err != nil {
				if errors.Is(err, chat.ErrInvalid) {
					return "", err
				}
				return "", errors.New("the search could not be run")
			}
			recaps, err := recap.Search(ctx, in.Store, args.Query, o.Limits.SearchResults)
			if err != nil {
				return "", errors.New("the search could not be run")
			}

			byID := map[string]*found{}
			var all []*found
			get := func(id string) *found {
				f := byID[id]
				if f == nil {
					f = &found{id: id}
					byID[id] = f
					all = append(all, f)
				}
				return f
			}
			for _, m := range titles {
				f := get(m.ChatID)
				f.titled, f.title = true, min(f.title, m.Score)
			}
			for _, h := range recaps {
				f := get(h.ChatID)
				f.recap = min(f.recap, h.Score)
			}
			for _, m := range messages { // the best first
				f := get(m.ChatID)
				f.best = min(f.best, m.Score)
				f.passages = append(f.passages, m)
			}
			sort.SliceStable(all, func(i, j int) bool { return all[i].score() < all[j].score() })

			var entries []string
			withheld, sensitive := 0, false
			for _, f := range all {
				if len(entries) >= limit {
					break
				}
				if f.id == in.ChatID {
					continue
				}
				// Fixed rule 3. A mark that cannot be read counts as sensitive.
				sens, _ := recap.Sensitive(ctx, in.Store, f.id)
				if !recap.MayShow(sens, mayRead) {
					withheld++
					continue
				}
				c, err := chat.Get(in.Store, f.id)
				if err != nil {
					continue // deleted while the search ran
				}
				rc, has, err := recap.Get(ctx, in.Store, f.id)
				if err != nil {
					has = false
				}
				sensitive = sensitive || sens
				entries = append(entries, entry(len(entries)+1, c, f, rc, has, o.PastChats.Passages))
			}
			if len(entries) == 0 {
				if withheld > 0 {
					return fmt.Sprintf("No other chat matches %q that this call may read. %s", args.Query, leftOut(withheld, false)), nil
				}
				return fmt.Sprintf("No other chat matches %q. Any of the words may match, so other words may find more.", args.Query), nil
			}
			noun := "matches"
			if len(entries) == 1 {
				noun = "match"
			}
			text := fmt.Sprintf("%d %s for %q in the person's other chats, best first:\n\n%s", len(entries), noun, args.Query, strings.Join(entries, "\n\n"))
			if withheld > 0 {
				text += "\n\n" + leftOut(withheld, true)
			}
			return given(ctx, in, text, sensitive, hold)
		},
	}
}

// entry is one chat as the search gives it: its title, id and date, its
// recap, and its best passages.
func entry(n int, c chat.Chat, f *found, rc recap.Recap, has bool, passages int) string {
	title := c.Title
	if title == "" {
		title = "(no title)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d. %q, chat %s, last message %s.", n, title, c.ID, c.Active.Format("January 2, 2006"))
	if f.titled {
		b.WriteString(" The title matched.")
	}
	if has {
		b.WriteString("\n" + recap.Text(rc))
	}
	if len(f.passages) > 0 && passages > 0 {
		b.WriteString("\nPassages:")
		for _, m := range f.passages[:min(passages, len(f.passages))] {
			if m.Role == seam.RoleUser {
				b.WriteString("\n- The person wrote: " + m.Snippet)
			} else {
				b.WriteString("\n- The reply said: " + m.Snippet)
			}
		}
	}
	return b.String()
}

func readChat(o Options, mayRead bool, hold Hold) tools.Tool {
	return tools.Tool{
		Name:        ReadChat,
		Description: fmt.Sprintf("Read one of the person's own chats by its id, as search_chats gives it: its title, its summary and decisions when it has them, and the messages on its current branch, oldest first. A long chat comes back as its last %d messages.", o.ReadChatMessages),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"chat_id":{"type":"string","description":"The chat's id, sixteen characters."}},"required":["chat_id"],"additionalProperties":false}`),
		ReadOnly:    true,
		Inside:      true, // the person's own chats: not outside content (P7-02)
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
			// Fixed rule 3. A mark that cannot be read counts as sensitive.
			sensitive, _ := recap.Sensitive(ctx, in.Store, id)
			if !recap.MayShow(sensitive, mayRead) {
				return "", fmt.Errorf("the chat %s holds content from a sensitive space, and a model this call may reach may not read it, so nothing of it is given here", id)
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
			if rc, ok, err := recap.Get(ctx, in.Store, id); err == nil && ok {
				b.WriteString(recap.Text(rc) + "\n")
			}
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
			return given(ctx, in, strings.TrimRight(b.String(), "\n"), sensitive, hold)
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
