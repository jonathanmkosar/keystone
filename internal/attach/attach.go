// Package attach keeps which chats are attached to which (P5-06). A chat
// can attach other chats of the same person: each model call of it then
// carries every attached chat as its recap, and the model can open the rest
// of one, a page at a time. A handoff opens a fresh chat that carries the
// chat it was handed off from, its parent, in the same way, and says so.
//
// The package keeps the links in the tenant's own database, says what each
// attached chat is in a call and what a page of one holds, and gives fixed
// rules 1 and 3 their answers for attached chats:
//
//   - Rule 1. A link joins two chats of one tenant's store, and both ends
//     refer to that store's own chats table, so a chat of another tenant
//     cannot be named. There is no function that takes two stores.
//   - Rule 3. A chat that was sent content from a sensitive space, or whose
//     recap was made while it was, reaches a call only when every model the
//     call may reach may read such content (MayShow, with internal/recap's
//     Sensitive and MayShow, as the past-chat search asks).
//
// An incognito chat is outside all of it: it cannot be attached, attaches
// nothing and cannot be handed off, and one that becomes incognito later is
// left out of every call (decision 0361).
//
// It reads no setting and does not import the chat record: the caller reads
// both and passes plain values in, as internal/recap does.
package attach

import (
	"errors"
	"fmt"
	"time"
)

// Link is one chat attached to another.
type Link struct {
	// ChatID is the chat that carries the other.
	ChatID string `json:"chat_id"`
	// AttachedID is the chat it carries.
	AttachedID string `json:"attached_id"`
	// Handoff says ChatID was handed off from AttachedID, its parent: it
	// continues from it, and the link is not taken off by hand.
	Handoff bool      `json:"handoff"`
	Added   time.Time `json:"added"`
}

// Options are the values of the settings this package is given. The caller
// reads them and passes them in.
type Options struct {
	// Max is the most chats one chat may have attached, the one it
	// continues from included: chats.attach_max.
	Max int
	// PageMessages is the most messages one page of an attached chat holds:
	// chats.attached_page_messages.
	PageMessages int
	// PageMaxChars is the most characters one page holds:
	// chats.attached_page_max_chars. A message longer than that is cut.
	PageMaxChars int
}

// The setting each option is read from, for the errors CheckOptions gives.
const (
	SettingMax          = "chats.attach_max"
	SettingPageMessages = "chats.attached_page_messages"
	SettingPageMaxChars = "chats.attached_page_max_chars"
)

var (
	// ErrOptions says a setting holds a value nothing can be attached or
	// read with. The rest of the error names it.
	ErrOptions = errors.New("attach: a setting is out of range")
	// ErrNotFound says one of the two chats is not in this tenant's record.
	ErrNotFound = errors.New("attach: no such chat")
	// ErrSelf says a chat was to be attached to itself.
	ErrSelf = errors.New("attach: a chat cannot be attached to itself")
	// ErrFull says the chat has as many attached chats as it may.
	ErrFull = errors.New("attach: the chat has as many attached chats as it may")
	// ErrIncognito says one of the chats is incognito: an incognito chat is
	// not attached, attaches nothing and is not handed off.
	ErrIncognito = errors.New("attach: an incognito chat is not attached to anything, attaches nothing and is not handed off")
	// ErrParent says the link to the chat a chat continues from was to be
	// taken off. It stays.
	ErrParent = errors.New("attach: the chat this one continues from stays attached")
	// ErrHandedOff says the chat already continues from a chat.
	ErrHandedOff = errors.New("attach: the chat already continues from another")
	// ErrInput says what the model asked a page for cannot be read: a page
	// that is not there. It is the model's own mistake, which it can mend.
	ErrInput = errors.New("attach: the page asked for is not there")
)

// CheckOptions refuses settings nothing can be attached or read with,
// naming the setting.
func CheckOptions(o Options) error {
	bad := func(key, want string) error { return fmt.Errorf("%w: %s must be %s", ErrOptions, key, want) }
	switch {
	case o.Max < 1 || o.Max > 50:
		return bad(SettingMax, "from 1 to 50")
	case o.PageMessages < 1 || o.PageMessages > 500:
		return bad(SettingPageMessages, "from 1 to 500")
	case o.PageMaxChars < 500 || o.PageMaxChars > 200000:
		return bad(SettingPageMaxChars, "from 500 to 200000")
	}
	return nil
}
