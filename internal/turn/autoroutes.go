package turn

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/testtenant"
)

// The route of Auto in the turn (P14-04): "Try a stronger model".
// internal/app adds it to the one list beside Routes.

// AutoRoutes returns Auto's route, served by r.
func AutoRoutes(r *Runner) []api.Route {
	return []api.Route{
		{
			Method: "POST", Path: api.Prefix + "chats/{id}/turns/{turn}/stronger", Handle: r.stronger,
			Summary: "Try a stronger model: write a turn on Auto again, on a new branch beside its reply, one step of the ladder above the step that answered, at the same effort, and stream it as a send does. It takes no body. The verdict of the turn is marked stronger_asked, and the chat's next message is routed afresh. Refused with 409 not_auto for a turn that was not on Auto or has not ended, and no_stronger when no step above the one that answered may take the message.",
			Events:  streamEvents,
		},
	}
}

func conflict(w http.ResponseWriter, req *http.Request, code, why string) {
	api.Error(w, req, http.StatusConflict, code, why)
}

func (r *Runner) stronger(w http.ResponseWriter, req *http.Request, c api.Call) {
	ctx := req.Context()
	chatID := req.PathValue("id")
	of, err := getTurn(ctx, c.Store, chatID, req.PathValue("turn"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	if of.Route == nil || !ended(of) {
		conflict(w, req, CodeNotAuto, "only a turn on Auto that has ended can be written again by a stronger model")
		return
	}
	// The whole turn is written again, from the person's message: the
	// turn's first reply is made again, as a retry of it is.
	var first int64
	err = c.Store.DB().QueryRowContext(ctx, `SELECT tm.message_id FROM turn_messages tm JOIN messages m ON m.id = tm.message_id
		WHERE tm.turn_id = ? AND m.parent_id = ? AND m.role = 'assistant' ORDER BY tm.message_id LIMIT 1`, of.ID, of.UserMessageID).Scan(&first)
	if errors.Is(err, sql.ErrNoRows) {
		conflict(w, req, CodeNotAuto, "the turn wrote no reply to write again")
		return
	}
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	if err := r.setup(); err != nil {
		r.answer(w, req, c, err)
		return
	}
	body := SendBody{RetryOf: first}
	if err := checkSend(c.Store, chatID, &body); err != nil {
		r.answer(w, req, c, err)
		return
	}
	// Asked now, with the facts the turn will have, so a refusal is the
	// answer and not a failed turn. The turn asks again when it runs.
	test, err := testtenant.IsTest(c.Store)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	probe := row{Turn: Turn{ChatID: chatID}, input: body}
	probe.media = r.mediaOf(c.Store, probe)
	in, err := r.strongerInput(ctx, c.Store, probe, test)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	if _, err := router.Stronger(in, of.Model, seam.Effort(of.Route.Effort)); err != nil {
		var refused *router.Refusal
		if errors.As(err, &refused) {
			conflict(w, req, refused.Code, refused.Message)
			return
		}
		r.answer(w, req, c, err)
		return
	}
	if r.o.Install == nil {
		r.answer(w, req, c, errors.New("the runner has no data folder"))
		return
	}
	t, err := enqueueStronger(ctx, c.Store, chatID, c.RequestID, api.ActorPerson, body, r.o.Config.QueueMax, of.ID)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	if err := markStronger(c.Store, of.Route.VerdictID); err != nil {
		r.logf("%s marking the verdict of turn %s as stronger asked: %v", c.RequestID, of.ID, err)
	}
	r.kick(c.Store.TenantID(), chatID)
	w.Header().Set("X-Turn-Id", t.ID)
	r.stream(w, req, c, t.ID, 0)
}
