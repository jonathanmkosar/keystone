package testtenant

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

const (
	// markKey is the row in a tenant's tenant_meta that says the tenant is
	// the test tenant. Only Load writes it, and only into a tenant it has
	// just made.
	markKey   = "test_tenant"
	markValue = "true"
	// chatKeyPrefix starts the tenant_meta rows that remember the chats
	// the loader made: the fixture file's name follows, and the value is
	// the chat's id and the fixture's hash.
	chatKeyPrefix = "test_tenant.chat."
)

// Options are the settings the loader works under. The caller reads them,
// from the registry or from stored values, and passes them in.
type Options struct {
	SignIn signin.Options
	Chat   chat.Limits
}

// Result says what one run of Load did.
type Result struct {
	Tenant tenant.Tenant
	// Created is true when this run made the tenant.
	Created bool
	// PasswordSet is true when this run set the password, because the
	// fixture's password did not sign in.
	PasswordSet bool
	// ChatsMade and ChatsKept count the fixture's chats this run made and
	// the ones it found already there.
	ChatsMade, ChatsKept int
}

// Load puts the test tenant into an install: the person, the password and
// the chats. It is safe to run twice. A second run finds the tenant by its
// mark, leaves a password that works alone, and makes only the chats that
// are missing or whose fixtures have changed. Chats the person made by hand
// are never touched, and no other tenant is opened for writing.
func Load(in *tenant.Install, fx Fixtures, o Options) (Result, error) {
	var res Result
	t, found, err := Find(in)
	if err != nil {
		return res, err
	}
	if !found {
		if t, err = in.Create(fx.Name); err != nil {
			return res, err
		}
		res.Created = true
	}
	res.Tenant = t
	s, err := in.Store(t.ID)
	if err != nil {
		return res, err
	}
	defer s.Close()
	if res.Created {
		// The mark goes in before anything else, so a tenant the loader
		// made is never left looking like a person's.
		if err := setMeta(s, markKey, markValue); err != nil {
			return res, err
		}
	}

	if res.PasswordSet, err = ensurePassword(s, fx.Password, o.SignIn); err != nil {
		return res, err
	}

	for _, c := range fx.Chats {
		key := chatKeyPrefix + c.File
		v, err := getMeta(s, key)
		if err != nil {
			return res, err
		}
		if id, hash, ok := strings.Cut(v, " "); ok {
			_, err := chat.Get(s, id)
			switch {
			case err == nil && hash == c.Hash:
				res.ChatsKept++
				continue
			case err == nil:
				// The fixture changed: the chat is made again from it.
				if err := chat.Delete(s, id); err != nil {
					return res, err
				}
			case !errors.Is(err, chat.ErrNotFound):
				return res, err
			}
		}
		id, err := makeChat(s, c, o.Chat)
		if err != nil {
			return res, fmt.Errorf("chats/%s: %w", c.File, err)
		}
		if err := setMeta(s, key, id+" "+c.Hash); err != nil {
			return res, err
		}
		res.ChatsMade++
	}
	return res, nil
}

// Find returns the install's test tenant, if it has one. It opens each
// tenant's store to read the mark, so a server calls it once when it starts
// and not on every request.
func Find(in *tenant.Install) (tenant.Tenant, bool, error) {
	for _, t := range in.Tenants() {
		s, err := in.Store(t.ID)
		if err != nil {
			return tenant.Tenant{}, false, err
		}
		is, err := IsTest(s)
		if cerr := s.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return tenant.Tenant{}, false, err
		}
		if is {
			return t, true, nil
		}
	}
	return tenant.Tenant{}, false, nil
}

// IsTest reports whether a store is the test tenant's. The mark is in the
// tenant's own database, so it goes wherever the database goes, and a
// tenant's name says nothing either way.
func IsTest(s *tenant.Store) (bool, error) {
	v, err := getMeta(s, markKey)
	return v == markValue, err
}

// ensurePassword makes the fixture's password the tenant's, and reports
// whether it had to set it. It tries the password first, so a run that
// changes nothing ends no session.
func ensurePassword(s *tenant.Store, password string, o signin.Options) (bool, error) {
	token, _, err := signin.SignIn(s, password, o)
	switch {
	case err == nil:
		return false, signin.SignOut(s, token)
	case errors.Is(err, signin.ErrRefused), errors.Is(err, signin.ErrWait):
		return true, signin.SetPassword(s, password, o)
	}
	return false, err
}

// makeChat writes one chat: each of the person's messages, and under it the
// reply the recorded turn gave. If anything fails, the half-made chat is
// removed.
func makeChat(s *tenant.Store, c Chat, lim chat.Limits) (id string, err error) {
	made, err := chat.Create(s)
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			chat.Delete(s, made.ID)
		}
	}()
	if _, err := chat.Rename(s, lim, made.ID, c.Title); err != nil {
		return "", err
	}
	if c.Starred {
		if _, err := chat.Star(s, made.ID, true); err != nil {
			return "", err
		}
	}
	// parents[i] is the message that message i was added under.
	parents := make([]int64, len(c.Messages))
	var last int64
	for i, m := range c.Messages {
		parents[i] = last
		if m.Edits > 0 {
			parents[i] = parents[m.Edits-1]
		}
		asked, err := chat.Append(s, made.ID, chat.NewMessage{ParentID: parents[i], Role: m.Role, Blocks: m.Blocks})
		if err != nil {
			return "", err
		}
		reply, err := chat.Append(s, made.ID, chat.NewMessage{ParentID: asked.ID, Role: seam.RoleAssistant, Blocks: c.Replies[i].Blocks})
		if err != nil {
			return "", err
		}
		last = reply.ID
	}
	return made.ID, nil
}

func getMeta(s *tenant.Store, key string) (string, error) {
	var v string
	err := s.DB().QueryRow("SELECT value FROM tenant_meta WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func setMeta(s *tenant.Store, key, value string) error {
	_, err := s.DB().Exec("INSERT INTO tenant_meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// DefaultOptions reads the loader's options from the defaults in the
// settings registry, as the scripts do.
func DefaultOptions() (Options, error) {
	si, err := signin.Defaults()
	if err != nil {
		return Options{}, err
	}
	lim, err := chat.DefaultLimits()
	if err != nil {
		return Options{}, err
	}
	return Options{SignIn: si, Chat: lim}, nil
}
