package inspector

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// This file keeps the skips in the tenant's own database, in the table
// chat_skips: what a person took out of a chat's next turn.

// The kinds of item a skip names.
const (
	// KindPage is a page of the person's knowledge store that recall
	// brought into a call. Its ref is the page's id. It is
	// knowledge.HitPage.
	KindPage = "page"
	// KindSource is a source of the knowledge store, as KindPage.
	KindSource = "source"
	// KindMessage is a pinned message of the chat. Its ref is the
	// message's id.
	KindMessage = "message"
)

// refMaxChars is the longest id a skip may name. A knowledge store's ids
// are far shorter; it keeps a row from holding anything else. It is a
// bound on the shape of an id, not a limit a person tunes.
const refMaxChars = 200

// ErrInvalid says a skip is not one that can be kept: a kind that is not
// page, source or message, or an id that is not of its shape.
var ErrInvalid = errors.New("inspector: not a skip")

// Skip is one item a person took out of a chat's next turn.
type Skip struct {
	// Kind is page, source or message.
	Kind string `json:"kind"`
	// Ref is the page's or source's id in the knowledge store, or the
	// message's id.
	Ref string `json:"ref"`
	// Label is what the inspector called the item when it was taken out.
	Label string `json:"label"`
	// Asked is when the person took it out.
	Asked time.Time `json:"asked"`
	// TurnID is the turn that left it out. It is empty while the skip
	// waits for the chat's next turn.
	TurnID string `json:"turn_id,omitempty"`
}

// Ref is the name an item goes by on the context builder's input and in
// its record, as contextbuild.Item.Ref: the kind and the id, such as
// page:7 or message:42.
func Ref(kind, id string) string { return kind + ":" + id }

// Name is the skip's Ref.
func (s Skip) Name() string { return Ref(s.Kind, s.Ref) }

// Check refuses a skip that cannot be kept, with ErrInvalid: a kind that
// is not page, source or message, a message id that is not a whole number
// above zero, and an id that is empty, too long, or holds a space or a
// control character.
func Check(kind, ref string) error {
	switch kind {
	case KindPage, KindSource:
	case KindMessage:
		if n, err := strconv.ParseInt(ref, 10, 64); err != nil || n < 1 || strconv.FormatInt(n, 10) != ref {
			return fmt.Errorf("%w: a message is named by its id, a whole number", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: %q is not page, source or message", ErrInvalid, kind)
	}
	if ref == "" || utf8.RuneCountInString(ref) > refMaxChars || !utf8.ValidString(ref) || strings.IndexFunc(ref, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("%w: %q is not the id of a %s", ErrInvalid, ref, kind)
	}
	return nil
}

// Times are stored as the chat record stores them: text of one fixed
// width, in UTC, so they sort as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

// now is the clock, so a test can set it.
var now = time.Now

// Add keeps a skip for a chat's next turn. Taking out the same item twice
// before that turn keeps one. The caller has checked that the chat is the
// person's.
func Add(ctx context.Context, s *tenant.Store, chatID string, sk Skip) error {
	if err := Check(sk.Kind, sk.Ref); err != nil {
		return err
	}
	_, err := s.DB().ExecContext(ctx, `INSERT INTO chat_skips (chat_id, kind, ref, label, asked_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		chatID, sk.Kind, sk.Ref, strings.TrimSpace(sk.Label), now().UTC().Format(timeLayout))
	return err
}

// Remove takes back a skip that waits for a chat's next turn. Taking back
// one that is not there changes nothing. A skip a turn has used stays.
func Remove(ctx context.Context, s *tenant.Store, chatID, kind, ref string) error {
	_, err := s.DB().ExecContext(ctx, "DELETE FROM chat_skips WHERE chat_id = ? AND kind = ? AND ref = ? AND turn_id = ''", chatID, kind, ref)
	return err
}

// Waiting returns the skips that wait for a chat's next turn, oldest first.
func Waiting(ctx context.Context, s *tenant.Store, chatID string) ([]Skip, error) {
	return list(ctx, s, "chat_id = ? AND turn_id = ''", chatID)
}

// Claim gives a turn every skip that waits in its chat, and returns the
// turn's skips: those, and any it claimed before. A turn calls it when its
// first model call is built. A later turn finds none of them waiting, so
// each skip is used by one turn.
func Claim(ctx context.Context, s *tenant.Store, chatID, turnID string) ([]Skip, error) {
	if turnID == "" {
		return nil, errors.New("inspector: a skip is claimed by a turn")
	}
	if _, err := s.DB().ExecContext(ctx, "UPDATE chat_skips SET turn_id = ?, taken_at = ? WHERE chat_id = ? AND turn_id = ''",
		turnID, now().UTC().Format(timeLayout), chatID); err != nil {
		return nil, err
	}
	return OfTurn(ctx, s, turnID)
}

// OfTurn returns the skips a turn claimed, oldest first. Every model call
// of the turn after the first reads them.
func OfTurn(ctx context.Context, s *tenant.Store, turnID string) ([]Skip, error) {
	if turnID == "" {
		return nil, nil
	}
	return list(ctx, s, "turn_id = ?", turnID)
}

func list(ctx context.Context, s *tenant.Store, where string, args ...any) ([]Skip, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT kind, ref, label, asked_at, turn_id FROM chat_skips WHERE "+where+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Skip{}
	for rows.Next() {
		var sk Skip
		var asked string
		if err := rows.Scan(&sk.Kind, &sk.Ref, &sk.Label, &asked, &sk.TurnID); err != nil {
			return nil, err
		}
		if sk.Asked, err = time.Parse(timeLayout, asked); err != nil {
			return nil, fmt.Errorf("inspector: a skip's time does not read: %w", err)
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// LeaveOut marks, on what the context builder is given, the recalled items
// and the pinned messages the skips name as removed. It returns copies and
// changes neither slice it was given. An item whose Ref no skip names is
// left as it is; a skip that names no item does nothing.
func LeaveOut(items []contextbuild.Item, pins []contextbuild.Pin, skips []Skip) ([]contextbuild.Item, []contextbuild.Pin) {
	if len(skips) == 0 {
		return items, pins
	}
	named := map[string]bool{}
	for _, sk := range skips {
		named[sk.Name()] = true
	}
	outItems := make([]contextbuild.Item, len(items))
	for i, it := range items {
		it.Removed = it.Removed || (it.Ref != "" && named[it.Ref] && !strings.HasPrefix(it.Ref, KindMessage+":"))
		outItems[i] = it
	}
	outPins := make([]contextbuild.Pin, len(pins))
	for i, p := range pins {
		p.Removed = p.Removed || (p.Ref != "" && named[p.Ref] && strings.HasPrefix(p.Ref, KindMessage+":"))
		outPins[i] = p
	}
	return outItems, outPins
}
