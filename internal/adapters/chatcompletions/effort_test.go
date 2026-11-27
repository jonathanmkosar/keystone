package chatcompletions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/internal/seam"
)

// effortExchange is one exchange of fixtures/chat-completions/effort.
type effortExchange struct {
	Name                string              `json:"name"`
	About               string              `json:"about"`
	Capabilities        []models.Capability `json:"capabilities"`
	SendReasoningEffort bool                `json:"send_reasoning_effort"`
	Request             seam.Request        `json:"request"`
	Sent                json.RawMessage     `json:"sent"`
}

// TestEffortIsSentAsReasoningEffort replays the effort exchanges (P14-01):
// each effort, with the setting on and off, to an entry with and without
// thinking. Every call is answered by the same short stream.
func TestEffortIsSentAsReasoningEffort(t *testing.T) {
	var file struct {
		TestData  bool             `json:"test_data"`
		About     string           `json:"about"`
		Exchanges []effortExchange `json:"exchanges"`
	}
	readJSON(t, "effort/exchanges.json", &file)
	if !file.TestData || len(file.Exchanges) < 8 {
		t.Fatalf("the effort fixture is not marked as test data, or holds %d exchanges", len(file.Exchanges))
	}
	for _, x := range file.Exchanges {
		t.Run(x.Name, func(t *testing.T) {
			var problems []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if !sameJSON(t, json.RawMessage(body), x.Sent) {
					problems = append(problems, "sent:\n"+string(body)+"\nwant:\n"+compact(x.Sent))
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, chunk("Soup on Monday.")+stopChunk)
			}))
			defer srv.Close()
			m := entry(srv.URL)
			m.Capabilities = x.Capabilities
			opts := quick()
			opts.SendReasoningEffort = x.SendReasoningEffort
			got, err := seam.Call(context.Background(), provider(t, m, "", opts), x.Request)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range problems {
				t.Error(p)
			}
			if seam.Text(got.Message) != "Soup on Monday." {
				t.Errorf("the reply is %+v", got.Message)
			}
		})
	}
}
