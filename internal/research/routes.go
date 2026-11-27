package research

import (
	"errors"
	"net/http"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
)

// Routes returns the research routes, served by r. The progress view reads
// one task about once a second while it is under way: there is no stream,
// since a task's steps stream on their own turns.
func Routes(r *Runner) []api.Route {
	const all = api.Prefix + "chats/{id}/research"
	const one = all + "/{research}"
	return []api.Route{
		{
			Method: "GET", Path: api.Prefix + "research", Handle: r.terms,
			Summary: "What a research task started now would run with: the person's budget for one, how many parts and sources it aims for, how long the report is asked to be, and how many of their tasks are under way.",
			Reply:   Terms{},
		},
		{
			Method: "GET", Path: all, Handle: r.list,
			Summary: "The research tasks started from this chat, or working in it, oldest first, each with its plan, its parts, the sources they have read and what it has spent.",
			Reply:   ListReply{},
		},
		{
			Method: "POST", Path: all, Handle: r.start,
			Summary: "Start a research task from this chat. It plans the question as parts, sends a sub-agent to search and read for each, and writes one report with numbered citations to the sources they read, saved as a Markdown artifact in this chat with a note that summarises it. It goes on without a client, within its own budget.",
			Body:    StartBody{}, Reply: Research{},
		},
		{
			Method: "GET", Path: one, Handle: r.get,
			Summary: "One research task: where it is, its plan, its parts, the sources read, what it has spent, and its report's artifact once it is written.",
			Reply:   Research{},
		},
		{
			Method: "POST", Path: one + "/stop", Handle: r.stop,
			Summary: "Stop a research task. The step under way and its parts are stopped, what they did is kept in their chats, and the chat it was started from is told. It takes no body. Stopping one that has ended changes nothing.",
			Reply:   Research{},
		},
	}
}

// answer writes the error a research function gave as the API's error body.
func (r *Runner) answer(w http.ResponseWriter, req *http.Request, c api.Call, err error) {
	switch {
	case errors.Is(err, chat.ErrNotFound), errors.Is(err, errNoResearch):
		api.Error(w, req, http.StatusNotFound, "not_found", "there is no such chat or research task")
	case errors.Is(err, errInvalid):
		api.Error(w, req, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, errTooMany):
		api.Error(w, req, http.StatusTooManyRequests, "too_many_research", err.Error())
	default:
		r.logf("%s %s %s: %v", c.RequestID, req.Method, req.URL.Path, err)
		api.Error(w, req, http.StatusInternalServerError, "failed", "the call failed")
	}
}

func (r *Runner) terms(w http.ResponseWriter, req *http.Request, c api.Call) {
	t, err := r.Terms(req.Context(), c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, t)
}

func (r *Runner) list(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := chat.Get(c.Store, id); err != nil {
		r.answer(w, req, c, err)
		return
	}
	got, err := list(req.Context(), c.Store.DB(), "chat_id = ? OR work_chat_id = ?", id, id)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	reply := ListReply{Research: []Research{}}
	for _, j := range got {
		reply.Research = append(reply.Research, r.show(req.Context(), c.Store, j))
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
	j, err := r.Start(req.Context(), c.Store, id, c.RequestID, api.ActorPerson, body)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, j)
}

func (r *Runner) get(w http.ResponseWriter, req *http.Request, c api.Call) {
	j, err := getInChat(req.Context(), c.Store, req.PathValue("id"), req.PathValue("research"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, r.show(req.Context(), c.Store, j))
}

func (r *Runner) stop(w http.ResponseWriter, req *http.Request, c api.Call) {
	j, err := r.Stop(req.Context(), c.Store, req.PathValue("id"), req.PathValue("research"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, j)
}
