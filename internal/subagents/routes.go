package subagents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/unattended"
)

// Routes returns the sub-agent routes, served by r. A chat's lanes read
// them: the sub-agents its turns started, each with where it is, and a stop
// for one. The sub-agent's steps are its chat's, read through the chat
// routes, and its approval cards its turn's, answered through the approval
// routes.
func Routes(r *Runner) []api.Route {
	const subs = api.Prefix + "chats/{id}/subagents"
	const one = subs + "/{sub}"
	return []api.Route{
		{
			Method: "GET", Path: subs, Handle: r.list,
			Summary: "The sub-agents started from this chat's turns, oldest first, each with where it is, what it has spent and its result once it has ended; or, in a sub-agent's own chat, that sub-agent.",
			Query: []api.Param{
				{Name: "call", Description: "Only those one run_subagents call started: the tool call's id."},
				{Name: "turn", Description: "Only those one turn started: the turn's id."},
			},
			Reply: SubagentsReply{},
		},
		{
			Method: "GET", Path: one, Handle: r.get,
			Summary: "One sub-agent: where it is, what it has spent, and its result once it has ended.",
			Reply:   Subagent{},
		},
		{
			Method: "POST", Path: one + "/stop", Handle: r.stop,
			Summary: "Stop one sub-agent. Its turn is stopped, what it did is kept in its chat, and the turn that started it goes on with the others; its result says it was stopped. It takes no body. Stopping one that has ended changes nothing.",
			Reply:   Subagent{},
		},
	}
}

// answer writes the error a sub-agent function gave as the API's error
// body.
func (r *Runner) answer(w http.ResponseWriter, req *http.Request, c api.Call, err error) {
	switch {
	case errors.Is(err, chat.ErrNotFound), errors.Is(err, errNoSubagent):
		api.Error(w, req, http.StatusNotFound, "not_found", "there is no such chat or sub-agent")
	default:
		r.logf("%s %s %s: %v", c.RequestID, req.Method, req.URL.Path, err)
		api.Error(w, req, http.StatusInternalServerError, "failed", "the call failed")
	}
}

func (r *Runner) list(w http.ResponseWriter, req *http.Request, c api.Call) {
	ctx := req.Context()
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	where, args := "(chat_id = ? OR work_chat_id = ?)", []any{id, id}
	if call := req.URL.Query().Get("call"); call != "" {
		where, args = where+" AND call_id = ?", append(args, call)
	}
	if t := req.URL.Query().Get("turn"); t != "" {
		where, args = where+" AND turn_id = ?", append(args, t)
	}
	got, err := list(ctx, c.Store.DB(), where, args...)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply := SubagentsReply{Subagents: []Subagent{}}
	for _, a := range got {
		r.live(ctx, c.Store, &a)
		reply.Subagents = append(reply.Subagents, a.Subagent)
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

func (r *Runner) get(w http.ResponseWriter, req *http.Request, c api.Call) {
	a, err := getInChat(req.Context(), c.Store, req.PathValue("id"), req.PathValue("sub"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	r.live(req.Context(), c.Store, &a)
	api.WriteJSON(w, http.StatusOK, a.Subagent)
}

func (r *Runner) stop(w http.ResponseWriter, req *http.Request, c api.Call) {
	a, err := r.Stop(req.Context(), c.Store, req.PathValue("id"), req.PathValue("sub"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, a)
}

// Stop stops one sub-agent and returns it as it then stands. The stop is
// written on its row first, so its call's supervisor finds it and stops its
// turn; this waits for it to end, up to chats.stop_wait. One that has ended
// is returned as it is.
func (r *Runner) Stop(ctx context.Context, s *tenant.Store, chatID, id string) (Subagent, error) {
	a, err := getInChat(ctx, s, chatID, id)
	if err != nil {
		return Subagent{}, err
	}
	if !ended(a.Status) {
		if err := change(ctx, s, a.ID, "stop_why = CASE WHEN stop_why = '' THEN ? ELSE stop_why END", byPerson); err != nil {
			return Subagent{}, err
		}
		key := s.TenantID() + "/" + a.ID
		r.wake(callKey(s.TenantID(), a.TurnID, a.CallID))
		deadline := time.Now().Add(r.o.Turns.StopWait)
		for {
			news := r.waitFor(key)
			if a, err = get(ctx, s.DB(), a.ID); err != nil || ended(a.Status) {
				break
			}
			left := time.Until(deadline)
			if left <= 0 || ctx.Err() != nil {
				break
			}
			select {
			case <-news:
			case <-time.After(min(left, r.o.Turns.StreamPoll)):
			case <-ctx.Done():
			}
		}
		if err != nil {
			return Subagent{}, err
		}
	}
	r.live(ctx, s, &a)
	return a.Subagent, nil
}

// live fills in where a sub-agent is that has not ended, from its turn: its
// steps, its spend, whether a call waits for the person, and a few words
// for what it is doing. One that has ended shows what was written when it
// did.
func (r *Runner) live(ctx context.Context, s *tenant.Store, a *row) {
	switch {
	case a.Status == StatusQueued && a.stopWhy != "":
		a.Progress = "Stopping"
	case a.Status == StatusQueued:
		a.Progress = "Waiting for a free lane"
	case a.Status == StatusRunning:
		if a.WorkTurnID == "" {
			a.Progress = "Starting"
			return
		}
		if n, err := turn.Calls(ctx, s, a.WorkChatID, a.requestID); err == nil {
			a.Steps = n
		}
		if n, err := unattended.Spent(ctx, s, a.requestID); err == nil {
			a.SpentMicros = n
		}
		pending, _ := turn.Pending(ctx, s, a.WorkTurnID)
		a.Waiting = pending != ""
		switch {
		case a.stopWhy != "":
			a.Progress = "Stopping"
		case pending != "":
			a.Progress = "Waiting for your approval to run " + pending
		default:
			a.Progress = r.doing(ctx, s, a)
		}
	case a.Status == StatusDone:
		a.Progress = "Done"
	case a.Status == StatusStopped:
		a.Progress = "Stopped"
	case a.Status == StatusPaused:
		a.Progress = "Paused: every agent was paused, so it stopped"
	default:
		a.Progress = "Did not finish"
	}
}

// doing reads a few words for what a running sub-agent is doing from its
// turn's newest events.
func (r *Runner) doing(ctx context.Context, s *tenant.Store, a *row) string {
	t, err := turn.Get(ctx, s, a.WorkChatID, a.WorkTurnID)
	if err != nil {
		return "Working"
	}
	if t.Status == turn.StatusQueued {
		return "Starting"
	}
	events, err := turn.Events(ctx, s, t.ID, max(t.LastEventID-40, 0), 40)
	if err != nil {
		return "Working"
	}
	words := "Working"
	for _, e := range events {
		switch e.Name {
		case turn.EventStart:
			words = "Working out the next step"
		case turn.EventText:
			words = "Writing"
		case turn.EventThinking:
			words = "Thinking"
		case turn.EventToolCall:
			var call turn.ToolCallEvent
			if json.Unmarshal(e.Data, &call) == nil && call.Name != "" {
				words = "Using " + call.Name
			}
		}
	}
	return words
}
