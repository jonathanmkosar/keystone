package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/knowledge/memory"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/settings"
)

// Location per message (P10-06): the control's pure part is a file of the
// web interface, described in web.md with the control.
func init() {
	componentDocs["web/app/src/location.ts"] = "docs/architecture/web.md"
	componentDocs["internal/turn/location.go"] = "docs/architecture/turns.md"
}

// The "location" check (P10-06), the item's proof as a standing check. On
// the real server, with the shipped settings, on a throwaway folder, with
// two invented people and one local model the check answers for:
//
//  1. A message sent with a position carries it, rounded to the setting,
//     as one line before the person's words in the model's request and in
//     the audit log; the exact position is in neither, nor in the turn's
//     stored input; the record keeps the rounded position with that
//     message alone.
//  2. The next message carries no line of its own, and the first message
//     goes in its call byte for byte as it went, with the standing start
//     unchanged, so both still cache.
//  3. A message without a position, in another chat, is sent byte for byte
//     as the same message was before any position was shared.
//  4. Fixed rule 1: the other person, with the right ids, reads nothing.
//  5. The position goes with its chat.
//
// And from the source: the browser is asked for a position in one place
// only, on the control's tap, and never watched; the pure part imports
// nothing and its Node tests pass; the server allows the position to
// Keystone's own pages beside the microphone; the settings are in the
// registry.

// Invented, for a throwaway folder only; the point is in the open Atlantic.
const (
	locationPassword      = "an invented phrase for the location check"
	locationOtherPassword = "another invented phrase for the location check"
	locationAsk           = "Is the harbour wind strong this evening?"
	locationNext          = "And at dawn?"
	locationLine          = "The person's approximate location: 12.35, -45.68 (rounded to about 1 km), shared with this message."
)

var locationModel = models.Model{
	Name: "location-local", Label: "Location Local", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8085/v1",
	ProviderModel: "small-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
}

// locationHeard keeps every request it is sent and answers each briefly.
type locationHeard struct {
	mu   sync.Mutex
	reqs []seam.Request
}

func (p *locationHeard) build(models.Model, string) (seam.Provider, error) { return p, nil }

func (p *locationHeard) Stream(_ context.Context, req seam.Request, emit func(seam.Event) error) error {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	p.mu.Unlock()
	events, err := seam.Events(seam.Response{
		Model:      req.Model,
		Message:    seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: "A light breeze."}}},
		StopReason: seam.StopEnd,
		Usage:      seam.Usage{InputTokens: 100, OutputTokens: 5},
	})
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := emit(e); err != nil {
			return err
		}
	}
	return nil
}

// asked returns the requests whose last message holds the words.
func (p *locationHeard) asked(words string) []seam.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []seam.Request
	for _, r := range p.reqs {
		if n := len(r.Messages); n > 0 && strings.Contains(seam.Text(r.Messages[n-1]), words) {
			out = append(out, r)
		}
	}
	return out
}

func checkLocation(root string) error {
	problems, err := locationProblems()
	if err != nil {
		return err
	}
	problems = append(problems, locationSourceProblems(root)...)
	problems = append(problems, locationSettingsProblems()...)
	problems = append(problems, locationTestProblems(root)...)
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func locationProblems() ([]string, error) {
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	ctx := context.Background()

	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "keystone-location-")
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
	person, err := in.Create("Location Check Person")
	if err != nil {
		return nil, err
	}
	other, err := in.Create("Location Check Other Person")
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
		if err := signin.SetPassword(s, locationPassword, o); err != nil {
			return err
		}
		for key, value := range map[string]string{turn.KeyTitleAuto: "false", turn.KeyRecap: "false", memory.SettingOn: "false"} {
			if err := tenantsettings.Set(ctx, s, reg, tenantsettings.Person, key, value); err != nil {
				return err
			}
		}
		lim, err := adapters.Limits(reg)
		if err != nil {
			return err
		}
		return models.Add(ctx, s, lim, locationModel)
	})
	if err != nil {
		return nil, err
	}
	if err := with(other.ID, func(s *tenant.Store) error { return signin.SetPassword(s, locationOtherPassword, o) }); err != nil {
		return nil, err
	}

	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	heard := &locationHeard{}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	c.Adapters = adapters.Set{models.AdapterChatCompletions: heard.build}
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	token, rec := w.signIn(person.ID, locationPassword)
	if token == "" {
		return nil, fmt.Errorf("the invented person cannot sign in through the API: status %d", rec.Code)
	}
	otherToken, rec := w.signIn(other.ID, locationOtherPassword)
	if otherToken == "" {
		return nil, fmt.Errorf("the other invented person cannot sign in through the API: status %d", rec.Code)
	}
	newChat := func() (string, error) {
		var made turn.ChatReply
		rec := w.request("POST", api.Prefix+"chats", token, "", nil)
		if rec.Code != http.StatusCreated {
			return "", fmt.Errorf("making a chat answered %d: %s", rec.Code, rec.Body.String())
		}
		err := json.Unmarshal(rec.Body.Bytes(), &made)
		return made.Chat.ID, err
	}
	send := func(chatID string, body turn.SendBody) error {
		raw, _ := json.Marshal(body)
		rec := w.request("POST", api.Prefix+"chats/"+chatID+"/turns", token, string(raw), nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "event: end") {
			return fmt.Errorf("sending a message answered %d: %s", rec.Code, rec.Body.String())
		}
		return nil
	}
	sentFor := func(words string) []string {
		var out []string
		with(person.ID, func(s *tenant.Store) error {
			entries, err := audit.List(ctx, s, audit.Filter{Kind: audit.KindModel})
			if err != nil {
				say("the audit log cannot be read: %v", err)
			}
			for _, e := range entries {
				if e.Outcome == audit.OutcomeStarted && strings.Contains(e.Sent, words) {
					out = append(out, e.Sent)
				}
			}
			return nil
		})
		return out
	}
	exact := &turn.Location{Latitude: 12.345678, Longitude: -45.678912}

	// 3, first half: a plain message before any position is shared.
	before, err := newChat()
	if err != nil {
		return nil, err
	}
	if err := send(before, turn.SendBody{Text: locationNext}); err != nil {
		return nil, err
	}

	// 1. A message with a position.
	located, err := newChat()
	if err != nil {
		return nil, err
	}
	if err := send(located, turn.SendBody{Text: locationAsk, Location: exact}); err != nil {
		return nil, err
	}
	first := heard.asked(locationAsk)
	if len(first) != 1 {
		return nil, fmt.Errorf("the model was asked %d times for one message", len(first))
	}
	said := first[0].Messages[len(first[0].Messages)-1]
	if len(said.Blocks) != 2 || said.Blocks[0].Text != locationLine || said.Blocks[1].Text != locationAsk {
		raw, _ := json.Marshal(said)
		say("a message sent with a position reached the model as %s, want the rounded line and then the person's words", raw)
	}
	logged := sentFor(locationAsk)
	if len(logged) != 1 || !strings.Contains(logged[0], locationLine) {
		say("the audit log does not hold the line the model was sent (%d entries)", len(logged))
	}
	err = with(person.ID, func(s *tenant.Store) error {
		var inputs []string
		rows, err := s.DB().Query("SELECT input FROM turns")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var input string
			if err := rows.Scan(&input); err != nil {
				return err
			}
			inputs = append(inputs, input)
		}
		for _, text := range append(inputs, logged...) {
			if strings.Contains(text, "12.3456") || strings.Contains(text, "45.6789") {
				say("the exact position was kept or sent: the server must keep and send only the rounded one")
				break
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	var chatRead turn.ChatReply
	rec = w.request("GET", api.Prefix+"chats/"+located, token, "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &chatRead); err != nil || rec.Code != http.StatusOK {
		return nil, fmt.Errorf("reading the chat answered %d: %s", rec.Code, rec.Body.String())
	}
	want := &turn.SentLocation{Latitude: 12.35, Longitude: -45.68, Decimals: 2, Line: locationLine}
	if len(chatRead.Messages) != 2 || !reflect.DeepEqual(chatRead.Messages[0].Location, want) || chatRead.Messages[1].Location != nil {
		say("the record does not keep the rounded position with the person's message alone: %s", rec.Body.String())
	}

	// 2. The next message.
	if err := send(located, turn.SendBody{Text: locationNext}); err != nil {
		return nil, err
	}
	var next []seam.Request
	for _, r := range heard.asked(locationNext) {
		if len(r.Messages) == 3 {
			next = append(next, r)
		}
	}
	switch {
	case len(next) != 1:
		say("the second message of the located chat made %d calls with its history", len(next))
	case !reflect.DeepEqual(next[0].Messages[0], said):
		say("the second call sent the located message differently, so its history would not be read from the cache")
	case !reflect.DeepEqual(next[0].System, first[0].System):
		say("the standing start changed between the calls")
	case len(next[0].Messages[2].Blocks) != 1 || next[0].Messages[2].Blocks[0].Text != locationNext:
		say("the message after the located one carries more than its words: %+v", next[0].Messages[2].Blocks)
	}

	// 3, second half: the same plain message, in a new chat, after.
	after, err := newChat()
	if err != nil {
		return nil, err
	}
	if err := send(after, turn.SendBody{Text: locationNext}); err != nil {
		return nil, err
	}
	var plain []string
	for _, s := range sentFor(locationNext) {
		if !strings.Contains(s, locationAsk) {
			plain = append(plain, s)
		}
	}
	if len(plain) != 2 || plain[0] != plain[1] {
		say("a plain message was not sent byte for byte as before a position was shared (%d requests)", len(plain))
	} else if strings.Contains(plain[0], "approximate location") {
		say("a plain message's request carries a location")
	}

	// 4. The other person, with the right ids.
	for _, path := range []string{"chats/" + located, "chats/" + located + "/turns"} {
		if rec := w.request("GET", api.Prefix+path, otherToken, "", nil); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			say("the other person's GET %s answered %d, want 404", path, rec.Code)
		}
	}
	err = with(other.ID, func(s *tenant.Store) error {
		var n int
		if err := s.DB().QueryRow("SELECT count(*) FROM message_locations").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			say("the other person's database holds %d locations", n)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// 5. The position goes with its chat.
	if rec := w.request("DELETE", api.Prefix+"chats/"+located, token, "", nil); rec.Code != http.StatusOK {
		return nil, fmt.Errorf("deleting the located chat answered %d", rec.Code)
	}
	err = with(person.ID, func(s *tenant.Store) error {
		var n int
		if err := s.DB().QueryRow("SELECT count(*) FROM message_locations").Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			say("deleting the chat left %d locations", n)
		}
		return nil
	})
	return problems, err
}

var locationComment = regexp.MustCompile(`(?m)^\s*(//|\*|/\*).*$`)

// locationSourceProblems reads the source for what a test in a browser
// would otherwise have to watch: the browser is asked for a position in
// location.ts alone, by askPosition, which only the control's tap calls;
// nothing watches the position; the pure part imports nothing; the
// composer sends the position with its one send; and the server allows the
// position to Keystone's own pages, beside the microphone.
func locationSourceProblems(root string) []string {
	var problems []string
	src := filepath.Join(root, "web", "app", "src")
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !slices.Contains([]string{".ts", ".tsx"}, filepath.Ext(path)) {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		code := locationComment.ReplaceAll(raw, nil)
		name, _ := filepath.Rel(root, path)
		name = filepath.ToSlash(name)
		if bytes.Contains(code, []byte("watchPosition")) {
			problems = append(problems, name+" watches the position (watchPosition): a position is read once, on a tap")
		}
		if bytes.Contains(code, []byte("getCurrentPosition")) && filepath.Base(path) != "location.ts" {
			problems = append(problems, name+" asks the browser for a position; only askPosition in location.ts may, on the control's tap")
		}
		return nil
	})
	if err != nil {
		problems = append(problems, "web/app/src cannot be read: "+err.Error())
	}
	if raw, err := os.ReadFile(filepath.Join(src, "location.ts")); err != nil {
		problems = append(problems, "web/app/src/location.ts, the location control's pure part, cannot be read: "+err.Error())
	} else if regexp.MustCompile(`(?m)^\s*import\s`).Match(raw) {
		problems = append(problems, "web/app/src/location.ts imports something; it is given all it uses")
	}
	if raw, err := os.ReadFile(filepath.Join(src, "LocationControl.tsx")); err != nil {
		problems = append(problems, "web/app/src/LocationControl.tsx cannot be read: "+err.Error())
	} else {
		c := string(locationComment.ReplaceAll(raw, nil))
		tap, remove, call := strings.Index(c, "tap: () => {"), strings.Index(c, "remove: () => {"), strings.Index(c, "askPosition(")
		if strings.Count(c, "askPosition(") != 1 || tap < 0 || call < tap || remove < call {
			problems = append(problems, "web/app/src/LocationControl.tsx asks for a position somewhere other than its tap: askPosition must be called once, in tap")
		}
	}
	if raw, err := os.ReadFile(filepath.Join(src, "Composer.tsx")); err != nil || !bytes.Contains(raw, []byte("onSend(text.trim(), attach.files, location.take())")) {
		problems = append(problems, "Composer.tsx no longer sends the location with its one send and lets it go (location.take())")
	}
	serve, err := os.ReadFile(filepath.Join(root, "cmd", "keystone", "serve.go"))
	if err != nil {
		return append(problems, "cmd/keystone/serve.go cannot be read: "+err.Error())
	}
	if !bytes.Contains(serve, []byte(`h.Add("Permissions-Policy", "geolocation=(self)")`)) {
		problems = append(problems, "cmd/keystone/serve.go no longer allows the position to Keystone's own origin alone (Permissions-Policy: geolocation=(self))")
	}
	return problems
}

// locationSettingsProblems: the control's settings are in the registry,
// with the types the page and the server read them as.
func locationSettingsProblems() []string {
	reg, err := settings.Registry()
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	want := map[string]string{turn.KeyLocationShare: "bool", turn.KeyLocationDecimals: "string", turn.KeyLocationTimeout: "duration", turn.KeyLocationLine: "string"}
	for key, typ := range want {
		s, ok := settings.Lookup(reg, key)
		switch {
		case !ok:
			problems = append(problems, key+" is not in the settings registry")
		case s.Type != typ:
			problems = append(problems, fmt.Sprintf("%s is a %s, and it is read as a %s", key, s.Type, typ))
		}
	}
	if s, ok := settings.Lookup(reg, turn.KeyLocationDecimals); ok && !reflect.DeepEqual(s.Options, []string{"1", "2", "3", "4"}) {
		problems = append(problems, turn.KeyLocationDecimals+" must offer 1 to 4 decimals and nothing else, so a position is never kept closer than about 11 m")
	}
	if s, ok := settings.Lookup(reg, turn.KeyLocationLine); ok {
		for _, p := range []string{"{latitude}", "{longitude}", "{distance}"} {
			if !strings.Contains(s.Default, p) {
				problems = append(problems, turn.KeyLocationLine+"'s default does not name "+p)
			}
		}
	}
	return problems
}

// locationTestProblems runs web/app/test/location.test.ts with Node's own
// test runner when Node is on this computer (decision 0501), as the
// dictation check does for its tests.
func locationTestProblems(root string) []string {
	node, err := exec.LookPath("node")
	if err != nil {
		return nil
	}
	app := filepath.Join(root, "web", "app")
	ver, err := project.Run(app, node, "--version")
	if err != nil {
		return []string{"node --version failed: " + ver}
	}
	if !nodeReadsTypeScript(ver) {
		return []string{"node " + ver + " cannot run the location tests, which need Node 22.6 or later; update Node"}
	}
	out, err := project.Run(app, node, "--experimental-strip-types", "--test", filepath.Join("test", "location.test.ts"))
	if err != nil {
		return []string{"the location tests failed (npm test in web/app):\n" + out}
	}
	if !strings.Contains(out, "fail 0") {
		return []string{"the location tests did not report their result:\n" + out}
	}
	return nil
}
