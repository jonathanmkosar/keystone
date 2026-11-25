package connectors

import (
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// The routes behind Settings → Connectors and the chat's tools menu. Each
// acts for the signed-in person on their own tenant, with the store the API
// shell hands it. A token is stored with the secrets route (PUT
// /api/v1/secrets/{name}) and no route here returns one.

// Row is one connector as the routes show it. It holds the name of the
// secret its token is kept under and never the token, and a stdio
// connector's environment by its names only.
type Row struct {
	Name      string   `json:"name"`
	Label     string   `json:"label"`
	Transport string   `json:"transport"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args"`
	EnvNames  []string `json:"env_names"`
	TokenEnv  string   `json:"token_env,omitempty"`
	URL       string   `json:"url,omitempty"`
	Local     bool     `json:"local"`
	// Secret is the name the token is stored under. Empty means it takes
	// none.
	Secret string `json:"secret"`
	// TokenPresent says a secret of that name is stored.
	TokenPresent bool `json:"token_present"`
	Enabled      bool `json:"enabled"`
	Outside      bool `json:"outside"`
	// AddedBy is person or command line. A connector added from the
	// command line runs a command, and only its label, token name, switch
	// and outside mark may be changed through the API.
	AddedBy string    `json:"added_by"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Listed  time.Time `json:"listed,omitzero"`
	Problem string    `json:"problem,omitempty"`
	Tools   []Tool    `json:"tools"`
}

// ListReply answers GET /api/v1/connectors.
type ListReply struct {
	Connectors []Row `json:"connectors"`
	// Max is the most connectors one person may have.
	Max int `json:"max"`
	// Transports are the transports Keystone speaks. Only http can be added
	// through the API.
	Transports []string `json:"transports"`
}

// Body adds or changes a connector. Enabled and Outside are true when left
// out of an add, and unchanged when left out of a change.
type Body struct {
	Name      string            `json:"name"`
	Label     string            `json:"label"`
	Transport string            `json:"transport"`
	URL       string            `json:"url"`
	Local     bool              `json:"local"`
	Secret    string            `json:"secret"`
	Enabled   *bool             `json:"enabled"`
	Outside   *bool             `json:"outside"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	TokenEnv  string            `json:"token_env"`
	// Check reaches the connector after it is stored and lists its tools.
	// True when left out.
	Check *bool `json:"check"`
}

// ChatRow is one connector in a chat's tools menu.
type ChatRow struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Enabled says the connector is on in Settings. One that is not is off
	// in every chat.
	Enabled bool `json:"enabled"`
	// On says its tools are offered in this chat.
	On bool `json:"on"`
	// Switched says the chat has a switch of its own for it.
	Switched bool   `json:"switched"`
	Tools    int    `json:"tools"`
	Problem  string `json:"problem,omitempty"`
}

// ChatReply answers the chat's connector routes.
type ChatReply struct {
	Connectors []ChatRow `json:"connectors"`
}

// SwitchBody switches a connector on or off in one chat.
type SwitchBody struct {
	On bool `json:"on"`
}

// DoneReply answers a route that removed something.
type DoneReply struct {
	Removed bool `json:"removed"`
}

// commandLineOnly is the refusal of a command through the API.
const commandLineOnly = "a connector that runs a command on this machine is added and changed from the command line only (go run ./tools/tenant connector add), so that nobody signed in through the browser can run a program on it"

// Routes returns the connector routes.
func Routes(o Options) []api.Route {
	const (
		all  = api.Prefix + "connectors"
		one  = all + "/{name}"
		menu = api.Prefix + "chats/{id}/connectors"
	)
	h := handlers{o: o}
	return []api.Route{
		{Method: "GET", Path: all, Handle: h.list, Reply: ListReply{},
			Summary: "The person's connectors: MCP servers whose tools a chat may use. Each names the secret its token is stored under and says whether it is stored, with the tools it last listed. Never a token."},
		{Method: "POST", Path: all, Handle: h.add, Body: Body{}, Reply: Row{},
			Summary: "Add a remote connector, reached over the streamable HTTP transport, and reach it to list its tools. A connector that runs a command is refused: it is added from the command line."},
		{Method: "PUT", Path: one, Handle: h.replace, Body: Body{}, Reply: Row{},
			Summary: "Change a connector: its label, address, token name, switch and outside mark. A command cannot be set or changed here."},
		{Method: "DELETE", Path: one, Handle: h.remove, Reply: DoneReply{},
			Summary: "Remove a connector, its switches in every chat and the policies for its tools. Its token stays in the vault."},
		{Method: "POST", Path: one + "/check", Handle: h.check, Reply: Row{},
			Summary: "Reach a connector now and keep the tools it lists, or why it could not be reached."},
		{Method: "GET", Path: menu, Handle: h.chatList, Reply: ChatReply{},
			Summary: "The chat's tools menu: each connector, whether it is on in Settings, and whether its tools are offered in this chat."},
		{Method: "PUT", Path: menu + "/{name}", Handle: h.chatSet, Body: SwitchBody{}, Reply: ChatReply{},
			Summary: "Switch one connector on or off in one chat. A connector that is off in Settings stays off."},
	}
}

type handlers struct{ o Options }

func failed(w http.ResponseWriter, r *http.Request) {
	api.Error(w, r, http.StatusInternalServerError, "failed", "the call failed")
}

// refuse answers an error from the record.
func refuse(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		api.Error(w, r, http.StatusNotFound, "not_found", "there is no connector of that name")
	case errors.Is(err, ErrExists):
		api.Error(w, r, http.StatusConflict, "exists", "a connector of that name is already there")
	case errors.Is(err, ErrFull):
		api.Error(w, r, http.StatusConflict, "full", "you have the most connectors one person may have")
	case errors.Is(err, ErrBad):
		api.Error(w, r, http.StatusBadRequest, "bad_connector", err.Error())
	default:
		failed(w, r)
	}
}

func row(k Kept, stored map[string]bool) Row {
	env := []string{}
	for name := range k.Env {
		env = append(env, name)
	}
	sort.Strings(env)
	args := k.Args
	if args == nil {
		args = []string{}
	}
	tools := k.Tools
	if tools == nil {
		tools = []Tool{}
	}
	return Row{
		Name: k.Name, Label: k.Label, Transport: k.Transport, Command: k.Command, Args: args, EnvNames: env, TokenEnv: k.TokenEnv,
		URL: k.URL, Local: k.Local, Secret: k.Secret, TokenPresent: k.Secret != "" && stored[k.Secret],
		Enabled: k.Enabled, Outside: k.Outside, AddedBy: k.AddedBy, Created: k.Created, Updated: k.Updated, Listed: k.Listed,
		Problem: k.Problem, Tools: tools,
	}
}

func storedSecrets(s *tenant.Store) map[string]bool {
	out := map[string]bool{}
	names, err := s.Secrets().Names()
	if err != nil {
		return out
	}
	for _, n := range names {
		out[n] = true
	}
	return out
}

func (h handlers) list(w http.ResponseWriter, r *http.Request, c api.Call) {
	kept, err := List(r.Context(), c.Store)
	if err != nil {
		failed(w, r)
		return
	}
	stored := storedSecrets(c.Store)
	reply := ListReply{Connectors: []Row{}, Max: h.o.Max, Transports: []string{TransportHTTP, TransportStdio}}
	for _, k := range kept {
		reply.Connectors = append(reply.Connectors, row(k, stored))
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

func (b Body) runsCommand() bool {
	return b.Transport == TransportStdio || b.Command != "" || len(b.Args) > 0 || len(b.Env) > 0 || b.TokenEnv != ""
}

func orTrue(b *bool) bool { return b == nil || *b }

func (h handlers) add(w http.ResponseWriter, r *http.Request, c api.Call) {
	var b Body
	if !api.ReadJSON(w, r, &b) {
		return
	}
	if b.runsCommand() {
		api.Error(w, r, http.StatusForbidden, "command_line_only", commandLineOnly)
		return
	}
	k, err := Add(r.Context(), c.Store, Connector{
		Name: b.Name, Label: b.Label, Transport: TransportHTTP, URL: b.URL, Local: b.Local, Secret: b.Secret,
		Enabled: orTrue(b.Enabled), Outside: orTrue(b.Outside),
	}, ByPerson, h.o.Max)
	if err != nil {
		refuse(w, r, err)
		return
	}
	if orTrue(b.Check) {
		if k, err = h.o.Check(r.Context(), c.Store, k.Name); err != nil {
			failed(w, r)
			return
		}
	}
	api.WriteJSON(w, http.StatusCreated, row(k, storedSecrets(c.Store)))
}

func (h handlers) replace(w http.ResponseWriter, r *http.Request, c api.Call) {
	name := r.PathValue("name")
	old, err := Get(r.Context(), c.Store, name)
	if err != nil {
		refuse(w, r, err)
		return
	}
	var b Body
	if !api.ReadJSON(w, r, &b) {
		return
	}
	if b.Name != "" && b.Name != name {
		api.Error(w, r, http.StatusBadRequest, "bad_connector", "the name in the body is not the one in the address: a connector is renamed by removing it and adding it again")
		return
	}
	next := old.Connector
	next.Label, next.Secret = b.Label, b.Secret
	if b.Enabled != nil {
		next.Enabled = *b.Enabled
	}
	if b.Outside != nil {
		next.Outside = *b.Outside
	}
	switch {
	case old.Transport == TransportStdio:
		// Only what cannot run a program may change.
		if (b.Transport != "" && b.Transport != TransportStdio) || b.Command != "" || len(b.Args) > 0 || len(b.Env) > 0 || b.TokenEnv != "" || b.URL != "" || b.Local {
			api.Error(w, r, http.StatusForbidden, "command_line_only", commandLineOnly)
			return
		}
	case b.runsCommand():
		api.Error(w, r, http.StatusForbidden, "command_line_only", commandLineOnly)
		return
	default:
		next.URL, next.Local = b.URL, b.Local
	}
	k, err := Replace(r.Context(), c.Store, next)
	if err != nil {
		refuse(w, r, err)
		return
	}
	if orTrue(b.Check) && k.Enabled && (k.Listed.IsZero() || k.URL != old.URL || k.Secret != old.Secret) {
		if k, err = h.o.Check(r.Context(), c.Store, k.Name); err != nil {
			failed(w, r)
			return
		}
	}
	api.WriteJSON(w, http.StatusOK, row(k, storedSecrets(c.Store)))
}

func (h handlers) remove(w http.ResponseWriter, r *http.Request, c api.Call) {
	if err := Remove(r.Context(), c.Store, r.PathValue("name")); err != nil {
		refuse(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, DoneReply{Removed: true})
}

func (h handlers) check(w http.ResponseWriter, r *http.Request, c api.Call) {
	k, err := h.o.Check(r.Context(), c.Store, r.PathValue("name"))
	if err != nil {
		refuse(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, row(k, storedSecrets(c.Store)))
}

// menu answers with a chat's tools menu.
func menu(w http.ResponseWriter, r *http.Request, s *tenant.Store, chatID string) {
	kept, err := List(r.Context(), s)
	if err != nil {
		failed(w, r)
		return
	}
	switches, err := Switches(r.Context(), s, chatID)
	if err != nil {
		failed(w, r)
		return
	}
	reply := ChatReply{Connectors: []ChatRow{}}
	for _, k := range kept {
		on, set := switches[k.Name]
		reply.Connectors = append(reply.Connectors, ChatRow{
			Name: k.Name, Label: k.DisplayName(), Enabled: k.Enabled, On: k.Enabled && (!set || on), Switched: set,
			Tools: len(k.Tools), Problem: k.Problem,
		})
	}
	api.WriteJSON(w, http.StatusOK, reply)
}

// chatOf answers 404 when the chat is not in the person's record.
func chatOf(w http.ResponseWriter, r *http.Request, s *tenant.Store) (string, bool) {
	id := r.PathValue("id")
	if _, err := chat.Get(s, id); err != nil {
		if errors.Is(err, chat.ErrNotFound) {
			api.Error(w, r, http.StatusNotFound, "not_found", "there is no chat of that id")
		} else {
			failed(w, r)
		}
		return "", false
	}
	return id, true
}

func (h handlers) chatList(w http.ResponseWriter, r *http.Request, c api.Call) {
	id, ok := chatOf(w, r, c.Store)
	if !ok {
		return
	}
	menu(w, r, c.Store, id)
}

func (h handlers) chatSet(w http.ResponseWriter, r *http.Request, c api.Call) {
	id, ok := chatOf(w, r, c.Store)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if _, err := Get(r.Context(), c.Store, name); err != nil {
		refuse(w, r, err)
		return
	}
	var b SwitchBody
	if !api.ReadJSON(w, r, &b) {
		return
	}
	if err := SetSwitch(r.Context(), c.Store, id, name, b.On); err != nil {
		failed(w, r)
		return
	}
	menu(w, r, c.Store, id)
}
