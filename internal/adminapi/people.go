package adminapi

import (
	"errors"
	"net/http"

	"github.com/jonathanmkosar/keystone/internal/adminview"
	"github.com/jonathanmkosar/keystone/internal/api"
)

// People, the install and monitoring (P15-02): adding a person with a
// setup code, a new code, disabling and enabling, one person's usage and
// the whole install. Each reads or changes a person through the view
// alone, which writes the call in their log; the door writes it in the
// admin's.

// PeopleOptions is what these routes run with.
type PeopleOptions struct {
	// View is the one way to a person, made with adminview.NewWith so it
	// has the pause switch's hooks and admin.setup_code_for.
	View *adminview.View
	// Log is the server's own log.
	Log *api.Log
}

// AddPersonBody is what POST /api/v1/admin/people takes.
type AddPersonBody struct {
	Name string `json:"name"`
}

// AddedReply answers POST /api/v1/admin/people: the person, and their
// setup code, shown this once.
type AddedReply struct {
	Person    adminview.Person    `json:"person"`
	SetupCode adminview.SetupCode `json:"setup_code"`
}

// SetupCodeReply answers POST /api/v1/admin/people/{id}/setup-code.
type SetupCodeReply struct {
	SetupCode adminview.SetupCode `json:"setup_code"`
}

// PersonReply answers the routes that change one person: the person as
// People shows them, after the change.
type PersonReply struct {
	Person adminview.Person `json:"person"`
}

// PeopleRoutes returns the routes. Every one is under api.AdminPrefix and
// marked Admin.
func PeopleRoutes(o PeopleOptions) []api.Route {
	h := peopleHandlers{o}
	return []api.Route{
		{
			Method: "POST", Path: api.AdminPrefix + "people", Admin: true, Handle: h.add,
			Summary: "Add a person with a name. Answers the person and a one-time setup code, good for admin.setup_code_for, which the person enters at /setup to set their own password. The code is shown this once and kept only as a hash. The admin never sees or sets a password.",
			Body:    AddPersonBody{}, Reply: AddedReply{},
		},
		{
			Method: "GET", Path: api.AdminPrefix + "people/{id}", Admin: true, Handle: h.person,
			Summary: "One person, as a row of the people list: numbers only. Their own audit log records the read.",
			Reply:   PersonReply{},
		},
		{
			Method: "POST", Path: api.AdminPrefix + "people/{id}/setup-code", Admin: true, Handle: h.setupCode,
			Summary: "A new setup code for a person, ending any earlier one. Used, it sets their password and ends every session they had. 409 disabled for a person who is disabled.",
			Reply:   SetupCodeReply{},
		},
		{
			Method: "POST", Path: api.AdminPrefix + "people/{id}/disable", Admin: true, Handle: h.disable,
			Summary: "Disable a person: they cannot sign in, every session ends, their schedules are held and their tasks pause, and their share links answer as revoked. Their data is kept. Both audit logs record it.",
			Reply:   PersonReply{},
		},
		{
			Method: "POST", Path: api.AdminPrefix + "people/{id}/enable", Admin: true, Handle: h.enable,
			Summary: "Enable a disabled person again: sign-in, schedules and share links return. Runs missed while disabled are not caught up.",
			Reply:   PersonReply{},
		},
		{
			Method: "GET", Path: api.AdminPrefix + "people/{id}/usage", Admin: true, Handle: h.usage,
			Summary: "One person's model calls and spend by day and by model, failures counted by kind (never their words) and storage over time, for a period: from and to are dates, this month to today when left out.",
			Query: []api.Param{
				{Name: "from", Description: "The first day, as 2026-11-01. The first of this month when left out."},
				{Name: "to", Description: "The last day, as 2026-11-30. Today when left out."},
			},
			Reply: adminview.PersonUsage{},
		},
		{
			Method: "GET", Path: api.AdminPrefix + "install", Admin: true, Handle: h.install,
			Summary: "The whole install this month: everyone's calls and spend by model and by day (the sum of each person's), failures by kind, the disk, the people, the version and the sampler's state. Each person's audit log records the read.",
			Reply:   adminview.Install{},
		},
	}
}

type peopleHandlers struct {
	o PeopleOptions
}

func asker(r *http.Request, c api.Call) adminview.Asker {
	return adminview.Asker{AdminID: c.AdminSession.AccountID, RequestID: c.RequestID, Action: r.Method + " " + r.URL.Path}
}

// fail answers an error from the view.
func (h peopleHandlers) fail(w http.ResponseWriter, r *http.Request, c api.Call, what string, err error) {
	switch {
	case errors.Is(err, adminview.ErrNotAdmin):
		api.Error(w, r, http.StatusUnauthorized, "no_session", "sign in first")
	case errors.Is(err, adminview.ErrNoPerson):
		api.Error(w, r, http.StatusNotFound, "not_found", "there is no such person")
	case errors.Is(err, adminview.ErrName):
		api.Error(w, r, http.StatusBadRequest, "bad_request", "a person's name is 1 to 100 characters")
	case errors.Is(err, adminview.ErrPeriod):
		api.Error(w, r, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, adminview.ErrDisabled):
		api.Error(w, r, http.StatusConflict, "disabled", "the person is disabled: enable them first")
	default:
		// The reason is for the server's log, by request id; it holds no
		// person's content, and the reply says nothing of it.
		h.o.Log.Printf("%s %s failed: %v", c.RequestID, what, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", what+" failed")
	}
}

func (h peopleHandlers) add(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	var body AddPersonBody
	if !api.ReadJSON(w, r, &body) {
		return
	}
	p, code, err := h.o.View.AddPerson(r.Context(), asker(r, c), body.Name)
	if err != nil {
		h.fail(w, r, c, "adding a person", err)
		return
	}
	api.WriteJSON(w, http.StatusOK, AddedReply{Person: p, SetupCode: code})
}

func (h peopleHandlers) person(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	p, err := h.o.View.Person(r.Context(), asker(r, c), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, c, "reading a person", err)
		return
	}
	api.WriteJSON(w, http.StatusOK, PersonReply{Person: p})
}

func (h peopleHandlers) setupCode(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	code, err := h.o.View.NewSetupCode(r.Context(), asker(r, c), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, c, "making a setup code", err)
		return
	}
	api.WriteJSON(w, http.StatusOK, SetupCodeReply{SetupCode: code})
}

func (h peopleHandlers) setEnabled(w http.ResponseWriter, r *http.Request, c api.Call, enabled bool) {
	if !signedIn(w, r, c) {
		return
	}
	p, err := h.o.View.SetEnabled(r.Context(), asker(r, c), r.PathValue("id"), enabled)
	if err != nil {
		what := "disabling a person"
		if enabled {
			what = "enabling a person"
		}
		h.fail(w, r, c, what, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, PersonReply{Person: p})
}

func (h peopleHandlers) disable(w http.ResponseWriter, r *http.Request, c api.Call) {
	h.setEnabled(w, r, c, false)
}

func (h peopleHandlers) enable(w http.ResponseWriter, r *http.Request, c api.Call) {
	h.setEnabled(w, r, c, true)
}

func (h peopleHandlers) usage(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	q := r.URL.Query()
	u, err := h.o.View.Usage(r.Context(), asker(r, c), r.PathValue("id"), q.Get("from"), q.Get("to"))
	if err != nil {
		h.fail(w, r, c, "reading a person's usage", err)
		return
	}
	api.WriteJSON(w, http.StatusOK, u)
}

func (h peopleHandlers) install(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	in, err := h.o.View.Install(r.Context(), asker(r, c))
	if err != nil {
		h.fail(w, r, c, "reading the install", err)
		return
	}
	api.WriteJSON(w, http.StatusOK, in)
}
