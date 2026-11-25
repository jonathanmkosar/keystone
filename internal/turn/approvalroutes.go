package turn

import (
	"errors"
	"net/http"

	"github.com/jonathanmkosar/keystone/internal/api"
)

// The routes of the approval policy (P7-02): a turn's approvals and a
// person's answer to one, and each tool's policy. internal/app adds them
// to the one list beside Routes.

// ApprovalRoutes returns the approval policy's routes, served by r.
func ApprovalRoutes(r *Runner) []api.Route {
	const approvals = api.Prefix + "chats/{id}/turns/{turn}/approvals"
	const tools = api.Prefix + "tools"
	return []api.Route{
		{
			Method: "GET", Path: approvals, Handle: r.listApprovals,
			Summary: "The approvals a turn asked for, oldest first: each tool call that needed the person's answer before it ran, and what became of it.",
			Reply:   ApprovalsReply{},
		},
		{
			Method: "POST", Path: approvals + "/{approval}/approve", Handle: r.decide(ApprovalApproved),
			Summary: "Approve one tool call that waits for the person's answer. The turn goes on, and the call runs once. It takes no body. An approval that was already answered, or whose turn has ended, is refused with 409.",
			Reply:   Approval{},
		},
		{
			Method: "POST", Path: approvals + "/{approval}/decline", Handle: r.decide(ApprovalDeclined),
			Summary: "Decline one tool call that waits for the person's answer. The turn goes on, and the model is told the call was not run. It takes no body. An approval that was already answered, or whose turn has ended, is refused with 409.",
			Reply:   Approval{},
		},
		{
			Method: "GET", Path: tools, Handle: r.listTools,
			Summary: "Every tool a turn may use, with the person's policy for it (always, ask or never) and its default.",
			Reply:   ToolsReply{},
		},
		{
			Method: "PUT", Path: tools + "/{name}/policy", Handle: r.setPolicy,
			Summary: "Set the person's policy for one tool: always, ask or never. Whatever the policy, a tool that changes something asks once the chat has read outside content.",
			Body:    PolicyBody{}, Reply: ToolPolicy{},
		},
		{
			Method: "DELETE", Path: tools + "/{name}/policy", Handle: r.clearPolicy,
			Summary: "Remove the person's policy for one tool, so its default is back.",
			Reply:   ToolPolicy{},
		},
	}
}

func (r *Runner) listApprovals(w http.ResponseWriter, req *http.Request, c api.Call) {
	t, err := getTurn(req.Context(), c.Store, req.PathValue("id"), req.PathValue("turn"))
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	list, err := approvalsOf(req.Context(), c.Store, t)
	if err != nil {
		r.answer(w, req, c, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, ApprovalsReply{Approvals: list})
}

// decide answers one approval for the person who holds the session. The
// answer is written with the call's request id and actor, and the path,
// which says the answer, is in the audit log with them.
func (r *Runner) decide(status string) func(http.ResponseWriter, *http.Request, api.Call) {
	return func(w http.ResponseWriter, req *http.Request, c api.Call) {
		t, err := getTurn(req.Context(), c.Store, req.PathValue("id"), req.PathValue("turn"))
		if err != nil {
			r.answer(w, req, c, err)
			return
		}
		a, err := getApproval(req.Context(), c.Store, t.ID, req.PathValue("approval"))
		if err != nil {
			r.answer(w, req, c, err)
			return
		}
		if ended(t) {
			api.Error(w, req, http.StatusConflict, "turn_ended", "the turn has ended, so its calls can no longer be approved or declined")
			return
		}
		if a.Status != ApprovalPending {
			api.Error(w, req, http.StatusConflict, "answered", "this call was already "+a.Status)
			return
		}
		got, settled, err := settleApproval(c.Store, t.ID, a.ID, status, api.ActorPerson, c.RequestID)
		switch {
		case err != nil && !errors.Is(err, errNoTurn):
			r.answer(w, req, c, err)
			return
		case !settled:
			api.Error(w, req, http.StatusConflict, "answered", "this call was answered a moment ago")
			return
		}
		r.wake(c.Store.TenantID() + "/" + t.ID)
		api.WriteJSON(w, http.StatusOK, got)
	}
}
