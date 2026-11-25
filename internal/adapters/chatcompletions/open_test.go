package chatcompletions_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// The whole path: an entry in a tenant's registry, its key in the tenant's
// vault, the builder from the settings registry, and a call that answers.
func TestAModelInTheRegistryIsOpenedAndAnswers(t *testing.T) {
	const stored = "an invented value, not a real key"
	t.Setenv("PROVIDER_LOCAL", "a value from the environment")
	sent := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"Here."},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	made, err := in.Create("Test Person One")
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(made.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	set, err := adapters.ChatCompletions(reg)
	if err != nil {
		t.Fatal(err)
	}
	if len(set) != 1 || set[models.AdapterChatCompletions] == nil {
		t.Fatalf("the set does not hold the %s builder alone", models.AdapterChatCompletions)
	}
	m := models.Model{
		Name: "house-small", Label: "House small", Adapter: models.AdapterChatCompletions,
		Endpoint: srv.URL + "/v1", ProviderModel: "example-small:8b", Key: "provider.local",
		ContextWindow: 8192, OutputLimit: 2048, Class: models.ClassLocal,
	}
	if err := models.Add(ctx, s, models.Limits{MaxModels: 5}, m); err != nil {
		t.Fatal(err)
	}
	if err := s.Secrets().Set(m.Key, stored); err != nil {
		t.Fatal(err)
	}
	opened, p, err := adapters.Open(ctx, s, set, m.Name)
	if err != nil {
		t.Fatal(err)
	}
	got, err := seam.Call(ctx, p, seam.Request{Model: opened.Name, Messages: []seam.Message{
		{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Hello."}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if seam.Text(got.Message) != "Here." || got.Model != "house-small" || got.Usage.Total() != 6 {
		t.Errorf("got %+v", got)
	}
	if sent != "Bearer "+stored {
		t.Error("the key from the vault did not reach the endpoint as it is")
	}

	// An entry that names no key sends no Authorization header.
	m.Name, m.Key = "house-open", ""
	if err := models.Add(ctx, s, models.Limits{MaxModels: 5}, m); err != nil {
		t.Fatal(err)
	}
	_, p, err = adapters.Open(ctx, s, set, m.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seam.Call(ctx, p, seam.Request{Messages: []seam.Message{
		{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Hello."}}},
	}}); err != nil {
		t.Fatal(err)
	}
	if sent != "" {
		t.Error("an entry with no key sent an Authorization header")
	}
}

func TestTheSettingsGiveTheAdapterItsOptions(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	opts, err := adapters.ChatCompletionsOptions(reg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.ConnectTimeout <= 0 || opts.ResponseTimeout <= 0 || opts.IdleTimeout <= 0 ||
		opts.MaxAttempts < 1 || opts.RetryBase <= 0 || opts.RetryMax < opts.RetryBase {
		t.Errorf("the options from the settings are not usable: %+v", opts)
	}
	if _, err := adapters.ChatCompletionsOptions(nil); err == nil {
		t.Error("a registry with none of the settings gave options")
	}
}
