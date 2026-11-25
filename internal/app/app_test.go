package app

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// The committed document is the contract the interface builds against. It
// must be what the routes say. To bring it up to date:
//
//	go run ./cmd/keystone openapi --out docs/api/openapi.json
func TestTheCommittedDocumentMatchesTheRoutes(t *testing.T) {
	want, err := OpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile(filepath.Join("..", "..", "docs", "api", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may hold the file with its own line ends.
	have = bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(have, want) {
		t.Errorf("docs/api/openapi.json is not what the routes say. Run:\n\tgo run ./cmd/keystone openapi --out docs/api/openapi.json")
	}
}

func TestTheShippedSettingsMakeAHandler(t *testing.T) {
	c, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxBodyBytes < 1 || c.Heartbeat <= 0 || c.PageLimit < 1 || c.SignIn.SessionLifetime <= 0 {
		t.Errorf("the shipped settings read as %+v", c)
	}
	if _, err := Handler(c); err == nil {
		t.Errorf("a handler was made with no data folder")
	}
	if c.Install, err = tenant.Open(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := Handler(c); err != nil {
		t.Error(err)
	}
	if err := api.Check(Routes(c)); err != nil {
		t.Error(err)
	}
}

// shipped returns a setting's value as the registry ships it.
func shipped(t *testing.T, key string) (string, bool) {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := settings.Lookup(reg, key)
	return s.Default, ok
}

func TestASettingThatIsWrongIsRefused(t *testing.T) {
	for key, bad := range map[string]string{
		"server.max_body_bytes":   "0",
		"server.stream_heartbeat": "0s",
		"server.page_limit":       "none",
		"signin.list_people":      "perhaps",
		"server.secure_cookie":    "",
	} {
		_, err := ReadConfig(func(k string) (string, bool) {
			if k == key {
				return bad, true
			}
			return shipped(t, k)
		})
		if err == nil {
			t.Errorf("%s = %q was accepted", key, bad)
		}
	}
	if _, err := ReadConfig(func(k string) (string, bool) {
		if k == "server.page_limit" {
			return "", false
		}
		return shipped(t, k)
	}); err == nil {
		t.Errorf("a missing setting was accepted")
	}
}
