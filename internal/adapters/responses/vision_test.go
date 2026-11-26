package responses

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// TestAnImageReachesOnlyAModelThatSees is P9-02's proof at the adapter: a
// request that carries an image is refused before anything is sent when
// the entry does not say it can see, and sent as an input_image part when
// it does. The stand-in answers every call with a refusal, so the request
// the adapter built is what is checked.
func TestAnImageReachesOnlyAModelThatSees(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"the stand-in answers every call so","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()
	picture := []byte("invented image bytes")
	image := seam.Block{Type: seam.BlockImage, MediaType: "image/png", Data: picture}
	asks := map[string]seam.Request{
		"in the message": {Messages: []seam.Message{{Role: seam.RoleUser, Blocks: []seam.Block{
			{Type: seam.BlockText, Text: "What is in this picture?"}, image,
		}}}},
		"in a tool's result": {Messages: []seam.Message{
			{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockText, Text: "Draw a square."}}},
			{Role: seam.RoleAssistant, Blocks: []seam.Block{{Type: seam.BlockToolCall, ID: "call_invented_1", Name: "draw", Input: json.RawMessage(`{}`)}}},
			{Role: seam.RoleUser, Blocks: []seam.Block{{Type: seam.BlockToolResult, ID: "call_invented_1", Content: []seam.Block{{Type: seam.BlockText, Text: "drawn"}, image}}}},
		}},
	}
	ctx := context.Background()

	for name, req := range asks {
		p, err := New(entry(srv.URL+"/v1", models.CapTools, models.CapFiles), testKey, testOptions)
		if err != nil {
			t.Fatal(err)
		}
		err = p.Stream(ctx, req, func(seam.Event) error { return nil })
		var refused *seam.Error
		if !errors.As(err, &refused) || refused.Kind != seam.ErrInvalid || !strings.Contains(refused.Message, "never sent to a model without vision") {
			t.Errorf("%s: a model without vision was not refused the image: %v", name, err)
		}
	}
	if len(bodies) != 0 {
		t.Fatalf("the stand-in was sent %d requests for a model that cannot see", len(bodies))
	}

	p, err := New(entry(srv.URL+"/v1", models.CapVision), testKey, testOptions)
	if err != nil {
		t.Fatal(err)
	}
	p.Stream(ctx, asks["in the message"], func(seam.Event) error { return nil })
	if len(bodies) != 1 {
		t.Fatalf("the stand-in was sent %d requests for a model that sees, want 1", len(bodies))
	}
	var sent struct {
		Input []struct {
			Content []struct {
				Type     string `json:"type"`
				ImageURL string `json:"image_url"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(bodies[0], &sent); err != nil || len(sent.Input) != 1 || len(sent.Input[0].Content) != 2 {
		t.Fatalf("the request reads %s: %v", bodies[0], err)
	}
	got := sent.Input[0].Content[1]
	if got.Type != "input_image" || got.ImageURL != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(picture) {
		t.Errorf("the image was sent as %s", bodies[0])
	}
}
