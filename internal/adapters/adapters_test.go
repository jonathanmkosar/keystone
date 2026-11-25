package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

func store(t *testing.T) *tenant.Store {
	t.Helper()
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
	t.Cleanup(func() { s.Close() })
	return s
}

func limits(t *testing.T) models.Limits {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	lim, err := Limits(reg)
	if err != nil {
		t.Fatal(err)
	}
	return lim
}

var (
	scripted = models.Model{Name: "scripted", Label: "Scripted model", Adapter: models.AdapterFake,
		ContextWindow: 32000, OutputLimit: 4000, Class: models.ClassLocal}
	hosted = models.Model{Name: "example-large", Label: "Example Large", Adapter: models.AdapterChatCompletions,
		Endpoint: "https://api.provider-one.example/v1", ProviderModel: "example-large-1", Key: "provider.one",
		ContextWindow: 200000, OutputLimit: 16000, Class: models.ClassHosted}
)

func fakeSet(t *testing.T) Set {
	t.Helper()
	scripts, err := fake.Load(os.DirFS(filepath.Join("..", "..", "fixtures", "fake-provider")))
	if err != nil {
		t.Fatal(err)
	}
	set, err := Fake(scripts, fake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestTheLimitComesFromTheSettingsRegistry(t *testing.T) {
	if lim := limits(t); lim.MaxModels != 50 {
		t.Errorf("models.max_per_tenant reads as %d", lim.MaxModels)
	}
	if _, err := Limits(nil); err == nil {
		t.Error("a registry without the setting gave a limit")
	}
	if _, err := Limits([]settings.Setting{{Key: "models.max_per_tenant", Default: "0"}}); err == nil {
		t.Error("a limit of zero was accepted")
	}
}

// The item's proof: a model added to a tenant's registry can be called,
// with no change to code.
func TestAModelAddedToTheRegistryCanBeCalled(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	set := fakeSet(t)
	if _, _, err := Open(ctx, s, set, scripted.Name); !errors.Is(err, models.ErrUnknown) {
		t.Fatalf("before it is added: %v", err)
	}
	if err := models.Add(ctx, s, limits(t), scripted); err != nil {
		t.Fatal(err)
	}
	m, p, err := Open(ctx, s, set, scripted.Name)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != scripted.Name || !m.MayReadSensitive() {
		t.Errorf("the entry came back as %+v", m)
	}
	reply, err := seam.Call(ctx, p, seam.Request{Model: m.Name, Messages: []seam.Message{
		{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Is anyone there?"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Model != m.Name || seam.Text(reply.Message) == "" {
		t.Errorf("the reply: %+v", reply)
	}
}

func TestAnAdapterThatIsNotBuiltIsRefusedByName(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	if err := models.Add(ctx, s, limits(t), hosted); err != nil {
		t.Fatal(err)
	}
	_, p, err := Open(ctx, s, fakeSet(t), hosted.Name)
	if !errors.Is(err, ErrNotBuilt) || p != nil || !strings.Contains(err.Error(), "chat_completions") {
		t.Errorf("an adapter with no builder: %v", err)
	}
}

func TestTheKeyComesFromTheVaultByName(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	const value = "an invented value, not a real key"
	if err := models.Add(ctx, s, limits(t), hosted); err != nil {
		t.Fatal(err)
	}
	// Somewhere a key must not come from.
	t.Setenv("PROVIDER_ONE_API_KEY", "from the environment")

	var got []string
	set := Set{models.AdapterChatCompletions: func(m models.Model, key string) (seam.Provider, error) {
		got = append(got, key)
		return fakeSet(t)[models.AdapterFake](m, "")
	}}

	_, _, err := Open(ctx, s, set, hosted.Name)
	if !errors.Is(err, ErrNoKey) || !strings.Contains(err.Error(), "provider.one") || len(got) != 0 {
		t.Fatalf("with no secret stored: %v, and the builder was called %d times", err, len(got))
	}
	if err := s.Secrets().Set("provider.one", value); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(ctx, s, set, hosted.Name); err != nil || len(got) != 1 || got[0] != value {
		t.Fatalf("with the secret stored: %v, and the builder got %d keys", err, len(got))
	}

	// A builder that puts the key in its error does not get it out.
	set[models.AdapterChatCompletions] = func(_ models.Model, key string) (seam.Provider, error) {
		return nil, errors.New("could not use " + key)
	}
	_, _, err = Open(ctx, s, set, hosted.Name)
	if err == nil || strings.Contains(err.Error(), value) {
		t.Errorf("the builder's error: %v", err)
	}
	set[models.AdapterChatCompletions] = func(models.Model, string) (seam.Provider, error) { return nil, nil }
	if _, _, err := Open(ctx, s, set, hosted.Name); err == nil {
		t.Error("a builder that gave no provider was accepted")
	}
}
