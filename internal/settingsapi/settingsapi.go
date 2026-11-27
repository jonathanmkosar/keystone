// Package settingsapi is the routes behind the settings screens: the
// settings themselves, the names of a person's secrets, their model
// registry and their profile.
//
// Every route acts for the signed-in person on their own tenant, with the
// store the API shell hands it. Nothing here opens another store.
//
// The packages that guard a fixed rule (the tenant store, the audit log,
// the model registry, the profile) read no setting. This package reads the
// settings and passes the values in, so those packages stay as they are.
//
// Three things hold for every route here:
//
//   - A setting is changed through internal/tenantsettings, as the person.
//     A setting whose scope is not the person's own is listed and cannot be
//     changed, and a fixed rule is not a setting at all: it is listed apart,
//     with no value, and a write to one is refused.
//   - A secret's value goes in and never comes out. No route returns one,
//     and no error repeats what was sent.
//   - A model entry names its key and never holds it. The list says whether
//     a secret of that name is stored.
//
// docs/architecture/settings-screens.md describes the routes and the
// screens built on them.
package settingsapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/knowledge/personmap"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// by is who every route here acts as. There is no admin role in the API
// yet, so a session is always the person.
const by = tenantsettings.Person

// The settings this package reads for the parts that read none themselves.
const (
	settingMaxModels = "models.max_per_tenant"
)

// The shapes the routes take and return.

// Setting is one setting as it stands for the signed-in person.
type Setting struct {
	Key string `json:"key"`
	// Title is the setting's readable name, and Summary one short plain line
	// on what it does: what the settings screen shows first. Either is
	// empty for a setting the registry gives none.
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Lane    string `json:"lane"`
	// Type is bool, int, float, string or duration.
	Type    string `json:"type"`
	Default string `json:"default"`
	// Options are the values a string setting may take. Empty means any.
	Options     []string `json:"options"`
	Scope       string   `json:"scope"`
	Description string   `json:"description"`
	// Value is the value in force: the person's own, or the default.
	Value string `json:"value"`
	// Overridden says Value is the person's own and not the default.
	Overridden bool `json:"overridden"`
	// Changeable says the signed-in person may change this setting.
	Changeable bool `json:"changeable"`
	// Updated is when the person's own value was stored.
	Updated time.Time `json:"updated,omitzero"`
}

// Rule is one of the four fixed rules. It has no value and no setting can
// switch it off, so it has nothing but its name and its words.
type Rule struct {
	Key  string `json:"key"`
	Rule string `json:"rule"`
}

// SettingsReply answers GET /api/v1/settings.
type SettingsReply struct {
	Settings []Setting `json:"settings"`
	// Rules are the fixed rules, for the screen to show. They are not
	// settings and none can be changed.
	Rules []Rule `json:"rules"`
}

// ValueBody is what a route that stores one value takes.
type ValueBody struct {
	Value string `json:"value"`
}

// Secret is the name of one stored secret. It never holds the value.
type Secret struct {
	Name string `json:"name"`
	// UsedBy are the models in the person's registry that name it as
	// their key.
	UsedBy []string `json:"used_by"`
}

// SecretsReply answers GET /api/v1/secrets.
type SecretsReply struct {
	Secrets []Secret `json:"secrets"`
}

// DoneReply answers a route that removed something.
type DoneReply struct {
	Removed bool `json:"removed"`
}

// Model is one row of the person's registry.
type Model struct {
	// Entry is the entry as the registry holds it. Its key field is the
	// name of a secret and never the secret.
	Entry models.Model `json:"entry"`
	// KeyPresent says a secret with the entry's key name is stored. It is
	// true for an entry that names no key, because none is missing.
	KeyPresent bool `json:"key_present"`
	// MayReadSensitive says content from a sensitive space may be sent to
	// this model. It follows from the class alone.
	MayReadSensitive bool      `json:"may_read_sensitive"`
	Updated          time.Time `json:"updated,omitzero"`
	// Problem is why a stored entry is refused today. Such an entry has
	// only its name, and can be replaced or removed.
	Problem string `json:"problem,omitempty"`
}

// Adapter is one adapter an entry may name.
type Adapter struct {
	Name   string `json:"name"`
	Speaks string `json:"speaks"`
	// InProcess says entries of this adapter take no endpoint and no key.
	InProcess bool `json:"in_process"`
}

// ModelsReply answers GET /api/v1/models.
type ModelsReply struct {
	Models []Model `json:"models"`
	// The rest is what a form needs to offer the choices an entry has.
	Adapters     []Adapter `json:"adapters"`
	Classes      []string  `json:"classes"`
	Capabilities []string  `json:"capabilities"`
	// Max is the most models one person may hold.
	Max int `json:"max"`
}

// Profile is the short text about the person that starts every call.
type Profile struct {
	Text    string    `json:"text"`
	Updated time.Time `json:"updated,omitzero"`
	// MaxChars is the longest the text may be.
	MaxChars int `json:"max_chars"`
}

// ProfileBody is what PUT /api/v1/profile takes.
type ProfileBody struct {
	Text string `json:"text"`
}

// secretName is the shape of a secret's name, the same as the vault's.
var secretName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)

// maxSecretBytes is the longest value the vault keeps.
const maxSecretBytes = 64 << 10

// registry reads the settings registry once.
var registry = sync.OnceValues(settings.Registry)

// Routes returns the routes behind the settings screens.
func Routes() []api.Route {
	const (
		all     = api.Prefix + "settings"
		one     = all + "/{key}"
		secrets = api.Prefix + "secrets"
		secret  = secrets + "/{name}"
		reg     = api.Prefix + "models"
		model   = reg + "/{name}"
		profile = api.Prefix + "profile"
	)
	return []api.Route{
		{
			Method: "GET", Path: all, Handle: listSettings,
			Summary: "Every setting as it stands for the person: its title, its short summary, its value, default, type, options, scope and description, and whether the person may change it. The fixed rules are listed apart.",
			Reply:   SettingsReply{},
		},
		{
			Method: "PUT", Path: one, Handle: setSetting,
			Summary: "Store the person's own value for one setting. Refused for a setting that is not theirs to change, and for a fixed rule.",
			Body:    ValueBody{}, Reply: Setting{},
		},
		{
			Method: "DELETE", Path: one, Handle: clearSetting,
			Summary: "Remove the person's own value, so the setting goes back to its default.",
			Reply:   Setting{},
		},
		{
			Method: "GET", Path: secrets, Handle: listSecrets,
			Summary: "The names of the person's stored secrets, and the models that use each. Never a value.",
			Reply:   SecretsReply{},
		},
		{
			Method: "PUT", Path: secret, Handle: setSecret,
			Summary: "Store a secret, such as a provider key, under a name. The answer holds the name and never the value.",
			Body:    ValueBody{}, Reply: Secret{},
		},
		{
			Method: "DELETE", Path: secret, Handle: deleteSecret,
			Summary: "Remove one stored secret.",
			Reply:   DoneReply{},
		},
		{
			Method: "GET", Path: reg, Handle: listModels,
			Summary: "The person's model registry. Each entry names its key and says whether a secret of that name is stored. Never the key.",
			Reply:   ModelsReply{},
		},
		{
			Method: "POST", Path: reg, Handle: addModel,
			Summary: "Add a model to the person's registry.",
			Body:    models.Model{}, Reply: Model{},
		},
		{
			Method: "PUT", Path: model, Handle: replaceModel,
			Summary: "Replace the entry for one model.",
			Body:    models.Model{}, Reply: Model{},
		},
		{
			Method: "DELETE", Path: model, Handle: removeModel,
			Summary: "Remove a model from the person's registry.",
			Reply:   DoneReply{},
		},
		{
			Method: "GET", Path: profile, Handle: readProfile,
			Summary: "The person's profile: the short text about them that starts every call to a model.",
			Reply:   Profile{},
		},
		{
			Method: "PUT", Path: profile, Handle: writeProfile,
			Summary: "Replace the person's profile. An empty text clears it.",
			Body:    ProfileBody{}, Reply: Profile{},
		},
	}
}

// failed answers a call that broke for a reason the caller cannot mend.
// The reason goes nowhere but the status: the shell's log has the call.
func failed(w http.ResponseWriter, r *http.Request) {
	api.Error(w, r, http.StatusInternalServerError, "failed", "the call failed")
}

// The settings.

// view turns one tenant value into what the route returns.
func view(v tenantsettings.Value) Setting {
	options := v.Setting.Options
	if options == nil {
		options = []string{}
	}
	return Setting{
		Key: v.Setting.Key, Lane: v.Setting.Lane, Type: v.Setting.Type,
		Title: v.Setting.Title, Summary: v.Setting.Summary,
		Default: v.Setting.Default, Options: options,
		Scope: string(v.Setting.Scope), Description: v.Setting.Description,
		Value: v.Value, Overridden: v.Overridden,
		Changeable: tenantsettings.MayChange(v.Setting, by) == nil,
		Updated:    v.UpdatedAt,
	}
}

func listSettings(w http.ResponseWriter, r *http.Request, c api.Call) {
	reg, err := registry()
	if err != nil {
		failed(w, r)
		return
	}
	values, err := tenantsettings.All(r.Context(), c.Store, reg)
	if err != nil {
		failed(w, r)
		return
	}
	reply := SettingsReply{Settings: make([]Setting, 0, len(values)), Rules: []Rule{}}
	for _, v := range values {
		// The registry refuses a key kept for the fixed rules. This is the
		// same refusal again, so a rule can never be listed as a setting.
		if settings.IsFixedRule(v.Setting.Key) {
			continue
		}
		reply.Settings = append(reply.Settings, view(v))
	}
	for _, rule := range settings.FixedRules() {
		reply.Rules = append(reply.Rules, Rule{Key: rule.Key, Rule: rule.Rule})
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

// one answers with a setting as it stands now.
func one(w http.ResponseWriter, r *http.Request, s *tenant.Store, reg []settings.Setting, key string) {
	values, err := tenantsettings.All(r.Context(), s, reg)
	if err != nil {
		failed(w, r)
		return
	}
	for _, v := range values {
		if v.Setting.Key == key {
			api.WriteJSON(w, http.StatusOK, view(v))
			return
		}
	}
	failed(w, r)
}

// refuseSetting answers a change tenantsettings refused. What was sent as
// the value is not repeated.
func refuseSetting(w http.ResponseWriter, r *http.Request, reg []settings.Setting, key string, err error) {
	switch {
	case settings.IsFixedRule(key):
		api.Error(w, r, http.StatusForbidden, "fixed_rule", "that is a fixed rule, not a setting, and nothing can switch it off")
	case errors.Is(err, tenantsettings.ErrUnknown):
		api.Error(w, r, http.StatusNotFound, "not_found", "there is no such setting")
	case errors.Is(err, tenantsettings.ErrNotAllowed):
		api.Error(w, r, http.StatusForbidden, "not_allowed", "that setting is not yours to change: the admin sets it")
	case errors.Is(err, tenantsettings.ErrBadValue):
		message := "that is not a value this setting takes"
		if st, ok := settings.Lookup(reg, key); ok {
			message = "that is not a value for " + key + ": it takes " + takes(st)
		}
		api.Error(w, r, http.StatusBadRequest, "bad_value", message)
	default:
		failed(w, r)
	}
}

// takes says in words what a setting's value must be.
func takes(st settings.Setting) string {
	if len(st.Options) > 0 {
		out := "one of "
		for i, o := range st.Options {
			if i > 0 {
				out += ", "
			}
			out += o
		}
		return out
	}
	switch st.Type {
	case "bool":
		return "true or false"
	case "int":
		return "a whole number"
	case "float":
		return "a number"
	case "duration":
		return "a length of time such as 30s, 5m or 1h"
	}
	return "text"
}

func setSetting(w http.ResponseWriter, r *http.Request, c api.Call) {
	key := r.PathValue("key")
	var body ValueBody
	if !api.ReadJSON(w, r, &body) {
		return
	}
	reg, err := registry()
	if err != nil {
		failed(w, r)
		return
	}
	if err := tenantsettings.Set(r.Context(), c.Store, reg, by, key, body.Value); err != nil {
		refuseSetting(w, r, reg, key, err)
		return
	}
	one(w, r, c.Store, reg, key)
}

func clearSetting(w http.ResponseWriter, r *http.Request, c api.Call) {
	key := r.PathValue("key")
	reg, err := registry()
	if err != nil {
		failed(w, r)
		return
	}
	if err := tenantsettings.Clear(r.Context(), c.Store, reg, by, key); err != nil {
		refuseSetting(w, r, reg, key, err)
		return
	}
	one(w, r, c.Store, reg, key)
}

// The secrets.

// usedBy returns, for each key name, the models that name it.
func usedBy(ctx context.Context, s *tenant.Store) (map[string][]string, error) {
	entries, err := models.List(ctx, s)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, e := range entries {
		if e.Model.Key != "" {
			out[e.Model.Key] = append(out[e.Model.Key], e.Model.Name)
		}
	}
	return out, nil
}

func listSecrets(w http.ResponseWriter, r *http.Request, c api.Call) {
	names, err := c.Store.Secrets().Names()
	if err != nil {
		failed(w, r)
		return
	}
	used, err := usedBy(r.Context(), c.Store)
	if err != nil {
		failed(w, r)
		return
	}
	reply := SecretsReply{Secrets: make([]Secret, 0, len(names))}
	for _, name := range names {
		reply.Secrets = append(reply.Secrets, Secret{Name: name, UsedBy: orEmpty(used[name])})
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

func orEmpty(names []string) []string {
	if names == nil {
		return []string{}
	}
	return names
}

// badSecretName answers a name the vault would refuse. The name is not
// repeated, in case a key was pasted where the name goes.
func badSecretName(w http.ResponseWriter, r *http.Request) {
	api.Error(w, r, http.StatusBadRequest, "bad_name", "a secret's name is 1 to 100 lower-case letters, digits, dots, dashes and underscores, starting with a letter or a digit, such as provider.example")
}

func setSecret(w http.ResponseWriter, r *http.Request, c api.Call) {
	name := r.PathValue("name")
	var body ValueBody
	if !api.ReadJSON(w, r, &body) {
		return
	}
	if !secretName.MatchString(name) {
		badSecretName(w, r)
		return
	}
	if body.Value == "" || len(body.Value) > maxSecretBytes || !utf8.ValidString(body.Value) {
		api.Error(w, r, http.StatusBadRequest, "bad_value", "a secret's value is text of 1 to "+strconv.Itoa(maxSecretBytes)+" bytes")
		return
	}
	if err := c.Store.Secrets().Set(name, body.Value); err != nil {
		failed(w, r)
		return
	}
	used, err := usedBy(r.Context(), c.Store)
	if err != nil {
		failed(w, r)
		return
	}
	api.WriteJSON(w, http.StatusOK, Secret{Name: name, UsedBy: orEmpty(used[name])})
}

func deleteSecret(w http.ResponseWriter, r *http.Request, c api.Call) {
	name := r.PathValue("name")
	if !secretName.MatchString(name) {
		badSecretName(w, r)
		return
	}
	vault := c.Store.Secrets()
	names, err := vault.Names()
	if err != nil {
		failed(w, r)
		return
	}
	if !slices.Contains(names, name) {
		api.Error(w, r, http.StatusNotFound, "not_found", "there is no secret of that name")
		return
	}
	if err := vault.Delete(name); err != nil {
		failed(w, r)
		return
	}
	api.WriteJSON(w, http.StatusOK, DoneReply{Removed: true})
}

// The model registry.

// limits reads the registry's one limit for this person. The registry
// package guards a fixed rule and reads no setting, so it is read here.
func limits(ctx context.Context, s *tenant.Store) (models.Limits, error) {
	reg, err := registry()
	if err != nil {
		return models.Limits{}, err
	}
	text, err := tenantsettings.Get(ctx, s, reg, settingMaxModels)
	if err != nil {
		return models.Limits{}, err
	}
	n, err := strconv.Atoi(text)
	if err != nil || n <= 0 {
		return models.Limits{}, errors.New(settingMaxModels + " must be a whole number more than zero")
	}
	return models.Limits{MaxModels: n}, nil
}

// entryView turns a registry row into what the route returns. names are
// the names of the stored secrets.
func entryView(e models.Entry, names []string) Model {
	return Model{
		Entry:            e.Model,
		KeyPresent:       e.Model.Key == "" || slices.Contains(names, e.Model.Key),
		MayReadSensitive: e.Problem == "" && e.Model.MayReadSensitive(),
		Updated:          e.UpdatedAt,
		Problem:          e.Problem,
	}
}

func listModels(w http.ResponseWriter, r *http.Request, c api.Call) {
	entries, err := models.List(r.Context(), c.Store)
	if err != nil {
		failed(w, r)
		return
	}
	names, err := c.Store.Secrets().Names()
	if err != nil {
		failed(w, r)
		return
	}
	lim, err := limits(r.Context(), c.Store)
	if err != nil {
		failed(w, r)
		return
	}
	reply := ModelsReply{
		Models:   make([]Model, 0, len(entries)),
		Adapters: []Adapter{}, Classes: []string{}, Capabilities: []string{},
		Max: lim.MaxModels,
	}
	for _, e := range entries {
		reply.Models = append(reply.Models, entryView(e, names))
	}
	for _, a := range models.Adapters() {
		reply.Adapters = append(reply.Adapters, Adapter{Name: a.Name, Speaks: a.Speaks, InProcess: a.InProcess})
	}
	for _, class := range []models.Class{models.ClassLocal, models.ClassHosted, models.ClassTrusted} {
		reply.Classes = append(reply.Classes, string(class))
	}
	for _, capability := range models.Capabilities() {
		reply.Capabilities = append(reply.Capabilities, string(capability))
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

// refuseModel answers a change the registry refused. The registry's reason
// for an entry that is not valid names the field and never repeats an
// endpoint, a key name or a certificate.
func refuseModel(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, models.ErrInvalid):
		api.Error(w, r, http.StatusBadRequest, "bad_model", strings.TrimPrefix(err.Error(), models.ErrInvalid.Error()+": "))
	case errors.Is(err, models.ErrExists):
		api.Error(w, r, http.StatusConflict, "exists", "a model of that name is already there")
	case errors.Is(err, models.ErrFull):
		api.Error(w, r, http.StatusConflict, "full", "the registry is full: remove a model before adding one")
	case errors.Is(err, models.ErrUnknown):
		api.Error(w, r, http.StatusNotFound, "not_found", "there is no model of that name")
	default:
		failed(w, r)
	}
}

// stored answers with one model as the registry holds it now.
func stored(w http.ResponseWriter, r *http.Request, s *tenant.Store, name string, status int) {
	entries, err := models.List(r.Context(), s)
	if err != nil {
		failed(w, r)
		return
	}
	names, err := s.Secrets().Names()
	if err != nil {
		failed(w, r)
		return
	}
	for _, e := range entries {
		if e.Model.Name == name {
			api.WriteJSON(w, status, entryView(e, names))
			return
		}
	}
	failed(w, r)
}

func addModel(w http.ResponseWriter, r *http.Request, c api.Call) {
	var m models.Model
	if !api.ReadJSON(w, r, &m) {
		return
	}
	if refuseCACertPath(w, r, m, "") { // a path only from the command line (P13-02, decision 0490)
		return
	}
	lim, err := limits(r.Context(), c.Store)
	if err != nil {
		failed(w, r)
		return
	}
	if err := models.Add(r.Context(), c.Store, lim, m); err != nil {
		refuseModel(w, r, err)
		return
	}
	stored(w, r, c.Store, m.Name, http.StatusCreated)
}

func replaceModel(w http.ResponseWriter, r *http.Request, c api.Call) {
	name := r.PathValue("name")
	var m models.Model
	if !api.ReadJSON(w, r, &m) {
		return
	}
	if m.Name == "" {
		m.Name = name
	}
	if m.Name != name {
		api.Error(w, r, http.StatusBadRequest, "bad_model", "the entry's name is not the name in the address: a model is renamed by removing it and adding it again")
		return
	}
	if refuseCACertPath(w, r, m, heldCACert(r.Context(), c.Store, name)) { // a path only from the command line (P13-02, decision 0490)
		return
	}
	if err := models.Replace(r.Context(), c.Store, m); err != nil {
		refuseModel(w, r, err)
		return
	}
	stored(w, r, c.Store, name, http.StatusOK)
}

func removeModel(w http.ResponseWriter, r *http.Request, c api.Call) {
	if err := models.Remove(r.Context(), c.Store, r.PathValue("name")); err != nil {
		refuseModel(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, DoneReply{Removed: true})
}

// The profile.

// profileMax reads the longest a profile may be. The profile's own package
// reads no setting, so it is read here.
func profileMax(ctx context.Context, s *tenant.Store) (int, error) {
	reg, err := registry()
	if err != nil {
		return 0, err
	}
	text, err := tenantsettings.Get(ctx, s, reg, personmap.SettingProfileMaxChars)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(text)
}

func readProfile(w http.ResponseWriter, r *http.Request, c api.Call) {
	most, err := profileMax(r.Context(), c.Store)
	if err != nil {
		failed(w, r)
		return
	}
	p, err := personmap.ReadProfile(r.Context(), c.Store)
	if err != nil {
		failed(w, r)
		return
	}
	api.WriteJSON(w, http.StatusOK, Profile{Text: p.Text, Updated: p.Updated, MaxChars: most})
}

func writeProfile(w http.ResponseWriter, r *http.Request, c api.Call) {
	var body ProfileBody
	if !api.ReadJSON(w, r, &body) {
		return
	}
	most, err := profileMax(r.Context(), c.Store)
	if err != nil {
		failed(w, r)
		return
	}
	if !utf8.ValidString(body.Text) || strings.ContainsRune(body.Text, 0) {
		api.Error(w, r, http.StatusBadRequest, "bad_text", "the profile must be plain text")
		return
	}
	p, err := personmap.SetProfile(r.Context(), c.Store, body.Text, most)
	if errors.Is(err, personmap.ErrTooLong) {
		api.Error(w, r, http.StatusBadRequest, "too_long", "the profile is too long: the most is "+strconv.Itoa(most)+" characters")
		return
	}
	if err != nil {
		failed(w, r)
		return
	}
	api.WriteJSON(w, http.StatusOK, Profile{Text: p.Text, Updated: p.Updated, MaxChars: most})
}
