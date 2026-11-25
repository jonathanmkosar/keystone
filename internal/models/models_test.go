package models

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// fixture reads one file of fixtures/models and checks it is marked as
// test data. The entries stay as raw JSON, so Decode reads each one as it
// would read any other.
func fixture(t *testing.T, name string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "models", name))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if string(file["test_data"]) != "true" {
		t.Fatalf(`%s is not marked "test_data": true`, name)
	}
	return file
}

func examples(t *testing.T) []Model {
	t.Helper()
	var raws []json.RawMessage
	if err := json.Unmarshal(fixture(t, "examples.json")["models"], &raws); err != nil {
		t.Fatal(err)
	}
	var out []Model
	for i, raw := range raws {
		m, err := Decode(raw)
		if err != nil {
			t.Fatalf("example %d: %v", i+1, err)
		}
		out = append(out, m)
	}
	return out
}

func TestEveryExampleIsAccepted(t *testing.T) {
	adapters, classes := map[string]bool{}, map[Class]bool{}
	for _, m := range examples(t) {
		adapters[m.Adapter], classes[m.Class] = true, true
		raw, err := Encode(m)
		if err != nil {
			t.Errorf("%s: %v", m.Name, err)
			continue
		}
		if again, err := Decode(raw); err != nil || !same(again, m) {
			t.Errorf("%s does not come back the same from JSON: %v", m.Name, err)
		}
	}
	for _, a := range Adapters() {
		if !adapters[a.Name] {
			t.Errorf("no example uses the %s adapter", a.Name)
		}
	}
	for _, c := range []Class{ClassLocal, ClassHosted, ClassTrusted} {
		if !classes[c] {
			t.Errorf("no example is %s", c)
		}
	}
}

func same(a, b Model) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestEveryRefusedEntryIsRefusedWithItsReason(t *testing.T) {
	var cases []struct {
		Why   string          `json:"why"`
		Entry json.RawMessage `json:"entry"`
	}
	if err := json.Unmarshal(fixture(t, "refused.json")["refused"], &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 20 {
		t.Fatalf("refused.json holds %d entries", len(cases))
	}
	for _, c := range cases {
		_, err := Decode(c.Entry)
		switch {
		case err == nil:
			t.Errorf("accepted, and should be refused because %q: %s", c.Why, c.Entry)
		case !errors.Is(err, ErrInvalid):
			t.Errorf("%q: the error is not ErrInvalid: %v", c.Why, err)
		case !strings.Contains(err.Error(), c.Why):
			t.Errorf("refused for the wrong reason:\n  want: %s\n  got:  %v", c.Why, err)
		}
	}
}

func TestARefusalDoesNotRepeatWhatMightBeAKey(t *testing.T) {
	const pasted = "an-invented-value-0123456789"
	base := Model{Name: "m", Label: "M", Adapter: AdapterChatCompletions, ProviderModel: "example", ContextWindow: 10, OutputLimit: 5, Class: ClassHosted}
	for name, change := range map[string]func(*Model){
		"in key":               func(m *Model) { m.Endpoint = "https://api.provider.example"; m.Key = "Key " + pasted },
		"in the address":       func(m *Model) { m.Endpoint = "https://user:" + pasted + "@api.provider.example" },
		"in the query":         func(m *Model) { m.Endpoint = "https://api.provider.example/?key=" + pasted },
		"in a broken address":  func(m *Model) { m.Endpoint = "https://api.provider.example:port" + pasted },
		"in an address, plain": func(m *Model) { m.Endpoint = pasted },
	} {
		m := base
		change(&m)
		err := Check(m)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), pasted) {
			t.Errorf("%s: the refusal repeats the value: %v", name, err)
		}
	}
}

func TestOnlyLocalAndTrustedMayReadSensitiveContent(t *testing.T) {
	for class, want := range map[Class]bool{
		ClassLocal: true, ClassTrusted: true, ClassHosted: false,
		"": false, "Local": false, "safe": false,
	} {
		if got := (Model{Class: class}).MayReadSensitive(); got != want {
			t.Errorf("class %q: MayReadSensitive is %v", class, got)
		}
	}
}

func TestWhichHostsAreOnTheHomeNetwork(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "127.0.0.1": true, "::1": true, "10.0.0.7": true, "192.168.1.20": true,
		"172.16.4.4": true, "169.254.1.1": true, "100.64.0.9": true, "fd00::1": true, "::ffff:192.168.1.2": true,
		"study": true, "study.local": true, "gateway.home.arpa": true, "box.lan": true, "Box.LAN.": true,
		"api.provider.example": false, "8.8.8.8": false, "172.32.0.1": false, "100.128.0.1": false,
		"local.provider.example": false, "notlocal": true, "provider.localish": false, "2001:db8::1": false,
	} {
		if got := onHomeNetwork(host); got != want {
			t.Errorf("%s: on the home network is %v", host, got)
		}
	}
}

func TestCostIsInMillionthsOfADollar(t *testing.T) {
	p := Price{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}
	// A million of each costs the sum of the prices.
	if got := p.CostMicros(1e6, 1e6, 1e6, 1e6); got != 22_050_000 {
		t.Errorf("a million of each: %d", got)
	}
	if got := p.CostMicros(1000, 200, 0, 0); got != 6000 {
		t.Errorf("a small call: %d", got)
	}
	if got := (Price{}).CostMicros(5000, 5000, 5000, 5000); got != 0 {
		t.Errorf("a local model: %d", got)
	}
}

func TestCapabilitiesAreKeptInOneOrder(t *testing.T) {
	m, err := Decode([]byte(`{"name":"m","label":"M","adapter":"fake","context_window":10,"output_limit":5,
		"capabilities":["thinking","tools"],"price":{"input":0,"output":0},"class":"local"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Capabilities) != 2 || m.Capabilities[0] != CapTools || !m.Has(CapThinking) || m.Has(CapVision) {
		t.Errorf("capabilities: %v", m.Capabilities)
	}
}

// open makes an install in a folder of its own with two tenants.
func open(t *testing.T) (dir string, keeper secrets.Keeper, ids [2]string) {
	t.Helper()
	dir, keeper = t.TempDir(), secrets.NewMemoryKeeper()
	in, err := tenant.OpenWithKeeper(dir, keeper)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"Test Person One", "Test Person Two"} {
		made, err := in.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = made.ID
	}
	return dir, keeper, ids
}

func store(t *testing.T, dir string, keeper secrets.Keeper, id string) *tenant.Store {
	t.Helper()
	in, err := tenant.OpenWithKeeper(dir, keeper)
	if err != nil {
		t.Fatal(err)
	}
	s, err := in.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var roomy = Limits{MaxModels: 50}

func TestAddingAModelIsARowAndNoCode(t *testing.T) {
	ctx := context.Background()
	dir, keeper, ids := open(t)
	one := store(t, dir, keeper, ids[0])
	all := examples(t)
	for _, m := range all {
		if err := Add(ctx, one, roomy, m); err != nil {
			t.Fatalf("%s: %v", m.Name, err)
		}
	}
	if err := one.Close(); err != nil {
		t.Fatal(err)
	}

	// The models are there after the database is reopened.
	one = store(t, dir, keeper, ids[0])
	for _, m := range all {
		got, err := Get(ctx, one, m.Name)
		if err != nil || !same(got, m) {
			t.Errorf("%s did not come back the same: %v", m.Name, err)
		}
	}
	listed, err := List(ctx, one)
	if err != nil || len(listed) != len(all) {
		t.Fatalf("listed %d of %d: %v", len(listed), len(all), err)
	}
	for i, e := range listed {
		if e.Problem != "" || e.UpdatedAt.IsZero() || (i > 0 && listed[i-1].Model.Name >= e.Model.Name) {
			t.Errorf("entry %d, %s: problem %q, at %v", i, e.Model.Name, e.Problem, e.UpdatedAt)
		}
	}

	// They are that tenant's and not another's.
	two := store(t, dir, keeper, ids[1])
	if listed, err := List(ctx, two); err != nil || len(listed) != 0 {
		t.Errorf("the other tenant lists %d models: %v", len(listed), err)
	}
	if _, err := Get(ctx, two, all[0].Name); !errors.Is(err, ErrUnknown) {
		t.Errorf("the other tenant can read %s: %v", all[0].Name, err)
	}
}

func TestAddReplaceAndRemove(t *testing.T) {
	ctx := context.Background()
	dir, keeper, ids := open(t)
	s := store(t, dir, keeper, ids[0])
	m := examples(t)[0]

	at := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	now = func() time.Time { return at }
	t.Cleanup(func() { now = time.Now })

	if err := Replace(ctx, s, m); !errors.Is(err, ErrUnknown) {
		t.Errorf("replacing a model that is not there: %v", err)
	}
	if err := Add(ctx, s, roomy, m); err != nil {
		t.Fatal(err)
	}
	if err := Add(ctx, s, roomy, m); !errors.Is(err, ErrExists) {
		t.Errorf("adding the same name twice: %v", err)
	}
	bad := m
	bad.Class = "safe"
	if err := Replace(ctx, s, bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("replacing with an entry that is not valid: %v", err)
	}
	bad.Name = "another"
	if err := Add(ctx, s, roomy, bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("adding an entry that is not valid: %v", err)
	}

	at = at.Add(time.Hour)
	m.Label, m.OutputLimit = "Scripted model, shorter", 1000
	if err := Replace(ctx, s, m); err != nil {
		t.Fatal(err)
	}
	listed, err := List(ctx, s)
	if err != nil || len(listed) != 1 || !same(listed[0].Model, m) || !listed[0].UpdatedAt.Equal(at) {
		t.Errorf("after the change: %+v, %v", listed, err)
	}

	if err := Remove(ctx, s, m.Name); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, s, m.Name); !errors.Is(err, ErrUnknown) {
		t.Errorf("removing it twice: %v", err)
	}
	if _, err := Get(ctx, s, m.Name); !errors.Is(err, ErrUnknown) {
		t.Errorf("reading it after it is removed: %v", err)
	}
}

func TestTheRegistryHoldsNoMoreThanItsLimit(t *testing.T) {
	ctx := context.Background()
	dir, keeper, ids := open(t)
	s := store(t, dir, keeper, ids[0])
	all := examples(t)
	two := Limits{MaxModels: 2}
	for _, m := range all[:2] {
		if err := Add(ctx, s, two, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := Add(ctx, s, two, all[2]); !errors.Is(err, ErrFull) {
		t.Errorf("one more than the limit: %v", err)
	}
	// A name that is already there is reported as that, full or not.
	if err := Add(ctx, s, two, all[0]); !errors.Is(err, ErrExists) {
		t.Errorf("the same name in a full registry: %v", err)
	}
	if err := Add(ctx, s, Limits{}, all[2]); err == nil {
		t.Error("a limit of zero was taken as no limit")
	}
	if got, err := Usable(ctx, s); err != nil || len(got) != 2 {
		t.Errorf("usable: %d, %v", len(got), err)
	}
}

// A row can be written with SQL, and the rules can change after a row is
// stored. Either way the entry is checked when it is read.
func TestAStoredEntryIsCheckedWhenItIsRead(t *testing.T) {
	ctx := context.Background()
	dir, keeper, ids := open(t)
	s := store(t, dir, keeper, ids[0])
	good := examples(t)[0]
	if err := Add(ctx, s, roomy, good); err != nil {
		t.Fatal(err)
	}
	for name, entry := range map[string]string{
		// A hosted address marked local, so that it could read sensitive content.
		"sneaky":   `{"name":"sneaky","label":"Sneaky","adapter":"chat_completions","endpoint":"https://api.provider.example/v1","provider_model":"x","context_window":10,"output_limit":5,"price":{"input":0,"output":0},"class":"local"}`,
		"renamed":  `{"name":"other","label":"Other","adapter":"fake","context_window":10,"output_limit":5,"price":{"input":0,"output":0},"class":"local"}`,
		"not-json": `class: trusted`,
		"extra":    `{"name":"extra","label":"Extra","adapter":"fake","context_window":10,"output_limit":5,"price":{"input":0,"output":0},"class":"local","trusted":true}`,
	} {
		if _, err := s.DB().Exec("INSERT INTO models(name, entry, updated_at) VALUES(?, ?, '2026-11-24T00:00:00Z')", name, entry); err != nil {
			t.Fatal(err)
		}
		m, err := Get(ctx, s, name)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s was read: %v", name, err)
		}
		if m.MayReadSensitive() {
			t.Errorf("%s may read sensitive content", name)
		}
	}
	listed, err := List(ctx, s)
	if err != nil || len(listed) != 5 {
		t.Fatalf("listed %d: %v", len(listed), err)
	}
	for _, e := range listed {
		if (e.Problem == "") != (e.Model.Name == good.Name) {
			t.Errorf("%s: problem %q", e.Model.Name, e.Problem)
		}
		if e.Problem != "" && (e.Model.Class != "" || e.Model.MayReadSensitive()) {
			t.Errorf("%s is refused and still has a class", e.Model.Name)
		}
	}
	usable, err := Usable(ctx, s)
	if err != nil || len(usable) != 1 || usable[0].Name != good.Name {
		t.Errorf("usable: %v, %v", usable, err)
	}
	// A refused entry can be removed.
	if err := Remove(ctx, s, "sneaky"); err != nil {
		t.Error(err)
	}
}
