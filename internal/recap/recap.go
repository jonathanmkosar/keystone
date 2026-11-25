// Package recap keeps each chat's recap: a short summary of the chat, its
// topics, and the decisions made in it. A job after a chat's turns asks the
// model routing gives the summary task for it (internal/turn/recap.go), and
// the past-chat search, the search_chats tool, finds chats by it and gives
// it to the model (internal/tools/builtin).
//
// The package writes the request and reads the answer, says when a chat is
// due a new recap, keeps one recap for each chat with a full-text index of
// its own, and searches it. It reads no setting and does not import the
// chat record: the caller reads both and passes plain values in.
//
// It also holds fixed rule 3's one answer for past chats. A chat that was
// sent content from a sensitive space, or whose recap was made while it
// was, holds sensitive content: its title, its recap and its messages reach
// a call only when every model the call may reach may read sensitive
// content (Sensitive, MayShow).
package recap

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Recap is one chat's recap, as it is kept in chat_recaps (the migration
// 20261125T171000_chat_recaps.sql).
type Recap struct {
	ChatID string `json:"chat_id"`
	// Summary says what the chat is about and where it ended.
	Summary string `json:"summary"`
	// Topics are short phrases naming what the chat covers.
	Topics []string `json:"topics"`
	// Decisions are what the person decided, chose or agreed to in it.
	Decisions []string `json:"decisions"`
	// ThroughID is the last message of the branch it was made from.
	ThroughID int64 `json:"through_id"`
	// Turns is how many of the chat's turns had ended with a whole reply
	// when it was made. A chat is due a new recap by how many more have.
	Turns int `json:"turns"`
	// Model is the registry name of the model that wrote it.
	Model string `json:"model"`
	// Sensitive says the chat had been sent content from a sensitive space
	// when it, or a recap before it, was made. It is never taken back.
	Sensitive bool `json:"sensitive"`
	// RequestID names the call it was made under in the audit log: the
	// turn's, or the route's that asked for it.
	RequestID string    `json:"request_id"`
	Made      time.Time `json:"made"`
}

// Options are the values of the settings a recap is made with. The caller
// reads them and passes them in.
type Options struct {
	// Prompt is the instruction to the model, chats.recap_prompt. {words},
	// {topics} and {decisions} become the three limits below.
	Prompt string
	// SummaryMaxWords is the most words the summary may have,
	// chats.recap_summary_max_words.
	SummaryMaxWords int
	// MaxTopics and MaxDecisions are the most of each a recap keeps,
	// chats.recap_max_topics and chats.recap_max_decisions.
	MaxTopics    int
	MaxDecisions int
	// ItemMaxChars is the longest topic or decision kept,
	// chats.recap_item_max_chars.
	ItemMaxChars int
	// MessageMaxChars is the most of one message the model is shown,
	// chats.recap_message_max_chars.
	MessageMaxChars int
	// InputMaxChars is the most of the chat the model is shown in one
	// request, chats.recap_input_max_chars.
	InputMaxChars int
	// MaxOutputTokens is the longest answer, chats.recap_max_output_tokens.
	MaxOutputTokens int
}

// The setting each option is read from, for the errors CheckOptions gives.
const (
	SettingPrompt          = "chats.recap_prompt"
	SettingSummaryMaxWords = "chats.recap_summary_max_words"
	SettingMaxTopics       = "chats.recap_max_topics"
	SettingMaxDecisions    = "chats.recap_max_decisions"
	SettingItemMaxChars    = "chats.recap_item_max_chars"
	SettingMessageMaxChars = "chats.recap_message_max_chars"
	SettingInputMaxChars   = "chats.recap_input_max_chars"
	SettingMaxOutputTokens = "chats.recap_max_output_tokens"
)

var (
	// ErrOptions says a setting holds a value no recap can be made with.
	// The rest of the error names it.
	ErrOptions = errors.New("recap: a setting is out of range")
	// ErrAnswer says the model's answer was not the recap asked for.
	ErrAnswer = errors.New("recap: the answer is not a recap")
	// ErrNoRoom says no request fits the model's window, not even with one
	// message cut short.
	ErrNoRoom = errors.New("recap: the chat does not fit the model's window")
	// ErrNothing says the chat has no message to make a recap from.
	ErrNothing = errors.New("recap: the chat has no messages")
	// ErrInvalid says a recap was refused before it was kept.
	ErrInvalid = errors.New("recap: the recap is refused")
)

// CheckOptions refuses settings no recap can be made with, naming the
// setting.
func CheckOptions(o Options) error {
	bad := func(key, want string) error { return fmt.Errorf("%w: %s must be %s", ErrOptions, key, want) }
	switch {
	case strings.TrimSpace(o.Prompt) == "":
		return bad(SettingPrompt, "an instruction, not empty")
	case o.SummaryMaxWords < 10 || o.SummaryMaxWords > 1000:
		return bad(SettingSummaryMaxWords, "from 10 to 1000")
	case o.MaxTopics < 0 || o.MaxTopics > 50:
		return bad(SettingMaxTopics, "from 0 to 50")
	case o.MaxDecisions < 0 || o.MaxDecisions > 50:
		return bad(SettingMaxDecisions, "from 0 to 50")
	case o.ItemMaxChars < 20 || o.ItemMaxChars > 2000:
		return bad(SettingItemMaxChars, "from 20 to 2000")
	case o.MessageMaxChars < 100 || o.MessageMaxChars > 100000:
		return bad(SettingMessageMaxChars, "from 100 to 100000")
	case o.InputMaxChars < o.MessageMaxChars || o.InputMaxChars > 1000000:
		return bad(SettingInputMaxChars, "no less than "+SettingMessageMaxChars+", and no more than 1000000")
	case o.MaxOutputTokens < 64 || o.MaxOutputTokens > 32000:
		return bad(SettingMaxOutputTokens, "from 64 to 32000")
	}
	return nil
}

// Due says whether a chat is due a new recap. done is how many of its
// turns have ended with a whole reply; have and ok are its recap, if it has
// one; after is chats.recap_after_turns. A chat with no recap is due once a
// turn has ended; one with a recap, once after more turns have ended since.
func Due(have Recap, ok bool, done, after int) bool {
	if after < 1 {
		after = 1
	}
	if !ok {
		return done >= 1
	}
	return done-have.Turns >= after
}
