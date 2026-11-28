package adminapi

import (
	"errors"
	"net/http"

	"github.com/jonathanmkosar/keystone/internal/adminview"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/installsettings"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
)

// Settings from the browser (P15-03): a person's tenant-admin settings, and
// the install's own values. Every route reads and changes through the view,
// which writes each read and change of a person in their log and each
// change in the admin log with its old and new value.

// ChangeBody is what both PUT routes take: a setting's key and its new
// value, or reset to take the value away so the setting has its default.
type ChangeBody struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
	Reset bool   `json:"reset,omitempty"`
}

// PersonSettingsReply answers GET /api/v1/admin/people/{id}/settings.
type PersonSettingsReply struct {
	Person   string              `json:"person"`
	Settings []adminview.Setting `json:"settings"`
}

// InstallSettingsReply answers GET /api/v1/admin/install/settings: every
// install setting, and every person's own setting as the default a person
// starts from. The scope of each says which.
type InstallSettingsReply struct {
	Settings []adminview.Setting `json:"settings"`
}

// ChangedReply answers both PUT routes: the setting as it stands after the
// change, and the change as both logs hold it.
type ChangedReply struct {
	Setting adminview.Setting `json:"setting"`
	Change  adminview.Change  `json:"change"`
}

// SettingsRoutes returns the routes for settings from the browser. They are
// admin routes, behind the admin door, and go to a person through the view
// alone.
func SettingsRoutes(o Options) []api.Route {
	h := handlers{o}
	return []api.Route{
		{
			Method: "GET", Path: api.AdminPrefix + "people/{id}/settings", Admin: true, Handle: h.personSettings,
			Summary: "A person's tenant-admin settings: those the admin sets for them, the router's policy among them, each with its value, its default and whether it is set for them. Never the person's own settings. The person's own audit log records the read.",
			Reply:   PersonSettingsReply{},
		},
		{
			Method: "PUT", Path: api.AdminPrefix + "people/{id}/settings", Admin: true, Handle: h.changePersonSetting,
			Summary: "Set one of a person's tenant-admin settings, or with reset take it back to its default. A setting that is the person's own, or a fixed rule, is 403 not_allowed. The change is in the person's audit log and the admin audit log, with its old and new value.",
			Body:    ChangeBody{}, Reply: ChangedReply{},
		},
		{
			Method: "GET", Path: api.AdminPrefix + "install/settings", Admin: true, Handle: h.installSettings,
			Summary: "Every install setting, and every person's own setting as the default a person starts from, each with its shipped default, the install's value, what is in force now and whether it waits for a restart. Kept in keystone.json.",
			Reply:   InstallSettingsReply{},
		},
		{
			Method: "PUT", Path: api.AdminPrefix + "install/settings", Admin: true, Handle: h.changeInstallSetting,
			Summary: "Set the install's value for one setting, or with reset take it back to its shipped default: an install setting, or the default for a person's own setting. A setting marked restart takes effect at the next start. A tenant-admin setting, a fixed rule or data.dir is 403 not_allowed. The change is in the admin audit log with its old and new value.",
			Body:    ChangeBody{}, Reply: ChangedReply{},
		},
	}
}

func asker(r *http.Request, c api.Call) adminview.Asker {
	return adminview.Asker{AdminID: c.AdminSession.AccountID, RequestID: c.RequestID, Action: r.Method + " " + r.URL.Path}
}

// settingError answers a refused or failed settings call. It reports
// whether there was an error.
func (h handlers) settingError(w http.ResponseWriter, r *http.Request, c api.Call, err error, what string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, adminview.ErrNotAdmin):
		api.Error(w, r, http.StatusUnauthorized, "no_session", "sign in first")
	case errors.Is(err, adminview.ErrNoPerson):
		api.Error(w, r, http.StatusNotFound, "not_found", "there is no such person")
	case errors.Is(err, tenantsettings.ErrUnknown), errors.Is(err, installsettings.ErrUnknown):
		api.Error(w, r, http.StatusNotFound, "unknown_setting", err.Error())
	case errors.Is(err, tenantsettings.ErrNotAllowed), errors.Is(err, installsettings.ErrNotAllowed):
		api.Error(w, r, http.StatusForbidden, "not_allowed", err.Error())
	case errors.Is(err, tenantsettings.ErrBadValue), errors.Is(err, installsettings.ErrBadValue):
		api.Error(w, r, http.StatusBadRequest, "bad_value", err.Error())
	default:
		h.o.Log.Printf("%s %s failed: %v", c.RequestID, what, err)
		api.Error(w, r, http.StatusInternalServerError, "failed", what+" failed")
	}
	return true
}

// readChange reads a PUT's body: a key, and a value or reset.
func readChange(w http.ResponseWriter, r *http.Request) (ChangeBody, bool) {
	var body ChangeBody
	if !api.ReadJSON(w, r, &body) {
		return body, false
	}
	switch {
	case body.Key == "":
		api.Error(w, r, http.StatusBadRequest, "bad_request", "name the setting's key")
		return body, false
	case body.Reset && body.Value != "":
		api.Error(w, r, http.StatusBadRequest, "bad_request", "send a value or reset, not both")
		return body, false
	}
	return body, true
}

func (h handlers) personSettings(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	id := r.PathValue("id")
	list, err := h.o.View.PersonSettings(r.Context(), asker(r, c), id)
	if h.settingError(w, r, c, err, "reading a person's settings") {
		return
	}
	api.WriteJSON(w, http.StatusOK, PersonSettingsReply{Person: id, Settings: list})
}

func (h handlers) changePersonSetting(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	body, ok := readChange(w, r)
	if !ok {
		return
	}
	after, change, err := h.o.View.ChangePersonSetting(r.Context(), asker(r, c), r.PathValue("id"), body.Key, body.Value, body.Reset)
	if h.settingError(w, r, c, err, "changing a person's setting") {
		return
	}
	api.WriteJSON(w, http.StatusOK, ChangedReply{Setting: after, Change: change})
}

func (h handlers) installSettings(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	list, err := h.o.View.InstallSettings(asker(r, c))
	if h.settingError(w, r, c, err, "reading the install's settings") {
		return
	}
	api.WriteJSON(w, http.StatusOK, InstallSettingsReply{Settings: list})
}

func (h handlers) changeInstallSetting(w http.ResponseWriter, r *http.Request, c api.Call) {
	if !signedIn(w, r, c) {
		return
	}
	body, ok := readChange(w, r)
	if !ok {
		return
	}
	after, change, err := h.o.View.ChangeInstallSetting(r.Context(), asker(r, c), body.Key, body.Value, body.Reset)
	if h.settingError(w, r, c, err, "changing an install setting") {
		return
	}
	api.WriteJSON(w, http.StatusOK, ChangedReply{Setting: after, Change: change})
}
