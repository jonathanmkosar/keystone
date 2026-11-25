package schedules

import (
	"errors"
	"net/http"

	"github.com/jonathanmkosar/keystone/internal/api"
)

// Routes returns the schedule routes, served by sc.
func Routes(sc *Scheduler) []api.Route {
	const all = api.Prefix + "schedules"
	const one = all + "/{id}"
	return []api.Route{
		{
			Method: "GET", Path: all, Handle: sc.list,
			Summary: "Your scheduled tasks, oldest first, with the switch that holds them all and the time zone they run in.",
			Reply:   SchedulesReply{},
		},
		{
			Method: "POST", Path: all, Handle: sc.create,
			Summary: "Make a scheduled task. Its first run is at its first due time after now. Each run is a chat of its own, titled by the schedule and the date.",
			Body:    ScheduleBody{}, Reply: Schedule{},
		},
		{
			Method: "POST", Path: all + "/pause-all", Handle: sc.holdAll,
			Summary: "Hold every scheduled task: none starts a run until resume-all, and the times that pass meanwhile are not made up. A run under way goes on. It takes no body.",
			Reply:   SchedulesReply{},
		},
		{
			Method: "POST", Path: all + "/resume-all", Handle: sc.releaseAll,
			Summary: "Let every scheduled task run again, each at its first due time after now. It takes no body.",
			Reply:   SchedulesReply{},
		},
		{
			Method: "GET", Path: one, Handle: sc.get,
			Summary: "One scheduled task, with its newest runs, newest first.",
			Reply:   ScheduleReply{},
		},
		{
			Method: "PUT", Path: one, Handle: sc.update,
			Summary: "Change a scheduled task: every field is replaced. Its next due time is worked out again from now.",
			Body:    ScheduleBody{}, Reply: Schedule{},
		},
		{
			Method: "DELETE", Path: one, Handle: sc.remove,
			Summary: "Delete a scheduled task and the record of its runs. Each run's chat stays, and a run under way goes on.",
			Reply:   DeletedReply{},
		},
		{
			Method: "POST", Path: one + "/pause", Handle: sc.pause,
			Summary: "Pause a scheduled task: it starts no run until it is resumed. It takes no body.",
			Reply:   Schedule{},
		},
		{
			Method: "POST", Path: one + "/resume", Handle: sc.resume,
			Summary: "Resume a paused scheduled task, at its first due time after now: the times that passed while it was paused are not made up. It takes no body.",
			Reply:   Schedule{},
		},
		{
			Method: "POST", Path: one + "/run", Handle: sc.runNow,
			Summary: "Run a scheduled task now, paused or held, in a chat of its own. It changes no due time, and is refused while a run of it is under way. It takes no body.",
			Reply:   Run{},
		},
	}
}

// answer writes the error a schedule function gave as the API's error body.
func (sc *Scheduler) answer(w http.ResponseWriter, req *http.Request, c api.Call, err error) {
	switch {
	case errors.Is(err, errNoSchedule):
		api.Error(w, req, http.StatusNotFound, "not_found", "there is no such schedule")
	case errors.Is(err, errInvalid):
		api.Error(w, req, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, errTooMany):
		api.Error(w, req, http.StatusTooManyRequests, "too_many_schedules", err.Error())
	case errors.Is(err, errRunOpen):
		api.Error(w, req, http.StatusConflict, "run_open", err.Error())
	default:
		sc.logf("%s %s %s: %v", c.RequestID, req.Method, req.URL.Path, err)
		api.Error(w, req, http.StatusInternalServerError, "failed", "the call failed")
	}
}

func (sc *Scheduler) list(w http.ResponseWriter, req *http.Request, c api.Call) {
	reply, err := sc.List(req.Context(), c.Store)
	if err != nil {
		sc.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

func (sc *Scheduler) create(w http.ResponseWriter, req *http.Request, c api.Call) {
	var body ScheduleBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	got, err := sc.Create(req.Context(), c.Store, body)
	if err != nil {
		sc.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, got)
}

func (sc *Scheduler) get(w http.ResponseWriter, req *http.Request, c api.Call) {
	reply, err := sc.Get(req.Context(), c.Store, req.PathValue("id"))
	if err != nil {
		sc.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

func (sc *Scheduler) update(w http.ResponseWriter, req *http.Request, c api.Call) {
	id := req.PathValue("id")
	if _, err := getSchedule(req.Context(), c.Store.DB(), id); err != nil {
		sc.answer(w, req, c, err)
		return
	}
	var body ScheduleBody
	if !api.ReadJSON(w, req, &body) {
		return
	}
	got, err := sc.Update(req.Context(), c.Store, id, body)
	if err != nil {
		sc.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, got)
}

func (sc *Scheduler) remove(w http.ResponseWriter, req *http.Request, c api.Call) {
	if err := sc.Delete(req.Context(), c.Store, req.PathValue("id")); err != nil {
		sc.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, DeletedReply{Deleted: true})
}

func (sc *Scheduler) pause(w http.ResponseWriter, req *http.Request, c api.Call) {
	sc.enable(w, req, c, false)
}

func (sc *Scheduler) resume(w http.ResponseWriter, req *http.Request, c api.Call) {
	sc.enable(w, req, c, true)
}

func (sc *Scheduler) enable(w http.ResponseWriter, req *http.Request, c api.Call, on bool) {
	got, err := sc.SetEnabled(req.Context(), c.Store, req.PathValue("id"), on)
	if err != nil {
		sc.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, got)
}

func (sc *Scheduler) runNow(w http.ResponseWriter, req *http.Request, c api.Call) {
	got, err := sc.RunNow(req.Context(), c.Store, req.PathValue("id"), c.RequestID, api.ActorPerson)
	if err != nil {
		sc.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, got)
}

func (sc *Scheduler) holdAll(w http.ResponseWriter, req *http.Request, c api.Call) {
	sc.hold(w, req, c, true)
}

func (sc *Scheduler) releaseAll(w http.ResponseWriter, req *http.Request, c api.Call) {
	sc.hold(w, req, c, false)
}

func (sc *Scheduler) hold(w http.ResponseWriter, req *http.Request, c api.Call, on bool) {
	reply, err := sc.Hold(req.Context(), c.Store, on)
	if err != nil {
		sc.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, reply)
}
