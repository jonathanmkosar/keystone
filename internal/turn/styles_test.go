package turn

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/styles"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/settings"
)

// Styles (P9-05). Every style, question and reply here is invented.

// The question the scripted model answers in one format for each style.
const packQuestion = "Name three things to pack for a day at the harbour."

// The replies, one format each, and the marker that picks the bulleted one.
const (
	bulletMark  = "BULLET-TERN"
	plainReply  = "Pack a sun hat, a bottle of water and a light coat."
	bulletReply = "- A sun hat\n- A bottle of water\n- A light coat"
	formalReply = "I would recommend the following three items: a sun hat, a bottle of water, and a light coat."
)

// shippedStyles are the built-in styles as settings/styles.json ships them.
func shippedStyles(t *testing.T) []styles.Style {
	t.Helper()
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := settings.Lookup(reg, styles.SettingBuiltIn)
	if !ok {
		t.Fatalf("%s is not in the registry", styles.SettingBuiltIn)
	}
	b, err := styles.ParseBuiltIn(st.Default)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func shippedStyle(t *testing.T, id string) styles.Style {
	t.Helper()
	for _, st := range shippedStyles(t) {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("no built-in style %q is shipped", id)
	return styles.Style{}
}

// styleScripts answer packQuestion in a format for each style, by what the
// request's system text holds: a plain sentence for a chat with no style,
// a bulleted list when the bullet marker is there, and a formal sentence
// when the shipped Formal style's instructions are there. A script with
// more conditions wins over one with fewer, so the style decides.
func styleScripts(formal string) []fake.Script {
	reply := func(text string) []fake.Step {
		return []fake.Step{{Reply: &seam.Response{
			Message:    seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}},
			StopReason: seam.StopEnd, Usage: seam.Usage{InputTokens: 300, OutputTokens: 20},
		}}}
	}
	return []fake.Script{
		{Name: "packing, no style", When: fake.When{UserText: packQuestion}, Steps: reply(plainReply)},
		{Name: "packing, bulleted", When: fake.When{UserText: packQuestion, SystemContains: bulletMark}, Steps: reply(bulletReply)},
		{Name: "packing, formal", When: fake.When{UserText: packQuestion, SystemContains: formal}, Steps: reply(formalReply)},
	}
}

// serveStyles starts a server as serve does, with the style routes beside
// the chat and turn routes, and the test tenant's recorded turns joined by
// the scripts that answer by style.
func (g *rig) serveStyles() *served {
	g.t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		g.t.Fatal(err)
	}
	cfg.StreamPoll = 50 * time.Millisecond
	formal := shippedStyle(g.t, "formal").Instructions
	provider, err := fake.New(append(styleScripts(formal), g.fx.Scripts...), fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	set, err := adapters.Fake(g.fx.Scripts, fake.Options{DeltaSize: 12})
	if err != nil {
		g.t.Fatal(err)
	}
	for name, build := range g.extra {
		set[name] = build
	}
	runner := NewRunner(Options{Install: g.install, Config: cfg, PageLimit: 50, TestProvider: provider, Adapters: set})
	runner.Recover()
	h, err := api.Handler(api.Options{Install: g.install, MaxBodyBytes: 1 << 20, Heartbeat: time.Minute}, append(Routes(runner), StyleRoutes(runner)...))
	if err != nil {
		g.t.Fatal(err)
	}
	sv := &served{g: g, runner: runner, server: httptest.NewServer(h), tokens: map[string]string{}}
	g.t.Cleanup(sv.stop)
	for id, password := range map[string]string{g.test.ID: g.fx.Password, g.ordinary.ID: ordinaryPassword} {
		g.with(id, func(s *tenant.Store) {
			token, _, err := signin.SignIn(s, password, g.signIn)
			if err != nil {
				g.t.Fatal(err)
			}
			sv.tokens[id] = token
		})
	}
	return sv
}

// newStyle makes a style of the person's own through the route.
func (sv *served) newStyle(tenantID, name, instructions string) styles.Style {
	sv.g.t.Helper()
	body, _ := json.Marshal(StyleBody{Name: &name, Instructions: &instructions})
	var made styles.Style
	sv.call(tenantID, "POST", "styles", string(body), http.StatusCreated, &made)
	return made
}

// chooseStyle gives a chat a style through the route.
func (sv *served) chooseStyle(tenantID, chatID, styleID string) ChatStyleReply {
	sv.g.t.Helper()
	body, _ := json.Marshal(ChatStyleBody{Style: styleID})
	var reply ChatStyleReply
	sv.call(tenantID, "PUT", "chats/"+chatID+"/style", string(body), http.StatusOK, &reply)
	return reply
}

// standing is the standing start of a turn's first chat call: the system
// blocks its first cache point covers.
func standing(t *testing.T, req seam.Request) []string {
	t.Helper()
	if req.Cache == nil {
		t.Fatal("the call has no cache points")
	}
	var out []string
	for _, b := range req.System[:req.Cache.Start] {
		out = append(out, b.Text)
	}
	return out
}

// The item's proof: a chosen style is in the context the model is given, in
// the standing start, and with the model scripted to answer by style, the
// reply's format changes with the style. A chat that keeps its style keeps
// its start byte for byte; changing the style changes the next call's style
// part and nothing before it.
func TestAChosenStyleIsInTheContextAndShapesTheReply(t *testing.T) {
	g := newRig(t)
	sv := g.serveStyles()
	id := sv.newChat(g.test.ID)

	// A new chat uses the shipped default, Normal, which sends nothing.
	var at ChatStyleReply
	sv.call(g.test.ID, "GET", "chats/"+id+"/style", "", http.StatusOK, &at)
	if at.Source != styles.SourceDefault || at.Style == nil || at.Style.ID != "normal" || at.Default != "normal" || len(at.Styles) != len(shippedStyles(t)) {
		t.Fatalf("a new chat's style is %+v", at)
	}
	events, turnID, requestID := sv.send(g.test.ID, id, ask(packQuestion, ""))
	if got := joined(t, events, EventText); got != plainReply {
		t.Fatalf("with no style the reply is %q", got)
	}
	if p := firstCall(t, g, g.test.ID, turnID).Part(contextbuild.PartStyle); p.State != contextbuild.StateEmpty {
		t.Errorf("with Normal the style part is %+v", p)
	}
	_, normalReq := sentSystem(t, g, g.test.ID, requestID)
	// The first turn kept the default as the chat's own.
	sv.call(g.test.ID, "GET", "chats/"+id+"/style", "", http.StatusOK, &at)
	if at.Source != styles.SourceChat || at.Style == nil || at.Style.ID != "normal" {
		t.Errorf("after its first turn the chat's style is %+v", at)
	}

	// The person's own style, chosen for the chat.
	bulleted := sv.newStyle(g.test.ID, "Bulleted", "Answer as a bulleted list, one item to a line, with no sentence before it. Marker: "+bulletMark+".")
	if got := sv.chooseStyle(g.test.ID, id, bulleted.ID); got.Source != styles.SourceChat || got.Style == nil || got.Style.ID != bulleted.ID {
		t.Fatalf("choosing the style answered %+v", got)
	}
	events, turnID, requestID = sv.send(g.test.ID, id, ask(packQuestion, ""))
	if got := joined(t, events, EventText); got != bulletReply {
		t.Fatalf("with the bulleted style the reply is %q", got)
	}
	system, bulletReq := sentSystem(t, g, g.test.ID, requestID)
	start := standing(t, bulletReq)
	if len(start) == 0 || !strings.Contains(start[len(start)-1], bulletMark) || !strings.Contains(start[len(start)-1], `style "Bulleted"`) {
		t.Fatalf("the style is not the last block of the standing start:\n%s", strings.Join(start, "\n---\n"))
	}
	if strings.Count(system, bulletMark) != 1 {
		t.Errorf("the style is sent %d times", strings.Count(system, bulletMark))
	}
	if p := firstCall(t, g, g.test.ID, turnID).Part(contextbuild.PartStyle); p.State != contextbuild.StateKept || p.Tokens == 0 {
		t.Errorf("the record's style part is %+v", p)
	}
	// Everything before the style is as it was with no style.
	if before := standing(t, normalReq); strings.Join(before, "\n") != strings.Join(start[:len(start)-1], "\n") {
		t.Errorf("the standing start before the style changed:\n%q\n%q", before, start[:len(start)-1])
	}

	// The same style again: the start of the call is the same, byte for byte.
	_, _, requestID = sv.send(g.test.ID, id, ask(packQuestion, ""))
	_, again := sentSystem(t, g, g.test.ID, requestID)
	if a, b := strings.Join(standing(t, again), "\n"), strings.Join(start, "\n"); a != b {
		t.Errorf("a chat that kept its style sent another start:\n%q\n%q", a, b)
	}

	// A built-in style: the shipped Formal. The next call carries it in
	// place of the bulleted one, and the reply changes format again.
	formal := shippedStyle(t, "formal")
	sv.chooseStyle(g.test.ID, id, "formal")
	events, turnID, requestID = sv.send(g.test.ID, id, ask(packQuestion, ""))
	if got := joined(t, events, EventText); got != formalReply {
		t.Fatalf("with the formal style the reply is %q", got)
	}
	system, formalReq := sentSystem(t, g, g.test.ID, requestID)
	fstart := standing(t, formalReq)
	if !strings.Contains(fstart[len(fstart)-1], formal.Instructions) || strings.Contains(system, bulletMark) {
		t.Errorf("the formal call's start is:\n%s", strings.Join(fstart, "\n---\n"))
	}
	if a, b := strings.Join(fstart[:len(fstart)-1], "\n"), strings.Join(start[:len(start)-1], "\n"); a != b {
		t.Errorf("changing the style changed the start before it")
	}
	if p := firstCall(t, g, g.test.ID, turnID).Part(contextbuild.PartStyle); p.State != contextbuild.StateKept {
		t.Errorf("the record's style part is %+v", p)
	}

	// The person's own style, changed: the next call carries the new words.
	words := "Answer as a numbered list. Marker: NUMBER-HERON."
	body, _ := json.Marshal(StyleBody{Instructions: &words})
	var changed styles.Style
	sv.call(g.test.ID, "PATCH", "styles/"+bulleted.ID, string(body), http.StatusOK, &changed)
	sv.chooseStyle(g.test.ID, id, bulleted.ID)
	_, _, requestID = sv.send(g.test.ID, id, ask("And for a rainy day?", ""))
	if system, _ := sentSystem(t, g, g.test.ID, requestID); !strings.Contains(system, "NUMBER-HERON") || strings.Contains(system, bulletMark) {
		t.Errorf("after the change the call carries:\n%s", system)
	}
}

// The default style is for new chats: a chat's first turn keeps it, so
// changing the default later leaves that chat as it was.
func TestTheDefaultStyleIsKeptByAChatsFirstTurn(t *testing.T) {
	g := newRig(t)
	sv := g.serveStyles()
	concise := shippedStyle(t, "concise")

	var list StylesReply
	sv.call(g.test.ID, "PUT", "styles/concise/default", "", http.StatusOK, &list)
	if list.Default != "concise" {
		t.Fatalf("making Concise the default answered %+v", list)
	}
	first := sv.newChat(g.test.ID)
	var at ChatStyleReply
	sv.call(g.test.ID, "GET", "chats/"+first+"/style", "", http.StatusOK, &at)
	if at.Source != styles.SourceDefault || at.Style == nil || at.Style.ID != "concise" {
		t.Fatalf("a new chat's style is %+v", at)
	}
	_, _, requestID := sv.send(g.test.ID, first, ask(bakery, ""))
	if system, _ := sentSystem(t, g, g.test.ID, requestID); !strings.Contains(system, concise.Instructions) || !strings.Contains(system, `style "Concise"`) {
		t.Fatalf("a chat on the default Concise is not sent it:\n%s", system)
	}

	sv.call(g.test.ID, "PUT", "styles/normal/default", "", http.StatusOK, &list)
	_, _, requestID = sv.send(g.test.ID, first, ask(bakery, ""))
	if system, _ := sentSystem(t, g, g.test.ID, requestID); !strings.Contains(system, concise.Instructions) {
		t.Errorf("after the default changed, the older chat lost its style:\n%s", system)
	}
	second := sv.newChat(g.test.ID)
	_, turnID, requestID := sv.send(g.test.ID, second, ask(bakery, ""))
	if system, _ := sentSystem(t, g, g.test.ID, requestID); strings.Contains(system, concise.Instructions) {
		t.Errorf("a new chat after the default changed is sent Concise:\n%s", system)
	}
	if p := firstCall(t, g, g.test.ID, turnID).Part(contextbuild.PartStyle); p.State != contextbuild.StateEmpty {
		t.Errorf("a new chat on Normal has the style part %+v", p)
	}

	// A default that names a style of the person's own goes back to the
	// shipped one when the style is deleted, and the chats that used it
	// use the default from their next turn.
	mine := sv.newStyle(g.test.ID, "Haiku", "Answer in three short lines. Marker: HAIKU-WREN.")
	sv.call(g.test.ID, "PUT", "styles/"+mine.ID+"/default", "", http.StatusOK, &list)
	third := sv.newChat(g.test.ID)
	_, _, requestID = sv.send(g.test.ID, third, ask(bakery, ""))
	if system, _ := sentSystem(t, g, g.test.ID, requestID); !strings.Contains(system, "HAIKU-WREN") {
		t.Fatalf("a chat on the person's own default is not sent it:\n%s", system)
	}
	var gone DeletedReply
	sv.call(g.test.ID, "DELETE", "styles/"+mine.ID, "", http.StatusOK, &gone)
	sv.call(g.test.ID, "GET", "styles", "", http.StatusOK, &list)
	if list.Default != "normal" || len(list.Styles) != len(shippedStyles(t)) {
		t.Errorf("after the default style was deleted the list is %+v", list)
	}
	sv.call(g.test.ID, "GET", "chats/"+third+"/style", "", http.StatusOK, &at)
	if at.Source != styles.SourceDefault || at.Style == nil || at.Style.ID != "normal" {
		t.Errorf("a chat whose style was deleted uses %+v", at)
	}
	_, _, requestID = sv.send(g.test.ID, third, ask(bakery, ""))
	if system, _ := sentSystem(t, g, g.test.ID, requestID); strings.Contains(system, "HAIKU-WREN") {
		t.Errorf("a deleted style is still sent:\n%s", system)
	}
}

// Fixed rule 1: another tenant reaches a person's styles on no route, and
// no chat is given another tenant's style.
func TestAnotherTenantsStyleIsUnreachable(t *testing.T) {
	g := newRig(t)
	sv := g.serveStyles()
	mine := sv.newStyle(g.test.ID, "Ledger", "Answer as a table. Marker: LEDGER-AUK.")
	myChat := sv.newChat(g.test.ID)
	theirChat := sv.newChat(g.ordinary.ID)
	choose := func(styleID string) string {
		body, _ := json.Marshal(ChatStyleBody{Style: styleID})
		return string(body)
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "styles/" + mine.ID, ""},
		{"PATCH", "styles/" + mine.ID, `{"name":"Taken"}`},
		{"DELETE", "styles/" + mine.ID, ""},
		{"PUT", "styles/" + mine.ID + "/default", ""},
		{"PUT", "chats/" + theirChat + "/style", choose(mine.ID)},
		{"GET", "chats/" + myChat + "/style", ""},
		{"PUT", "chats/" + myChat + "/style", choose("concise")},
	} {
		resp := sv.request(g.ordinary.ID, c.method, c.path, c.body, nil)
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s as another tenant answered %d: %s", c.method, c.path, resp.StatusCode, raw)
		}
	}
	// The owner cannot give another tenant's chat a style either.
	if code, _ := sv.statusBody(g.test.ID, "PUT", "chats/"+theirChat+"/style", choose("concise")); code != http.StatusNotFound {
		t.Errorf("giving another tenant's chat a style answered %d", code)
	}
	var list StylesReply
	sv.call(g.ordinary.ID, "GET", "styles", "", http.StatusOK, &list)
	for _, st := range list.Styles {
		if !st.BuiltIn {
			t.Errorf("another tenant lists %+v", st)
		}
	}
	var read styles.Style
	sv.call(g.test.ID, "GET", "styles/"+mine.ID, "", http.StatusOK, &read)
	if read.Name != "Ledger" || !strings.Contains(read.Instructions, "LEDGER-AUK") {
		t.Errorf("after another tenant's calls the style reads %+v", read)
	}
	var at ChatStyleReply
	sv.call(g.test.ID, "GET", "chats/"+myChat+"/style", "", http.StatusOK, &at)
	if at.Source != styles.SourceDefault {
		t.Errorf("another tenant's call gave the chat a style: %+v", at)
	}
	var setting string
	g.with(g.test.ID, func(s *tenant.Store) {
		reg, _ := settings.Registry()
		setting, _ = tenantsettings.Get(t.Context(), s, reg, styles.SettingDefault)
	})
	if setting != "normal" {
		t.Errorf("another tenant's call changed the default to %q", setting)
	}
}

// The routes' refusals, and the limit.
func TestTheStyleRoutes(t *testing.T) {
	g := newRig(t)
	g.setFor(g.test.ID, tenantsettings.Person, styles.SettingMaxStyles, "1")
	sv := g.serveStyles()
	id := sv.newChat(g.test.ID)
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "styles", `{}`, http.StatusBadRequest},
		{"POST", "styles", `{"name":"Plain"}`, http.StatusBadRequest},
		{"POST", "styles", `{"name":"  ","instructions":"Words."}`, http.StatusBadRequest},
		{"POST", "styles", `{"name":"Quiet","instructions":"  "}`, http.StatusBadRequest},
		{"POST", "styles", `{"name":"formal","instructions":"Words."}`, http.StatusBadRequest},
		{"POST", "styles", `{"name":"A","instructions":"B","colour":"teal"}`, http.StatusBadRequest},
		{"GET", "styles/nothere", "", http.StatusNotFound},
		{"PATCH", "styles/formal", `{"name":"Mine now"}`, http.StatusBadRequest},
		{"DELETE", "styles/normal", "", http.StatusBadRequest},
		{"PUT", "styles/nothere/default", "", http.StatusNotFound},
		{"PUT", "chats/" + id + "/style", `{}`, http.StatusBadRequest},
		{"PUT", "chats/" + id + "/style", `{"style":"nothere"}`, http.StatusNotFound},
		{"PUT", "chats/nochat/style", `{"style":"formal"}`, http.StatusNotFound},
		{"GET", "chats/nochat/style", "", http.StatusNotFound},
	} {
		if code, body := sv.statusBody(g.test.ID, c.method, c.path, c.body); code != c.want {
			t.Errorf("%s %s %s answered %d, want %d: %s", c.method, c.path, c.body, code, c.want, body)
		}
	}
	mine := sv.newStyle(g.test.ID, "Ledger", "Answer as a table.")
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "styles", `{"name":"Second","instructions":"Words."}`, http.StatusConflict},
		{"PATCH", "styles/" + mine.ID, `{}`, http.StatusBadRequest},
		{"PATCH", "styles/" + mine.ID, `{"name":"Concise"}`, http.StatusBadRequest},
	} {
		if code, body := sv.statusBody(g.test.ID, c.method, c.path, c.body); code != c.want {
			t.Errorf("%s %s %s answered %d, want %d: %s", c.method, c.path, c.body, code, c.want, body)
		}
	}
	var read styles.Style
	sv.call(g.test.ID, "GET", "styles/formal", "", http.StatusOK, &read)
	if !read.BuiltIn || read.Name != "Formal" || read.Instructions == "" {
		t.Errorf("a built-in style reads %+v", read)
	}
	var list StylesReply
	sv.call(g.test.ID, "GET", "styles", "", http.StatusOK, &list)
	if list.Limits.MaxStyles != 1 || list.Limits.NameMaxChars < 1 || len(list.Styles) != len(shippedStyles(t))+1 || list.Styles[len(list.Styles)-1].ID != mine.ID {
		t.Errorf("the list is %+v", list)
	}
}

// The shipped built-in styles read, the shipped default names one of them,
// and Normal sends nothing.
func TestTheShippedStyles(t *testing.T) {
	b := shippedStyles(t)
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	def, _ := settings.Lookup(reg, styles.SettingDefault)
	found := false
	for _, st := range b {
		found = found || st.ID == def.Default
	}
	if !found || len(b) < 2 {
		t.Fatalf("the shipped default %q is not among the built-in styles %+v", def.Default, b)
	}
	if normal := shippedStyle(t, "normal"); styles.Text("{name}", normal) != "" {
		t.Errorf("Normal sends %q", styles.Text("{name}", normal))
	}
	for _, key := range []string{styles.SettingMaxStyles, styles.SettingNameMaxChars, styles.SettingInstructionsMaxChars, styles.SettingHeading} {
		if _, ok := settings.Lookup(reg, key); !ok {
			t.Errorf("%s is not in the registry", key)
		}
	}
}
