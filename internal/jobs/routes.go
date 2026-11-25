package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Routes returns the background task routes, served by r.
func Routes(r *Runner) []api.Route {
	const tasks = api.Prefix + "chats/{id}/tasks"
	const one = tasks + "/{task}"
	return []api.Route{
		{
			Method: "GET", Path: tasks, Handle: r.list,
			Summary: "The background tasks started from this chat, or working in it, oldest first.",
			Reply:   TasksReply{},
		},
		{
			Method: "POST", Path: tasks, Handle: r.start,
			Summary: "Start a background task from this chat. It runs in a chat of its own, goes on without a client and after a restart, and writes its result in this chat when it ends.",
			Body:    StartBody{}, Reply: Task{},
		},
		{
			Method: "GET", Path: one, Handle: r.get,
			Summary: "One background task: where it is, what it has spent, and its result once it has one.",
			Reply:   Task{},
		},
		{
			Method: "GET", Path: one + "/events", Handle: r.events,
			Summary: "A background task's stream: the task each time it changes, then end. From the start, or from after the event named by Last-Event-ID.",
			Query:   []api.Param{{Name: "after", Description: "The id of the last event the client has, for a client that cannot set Last-Event-ID. The header wins when both are sent."}},
			Events:  streamEvents,
		},
		{
			Method: "POST", Path: one + "/stop", Handle: r.stop,
			Summary: "Stop a background task. Its round is stopped, what it did is kept in its chat, and the chat it was started from is told. It takes no body. Stopping a task that has ended changes nothing.",
			Reply:   Task{},
		},
	}
}

// answer writes the error a task function gave as the API's error body.
func (r *Runner) answer(w http.ResponseWriter, req *http.Request, c api.Call, err error) {
	switch {
	case errors.Is(err, chat.ErrNotFound), errors.Is(err, errNoTask):
		api.Error(w, req, http.StatusNotFound, "not_found", "there is no such chat or background task")
	case errors.Is(err, errInvalid):
		api.Error(w, req, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, errTooMany):
		api.Error(w, req, http.StatusTooManyRequests, "too_many_tasks", err.Error())
	default:
		r.logf("%s %s %s: %v", c.RequestID, req.Method, req.URL.Path, err)
		api.Error(w, req, http.StatusInternalServerError, "failed", "the call failed")
	}
}

func (r *Runner) list(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	got, err := listInChat(req.Context(), c.Store, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply := TasksReply{Tasks: []Task{}}
	for _, j := range got {
		reply.Tasks = append(reply.Tasks, j.Task)
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

func (r *Runner) start(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	var body StartBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	t, err := r.Start(req.Context(), c.Store, id, c.RequestID, api.ActorPerson, StartedByPerson, body)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, t)
}

func (r *Runner) get(w http.ResponseWriter, req *http.Request, c api.Call) {
	j, err := getInChat(req.Context(), c.Store, req.PathValue("id"), req.PathValue("task"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, j.Task)
}

func (r *Runner) events(w http.ResponseWriter, req *http.Request, c api.Call) {
	j, err := getInChat(req.Context(), c.Store, req.PathValue("id"), req.PathValue("task"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	last := api.LastEventID(req)
	if last == "" {
		last = req.URL.Query().Get("after")
	}
	var after int64
	if last != "" {
		if after, err = strconv.ParseInt(last, 10, 64); err != nil || after < 0 {
			api.Error(w, req, http.StatusBadRequest, "bad_request", "Last-Event-ID and after are the id of an event of this task, a whole number")
			return
		}
	}
	r.stream(w, req, c, j.ID, after)
}

// stream sends a task's events after the one with the id after, as they
// are written, until the end event or until the client goes. The events are
// read from the database, so it does not matter which start of the server
// wrote them.
func (r *Runner) stream(w http.ResponseWriter, req *http.Request, c api.Call, id string, after int64) {
	st, err := api.OpenStream(w, req)
	if err != nil {
		return
	}
	defer st.Close()
	ctx := req.Context()
	key := c.Store.TenantID() + "/" + id + "/events"
	for {
		got, err := eventsAfter(ctx, c.Store, id, after, r.o.Turns.StreamBatch)
		if err != nil {
			if ctx.Err() == nil {
				r.logf("%s reading the events of task %s: %v", c.RequestID, id, err)
			}
			return
		}
		for _, e := range got {
			if st.Send(api.Event{ID: strconv.FormatInt(e.seq, 10), Name: e.name, Data: json.RawMessage(e.data)}) != nil {
				return
			}
			after = e.seq
			if e.name == EventEnd {
				return
			}
		}
		if len(got) > 0 {
			continue
		}
		// Ask to be woken, then look once more.
		news := r.waitFor(key)
		var newest int64
		if err := c.Store.DB().QueryRowContext(ctx, "SELECT last_event FROM jobs WHERE id = ?", id).Scan(&newest); err != nil {
			return // the task went with its chat, or the client left
		}
		if newest > after {
			continue
		}
		select {
		case <-news:
		case <-time.After(r.o.Turns.StreamPoll):
		case <-ctx.Done():
			return
		}
	}
}

func (r *Runner) stop(w http.ResponseWriter, req *http.Request, c api.Call) {
	t, err := r.Stop(req.Context(), c.Store, req.PathValue("id"), req.PathValue("task"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, t)
}

// Stop stops a task and returns it as it then stands. The stop is written
// on the task first, so its goroutine finds it; its round is stopped; and
// this waits for the task to end, up to chats.stop_wait. A task that has
// ended is returned as it is.
func (r *Runner) Stop(ctx context.Context, s *tenant.Store, chatID, id string) (Task, error) {
	j, err := getInChat(ctx, s, chatID, id)
	if err != nil || ended(j.Status) {
		return j.Task, err
	}
	if _, err := s.DB().ExecContext(ctx, "UPDATE jobs SET stop_requested = 1 WHERE id = ?", j.ID); err != nil {
		return Task{}, err
	}
	key := s.TenantID() + "/" + j.ID
	r.wake(key)
	r.kick(s.TenantID(), j.ID)
	deadline := time.Now().Add(r.o.Turns.StopWait)
	for {
		news := r.waitFor(key + "/events")
		if j, err = getJob(ctx, s.DB(), j.ID); err != nil || ended(j.Status) {
			return j.Task, err
		}
		left := time.Until(deadline)
		if left <= 0 {
			return j.Task, nil
		}
		select {
		case <-news:
		case <-time.After(min(left, r.o.Turns.StreamPoll)):
		case <-ctx.Done():
			return j.Task, nil
		}
	}
}
