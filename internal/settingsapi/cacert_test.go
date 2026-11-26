package settingsapi

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/models"
)

// gateway is a local model behind its own certificate authority, as the
// owner's home gateway is. Invented.
func gateway(name, caCert string) models.Model {
	return models.Model{
		Name: name, Label: "Home Gateway", Adapter: models.AdapterChatCompletions,
		Endpoint: "https://gateway.home.arpa/v1", ProviderModel: "small-1",
		ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal, CACert: caCert,
	}
}

// invented is PEM-shaped text, enough for the registry's check, which does
// not parse it.
const invented = "-----BEGIN CERTIFICATE-----\nTUlJQmludmVudGVkY2VydGlmaWNhdGU=\n-----END CERTIFICATE-----"

// TestACACertPathIsRefusedThroughTheAPI is the isolation drill's finding on
// the model registry (P13-02, decision 0490): a person names a file in
// another person's folder as their model's ca_cert, and the route refuses
// it, so the adapter never reads it. PEM text is taken, and a path set from
// the command line is kept when the entry is changed with the same value.
func TestACACertPathIsRefusedThroughTheAPI(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()

	// A file in person one's folder, as an upload is kept.
	one, err := g.install.Store(g.one)
	if err != nil {
		t.Fatal(err)
	}
	const hash = "5f1e0c3a9d7b2e4f6a8c0e2d4b6f8a1c3e5d7f9b1a3c5e7d9f1b3a5c7e9d1f3b"
	if err := one.Files().WriteFile(hash, []byte("person one's invented upload"), 0o600); err != nil {
		t.Fatal(err)
	}
	theirs := filepath.Join(one.Files().Name(), hash)
	one.Close()

	for _, path := range []string{
		theirs,
		filepath.Join("data", "tenants", g.one, "files", hash),
		filepath.Join("..", g.one, "files", hash),
		`\\files.example\share\ca.pem`,
		filepath.Join(t.TempDir(), "not-there.pem"),
	} {
		for _, who := range []string{g.two, g.one} {
			rec := g.call(who, "POST", "models", gateway("gateway", path), nil)
			g.want(rec, 400, "bad_model")
			if strings.Contains(rec.Body.String(), path) {
				t.Errorf("the refusal says the path back: %s", rec.Body)
			}
		}
	}
	var reply ModelsReply
	g.want(g.call(g.two, "GET", "models", nil, &reply), 200, "")
	if len(reply.Models) != 0 {
		t.Fatalf("a refused entry was stored: %+v", reply.Models)
	}

	// PEM text is the authority itself, and is taken.
	g.want(g.call(g.two, "POST", "models", gateway("gateway", invented), nil), 201, "")
	// And may not be changed to a path.
	g.want(g.call(g.two, "PUT", "models/gateway", gateway("gateway", theirs), nil), 400, "bad_model")

	// A path set from the command line, as tools/tenant does it.
	two, err := g.install.Store(g.two)
	if err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(t.TempDir(), "gateway-ca.pem")
	err = models.Add(ctx, two, models.Limits{MaxModels: 50}, gateway("cli-gateway", own))
	two.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Changed through the API with the same path: kept.
	kept := gateway("cli-gateway", own)
	kept.Label = "Home Gateway, renamed"
	g.want(g.call(g.two, "PUT", "models/cli-gateway", kept, nil), 200, "")
	// With another path: refused, and the stored one stays.
	g.want(g.call(g.two, "PUT", "models/cli-gateway", gateway("cli-gateway", theirs), nil), 400, "bad_model")
	if got := heldCACertOf(t, g, g.two, "cli-gateway"); got != own {
		t.Errorf("after a refused change the entry holds %q", got)
	}
	// Taken off: allowed.
	g.want(g.call(g.two, "PUT", "models/cli-gateway", gateway("cli-gateway", ""), nil), 200, "")
}

func heldCACertOf(t *testing.T, g *rig, who, name string) string {
	t.Helper()
	s, err := g.install.Store(who)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	return heldCACert(context.Background(), s, name)
}
