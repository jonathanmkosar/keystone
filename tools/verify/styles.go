package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/adapters/fake"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/styles"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/settings"
)

// Styles (P9-05): saved presets of tone and format, chosen per chat. The
// package has a doc, keeps its rows inside one tenant's store and reads no
// setting, so it is a guard of fixed rule 1.
func init() {
	componentDocs["internal/styles"] = "docs/architecture/styles.md"
	fixedRuleGuards["rules.tenant_isolation"] = append(fixedRuleGuards["rules.tenant_isolation"], "internal/styles")
}

// The "styles" check (P9-05), the item's proof as a standing check: a
// chosen style is in the context a model is given, and with the model
// scripted to answer by style, the reply's format changes with it.
//
// It runs the real server, with the shipped settings, on a throwaway
// folder, with an invented person and one local model the check answers
// for: the fake provider, scripted to answer one question as a sentence
// with no style, as a bulleted list when the system text holds the
// person's own style, and formally when it holds the shipped Formal
// style. Every request the model is sent is kept. The check proves:
//
//  1. A new chat uses the shipped default, which sends nothing, and gets
//     the sentence.
//  2. With the person's own style chosen, the request's standing start ends
//     with the style, under its heading, the turn's record lists the style
//     part as kept, and the reply is the bulleted list. Asked again, the
//     start is the same, byte for byte.
//  3. With the built-in Formal chosen, the reply is the formal one, and
//     nothing in the standing start before the style changed.
//  4. Fixed rule 1: another person reaches the style on no route, cannot
//     choose a style for the person's chat, and the person cannot give the
//     other's chat a style.
//  5. A style is words and nothing more: internal/styles reaches none of
//     routing, the model registry's classes, recall or the approval rule.

// Invented, for a throwaway folder only.
const (
	stylesPassword      = "an invented phrase for the styles check"
	stylesOtherPassword = "another invented phrase for the styles check"
	stylesQuestion      = "Name three things to pack for a day at the harbour."
	stylesMark          = "BULLET-TERN"
	stylesPlain         = "Pack a sun hat, a bottle of water and a light coat."
	stylesBullets       = "- A sun hat\n- A bottle of water\n- A light coat"
	stylesFormal        = "I would recommend the following three items: a sun hat, a bottle of water, and a light coat."
)

var stylesModel = models.Model{
	Name: "styles-local", Label: "Styles Local", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8083/v1",
	ProviderModel: "small-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
}

// styleEars plays the style scripts and keeps every request.
type styleEars struct {
	mu   sync.Mutex
	p    seam.Provider
	sent []seam.Request
}

func (e *styleEars) build(models.Model, string) (seam.Provider, error) { return e, nil }

func (e *styleEars) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	e.mu.Lock()
	e.sent = append(e.sent, req)
	e.mu.Unlock()
	return e.p.Stream(ctx, req, emit)
}

func (e *styleEars) last() (seam.Request, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.sent) == 0 {
		return seam.Request{}, false
	}
	return e.sent[len(e.sent)-1], true
}

func checkStyles(root string) error {
	problems, err := styleProblems(root)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func styleProblems(root string) ([]string, error) {
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	ctx := context.Background()

	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	shipped, _ := settings.Lookup(reg, styles.SettingBuiltIn)
	builtIn, err := styles.ParseBuiltIn(shipped.Default)
	if err != nil {
		return nil, fmt.Errorf("the shipped built-in styles do not read: %w", err)
	}
	var formal styles.Style
	for _, st := range builtIn {
		if st.ID == "formal" {
			formal = st
		}
	}
	if formal.Instructions == "" {
		return nil, errors.New("settings/styles.json ships no built-in style formal with instructions, which the check chooses")
	}
	reply := func(text string) []fake.Step {
		return []fake.Step{{Reply: &seam.Response{
			Message:    seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: text}}},
			StopReason: seam.StopEnd, Usage: seam.Usage{InputTokens: 300, OutputTokens: 20},
		}}}
	}
	p, err := fake.New([]fake.Script{
		{Name: "no style", When: fake.When{UserText: stylesQuestion}, Steps: reply(stylesPlain)},
		{Name: "bulleted", When: fake.When{UserText: stylesQuestion, SystemContains: stylesMark}, Steps: reply(stylesBullets)},
		{Name: "formal", When: fake.When{UserText: stylesQuestion, SystemContains: formal.Instructions}, Steps: reply(stylesFormal)},
	}, fake.Options{})
	if err != nil {
		return nil, err
	}
	ears := &styleEars{p: p}

	dir, err := os.MkdirTemp("", "keystone-styles-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	o, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	person, err := in.Create("Styles Check Person")
	if err != nil {
		return nil, err
	}
	other, err := in.Create("Styles Check Other Person")
	if err != nil {
		return nil, err
	}
	with := func(id string, f func(s *tenant.Store) error) error {
		s, err := in.Store(id)
		if err != nil {
			return err
		}
		defer s.Close()
		return f(s)
	}
	err = with(person.ID, func(s *tenant.Store) error {
		if err := signin.SetPassword(s, stylesPassword, o); err != nil {
			return err
		}
		lim, err := adapters.Limits(reg)
		if err != nil {
			return err
		}
		if err := models.Add(ctx, s, lim, stylesModel); err != nil {
			return err
		}
		for key, value := range map[string]string{turn.KeyTitleAuto: "false", turn.KeyRecap: "false", memory.SettingOn: "false"} {
			if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, key, value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := with(other.ID, func(s *tenant.Store) error { return signin.SetPassword(s, stylesOtherPassword, o) }); err != nil {
		return nil, err
	}

	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	c.Adapters = adapters.Set{models.AdapterChatCompletions: ears.build}
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	token, rec := w.signIn(person.ID, stylesPassword)
	if token == "" {
		return nil, fmt.Errorf("the invented person cannot sign in through the API: status %d", rec.Code)
	}
	otherToken, rec := w.signIn(other.ID, stylesOtherPassword)
	if otherToken == "" {
		return nil, fmt.Errorf("the other invented person cannot sign in through the API: status %d", rec.Code)
	}
	route := func(method, tok, path, body string, want int, out any) error {
		rec := w.request(method, api.Prefix+path, tok, body, nil)
		if rec.Code != want {
			return fmt.Errorf("%s %s answered %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		if out != nil {
			return json.Unmarshal(rec.Body.Bytes(), out)
		}
		return nil
	}
	newChat := func(tok string) (string, error) {
		var made turn.ChatReply
		err := route("POST", tok, "chats", "", http.StatusCreated, &made)
		return made.Chat.ID, err
	}
	// send sends the question and returns the reply's text, the request the
	// model was sent, and what the call was built from.
	send := func(chatID string) (string, seam.Request, contextbuild.Record, error) {
		body, _ := json.Marshal(turn.SendBody{Text: stylesQuestion})
		rec := w.request("POST", api.Prefix+"chats/"+chatID+"/turns", token, string(body), nil)
		stream := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(stream, "event: "+turn.EventEnd+"\n") {
			return "", seam.Request{}, contextbuild.Record{}, fmt.Errorf("a turn did not stream to its end: status %d", rec.Code)
		}
		var text strings.Builder
		for _, e := range readStream(stream) {
			if e.name == turn.EventText {
				var d turn.DeltaEvent
				if json.Unmarshal([]byte(e.data), &d) == nil {
					text.WriteString(d.Text)
				}
			}
		}
		req, ok := ears.last()
		if !ok {
			return "", seam.Request{}, contextbuild.Record{}, errors.New("the model was sent nothing")
		}
		var record contextbuild.Record
		err := with(person.ID, func(s *tenant.Store) error {
			calls, err := turn.Contexts(ctx, s, rec.Header().Get("X-Turn-Id"))
			if err != nil || len(calls) == 0 {
				return fmt.Errorf("the turn kept no record of its call (%v)", err)
			}
			record = calls[0].Record
			return nil
		})
		return text.String(), req, record, err
	}
	start := func(req seam.Request) []string {
		if req.Cache == nil {
			return nil
		}
		var out []string
		for _, b := range req.System[:req.Cache.Start] {
			out = append(out, b.Text)
		}
		return out
	}
	choose := func(tok, chatID, styleID string, want int) error {
		body, _ := json.Marshal(turn.ChatStyleBody{Style: styleID})
		return route("PUT", tok, "chats/"+chatID+"/style", string(body), want, nil)
	}

	// 1. A new chat: the shipped default, which sends nothing.
	chatID, err := newChat(token)
	if err != nil {
		return nil, err
	}
	var at turn.ChatStyleReply
	if err := route("GET", token, "chats/"+chatID+"/style", "", http.StatusOK, &at); err != nil {
		return nil, err
	}
	if at.Source != styles.SourceDefault || at.Style == nil || at.Style.Instructions != "" {
		say("a new chat does not use the shipped default with no instructions: %+v", at)
	}
	text, plainReq, record, err := send(chatID)
	if err != nil {
		return nil, err
	}
	if text != stylesPlain {
		say("with no style the reply is %q", text)
	}
	if p := record.Part(contextbuild.PartStyle); p.State != contextbuild.StateEmpty {
		say("with the shipped default the style part is %s", p.State)
	}

	// 2. The person's own style.
	name, words := "Bulleted", "Answer as a bulleted list, one item to a line. Marker: "+stylesMark+"."
	body, _ := json.Marshal(turn.StyleBody{Name: &name, Instructions: &words})
	var mine styles.Style
	if err := route("POST", token, "styles", string(body), http.StatusCreated, &mine); err != nil {
		return nil, err
	}
	if err := choose(token, chatID, mine.ID, http.StatusOK); err != nil {
		return nil, err
	}
	text, bulletReq, record, err := send(chatID)
	if err != nil {
		return nil, err
	}
	if text != stylesBullets {
		say("with the person's own style the reply is %q, not the bulleted list", text)
	}
	bs := start(bulletReq)
	if len(bs) == 0 || !strings.Contains(bs[len(bs)-1], stylesMark) || !strings.Contains(bs[len(bs)-1], `"`+name+`"`) {
		say("the chosen style is not the last block of the standing start, under its heading")
	}
	if p := record.Part(contextbuild.PartStyle); p.State != contextbuild.StateKept || p.Tokens == 0 {
		say("the record's style part is %s with %d tokens", p.State, p.Tokens)
	}
	if len(bs) > 0 && strings.Join(start(plainReq), "\n") != strings.Join(bs[:len(bs)-1], "\n") {
		say("choosing a style changed the standing start before the style")
	}
	if _, again, _, err := send(chatID); err != nil {
		return nil, err
	} else if strings.Join(start(again), "\n") != strings.Join(bs, "\n") {
		say("a chat that kept its style sent another standing start")
	}

	// 3. A built-in style.
	if err := choose(token, chatID, formal.ID, http.StatusOK); err != nil {
		return nil, err
	}
	text, formalReq, _, err := send(chatID)
	if err != nil {
		return nil, err
	}
	if text != stylesFormal {
		say("with the built-in Formal the reply is %q", text)
	}
	fs := start(formalReq)
	switch {
	case len(fs) == 0 || !strings.Contains(fs[len(fs)-1], formal.Instructions):
		say("the built-in Formal is not the last block of the standing start")
	case strings.Contains(systemOf(mustEncode(formalReq)), stylesMark):
		say("the call after the style changed still carries the old one")
	case len(bs) > 0 && strings.Join(fs[:len(fs)-1], "\n") != strings.Join(bs[:len(bs)-1], "\n"):
		say("changing the style changed the standing start before the style")
	}

	// 4. Fixed rule 1.
	theirChat, err := newChat(otherToken)
	if err != nil {
		return nil, err
	}
	for _, x := range []struct{ method, path, body string }{
		{"GET", "styles/" + mine.ID, ""},
		{"PATCH", "styles/" + mine.ID, `{"name":"Taken"}`},
		{"DELETE", "styles/" + mine.ID, ""},
		{"PUT", "styles/" + mine.ID + "/default", ""},
		{"GET", "chats/" + chatID + "/style", ""},
		{"PUT", "chats/" + chatID + "/style", `{"style":"normal"}`},
		{"PUT", "chats/" + theirChat + "/style", `{"style":"` + mine.ID + `"}`},
	} {
		if r := w.request(x.method, api.Prefix+x.path, otherToken, x.body, nil); r.Code != http.StatusNotFound {
			say("%s %s as another person answered %d, want 404", x.method, x.path, r.Code)
		}
	}
	if err := choose(token, theirChat, "normal", http.StatusNotFound); err != nil {
		say("giving another person's chat a style: %v", err)
	}
	var read styles.Style
	if err := route("GET", token, "styles/"+mine.ID, "", http.StatusOK, &read); err != nil {
		return nil, err
	}
	if read.Name != name || read.Instructions != words {
		say("after another person's calls the style reads %+v", read)
	}
	if err := route("GET", token, "chats/"+chatID+"/style", "", http.StatusOK, &at); err != nil {
		return nil, err
	}
	if at.Style == nil || at.Style.ID != formal.ID {
		say("after another person's calls the chat's style is %+v", at.Style)
	}

	// 5. A style is words: its package reaches none of what decides where a
	// call goes, what it is given, or what may run.
	out, err := project.Run(root, "go", "list", "-deps", "./internal/styles")
	if err != nil {
		return nil, fmt.Errorf("go list -deps ./internal/styles failed:\n%s", out)
	}
	for _, dep := range strings.Fields(out) {
		for _, banned := range []string{"internal/routing", "internal/models", "internal/knowledge/recall", "internal/tools", "internal/tools/approval"} {
			if dep == module+"/"+banned {
				say("internal/styles reaches %s, and a style must not change where a call goes or what may run", banned)
			}
		}
	}
	return problems, nil
}

// mustEncode is a request as the audit log keeps it.
func mustEncode(req seam.Request) string {
	raw, err := seam.EncodeRequest(req)
	if err != nil {
		return ""
	}
	return string(raw)
}
