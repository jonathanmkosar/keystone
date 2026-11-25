package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// keysInTheRegistry is a surface of the "no keys in output" check. It gives
// a tenant a model whose entry names a secret, stores value as that secret,
// and opens the model three ways: with a builder that works, with one that
// fails and puts the key in its error, and with the key pasted into the
// entry where its name belongs. It returns every error, the registry as it
// lists, and every file in the data folder.
func keysInTheRegistry(_, dir, value string) ([]keyOutput, error) {
	data := filepath.Join(dir, "data")
	in, err := tenant.OpenWithKeeper(data, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		return nil, err
	}
	s, err := in.Store(made.ID)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	ctx := context.Background()

	m := models.Model{
		Name: "example-large", Label: "Example Large", Adapter: models.AdapterChatCompletions,
		Endpoint: "https://api.provider-one.example/v1", ProviderModel: "example-large-1", Key: keyCheckName,
		ContextWindow: 200000, OutputLimit: 16000, Class: models.ClassHosted,
	}
	if err := models.Add(ctx, s, models.Limits{MaxModels: 5}, m); err != nil {
		return nil, err
	}
	if err := s.Secrets().Set(keyCheckName, value); err != nil {
		return nil, err
	}

	var out []keyOutput
	// The check is worth nothing unless the key really reaches a builder.
	reached := false
	works := adapters.Set{m.Adapter: func(_ models.Model, key string) (seam.Provider, error) {
		reached = key == value
		return nil, nil
	}}
	_, _, err = adapters.Open(ctx, s, works, m.Name)
	if !reached {
		return nil, fmt.Errorf("the key did not reach the adapter's builder: %v", err)
	}
	if err != nil {
		out = append(out, keyOutput{"the error for a builder that gave no provider", []byte(err.Error())})
	}
	fails := adapters.Set{m.Adapter: func(_ models.Model, key string) (seam.Provider, error) {
		return nil, errors.New("the provider refused " + key)
	}}
	if _, _, err := adapters.Open(ctx, s, fails, m.Name); err != nil {
		out = append(out, keyOutput{"the error for a builder that failed", []byte(err.Error())})
	}

	// A key pasted where its name belongs, and into the address.
	pasted := m
	pasted.Name, pasted.Key = "pasted-key", value
	if err := models.Add(ctx, s, models.Limits{MaxModels: 5}, pasted); err != nil {
		out = append(out, keyOutput{"the refusal of a key pasted into key", []byte(err.Error())})
	}
	pasted.Key, pasted.Endpoint = "", "https://api.provider-one.example/v1?key="+value
	if err := models.Add(ctx, s, models.Limits{MaxModels: 5}, pasted); err != nil {
		out = append(out, keyOutput{"the refusal of a key pasted into the endpoint", []byte(err.Error())})
	}

	listed, err := models.List(ctx, s)
	if err != nil {
		return out, err
	}
	raw, err := json.Marshal(listed)
	if err != nil {
		return out, err
	}
	out = append(out, keyOutput{"the registry as listed", raw})

	files, err := folderFiles(data)
	return append(out, files...), err
}
