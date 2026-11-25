package adapters

import (
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters/responses"
	"github.com/jonathanmkosar/keystone/internal/models"
)

// The calls themselves are proven in the adapter's own folder, which may
// use the network package. Here it is only the builder.
func TestResponsesBuilder(t *testing.T) {
	opts, err := responses.DefaultOptions()
	if err != nil {
		t.Fatal(err)
	}
	set := Set{models.AdapterResponses: Responses(opts)}
	m := models.Model{
		Name: "example-private", Label: "Example Private", Adapter: models.AdapterResponses,
		Endpoint: "https://private.provider-two.example/v1", ProviderModel: "example-private-2",
		Key: "provider.two", ContextWindow: 128000, OutputLimit: 8000, Class: models.ClassTrusted,
	}
	if err := models.Check(m); err != nil {
		t.Fatal(err)
	}
	const key = "invented-key-for-the-builder-0001"
	p, err := set[m.Adapter](m, key)
	if err != nil || p == nil {
		t.Fatalf("the builder gave %v and %v", p, err)
	}

	m.Endpoint = ""
	p, err = set[m.Adapter](m, key)
	if err == nil || p != nil {
		t.Fatalf("an entry with no endpoint was built: %v", p)
	}
	if strings.Contains(err.Error(), key) {
		t.Error("the builder's error holds the key")
	}
}
