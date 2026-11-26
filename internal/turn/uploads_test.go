package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/adapters"
	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/contextbuild"
	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/routing"
	"github.com/jonathanmkosar/keystone/internal/seam"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/internal/tenantsettings"
	"github.com/jonathanmkosar/keystone/internal/uploads"
)

// Two invented models on this machine: one that cannot see and one that
// can, and takes files.
var (
	blindModel = models.Model{
		Name: "house-blind", Label: "House Blind", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8081/v1",
		ProviderModel: "blind-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
		Capabilities: []models.Capability{models.CapTools},
	}
	sightModel = models.Model{
		Name: "house-sight", Label: "House Sight", Adapter: models.AdapterChatCompletions, Endpoint: "http://localhost:8082/v1",
		ProviderModel: "sight-1", ContextWindow: 16384, OutputLimit: 1024, Class: models.ClassLocal,
		Capabilities: []models.Capability{models.CapTools, models.CapVision, models.CapFiles},
	}
)

// sightEars keeps every request each model is sent, and answers each.
type sightEars struct {
	mu   sync.Mutex
	sent map[string][]seam.Request
}

func (e *sightEars) build(m models.Model, _ string) (seam.Provider, error) {
	return sightEar{e, m.Name}, nil
}

func (e *sightEars) of(name string) []seam.Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]seam.Request(nil), e.sent[name]...)
}

type sightEar struct {
	e    *sightEars
	name string
}

func (h sightEar) Stream(ctx context.Context, req seam.Request, emit func(seam.Event) error) error {
	h.e.mu.Lock()
	h.e.sent[h.name] = append(h.e.sent[h.name], req)
	h.e.mu.Unlock()
	return answers{chat: "A reply from " + h.name + "."}.Stream(ctx, req, emit)
}

// testPicture is an invented picture: a small square of one colour.
func testPicture(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 6, 6))
	for x := 0; x < 6; x++ {
		for y := 0; y < 6; y++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func uploadTo(t *testing.T, g *rig, chatID, name string, data []byte) uploads.Upload {
	t.Helper()
	var up uploads.Upload
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		o := uploads.Options{MaxBytes: 1 << 20, ImageTypes: []string{"image/png"}, TextExtensions: []string{".txt"}, DocumentTypes: []string{"application/pdf"}}
		var err error
		if up, _, err = uploads.Add(context.Background(), s, o, chatID, name, data); err != nil {
			t.Fatal(err)
		}
	})
	return up
}

func sendBlocks(t *testing.T, sv *served, chatID, model string, blocks ...seam.Block) ([]event, string, string) {
	t.Helper()
	body, _ := json.Marshal(SendBody{Blocks: blocks, Model: model})
	return sv.send(sv.g.ordinary.ID, chatID, string(body))
}

func asked(text string) seam.Block { return seam.Block{Type: seam.BlockText, Text: text} }

// The item's proof: an image never reaches a model without vision, by the
// model picked, by a fallback, or as an earlier turn; a turn whose model
// cannot see goes to the first fallback that can; and a text file's text
// is in the call and in the turn inspector's record.
func TestAnImageNeverReachesAModelWithoutVision(t *testing.T) {
	g := newRig(t)
	ears := &sightEars{sent: map[string][]seam.Request{}}
	g.extra = adapters.Set{models.AdapterChatCompletions: ears.build}
	g.addModels(blindModel, sightModel)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	g.set(tenantsettings.Person, KeyFallbackModels, "")
	sv := g.serve(nil)
	chatID := sv.newChat(g.ordinary.ID)
	red := uploadTo(t, g, chatID, "red-square.png", testPicture(t, color.RGBA{220, 30, 30, 255}))
	notes := uploadTo(t, g, chatID, "harbour-notes.txt", []byte("The invented harbour bakery opens at seven."))
	ctx := context.Background()

	// 1. No model of the route can see: the image is left out, with a note,
	// and the text file's text is in the call as an attachment.
	events, turnID, requestID := sendBlocks(t, sv, chatID, blindModel.Name, asked("What is in the picture, and when does it open?"), uploads.BlockOf(red), uploads.BlockOf(notes))
	if end := last(t, events); end.Status != StatusDone || end.Model != blindModel.Name {
		t.Fatalf("the turn ended as %+v", end)
	}
	heard := ears.of(blindModel.Name)
	if len(heard) == 0 {
		t.Fatal("the model that cannot see was not called")
	}
	call := heard[len(heard)-1]
	sent, _ := json.Marshal(call)
	if !strings.Contains(string(sent), "red-square.png. It is not shown to you, because you cannot see images") {
		t.Errorf("the call has no note in place of the image: %s", sent)
	}
	if !strings.Contains(strings.Join(systemTexts(call), "\n"), "The invented harbour bakery opens at seven.") {
		t.Errorf("the file's text is not with the attachments: %v", systemTexts(call))
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		calls, err := uploads.Calls(ctx, s, turnID)
		if err != nil || len(calls) != 1 {
			t.Fatalf("the turn's uploads record: %+v, %v", calls, err)
		}
		withheld := calls[0].Call.Withheld()
		if calls[0].Call.Sees || len(withheld) != 1 || withheld[0].ID != red.ID || withheld[0].Why != uploads.WhyVision || !withheld[0].Current {
			t.Errorf("the record of what was withheld is %+v", calls[0].Call)
		}
		contexts, err := Contexts(ctx, s, turnID)
		if err != nil || len(contexts) != 1 {
			t.Fatalf("the turn's context records: %v", err)
		}
		part := contexts[0].Record.Part(contextbuild.PartAttached)
		if part.State != contextbuild.StateKept || len(part.Items) != 1 || part.Items[0].Ref != uploads.RefOf(notes.ID) || part.Items[0].Tokens == 0 || part.Items[0].Label != "file: harbour-notes.txt" {
			t.Errorf("the inspector's attachments part is %+v", part)
		}
		notices, err := uploads.Notices(ctx, s, chatID)
		if err != nil || len(notices) != 1 || notices[0].Kind != uploads.NoticeWithheld || notices[0].TurnID != turnID || notices[0].UploadID != red.ID {
			t.Errorf("the notices are %+v, %v", notices, err)
		}
		entries, err := audit.List(ctx, s, audit.Filter{RequestID: requestID, Kind: audit.KindModel})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.Contains(e.Sent, `"type":"image"`) {
				t.Errorf("the audit log holds an image sent to %s", e.Action)
			}
		}
	})

	// 2. The chat's model cannot see, and a fallback can: the turn goes to
	// the fallback, with the image, and the chat keeps its model.
	g.set(tenantsettings.Person, KeyFallbackModels, sightModel.Name)
	blue := uploadTo(t, g, chatID, "blue-square.png", testPicture(t, color.RGBA{30, 30, 220, 255}))
	blindBefore := len(ears.of(blindModel.Name))
	events, turnID, _ = sendBlocks(t, sv, chatID, "", asked("And this one?"), uploads.BlockOf(blue))
	end := last(t, events)
	if end.Status != StatusDone || end.Model != blindModel.Name {
		t.Errorf("the turn ended as %+v: the chat's model is still the one picked", end)
	}
	if n := len(ears.of(blindModel.Name)); n != blindBefore {
		t.Errorf("the model that cannot see was called %d times for a turn with an image and a fallback that can", n-blindBefore)
	}
	seen := ears.of(sightModel.Name)
	if len(seen) != 1 || seam.Images(seen[0]) != 2 {
		t.Fatalf("the fallback that can see was sent %d requests, the last with %d images: want one with both", len(seen), imagesOf(seen))
	}
	if got := imageData(seen[0]); !bytes.Equal(got, testPicture(t, color.RGBA{30, 30, 220, 255})) {
		t.Errorf("the last image sent is %d bytes, not the picture uploaded", len(got))
	}
	var choice ModelChoice
	sv.call(g.ordinary.ID, "GET", "chats/"+chatID+"/model", "", http.StatusOK, &choice)
	if choice.Model == nil || choice.Model.Name != blindModel.Name {
		t.Errorf("after a fallback saw an image, the chat's model is %+v", choice.Model)
	}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		notices, _ := uploads.Notices(ctx, s, chatID)
		if len(notices) != 2 || notices[1].Kind != uploads.NoticeMoved || notices[1].TurnID != turnID || !strings.Contains(notices[1].Text, sightModel.Name) {
			t.Errorf("the notices are %+v", notices)
		}
	})

	// 3. A question with no image, on the model that cannot see, with the
	// one that can behind it: the call is built for the least of its
	// route, so the earlier images are left out.
	sendBlocks(t, sv, chatID, "", asked("Which was brighter?"))
	if n := len(ears.of(blindModel.Name)); n != blindBefore+1 {
		t.Errorf("the model that cannot see was called %d times for a plain question, want once", n-blindBefore)
	}

	// 4. The model that can see, picked, with the one that cannot behind
	// it: an earlier image keeps the route to models that can see.
	g.set(tenantsettings.Person, KeyFallbackModels, blindModel.Name)
	blindBefore = len(ears.of(blindModel.Name))
	sendBlocks(t, sv, chatID, sightModel.Name, asked("Describe both again."))
	seen = ears.of(sightModel.Name)
	if len(seen) != 2 || seam.Images(seen[1]) != 2 {
		t.Errorf("the model that can see, picked, was sent %d requests, the last with %d images", len(seen), imagesOf(seen))
	}
	if n := len(ears.of(blindModel.Name)); n != blindBefore {
		t.Errorf("the model that cannot see was called %d times", n-blindBefore)
	}

	// Every request the model that cannot see was ever sent.
	for i, req := range ears.of(blindModel.Name) {
		if n := seam.Images(req); n != 0 {
			t.Errorf("request %d to the model that cannot see held %d images", i+1, n)
		}
	}
}

// A call is built once for its whole route, so a route with a model that
// cannot see anywhere in it is sent no image, even when the first model
// can: the second lock behind the route's own choice.
func TestACallIsReadiedForTheLeastOfItsRoute(t *testing.T) {
	g := newRig(t)
	ears := &sightEars{sent: map[string][]seam.Request{}}
	g.extra = adapters.Set{models.AdapterChatCompletions: ears.build}
	g.addModels(sightModel, blindModel)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	sv := g.serve(nil)
	chatID := sv.newChat(g.ordinary.ID)
	sendBlocks(t, sv, chatID, sightModel.Name, asked("Hello")) // the runner reads its settings once
	picture := uploadTo(t, g, chatID, "square.png", testPicture(t, color.RGBA{4, 5, 6, 255}))
	branch := []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{asked("Look"), uploads.BlockOf(picture)}}}
	g.with(g.ordinary.ID, func(s *tenant.Store) {
		for name, route := range map[string][]models.Model{
			"the one that sees first":  {sightModel, blindModel},
			"the one that sees second": {blindModel, sightModel},
		} {
			var links []routing.Link
			for _, m := range route {
				p, _ := ears.build(m, "")
				links = append(links, routing.Link{Model: m, Provider: p})
			}
			chain, err := routing.NewChain(false, links, routing.Hooks{})
			if err != nil {
				t.Fatal(err)
			}
			got, _ := sv.runner.withUploads(context.Background(), s, row{Turn: Turn{ID: "invented-turn", ChatID: chatID}}, 1, route[0], chain, branch)
			if n := seam.Images(seam.Request{Messages: got}); n != 0 {
				t.Errorf("%s: a route with a model that cannot see was readied with %d images", name, n)
			}
		}
		alone, err := routing.NewChain(false, []routing.Link{{Model: sightModel, Provider: sightEar{ears, sightModel.Name}}}, routing.Hooks{})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := sv.runner.withUploads(context.Background(), s, row{Turn: Turn{ID: "invented-turn", ChatID: chatID}}, 1, sightModel, alone, branch)
		if n := seam.Images(seam.Request{Messages: got}); n != 1 {
			t.Errorf("a route of the model that sees alone was readied with %d images", n)
		}
	})
}

func systemTexts(req seam.Request) []string {
	var out []string
	for _, b := range req.System {
		out = append(out, b.Text)
	}
	return out
}

func imagesOf(reqs []seam.Request) int {
	if len(reqs) == 0 {
		return 0
	}
	return seam.Images(reqs[len(reqs)-1])
}

// imageData is the bytes of the last image a request holds.
func imageData(req seam.Request) []byte {
	var out []byte
	for _, m := range req.Messages {
		for _, b := range m.Blocks {
			if b.Type == seam.BlockImage {
				out = b.Data
			}
		}
	}
	return out
}

// A message can name only an upload of its own chat, and is written as
// the upload says.
func TestAMessageNamesOnlyAnUploadOfItsChat(t *testing.T) {
	g := newRig(t)
	ears := &sightEars{sent: map[string][]seam.Request{}}
	g.extra = adapters.Set{models.AdapterChatCompletions: ears.build}
	g.addModels(sightModel)
	g.set(tenantsettings.Person, KeyTitleAuto, "false")
	sv := g.serve(nil)
	one, two := sv.newChat(g.ordinary.ID), sv.newChat(g.ordinary.ID)
	picture := uploadTo(t, g, one, "square.png", testPicture(t, color.RGBA{1, 2, 3, 255}))

	body, _ := json.Marshal(SendBody{Blocks: []seam.Block{asked("Look"), uploads.BlockOf(picture)}, Model: sightModel.Name})
	resp := sv.request(g.ordinary.ID, "POST", "chats/"+two+"/turns", string(body), nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a message naming another chat's upload answered %d", resp.StatusCode)
	}

	// Named as a file with another type, it is still sent as the image it is.
	events, _, _ := sendBlocks(t, sv, one, sightModel.Name, asked("Look"), seam.Block{Type: seam.BlockFile, MediaType: "text/plain", Name: "lie.txt", Ref: picture.ID})
	if end := last(t, events); end.Status != StatusDone {
		t.Fatalf("the turn ended as %+v", end)
	}
	seen := ears.of(sightModel.Name)
	if len(seen) != 1 || seam.Images(seen[0]) != 1 {
		t.Errorf("the model that can see was sent %d images", imagesOf(seen))
	}
	var got ChatReply
	sv.call(g.ordinary.ID, "GET", "chats/"+one, "", http.StatusOK, &got)
	if b := got.Messages[0].Blocks[1]; b.Type != seam.BlockImage || b.Ref != picture.ID || b.MediaType != "image/png" || len(b.Data) != 0 {
		t.Errorf("the message keeps the upload as %+v", b)
	}
}
