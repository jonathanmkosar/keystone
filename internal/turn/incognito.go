package turn

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// Incognito chats that keep no record (P6-07). A chat made incognito runs
// no memory pass (P6-06, memory.go), gets no recap (recap.go) and is left
// out of the past-chat tools (internal/tools/builtin). This file makes one
// incognito from its start, and deletes it, with its messages, its turns
// and their events, when the person leaves it or once it has been idle for
// knowledge.incognito_keep, so nothing of it is kept in the chat record.
// Its model calls stay in the audit log and its usage rows, with tokens and
// cost, because every call is recorded (fixed rule 4); a usage row holds no
// words. docs/architecture/memory-screens.md is the whole of it.
//
// A background task started from an incognito chat works in an incognito
// chat (internal/jobs). A chat a task still works in, or one a task that
// has not ended was started from, is kept until the task ends, so the task
// is not cut short; once it has ended the chat goes as any other.

// IncognitoReply answers POST /chats/{id}/close.
type IncognitoReply struct {
	ChatID string `json:"chat_id"`
	// Deleted says the chat is gone, with everything of it.
	Deleted bool `json:"deleted"`
	// Waiting says a background task still works in it or for it: it is
	// deleted as soon as the task ends.
	Waiting bool `json:"waiting"`
}

// IncognitoRoutes returns the routes of incognito chats (P6-07), served by
// r.
func IncognitoRoutes(r *Runner) []api.Route {
	const chats = api.Prefix + "chats"
	return []api.Route{
		{
			Method: "POST", Path: chats + "/incognito", Handle: r.createIncognito,
			Summary: "Make an incognito chat: no memory pass reads it, it gets no recap, past-chat search does not find it, and it is deleted when the person leaves it (POST /chats/{id}/close) or once it has been idle for knowledge.incognito_keep. It takes no body.",
			Reply:   ChatReply{},
		},
		{
			Method: "POST", Path: chats + "/{id}/close", Handle: r.closeIncognito,
			Summary: "The person left an incognito chat: it is deleted, with its messages and turns and the chats of the tasks it started, as soon as no background task works in it or for it. A chat that is not incognito is refused. It takes no body.",
			Reply:   IncognitoReply{},
		},
	}
}

func (r *Runner) createIncognito(w http.ResponseWriter, req *http.Request, c api.Call) {
	made, err := chat.Create(c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	if err := memory.SetIncognito(req.Context(), c.Store, made.ID, true); err != nil {
		if derr := chat.Delete(c.Store, made.ID); derr != nil {
			r.logf("%s: removing a chat that could not be made incognito: %v", c.RequestID, derr)
		}
		r.answer(w, req, c, err)
		return
	}
	reply, err := r.whole(req.Context(), c.Store, made)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, reply)
}

func (r *Runner) closeIncognito(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	ctx := req.Context()
	on, err := memory.Leave(ctx, c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	if !on {
		api.Error(w, req, http.StatusConflict, "not_incognito", "the chat is not incognito, so leaving it deletes nothing")
		return
	}
	// The chats of the tasks it started go with it.
	works, err := taskChats(ctx, c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	for _, work := range works {
		if _, err := memory.Leave(ctx, c.Store, work); err != nil {
			r.answer(w, req, c, err)
			return
		}
		if _, err := r.dropIncognito(ctx, c.Store, work); err != nil {
			r.answer(w, req, c, err)
			return
		}
	}
	gone, err := r.dropIncognito(ctx, c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, IncognitoReply{ChatID: id, Deleted: gone, Waiting: !gone})
}

// incognitoRecap reports whether a chat is incognito, so it is given no
// recap. A flag that cannot be read counts as on, with a line in the log.
func (r *Runner) incognitoRecap(ctx context.Context, s *tenant.Store, chatID string) bool {
	on, err := memory.Incognito(ctx, s, chatID)
	if err != nil {
		r.logf("turns: reading whether chat %s of tenant %s is incognito, so it is given no recap: %v", chatID, s.TenantID(), err)
		return true
	}
	return on
}

// taskChats returns the chats the background tasks started from a chat
// work in. It reads the jobs table (internal/jobs) and changes nothing in
// it.
func taskChats(ctx context.Context, s *tenant.Store, chatID string) ([]string, error) {
	rows, err := s.DB().QueryContext(ctx, "SELECT work_chat_id FROM jobs WHERE chat_id = ? AND work_chat_id <> ?", chatID, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// taskWorking reports whether a background task that has not ended works
// in the chat or was started from it.
func taskWorking(ctx context.Context, s *tenant.Store, chatID string) (bool, error) {
	var n int
	err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE status IN ('queued', 'running') AND (chat_id = ?1 OR work_chat_id = ?1)", chatID).Scan(&n)
	return n > 0, err
}

// dropIncognito deletes an incognito chat with everything of it, unless a
// background task still works in it or for it, and reports whether it is
// gone. A turn of the person's own that is still running is stopped first,
// as deleting a chat does. A chat that is gone already is gone.
func (r *Runner) dropIncognito(ctx context.Context, s *tenant.Store, chatID string) (bool, error) {
	if on, err := memory.Incognito(ctx, s, chatID); err != nil || !on {
		return false, err
	}
	if busy, err := taskWorking(ctx, s, chatID); err != nil || busy {
		return false, err
	}
	open, err := openTurns(ctx, s, chatID)
	if err != nil {
		return false, err
	}
	for i := len(open) - 1; i >= 0; i-- {
		if _, err := r.stopTurn(ctx, s, chatID, open[i].ID); err != nil && !errors.Is(err, errNoTurn) {
			return false, err
		}
	}
	if err := chat.Delete(s, chatID); err != nil && !errors.Is(err, chat.ErrNotFound) {
		return false, err
	}
	return true, nil
}

// The bounds of the incognito settings.
const (
	incognitoKeepLeast  = 5 * time.Minute
	incognitoKeepMost   = 720 * time.Hour
	incognitoSweepLeast = 5 * time.Second
	incognitoSweepMost  = time.Hour
)

// SweepIncognito starts the sweep of incognito chats: now, and then every
// knowledge.incognito_sweep_every until the runner closes, each tenant's
// incognito chats that were left, or have been idle for longer than its
// knowledge.incognito_keep, are deleted. The server calls it once, when it
// starts.
func (r *Runner) SweepIncognito() {
	if r.o.Install == nil || r.setup() != nil {
		return
	}
	every := time.Minute
	if s, ok := settings.Lookup(r.reg, memory.SettingIncognitoSweep); ok {
		if d, err := time.ParseDuration(s.Default); err == nil && d >= incognitoSweepLeast && d <= incognitoSweepMost {
			every = d
		} else {
			r.logf("turns: the setting %s does not read as a time from %s to %s, so the sweep runs every %s", memory.SettingIncognitoSweep, incognitoSweepLeast, incognitoSweepMost, every)
		}
	}
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return
	}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			r.SweepIncognitoNow(time.Now())
			select {
			case <-r.base.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

// SweepIncognitoNow deletes, for every tenant, the incognito chats that
// were left, or whose last message is older than now less the tenant's
// knowledge.incognito_keep, unless a background task works in them or for
// them. It returns how many it deleted. A check calls it with a clock of
// its own.
func (r *Runner) SweepIncognitoNow(now time.Time) int {
	if r.o.Install == nil || r.setup() != nil {
		return 0
	}
	n := 0
	for _, t := range r.o.Install.Tenants() {
		if r.isClosing() {
			return n
		}
		s, err := r.o.Install.Store(t.ID)
		if err != nil {
			r.logf("turns: the incognito sweep: opening tenant %s: %v", t.ID, err)
			continue
		}
		n += r.sweepTenant(s, now)
		s.Close()
	}
	return n
}

func (r *Runner) sweepTenant(s *tenant.Store, now time.Time) int {
	ctx := r.base
	chats, err := memory.IncognitoChats(ctx, s)
	if err != nil || len(chats) == 0 {
		if err != nil {
			r.logf("turns: the incognito sweep of tenant %s: %v", s.TenantID(), err)
		}
		return 0
	}
	keep, err := time.ParseDuration(r.setting(ctx, s, memory.SettingIncognitoKeep))
	if err != nil || keep < incognitoKeepLeast || keep > incognitoKeepMost {
		keep = 24 * time.Hour
		r.logf("turns: tenant %s: the setting %s does not read as a time from %s to %s, so %s is used", s.TenantID(), memory.SettingIncognitoKeep, incognitoKeepLeast, incognitoKeepMost, keep)
	}
	n := 0
	for _, ic := range chats {
		c, err := chat.Get(s, ic.ID)
		if err != nil {
			continue // deleted another way
		}
		if !ic.Left {
			if now.Sub(c.Active) < keep {
				continue
			}
			// Idle, and not left: a turn of the person's that is still
			// waiting or running keeps it.
			if open, err := openTurns(ctx, s, ic.ID); err != nil || len(open) > 0 {
				continue
			}
		}
		gone, err := r.dropIncognito(ctx, s, ic.ID)
		if err != nil {
			if ctx.Err() == nil {
				r.logf("turns: the incognito sweep of tenant %s: deleting a chat: %v", s.TenantID(), err)
			}
			continue
		}
		if gone {
			n++
		}
	}
	return n
}
