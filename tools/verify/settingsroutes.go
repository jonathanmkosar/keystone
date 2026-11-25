package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/settingsapi"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// keysInTheSettingsRoutes is the settings routes' part of the "no keys in
// output" check. keysInTheAPI walks every route with a value stored some
// other way. This walk stores the value through the API itself, the way the
// Models and keys screen does, and then uses the routes that could hand it
// back: the list of secrets, the model registry with an entry that names
// the secret, the settings and the profile. It also sends the value where
// it does not belong, as a secret's name, a model's key name and endpoint,
// and a setting's value, and reads the refusals. It returns every answer,
// the server's log and every file in the data folder.
//
// Routes that keep what they are sent because it is not a secret, the
// profile's text and a string setting's value, are not sent the value.
func keysInTheSettingsRoutes(_, dir, value string) ([]keyOutput, error) {
	data := filepath.Join(dir, "data")
	in, err := tenant.OpenWithKeeper(data, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		return nil, err
	}
	store, err := in.Store(made.ID)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	o, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	if err := signin.SetPassword(store, walkPasswordOne, o); err != nil {
		return nil, err
	}
	w, err := newWalker(in)
	if err != nil {
		return nil, err
	}
	token, rec := w.signIn(made.ID, walkPasswordOne)
	if token == "" {
		return nil, fmt.Errorf("the test person cannot sign in through the API: status %d", rec.Code)
	}

	var out []keyOutput
	// send makes one call, keeps its answer, and reports a status that is
	// not the one expected: a walk whose calls all fail proves nothing.
	send := func(what, method, path string, body any, want int) (*httptest.ResponseRecorder, error) {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rec := w.request(method, api.Prefix+path, token, string(raw), nil)
		out = append(out, keyOutput{"the answer to " + what, []byte(rec.Body.String() + fmt.Sprint(rec.Header()))})
		if rec.Code != want {
			return rec, fmt.Errorf("%s answered %d, want %d", what, rec.Code, want)
		}
		return rec, nil
	}

	entry := models.Model{
		Name: "walk-model", Label: "Walk Model", Adapter: models.AdapterAnthropic,
		Endpoint: "https://api.provider-one.example", ProviderModel: "walk-model-1",
		Key: keyCheckName, ContextWindow: 100000, OutputLimit: 8000, Class: models.ClassHosted,
	}
	pastedKey, pastedEndpoint := entry, entry
	pastedKey.Name, pastedKey.Key = "walk-pasted-key", value
	pastedEndpoint.Name, pastedEndpoint.Endpoint = "walk-pasted-endpoint", "https://api.provider-one.example/v1?key="+value

	steps := []struct {
		what, method, path string
		body               any
		want               int
	}{
		{"storing a secret", "PUT", "secrets/" + keyCheckName, settingsapi.ValueBody{Value: value}, http.StatusOK},
		{"storing it again", "PUT", "secrets/" + keyCheckName, settingsapi.ValueBody{Value: value}, http.StatusOK},
		{"the list of secrets", "GET", "secrets", nil, http.StatusOK},
		{"adding a model that names the secret", "POST", "models", entry, http.StatusCreated},
		{"the model registry", "GET", "models", nil, http.StatusOK},
		{"replacing the model", "PUT", "models/" + entry.Name, entry, http.StatusOK},
		{"the list of secrets, with a model using one", "GET", "secrets", nil, http.StatusOK},
		{"a model with the value as its key name", "POST", "models", pastedKey, http.StatusBadRequest},
		{"a model with the value in its endpoint", "POST", "models", pastedEndpoint, http.StatusBadRequest},
		{"a secret with a bad name", "PUT", "secrets/Not.A.Name", settingsapi.ValueBody{Value: value}, http.StatusBadRequest},
		{"a secret with a field the route does not take", "PUT", "secrets/" + keyCheckName, map[string]string{"value": value, "note": value}, http.StatusBadRequest},
		{"a number setting given the value", "PUT", "settings/chats.search_max_results", settingsapi.ValueBody{Value: value}, http.StatusBadRequest},
		{"a setting with options given the value", "PUT", "settings/appearance.theme", settingsapi.ValueBody{Value: value}, http.StatusBadRequest},
		{"a fixed rule given the value", "PUT", "settings/rules.call_recording", settingsapi.ValueBody{Value: value}, http.StatusForbidden},
		{"the settings", "GET", "settings", nil, http.StatusOK},
		{"the profile", "GET", "profile", nil, http.StatusOK},
		{"the audit log", "GET", "audit", nil, http.StatusOK},
	}
	for _, s := range steps {
		if _, err := send(s.what, s.method, s.path, s.body, s.want); err != nil {
			return out, err
		}
	}
	// The check is worth nothing unless the value really is stored.
	if got, ok, err := store.Secrets().Get(keyCheckName); err != nil || !ok || got != value {
		return out, fmt.Errorf("a value was stored through the API and did not come back from the vault: found %v, %v", ok, err)
	}
	listed, err := send("the list of secrets, read for its names", "GET", "secrets", nil, http.StatusOK)
	if err != nil {
		return out, err
	}
	var reply settingsapi.SecretsReply
	if err := json.Unmarshal(listed.Body.Bytes(), &reply); err != nil || len(reply.Secrets) != 1 || reply.Secrets[0].Name != keyCheckName {
		return out, errors.New("the list of secrets does not name the one secret that is stored")
	}

	out = append(out, keyOutput{"the server's log", append([]byte(nil), w.log.Bytes()...)})
	files, err := folderFiles(data)
	if err != nil {
		return out, err
	}
	out = append(out, files...)

	// Deleted, the value is in no file either.
	if _, err := send("deleting the secret", "DELETE", "secrets/"+keyCheckName, nil, http.StatusOK); err != nil {
		return out, err
	}
	return out, nil
}
