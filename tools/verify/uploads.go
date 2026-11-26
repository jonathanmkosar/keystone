package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/api"
	"github.com/jonathanmkosar/keystone/internal/app"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/secrets"
	"github.com/jonathanmkosar/keystone/internal/signin"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/turn"
	"github.com/jonathanmkosar/keystone/internal/uploads"
	"github.com/jonathanmkosar/keystone/settings"
)

// Uploads (P9-02): the files and images a person adds to a chat. The
// package has a doc, and serves routes.
func init() {
	componentDocs["internal/uploads"] = "docs/architecture/uploads.md"
	networkAllowed["internal/uploads"] = "the upload routes' handlers, served by the server, and an image known by its bytes with net/http's sniffer; it opens no connection"
	networkAllowed["tools/verify"] += "; the uploads check serves stand-ins for the three real adapters on this machine"
}

// The "uploads" check (P9-02), the item's proof as a standing check: an
// image never reaches a model without vision, and a file's extracted text
// is in the turn inspector's record.
//
// It runs the real server on a throwaway folder, with an invented person
// and two models the check answers for: one that cannot see and one that
// can. Every request each is sent is kept.
//
//  1. An image and a text file are uploaded through the API and sent to the
//     model that cannot see, with nothing to fall back to. Its call holds
//     no image, nor does the audit log's copy of it; the inspector records
//     the image as withheld and the file's text in the attachments, with
//     its tokens; the chat's notices tell the person.
//  2. With the model that can see as the fallback, another image goes to
//     it, as its bytes, and not to the model that cannot see, and the chat
//     keeps the model the person picked.
//  3. Another person cannot read the uploads, by any route.
//  4. Each of the three real adapters, given an entry without vision and a
//     request with an image, refuses it before anything reaches a stand-in
//     server on this machine.
//
// Last, every request the model that cannot see was ever sent is searched
// for an image.
func checkUploads(string) error {
	problems, err := uploadProblems()
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

var (
	uploadBlind = models.Model{
		Name: "blind-example", Label: "Blind Example", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8081/v1",
		ProviderModel: "blind-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
		Capabilities: []models.Capability{models.CapTools, models.CapFiles},
	}
	uploadSight = models.Model{
		Name: "sight-example", Label: "Sight Example", Adapter: models.AdapterAnthropic, Endpoint: "http://localhost:8082/v1",
		ProviderModel: "sight-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
		Capabilities: []models.Capability{models.CapTools, models.CapVision, models.CapFiles},
	}
)

// Invented, for a throwaway folder only.
const (
	uploadPassword      = "an invented phrase for the uploads check"
	uploadOtherPassword = "another invented phrase for the uploads check"
	uploadNotesText     = "The invented ferry leaves the north pier at ten past nine."
)

// uploadEars keeps every request each model is sent, and answers each.
type uploadEars struct {
	mu   sync.Mutex
	sent map[string][]seam.Request
}

func (e *uploadEars) build(m models.Model, _ string) (seam.Provider, error) {
	return uploadEar{e, m.Name}, nil
}

func (e *uploadEars) of(name string) []seam.Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]seam.Request(nil), e.sent[name]...)
}

type uploadEar struct {
	e    *uploadEars
	name string
}

func (h uploadEar) Stream(_ context.Context, req seam.Request, emit func(seam.Event) error) error {
	h.e.mu.Lock()
	h.e.sent[h.name] = append(h.e.sent[h.name], req)
	h.e.mu.Unlock()
	events, err := seam.Events(seam.Response{
		Model: req.Model, StopReason: seam.StopEnd, Usage: seam.Usage{InputTokens: 100, OutputTokens: 10},
		Message: seam.Message{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockText, Text: "A reply from " + h.name + "."}}},
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

// checkPicture is an invented picture: a small square of one colour.
func checkPicture(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func uploadProblems() ([]string, error) {
	var problems []string
	say := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "keystone-uploads-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in, err := tenant.OpenWithKeeper(dir, secrets.NewMemoryKeeper())
	if err != nil {
		return nil, err
	}
	reg, err := settings.Registry()
	if err != nil {
		return nil, err
	}
	o, err := signin.Defaults()
	if err != nil {
		return nil, err
	}
	person, err := in.Create("Uploads Check Person")
	if err != nil {
		return nil, err
	}
	other, err := in.Create("Uploads Check Other Person")
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
	set := func(key, value string) error {
		return with(person.ID, func(s *tenant.Store) error {
			return tenantsettings.Set(ctx, s, reg, tenantsettings.Person, key, value)
		})
	}
	err = with(person.ID, func(s *tenant.Store) error {
		if err := signin.SetPassword(s, uploadPassword, o); err != nil {
			return err
		}
		lim, err := adapters.Limits(reg)
		if err != nil {
			return err
		}
		for _, m := range []models.Model{uploadBlind, uploadSight} {
			if err := models.Add(ctx, s, lim, m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := with(other.ID, func(s *tenant.Store) error { return signin.SetPassword(s, uploadOtherPassword, o) }); err != nil {
		return nil, err
	}
	for key, value := range map[string]string{turn.KeyTitleAuto: "false", turn.KeyFallbackModels: ""} {
		if err := set(key, value); err != nil {
			return nil, err
		}
	}

	ears := &uploadEars{sent: map[string][]seam.Request{}}
	c, err := app.Defaults()
	if err != nil {
		return nil, err
	}
	w := &walker{log: &bytes.Buffer{}}
	c.Install, c.Log = in, w.log
	c.Adapters = adapters.Set{models.AdapterChatCompletions: ears.build, models.AdapterAnthropic: ears.build}
	var stop func()
	if w.h, stop, err = app.Open(c); err != nil {
		return nil, err
	}
	defer stop()
	token, rec := w.signIn(person.ID, uploadPassword)
	if token == "" {
		return nil, fmt.Errorf("the invented person cannot sign in through the API: status %d", rec.Code)
	}
	otherToken, rec := w.signIn(other.ID, uploadOtherPassword)
	if otherToken == "" {
		return nil, fmt.Errorf("the other invented person cannot sign in through the API: status %d", rec.Code)
	}
	rec = w.request("POST", api.Prefix+"chats", token, "", nil)
	var made turn.ChatReply
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &made) != nil {
		return nil, fmt.Errorf("the invented person cannot make a chat: status %d", rec.Code)
	}
	chatID := made.Chat.ID
	upload := func(name string, data []byte) (uploads.Upload, error) {
		rec := w.request("POST", api.Prefix+"chats/"+chatID+"/uploads?name="+url.QueryEscape(name), token, string(data), nil)
		var got uploads.AddReply
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			return uploads.Upload{}, fmt.Errorf("uploading %s answered %d: %s", name, rec.Code, rec.Body)
		}
		return got.Upload, nil
	}
	send := func(model string, blocks ...seam.Block) (turnID, requestID string, err error) {
		body, _ := json.Marshal(turn.SendBody{Blocks: blocks, Model: model})
		rec := w.request("POST", api.Prefix+"chats/"+chatID+"/turns", token, string(body), nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"done"`) {
			return "", "", fmt.Errorf("a turn with an upload did not end as done: status %d: %s", rec.Code, rec.Body)
		}
		return rec.Header().Get("X-Turn-Id"), rec.Header().Get("X-Request-Id"), nil
	}
	text := func(s string) seam.Block { return seam.Block{Type: seam.BlockText, Text: s} }

	// 1. No model of the route can see.
	red, err := upload("red-square.png", checkPicture(color.RGBA{220, 40, 40, 255}))
	if err != nil {
		return nil, err
	}
	notes, err := upload("ferry-notes.txt", []byte(uploadNotesText))
	if err != nil {
		return nil, err
	}
	turnID, requestID, err := send(uploadBlind.Name, text("What does the picture show, and when does the ferry leave?"), uploads.BlockOf(red), uploads.BlockOf(notes))
	if err != nil {
		return nil, err
	}
	heard := ears.of(uploadBlind.Name)
	switch {
	case len(heard) == 0:
		say("the model that cannot see was not called with the turn")
	case !strings.Contains(strings.Join(uploadSystem(heard[len(heard)-1]), "\n"), uploadNotesText):
		say("the call to the model that cannot see does not hold the text file's text in its attachments")
	}
	err = with(person.ID, func(s *tenant.Store) error {
		entries, err := audit.List(ctx, s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil {
			return err
		}
		for _, e := range entries {
			if strings.Contains(e.Sent, `"type":"image"`) {
				say("the audit log holds an image sent to %s", e.Action)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	rec = w.request("GET", api.Prefix+"chats/"+chatID+"/turns/"+turnID+"/inspect", token, "", nil)
	var inspected turn.InspectReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &inspected) != nil {
		return nil, fmt.Errorf("the turn inspector answered %d", rec.Code)
	}
	if len(inspected.Uploads) == 0 {
		say("the turn inspector holds no record of the turn's uploads")
	} else if held := inspected.Uploads[0].Call.Withheld(); len(held) != 1 || held[0].ID != red.ID || held[0].Why != uploads.WhyVision {
		say("the turn inspector records %+v as withheld, want the image, for want of vision", held)
	}
	found := false
	for _, call := range inspected.Calls {
		if call.Context == nil {
			continue
		}
		for _, it := range call.Context.Part(contextbuild.PartAttached).Items {
			if it.Ref == uploads.RefOf(notes.ID) && it.Kept && it.Tokens > 0 {
				found = true
			}
		}
	}
	if !found {
		say("the turn inspector does not show the text file's extracted text in the call's attachments, with its tokens")
	}
	rec = w.request("GET", api.Prefix+"chats/"+chatID+"/uploads/"+notes.ID, token, "", nil)
	var one uploads.OneReply
	if json.Unmarshal(rec.Body.Bytes(), &one) != nil || one.Text != uploadNotesText {
		say("the text read from the file reads %q", one.Text)
	}
	rec = w.request("GET", api.Prefix+"chats/"+chatID+"/uploads", token, "", nil)
	var listed uploads.ListReply
	if json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Notices) != 1 || listed.Notices[0].Kind != uploads.NoticeWithheld {
		say("the chat's notices are %+v, want one that the image was withheld", listed.Notices)
	}

	// 2. A fallback that can see.
	if err := set(turn.KeyFallbackModels, uploadSight.Name); err != nil {
		return nil, err
	}
	blue := checkPicture(color.RGBA{40, 40, 220, 255})
	blueUp, err := upload("blue-square.png", blue)
	if err != nil {
		return nil, err
	}
	before := len(ears.of(uploadBlind.Name))
	if _, _, err := send("", text("And this one?"), uploads.BlockOf(blueUp)); err != nil {
		return nil, err
	}
	if n := len(ears.of(uploadBlind.Name)) - before; n != 0 {
		say("with a fallback that can see, the model that cannot see was called %d times for a turn with an image", n)
	}
	seen := ears.of(uploadSight.Name)
	if len(seen) == 0 || !holdsBytes(seen[len(seen)-1], blue) {
		say("the fallback that can see was not sent the image's bytes")
	}
	rec = w.request("GET", api.Prefix+"chats/"+chatID+"/model", token, "", nil)
	var choice turn.ModelChoice
	if json.Unmarshal(rec.Body.Bytes(), &choice) != nil || choice.Model == nil || choice.Model.Name != uploadBlind.Name {
		say("after a fallback saw an image for it, the chat's model is %+v, want the one picked", choice.Model)
	}

	// 3. Another person.
	for _, path := range []string{"chats/" + chatID + "/uploads", "chats/" + chatID + "/uploads/" + red.ID, "chats/" + chatID + "/uploads/" + red.ID + "/content"} {
		if rec := w.request("GET", api.Prefix+path, otherToken, "", nil); rec.Code != http.StatusNotFound {
			say("another person's GET %s answered %d", path, rec.Code)
		}
	}

	// 4. The three real adapters.
	refused, err := realAdaptersRefuseImages(reg)
	if err != nil {
		return nil, err
	}
	problems = append(problems, refused...)

	for i, req := range ears.of(uploadBlind.Name) {
		if n := seam.Images(req); n != 0 {
			say("request %d to the model that cannot see held %d images", i+1, n)
		}
	}
	return problems, nil
}

func uploadSystem(req seam.Request) []string {
	var out []string
	for _, b := range req.System {
		out = append(out, b.Text)
	}
	return out
}

// holdsBytes reports whether a request holds an image of exactly these
// bytes.
func holdsBytes(req seam.Request, data []byte) bool {
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			if b.Type == seam.BlockImage && bytes.Equal(b.Data, data) {
				return true
			}
		}
	}
	return false
}

// realAdaptersRefuseImages gives each of the three real adapters, as the
// server builds them from the settings, an entry without vision that
// points at a stand-in on this machine, and a request with an image. Each
// must refuse it, and the stand-in must hear nothing.
func realAdaptersRefuseImages(reg []settings.Setting) ([]string, error) {
	var problems []string
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, `{"error":{"message":"the stand-in answers every call so"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	set, err := adapters.Real(reg)
	if err != nil {
		return nil, err
	}
	req := seam.Request{Messages: []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{
		{Type: seam.BlockText, Text: "What is in this picture?"},
		{Type: seam.BlockImage, MediaType: "image/png", Data: checkPicture(color.RGBA{1, 2, 3, 255})},
	}}}}
	for _, name := range []string{models.AdapterAnthropic, models.AdapterResponses, models.AdapterChatCompletions} {
		build := set[name]
		if build == nil {
			problems = append(problems, "the server has no "+name+" adapter")
			continue
		}
		entry := models.Model{
			Name: "blind-" + strings.ReplaceAll(name, "_", "-"), Label: "Blind", Adapter: name, Endpoint: srv.URL + "/v1",
			ProviderModel: "blind-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
			Capabilities: []models.Capability{models.CapTools, models.CapFiles},
		}
		p, err := build(entry, "an invented value, not a key")
		if err != nil {
			return nil, fmt.Errorf("the %s adapter would not open an entry: %w", name, err)
		}
		req.Model = entry.Name
		err = p.Stream(context.Background(), req, func(seam.Event) error { return nil })
		var se *seam.Error
		if !errors.As(err, &se) || se.Kind != seam.ErrInvalid {
			problems = append(problems, fmt.Sprintf("the %s adapter did not refuse an image for a model without vision: %v", name, err))
		}
	}
	if n := hits.Load(); n != 0 {
		problems = append(problems, fmt.Sprintf("a stand-in for the real adapters was sent %d requests holding an image for a model without vision", n))
	}
	return problems, nil
}
