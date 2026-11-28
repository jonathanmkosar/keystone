package turn

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/router"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// A third invented model in the house, beside houseModel and paidModel.
var houseMedium = models.Model{
	Name: "house-medium", Label: "House Medium", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8080/v1",
	ProviderModel: "medium-1", ContextWindow: 8192, OutputLimit: 1024, Class: models.ClassLocal,
}

// policyRig gives the ordinary person three models that answer, and no
// automatic titles.
func policyRig(t *testing.T) *rig {
	g := newRig(t)
	reply := func(models.Model, string) (seam.Provider, error) { return answers{chat: "A reply."}, nil }
	g.extra = adapters.Set{models.AdapterChatCompletions: reply, models.AdapterAnthropic: reply}
	g.addModels(houseModel, houseMedium, paidModel)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	return g
}

// turnsOf counts a chat's turns.
func (g *rig) turnsOf(tenantID, chatID string) int {
	g.t.Helper()
	n := 0
	g.with(tenantID, func(s *tenant.Store) {
		if err := s.DB().QueryRow("SELECT count(*) FROM turns WHERE chat_id = ?", chatID).Scan(&n); err != nil {
			g.t.Fatal(err)
		}
	})
	return n
}

// refused sends a message and expects the policy's 403 with code.
func (sv *served) refused(tenantID, chatID string, b SendBody, code string) string {
	sv.g.t.Helper()
	body, _ := json.Marshal(b)
	status, answer := sv.statusBody(tenantID, "POST", "chats/"+chatID+"/turns", string(body))
	var e api.ErrorBody
	if err := json.Unmarshal([]byte(answer), &e); err != nil || status != http.StatusForbidden || e.Code != code || e.Error == "" {
		sv.g.t.Fatalf("a message naming %q answered %d %s, want 403 %s", b.Model, status, answer, code)
	}
	return e.Error
}

// The two settings are in the registry with the spec's type, default and
// scope, and the list reads as names.
func TestThePolicySettings(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string][2]string{KeyMaySwitchOff: {"bool", "true"}, KeyAllowedModels: {"string", ""}} {
		st, ok := settings.Lookup(reg, key)
		if !ok || string(st.Type) != want[0] || st.Default != want[1] || st.Scope != settings.ScopeTenantAdmin {
			t.Errorf("%s is %+v", key, st)
		}
	}
	if got := ParseAllowed(" house-small, ,paid-large,house-small "); !reflect.DeepEqual(got, []string{"house-small", "paid-large"}) {
		t.Errorf("the list reads as %q", got)
	}
	if ParseAllowed("") != nil || !(Policy{}).Allows("anything") {
		t.Error("an empty list does not allow every model")
	}
	p := Policy{Allowed: []string{"house-small"}, MaySwitchOff: true, AutoOffered: true}
	if p.Refusal("") != nil || p.Refusal("house-small") != nil || p.Refusal("paid-large").Code != CodeModelNotAllowed || p.AutoRequired() {
		t.Errorf("the policy %+v refuses wrongly", p)
	}
	p.MaySwitchOff = false
	if !p.AutoRequired() || p.Refusal("house-small").Code != CodeAutoRequired {
		t.Errorf("the policy %+v does not require Auto", p)
	}
	p.AutoOffered = false
	if p.AutoRequired() || p.Refusal("house-small") != nil {
		t.Errorf("with no ladder the policy %+v still requires Auto", p)
	}
}

// The item's proof: a person who may not pick a model is refused by the
// server, 403 model_not_allowed, and nothing is queued; the model choice
// greys it; chats.default_model and the chat's last model outside the list
// are passed over; and a turn queued before the admin changed the policy
// is refused when it leaves the queue.
func TestAPersonWhoMayNotPickAModelIsRefused(t *testing.T) {
	g := policyRig(t)
	sv := g.serve(nil)
	id := sv.newChat(g.ordinary.ID)

	// Before the policy: the paid model answers and is the chat's model.
	_, _, end := sv.say(g.ordinary.ID, id, "Hello.", func(b *SendBody) { b.Model = paidModel.Name })
	if end.Status != StatusDone || end.Model != paidModel.Name {
		t.Fatalf("before the policy the turn ended as %+v", end)
	}

	g.adminSet(g.ordinary.ID, KeyAllowedModels, "house-small, house-medium")
	g.set(tenantsettings.Person, KeyDefaultModel, paidModel.Name)

	c := sv.modelChoice(g.ordinary.ID, id)
	if len(c.Models) != 3 || !reflect.DeepEqual(c.Allowed, []string{"house-medium", "house-small"}) || !c.MaySwitchOff || c.Auto {
		t.Fatalf("the model choice is %+v", c)
	}
	// The chat's last model and the default are both outside the list.
	if c.Model == nil || c.Model.Name != "house-medium" || c.Source != SourceFirst {
		t.Errorf("the choice passes over the chat's model and the default to %+v from %s", c.Model, c.Source)
	}
	if fresh := sv.modelChoice(g.ordinary.ID, sv.newChat(g.ordinary.ID)); fresh.Model == nil || fresh.Model.Name != "house-medium" {
		t.Errorf("a new chat's choice ignores chats.default_model outside the list as %+v", fresh.Model)
	}

	before := g.turnsOf(g.ordinary.ID, id)
	why := sv.refused(g.ordinary.ID, id, SendBody{Text: "Again.", Model: paidModel.Name}, CodeModelNotAllowed)
	if !strings.Contains(why, "Paid Large") || !strings.Contains(why, "admin") {
		t.Errorf("the refusal says %q", why)
	}
	// A retry and an edit that name it are refused the same way.
	sv.refused(g.ordinary.ID, id, SendBody{RetryOf: end.ReplyMessageID, Model: paidModel.Name}, CodeModelNotAllowed)
	if n := g.turnsOf(g.ordinary.ID, id); n != before {
		t.Errorf("a refused message queued %d turns", n-before)
	}

	// An allowed model, and no model named: the chat goes on, never on the
	// model outside the list.
	_, _, end = sv.say(g.ordinary.ID, id, "On the small one.", func(b *SendBody) { b.Model = houseModel.Name })
	if end.Status != StatusDone || end.Model != houseModel.Name {
		t.Errorf("an allowed model's turn ended as %+v", end)
	}
	_, _, end = sv.say(g.ordinary.ID, id, "And with none named.", nil)
	if end.Status != StatusDone || end.Model != houseModel.Name {
		t.Errorf("a turn naming no model ended as %+v", end)
	}

	// A turn sent by code, as unattended work sends one, naming the model
	// outside the list: refused when it leaves the queue.
	var sent Turn
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		var err error
		if sent, err = sv.runner.Send(context.Background(), s, id, "req-policy", api.ActorPerson, SendBody{Text: "From a schedule.", Model: paidModel.Name}, Carry{}); err != nil {
			t.Fatal(err)
		}
	})
	resp := sv.request(g.ordinary.ID, "GET", "chats/"+id+"/turns/"+sent.ID+"/events", "", nil)
	defer resp.Body.Close()
	queued := last(t, readEvents(t, resp.Body, nil))
	if queued.Status != StatusFailed || queued.Error == nil || queued.Error.Code != CodeModelNotAllowed {
		t.Errorf("a queued turn naming a model outside the list ended as %+v", queued)
	}

	// With nothing allowed, the choice says why.
	g.adminSet(g.ordinary.ID, KeyAllowedModels, "not-in-the-registry")
	if c := sv.modelChoice(g.ordinary.ID, id); c.Model != nil || len(c.Allowed) != 0 || !strings.Contains(c.Reason, "admin") {
		t.Errorf("with nothing allowed the choice is %+v", c)
	}
}

// The item's proof: a person who may not leave Auto is refused by the
// server, 403 auto_required; their chats are on Auto, new and old; a
// stronger model still works whatever the allowed list says, since Auto
// uses the ladder; and with no ladder there is no Auto to stay on.
func TestAPersonWhoMayNotLeaveAutoIsRefused(t *testing.T) {
	g := newRig(t)
	g.recordedLadder()
	sv := g.serveAuto()
	g.adminSet(g.test.ID, KeyAutoDefault, "false")
	old := sv.newChat(g.test.ID)
	if c := sv.modelChoice(g.test.ID, old); c.Auto || !c.MaySwitchOff {
		t.Fatalf("with router.auto_default off a new chat's choice is %+v", c)
	}

	g.adminSet(g.test.ID, KeyMaySwitchOff, "false")
	g.adminSet(g.test.ID, KeyAllowedModels, TestSmallModel) // leaves out the test tenant's model and the upper steps
	made := sv.newChat(g.test.ID)
	for _, id := range []string{made, old} {
		if c := sv.modelChoice(g.test.ID, id); !c.Auto || c.MaySwitchOff || len(c.Ladder) != 3 {
			t.Errorf("chat %s's choice is %+v", id, c)
		}
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		if on, err := ChatAuto(context.Background(), s, made); err != nil || !on {
			t.Errorf("a chat made while Auto is required is stored off Auto (%v)", err)
		}
	})

	before := g.turnsOf(g.test.ID, made)
	why := sv.refused(g.test.ID, made, SendBody{Text: autoEasy, Model: TestModelName}, CodeAutoRequired)
	if !strings.Contains(why, "Auto") {
		t.Errorf("the refusal says %q", why)
	}
	sv.refused(g.test.ID, made, SendBody{Text: autoEasy, Model: TestSmallModel}, CodeAutoRequired)
	if n := g.turnsOf(g.test.ID, made); n != before {
		t.Errorf("a refused message queued %d turns", n-before)
	}

	// The chat off Auto is put on it by its next message.
	_, route, end := sv.say(g.test.ID, old, autoHard, nil)
	if end.Status != StatusDone || !route.Auto || end.Model != TestLargeModel {
		t.Errorf("the old chat's message ended as %+v, routed %+v", end, route)
	}
	g.with(g.test.ID, func(s *tenant.Store) {
		if on, err := ChatAuto(context.Background(), s, old); err != nil || !on {
			t.Errorf("the old chat is still off Auto (%v)", err)
		}
	})

	// Try a stronger model stays on Auto, on the ladder, though the upper
	// steps are not allowed by hand.
	_, _, small := sv.say(g.test.ID, made, autoEasy, nil)
	resp := sv.request(g.test.ID, "POST", "chats/"+made+"/turns/"+small.ID+"/stronger", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a stronger model answered %d", resp.StatusCode)
	}
	if stronger := last(t, readEvents(t, resp.Body, nil)); stronger.Status != StatusDone || stronger.Model != TestMiddleModel {
		t.Errorf("the stronger turn ended as %+v", stronger)
	}

	// May switch off again: a model outside the list is now refused as
	// not allowed, and one in it takes the chat off Auto.
	g.adminSet(g.test.ID, KeyMaySwitchOff, "true")
	sv.refused(g.test.ID, made, SendBody{Text: autoEasy, Model: TestModelName}, CodeModelNotAllowed)

	// With no ladder there is no Auto to stay on: nothing is required.
	g.adminSet(g.test.ID, KeyMaySwitchOff, "false")
	g.adminSet(g.test.ID, KeyAllowedModels, "")
	g.adminSet(g.test.ID, router.KeyLadder, "")
	if c := sv.modelChoice(g.test.ID, made); c.Auto || !c.MaySwitchOff {
		t.Errorf("with no ladder the choice is %+v", c)
	}
	if _, _, end := sv.say(g.test.ID, made, "Thank you.", func(b *SendBody) { b.Model = TestModelName }); end.Status != StatusDone {
		t.Errorf("with no ladder a picked model's turn ended as %+v", end)
	}
}
