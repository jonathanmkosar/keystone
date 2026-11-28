// Package share is the rules of a share link (P10-04): a read-only link to
// one chat that someone who has not signed in may open.
//
// A link is a row in the chat's own tenant database. Its token names that
// tenant, as a session token does (decision 0007), and the database keeps
// only the token's SHA-256. Find reads the tenant from the token, opens that
// one store and looks for the hash there, so a token finds one tenant's one
// link or nothing. Usable refuses a link that was revoked or has expired,
// and one whose chat is held for content from a sensitive space or is
// incognito. Read builds what the link shows from the chat record, and
// nothing else of the person's.
//
// This package enforces fixed rules 1 and 3 for the only door to a
// tenant's data that is not a sign-in. It reads no setting: what may change
// (a link's lifetime, how many links, whether images show) is passed in by
// the caller, and no value can make a link reach another tenant or show a
// held chat. The "fixed rules" check in verify fails if it ever imports one.
package share

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/knowledge/recall"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

var (
	// ErrNotFound is a token that finds no link: not the shape of one, for
	// a tenant that is not on this install, or whose hash is not in that
	// tenant's database. Also a chat or a link that is not in the record.
	ErrNotFound = errors.New("share: no such link")
	// ErrRevoked is a link its person turned off.
	ErrRevoked = errors.New("share: the link was turned off")
	// ErrExpired is a link whose time ran out.
	ErrExpired = errors.New("share: the link has expired")
	// ErrHeld is a chat that was sent content from a sensitive space. It is
	// never shared, and a link made before it was held is refused.
	ErrHeld = errors.New("share: the chat holds content from a sensitive space")
	// ErrIncognito is a chat that keeps no record. It is never shared.
	ErrIncognito = errors.New("share: the chat is incognito")
	// ErrEmpty is a chat with no messages yet: there is nothing to share.
	ErrEmpty = errors.New("share: the chat has no messages yet")
	// ErrTooMany is a person who already has as many open links as the
	// caller allows.
	ErrTooMany = errors.New("share: too many open links")
	// ErrInvalid wraps a draft that is not one this package takes.
	ErrInvalid = errors.New("share: invalid")
)

// What a link shows.
const (
	// ViewLive shows the chat's current branch as it is when the link is
	// opened.
	ViewLive = "live"
	// ViewSnapshot shows the branch as it was when the link was made: the
	// messages up to the last one there was then.
	ViewSnapshot = "snapshot"
)

// A link's state, in Link.State.
const (
	StateOpen    = "open"
	StateRevoked = "revoked"
	StateExpired = "expired"
	// StateRefused is a link whose chat is now held or incognito.
	StateRefused = "refused"
)

const (
	tokenPrefix = "ksh_"
	secretBytes = 32
	timeLayout  = "2006-01-02T15:04:05.000Z"
)

// tokenShape is a share token: ksh_, the tenant's id, an underscore, and 32
// random bytes as hex. audit.Scrub removes anything of this shape.
var tokenShape = regexp.MustCompile(`^ksh_([a-f0-9]{16})_[a-f0-9]{64}$`)

// now is the clock. Tests replace it.
var now = time.Now

func stamp(t time.Time) string { return t.UTC().Format(timeLayout) }

// TenantOf returns the tenant a token names, and whether it has the shape
// of a share token at all. The id is a claim and proves nothing: a token is
// a link only when its hash is found in that tenant's database.
func TenantOf(token string) (string, bool) {
	m := tokenShape.FindStringSubmatch(token)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Link is one share link, as its person sees it. It never holds the token.
type Link struct {
	// ID names the link in lists and in the audit log. It opens nothing.
	ID        string `json:"id"`
	ChatID    string `json:"chat_id"`
	ChatTitle string `json:"chat_title"`
	// View is live or snapshot.
	View    string     `json:"view"`
	Created time.Time  `json:"created"`
	Expires *time.Time `json:"expires,omitempty"`
	Revoked *time.Time `json:"revoked,omitempty"`
	// Views counts the times the link was opened and shown.
	Views      int        `json:"views"`
	LastViewed *time.Time `json:"last_viewed,omitempty"`
	// State is open, revoked, expired or refused, and Reason says why a
	// link that is not open is not, for a person.
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`

	// upto is the last message a snapshot shows.
	upto int64
}

// Draft is what a new link is made with. The caller reads it from the
// settings.
type Draft struct {
	// View is live or snapshot. Empty means live.
	View string
	// Lifetime is how long the link opens. Zero means until it is revoked.
	Lifetime time.Duration
	// MaxOpen is the most links the person may have open at once, counting
	// this one. Zero means no new link may be made.
	MaxOpen int
}

// Create makes a link to one chat of this tenant and returns its token,
// which is not kept and cannot be had again. It refuses a chat that is not
// in the record, an incognito chat, a chat held for content from a
// sensitive space, a chat with no messages, and a person who already has
// MaxOpen open links.
func Create(ctx context.Context, s *tenant.Store, chatID string, d Draft) (string, Link, error) {
	view := d.View
	if view == "" {
		view = ViewLive
	}
	switch {
	case view != ViewLive && view != ViewSnapshot:
		return "", Link{}, fmt.Errorf("%w: a link shows the chat %s or %s, not %q", ErrInvalid, ViewLive, ViewSnapshot, d.View)
	case d.Lifetime < 0:
		return "", Link{}, fmt.Errorf("%w: a link's lifetime cannot be negative", ErrInvalid)
	}
	if err := shareable(ctx, s, chatID); err != nil {
		return "", Link{}, err
	}
	leaf, err := leafOf(ctx, s, chatID)
	if err != nil {
		return "", Link{}, err
	}
	if leaf == 0 {
		return "", Link{}, ErrEmpty
	}
	t := now().UTC().Truncate(time.Millisecond)
	open, err := openCount(ctx, s, t)
	if err != nil {
		return "", Link{}, err
	}
	if open >= d.MaxOpen {
		return "", Link{}, fmt.Errorf("%w: you have %d open, and the most is %d", ErrTooMany, open, max(d.MaxOpen, 0))
	}
	var raw [secretBytes + 8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", Link{}, err
	}
	token := tokenPrefix + s.TenantID() + "_" + hex.EncodeToString(raw[:secretBytes])
	id := hex.EncodeToString(raw[secretBytes:])
	expires := ""
	if d.Lifetime > 0 {
		expires = stamp(t.Add(d.Lifetime))
	}
	var upto any
	if view == ViewSnapshot {
		upto = leaf
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO share_links(id, chat_id, token_hash, view, upto, created_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, id, chatID, hashToken(token), view, upto, stamp(t), expires); err != nil {
		return "", Link{}, err
	}
	link, err := Get(ctx, s, id)
	return token, link, err
}

// shareable refuses a chat that is not in the record, is incognito, or is
// held for content from a sensitive space. A flag that cannot be read
// refuses too.
func shareable(ctx context.Context, s *tenant.Store, chatID string) error {
	var one int
	err := s.DB().QueryRowContext(ctx, "SELECT 1 FROM chats WHERE id = ?", chatID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("chat %q: %w", chatID, ErrNotFound)
	}
	if err != nil {
		return err
	}
	return refusal(ctx, s, chatID)
}

// refusal is why a chat may not be shown through a link now, or nil.
func refusal(ctx context.Context, s *tenant.Store, chatID string) error {
	incognito, err := memory.Incognito(ctx, s, chatID)
	if err != nil {
		return fmt.Errorf("share: reading whether chat %s is incognito: %w", chatID, err)
	}
	if incognito {
		return ErrIncognito
	}
	held, err := recall.Held(ctx, s, chatID)
	if err != nil {
		return fmt.Errorf("share: reading whether chat %s is held: %w", chatID, err)
	}
	if held {
		return ErrHeld
	}
	return nil
}

// leafOf is the last message of the chat's current branch, or 0 for a chat
// with no messages.
func leafOf(ctx context.Context, s *tenant.Store, chatID string) (int64, error) {
	rows, err := s.DB().QueryContext(ctx, currentBranch+" SELECT id FROM branch ORDER BY n DESC LIMIT 1", chatID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var leaf int64
	if rows.Next() {
		if err := rows.Scan(&leaf); err != nil {
			return 0, err
		}
	}
	return leaf, rows.Err()
}

// openCount is how many of the tenant's links are open at t.
func openCount(ctx context.Context, s *tenant.Store, t time.Time) (int, error) {
	var n int
	err := s.DB().QueryRowContext(ctx, `SELECT count(*) FROM share_links
		WHERE revoked_at = '' AND (expires_at = '' OR expires_at > ?)`, stamp(t)).Scan(&n)
	return n, err
}

const linkColumns = `l.id, l.chat_id, c.title, l.view, l.upto, l.created_at, l.expires_at, l.revoked_at, l.views, l.viewed_at`

func scanLink(sc interface{ Scan(...any) error }) (Link, error) {
	var (
		l                                 Link
		upto                              sql.NullInt64
		created, expires, revoked, viewed string
	)
	if err := sc.Scan(&l.ID, &l.ChatID, &l.ChatTitle, &l.View, &upto, &created, &expires, &revoked, &l.Views, &viewed); err != nil {
		return Link{}, err
	}
	l.upto = upto.Int64
	var err error
	if l.Created, err = time.Parse(timeLayout, created); err != nil {
		return Link{}, fmt.Errorf("share: link %s: %w", l.ID, err)
	}
	for _, f := range []struct {
		text string
		into **time.Time
	}{{expires, &l.Expires}, {revoked, &l.Revoked}, {viewed, &l.LastViewed}} {
		if f.text == "" {
			continue
		}
		t, err := time.Parse(timeLayout, f.text)
		if err != nil {
			return Link{}, fmt.Errorf("share: link %s: %w", l.ID, err)
		}
		*f.into = &t
	}
	return l, nil
}

// state fills in a link's state and reason, as of now. checked caches what
// each chat's refusal was, for a list of many links.
func state(ctx context.Context, s *tenant.Store, l *Link, checked map[string]error) error {
	switch {
	case l.Revoked != nil:
		l.State, l.Reason = StateRevoked, "You turned this link off."
		return nil
	case l.Expires != nil && !now().Before(*l.Expires):
		l.State, l.Reason = StateExpired, "This link's time ran out."
		return nil
	}
	why, ok := checked[l.ChatID]
	if !ok {
		why = refusal(ctx, s, l.ChatID)
		if why != nil && !errors.Is(why, ErrHeld) && !errors.Is(why, ErrIncognito) {
			return why
		}
		if checked != nil {
			checked[l.ChatID] = why
		}
	}
	switch {
	case errors.Is(why, ErrHeld):
		l.State, l.Reason = StateRefused, "The chat now holds content from a sensitive space, so the link shows nothing."
	case errors.Is(why, ErrIncognito):
		l.State, l.Reason = StateRefused, "The chat is incognito, so the link shows nothing."
	default:
		l.State, l.Reason = StateOpen, ""
	}
	return nil
}

// Get returns one link of this tenant.
func Get(ctx context.Context, s *tenant.Store, id string) (Link, error) {
	l, err := scanLink(s.DB().QueryRowContext(ctx, "SELECT "+linkColumns+" FROM share_links l JOIN chats c ON c.id = l.chat_id WHERE l.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, fmt.Errorf("link %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return Link{}, err
	}
	return l, state(ctx, s, &l, nil)
}

// List returns this tenant's links, the newest first: every link when
// chatID is empty, or one chat's. A chat that is not in the record is
// ErrNotFound.
func List(ctx context.Context, s *tenant.Store, chatID string) ([]Link, error) {
	q := "SELECT " + linkColumns + " FROM share_links l JOIN chats c ON c.id = l.chat_id"
	var args []any
	if chatID != "" {
		var one int
		err := s.DB().QueryRowContext(ctx, "SELECT 1 FROM chats WHERE id = ?", chatID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("chat %q: %w", chatID, ErrNotFound)
		}
		if err != nil {
			return nil, err
		}
		q += " WHERE l.chat_id = ?"
		args = append(args, chatID)
	}
	rows, err := s.DB().QueryContext(ctx, q+" ORDER BY l.created_at DESC, l.id", args...)
	if err != nil {
		return nil, err
	}
	var out []Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	checked := map[string]error{}
	for i := range out {
		if err := state(ctx, s, &out[i], checked); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Revoke turns a link off for good. The row is kept, so the link answers
// that it was turned off, and its person still sees it in the list.
// Revoking a link twice changes nothing.
func Revoke(ctx context.Context, s *tenant.Store, id string) (Link, error) {
	res, err := s.DB().ExecContext(ctx, "UPDATE share_links SET revoked_at = ? WHERE id = ? AND revoked_at = ''", stamp(now()), id)
	if err != nil {
		return Link{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Not there, or revoked already: Get says which.
		return Get(ctx, s, id)
	}
	return Get(ctx, s, id)
}

// Find turns a presented token into the one tenant's store and the link it
// names. It reads the tenant from the token, opens that store, and looks
// there for the SHA-256 of the whole token. No other store is opened, and
// nothing else in a request is read to choose one. A token that finds no
// link is ErrNotFound, and no store is left open. The caller closes the
// store it is given.
//
// A link that is found may still be one that shows nothing: Usable says.
func Find(in *tenant.Install, token string) (*tenant.Store, Link, error) {
	id, ok := TenantOf(token)
	if !ok {
		return nil, Link{}, ErrNotFound
	}
	known := false
	for _, t := range in.Tenants() {
		known = known || t.ID == id
	}
	if !known {
		return nil, Link{}, ErrNotFound
	}
	s, err := in.Store(id)
	if err != nil {
		return nil, Link{}, err
	}
	if s.TenantID() != id {
		// Not reachable: the install opens the store it was asked for.
		s.Close()
		return nil, Link{}, ErrNotFound
	}
	ctx := context.Background()
	var linkID string
	err = s.DB().QueryRowContext(ctx, "SELECT id FROM share_links WHERE token_hash = ?", hashToken(token)).Scan(&linkID)
	if errors.Is(err, sql.ErrNoRows) {
		s.Close()
		return nil, Link{}, ErrNotFound
	}
	if err != nil {
		s.Close()
		return nil, Link{}, err
	}
	link, err := Get(ctx, s, linkID)
	if err != nil {
		s.Close()
		return nil, Link{}, err
	}
	return s, link, nil
}

// Usable says whether a link shows its chat now: ErrRevoked, ErrExpired,
// ErrHeld or ErrIncognito when it does not, and nil when it does. The chat
// is read again, so a chat held since the link was made is refused.
func Usable(ctx context.Context, s *tenant.Store, l Link) error {
	// P15-02: a person the admin disabled has every link answer as revoked.
	if _, off, err := s.Disabled(ctx); err != nil {
		return err
	} else if off {
		return ErrRevoked
	}
	switch {
	case l.Revoked != nil:
		return ErrRevoked
	case l.Expires != nil && !now().Before(*l.Expires):
		return ErrExpired
	}
	return refusal(ctx, s, l.ChatID)
}

// Seen counts one showing of a link.
func Seen(ctx context.Context, s *tenant.Store, id string) error {
	_, err := s.DB().ExecContext(ctx, "UPDATE share_links SET views = views + 1, viewed_at = ? WHERE id = ?", stamp(now()), id)
	return err
}

// Reason is a refusal's words for someone who opened a link. They say why
// the link shows nothing, and nothing of the chat.
func Reason(err error) string {
	switch {
	case errors.Is(err, ErrRevoked):
		return "Its owner turned this link off."
	case errors.Is(err, ErrExpired):
		return "This link has expired."
	case errors.Is(err, ErrHeld):
		return "This chat now holds content from a sensitive space, and such a chat is never shown through a link."
	case errors.Is(err, ErrIncognito):
		return "This chat is incognito, and an incognito chat is never shown through a link."
	}
	return "This link opens nothing. It may have been typed wrong, or its chat deleted."
}
