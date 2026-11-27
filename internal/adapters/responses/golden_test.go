package responses

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// goldenPath holds the exact bytes the adapter sent for every recorded
// request, written before effort was added to the seam (P14-01). A request
// with no effort must still be sent as those bytes, not only as the same
// JSON.
var goldenPath = filepath.Join("..", "..", "..", "fixtures", "responses-adapter", "golden", "bodies.json")

type goldenFile struct {
	TestData bool              `json:"test_data"`
	About    string            `json:"about"`
	Bodies   map[string]string `json:"bodies"`
}

// TestARequestWithNoEffortIsSentAsBefore compares, byte for byte, the body
// of every recorded request that asks for no effort with the body written
// down before effort existed. KEYSTONE_WRITE_GOLDEN=1 writes the file
// again, for a change to the wire that is meant.
func TestARequestWithNoEffortIsSentAsBefore(t *testing.T) {
	got := map[string]string{}
	for _, c := range loadCases(t) {
		if c.Request.Effort != "" {
			continue
		}
		p, err := New(entry("http://127.0.0.1:1/v1", c.Capabilities...), testKey, testOptions)
		if err != nil {
			t.Fatal(err)
		}
		body, err := p.body(c.Request)
		if err != nil {
			continue // a request the adapter refuses sends nothing
		}
		got[c.Name] = string(body)
	}
	if os.Getenv("KEYSTONE_WRITE_GOLDEN") == "1" {
		writeGolden(t, goldenPath, got)
		return
	}
	want := readGolden(t, goldenPath)
	if len(want) < 10 {
		t.Fatalf("the golden file holds only %d bodies", len(want))
	}
	for _, name := range sortedKeys(want) {
		body, ok := got[name]
		if !ok {
			t.Errorf("%s: the golden file has a body for it, and no recorded request with no effort builds one", name)
			continue
		}
		if body != want[name] {
			t.Errorf("%s: the body is no longer the same bytes\nnow:    %s\nbefore: %s", name, body, want[name])
		}
	}
}

func readGolden(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f goldenFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if !f.TestData {
		t.Fatalf("%s is not marked as test data", path)
	}
	return f.Bodies
}

func writeGolden(t *testing.T, path string, bodies map[string]string) {
	t.Helper()
	raw, err := json.MarshalIndent(goldenFile{
		TestData: true,
		About:    "The exact body the adapter sends for each recorded request with no effort, by case name. Written by TestARequestWithNoEffortIsSentAsBefore with KEYSTONE_WRITE_GOLDEN=1.",
		Bodies:   bodies,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d bodies to %s", len(bodies), path)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
