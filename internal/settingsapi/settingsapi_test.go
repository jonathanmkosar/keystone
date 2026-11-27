package settingsapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// Invented, for throwaway folders only.
const (
	phraseOne = "settings person one's phrase"
	phraseTwo = "settings person two's phrase"
)

// rig is the routes served on a data folder of the test's own, with two
// people signed in.
type rig struct {
	t        *testing.T
	h        http.Handler
	install  *tenant.Install
	one, two string // tenant ids
	tokens   map[string]string
	// said is every answer's body, so a test can look through all of them.
	said []string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	in, err := tenant.OpenWithKeeper(t.TempDir(), secrets.NewMemoryKeeper())
	if err != nil {
		t.Fatal(err)
	}
	o, err := signin.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	g := &rig{t: t, install: in, tokens: map[string]string{}}
	for i, person := range []struct{ name, phrase string }{{"Test Person One", phraseOne}, {"Test Person Two", phraseTwo}} {
		made, err := in.Create(person.name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := in.Store(made.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := signin.SetPassword(s, person.phrase, o); err != nil {
			t.Fatal(err)
		}
		token, _, err := signin.SignIn(s, person.phrase, o)
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
		g.tokens[made.ID] = token
		if i == 0 {
			g.one = made.ID
		} else {
			g.two = made.ID
		}
	}
	g.h, err = api.Handler(api.Options{Install: in, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, Routes())
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// call makes one call as a tenant and reads the answer into out, when out
// is not nil and the call succeeded.
func (g *rig) call(who, method, path string, body, out any) *httptest.ResponseRecorder {
	g.t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, api.Prefix+path, nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			g.t.Fatal(err)
		}
		r = httptest.NewRequest(method, api.Prefix+path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
	}
	r.AddCookie(&http.Cookie{Name: api.CookieName, Value: g.tokens[who]})
	rec := httptest.NewRecorder()
	g.h.ServeHTTP(rec, r)
	g.said = append(g.said, rec.Body.String())
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			g.t.Fatalf("%s %s: the answer does not read: %v\n%s", method, path, err, rec.Body)
		}
	}
	return rec
}

// want fails the test when a call did not answer with the status and, for
// a refusal, the code.
func (g *rig) want(rec *httptest.ResponseRecorder, status int, code string) {
	g.t.Helper()
	var e api.ErrorBody
	json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != status || (code != "" && e.Code != code) {
		g.t.Fatalf("answered %d %q, want %d %q: %s", rec.Code, e.Code, status, code, rec.Body)
	}
}

// controls are the types the General screen has a control for. A setting
// of another type would be listed with nothing to change it with.
var controls = []string{"bool", "int", "float", "string", "duration"}

// TestEveryOverridableSettingHasAControl is the proof of P11-01. The
// General screen draws one control for each setting the settings route
// marks changeable, from the route's own data, so the proof is about the
// route: every setting in the registry is listed, every one whose scope
// lets a person change it is marked changeable, is of a type the screen has
// a control for, and can in fact be stored and reset through the routes;
// no other setting is; and no fixed rule is a setting or can be written.
func TestEveryOverridableSettingHasAControl(t *testing.T) {
	g := newRig(t)
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	var reply SettingsReply
	g.want(g.call(g.one, "GET", "settings", nil, &reply), 200, "")

	listed := map[string]Setting{}
	for _, s := range reply.Settings {
		listed[s.Key] = s
		if settings.IsFixedRule(s.Key) {
			t.Errorf("%s is a fixed rule and is listed as a setting", s.Key)
		}
	}
	if len(listed) != len(reg) {
		t.Errorf("the route lists %d settings and the registry holds %d", len(listed), len(reg))
	}
	overridable := 0
	for _, st := range reg {
		got, ok := listed[st.Key]
		if !ok {
			t.Errorf("%s is in the registry and not in the route's answer", st.Key)
			continue
		}
		if got.Description == "" || got.Type != st.Type || got.Default != st.Default || got.Lane != st.Lane || got.Scope != string(st.Scope) {
			t.Errorf("%s is listed as %+v, and the registry says %+v", st.Key, got, st)
		}
		// P11-04: the screen shows a setting by its title and its short line,
		// and gives the description on request, so the route carries all three.
		if got.Title == "" || got.Summary == "" || got.Title != st.Title || got.Summary != st.Summary || got.Description != st.Description {
			t.Errorf("%s is listed with the title %q and the summary %q, and the registry says %q and %q", st.Key, got.Title, got.Summary, st.Title, st.Summary)
		}
		may := tenantsettings.MayChange(st, tenantsettings.Person) == nil
		if may != settings.TenantMayOverride(st) {
			t.Fatalf("%s: the registry and the tenant settings disagree on whether a person may change it", st.Key)
		}
		if got.Changeable != may {
			t.Errorf("%s: changeable is %v, and the person's right to change it is %v", st.Key, got.Changeable, may)
		}
		path := "settings/" + st.Key
		if !may {
			g.want(g.call(g.one, "PUT", path, ValueBody{Value: st.Default}, nil), 403, "not_allowed")
			g.want(g.call(g.one, "DELETE", path, nil, nil), 403, "not_allowed")
			continue
		}
		overridable++
		if !slices.Contains(controls, st.Type) {
			t.Errorf("%s has the type %s, which the General screen has no control for", st.Key, st.Type)
		}
		// Every value a control can offer is taken: each option, or the default.
		values := st.Options
		if len(values) == 0 {
			values = []string{st.Default}
		}
		for _, value := range values {
			var set Setting
			g.want(g.call(g.one, "PUT", path, ValueBody{Value: value}, &set), 200, "")
			if set.Value != value || !set.Overridden || !set.Changeable {
				t.Errorf("%s set to %q reads back as %+v", st.Key, value, set)
			}
		}
		var cleared Setting
		g.want(g.call(g.one, "DELETE", path, nil, &cleared), 200, "")
		if cleared.Value != st.Default || cleared.Overridden {
			t.Errorf("%s, reset, reads back as %+v", st.Key, cleared)
		}
	}
	if overridable == 0 {
		t.Fatal("the registry has no setting a person may change, so the test proved nothing")
	}

	// The fixed rules: listed apart, word for word, and never written.
	rules := settings.FixedRules()
	if len(reply.Rules) != len(rules) || len(rules) != 4 {
		t.Fatalf("the route lists %d fixed rules, want the four", len(reply.Rules))
	}
	for i, rule := range rules {
		if reply.Rules[i].Key != rule.Key || reply.Rules[i].Rule != rule.Rule {
			t.Errorf("rule %d is listed as %+v", i, reply.Rules[i])
		}
		for _, value := range []string{"false", "off", "0", ""} {
			g.want(g.call(g.one, "PUT", "settings/"+rule.Key, ValueBody{Value: value}, nil), 403, "fixed_rule")
		}
		g.want(g.call(g.one, "DELETE", "settings/"+rule.Key, nil, nil), 403, "fixed_rule")
	}
	g.want(g.call(g.one, "PUT", "settings/rules.made_up", ValueBody{Value: "false"}, nil), 403, "fixed_rule")
	// Nothing was stored for a rule or for a setting that is not the person's.
	s, err := g.install.Store(g.one)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var rows int
	if err := s.DB().QueryRow("SELECT count(*) FROM tenant_settings").Scan(&rows); err != nil || rows != 0 {
		t.Errorf("%d values are stored after every one was reset or refused (%v)", rows, err)
	}
}

func TestSettingsAreThePersonsOwn(t *testing.T) {
	g := newRig(t)
	const key = "appearance.theme"
	var set Setting
	g.want(g.call(g.one, "PUT", "settings/"+key, ValueBody{Value: "light"}, &set), 200, "")
	if set.Value != "light" || set.Updated.IsZero() {
		t.Errorf("set reads back as %+v", set)
	}
	value := func(who string) string {
		var reply SettingsReply
		g.want(g.call(who, "GET", "settings", nil, &reply), 200, "")
		for _, s := range reply.Settings {
			if s.Key == key {
				return s.Value
			}
		}
		t.Fatalf("%s is not listed", key)
		return ""
	}
	if value(g.one) != "light" || value(g.two) != "dark" {
		t.Errorf("person one has %q and person two has %q", value(g.one), value(g.two))
	}

	// A value that does not fit is refused, and is not said back.
	const pasted = "an-invented-value-that-is-not-a-theme"
	rec := g.call(g.one, "PUT", "settings/"+key, ValueBody{Value: pasted}, nil)
	g.want(rec, 400, "bad_value")
	if strings.Contains(rec.Body.String(), pasted) || !strings.Contains(rec.Body.String(), "dark, light, system") {
		t.Errorf("the refusal reads %s", rec.Body)
	}
	g.want(g.call(g.one, "PUT", "settings/chats.search_max_results", ValueBody{Value: "many"}, nil), 400, "bad_value")
	g.want(g.call(g.one, "PUT", "settings/no.such_setting", ValueBody{Value: "1"}, nil), 404, "not_found")
	g.want(g.call(g.one, "DELETE", "settings/no.such_setting", nil, nil), 404, "not_found")
	if value(g.one) != "light" {
		t.Error("a refused change altered the stored value")
	}
}

func TestSecretsGoInAndNeverComeOut(t *testing.T) {
	g := newRig(t)
	const value = "an invented value 5c1d0e77, not a real key"

	var empty SecretsReply
	g.want(g.call(g.one, "GET", "secrets", nil, &empty), 200, "")
	if empty.Secrets == nil || len(empty.Secrets) != 0 {
		t.Errorf("a person with no secrets is listed as %+v", empty)
	}
	var set Secret
	g.want(g.call(g.one, "PUT", "secrets/provider.example", ValueBody{Value: value}, &set), 200, "")
	if set.Name != "provider.example" {
		t.Errorf("the answer names %q", set.Name)
	}
	// The value really is stored, for this person and not the other.
	for who, wantStored := range map[string]bool{g.one: true, g.two: false} {
		s, err := g.install.Store(who)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.Secrets().Get("provider.example")
		s.Close()
		if err != nil || ok != wantStored || (ok && got != value) {
			t.Errorf("tenant %s: stored is %v (%v)", who, ok, err)
		}
	}
	var listed SecretsReply
	g.want(g.call(g.one, "GET", "secrets", nil, &listed), 200, "")
	if len(listed.Secrets) != 1 || listed.Secrets[0].Name != "provider.example" {
		t.Errorf("the list is %+v", listed)
	}
	var other SecretsReply
	g.want(g.call(g.two, "GET", "secrets", nil, &other), 200, "")
	if len(other.Secrets) != 0 {
		t.Errorf("person two sees %+v", other)
	}

	// Refusals, with the value sent wherever it could be put.
	g.want(g.call(g.one, "PUT", "secrets/Not.A.Name", ValueBody{Value: value}, nil), 400, "bad_name")
	g.want(g.call(g.one, "PUT", "secrets/provider.empty", ValueBody{Value: ""}, nil), 400, "bad_value")
	g.want(g.call(g.one, "PUT", "secrets/provider.long", ValueBody{Value: value + strings.Repeat("x", maxSecretBytes)}, nil), 400, "bad_value")
	g.want(g.call(g.one, "PUT", "secrets/provider.example", map[string]string{"value": value, "extra": value}, nil), 400, "bad_request")
	g.want(g.call(g.one, "DELETE", "secrets/provider.absent", nil, nil), 404, "not_found")
	g.want(g.call(g.two, "DELETE", "secrets/provider.example", nil, nil), 404, "not_found")
	g.want(g.call(g.one, "GET", "models", nil, nil), 200, "")
	g.want(g.call(g.one, "GET", "settings", nil, nil), 200, "")
	g.want(g.call(g.one, "GET", "profile", nil, nil), 200, "")

	var done DoneReply
	g.want(g.call(g.one, "DELETE", "secrets/provider.example", nil, &done), 200, "")
	g.want(g.call(g.one, "GET", "secrets", nil, &listed), 200, "")
	if !done.Removed || len(listed.Secrets) != 0 {
		t.Errorf("after the delete the list is %+v", listed)
	}
	for i, body := range g.said {
		if strings.Contains(body, value) {
			t.Errorf("answer %d holds the stored value: %s", i, body)
		}
	}
}

func example() models.Model {
	return models.Model{
		Name: "example-large", Label: "Example Large", Adapter: models.AdapterAnthropic,
		Endpoint: "https://api.provider-one.example", ProviderModel: "example-large-1",
		Key: "provider.one", ContextWindow: 200000, OutputLimit: 16000,
		Capabilities: []models.Capability{models.CapTools}, Class: models.ClassHosted,
	}
}

func TestModelsNameTheirKeyAndSayWhetherItIsStored(t *testing.T) {
	g := newRig(t)
	var reply ModelsReply
	g.want(g.call(g.one, "GET", "models", nil, &reply), 200, "")
	if len(reply.Models) != 0 || len(reply.Adapters) != len(models.Adapters()) || len(reply.Classes) != 3 || reply.Max != 50 {
		t.Errorf("an empty registry is listed as %+v", reply)
	}

	var added Model
	g.want(g.call(g.one, "POST", "models", example(), &added), 201, "")
	if added.Entry.Name != "example-large" || added.KeyPresent || added.MayReadSensitive || added.Updated.IsZero() {
		t.Errorf("the added model reads back as %+v", added)
	}
	g.want(g.call(g.one, "POST", "models", example(), nil), 409, "exists")

	// The key arrives, and the entry says so by name alone.
	const value = "an invented value 0be4a1f2, not a real key"
	g.want(g.call(g.one, "PUT", "secrets/provider.one", ValueBody{Value: value}, nil), 200, "")
	g.want(g.call(g.one, "GET", "models", nil, &reply), 200, "")
	if len(reply.Models) != 1 || !reply.Models[0].KeyPresent || reply.Models[0].Entry.Key != "provider.one" {
		t.Errorf("with the key stored the list is %+v", reply.Models)
	}
	var secretsNow SecretsReply
	g.want(g.call(g.one, "GET", "secrets", nil, &secretsNow), 200, "")
	if len(secretsNow.Secrets) != 1 || !slices.Equal(secretsNow.Secrets[0].UsedBy, []string{"example-large"}) {
		t.Errorf("the secret is listed as %+v", secretsNow.Secrets)
	}

	// Replace: a trusted class may read sensitive content.
	changed := example()
	changed.Class, changed.Label = models.ClassTrusted, "Example Large, trusted"
	var replaced Model
	g.want(g.call(g.one, "PUT", "models/example-large", changed, &replaced), 200, "")
	if replaced.Entry.Label != changed.Label || !replaced.MayReadSensitive || !replaced.KeyPresent {
		t.Errorf("the replaced model reads back as %+v", replaced)
	}
	g.want(g.call(g.one, "PUT", "models/another-name", changed, nil), 400, "bad_model")
	g.want(g.call(g.one, "PUT", "models/not-there", models.Model{}, nil), 400, "bad_model")
	absent := example()
	absent.Name = "not-there"
	g.want(g.call(g.one, "PUT", "models/not-there", absent, nil), 404, "not_found")

	// Refused entries: a key pasted where its name goes, and a local class
	// on an address that is somewhere else. Neither is said back.
	pasted := example()
	pasted.Name, pasted.Key = "pasted", value
	g.want(g.call(g.one, "POST", "models", pasted, nil), 400, "bad_model")
	far := example()
	far.Name, far.Class = "far", models.ClassLocal
	g.want(g.call(g.one, "POST", "models", far, nil), 400, "bad_model")

	// Person two has a registry of their own.
	g.want(g.call(g.two, "GET", "models", nil, &reply), 200, "")
	if len(reply.Models) != 0 {
		t.Errorf("person two sees %+v", reply.Models)
	}
	g.want(g.call(g.two, "DELETE", "models/example-large", nil, nil), 404, "not_found")

	var done DoneReply
	g.want(g.call(g.one, "DELETE", "models/example-large", nil, &done), 200, "")
	g.want(g.call(g.one, "DELETE", "models/example-large", nil, nil), 404, "not_found")
	g.want(g.call(g.one, "GET", "models", nil, &reply), 200, "")
	if !done.Removed || len(reply.Models) != 0 {
		t.Errorf("after the removal the list is %+v", reply.Models)
	}
	for i, body := range g.said {
		if strings.Contains(body, value) {
			t.Errorf("answer %d holds the stored value: %s", i, body)
		}
	}
}

func TestTheRegistryIsFullAtItsLimit(t *testing.T) {
	g := newRig(t)
	var reply ModelsReply
	g.want(g.call(g.one, "GET", "models", nil, &reply), 200, "")
	for i := 0; i < reply.Max; i++ {
		m := example()
		m.Name = "example-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		g.want(g.call(g.one, "POST", "models", m, nil), 201, "")
	}
	g.want(g.call(g.one, "POST", "models", example(), nil), 409, "full")
}

func TestProfile(t *testing.T) {
	g := newRig(t)
	var p Profile
	g.want(g.call(g.one, "GET", "profile", nil, &p), 200, "")
	if p.Text != "" || p.MaxChars != 2000 || !p.Updated.IsZero() {
		t.Errorf("a person with no profile reads as %+v", p)
	}
	g.want(g.call(g.one, "PUT", "profile", ProfileBody{Text: "  I keep bees.\r\nShort answers, please.  "}, &p), 200, "")
	if p.Text != "I keep bees.\nShort answers, please." || p.Updated.IsZero() {
		t.Errorf("the profile was kept as %+v", p)
	}
	g.want(g.call(g.one, "PUT", "profile", ProfileBody{Text: strings.Repeat("a", p.MaxChars+1)}, nil), 400, "too_long")
	g.want(g.call(g.one, "PUT", "profile", ProfileBody{Text: "a\x00b"}, nil), 400, "bad_text")
	var again, other Profile
	g.want(g.call(g.one, "GET", "profile", nil, &again), 200, "")
	g.want(g.call(g.two, "GET", "profile", nil, &other), 200, "")
	if again.Text != p.Text || other.Text != "" {
		t.Errorf("person one reads %q and person two reads %q", again.Text, other.Text)
	}
	g.want(g.call(g.one, "PUT", "profile", ProfileBody{}, &p), 200, "")
	if p.Text != "" {
		t.Errorf("an empty text left %q", p.Text)
	}
}
